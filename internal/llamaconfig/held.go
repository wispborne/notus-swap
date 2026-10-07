package llamaconfig

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wispborne/notus-swap/internal/idlewait"
)

// Held delays config saves until no requests are in flight, since a
// llama-swap config reload can interrupt streaming responses.
type Held struct {
	Editor *Editor
	Wait   *idlewait.Waiter
	Log    *slog.Logger

	mu   sync.Mutex
	last *Outcome
}

type SaveArgs struct {
	Content       string
	BaseHash      string
	SkipLlamaSwap bool
	Overwrite     bool
}

type Waiting struct {
	ID      int64     `json:"id"`
	Since   time.Time `json:"since"`
	Writing bool      `json:"writing"`
}

// Outcome is the result of the last held save.
type Outcome struct {
	ID    int64     `json:"id"`
	At    time.Time `json:"at"`
	Check Check     `json:"check"`
	File  *File     `json:"file,omitempty"`
	Error string    `json:"error,omitempty"`
}

// Hold replaces any waiting save and returns the new save's ID. Call
// CheckSave first to report validation errors before waiting.
func (h *Held) Hold(a SaveArgs) int64 {
	return h.Wait.Start("config save", func(j idlewait.Job, _ bool) {
		c, f, err := h.Editor.Save(context.Background(), a.Content, a.BaseHash, a.SkipLlamaSwap, a.Overwrite)
		o := &Outcome{ID: j.ID, At: time.Now(), Check: c}
		if err != nil {
			o.Error = err.Error()
			h.log().Error("held llama-swap config save failed", "err", err)
		} else {
			o.File = f
			h.log().Info("llama-swap config saved after waiting for requests", "path", f.Path, "waited", time.Since(j.Since).Round(time.Second))
		}
		h.mu.Lock()
		h.last = o
		h.mu.Unlock()
	})
}

// Now starts the waiting save immediately; false means no save is waiting.
func (h *Held) Now() bool { return h.Wait.Now() == nil }

// Cancel drops the waiting save; false means no save is waiting.
func (h *Held) Cancel() bool { return h.Wait.Cancel(nil) == nil }

// Status returns the pending save and the last result. Either may be nil.
func (h *Held) Status() (*Waiting, *Outcome) {
	var w *Waiting
	if j := h.Wait.Current(); j != nil {
		w = &Waiting{ID: j.ID, Since: j.Since, Writing: j.Running}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return w, h.last
}

func (h *Held) log() *slog.Logger {
	if h.Log == nil {
		return slog.Default()
	}
	return h.Log
}
