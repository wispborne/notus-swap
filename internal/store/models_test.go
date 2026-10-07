package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestModelStats(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now()
	add := func(model, state string, gen float64, ct, pt int64, cached *int64) {
		id, err := st.Begin(ctx, Begin{StartedAt: now, Method: "POST", Path: "/v1/x", Model: model})
		if err != nil {
			t.Fatal(err)
		}
		f := Finish{FinishedAt: now, State: state, CompletionTokens: &ct, PromptTokens: &pt, CachedTokens: cached}
		if gen > 0 {
			f.PredictedPerSecond = &gen
		}
		if err := st.Finish(ctx, id, f); err != nil {
			t.Fatal(err)
		}
	}
	eighty := int64(80)
	add("a", StateDone, 10, 5, 100, &eighty) // 80% cached: left out of the speeds
	add("a", StateDone, 20, 7, 50, nil) // no cache count: left out of the cache rate
	add("a", StateFailed, 0, 0, 0, nil)
	ms := int64(3000)
	st.AddModelEvent(ctx, ModelEvent{At: now.UnixMilli(), Model: "a", From: "starting", To: "ready", LoadMs: &ms})
	// Loaded but never asked anything.
	st.AddModelEvent(ctx, ModelEvent{At: now.UnixMilli(), Model: "b", From: "starting", To: "ready", LoadMs: &ms})

	stats, err := st.ModelStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(stats), stats)
	}
	a, b := stats[0], stats[1]
	if a.Model != "a" || a.Requests != 3 || a.Failed != 1 || a.CompletionTokens != 12 || a.GenPerSecond == nil || *a.GenPerSecond != 20 || a.Loads != 1 {
		t.Errorf("model a: %+v", a)
	}
	if a.CachedTokens != 80 || a.CacheRate == nil || *a.CacheRate != 0.8 {
		t.Errorf("model a cache: %d tokens, rate %v", a.CachedTokens, a.CacheRate)
	}
	if b.Model != "b" || b.Requests != 0 || b.Loads != 1 || b.LoadMs == nil || *b.LoadMs != 3000 {
		t.Errorf("model b: %+v", b)
	}

	evs, err := st.ModelEventsFor(ctx, "a", 0)
	if err != nil || len(evs) != 1 {
		t.Errorf("events for a: %v, err %v", evs, err)
	}
}

func TestEarliest(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if e, err := st.Earliest(ctx); err != nil || !e.IsZero() {
		t.Fatalf("empty store: %v, %v", e, err)
	}
	old := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	if err := st.AddModelEvent(ctx, ModelEvent{At: old.UnixMilli(), Model: "a", From: "stopped", To: "starting"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Begin(ctx, Begin{StartedAt: time.Now(), Method: "POST", Path: "/v1/x"}); err != nil {
		t.Fatal(err)
	}
	if e, err := st.Earliest(ctx); err != nil || !e.Equal(old) {
		t.Errorf("earliest %v, want %v (err %v)", e, old, err)
	}
}

func TestModelBuilds(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	start := time.Now()
	cached := int64(100)
	add := func(at time.Duration, build, state string, ct int64, predictedMs float64, body string) {
		id, err := st.Begin(ctx, Begin{StartedAt: start.Add(at), Method: "POST", Path: "/v1/x", Model: "a"})
		if err != nil {
			t.Fatal(err)
		}
		pt, promptMs := int64(1100), 500.0
		f := Finish{FinishedAt: start.Add(at), State: state, Build: build, CompletionTokens: &ct, PredictedMs: &predictedMs,
			PromptTokens: &pt, CachedTokens: &cached, PromptMs: &promptMs, ResponseBody: []byte(body)}
		if err := st.Finish(ctx, id, f); err != nil {
			t.Fatal(err)
		}
	}
	add(0, "", StateDone, 10, 1000, "old b1") // stored before builds were read
	add(time.Second, "b1-x", StateDone, 30, 1000, "")
	add(2*time.Second, "b2-y", StateDone, 100, 1000, "")
	add(3*time.Second, "b2-y", StateDone, 20, 1000, "")
	add(4*time.Second, "b2-y", StateFailed, 0, 0, "") // failed requests are left out
	// Over half cached: counted as a request, but left out of the speeds.
	cached = 1000
	add(5*time.Second, "b2-y", StateDone, 500, 1000, "")

	n, err := st.FillBuilds(ctx, func(b []byte) string {
		if string(b) == "old b1" {
			return "b1-x"
		}
		return ""
	})
	if err != nil || n != 1 {
		t.Fatalf("filled %d, err %v", n, err)
	}

	builds, err := st.ModelBuilds(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 2 {
		t.Fatalf("got %d builds, want 2: %+v", len(builds), builds)
	}
	b2, b1 := builds[0], builds[1]
	// 120 tokens over 2 s; prompt speed counts 1000 uncached tokens per 0.5 s.
	if b2.Build != "b2-y" || b2.Requests != 3 || b2.GenPerSecond == nil || *b2.GenPerSecond != 60 ||
		b2.PromptPerSecond == nil || *b2.PromptPerSecond != 2000 {
		t.Errorf("b2: %+v", b2)
	}
	if b1.Build != "b1-x" || b1.Requests != 2 || *b1.GenPerSecond != 20 || b1.FirstUsed != start.UnixMilli() {
		t.Errorf("b1: %+v", b1)
	}
}

func TestModelBuildsByCommand(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	start := time.Now()
	for i, cmd := range []string{"c1", "c1", "c2", ""} {
		id, err := st.Begin(ctx, Begin{StartedAt: start.Add(time.Duration(i) * time.Second), Method: "POST", Path: "/v1/x", Model: "a"})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Finish(ctx, id, Finish{FinishedAt: start, State: StateDone, Build: "b1-x", CmdHash: cmd}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveModelCmd(ctx, "c1", []string{"llama-server", "-c", "4096"}, start); err != nil {
		t.Fatal(err)
	}
	// Saving the same hash again keeps the first.
	if err := st.SaveModelCmd(ctx, "c1", []string{"other"}, start); err != nil {
		t.Fatal(err)
	}

	builds, err := st.ModelBuilds(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range builds {
		got = append(got, fmt.Sprintf("%s/%s:%d", b.Build, b.CmdHash, b.Requests))
	}
	if fmt.Sprint(got) != "[b1-x/:1 b1-x/c2:1 b1-x/c1:2]" {
		t.Errorf("groups %v", got)
	}
	cmds, err := st.ModelCmds(ctx, []string{"c1", "c2"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(cmds) != "map[c1:[llama-server -c 4096]]" {
		t.Errorf("cmds %v", cmds)
	}
}
