// Package awake keeps the computer from sleeping while requests are in
// flight. A remote request isn't user activity, so without this Windows can
// sleep in the middle of one. Only Windows has a lock to take; elsewhere
// Keeper does nothing.
package awake

import (
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Keeper counts requests in flight and holds the sleep lock while any are.
// It lets go Delay after the last one ends, so back-to-back requests keep
// one lock instead of taking and releasing it each time.
type Keeper struct {
	Delay time.Duration
	Log   *slog.Logger

	// set takes (true) or releases (false) the lock. Nil means there is no
	// lock on this system.
	set func(on bool)

	mu      sync.Mutex
	n       int
	enabled bool
	held    bool
	release *time.Timer
}

// New returns a Keeper for this system's sleep lock, turned off until
// SetEnabled turns it on.
func New(delay time.Duration, log *slog.Logger) *Keeper {
	return &Keeper{Delay: delay, Log: log, set: startLock(log)}
}

// Wrap counts each request next handles, except GET and HEAD. Those include
// the log streams, which stay open as long as a page shows them. A streamed
// answer counts until next returns: when its body has been sent, or the
// client has gone.
func (k *Keeper) Wrap(next http.Handler) http.Handler {
	if k.set == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		k.Begin()
		defer k.End()
		next.ServeHTTP(w, r)
	})
}

// Begin counts a request that has started.
func (k *Keeper) Begin() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.n++
	k.update()
}

// End counts a request that has ended.
func (k *Keeper) End() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.n > 0 {
		k.n--
	}
	k.update()
}

// SetEnabled turns keeping awake on or off. Turning it off releases the lock
// at once.
func (k *Keeper) SetEnabled(on bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.enabled = on
	k.update()
}

// State is what the Settings page shows.
type State struct {
	Supported bool `json:"supported"`
	Enabled   bool `json:"enabled"`
	// Held is whether the lock is held now, and InFlight the requests counted.
	Held     bool `json:"held"`
	InFlight int  `json:"in_flight"`
}

func (k *Keeper) State() State {
	k.mu.Lock()
	defer k.mu.Unlock()
	return State{Supported: k.set != nil, Enabled: k.enabled, Held: k.held, InFlight: k.n}
}

// update takes or releases the lock to match the count. Call it with k.mu
// held.
func (k *Keeper) update() {
	if k.set == nil {
		return
	}
	want := k.enabled && k.n > 0
	if want {
		if k.release != nil {
			k.release.Stop()
			k.release = nil
		}
		if !k.held {
			k.held = true
			k.set(true)
			k.Log.Info("keeping the computer awake while requests are in flight")
		}
		return
	}
	if !k.held {
		return
	}
	if !k.enabled || k.Delay <= 0 {
		k.letGo()
		return
	}
	if k.release != nil {
		return // already letting go
	}
	var t *time.Timer
	t = time.AfterFunc(k.Delay, func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		if k.release == t {
			k.letGo()
		}
	})
	k.release = t
}

// letGo releases the lock. Call it with k.mu held.
func (k *Keeper) letGo() {
	if k.release != nil {
		k.release.Stop()
		k.release = nil
	}
	k.held = false
	k.set(false)
	k.Log.Info("no requests in flight; the computer may sleep again")
}
