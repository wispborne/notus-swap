package capture_test

import (
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

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestIdleClosesWhenTheLastRequestFinishes(t *testing.T) {
	release := make(chan struct{})
	arrived := make(chan struct{}, 2)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-release
		w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := capture.NewHub()
	srv := httptest.NewServer(capture.Handler(st, hub, proxy.New(u, quiet), quiet))
	t.Cleanup(srv.Close)

	if !closed(hub.Idle()) {
		t.Fatal("a new hub should be idle")
	}
	done := make(chan struct{}, 2)
	for range 2 {
		go func() {
			resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m"}`))
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			done <- struct{}{}
		}()
	}
	<-arrived
	<-arrived
	idle := hub.Idle()
	if closed(idle) || hub.Count() != 2 {
		t.Fatalf("idle with %d requests in flight", hub.Count())
	}
	close(release)
	select {
	case <-idle:
	case <-time.After(3 * time.Second):
		t.Fatal("idle never closed after both requests finished")
	}
	if hub.Count() != 0 {
		t.Fatalf("idle closed with %d requests in flight", hub.Count())
	}
	<-done
	<-done
}
