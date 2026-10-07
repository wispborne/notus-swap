package capture

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/wispborne/notus-swap/internal/store"
)

// Start describes a request that has just arrived.
type Start struct {
	ID        int64  `json:"id"`
	StartedAt int64  `json:"started_at"` // unix ms
	Method    string `json:"method"`
	Path      string `json:"path"`
	Model     string `json:"model"`
	Streaming bool   `json:"streaming"`
	Preview   string `json:"preview"`
	// RequestBytes is the full size of the request body.
	RequestBytes int64 `json:"request_bytes"`
	// Tags are the tags known from the request alone.
	Tags []Tag `json:"tags"`
	// RetryOf is the request this one retries, or 0.
	RetryOf int64 `json:"retry_of,omitempty"`
	// ClientIP and UserAgent say where the request came from.
	ClientIP  string `json:"client_ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

// Progress is how far llama-server has got through a request's prompt, read
// from its /slots while the request waits for its first token.
type Progress struct {
	Processed int64 `json:"processed"` // prompt tokens done so far, cached ones included
	Cache     int64 `json:"cache"`     // prompt tokens reused from the cache
	// PerSecond is the prompt speed, once two steps have been seen.
	PerSecond *float64 `json:"per_second,omitempty"`
}

// Event is one message on the live feed.
//
//	start:  a request arrived (Start is set)
//	delta:    new streamed output (Content, Reasoning, Chunks, and Tools
//	          naming tool calls that start in this chunk)
//	progress: how far the prompt has got (Progress)
//	finish:   a request ended and its row is final in the store
type Event struct {
	Type      string `json:"type"`
	ID        int64  `json:"id"`
	Start     *Start `json:"start,omitempty"`
	Content   string `json:"content,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	Chunks    int    `json:"chunks,omitempty"` // output chunks so far, about one per token
	// ReasoningChunks counts the chunks so far that carried reasoning.
	ReasoningChunks int       `json:"reasoning_chunks,omitempty"`
	Tools           []string  `json:"tools,omitempty"`
	Progress        *Progress `json:"progress,omitempty"`
	At              int64     `json:"at"` // unix ms
}

// Hub tracks requests in flight and fans their progress out to subscribers
// (the live feed in the UI).
type Hub struct {
	// Account, if set, works out a finished request's GPU energy (joules)
	// and how long it waited behind other requests.
	Account func(w store.Work) (*float64, time.Duration)
	// Props, if set, looks up a model's limits (context size and default
	// output limit) for the issue checks. It returns nil when they aren't known.
	Props func(model string) *store.ModelProps
	// Cmd, if set, gives the hash of the command llama-swap started a model
	// with, or "" when it isn't known.
	Cmd func(model string) string

	mu      sync.Mutex
	flights map[int64]*flight
	subs    map[chan Event]struct{}
	// idle is closed while no requests are in flight. A new one is made
	// when a request arrives.
	idle chan struct{}
}

type flight struct {
	start  Start
	cancel context.CancelCauseFunc
	ps     parser
	chunks int
	// reasoningChunks counts the output chunks that carried reasoning.
	reasoningChunks int
	first           int64 // unix ms of the first output, 0 until then
	// began is when the server was first seen working on the request: its
	// first prompt progress, its first Chat Completions chunk, or its first
	// output. Unix ms, 0 until then.
	began    int64
	progress *Progress
}

func NewHub() *Hub {
	idle := make(chan struct{})
	close(idle)
	return &Hub{flights: map[int64]*flight{}, subs: map[chan Event]struct{}{}, idle: idle}
}

// Subscribe returns a channel of events plus a snapshot of every request in
// flight, taken at the same moment, so no output is missed or repeated. The
// channel is closed if the subscriber falls too far behind; it should then
// reconnect. Call cancel when done.
func (h *Hub) Subscribe() (events <-chan Event, inFlight []Flight, cancel func()) {
	ch := make(chan Event, 512)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	for _, f := range h.flights {
		inFlight = append(inFlight, f.snapshot())
	}
	h.mu.Unlock()
	sort.Slice(inFlight, func(i, j int) bool { return inFlight[i].Start.ID < inFlight[j].Start.ID })
	return ch, inFlight, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// Flight is a snapshot of one request in flight.
type Flight struct {
	Start           Start     `json:"start"`
	Output          Parsed    `json:"output"`
	Chunks          int       `json:"chunks"`
	ReasoningChunks int       `json:"reasoning_chunks,omitempty"`
	FirstTokenAt    int64     `json:"first_token_at,omitempty"` // unix ms
	Progress        *Progress `json:"progress,omitempty"`
}

func (f *flight) snapshot() Flight {
	return Flight{Start: f.start, Output: f.ps.result(), Chunks: f.chunks, ReasoningChunks: f.reasoningChunks, FirstTokenAt: f.first, Progress: f.progress}
}

// Get returns a snapshot of a request in flight.
func (h *Hub) Get(id int64) (Flight, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.flights[id]
	if !ok {
		return Flight{}, false
	}
	return f.snapshot(), true
}

func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.flights)
}

// Works lists the requests in flight, with when the server was first seen
// working on each.
func (h *Hub) Works() []store.Work {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]store.Work, 0, len(h.flights))
	for id, f := range h.flights {
		w := store.Work{ID: id, Arrived: time.UnixMilli(f.start.StartedAt)}
		if f.began != 0 {
			w.Began = time.UnixMilli(f.began)
		}
		out = append(out, w)
	}
	return out
}

// began is when the server was first seen working on a request in flight,
// or the zero time.
func (h *Hub) began(id int64) time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f, ok := h.flights[id]; ok && f.began != 0 {
		return time.UnixMilli(f.began)
	}
	return time.Time{}
}

// Idle returns a channel that is closed once no requests are in flight. It
// is closed already if none are now.
func (h *Hub) Idle() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.idle
}

// Cancel ends a request in flight, both its request to llama-swap and its
// answer to the client. It reports false if the request isn't in flight.
func (h *Hub) Cancel(id int64) bool {
	h.mu.Lock()
	f, ok := h.flights[id]
	h.mu.Unlock()
	if !ok || f.cancel == nil {
		return false
	}
	f.cancel(ErrCancelled)
	return true
}

func (h *Hub) start(s Start, cancel context.CancelCauseFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.flights) == 0 {
		h.idle = make(chan struct{})
	}
	h.flights[s.ID] = &flight{start: s, cancel: cancel}
	h.publish(Event{Type: "start", ID: s.ID, Start: &s, At: s.StartedAt})
}

func (h *Hub) chunk(id int64, x body, content, reasoning string, output bool, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.flights[id]
	if !ok {
		return
	}
	f.ps.add(x)
	// A Chat Completions chunk without text still means the server has
	// finished the prompt; see measureSpeeds.
	if f.began == 0 && len(x.Choices) > 0 {
		f.began = at.UnixMilli()
	}
	if !output {
		return
	}
	f.chunks++
	if reasoning != "" {
		f.reasoningChunks++
	}
	if f.first == 0 {
		f.first = at.UnixMilli()
	}
	if f.began == 0 {
		f.began = f.first
	}
	h.publish(Event{Type: "delta", ID: id, Content: content, Reasoning: reasoning, Chunks: f.chunks,
		ReasoningChunks: f.reasoningChunks, Tools: startTools(x), At: at.UnixMilli()})
}

// waiting lists the requests still waiting for their first token, by model.
func (h *Hub) waiting() map[string][]int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string][]int64{}
	for id, f := range h.flights {
		if f.first == 0 {
			out[f.start.Model] = append(out[f.start.Model], id)
		}
	}
	return out
}

// setProgress records how far a request's prompt has got. It is ignored once
// the first token has arrived.
func (h *Hub) setProgress(id int64, p Progress, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.flights[id]
	if !ok || f.first != 0 {
		return
	}
	f.progress = &p
	if f.began == 0 && p.Processed > 0 {
		f.began = at.UnixMilli()
	}
	h.publish(Event{Type: "progress", ID: id, Progress: &p, At: at.UnixMilli()})
}

func (h *Hub) finish(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.flights[id]; !ok {
		return
	}
	delete(h.flights, id)
	if len(h.flights) == 0 {
		close(h.idle)
	}
	h.publish(Event{Type: "finish", ID: id, At: time.Now().UnixMilli()})
}

// publish must be called with h.mu held. A subscriber whose buffer is full
// is dropped rather than slowing the proxy down.
func (h *Hub) publish(e Event) {
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}
