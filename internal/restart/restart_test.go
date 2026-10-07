package restart

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/idlewait"
)

// Closing idle releases the pending restart.
func newPlan() (*Plan, chan struct{}) {
	idle := make(chan struct{})
	return New(func() <-chan struct{} { return idle }), idle
}

// waitResult runs Wait in the background.
func waitResult(p *Plan, ctx context.Context) chan [2]bool {
	out := make(chan [2]bool, 1)
	go func() {
		atOnce, ok := p.Wait(ctx)
		out <- [2]bool{atOnce, ok}
	}()
	return out
}

func expect(t *testing.T, out chan [2]bool, want [2]bool) {
	t.Helper()
	select {
	case got := <-out:
		if got != want {
			t.Fatalf("Wait returned %v, want %v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait didn't return")
	}
}

func expectBlocked(t *testing.T, out chan [2]bool) {
	t.Helper()
	select {
	case got := <-out:
		t.Fatalf("Wait returned %v early", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWaitsForAskThenIdle(t *testing.T) {
	p, idle := newPlan()
	out := waitResult(p, context.Background())
	expectBlocked(t, out) // not asked yet
	if p.Pending() != nil {
		t.Fatal("pending before Ask")
	}
	p.Ask("update", nil)
	expectBlocked(t, out) // asked, but requests are in flight
	if pd := p.Pending(); pd == nil || pd.Reason != "update" {
		t.Fatalf("pending %+v", pd)
	}
	close(idle)
	expect(t, out, [2]bool{false, true})
	if pd := p.Pending(); pd == nil || pd.Reason != "update" {
		t.Fatalf("pending after Wait: %+v", pd)
	}
}

func TestNowDoesNotWaitForIdle(t *testing.T) {
	p, _ := newPlan()
	out := waitResult(p, context.Background())
	p.Ask("update", nil)
	expectBlocked(t, out)
	p.Now("restart now button")
	expect(t, out, [2]bool{true, true})
	if pd := p.Pending(); pd.Reason != "update" {
		t.Fatalf("the first reason should be kept, got %q", pd.Reason)
	}
	p.Now("again") // must not panic on a second close
}

func TestAskWhileIdleRestartsAtOnce(t *testing.T) {
	p, idle := newPlan()
	close(idle)
	p.Ask("restart button", nil)
	expect(t, waitResult(p, context.Background()), [2]bool{false, true})
}

func TestWaitEndsWithContext(t *testing.T) {
	p, _ := newPlan()
	ctx, cancel := context.WithCancel(context.Background())
	out := waitResult(p, ctx)
	cancel()
	expect(t, out, [2]bool{false, false})
}

func TestCancelUndoesPendingRestart(t *testing.T) {
	p, idle := newPlan()
	out := waitResult(p, context.Background())
	undone := 0
	p.Ask("update", func() error {
		undone++
		return nil
	})
	if p.Ask("restart button", nil) {
		t.Fatal("a second Ask replaced the first")
	}
	if err := p.Cancel(); err != nil {
		t.Fatal(err)
	}
	if undone != 1 || p.Pending() != nil {
		t.Fatalf("undone %d times, pending %+v", undone, p.Pending())
	}
	if err := p.Cancel(); !errors.Is(err, idlewait.ErrNotWaiting) {
		t.Fatalf("second Cancel: %v", err)
	}
	close(idle)
	expectBlocked(t, out) // cancelled, so going idle doesn't restart

	if !p.Ask("restart button", nil) {
		t.Fatal("Ask refused after a cancel")
	}
	expect(t, out, [2]bool{false, true})
	if err := p.Cancel(); !errors.Is(err, idlewait.ErrNotWaiting) {
		t.Fatalf("cancelled a restart that was already due: %v", err)
	}
}

func TestFailedUndoKeepsRestartPending(t *testing.T) {
	p, idle := newPlan()
	out := waitResult(p, context.Background())
	p.Ask("update", func() error { return errors.New("disk full") })
	if err := p.Cancel(); err == nil || err.Error() != "disk full" {
		t.Fatalf("Cancel: %v", err)
	}
	if p.Pending() == nil {
		t.Fatal("the restart was dropped although undo failed")
	}
	close(idle)
	expect(t, out, [2]bool{false, true})
}
