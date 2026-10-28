package store

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
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
	add := func(model, build, state string, gen float64, ct, pt int64, cached *int64) {
		id, err := st.Begin(ctx, Begin{StartedAt: now, Method: "POST", Path: "/v1/x", Model: model})
		if err != nil {
			t.Fatal(err)
		}
		f := Finish{FinishedAt: now, State: state, Build: build, CompletionTokens: &ct, PromptTokens: &pt, CachedTokens: cached}
		if gen > 0 {
			f.PredictedPerSecond = &gen
		}
		if err := st.Finish(ctx, id, f); err != nil {
			t.Fatal(err)
		}
	}
	eighty := int64(80)
	add("a", "b0", StateDone, 99, 5, 100, nil) // an older build: not in the current speeds
	add("a", "b1", StateDone, 10, 5, 100, &eighty)
	add("a", "b1", StateDone, 20, 7, 50, nil) // no cache count: left out of the cache rate
	add("a", "b1", StateFailed, 0, 0, 0, nil)
	add("a-alias", "b1", StateDone, 30, 6, 100, &eighty) // an alias of a: counts toward a
	ms := int64(3000)
	st.AddModelEvent(ctx, ModelEvent{At: now.UnixMilli(), Model: "a", From: "starting", To: "ready", LoadMs: &ms})
	// Loaded but never asked anything.
	st.AddModelEvent(ctx, ModelEvent{At: now.UnixMilli(), Model: "b", From: "starting", To: "ready", LoadMs: &ms})

	f := DefaultSpeedFilter()
	f.MinOutput = 1
	id := func(name string) string { return strings.TrimSuffix(name, "-alias") }
	stats, err := st.ModelStats(ctx, f, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(stats), stats)
	}
	a, b := stats[0], stats[1]
	if a.Model != "a" || a.Requests != 5 || a.Failed != 1 || a.CompletionTokens != 23 || a.Loads != 1 {
		t.Errorf("model a: %+v", a)
	}
	if a.CachedTokens != 160 || a.CacheRate == nil || *a.CacheRate != 0.8 {
		t.Errorf("model a cache: %d tokens, rate %v", a.CachedTokens, a.CacheRate)
	}
	// The median of 10, 20 and 30 (sent to the alias), on build b1.
	if c := a.Current; c == nil || c.Build != "b1" || c.GenCounted != 3 || c.GenPerSecond == nil || *c.GenPerSecond != 20 {
		t.Errorf("model a current speeds: %+v", a.Current)
	}
	if b.Model != "b" || b.Current != nil || b.Requests != 0 || b.Loads != 1 || b.LoadMs == nil || *b.LoadMs != 3000 {
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
	n := 0
	add := func(build, state string, ct int64, gen float64, body string) {
		n++
		id, err := st.Begin(ctx, Begin{StartedAt: start.Add(time.Duration(n) * time.Second), Method: "POST", Path: "/v1/x", Model: "a"})
		if err != nil {
			t.Fatal(err)
		}
		pt, cached, promptPS := int64(3100), int64(100), 1000.0
		f := Finish{FinishedAt: start, State: state, Build: build, CompletionTokens: &ct, PredictedPerSecond: &gen,
			PromptTokens: &pt, CachedTokens: &cached, PromptPerSecond: &promptPS, ResponseBody: []byte(body)}
		if err := st.Finish(ctx, id, f); err != nil {
			t.Fatal(err)
		}
	}
	add("", StateDone, 200, 10, "old b1") // stored before builds were read
	add("b1-x", StateDone, 200, 30, "")
	add("b2-y", StateDone, 200, 50, "")
	add("b2-y", StateDone, 200, 70, "")
	add("b2-y", StateDone, 20, 500, "") // too short to count
	add("b2-y", StateFailed, 0, 0, "")  // failed requests are left out

	if n, err := st.FillBuilds(ctx, func(b []byte) string {
		if string(b) == "old b1" {
			return "b1-x"
		}
		return ""
	}); err != nil || n != 1 {
		t.Fatalf("filled %d, err %v", n, err)
	}

	builds, err := st.ModelBuilds(ctx, []string{"a"}, DefaultSpeedFilter())
	if err != nil {
		t.Fatal(err)
	}
	if len(builds.Rows) != 2 || builds.Requests != 5 || builds.GenCounted != 4 || builds.PromptCounted != 5 {
		t.Fatalf("got %+v", builds)
	}
	b2, b1 := builds.Rows[0], builds.Rows[1]
	if b2.Build != "b2-y" || b2.Requests != 3 || !b2.Current || b2.GenCounted != 2 || *b2.GenPerSecond != 60 ||
		b2.PromptCounted != 3 || *b2.PromptPerSecond != 1000 {
		t.Errorf("b2: %+v", b2)
	}
	if b1.Build != "b1-x" || b1.Requests != 2 || b1.Current || *b1.GenPerSecond != 20 || b1.FirstUsed != start.Add(time.Second).UnixMilli() {
		t.Errorf("b1: %+v", b1)
	}
	// Too few requests of each kind to compare.
	if b2.GenChange != nil {
		t.Errorf("b2 change %v", *b2.GenChange)
	}
}

// Builds are compared on the kinds of requests both have: here b2 is 10%
// faster for each client, although most of b1's requests came from the
// faster client.
func TestModelBuildsCompareKinds(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	start := time.Now()
	n := 0
	add := func(build, agent string, gen float64, count int) {
		for range count {
			n++
			id, err := st.Begin(ctx, Begin{StartedAt: start.Add(time.Duration(n) * time.Second), Method: "POST", Path: "/v1/x", Model: "a", UserAgent: agent})
			if err != nil {
				t.Fatal(err)
			}
			ct, pt := int64(500), int64(1000)
			if err := st.Finish(ctx, id, Finish{FinishedAt: start, State: StateDone, Build: build, CompletionTokens: &ct,
				PromptTokens: &pt, PredictedPerSecond: &gen}); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("b1", "fast/1", 100, 40)
	add("b1", "slow/1", 50, 10)
	add("b2", "fast/1", 110, 10)
	add("b2", "slow/1", 55, 40)
	add("b2", "other/1", 999, 30) // a kind b1 doesn't have

	builds, err := st.ModelBuilds(ctx, []string{"a"}, DefaultSpeedFilter())
	if err != nil {
		t.Fatal(err)
	}
	b2 := builds.Rows[0]
	if b2.GenChange == nil || math.Abs(*b2.GenChange-0.1) > 1e-9 || b2.GenCompared != 100 {
		t.Errorf("b2 change %v over %d requests", b2.GenChange, b2.GenCompared)
	}
	if fmt.Sprint(builds.Clients) != "[{fast 50} {slow 50} {other 30}]" {
		t.Errorf("clients %v", builds.Clients)
	}
	// Leaving out the other clients leaves one kind.
	f := DefaultSpeedFilter()
	f.ExcludeClients = []string{"fast", "other"}
	builds, err = st.ModelBuilds(ctx, []string{"a"}, f)
	if err != nil {
		t.Fatal(err)
	}
	if b := builds.Rows[0]; *b.GenPerSecond != 55 || b.GenCompared != 50 {
		t.Errorf("slow only: %+v", b)
	}
}

func TestSpeedFilterRules(t *testing.T) {
	gen, prompt := 50.0, 900.0
	base := speedRow{prompt: 30_000, cached: 20_000, output: 500, genPS: &gen, promptPS: &prompt, workMs: 10_000, client: "opencode"}
	for _, c := range []struct {
		name        string
		change      func(r *speedRow, f *SpeedFilter)
		gen, prompt bool
	}{
		{"counted", func(*speedRow, *SpeedFilter) {}, true, true},
		{"short answer", func(r *speedRow, _ *SpeedFilter) { r.output = 100 }, false, true},
		{"few new tokens", func(r *speedRow, _ *SpeedFilter) { r.cached = 29_000 }, true, false},
		{"small overlap", func(r *speedRow, _ *SpeedFilter) { r.overlapMs = 400 }, true, true},
		{"shared the server", func(r *speedRow, _ *SpeedFilter) { r.overlapMs = 2000 }, false, false},
		{"shared, alone off", func(r *speedRow, f *SpeedFilter) { r.overlapMs = 2000; f.Alone = false }, true, true},
		{"overlap unknown", func(r *speedRow, _ *SpeedFilter) { r.overlapMs = -1 }, true, true},
		{"images", func(r *speedRow, _ *SpeedFilter) { r.images = true }, true, false},
		{"other client", func(_ *speedRow, f *SpeedFilter) { f.ExcludeClients = []string{"opencode"} }, false, false},
		{"under min context", func(_ *speedRow, f *SpeedFilter) { f.MinContext = 40_000 }, false, false},
		{"over max context", func(_ *speedRow, f *SpeedFilter) { f.MaxContext = 20_000 }, false, false},
		{"structured left out", func(r *speedRow, f *SpeedFilter) { r.structured = true; f.Structured = "exclude" }, false, false},
		{"only structured", func(_ *speedRow, f *SpeedFilter) { f.Structured = "only" }, false, false},
	} {
		r, f := base, DefaultSpeedFilter()
		c.change(&r, &f)
		if g, p := f.countsGen(r), f.countsPrompt(r); g != c.gen || p != c.prompt {
			t.Errorf("%s: counts gen %v prompt %v, want %v %v", c.name, g, p, c.gen, c.prompt)
		}
	}
	if (SpeedFilter{MinContext: 10, MaxContext: 5}).Valid() || (SpeedFilter{Structured: "maybe"}).Valid() || !DefaultSpeedFilter().Valid() {
		t.Error("Valid")
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

	builds, err := st.ModelBuilds(ctx, []string{"a"}, DefaultSpeedFilter())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range builds.Rows {
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
