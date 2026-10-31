package capture

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/llamaswap"
	"github.com/wispborne/notus-swap/internal/store"
)

func f64(v float64) *float64 { return &v }

func TestStrataStats(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	start := time.Unix(1793397946, 0)
	end := start.Add(28 * time.Second)
	// The client named the model by an alias.
	id, err := st.Begin(ctx, store.Begin{StartedAt: start, Method: "POST", Path: "/v1/chat/completions", Model: "code"})
	if err != nil {
		t.Fatal(err)
	}
	// An issue found in the output, which must outlast the server's.
	if err := st.Finish(ctx, id, store.Finish{FinishedAt: end, State: store.StateDone, StatusCode: 200, ResponseBody: []byte(`{}`),
		Issues: []store.Issue{{Kind: IssueContextNearFull, Level: levelInfo, Detail: json.RawMessage(`{}`)}}}); err != nil {
		t.Fatal(err)
	}

	// Trimmed from Strata 0.1.54. Another request of the same size finished
	// earlier, and one with another output size at the same time.
	rec := llamaswap.StrataRequest{ID: "bed3d1096479", Time: 1793397946.69, PromptTokens: 99960, OutputTokens: 1976,
		FirstTokenS: f64(1.454), HitRate: f64(0.833), PCIeShare: f64(0.117), Recoveries: 1, RepeatCoverage: 0.31, FileMB: f64(0)}
	older := rec
	older.ID, older.Time = "aaaaaaaaaaaa", 1793397800
	other := rec
	other.ID, other.OutputTokens = "bbbbbbbbbbbb", 12
	answers := [][]llamaswap.StrataRequest{nil, {rec, other, older}} // not recorded yet on the first try
	calls := 0
	h := NewHub()
	events, _, cancel := h.Subscribe()
	defer cancel()
	s := &StrataStats{Store: st, Hub: h, Ready: func(m string) bool { return m == "qwen" }, Wait: time.Millisecond,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ID:  func(name string) string { return map[string]string{"code": "qwen"}[name] },
		Fetch: func(ctx context.Context, model string) (llamaswap.Strata, error) {
			if model != "qwen" {
				t.Errorf("read the /metrics of %q, not the model's ID", model)
			}
			calls++
			return llamaswap.Strata{Requests: answers[min(calls-1, len(answers)-1)]}, nil
		}}
	s.Finished(ctx, id, "code", start, end, 99960, 1976)

	r, _ := st.Get(ctx, id)
	var got ServerStats
	if r.ServerStats == nil || json.Unmarshal([]byte(*r.ServerStats), &got) != nil {
		t.Fatalf("server stats %v", r.ServerStats)
	}
	if *got.ExpertHitRate != 0.833 || *got.PCIeShare != 0.117 || *got.FirstTokenMs != 1454 || got.ThinkingRestarts != 1 || *got.DiskMB != 0 {
		t.Errorf("stats %+v", got)
	}
	issues, _ := st.Issues(ctx, id)
	if len(issues) != 2 {
		t.Fatalf("issues %+v", issues)
	}
	if e := <-events; e.Type != "settled" || e.ID != id {
		t.Errorf("event %+v", e)
	}

	// Checking the request again keeps the server's issue.
	if _, err := Recheck(ctx, st, func(string) *store.ModelProps { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	issues, _ = st.Issues(ctx, id)
	if len(issues) != 1 || issues[0].Kind != IssueThinkingLoop {
		t.Errorf("after a recheck: %+v", issues)
	}

	// A record is matched once.
	if _, ok := s.match([]llamaswap.StrataRequest{rec}, start, end, 99960, 1976); ok {
		t.Error("matched a record twice")
	}
}

func TestStrataMatch(t *testing.T) {
	start := time.Unix(1000, 0)
	end := start.Add(10 * time.Second)
	a := llamaswap.StrataRequest{ID: "a", Time: 1000.5, PromptTokens: 500, OutputTokens: 20}
	b := a
	b.ID = "b"
	late := a
	late.ID, late.Time = "late", 1012
	s := &StrataStats{}
	if _, ok := s.match([]llamaswap.StrataRequest{a, b}, start, end, 500, 20); ok {
		t.Error("two records fit, so neither should be picked")
	}
	if _, ok := s.match([]llamaswap.StrataRequest{late}, start, end, 500, 20); ok {
		t.Error("a record that started after the request ended was picked")
	}
	if r, ok := s.match([]llamaswap.StrataRequest{a, late}, start, end, 500, 20); !ok || r.ID != "a" {
		t.Errorf("got %+v %v", r, ok)
	}
}

func TestServerIssues(t *testing.T) {
	for _, tc := range []struct {
		stats *ServerStats
		want  int
	}{
		{nil, 0},
		{&ServerStats{ThinkingRepeat: 0.1}, 0},
		{&ServerStats{ThinkingRepeat: 0.25}, 1}, // Strata's own cutoff, as when it ends the answer instead
		{&ServerStats{ThinkingRestarts: 2, ThinkingRepeat: 0.05}, 1},
	} {
		if got := serverIssues(tc.stats); len(got) != tc.want {
			t.Errorf("%+v: %+v", tc.stats, got)
		}
	}
}
