package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSystemdSockets(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	cases := []struct {
		vars map[string]string
		want int
	}{
		{map[string]string{}, 0},
		{map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "1"}, 1},
		{map[string]string{"LISTEN_PID": "7", "LISTEN_FDS": "1"}, 0}, // meant for another process
		{map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "x"}, 0},
		{map[string]string{"LISTEN_FDS": "1"}, 0},
	}
	for _, c := range cases {
		if got := systemdSockets(env(c.vars), 42); got != c.want {
			t.Errorf("%v: got %d, want %d", c.vars, got, c.want)
		}
	}
}

func TestEndOnShutdownEndsGetsOnly(t *testing.T) {
	shuttingDown, end := context.WithCancel(context.Background())
	started := make(chan string, 2)
	srv := httptest.NewServer(endOnShutdown(shuttingDown, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		started <- r.Method
		select {
		case <-r.Context().Done():
			io.WriteString(w, "ended")
		case <-time.After(300 * time.Millisecond):
			io.WriteString(w, "finished")
		}
	})))
	defer srv.Close()

	type result struct{ method, body string }
	results := make(chan result, 2)
	for _, m := range []string{"GET", "POST"} {
		go func() {
			req, _ := http.NewRequest(m, srv.URL, strings.NewReader(""))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				results <- result{m, err.Error()}
				return
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			results <- result{m, string(b)}
		}()
	}
	<-started
	<-started
	end()
	for range 2 {
		r := <-results
		want := map[string]string{"GET": "ended", "POST": "finished"}[r.method]
		if r.body != want {
			t.Errorf("%s: got %q, want %q", r.method, r.body, want)
		}
	}
}
