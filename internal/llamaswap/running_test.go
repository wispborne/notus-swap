package llamaswap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
)

func TestRunningArgs(t *testing.T) {
	r := Running{
		Cmd: "/opt/llama/current/llama-server \\\n  --port 5801\n# a comment\n  -m '/srv/models/a b.gguf' -c 32768 --host=127.0.0.1:5801 -fa on\n",
		Proxy: "http://127.0.0.1:5801",
	}
	want := []string{"/opt/llama/current/llama-server", "--port", "${PORT}", "-m", "/srv/models/a b.gguf",
		"-c", "32768", "--host=127.0.0.1:${PORT}", "-fa", "on"}
	if got := r.Args(); !slices.Equal(got, want) {
		t.Errorf("args\n got %q\nwant %q", got, want)
	}

	// The same command written differently, on another port, hashes the same.
	same := Running{Cmd: `/opt/llama/current/llama-server --port 5802 -m "/srv/models/a b.gguf" -c 32768 --host=127.0.0.1:5802 -fa on`,
		Proxy: "http://127.0.0.1:5802"}
	if HashArgs(r.Args()) != HashArgs(same.Args()) {
		t.Errorf("same command hashed differently: %q vs %q", r.Args(), same.Args())
	}
	other := Running{Cmd: `/opt/llama/current/llama-server --port 5802 -m "/srv/models/a b.gguf" -c 65536`, Proxy: "http://127.0.0.1:5802"}
	if HashArgs(r.Args()) == HashArgs(other.Args()) {
		t.Error("different commands hashed the same")
	}
	// 15801 is not the port.
	if got := (Running{Cmd: "x --n 15801 5801", Proxy: "http://127.0.0.1:5801"}).Args(); !slices.Equal(got, []string{"x", "--n", "15801", "${PORT}"}) {
		t.Errorf("port replaced inside another number: %q", got)
	}
}

func TestFetchRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/running" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"running":[{"model":"qwen","state":"ready","cmd":"llama-server --port 5800","proxy":"http://127.0.0.1:5800","ttl":0}]}`)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	got, err := NewMonitor(u).FetchRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Model != "qwen" || !slices.Equal(got[0].Args(), []string{"llama-server", "--port", "${PORT}"}) {
		t.Errorf("got %+v", got)
	}
}
