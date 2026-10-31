package awake

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeLock records each call to set.
type fakeLock struct {
	mu    sync.Mutex
	calls []bool
}

func (f *fakeLock) set(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, on)
}

func (f *fakeLock) got() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func newTestKeeper(delay time.Duration) (*Keeper, *fakeLock) {
	f := &fakeLock{}
	return &Keeper{Delay: delay, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), set: f.set, enabled: true}, f
}

func TestCountsRequests(t *testing.T) {
	k, f := newTestKeeper(0)
	k.Begin()
	k.Begin()
	if s := k.State(); !s.Held || s.InFlight != 2 {
		t.Fatalf("two in flight: %+v", s)
	}
	k.End()
	if s := k.State(); !s.Held || s.InFlight != 1 {
		t.Fatalf("one in flight: %+v", s)
	}
	k.End()
	if s := k.State(); s.Held || s.InFlight != 0 {
		t.Fatalf("none in flight: %+v", s)
	}
	k.End() // an extra End doesn't go below zero
	if s := k.State(); s.InFlight != 0 {
		t.Fatalf("count went below zero: %+v", s)
	}
	if got := f.got(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("lock calls = %v, want one take and one release", got)
	}
}

func TestReleasesAfterDelay(t *testing.T) {
	k, f := newTestKeeper(50 * time.Millisecond)
	k.Begin()
	k.End()
	if !k.State().Held {
		t.Fatal("released before the delay")
	}
	// A request within the delay keeps the same lock.
	k.Begin()
	k.End()
	time.Sleep(150 * time.Millisecond)
	if k.State().Held {
		t.Fatal("still held after the delay")
	}
	if got := f.got(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("lock calls = %v, want one take and one release", got)
	}
}

func TestTurnedOff(t *testing.T) {
	k, f := newTestKeeper(time.Hour)
	k.Begin()
	k.SetEnabled(false)
	if k.State().Held {
		t.Fatal("still held after turning off")
	}
	k.Begin()
	k.End()
	k.SetEnabled(true)
	if !k.State().Held {
		t.Fatal("not held after turning on with a request in flight")
	}
	k.End()
	k.SetEnabled(false) // releases at once, without waiting for the delay
	if got := f.got(); !slices.Equal(got, []bool{true, false, true, false}) {
		t.Fatalf("lock calls = %v", got)
	}
}

func TestWrapCountsUntilBodyEnds(t *testing.T) {
	k, f := newTestKeeper(0)
	sent, finish := make(chan struct{}), make(chan struct{})
	h := k.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data: 1\n\n"))
		w.(http.Flusher).Flush()
		close(sent)
		<-finish
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", nil)
		if err != nil {
			t.Error(err)
			return
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}()
	<-sent
	if s := k.State(); !s.Held || s.InFlight != 1 {
		t.Fatalf("while streaming: %+v", s)
	}
	close(finish)
	<-done
	// The handler returns before the client reads the end, so wait a little.
	for i := 0; i < 100 && k.State().InFlight > 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if s := k.State(); s.Held || s.InFlight != 0 {
		t.Fatalf("after the stream: %+v", s)
	}

	res, err := http.Get(srv.URL + "/logs/stream")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := f.got(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("lock calls = %v; a GET shouldn't count", got)
	}
}
