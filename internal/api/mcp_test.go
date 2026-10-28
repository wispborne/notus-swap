package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wispborne/notus-swap/internal/api"
	"github.com/wispborne/notus-swap/internal/capture"
	"github.com/wispborne/notus-swap/internal/gpu"
	"github.com/wispborne/notus-swap/internal/llamaswap"
	"github.com/wispborne/notus-swap/internal/metrics"
	"github.com/wispborne/notus-swap/internal/store"
)

// mcpServer serves the API over a database holding one finished request for
// "qwen" and one for "secret", which is hidden.
func mcpServer(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	t0 := time.Now().Add(-time.Minute)
	for _, model := range []string{"qwen", "secret"} {
		id, err := st.Begin(ctx, store.Begin{StartedAt: t0, Method: "POST", Path: "/v1/chat/completions", Model: model, Preview: "hello",
			RequestBody: []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"hello"}]}`)})
		if err != nil {
			t.Fatal(err)
		}
		answer := strings.Repeat("a", 500) + strings.Repeat("z", 500)
		n := int64(1000)
		if err := st.Finish(ctx, id, store.Finish{FirstTokenAt: t0.Add(200 * time.Millisecond), FinishedAt: t0.Add(2 * time.Second), State: store.StateDone,
			StatusCode: 200, CompletionTokens: &n, ResponseBody: []byte(`{"choices":[{"message":{"content":"` + answer + `"},"finish_reason":"stop"}]}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetSetting(ctx, "hidden_models", `["secret"]`); err != nil {
		t.Fatal(err)
	}
	rec := &metrics.Recorder{Store: st, GPUs: gpu.NewSampler(t.TempDir()), Log: log}
	u, _ := url.Parse("http://127.0.0.1:1")
	mux := http.NewServeMux()
	(&api.API{Store: st, Hub: capture.NewHub(), LlamaSwap: llamaswap.NewMonitor(u), Metrics: rec, Upstream: u, Log: log, Version: "test"}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestMCPTools(t *testing.T) {
	base := mcpServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/notus/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
		if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tl.Name)
		}
	}
	for _, want := range []string{"list_requests", "get_request", "current_status", "model_stats", "model_events", "model_speeds", "hardware_history"} {
		if !slices.Contains(names, want) {
			t.Errorf("no %s tool, got %v", want, names)
		}
	}
	if slices.Contains(names, "query_sql") {
		t.Error("query_sql is offered while the SQL API is off")
	}

	call := func(name string, args map[string]any, out any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out != nil && !res.IsError {
			b, _ := json.Marshal(res.StructuredContent)
			if err := json.Unmarshal(b, out); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		return res
	}

	var list struct {
		Requests []struct {
			ID     int64  `json:"id"`
			Model  string `json:"model"`
			TTFTMs int64  `json:"ttft_ms"`
		} `json:"requests"`
	}
	call("list_requests", map[string]any{"since": time.Now().Add(-time.Hour).Format(time.RFC3339)}, &list)
	if len(list.Requests) != 1 || list.Requests[0].Model != "qwen" || list.Requests[0].TTFTMs != 200 {
		t.Errorf("list_requests should show only qwen, with a 200 ms TTFT: %+v", list.Requests)
	}

	var one struct {
		Answer string `json:"answer"`
	}
	call("get_request", map[string]any{"id": 1, "max_chars": 100}, &one)
	if !strings.HasPrefix(one.Answer, strings.Repeat("a", 50)) || !strings.HasSuffix(one.Answer, strings.Repeat("z", 50)) || !strings.Contains(one.Answer, "900 characters cut") {
		t.Errorf("answer not cut in the middle: %q", one.Answer)
	}
	if res := call("get_request", map[string]any{"id": 2}, nil); !res.IsError {
		t.Error("get_request showed a hidden model's request")
	}
	if res := call("model_events", map[string]any{"model": "secret"}, nil); !res.IsError {
		t.Error("model_events answered for a hidden model")
	}

	var stats struct {
		Stats []struct {
			Model string `json:"model"`
		} `json:"stats"`
	}
	call("model_stats", nil, &stats)
	if len(stats.Stats) != 1 || stats.Stats[0].Model != "qwen" {
		t.Errorf("model_stats should list only qwen: %+v", stats.Stats)
	}
	for _, name := range []string{"current_status", "hardware_history"} {
		if res := call(name, nil, nil); res.IsError {
			t.Errorf("%s failed: %+v", name, res.Content)
		}
	}
	if res := call("hardware_history", map[string]any{"range": "2h"}, nil); !res.IsError {
		t.Error("hardware_history took an unknown range")
	}
}

func TestMCPRefusesBrowsersAndGets(t *testing.T) {
	base := mcpServer(t)
	req, _ := http.NewRequest("POST", base+"/notus/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a cross-site POST got %d, want 403", resp.StatusCode)
	}
	resp, err = http.Get(base + "/notus/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET got %d, want 405", resp.StatusCode)
	}
}

func TestMCPQuerySQL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ro, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	mux := http.NewServeMux()
	(&api.API{Store: st, ReadOnly: ro, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL + "/notus/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "query_sql", Arguments: map[string]any{"sql": "SELECT 1 + 1 AS two"}})
	if err != nil || res.IsError {
		t.Fatalf("query_sql: %v %+v", err, res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), `"rows":[[2]]`) {
		t.Errorf("query_sql answered %s", b)
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "query_sql", Arguments: map[string]any{"sql": "DELETE FROM requests"}})
	if err != nil || !res.IsError {
		t.Errorf("query_sql ran a DELETE: %v %+v", err, res)
	}
}
