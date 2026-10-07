package autoload

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/llamaswap"
)

type fake struct {
	mu      sync.Mutex
	status  llamaswap.Status
	setting Setting
	loads   []string
	done    chan error // each Load waits for a value here
}

func newFake() (*fake, *Loader) {
	f := &fake{
		status:  llamaswap.Status{Up: true, Known: []llamaswap.Model{{Model: "a"}, {Model: "b"}}},
		setting: Setting{Enabled: true, Model: "a", Minutes: 15},
		done:    make(chan error, 10),
	}
	l := &Loader{
		Read:   func() Setting { f.mu.Lock(); defer f.mu.Unlock(); return f.setting },
		Status: func() llamaswap.Status { f.mu.Lock(); defer f.mu.Unlock(); return f.status },
		Load: func(ctx context.Context, model string) error {
			f.mu.Lock()
			f.loads = append(f.loads, model)
			f.mu.Unlock()
			return <-f.done
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return f, l
}

func (f *fake) loaded(models ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.Models = nil
	for _, m := range models {
		f.status.Models = append(f.status.Models, llamaswap.Model{Model: m, State: "ready"})
	}
}

func (f *fake) loadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.loads)
}

// waitLoads waits for the background Load call to start.
func (f *fake) waitLoads(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < 200 && f.loadCount() < n; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if got := f.loadCount(); got != n {
		t.Fatalf("loads = %d, want %d", got, n)
	}
}

func TestLoadsAfterMinutesWithNothingLoaded(t *testing.T) {
	f, l := newFake()
	ctx := context.Background()
	t0 := time.Now()
	l.step(ctx, t0)
	l.step(ctx, t0.Add(14*time.Minute))
	if f.loadCount() != 0 {
		t.Fatal("loaded before 15 minutes")
	}
	if s := l.State(); s.Status != "waiting" || s.LoadsAt != t0.Add(15*time.Minute).UnixMilli() {
		t.Fatalf("state = %+v", s)
	}
	l.step(ctx, t0.Add(15*time.Minute))
	f.waitLoads(t, 1)
	if s := l.State(); s.Status != "loading" {
		t.Fatalf("status = %q, want loading", s.Status)
	}
	if !l.Observe(llamaswap.Transition{Model: "a", From: "stopped", To: "starting"}) {
		t.Error("starting not marked automatic")
	}
	if !l.Observe(llamaswap.Transition{Model: "a", From: "starting", To: "ready"}) {
		t.Error("ready not marked automatic")
	}
	if l.Observe(llamaswap.Transition{Model: "a", From: "ready", To: "stopping"}) {
		t.Error("stopping marked automatic")
	}
	f.done <- nil
}

func TestAnyLoadedModelResetsTheCountdown(t *testing.T) {
	f, l := newFake()
	ctx := context.Background()
	t0 := time.Now()
	l.step(ctx, t0)
	f.loaded("b")
	l.step(ctx, t0.Add(10*time.Minute))
	f.loaded()
	l.step(ctx, t0.Add(20*time.Minute))
	l.step(ctx, t0.Add(34*time.Minute))
	if f.loadCount() != 0 {
		t.Fatal("countdown didn't start again after b unloaded")
	}
	l.step(ctx, t0.Add(35*time.Minute))
	f.waitLoads(t, 1)
	f.done <- nil
}

func TestLlamaSwapDownResetsTheCountdown(t *testing.T) {
	f, l := newFake()
	ctx := context.Background()
	t0 := time.Now()
	l.step(ctx, t0)
	f.mu.Lock()
	f.status.Up = false
	f.mu.Unlock()
	l.step(ctx, t0.Add(10*time.Minute))
	if s := l.State(); s.Status != "down" {
		t.Fatalf("status = %q, want down", s.Status)
	}
	f.mu.Lock()
	f.status.Up = true
	f.mu.Unlock()
	l.step(ctx, t0.Add(11*time.Minute))
	l.step(ctx, t0.Add(20*time.Minute))
	if f.loadCount() != 0 {
		t.Fatal("counted the time llama-swap was down")
	}
}

func TestPauseWaitsForTheNextLoad(t *testing.T) {
	f, l := newFake()
	ctx := context.Background()
	t0 := time.Now()
	l.step(ctx, t0)
	l.Pause()
	l.step(ctx, t0.Add(60*time.Minute))
	if f.loadCount() != 0 {
		t.Fatal("loaded while paused")
	}
	if s := l.State(); s.Status != "paused" {
		t.Fatalf("status = %q, want paused", s.Status)
	}
	// A request loads b; the pause ends, and the countdown starts once b unloads.
	l.Observe(llamaswap.Transition{Model: "b", From: "stopped", To: "starting"})
	f.loaded("b")
	l.step(ctx, t0.Add(61*time.Minute))
	f.loaded()
	l.step(ctx, t0.Add(62*time.Minute))
	l.step(ctx, t0.Add(77*time.Minute))
	f.waitLoads(t, 1)
	f.done <- nil
}

func TestRestartClearsPauseAndCountsFromNow(t *testing.T) {
	f, l := newFake()
	ctx := context.Background()
	t0 := time.Now()
	l.step(ctx, t0)
	l.Pause()
	l.Restart()
	l.step(ctx, t0.Add(20*time.Minute))
	if f.loadCount() != 0 {
		t.Fatal("countdown didn't start from the save")
	}
	l.step(ctx, t0.Add(35*time.Minute))
	f.waitLoads(t, 1)
	f.done <- nil
}

func TestFailedLoadTriesAgainAfterAnotherWait(t *testing.T) {
	f, l := newFake()
	ctx := context.Background()
	t0 := time.Now()
	l.step(ctx, t0)
	l.step(ctx, t0.Add(15*time.Minute))
	f.waitLoads(t, 1)
	f.done <- context.DeadlineExceeded
	for i := 0; i < 200 && l.State().Status == "loading"; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	l.step(ctx, t0.Add(16*time.Minute))
	l.step(ctx, t0.Add(30*time.Minute))
	if f.loadCount() != 1 {
		t.Fatal("tried again too soon")
	}
	l.step(ctx, t0.Add(31*time.Minute))
	f.waitLoads(t, 2)
	f.done <- nil
}

func TestOffOrMissingModelDoesNothing(t *testing.T) {
	f, l := newFake()
	ctx := context.Background()
	t0 := time.Now()
	f.setting.Enabled = false
	l.step(ctx, t0)
	l.step(ctx, t0.Add(time.Hour))
	if s := l.State(); s.Status != "off" {
		t.Fatalf("status = %q, want off", s.Status)
	}
	f.mu.Lock()
	f.setting = Setting{Enabled: true, Model: "gone", Minutes: 15}
	f.mu.Unlock()
	l.step(ctx, t0.Add(2*time.Hour))
	if s := l.State(); s.Status != "missing" {
		t.Fatalf("status = %q, want missing", s.Status)
	}
	if f.loadCount() != 0 {
		t.Fatal("loaded a model that is off or not in the config")
	}
}

func TestParse(t *testing.T) {
	if s := Parse(""); s.Minutes != 15 || s.Enabled {
		t.Errorf("empty = %+v", s)
	}
	if s := Parse(`{"enabled":true,"model":"a","minutes":0}`); s.Minutes != 15 || s.Model != "a" {
		t.Errorf("zero minutes = %+v", s)
	}
	if s := Parse(`{"minutes":99999}`); s.Minutes != 1440 {
		t.Errorf("huge minutes = %+v", s)
	}
}
