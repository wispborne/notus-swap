package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/api"
	"github.com/wispborne/notus-swap/internal/store"
)

func TestIndexListsEndpoints(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mux := http.NewServeMux()
	(&api.API{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Version: "test"}).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var idx struct {
		Version   string         `json:"version"`
		Docs      string         `json:"docs"`
		Endpoints []api.Endpoint `json:"endpoints"`
	}
	getJSON(t, srv.URL+"/notus/api/", &idx)
	if idx.Version != "test" || idx.Docs != api.DocsURL {
		t.Errorf("version %q, docs %q", idx.Version, idx.Docs)
	}
	found := map[string]api.Endpoint{}
	for _, e := range idx.Endpoints {
		if e.About == "" {
			t.Errorf("%s %s has no description", e.Method, e.Path)
		}
		found[e.Method+" "+e.Path] = e
	}
	for _, want := range []string{"GET /notus/api/", "GET /notus/api/requests", "GET /notus/api/requests/{id}", "GET /notus/api/dashboard", "GET /notus/api/logs", "POST /notus/api/llama-updates/{component}/{action}"} {
		if _, ok := found[want]; !ok {
			t.Errorf("%s is not listed", want)
		}
	}
	if q := found["POST /notus/api/query"]; q.Off == "" {
		t.Error("the SQL endpoint should be marked off when it isn't turned on")
	}
	if found["GET /notus/api/requests"].Off != "" {
		t.Error("the requests endpoint should not be marked off")
	}
}

func TestRequestsSinceUntil(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		if _, err := st.Begin(context.Background(), store.Begin{StartedAt: t0.Add(time.Duration(i) * time.Hour), Method: "POST", Path: "/v1/chat/completions", Model: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	(&api.API{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ms := func(h int) string { return strconv.FormatInt(t0.Add(time.Duration(h)*time.Hour).UnixMilli(), 10) }
	var out struct {
		Requests []api.Summary `json:"requests"`
	}
	getJSON(t, srv.URL+"/notus/api/requests?since="+ms(1)+"&until="+ms(1), &out)
	if len(out.Requests) != 1 || out.Requests[0].ID != 2 {
		t.Errorf("since and until the second request: got %+v", out.Requests)
	}
	getJSON(t, srv.URL+"/notus/api/requests?since="+ms(1), &out)
	if len(out.Requests) != 2 {
		t.Errorf("since the second request: got %d requests, want 2", len(out.Requests))
	}
}
