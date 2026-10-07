// Package autoload loads a chosen default model once llama-swap has had no
// model loaded for a set number of minutes.
package autoload

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/wispborne/notus-swap/internal/llamaswap"
)

// DefaultMinutes is how long nothing must be loaded before the default
// model loads, when the setting doesn't say.
const DefaultMinutes = 15

// Setting is the "default_model" setting.
type Setting struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model"`
	Minutes int    `json:"minutes"`
}

// Parse reads a stored setting. An empty or broken value gives the defaults.
func Parse(v string) Setting {
	s := Setting{Minutes: DefaultMinutes}
	if v != "" {
		json.Unmarshal([]byte(v), &s)
	}
	return s.Clean()
}

// Clean keeps Minutes between 1 and a day.
func (s Setting) Clean() Setting {
	if s.Minutes < 1 {
		s.Minutes = DefaultMinutes
	}
	s.Minutes = min(s.Minutes, 24*60)
	return s
}

// State is what the System page shows about the default model.
type State struct {
	Setting
	// Status is one of:
	//   off      not enabled, or no model chosen
	//   down     llama-swap isn't reachable
	//   missing  the chosen model isn't in llama-swap's config
	//   loaded   some model is loaded, so nothing is counting down
	//   loading  this loader asked for the default model and it is loading
	//   paused   a model was unloaded by hand; waits for the next load
	//   waiting  counting down; LoadsAt says when the default model loads
	Status  string `json:"status"`
	LoadsAt int64  `json:"loads_at,omitempty"` // unix ms
}

// Loader watches llama-swap's model states and loads the default model when
// nothing has been loaded for the set time.
type Loader struct {
	// Read returns the current setting.
	Read func() Setting
	// Status returns what the llama-swap monitor last saw.
	Status func() llamaswap.Status
	// Load asks llama-swap to load a model, and returns once it is loaded
	// or has failed.
	Load func(ctx context.Context, model string) error
	Log  *slog.Logger

	mu sync.Mutex
	// emptySince is when llama-swap was last seen up with nothing loaded,
	// or zero while a model is loaded or llama-swap is down.
	emptySince time.Time
	// paused is set by an unload by hand, and cleared by the next load.
	paused bool
	// loading is the model this loader asked for, until its load ends.
	loading  string
	loadDone bool // Load has returned for loading
}

// Run checks every 5 seconds until ctx ends.
func (l *Loader) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		l.step(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (l *Loader) step(ctx context.Context, now time.Time) {
	st, s := l.Status(), l.Read()
	l.mu.Lock()
	switch {
	case !st.Up || len(st.Models) > 0:
		l.emptySince = time.Time{}
	case l.emptySince.IsZero():
		l.emptySince = now
	}
	// A load that ended without a transition to "ready" or "stopped" being
	// seen, such as when the event feed dropped.
	if l.loading != "" && l.loadDone && state(st, l.loading) != "starting" {
		l.loading = ""
	}
	due := s.Enabled && s.Model != "" && !l.paused && l.loading == "" && !l.emptySince.IsZero() &&
		now.Sub(l.emptySince) >= time.Duration(s.Minutes)*time.Minute && known(st, s.Model)
	if due {
		l.loading, l.loadDone, l.emptySince = s.Model, false, time.Time{}
	}
	l.mu.Unlock()
	if !due {
		return
	}
	l.Log.Info("no model loaded for a while; loading the default model", "model", s.Model, "minutes", s.Minutes)
	go func() {
		err := l.Load(ctx, s.Model)
		if err != nil && ctx.Err() == nil {
			l.Log.Warn("could not load the default model", "model", s.Model, "err", err)
		}
		l.mu.Lock()
		if l.loading == s.Model {
			l.loadDone = true
			if err != nil {
				l.loading = ""
			}
		}
		l.mu.Unlock()
	}()
}

// Observe sees every change of model state, and reports whether this loader
// started it: the "starting" and "ready" steps of a load it asked for.
func (l *Loader) Observe(t llamaswap.Transition) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	auto := t.Model == l.loading && (t.To == "starting" || t.To == "ready")
	if t.To == "starting" {
		l.paused = false
	}
	if t.Model == l.loading && t.To != "starting" {
		l.loading = ""
	}
	return auto
}

// Pause stops the countdown after a model was unloaded by hand. The next
// load of any model, or saving the setting, starts it again.
func (l *Loader) Pause() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paused = true
}

// Restart clears a pause and starts the countdown from now. Called after the
// setting is saved.
func (l *Loader) Restart() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paused = false
	l.emptySince = time.Time{}
}

// State reports the setting and what the loader is doing.
func (l *Loader) State() State {
	st, s := l.Status(), l.Read()
	l.mu.Lock()
	defer l.mu.Unlock()
	out := State{Setting: s}
	switch {
	case !s.Enabled || s.Model == "":
		out.Status = "off"
	case !st.Up:
		out.Status = "down"
	case l.loading != "":
		out.Status = "loading"
	case !known(st, s.Model):
		out.Status = "missing"
	case len(st.Models) > 0:
		out.Status = "loaded"
	case l.paused:
		out.Status = "paused"
	default:
		out.Status = "waiting"
		since := l.emptySince
		if since.IsZero() {
			since = time.Now() // the next check starts the countdown
		}
		out.LoadsAt = since.Add(time.Duration(s.Minutes) * time.Minute).UnixMilli()
	}
	return out
}

func known(st llamaswap.Status, model string) bool {
	for _, m := range st.Known {
		if m.Model == model {
			return true
		}
	}
	return false
}

func state(st llamaswap.Status, model string) string {
	for _, m := range st.Models {
		if m.Model == model {
			return m.State
		}
	}
	return "stopped"
}
