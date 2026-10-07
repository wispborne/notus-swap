package idlewait

import (
	"errors"
	"testing"
	"time"
)

func newWaiter() (*Waiter, chan struct{}) {
	idle := make(chan struct{})
	return &Waiter{Idle: func() <-chan struct{} { return idle }}, idle
}

func recordRuns() (func(Job, bool), chan Job) {
	runs := make(chan Job, 4)
	return func(j Job, _ bool) { runs <- j }, runs
}

func expectRun(t *testing.T, c chan Job, id int64) {
	t.Helper()
	select {
	case j := <-c:
		if j.ID != id {
			t.Fatalf("job %d ran, want %d", j.ID, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the job didn't run")
	}
}

func expectNoRun(t *testing.T, c chan Job) {
	t.Helper()
	select {
	case j := <-c:
		t.Fatalf("job %d ran", j.ID)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestStartReplacesWaitingJob(t *testing.T) {
	w, idle := newWaiter()
	run, runs := recordRuns()
	w.Start("first", run)
	id := w.Start("second", run)
	if j := w.Current(); j == nil || j.ID != id || j.Reason != "second" {
		t.Fatalf("current %+v", j)
	}
	close(idle)
	expectRun(t, runs, id)
	expectNoRun(t, runs)
}

func TestNowSkipsIdle(t *testing.T) {
	w, _ := newWaiter()
	got := make(chan bool, 1)
	w.Start("x", func(_ Job, atOnce bool) { got <- atOnce })
	if err := w.Now(); err != nil {
		t.Fatal(err)
	}
	select {
	case atOnce := <-got:
		if !atOnce {
			t.Fatal("atOnce is false after Now")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Now didn't run the job")
	}
}

func TestCancelRejectsRunningJob(t *testing.T) {
	w, idle := newWaiter()
	release := make(chan struct{})
	started := make(chan struct{})
	w.Start("x", func(Job, bool) {
		close(started)
		<-release
	})
	close(idle)
	<-started
	if j := w.Current(); j == nil || !j.Running {
		t.Fatalf("current %+v", j)
	}
	undone := false
	err := w.Cancel(func() error {
		undone = true
		return nil
	})
	if !errors.Is(err, ErrNotWaiting) || undone {
		t.Fatalf("cancelled a running job: %v, undo ran %v", err, undone)
	}
	if err := w.Now(); !errors.Is(err, ErrNotWaiting) {
		t.Fatalf("Now on a running job: %v", err)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for w.Current() != nil {
		if time.Now().After(deadline) {
			t.Fatal("the job stayed current after it finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
