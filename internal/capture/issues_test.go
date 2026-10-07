package capture_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/capture"
	"github.com/wispborne/notus-swap/internal/store"
)

// reply builds a non-streaming chat response.
func reply(content, reasoning, finish string, prompt, output int) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"content": content, "reasoning_content": reasoning},
			"finish_reason": finish,
		}},
		"usage": map[string]any{"prompt_tokens": prompt, "completion_tokens": output},
	})
	return string(b)
}

// respReply builds a non-streaming Responses API answer.
func respReply(text, reasoning, status string, prompt, output int) string {
	var items []any
	if reasoning != "" {
		items = append(items, map[string]any{"type": "reasoning", "content": []any{map[string]any{"type": "reasoning_text", "text": reasoning}}})
	}
	if text != "" {
		items = append(items, map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}})
	}
	body := map[string]any{"object": "response", "status": status, "output": items,
		"usage": map[string]any{"input_tokens": prompt, "output_tokens": output}}
	if status == "incomplete" {
		body["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	b, _ := json.Marshal(body)
	return string(b)
}

func TestCheck(t *testing.T) {
	props := &store.ModelProps{NCtx: 8192, NPredict: -1, Slots: 1}
	withLimit := &store.ModelProps{NCtx: 8192, NPredict: 1000, Slots: 1}
	for _, tc := range []struct {
		name     string
		path     string
		state    string
		status   int
		req      string
		resp     string
		props    *store.ModelProps
		want     string // "kind/level", or "" for no issue
		wantCtx  int64
		wantMsg  bool
		truncate bool
	}{
		{name: "normal answer", req: `{}`, resp: reply("Hi", "", "stop", 100, 5), props: props},
		{name: "own max_tokens", req: `{"max_tokens":500}`, resp: reply("It was a", "", "length", 100, 500), props: props, want: "token_limit/warning"},
		{name: "max_completion_tokens", req: `{"max_completion_tokens":300}`, resp: reply("It was a", "", "length", 100, 300), props: props, want: "token_limit/warning"},
		{name: "small limit on purpose", req: `{"max_tokens":50}`, resp: reply("Weather in", "", "length", 100, 50), props: props},
		{name: "warm-up request", req: `{"max_tokens":1}`, resp: reply("", "Hmm", "length", 100, 1), props: props},
		{name: "context full", req: `{}`, resp: reply("It was a", "", "length", 7000, 1190), props: props, want: "context_full/warning"},
		{name: "limit bigger than room left", req: `{"max_tokens":4000}`, resp: reply("It was a", "", "length", 7000, 1190), props: props, want: "context_full/warning"},
		{name: "server -n", req: `{}`, resp: reply("It was a", "", "length", 100, 1000), props: withLimit, want: "server_limit/warning"},
		{name: "cause unknown", req: `{}`, resp: reply("It was a", "", "length", 100, 1000), want: "cut_off/warning"},
		{name: "thinking only", req: `{"max_tokens":2000}`, resp: reply("", "Let me think", "length", 100, 2000), props: props, want: "thinking_budget/warning"},
		{name: "thinking only, small limit", req: `{"max_tokens":40}`, resp: reply("  ", "Let me think", "length", 100, 40), props: props, want: "thinking_budget/warning"},
		{name: "near full", req: `{}`, resp: reply("Hi", "", "stop", 7800, 5), props: props, want: "context_near_full/info"},
		{name: "near full, asked for more than fits", req: `{"max_tokens":2048}`, resp: reply("Hi", "", "stop", 6500, 5), props: props, want: "context_near_full/info"},
		{name: "high ceiling is not near full", req: `{"max_tokens":8192}`, resp: reply("Hi", "", "stop", 6500, 5), props: props},
		{name: "near full needs the context size", req: `{}`, resp: reply("Hi", "", "stop", 7800, 5)},
		{name: "context error", status: 400, req: `{}`,
			resp:  `{"error":{"code":400,"message":"the request exceeds the available context size, try increasing it","type":"exceed_context_size_error","n_prompt_tokens":9000,"n_ctx":8192}}`,
			props: props, want: "context_error/warning", wantCtx: 8192, wantMsg: true},
		{name: "other 400", status: 400, req: `{}`, resp: `{"error":{"message":"bad json"}}`, props: props},
		{name: "client hung up", state: store.StateClientGone, req: `{}`, resp: reply("It was a", "", "length", 7000, 1190), props: props},
		{name: "server error", status: 500, state: store.StateFailed, req: `{}`, resp: reply("", "", "length", 7000, 1190), props: props},
		{name: "cut short when stored", truncate: true, req: `{}`, resp: reply("It was a", "", "length", 7000, 1190), props: props},
		{name: "embeddings", path: "/v1/embeddings", req: `{}`, resp: `{"data":[]}`, props: props},
		{name: "fill in the middle", path: "/v1/completions", req: `{"prompt":"a","suffix":"b","max_tokens":500}`,
			resp: `{"choices":[{"text":"x","finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":500}}`, props: props},
		{name: "llama.cpp infill fields", path: "/v1/completions", req: `{"input_prefix":"a","n_predict":500}`,
			resp: reply("x", "", "length", 10, 500), props: props},
		{name: "n_predict", path: "/upstream/m/v1/completions", req: `{"prompt":"a","n_predict":500}`,
			resp: `{"choices":[{"text":"x","finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":500}}`, props: props, want: "token_limit/warning"},
		// llama-server marks every Responses answer "completed", even one that
		// stopped at a limit, so these are found from the token counts.
		{name: "responses: normal answer", path: "/v1/responses", req: `{}`, resp: respReply("Hi", "", "completed", 100, 5), props: props},
		{name: "responses: own max_output_tokens", path: "/v1/responses", req: `{"max_output_tokens":500}`,
			resp: respReply("It was a", "", "completed", 100, 500), props: props, want: "token_limit/warning"},
		{name: "responses: context full", path: "/v1/responses", req: `{}`, resp: respReply("It was a", "", "completed", 7000, 1190), props: props, want: "context_full/warning"},
		{name: "responses: server -n", path: "/v1/responses", req: `{}`, resp: respReply("It was a", "", "completed", 100, 1000), props: withLimit, want: "server_limit/warning"},
		{name: "responses: thinking only", path: "/v1/responses", req: `{"max_output_tokens":2000}`,
			resp: respReply("", "Let me think", "completed", 100, 2000), props: props, want: "thinking_budget/warning"},
		{name: "responses: near full", path: "/v1/responses", req: `{}`, resp: respReply("Hi", "", "completed", 7800, 5), props: props, want: "context_near_full/info"},
		{name: "responses: marked incomplete", path: "/v1/responses", req: `{}`, resp: respReply("It was a", "", "incomplete", 100, 1000), want: "cut_off/warning"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.path == "" {
				tc.path = "/v1/chat/completions"
			}
			if tc.state == "" {
				tc.state = store.StateDone
			}
			if tc.status == 0 {
				tc.status = 200
			}
			got := capture.Check(capture.CheckInput{Path: tc.path, State: tc.state, StatusCode: tc.status, Truncated: tc.truncate,
				Request: []byte(tc.req), Response: capture.Parse([]byte(tc.resp), "application/json"), Props: tc.props})
			var kinds []string
			for _, is := range got {
				kinds = append(kinds, is.Kind+"/"+is.Level)
			}
			if strings.Join(kinds, ",") != tc.want {
				t.Fatalf("got %v, want %q", kinds, tc.want)
			}
			if len(got) == 0 {
				return
			}
			var d capture.IssueDetail
			json.Unmarshal(got[0].Detail, &d)
			if tc.wantCtx != 0 && d.NCtx != tc.wantCtx {
				t.Errorf("context size %d, want %d", d.NCtx, tc.wantCtx)
			}
			if tc.wantMsg && d.Message == "" {
				t.Error("no message")
			}
		})
	}
}

func TestThinkingBudgetNamesItsCause(t *testing.T) {
	got := capture.Check(capture.CheckInput{Path: "/v1/chat/completions", State: store.StateDone, StatusCode: 200,
		Request:  []byte(`{}`),
		Response: capture.Parse([]byte(reply("", "Hmm", "length", 7000, 1190)), "application/json"),
		Props:    &store.ModelProps{NCtx: 8192}})
	var d capture.IssueDetail
	if len(got) == 1 {
		json.Unmarshal(got[0].Detail, &d)
	}
	if d.Cause != capture.IssueContextFull || d.Prompt != 7000 || d.Output != 1190 {
		t.Errorf("got %+v", got)
	}
}

// A streamed answer cut off by its limit is stored with its issue, finish
// reason and context size, and the list can filter by it and mute it.
func TestIssuesStoredAndFiltered(t *testing.T) {
	srv, st := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"It was a"}}]}`)
		sse(w, `{"choices":[{"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":200}}`)
	}))
	ctx := context.Background()
	st.SaveModelProps(ctx, []string{"qwen", "q"}, store.ModelProps{NCtx: 4096}, time.Now())
	// The test server's hub has no props lookup, so the context size is
	// filled in by checking again, as happens when a model loads.
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"q","stream":true,"max_tokens":200}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	rec := waitFinished(t, st, 1)
	if rec.FinishReason == nil || *rec.FinishReason != "length" {
		t.Errorf("finish reason %v", rec.FinishReason)
	}
	props := func(m string) *store.ModelProps { p, _ := st.GetModelProps(ctx, m); return p }
	if n, err := capture.Recheck(ctx, st, props, []string{"qwen", "q"}); err != nil || n != 1 {
		t.Fatalf("recheck: %d %v", n, err)
	}
	rec, _ = st.Get(ctx, 1)
	if rec.NCtx == nil || *rec.NCtx != 4096 {
		t.Errorf("context size %v", rec.NCtx)
	}
	// Known now, so a second pass for the model finds nothing to do.
	if n, _ := capture.Recheck(ctx, st, props, []string{"q"}); n != 0 {
		t.Errorf("checked %d again", n)
	}

	list := func(o store.ListOptions) string {
		recs, err := st.List(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range recs {
			out = append(out, fmt.Sprint(r.ID, r.Issues))
		}
		return strings.Join(out, " ")
	}
	if got := list(store.ListOptions{}); got != "1 [token_limit]" {
		t.Errorf("list: %s", got)
	}
	if got := list(store.ListOptions{Issue: "any"}); got != "1 [token_limit]" {
		t.Errorf("any issue: %s", got)
	}
	if got := list(store.ListOptions{Issue: "context_full"}); got != "" {
		t.Errorf("other kind: %s", got)
	}
	muted := []store.IssueMute{{Kind: "token_limit", Model: "q"}}
	if got := list(store.ListOptions{Mutes: muted}); got != "1 []" {
		t.Errorf("muted: %s", got)
	}
	if got := list(store.ListOptions{Issue: "any", Mutes: muted}); got != "" {
		t.Errorf("muted, any issue: %s", got)
	}
	if got := list(store.ListOptions{Issue: "any", Mutes: []store.IssueMute{{Kind: "token_limit", Model: "other"}}}); got != "1 [token_limit]" {
		t.Errorf("muted for another model: %s", got)
	}
}

// Changing the checks version checks every stored request again, once.
func TestRecheckIfChanged(t *testing.T) {
	srv, st := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, reply("It was a", "", "length", 10, 1000))
	}))
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	waitFinished(t, st, 1)
	ctx := context.Background()
	none := func(string) *store.ModelProps { return nil }
	if n, err := capture.RecheckIfChanged(ctx, st, none); err != nil || n != 1 {
		t.Fatalf("first run: %d %v", n, err)
	}
	if n, _ := capture.RecheckIfChanged(ctx, st, none); n != 0 {
		t.Errorf("second run checked %d", n)
	}
	if is, _ := st.Issues(ctx, 1); len(is) != 1 || is[0].Kind != capture.IssueCutOff {
		t.Errorf("issues %+v", is)
	}
}
