package llamaswap

import (
	"context"
	"encoding/json"
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
	Prompt     int64 // prompt tokens queued so far; on some builds this grows a batch at a time
	Processed  int64 // prompt tokens done so far, cached ones included
	Cache      int64 // prompt tokens reused from the cache
	Decoded    int64 // tokens generated so far
}

// FetchSlots reads a model's slots from llama-server's /slots, through
// llama-swap's /upstream/<model>/slots. Like FetchProps, call it only while
// the model is ready. It gives an error when /slots is turned off
// (--no-slots) or lacks the prompt fields.
func (m *Monitor) FetchSlots(ctx context.Context, model string) ([]Slot, error) {
	// llama-server may answer only between batches, which can take a second or more.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", m.base.JoinPath("upstream", model, "slots").String(), nil)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/slots returned HTTP %d", resp.StatusCode)
	}
	return parseSlots(resp.Body)
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
			return nil, fmt.Errorf("/slots has no n_prompt_tokens_processed")
		}
		s := Slot{ID: x.ID, TaskID: x.TaskID, Processing: x.IsProcessing, Processed: *x.Processed}
		if x.Prompt != nil {
			s.Prompt = *x.Prompt
		}
		if x.Cache != nil {
			s.Cache = *x.Cache
		}
		s.Decoded = decoded(x.NextToken)
		out = append(out, s)
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
