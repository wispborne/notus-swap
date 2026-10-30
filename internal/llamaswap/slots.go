package llamaswap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Slot is one llama-server slot, from /slots. Only the fields notus-swap uses
// are read, because the rest changes between llama.cpp builds.
type Slot struct {
	ID         int64
	TaskID     int64
	Processing bool
	Prompt     int64  // prompt tokens queued so far; on some builds this grows a batch at a time
	Processed  int64  // prompt tokens done so far, cached ones included
	Cache      *int64 // prompt tokens reused from the cache; nil when not given
	Decoded    int64  // tokens generated so far
	// Total is the prompt's full size, when the server gives it. Strata
	// does; llama-server doesn't.
	Total int64
	// SizeOnly is set when the server gives the prompt's size but not how
	// much of it is done, so Processed means nothing.
	SizeOnly bool
}

// errNoProcessed is a /slots answer without n_prompt_tokens_processed, such
// as Strata's.
var errNoProcessed = errors.New("/slots has no n_prompt_tokens_processed")

// FetchSlots reads a model's slots from llama-server's /slots, through
// llama-swap's /upstream/<model>/slots. Like FetchProps, call it only while
// the model is ready. When /slots lacks the prompt fields, it reads Strata's
// /metrics instead. It gives an error when /slots is turned off (--no-slots)
// or neither answer has the prompt fields.
func (m *Monitor) FetchSlots(ctx context.Context, model string) ([]Slot, error) {
	// llama-server may answer only between batches, which can take a second or more.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	slots, err := getUpstream(m, ctx, model, "slots", parseSlots)
	if errors.Is(err, errNoProcessed) {
		if s, serr := getUpstream(m, ctx, model, "metrics", parseStrataSlots); serr == nil {
			return s, nil
		}
	}
	return slots, err
}

// getUpstream reads one JSON answer from a model's server, through
// llama-swap's /upstream/<model>/<path>.
func getUpstream[T any](m *Monitor, ctx context.Context, model, path string, parse func(io.Reader) (T, error)) (T, error) {
	var zero T
	req, _ := http.NewRequestWithContext(ctx, "GET", m.base.JoinPath("upstream", model, path).String(), nil)
	// Strata's /metrics answers in Prometheus' text format unless JSON is asked for.
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("/%s returned HTTP %d", path, resp.StatusCode)
	}
	return parse(resp.Body)
}

func parseSlots(r io.Reader) ([]Slot, error) {
	var list []struct {
		ID           int64           `json:"id"`
		TaskID       int64           `json:"id_task"`
		IsProcessing bool            `json:"is_processing"`
		Prompt       *int64          `json:"n_prompt_tokens"`
		Processed    *int64          `json:"n_prompt_tokens_processed"`
		Cache        *int64          `json:"n_prompt_tokens_cache"`
		NextToken    json.RawMessage `json:"next_token"`
	}
	if err := json.NewDecoder(r).Decode(&list); err != nil {
		return nil, fmt.Errorf("reading /slots: %w", err)
	}
	out := make([]Slot, 0, len(list))
	for _, x := range list {
		if x.Processed == nil {
			return nil, errNoProcessed
		}
		s := Slot{ID: x.ID, TaskID: x.TaskID, Processing: x.IsProcessing, Processed: *x.Processed}
		if x.Prompt != nil {
			s.Prompt = *x.Prompt
		}
		s.Cache = x.Cache
		s.Decoded = decoded(x.NextToken)
		out = append(out, s)
	}
	return out, nil
}

// parseStrataSlots reads Strata's /metrics, in JSON. One request runs on its
// own outside the slots ("live"); with --batch, others run in slots. Strata
// reads every prompt with one counter, so the counter belongs to this
// request only when its total is this request's prompt size. Otherwise only
// the size is known.
func parseStrataSlots(r io.Reader) ([]Slot, error) {
	type row struct {
		State        string `json:"state"`
		PromptTokens int64  `json:"prompt_tokens"`
		Generated    int64  `json:"generated"`
	}
	var x struct {
		Live *struct {
			row
			PromptRead  *int64 `json:"prompt_read"`
			PromptTotal *int64 `json:"prompt_total"`
			Slots       []struct {
				Slot int64 `json:"slot"`
				row
			} `json:"slots"`
		} `json:"live"`
	}
	if err := json.NewDecoder(r).Decode(&x); err != nil {
		return nil, fmt.Errorf("reading Strata's /metrics: %w", err)
	}
	if x.Live == nil || x.Live.State == "" {
		return nil, errNoProcessed
	}
	slot := func(id int64, w row) (Slot, bool) {
		switch w.State {
		case "reading":
			return Slot{ID: id, Processing: true, Total: w.PromptTokens, SizeOnly: true}, true
		case "generating":
			// The first token may not be counted yet.
			return Slot{ID: id, Processing: true, Decoded: max(w.Generated, 1)}, true
		}
		return Slot{}, false
	}
	var out []Slot
	if s, ok := slot(-1, x.Live.row); ok {
		l := x.Live
		if s.SizeOnly && l.PromptRead != nil && l.PromptTotal != nil && *l.PromptTotal == l.PromptTokens {
			s.Processed, s.SizeOnly = *l.PromptRead, false
		}
		out = append(out, s)
	}
	for _, w := range x.Live.Slots {
		// With --batch, the request in "live" is often in a slot too. A
		// slot reading a prompt of the same size is taken to be it.
		if w.State == "reading" && x.Live.State == "reading" && w.PromptTokens == x.Live.PromptTokens {
			continue
		}
		if s, ok := slot(w.Slot, w.row); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// decoded reads n_decoded from next_token, which is an object on older
// builds and a list of one object on newer ones.
func decoded(raw json.RawMessage) int64 {
	type next struct {
		NDecoded int64 `json:"n_decoded"`
	}
	var one next
	if json.Unmarshal(raw, &one) == nil {
		return one.NDecoded
	}
	var list []next
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
		return list[0].NDecoded
	}
	return 0
}

// ID gives the model ID for a name that may be an alias. A name that isn't
// known is returned as it is.
func (m *Monitor) ID(name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.status.Known {
		if k.Model == name {
			return name
		}
		for _, a := range k.Aliases {
			if a == name {
				return k.Model
			}
		}
	}
	return name
}
