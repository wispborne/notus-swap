package api_test

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/api"
	"github.com/wispborne/notus-swap/internal/capture"
	"github.com/wispborne/notus-swap/internal/gpu"
	"github.com/wispborne/notus-swap/internal/llamaswap"
	"github.com/wispborne/notus-swap/internal/metrics"
	"github.com/wispborne/notus-swap/internal/proxy"
	"github.com/wispborne/notus-swap/internal/store"
)

// A full notus-swap (proxy, capture, API) in front of a fake llama-swap
// that streams two chunks, then waits for release before finishing.
func TestLiveFeedListAndDetail(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range []string{`{"choices":[{"delta":{"reasoning_content":"hmm"}}]}`, `{"choices":[{"delta":{"content":"Hi "}}]}`} {
			fmt.Fprintf(w, "data: %s\n\n", c)
			w.(http.Flusher).Flush()
		}
		<-release
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"there\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := capture.NewHub()
	mux := http.NewServeMux()
	rec := &metrics.Recorder{Store: st, GPUs: gpu.NewSampler(t.TempDir()), Log: log}
	rec.MemInfoPath = filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(rec.MemInfoPath, []byte("MemTotal: 8192 kB\nMemAvailable: 3072 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec.Sample(context.Background(), time.Now())
	(&api.API{Store: st, Hub: hub, LlamaSwap: llamaswap.NewMonitor(u), Metrics: rec, Upstream: u, Log: log, Version: "test"}).Register(mux)
	mux.Handle("/", capture.Handler(st, hub, proxy.New(u, log), log))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Subscribe to the live feed first.
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/notus/api/live", nil)
	liveResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer liveResp.Body.Close()
	events := make(chan map[string]any, 100)
	go func() {
		sc := bufio.NewScanner(liveResp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var e map[string]any
				json.Unmarshal([]byte(data), &e)
				events <- e
			}
		}
		close(events)
	}()
	next := func(want string) map[string]any {
		t.Helper()
		for {
			select {
			case e, ok := <-events:
				if !ok {
					t.Fatalf("live feed closed while waiting for %q", want)
				}
				if e["type"] == want {
					return e
				}
			case <-ctx.Done():
				t.Fatalf("no %q event", want)
			}
		}
	}
	next("hello")

	// Send a chat request through the proxy, in the background.
	done := make(chan struct{})
	go func() {
		defer close(done)
		r, err := http.Post(srv.URL+"/v1/chat/completions", "application/json",
			strings.NewReader(`{"model":"qwen","stream":true,"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"say   hi\nplease"}]}`))
		if err == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
		}
	}()

	start := next("start")
	if s := start["start"].(map[string]any); s["preview"] != "say hi please" || s["model"] != "qwen" {
		t.Errorf("start event: %v", s)
	}
	if d := next("delta"); d["reasoning"] != "hmm" {
		t.Errorf("first delta: %v", d)
	}
	if d := next("delta"); d["content"] != "Hi " {
		t.Errorf("second delta: %v", d)
	}

	// While in flight, the detail shows the output so far.
	var d api.Detail
	getJSON(t, srv.URL+"/notus/api/requests/1", &d)
	if !d.Live || d.Response.Content != "Hi " || d.Response.Reasoning != "hmm" || d.Chunks != 2 {
		t.Errorf("in-flight detail: live %v content %q reasoning %q chunks %d", d.Live, d.Response.Content, d.Response.Reasoning, d.Chunks)
	}

	close(release)
	next("finish")
	<-done

	// Once finished, the detail comes from the store.
	getJSON(t, srv.URL+"/notus/api/requests/1", &d)
	if d.Live || d.State != store.StateDone || d.Response.Content != "Hi there" || *d.CompletionTokens != 3 {
		t.Errorf("finished detail: %+v", d)
	}
	if !strings.Contains(d.ResponseRaw, "[DONE]") {
		t.Errorf("raw response missing: %q", d.ResponseRaw)
	}
	var sent map[string]any
	if json.Unmarshal(d.Request, &sent) != nil || sent["model"] != "qwen" {
		t.Errorf("request not returned as JSON: %s", d.Request)
	}

	var list struct {
		Requests []api.Summary `json:"requests"`
		Models   []string      `json:"models"`
	}
	getJSON(t, srv.URL+"/notus/api/requests?q=hi", &list)
	if len(list.Requests) != 1 || list.Requests[0].Preview != "say hi please" || len(list.Models) != 1 {
		t.Errorf("list: %+v", list)
	}
	getJSON(t, srv.URL+"/notus/api/requests?q=nothing", &list)
	if len(list.Requests) != 0 {
		t.Errorf("search matched too much: %+v", list.Requests)
	}

	var st2 api.Status
	getJSON(t, srv.URL+"/notus/api/status", &st2)
	if st2.Version != "test" || st2.InFlight != 0 || st2.GPUs == nil || st2.TotalWatts != nil ||
		st2.RAMUsed == nil || *st2.RAMUsed != 5120*1024 || st2.RAMTotal == nil || *st2.RAMTotal != 8192*1024 {
		t.Errorf("status: %+v", st2)
	}

	var dash api.Dashboard
	getJSON(t, srv.URL+"/notus/api/dashboard?range=15m", &dash)
	if len(dash.Requests) != 1 || dash.Requests[0].Model != "qwen" || dash.BucketS < 1 {
		t.Errorf("dashboard: %+v", dash)
	}
	// Sent gzipped when the client accepts it. Setting the header by hand
	// stops Go's client from unzipping the answer itself.
	gzReq, _ := http.NewRequest("GET", srv.URL+"/notus/api/dashboard?range=15m", nil)
	gzReq.Header.Set("Accept-Encoding", "gzip")
	if resp, err := http.DefaultClient.Do(gzReq); err != nil {
		t.Fatal(err)
	} else {
		defer resp.Body.Close()
		zr, err := gzip.NewReader(resp.Body)
		var unzipped api.Dashboard
		if resp.Header.Get("Content-Encoding") != "gzip" || err != nil || json.NewDecoder(zr).Decode(&unzipped) != nil || len(unzipped.Requests) != 1 {
			t.Errorf("gzipped dashboard: %v %v %+v", resp.Header, err, unzipped)
		}
	}

	// Settings: unset is null; a PUT is read back.
	var layout any
	getJSON(t, srv.URL+"/notus/api/settings/dashboard_layout", &layout)
	if layout != nil {
		t.Errorf("unset layout: %v", layout)
	}
	put, _ := http.NewRequest("PUT", srv.URL+"/notus/api/settings/dashboard_layout", strings.NewReader(`{"widgets":[1]}`))
	if resp, err := http.DefaultClient.Do(put); err != nil || resp.StatusCode != 204 {
		t.Fatalf("put layout: %v %v", resp, err)
	}
	getJSON(t, srv.URL+"/notus/api/settings/dashboard_layout", &layout)
	if m, ok := layout.(map[string]any); !ok || len(m["widgets"].([]any)) != 1 {
		t.Errorf("layout read back: %v", layout)
	}

	// Hidden models show up in the status for every open page.
	getJSON(t, srv.URL+"/notus/api/status", &st2)
	if st2.Privacy.ShowHidden || len(st2.Privacy.HiddenModels) != 0 {
		t.Errorf("default privacy: %+v", st2.Privacy)
	}
	for key, body := range map[string]string{"hidden_models": `["qwen"]`, "show_hidden": `true`} {
		put, _ := http.NewRequest("PUT", srv.URL+"/notus/api/settings/"+key, strings.NewReader(body))
		if resp, err := http.DefaultClient.Do(put); err != nil || resp.StatusCode != 204 {
			t.Fatalf("put %s: %v %v", key, resp, err)
		}
	}
	getJSON(t, srv.URL+"/notus/api/status", &st2)
	if !st2.Privacy.ShowHidden || len(st2.Privacy.HiddenModels) != 1 || st2.Privacy.HiddenModels[0] != "qwen" {
		t.Errorf("privacy after set: %+v", st2.Privacy)
	}
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		b, _ := io.ReadAll(r.Body)
		t.Fatalf("%s: %d %s", url, r.StatusCode, b)
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}
