// Package restart waits for requests to finish before restarting notus-swap.
package restart

import (
	"context"
	"sync"
	"time"

	"github.com/wispborne/notus-swap/internal/idlewait"
)

// Plan tracks a pending restart.
type Plan struct {
	w idlewait.Waiter

	mu    sync.Mutex
	undo  func() error // puts back what the waiting restart changed
	fired *Pending     // set once the restart is due; it can't be cancelled then
	due   chan bool    // receives atOnce when the restart is due
}

// New uses idle's channel, closed while no requests are in flight.
func New(idle func() <-chan struct{}) *Plan {
	return &Plan{w: idlewait.Waiter{Idle: idle}, due: make(chan bool, 1)}
}

// Ask schedules a restart once requests finish. If one is already pending
// or due, it returns false and keeps the original reason and undo.
// Cancel calls undo to reverse changes made for the restart.
func (p *Plan) Ask(reason string, undo func() error) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fired != nil || p.w.Current() != nil {
		return false
	}
	p.undo = undo
	p.w.Start(reason, func(j idlewait.Job, atOnce bool) {
		p.mu.Lock()
		p.fired = &Pending{Reason: j.Reason, Since: j.Since}
		p.mu.Unlock()
		p.due <- atOnce
	})
	return true
}

// Now asks for a restart without waiting for requests to finish.
func (p *Plan) Now(reason string) {
	p.Ask(reason, nil)
	p.w.Now()
}

// Cancel runs undo and drops the waiting restart. If undo fails, the restart
// stays pending. It returns idlewait.ErrNotWaiting if no restart is waiting.
func (p *Plan) Cancel() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.w.Cancel(p.undo); err != nil {
		return err
	}
	p.undo = nil
	return nil
}

// Pending describes a scheduled restart.
type Pending struct {
	Reason string    `json:"reason"`
	Since  time.Time `json:"since"`
}

// Pending returns the scheduled restart, or nil if none was requested.
func (p *Plan) Pending() *Pending {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fired != nil {
		f := *p.fired
		return &f
	}
	if j := p.w.Current(); j != nil {
		return &Pending{Reason: j.Reason, Since: j.Since}
	}
	return nil
}

// Wait blocks until a restart is due or ctx ends. atOnce reports whether Now
// skipped the wait; ok is false if ctx ends first.
func (p *Plan) Wait(ctx context.Context) (atOnce, ok bool) {
	select {
	case atOnce := <-p.due:
		return atOnce, true
	case <-ctx.Done():
		return false, false
	}
}
