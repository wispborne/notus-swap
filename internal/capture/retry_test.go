package capture_test

import (
	"bufio"
	"io"
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

// frontWithHub is front, but also returns the hub and the capture handler.
func frontWithHub(t *testing.T, upstream http.Handler) (*httptest.Server, *store.Store, *capture.Hub, http.Handler) {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := capture.NewHub()
	h := capture.Handler(st, hub, proxy.New(u, quiet), quiet)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, st, hub, h
}

func TestCancelMidStream(t *testing.T) {
	upstreamDone := make(chan struct{})
	srv, st, hub, _ := frontWithHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"one"}}]}`)
		<-r.Context().Done()
		close(upstreamDone)
	}))
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	if _, err := rd.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if !hub.Cancel(1) {
		t.Fatal("Cancel found no request in flight")
	}
	select {
	case <-upstreamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("the request to llama-swap was not ended")
	}
	// The client's stream ends.
	io.ReadAll(rd)
	r := waitFinished(t, st, 1)
	if r.State != store.StateCancelled {
		t.Errorf("state %s, want %s", r.State, store.StateCancelled)
	}
	if !strings.Contains(string(r.ResponseBody), "one") {
		t.Errorf("partial response not kept: %q", r.ResponseBody)
	}
	if hub.Cancel(1) {
		t.Error("Cancel found a finished request")
	}
}

func TestCancelBeforeAnswer(t *testing.T) {
	srv, st, hub, _ := frontWithHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The test server only notices a closed connection once the body is read.
		io.ReadAll(r.Body)
		<-r.Context().Done()
	}))
	go func() {
		for hub.Count() == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		hub.Cancel(1)
	}()
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 499 || !strings.Contains(string(body), "cancelled") {
		t.Errorf("client got %d %s, want 499 with an error", resp.StatusCode, body)
	}
	r := waitFinished(t, st, 1)
	if r.State != store.StateCancelled || r.StatusCode == nil || *r.StatusCode != 499 {
		t.Errorf("state %s, status %v", r.State, r.StatusCode)
	}
}

func TestRetry(t *testing.T) {
	var got []string
	srv, st, _, h := frontWithHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.URL.Path+" "+r.Header.Get("Content-Type")+" "+string(b))
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, `{"choices":[{"delta":{"content":"hello"}}]}`)
		sse(w, `[DONE]`)
	}))
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	first := waitFinished(t, st, 1)

	id, err := capture.Retry(h, first.Method, first.Path, first.RequestBody, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if id != 2 {
		t.Fatalf("new ID %d, want 2", id)
	}
	r := waitFinished(t, st, id)
	if r.State != store.StateDone || !strings.Contains(string(r.ResponseBody), "hello") {
		t.Errorf("retry state %s, response %q", r.State, r.ResponseBody)
	}
	if r.RetryOf == nil || *r.RetryOf != 1 {
		t.Errorf("retry_of %v, want 1", r.RetryOf)
	}
	if first.RetryOf != nil {
		t.Errorf("first request has retry_of %v", *first.RetryOf)
	}
	want := `/v1/chat/completions application/json {"model":"m","stream":true}`
	if len(got) != 2 || got[1] != want {
		t.Errorf("llama-swap got %q, want the second to be %q", got, want)
	}
}
