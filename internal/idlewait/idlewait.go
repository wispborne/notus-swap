// Package idlewait delays jobs until no requests are in flight.
package idlewait

import (
	"errors"
	"sync"
	"time"
)

// Waiter holds at most one job. Starting another replaces the one waiting.
type Waiter struct {
	// Idle returns a channel that is closed while no requests are in flight.
	Idle func() <-chan struct{}

	mu  sync.Mutex
	seq int64
	cur *job
}

// Job describes a waiting or running job.
type Job struct {
	ID     int64     `json:"id"`
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"`
	// Running jobs can't be cancelled.
	Running bool `json:"running"`
}

type job struct {
	Job
	run    func(j Job, atOnce bool)
	now    chan struct{} // closed by Now
	cancel chan struct{} // closed by Cancel, or when a newer job replaces this one
}

// ErrNotWaiting means there is no job, or it has already started.
var ErrNotWaiting = errors.New("nothing is waiting")

// Start replaces any waiting job and returns its ID. run executes in a
// goroutine after Idle closes or Now is called; atOnce is true if Now skipped
// the wait.
func (w *Waiter) Start(reason string, run func(j Job, atOnce bool)) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur != nil && !w.cur.Running {
		close(w.cur.cancel)
	}
	w.seq++
	j := &job{Job: Job{ID: w.seq, Reason: reason, Since: time.Now()}, run: run, now: make(chan struct{}), cancel: make(chan struct{})}
	w.cur = j
	go w.wait(j)
	return j.ID
}

func (w *Waiter) wait(j *job) {
	select {
	case <-j.cancel:
		return
	case <-j.now:
	case <-w.Idle():
	}
	w.mu.Lock()
	if w.cur != j {
		w.mu.Unlock()
		return
	}
	atOnce := false
	select {
	case <-j.now:
		atOnce = true
	default:
	}
	j.Running = true
	snap := j.Job
	w.mu.Unlock()

	j.run(snap, atOnce)

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == j {
		w.cur = nil
	}
}

// Now starts the waiting job immediately.
func (w *Waiter) Now() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == nil || w.cur.Running {
		return ErrNotWaiting
	}
	select {
	case <-w.cur.now:
	default:
		close(w.cur.now)
	}
	return nil
}

// Cancel runs undo under the lock, then drops the waiting job. If undo fails,
// Cancel returns its error and leaves the job waiting.
func (w *Waiter) Cancel(undo func() error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == nil || w.cur.Running {
		return ErrNotWaiting
	}
	if undo != nil {
		if err := undo(); err != nil {
			return err
		}
	}
	close(w.cur.cancel)
	w.cur = nil
	return nil
}

// Current returns a snapshot of the waiting or running job, or nil.
func (w *Waiter) Current() *Job {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cur == nil {
		return nil
	}
	j := w.cur.Job
	return &j
}
