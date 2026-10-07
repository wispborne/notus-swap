package capture

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wispborne/notus-swap/internal/llamaswap"
)

// SlotPoller publishes prompt progress from llama-server's /slots once a
// second while requests wait for their first token. /slots omits the full
// prompt size, so progress has token counts and speed but no percentage.
type SlotPoller struct {
	Hub *Hub
	// Fetch reads a model's slots. Ready reports whether a model is loaded,
	// since asking one that isn't would load it. ID turns an alias into the
	// model's ID.
	Fetch func(ctx context.Context, model string) ([]llamaswap.Slot, error)
	Ready func(model string) bool
	ID    func(name string) string
	Log   *slog.Logger

	mu     sync.Mutex
	off    map[string]bool // models whose /slots failed, until they load again
	tracks map[int64]*track
}

// track follows one request's prompt. The processed count moves a batch at
// a time, so the speed is measured from the first step seen to the latest.
type track struct {
	task    int64
	seen    int64 // processed count at the first poll
	firstN  int64
	firstAt time.Time
	lastN   int64
	lastAt  time.Time
	steps   int
}

// Run polls until ctx ends. Polls never overlap: a slow one delays the next.
func (p *SlotPoller) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.poll(ctx, time.Now)
		}
	}
}

// Loaded turns polling back on for a model that has just loaded.
func (p *SlotPoller) Loaded(model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.off, model)
}

func (p *SlotPoller) poll(ctx context.Context, now func() time.Time) {
	waiting := p.Hub.waiting()
	p.mu.Lock()
	if p.tracks == nil {
		p.tracks, p.off = map[int64]*track{}, map[string]bool{}
	}
	for id := range p.tracks {
		if !contains(waiting, id) {
			delete(p.tracks, id)
		}
	}
	p.mu.Unlock()

	for name, ids := range waiting {
		// With several requests waiting on one model, it isn't clear which
		// slot is whose.
		if len(ids) != 1 || name == "" {
			continue
		}
		model := p.ID(name)
		p.mu.Lock()
		off := p.off[model]
		p.mu.Unlock()
		if off || !p.Ready(model) {
			continue
		}
		slots, err := p.Fetch(ctx, model)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.mu.Lock()
			p.off[model] = true
			p.mu.Unlock()
			p.Log.Info("live prompt speed is off for this model until it loads again", "model", model, "err", err)
			continue
		}
		// The slot working on a prompt has generated nothing yet. Another
		// request may be generating in a second slot.
		var match *llamaswap.Slot
		n := 0
		for i := range slots {
			if slots[i].Processing && slots[i].Decoded == 0 {
				match = &slots[i]
				n++
			}
		}
		if n != 1 {
			continue
		}
		p.Hub.setProgress(ids[0], p.step(ids[0], *match, now()), now())
	}
}

func (p *SlotPoller) step(id int64, s llamaswap.Slot, at time.Time) Progress {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.tracks[id]
	if t == nil || t.task != s.TaskID {
		t = &track{task: s.TaskID, seen: s.Processed}
		p.tracks[id] = t
	}
	last := t.seen
	if t.steps > 0 {
		last = t.lastN
	}
	if s.Processed != last {
		if t.steps == 0 {
			t.firstN, t.firstAt = s.Processed, at
		}
		t.lastN, t.lastAt = s.Processed, at
		t.steps++
	}
	out := Progress{Processed: s.Processed, Cache: s.Cache}
	if t.steps >= 2 {
		if secs := t.lastAt.Sub(t.firstAt).Seconds(); secs > 0 {
			v := float64(t.lastN-t.firstN) / secs
			out.PerSecond = &v
		}
	}
	return out
}

func contains(m map[string][]int64, id int64) bool {
	for _, ids := range m {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
	}
	return false
}
