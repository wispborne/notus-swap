package capture

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/store"
)

func TestRemeasure(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	start := time.UnixMilli(1_790_000_000_000)
	pt, ct := int64(4000), int64(5401)
	add := func(path, body string) int64 {
		t.Helper()
		id, err := st.Begin(ctx, store.Begin{StartedAt: start, Method: "POST", Path: path, Model: "m", Streaming: true})
		if err != nil {
			t.Fatal(err)
		}
		f := store.Finish{State: store.StateDone, StatusCode: 200, PromptTokens: &pt, CompletionTokens: &ct,
			FirstByteAt: start.Add(4 * time.Second), FirstTokenAt: start.Add(57 * time.Second),
			FinishedAt: start.Add(58 * time.Second), ResponseBody: []byte(body)}
		measureSpeeds(&f, start, time.Time{}, 0, false)
		if err := st.Finish(ctx, id, f); err != nil {
			t.Fatal(err)
		}
		return id
	}
	vllm := add("/v1/chat/completions", `data: {"choices":[{"delta":{"role":"assistant","content":""}}]}`+"\n\n"+
		`data: {"choices":[{"delta":{"reasoning":"hm"}}]}`+"\n\n"+`data: [DONE]`)
	llama := add("/v1/chat/completions", `data: {"choices":[{"delta":{"content":"hi"}}]}`+"\n\n"+
		`data: {"choices":[],"timings":{"prompt_ms":4000,"predicted_ms":1000}}`+"\n\n"+`data: [DONE]`)
	responses := add("/v1/responses", `data: {"type":"response.created"}`)

	n, err := Remeasure(ctx, st)
	if err != nil || n != 1 {
		t.Fatalf("changed %d, err %v; want 1", n, err)
	}
	r, _ := st.Get(ctx, vllm)
	if !r.FirstTokenAt.Equal(start.Add(4*time.Second)) || r.PredictedPerSecond == nil || *r.PredictedPerSecond != 100 {
		t.Errorf("vLLM request: first token %v, speed %v", r.FirstTokenAt.Sub(start), r.PredictedPerSecond)
	}
	for _, id := range []int64{llama, responses} {
		r, _ := st.Get(ctx, id)
		if !r.FirstTokenAt.Equal(start.Add(57 * time.Second)) {
			t.Errorf("request %d changed: first token %v", id, r.FirstTokenAt.Sub(start))
		}
	}
	if n, _ := Remeasure(ctx, st); n != 0 {
		t.Errorf("second run changed %d", n)
	}
}
