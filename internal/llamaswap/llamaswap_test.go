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
		{`{"default_generation_settings":{"n_ctx":8192,"params":{"n_predict":-1}},"total_slots":2,"build_info":"b11146-7fe450e19"}`, "8192 -1 2 b11146-7fe450e19"},
		{`{"default_generation_settings":{"n_ctx":4096,"n_predict":512},"total_slots":1}`, "4096 512 1 "},
	} {
		p, err := parseProps(strings.NewReader(tc.body))
		if err != nil || fmt.Sprintf("%d %d %d %s", p.NCtx, p.NPredict, p.Slots, p.Build) != tc.want {
			t.Errorf("%s: got %v %v, want %s", tc.body, p, err, tc.want)
		}
	}
	if _, err := parseProps(strings.NewReader(`{"model_path":"x"}`)); err == nil {
		t.Error("props without a context size should be an error")
	}
}

func TestParsePropsCaps(t *testing.T) {
	for _, tc := range []struct {
		body string
		want Caps
	}{
		{`{"default_generation_settings":{"n_ctx":8192},"modalities":{"vision":true,"audio":false},"chat_template":"{{ message.reasoning_content }}","chat_template_caps":{"supports_tool_calls":true}}`,
			Caps{Vision: true, Thinking: true, Template: map[string]bool{"supports_tool_calls": true}}},
		{`{"default_generation_settings":{"n_ctx":8192},"chat_template_caps":{"supports_reasoning_effort":true}}`,
			Caps{Thinking: true, Template: map[string]bool{"supports_reasoning_effort": true}}},
		// Older llama-server versions send no modalities or template caps.
		{`{"default_generation_settings":{"n_ctx":8192},"chat_template":"{{ message.content }}"}`, Caps{}},
	} {
		p, err := parseProps(strings.NewReader(tc.body))
		if err != nil || fmt.Sprint(p.Caps) != fmt.Sprint(tc.want) {
			t.Errorf("%s: got %+v %v, want %+v", tc.body, p.Caps, err, tc.want)
		}
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
	if len(s) != 1 || s[0].Cache == nil || *s[0].Cache != 0 {
		t.Fatalf("got %+v", s)
	}
	if s[0].Cache = nil; s[0] != want {
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

// A Strata model through llama-swap: /slots lacks the prompt count, so it
// comes from Strata's /metrics.
func TestFetchSlotsFromStrata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upstream/qwen/slots":
			w.Write([]byte(`[{"id": 0, "n_ctx": 131072, "is_processing": true, "n_prompt_tokens": 611}]`))
		case "/upstream/qwen/metrics":
			if r.Header.Get("Accept") != "application/json" {
				w.Write([]byte("# HELP vllm:num_requests_running"))
				return
			}
			w.Write([]byte(`{"live": {"state": "reading", "prompt_tokens": 900, "prompt_read": 512, "prompt_total": 900}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	m := NewMonitor(u)
	s, err := m.FetchSlots(context.Background(), "qwen")
	if err != nil || len(s) != 1 || s[0].Processed != 512 || s[0].Total != 900 {
		t.Errorf("slots: %+v, %v", s, err)
	}
}

func TestParseStrataSlots(t *testing.T) {
	// Trimmed from Strata 0.1.54's /metrics with --batch 3: one request reads
	// its prompt on its own, one slot generates, one slot reads.
	answer := func(total string) string {
		return `{"engine": {}, "live": {"state": "reading", "phase": "reading the prompt", "prompt_tokens": 34302,
			"prompt_read": 8192, "prompt_total": ` + total + `, "generated": 0, "elapsed_s": 3.1, "parallel": 3, "running": 3,
			"slots": [{"slot": 0, "state": "idle", "held_tokens": 611},
				{"slot": 1, "state": "generating", "prompt_tokens": 900, "prompt_read": null, "generated": 12},
				{"slot": 2, "state": "reading", "prompt_tokens": 5000, "prompt_read": 8192, "generated": 0}]}}`
	}
	s, err := parseStrataSlots(strings.NewReader(answer("34302")))
	if err != nil {
		t.Fatal(err)
	}
	want := []Slot{
		{ID: -1, Processing: true, Total: 34302, Processed: 8192},
		{ID: 1, Processing: true, Decoded: 12},
		// The counter is never a slot's own.
		{ID: 2, Processing: true, Total: 5000, SizeOnly: true},
	}
	if fmt.Sprint(s) != fmt.Sprint(want) {
		t.Errorf("got %+v", s)
	}

	// The counter is another request's: only the size is known.
	s, _ = parseStrataSlots(strings.NewReader(answer("116598")))
	if !s[0].SizeOnly || s[0].Total != 34302 {
		t.Errorf("got %+v", s[0])
	}

	// Seen with --batch 3 while another request generated: the request in
	// "live" is in slot 1 too. It is listed once.
	s, _ = parseStrataSlots(strings.NewReader(`{"live": {"state": "reading", "prompt_tokens": 38572, "prompt_read": 8192,
		"prompt_total": 38572, "slots": [{"slot": 0, "state": "generating", "prompt_tokens": 120000, "generated": 541},
		{"slot": 1, "state": "reading", "prompt_tokens": 38572, "prompt_read": 8192, "generated": 0}]}}`))
	if fmt.Sprint(s) != fmt.Sprint([]Slot{{ID: -1, Processing: true, Total: 38572, Processed: 8192}, {ID: 0, Processing: true, Decoded: 541}}) {
		t.Errorf("got %+v", s)
	}

	// A generating request whose first token isn't counted yet still counts as generating.
	s, _ = parseStrataSlots(strings.NewReader(`{"live": {"state": "generating", "generated": 0}}`))
	if len(s) != 1 || s[0].Decoded != 1 {
		t.Errorf("got %+v", s)
	}

	// llama-server's Prometheus /metrics isn't JSON.
	if _, err := parseStrataSlots(strings.NewReader("# HELP llamacpp:prompt_tokens_total")); err == nil {
		t.Error("a non-JSON answer should give an error")
	}
	if _, err := parseStrataSlots(strings.NewReader(`{"other": 1}`)); err == nil {
		t.Error("JSON without Strata's live block should give an error")
	}
}

func TestParseStrata(t *testing.T) {
	// Trimmed from Strata 0.1.54's /metrics.
	body := `{"engine": {"model": "qwen", "max_context": 131072, "images": true, "version": "0.1.54", "kv": "int8",
		"expert_cache_mib": 7671, "spec": 6, "pcie_frac": "0.55", "vram_free_mib": 305, "drafting": "mtp", "drafting_note": ""},
		"live": {"state": "idle"},
		"requests": [{"id": "67575f0929a1", "time": 1793397970.753632, "duration_s": 24.4, "finish": "stop", "prompt_tokens": 103100,
			"output_tokens": 1718, "first_engine_token_s": 1.559, "reasoning_recoveries": 0, "reasoning_repeat_coverage": 0.0,
			"hit_rate": 0.843, "pcie_share": 0.107, "file_mb": 0, "drafts_offered": 1583}]}`
	s, err := parseStrata(strings.NewReader(body))
	if err != nil || !s.Loaded() || len(s.Requests) != 1 || *s.Requests[0].HitRate != 0.843 || s.Requests[0].OutputTokens != 1718 {
		t.Fatalf("got %+v, %v", s, err)
	}
	want := []string{"--strata.drafting=mtp", "--strata.expert_cache_mib=7671", "--strata.images=true", "--strata.kv=int8",
		"--strata.max_context=131072", "--strata.pcie_frac=0.55", "--strata.spec=6"}
	if got := s.SettingArgs(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("args %v", got)
	}

	// Before the engine loads, only three fields are known.
	s, _ = parseStrata(strings.NewReader(`{"engine": {"model": "qwen", "max_context": 131072, "images": true}, "requests": []}`))
	if s.Loaded() {
		t.Error("an engine without a version counts as loaded")
	}
	if _, err := parseStrata(strings.NewReader(`{"live": {}}`)); err == nil {
		t.Error("JSON without an engine block should give an error")
	}
}
