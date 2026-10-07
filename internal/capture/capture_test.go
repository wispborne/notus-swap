package capture_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/capture"
	"github.com/wispborne/notus-swap/internal/proxy"
	"github.com/wispborne/notus-swap/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// setup puts notus-swap (capture + proxy) in front of a fake llama-swap.
func setup(t *testing.T, upstream http.Handler) (*httptest.Server, *store.Store) {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	return front(t, u)
}

func front(t *testing.T, u *url.URL) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := capture.NewHub()
	hub.Cmd = func(model string) string { return "cmd-" + model }
	srv := httptest.NewServer(capture.Handler(st, hub, proxy.New(u, quiet), quiet))
	t.Cleanup(srv.Close)
	return srv, st
}

// waitFinished polls until the request leaves the in_flight state. The row is
// written after the handler returns, which can be just after the client has
// read the whole response.
func waitFinished(t *testing.T, st *store.Store, id int64) *store.Record {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		r, err := st.Get(context.Background(), id)
		if err == nil && r.State != store.StateInFlight {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("request %d never finished (last err %v, record %+v)", id, err, r)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sse(w http.ResponseWriter, payload string) {
	fmt.Fprintf(w, "data: %s\n\n", payload)
	w.(http.Flusher).Flush()
}

func TestStreamPassesThroughLiveAndIsRecorded(t *testing.T) {
	release := make(chan struct{})
	srv, st := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"role":"assistant"}}],"system_fingerprint":"b11146-7fe450e19"}`)
		sse(w, `{"choices":[{"delta":{"reasoning_content":"thinking"}}],"system_fingerprint":"b11146-7fe450e19"}`)
		sse(w, `{"choices":[{"delta":{"content":"Hel"}}]}`)
		<-release // hold the stream until the client proves it got "Hel"
		sse(w, `{"choices":[{"delta":{"content":"lo"}}]}`)
		sse(w, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":3},"timings":{"prompt_ms":12.5,"predicted_ms":40,"prompt_per_second":400,"predicted_per_second":75}}`)
		sse(w, `[DONE]`)
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"qwen3-30b-a3b","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// If notus-swap buffered the stream, this read would block until the
	// timeout, because the upstream holds the rest back until release.
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("stream did not arrive live: %v", err)
		}
		if strings.Contains(line, `"Hel"`) {
			break
		}
	}
	close(release)
	rest, _ := io.ReadAll(br)
	if !strings.Contains(string(rest), "[DONE]") {
		t.Fatalf("stream did not finish: %q", rest)
	}

	r := waitFinished(t, st, 1)
	if r.State != store.StateDone || *r.StatusCode != 200 {
		t.Errorf("state %s, status %v", r.State, *r.StatusCode)
	}
	if r.Model != "qwen3-30b-a3b" || !r.Streaming {
		t.Errorf("model %q streaming %v", r.Model, r.Streaming)
	}
	if *r.PromptTokens != 5 || *r.CompletionTokens != 3 || *r.PredictedPerSecond != 75 || *r.PromptMs != 12.5 {
		t.Errorf("tokens/timings not read: %+v", r)
	}
	if r.Build == nil || *r.Build != "b11146-7fe450e19" {
		t.Errorf("build %v", r.Build)
	}
	if r.FirstByteAt == nil || r.FirstTokenAt == nil || r.FirstTokenAt.Before(*r.FirstByteAt) {
		t.Errorf("first byte %v, first token %v", r.FirstByteAt, r.FirstTokenAt)
	}
	if !strings.Contains(string(r.RequestBody), `"content":"hi"`) {
		t.Errorf("request body not stored: %q", r.RequestBody)
	}
	p := capture.Parse(r.ResponseBody, "text/event-stream")
	if p.Content != "Hello" || p.Reasoning != "thinking" {
		t.Errorf("stored response reads back as content %q reasoning %q", p.Content, p.Reasoning)
	}
}

func TestClientDisconnectMidStream(t *testing.T) {
	srv, st := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"one"}}]}`)
		<-r.Context().Done()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/v1/chat/completions", strings.NewReader(`{"model":"m","stream":true}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()

	r := waitFinished(t, st, 1)
	if r.State != store.StateClientGone {
		t.Errorf("state %s, want %s", r.State, store.StateClientGone)
	}
	if !strings.Contains(string(r.ResponseBody), "one") {
		t.Errorf("partial response not kept: %q", r.ResponseBody)
	}
}

// Codex hangs up as soon as it reads response.completed, sometimes before
// llama-server has closed the stream. The answer arrived in full, so the
// request is done, not client_gone. The same goes for [DONE].
func TestClientHangsUpAfterEndOfStream(t *testing.T) {
	for _, tc := range []struct{ path, last string }{
		{"/v1/responses", `{"type":"response.completed","response":{"status":"completed"}}`},
		{"/v1/chat/completions", `[DONE]`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			srv, st := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				sse(w, `{"choices":[{"delta":{"content":"one"}}]}`)
				sse(w, tc.last)
				<-r.Context().Done()
			}))
			ctx, cancel := context.WithCancel(context.Background())
			req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+tc.path, strings.NewReader(`{"model":"m","stream":true}`))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			br := bufio.NewReader(resp.Body)
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(line, tc.last) {
					break
				}
			}
			cancel()
			resp.Body.Close()

			r := waitFinished(t, st, 1)
			if r.State != store.StateDone {
				t.Errorf("state %s, want %s", r.State, store.StateDone)
			}
		})
	}
}

func TestLlamaSwapDownGives503AndIsRecorded(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(dead.URL)
	dead.Close()
	srv, st := front(t, u)

	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 503 || !strings.Contains(string(body), "llama-swap is not reachable") {
		t.Fatalf("got %d %s", resp.StatusCode, body)
	}
	r := waitFinished(t, st, 1)
	if r.State != store.StateFailed || *r.StatusCode != 503 || r.Error == nil {
		t.Errorf("state %s status %v error %v", r.State, *r.StatusCode, r.Error)
	}
}

func TestNonStreamingJSON(t *testing.T) {
	srv, st := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":1},"system_fingerprint":"b1-abc"}`)
	}))
	resp, err := http.Post(srv.URL+"/upstream/gemma-3-27b/v1/chat/completions", "application/json", strings.NewReader(`{"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	r := waitFinished(t, st, 1)
	if r.Model != "gemma-3-27b" {
		t.Errorf("model from /upstream path: %q", r.Model)
	}
	if *r.PromptTokens != 7 || *r.CompletionTokens != 1 || r.Streaming || r.Build == nil || *r.Build != "b1-abc" ||
		r.CmdHash == nil || *r.CmdHash != "cmd-gemma-3-27b" {
		t.Errorf("got %+v", r)
	}
	if r.Tags == nil || *r.Tags != `[{"kind":"not_streamed"}]` {
		t.Errorf("tags: %v", r.Tags)
	}
}

func TestOtherPathsAreNotRecorded(t *testing.T) {
	srv, st := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "llama-swap ui")
	}))
	for _, path := range []string{"/ui/", "/v1/models", "/upstream/qwen/index.html"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "llama-swap ui" {
			t.Fatalf("%s: passthrough body %q", path, body)
		}
	}
	if _, err := st.Get(context.Background(), 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a GET request was recorded: %v", err)
	}
}

func TestParseFallsBackToTimings(t *testing.T) {
	p := capture.Parse([]byte(`{"choices":[{"text":"abc"}],"timings":{"prompt_n":11,"predicted_n":4}}`), "application/json")
	if p.Content != "abc" || *p.PromptTokens() != 11 || *p.CompletionTokens() != 4 {
		t.Errorf("got %+v", p)
	}
}

func TestParseCachedTokens(t *testing.T) {
	p := capture.Parse([]byte(`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":80}},"timings":{"cache_n":1}}`), "application/json")
	if *p.PromptTokens() != 100 || *p.CachedTokens() != 80 {
		t.Errorf("from usage: prompt %d, cached %d", *p.PromptTokens(), *p.CachedTokens())
	}
	// Without usage, the prompt is the processed tokens plus the cached ones.
	p = capture.Parse([]byte(`{"timings":{"prompt_n":20,"cache_n":80}}`), "application/json")
	if *p.PromptTokens() != 100 || *p.CachedTokens() != 80 {
		t.Errorf("from timings: prompt %d, cached %d", *p.PromptTokens(), *p.CachedTokens())
	}
}

// responsesStream is a Responses API stream shaped like llama-server's: a
// reasoning item, a message, and a tool call, each streamed as deltas, then
// the complete response in response.completed.
var responsesStream = strings.Join([]string{
	`event: response.created`,
	`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress"}}`,
	`data: {"type":"response.output_item.added","item":{"id":"rs_1","summary":[],"type":"reasoning","content":[],"status":"in_progress"}}`,
	`data: {"type":"response.reasoning_text.delta","delta":"Let me ","item_id":"rs_1"}`,
	`data: {"type":"response.reasoning_text.delta","delta":"look.","item_id":"rs_1"}`,
	`data: {"type":"response.content_part.added","item_id":"msg_1","part":{"type":"output_text","text":""}}`,
	`data: {"type":"response.output_text.delta","item_id":"msg_1","delta":"Hel"}`,
	`data: {"type":"response.output_text.delta","item_id":"msg_1","delta":"lo"}`,
	`data: {"type":"response.output_item.added","item":{"id":"fc_1","arguments":"","call_id":"call_1","name":"exec_command","type":"function_call","status":"in_progress"}}`,
	`data: {"type":"response.function_call_arguments.delta","delta":"{\"cmd\":","item_id":"fc_1"}`,
	`data: {"type":"response.function_call_arguments.delta","delta":"\"ls\"}","item_id":"fc_1"}`,
	`data: {"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","status":"completed","arguments":"{\"cmd\":\"ls\"}","call_id":"call_1","name":"exec_command"}}`,
	`event: response.completed`,
	`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","output":[` +
		`{"id":"rs_1","summary":[],"type":"reasoning","content":[{"text":"Let me look.","type":"reasoning_text"}]},` +
		`{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello"}]},` +
		`{"id":"fc_1","type":"function_call","status":"completed","arguments":"{\"cmd\":\"ls\"}","call_id":"call_1","name":"exec_command"}],` +
		`"usage":{"input_tokens":100,"output_tokens":7,"total_tokens":107,"input_tokens_details":{"cached_tokens":80}}},` +
		`"timings":{"cache_n":80,"prompt_n":20,"prompt_ms":40,"prompt_per_second":500,"predicted_n":7,"predicted_ms":100,"predicted_per_second":70}}`,
}, "\n\n")

func TestParseResponsesStream(t *testing.T) {
	check := func(name string, p capture.Parsed) {
		t.Helper()
		if p.Content != "Hello" || p.Reasoning != "Let me look." {
			t.Errorf("%s: content %q reasoning %q", name, p.Content, p.Reasoning)
		}
		if len(p.ToolCalls) != 1 || p.ToolCalls[0].Name != "exec_command" || p.ToolCalls[0].Arguments != `{"cmd":"ls"}` || p.ToolCalls[0].ID != "call_1" {
			t.Errorf("%s: tool calls %+v", name, p.ToolCalls)
		}
	}
	p := capture.ParseStored([]byte(responsesStream))
	check("whole stream", p)
	if p.FinishReason != "tool_calls" {
		t.Errorf("finish reason %q", p.FinishReason)
	}
	if *p.PromptTokens() != 100 || *p.CachedTokens() != 80 || *p.CompletionTokens() != 7 {
		t.Errorf("tokens: prompt %d cached %d output %d", *p.PromptTokens(), *p.CachedTokens(), *p.CompletionTokens())
	}

	// Before response.completed, the deltas alone give the output so far.
	partial, _, _ := strings.Cut(responsesStream, `event: response.completed`)
	check("before the end", capture.ParseStored([]byte(partial)))
}

func TestParseResponsesJSON(t *testing.T) {
	p := capture.Parse([]byte(`{"id":"resp_1","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},`+
		`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"It was a"}]}],`+
		`"usage":{"input_tokens":10,"output_tokens":50}}`), "application/json")
	if p.Content != "It was a" || p.FinishReason != "length" || *p.PromptTokens() != 10 || *p.CompletionTokens() != 50 {
		t.Errorf("got %+v", p)
	}
}

// Anthropic's usage counts cached prompt tokens apart from input_tokens.
func TestParseAnthropicUsage(t *testing.T) {
	p := capture.Parse([]byte(`{"usage":{"cache_read_input_tokens":80,"input_tokens":20,"output_tokens":5}}`), "application/json")
	if *p.PromptTokens() != 100 || *p.CachedTokens() != 80 || *p.CompletionTokens() != 5 {
		t.Errorf("prompt %d cached %d output %d", *p.PromptTokens(), *p.CachedTokens(), *p.CompletionTokens())
	}
}

func TestResponsesStreamIsRecorded(t *testing.T) {
	srv, st := setup(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, part := range strings.Split(responsesStream, "\n\n") {
			fmt.Fprintf(w, "%s\n\n", part)
			w.(http.Flusher).Flush()
		}
	}))
	resp, err := http.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"m","stream":true,"instructions":"Be brief.",`+
		`"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>"}]},`+
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"list the files"}]},`+
		`{"type":"function_call","name":"exec_command","arguments":"{}","call_id":"call_0"},`+
		`{"type":"function_call_output","call_id":"call_0","output":"a.txt"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()

	r := waitFinished(t, st, 1)
	if r.State != store.StateDone || r.Preview != "list the files" {
		t.Errorf("state %s, preview %q", r.State, r.Preview)
	}
	if r.FirstTokenAt == nil || r.PromptTokens == nil || *r.PromptTokens != 100 || r.FinishReason == nil || *r.FinishReason != "tool_calls" {
		t.Errorf("first token %v, prompt tokens %v, finish reason %v", r.FirstTokenAt, r.PromptTokens, r.FinishReason)
	}
}

// Responses API events don't name the server's build, so it comes from the
// build the model's llama-server reported in /props.
func TestBuildFromModelProps(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, responsesStream)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.SaveModelProps(ctx, []string{"m"}, store.ModelProps{NCtx: 8192, Build: "b11146-7fe450e19"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	hub := capture.NewHub()
	hub.Props = func(model string) *store.ModelProps { p, _ := st.GetModelProps(ctx, model); return p }
	srv := httptest.NewServer(capture.Handler(st, hub, proxy.New(u, quiet), quiet))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"m","stream":true,"input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	r := waitFinished(t, st, 1)
	if r.Build == nil || *r.Build != "b11146-7fe450e19" {
		t.Errorf("build %v", r.Build)
	}
}

func TestParseToolCallsFromStream(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Tokyo\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}, "\n\n")
	p := capture.ParseStored([]byte(body))
	if len(p.ToolCalls) != 1 || p.ToolCalls[0].Name != "get_weather" || p.ToolCalls[0].Arguments != `{"city":"Tokyo"}` || p.ToolCalls[0].ID != "c1" {
		t.Errorf("tool calls: %+v", p.ToolCalls)
	}
	if p.FinishReason != "tool_calls" {
		t.Errorf("finish reason %q", p.FinishReason)
	}
}

func TestAccountGetsTimesFromTimings(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"content":"hi"}}],"timings":{"prompt_ms":1000,"predicted_ms":2000}}`)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := capture.NewHub()
	got := make(chan store.Work, 1)
	hub.Account = func(w store.Work) (*float64, time.Duration) {
		got <- w
		j := 42.0
		return &j, 1500 * time.Millisecond
	}
	srv := httptest.NewServer(capture.Handler(st, hub, proxy.New(u, quiet), quiet))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	r := waitFinished(t, st, 1)
	w := <-got
	// The answer came back at once, so its 3 s of timings reach back to
	// when it arrived, and no further.
	if !w.Began.Equal(w.Arrived) || w.Ended.Before(w.Arrived) {
		t.Errorf("work %+v", w)
	}
	if r.EnergyJ == nil || *r.EnergyJ != 42 || r.QueuedMs == nil || *r.QueuedMs != 1500 {
		t.Errorf("energy %v, queued %v", r.EnergyJ, r.QueuedMs)
	}
}

func TestClientIP(t *testing.T) {
	for _, c := range []struct {
		remote, xff, real, want string
	}{
		{"192.0.2.5:4123", "", "", "192.0.2.5"},
		{"192.0.2.5:4123", "198.51.100.7, 192.0.2.9", "", "198.51.100.7"},
		{"192.0.2.5:4123", "", "198.51.100.8", "198.51.100.8"},
		{"[2001:db8::1]:80", "", "", "2001:db8::1"},
		{"", "", "", ""},
	} {
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.real != "" {
			r.Header.Set("X-Real-IP", c.real)
		}
		if got := capture.ClientIP(r); got != c.want {
			t.Errorf("ClientIP(%q, xff %q, real %q) = %q, want %q", c.remote, c.xff, c.real, got, c.want)
		}
	}
}
