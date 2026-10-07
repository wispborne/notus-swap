package llamaswap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// send writes one llama-swap event: an envelope whose data is a JSON string.
func send(w http.ResponseWriter, typ string, data any) {
	inner, _ := json.Marshal(data)
	env, _ := json.Marshal(map[string]string{"type": typ, "data": string(inner)})
	fmt.Fprintf(w, "event:message\ndata:%s\n\n", env)
	w.(http.Flusher).Flush()
}

func models(qwen, embed string) []map[string]any {
	return []map[string]any{
		{"id": "qwen", "name": "Qwen 3", "state": qwen},
		{"id": "embed", "state": embed},
		{"id": "remote-model", "peerID": "other-box", "state": ""},
	}
}

func TestFollowsModelStates(t *testing.T) {
	step := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events" {
			t.Errorf("asked for %s", r.URL.Path)
		}
		send(w, "logData", map[string]string{"source": "proxy", "data": "hello"})
		send(w, "modelStatus", models("ready", "stopped"))
		<-step
		send(w, "modelStatus", models("stopping", "starting"))
		send(w, "modelStatus", models("stopped", "ready"))
		<-step
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	m := NewMonitor(u)

	var mu sync.Mutex
	var seen, ready []string
	m.OnTransition = func(tr Transition) {
		mu.Lock()
		seen = append(seen, tr.Model+":"+tr.From+">"+tr.To)
		mu.Unlock()
	}
	m.OnReady = func(model string) {
		mu.Lock()
		ready = append(ready, model)
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx, 50*time.Millisecond)

	waitFor(t, func() bool { return m.Status().Up })
	s := m.Status()
	if len(s.Known) != 2 || len(s.Models) != 1 || s.Models[0].Name != "Qwen 3" {
		t.Errorf("first list (peer models left out): %+v", s)
	}

	step <- struct{}{}
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == 4 })
	want := map[string]bool{"qwen:ready>stopping": true, "embed:stopped>starting": true, "qwen:stopping>stopped": true, "embed:starting>ready": true}
	for _, s := range seen {
		if !want[s] {
			t.Errorf("unexpected transition %s (all: %v)", s, seen)
		}
	}
	if s := m.Status(); len(s.Models) != 1 || s.Models[0].Model != "embed" {
		t.Errorf("after swap: %+v", s.Models)
	}
	// Ready is reported for a model already ready at connect, and for each load.
	mu.Lock()
	if fmt.Sprint(ready) != "[qwen embed]" {
		t.Errorf("ready calls %v", ready)
	}
	mu.Unlock()
	if !m.Ready("embed") || m.Ready("qwen") {
		t.Error("Ready disagrees with the last model list")
	}

	srv.CloseClientConnections()
	close(step)
	waitFor(t, func() bool { return !m.Status().Up })
}

func TestAPIKeyNeeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	m := NewMonitor(u)
	if err := m.follow(context.Background()); err == nil || err.Error() != "llama-swap requires an API key for /api/events" {
		t.Errorf("got %v", err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestParseProps(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"default_generation_settings":{"n_ctx":8192,"params":{"n_predict":-1}},"total_slots":2,"build_info":"b11146-7fe450e19"}`, "{8192 -1 2 b11146-7fe450e19}"},
		{`{"default_generation_settings":{"n_ctx":4096,"n_predict":512},"total_slots":1}`, "{4096 512 1 }"},
	} {
		p, err := parseProps(strings.NewReader(tc.body))
		if err != nil || fmt.Sprint(p) != tc.want {
			t.Errorf("%s: got %v %v, want %s", tc.body, p, err, tc.want)
		}
	}
	if _, err := parseProps(strings.NewReader(`{"model_path":"x"}`)); err == nil {
		t.Error("props without a context size should be an error")
	}
}

func TestParseSlots(t *testing.T) {
	// Trimmed from a real answer: next_token is a list on newer builds.
	newer := `[{"id":0,"n_ctx":200192,"is_processing":true,"id_task":124391,"n_prompt_tokens":103805,
		"n_prompt_tokens_processed":102781,"n_prompt_tokens_cache":0,"params":{"temperature":1.0},
		"next_token":[{"has_next_token":false,"n_remain":-1,"n_decoded":0}]}]`
	s, err := parseSlots(strings.NewReader(newer))
	if err != nil {
		t.Fatal(err)
	}
	want := Slot{ID: 0, TaskID: 124391, Processing: true, Prompt: 103805, Processed: 102781}
	if len(s) != 1 || s[0] != want {
		t.Errorf("got %+v", s)
	}

	older := `[{"id":1,"is_processing":true,"id_task":5,"n_prompt_tokens_processed":10,"next_token":{"n_decoded":3}}]`
	if s, err := parseSlots(strings.NewReader(older)); err != nil || s[0].Decoded != 3 {
		t.Errorf("older: %+v, %v", s, err)
	}

	if _, err := parseSlots(strings.NewReader(`[{"id":0,"is_processing":false}]`)); err == nil {
		t.Error("a build without the prompt fields should give an error")
	}
}
