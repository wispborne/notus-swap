package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneBodiesKeepsMetadata(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now()
	add := func(age time.Duration, size int) int64 {
		id, err := st.Begin(ctx, Begin{StartedAt: now.Add(-age), Method: "POST", Path: "/v1/x", RequestBody: make([]byte, size)})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Finish(ctx, id, Finish{FinishedAt: now, State: StateDone, ResponseBody: make([]byte, size)}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	old := add(100*24*time.Hour, 10)
	a := add(3*time.Hour, 100) // 200 bytes of bodies
	b := add(2*time.Hour, 100)
	c := add(1*time.Hour, 100)

	// Past the 90-day cutoff: only the old one goes.
	n, err := st.PruneBodies(ctx, now.Add(-90*24*time.Hour), 1<<30)
	if err != nil || n != 1 {
		t.Fatalf("age prune deleted %d, err %v", n, err)
	}
	// 600 bytes left, cap 350: the oldest two go, leaving 200.
	n, err = st.PruneBodies(ctx, now.Add(-90*24*time.Hour), 350)
	if err != nil || n != 2 {
		t.Fatalf("size prune deleted %d, err %v", n, err)
	}

	for id, want := range map[int64]bool{old: false, a: false, b: false, c: true} {
		r, err := st.Get(ctx, id)
		if err != nil {
			t.Fatalf("metadata for %d was deleted: %v", id, err)
		}
		if r.HasBodies != want {
			t.Errorf("request %d: has bodies %v, want %v", id, r.HasBodies, want)
		}
	}

	u, err := st.DiskUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u.Requests != 4 || u.WithBodies != 1 || u.BodiesBytes != 200 || u.FileBytes == 0 {
		t.Errorf("disk usage %+v, want 4 requests, 1 with 200 bytes of bodies, and a file size", u)
	}
}

func TestMarkInterrupted(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t0 := time.Unix(1_000_000, 0)
	id, _ := st.Begin(ctx, Begin{StartedAt: t0.Add(500 * time.Millisecond), Method: "POST", Path: "/v1/x"})
	// Sampling ran for 10 s after the request started, stopped, then came back.
	for s := 0; s <= 10; s++ {
		st.InsertSamples(ctx, t0.Add(time.Duration(s)*time.Second), []Sample{{Source: "cpu", Kind: "cpu"}})
	}
	st.InsertSamples(ctx, t0.Add(60*time.Second), []Sample{{Source: "cpu", Kind: "cpu"}})
	// A request with no samples after it ends where it started.
	bare, _ := st.Begin(ctx, Begin{StartedAt: t0.Add(time.Hour), Method: "POST", Path: "/v1/x"})

	if n, err := st.MarkInterrupted(ctx); err != nil || n != 2 {
		t.Fatalf("marked %d, err %v", n, err)
	}
	r, _ := st.Get(ctx, id)
	if r.State != StateInterrupted {
		t.Errorf("state %s", r.State)
	}
	if r.FinishedAt == nil || !r.FinishedAt.Equal(t0.Add(10*time.Second)) {
		t.Errorf("finished at %v, want %v", r.FinishedAt, t0.Add(10*time.Second))
	}
	if r, _ := st.Get(ctx, bare); r.FinishedAt == nil || !r.FinishedAt.Equal(r.StartedAt) {
		t.Errorf("finished at %v, want the start %v", r.FinishedAt, r.StartedAt)
	}
}

func TestListFilters(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, b := range []Begin{
		{Model: "qwen", Preview: "100% sure?"},
		{Model: "gemma", Preview: "hello world"},
		{Model: "qwen", Preview: "hello again"},
	} {
		b.StartedAt, b.Method, b.Path = time.Now(), "POST", "/v1/chat/completions"
		if _, err := st.Begin(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(o ListOptions) []int64 {
		rs, err := st.List(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	check := func(name string, got []int64, want ...int64) {
		t.Helper()
		if len(want) == 0 {
			want = []int64{}
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	check("all, newest first", ids(ListOptions{}), 3, 2, 1)
	check("model", ids(ListOptions{Model: "qwen"}), 3, 1)
	check("search", ids(ListOptions{Search: "hello"}), 3, 2)
	check("search matches model", ids(ListOptions{Search: "gem"}), 2)
	check("percent is literal", ids(ListOptions{Search: "0%"}), 1)
	check("underscore is literal", ids(ListOptions{Search: "_"}))
	check("paging", ids(ListOptions{BeforeID: 3, Limit: 1}), 2)
	check("state", ids(ListOptions{State: StateDone}))
	if ms, _ := st.Models(ctx); fmt.Sprint(ms) != "[gemma qwen]" {
		t.Errorf("models %v", ms)
	}
}

func TestFillCachedTokens(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	add := func(body string) int64 {
		id, err := st.Begin(ctx, Begin{StartedAt: time.Now(), Method: "POST", Path: "/v1/x"})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Finish(ctx, id, Finish{FinishedAt: time.Now(), State: StateDone, ResponseBody: []byte(body)}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	var ids []int64
	for i := range 45 { // more than one batch
		ids = append(ids, add(fmt.Sprint(i)))
	}
	unknown := add("none")

	cached := func(b []byte) *int64 {
		var n int64
		if _, err := fmt.Sscan(string(b), &n); err != nil {
			return nil
		}
		return &n
	}
	n, err := st.FillCachedTokens(ctx, cached)
	if err != nil || n != 45 {
		t.Fatalf("filled %d, err %v", n, err)
	}
	for i, id := range ids {
		r, _ := st.Get(ctx, id)
		if r.CachedTokens == nil || *r.CachedTokens != int64(i) {
			t.Errorf("request %d: cached %v, want %d", id, r.CachedTokens, i)
		}
	}
	if r, _ := st.Get(ctx, unknown); r.CachedTokens != nil {
		t.Errorf("a body with no cache count got %d", *r.CachedTokens)
	}
	// It runs only once.
	if n, err := st.FillCachedTokens(ctx, cached); err != nil || n != 0 {
		t.Errorf("second run filled %d, err %v", n, err)
	}
}
