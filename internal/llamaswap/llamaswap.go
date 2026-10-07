// Package llamaswap watches llama-swap through its own event feed
// (GET /api/events): whether it is up, every configured model and its state,
// and each change of state as it happens.
package llamaswap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Model is one entry of llama-swap's model list.
type Model struct {
	Model         string   `json:"model"`
	Name          string   `json:"name,omitempty"`
	Description   string   `json:"description,omitempty"`
	State         string   `json:"state"` // stopped, starting, ready, stopping
	Unlisted      bool     `json:"unlisted,omitempty"`
	ContextLength int      `json:"context_length,omitempty"`
	Aliases       []string `json:"aliases,omitempty"`
}

// Status is the last thing the Monitor saw.
type Status struct {
	Up    bool   `json:"up"`
	Error string `json:"error,omitempty"`
	// Models are the loaded ones (any state but stopped).
	Models []Model `json:"models"`
	// Known is every model in llama-swap's config.
	Known     []Model `json:"known"`
	CheckedAt int64   `json:"checked_at"` // unix ms of the last update
}

// Transition is one model changing state.
type Transition struct {
	Model    string
	From, To string
	At       time.Time
	// Auto is set by notus-swap, not llama-swap: this step belongs to a load
	// notus-swap started to bring back the default model.
	Auto bool
}

// Monitor keeps a connection to llama-swap's event feed open, reconnecting
// every few seconds while llama-swap is down.
type Monitor struct {
	base   *url.URL
	client *http.Client
	// OnTransition, if set, is called for every change of model state seen
	// after the first model list.
	OnTransition func(Transition)
	// OnReady, if set, is called when a model becomes ready, and for each
	// model already ready in the first model list after connecting.
	OnReady func(model string)

	mu     sync.Mutex
	status Status
	states map[string]string
}

func NewMonitor(base *url.URL) *Monitor {
	return &Monitor{base: base, client: &http.Client{},
		status: Status{Models: []Model{}, Known: []Model{}, Error: "connecting"}}
}

func (m *Monitor) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Run follows the event feed until ctx ends.
func (m *Monitor) Run(ctx context.Context, retry time.Duration) {
	for ctx.Err() == nil {
		err := m.follow(ctx)
		m.mu.Lock()
		m.status = Status{Models: []Model{}, Known: m.status.Known, Error: err.Error(), CheckedAt: time.Now().UnixMilli()}
		m.states = nil // the next model list is a fresh baseline
		m.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-time.After(retry):
		}
	}
}

type envelope struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type apiModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	State         string   `json:"state"`
	Unlisted      bool     `json:"unlisted"`
	PeerID        string   `json:"peerID"`
	ContextLength int      `json:"context_length"`
	Aliases       []string `json:"aliases"`
}

// follow reads the feed until it ends and returns why it ended.
func (m *Monitor) follow(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", m.base.JoinPath("api", "events").String(), nil)
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("not reachable")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("llama-swap requires an API key for /api/events")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("/api/events returned HTTP %d", resp.StatusCode)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 64<<20) // log history can arrive as one big event
	for sc.Scan() {
		data, ok := bytes.CutPrefix(sc.Bytes(), []byte("data:"))
		if !ok {
			continue
		}
		var e envelope
		if json.Unmarshal(bytes.TrimSpace(data), &e) != nil || e.Type != "modelStatus" {
			continue
		}
		var list []apiModel
		if json.Unmarshal([]byte(e.Data), &list) == nil {
			m.update(list, time.Now())
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("event stream failed: %v", err)
	}
	return fmt.Errorf("not reachable")
}

func (m *Monitor) update(list []apiModel, now time.Time) {
	s := Status{Up: true, Models: []Model{}, Known: []Model{}, CheckedAt: now.UnixMilli()}
	states := map[string]string{}
	for _, a := range list {
		if a.PeerID != "" {
			continue // models on other llama-swap peers are not on this machine
		}
		mo := Model{Model: a.ID, Name: a.Name, Description: a.Description, State: a.State, Unlisted: a.Unlisted, ContextLength: a.ContextLength, Aliases: a.Aliases}
		s.Known = append(s.Known, mo)
		if a.State != "stopped" {
			s.Models = append(s.Models, mo)
		}
		states[a.ID] = a.State
	}

	m.mu.Lock()
	prev := m.states
	m.states, m.status = states, s
	m.mu.Unlock()

	for id, to := range states {
		from, ok := prev[id]
		if !ok {
			from = "stopped"
		}
		if m.OnReady != nil && to == "ready" && from != "ready" {
			m.OnReady(id)
		}
		if prev != nil && m.OnTransition != nil && from != to {
			m.OnTransition(Transition{Model: id, From: from, To: to, At: now})
		}
	}
}

// Names returns a model's ID and aliases, as requests may use any of them.
func (m *Monitor) Names(model string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.status.Known {
		if k.Model == model {
			return append([]string{model}, k.Aliases...)
		}
	}
	return []string{model}
}

// Ready reports whether a model is loaded and ready right now.
func (m *Monitor) Ready(model string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states[model] == "ready"
}
