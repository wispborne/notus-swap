package peers

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (m *memSettings) Setting(_ context.Context, k string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.m[k], nil
}

func (m *memSettings) SetSetting(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[k] = v
	return nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func open(t *testing.T) (*Set, *memSettings) {
	t.Helper()
	st := &memSettings{m: map[string]string{}}
	s, err := Open(context.Background(), st, quiet())
	if err != nil {
		t.Fatal(err)
	}
	s.checkTimeout = 300 * time.Millisecond
	return s, st
}

// fakePeer is a notus-swap that answers health checks as id, and a few
// other paths for the proxy tests.
type fakePeer struct {
	*httptest.Server
	id     atomic.Value // string
	silent atomic.Bool  // health checks get no answer
	last   atomic.Value // *http.Request, the last call to /notus/api/echo
}

func newFakePeer(t *testing.T, id string) *fakePeer {
	f := &fakePeer{}
	f.id.Store(id)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /notus/api/health", func(w http.ResponseWriter, r *http.Request) {
		if f.silent.Load() {
			<-r.Context().Done()
			return
		}
		json.NewEncoder(w).Encode(Health{Version: "v1", ServerID: f.id.Load().(string), PeerAPI: APIVersion, Name: "Fake", Now: time.Now().UnixMilli()})
	})
	mux.HandleFunc("/notus/api/", func(w http.ResponseWriter, r *http.Request) {
		f.last.Store(r)
		w.Write([]byte(r.Method + " " + r.URL.EscapedPath() + "?" + r.URL.RawQuery))
	})
	mux.HandleFunc("GET /logs/stream/proxy", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("first\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func routed(s *Set) *httptest.Server {
	mux := http.NewServeMux()
	mux.Handle("/notus/peer/{id}/{path...}", s)
	return httptest.NewServer(mux)
}

func TestOpenKeepsID(t *testing.T) {
	s, st := open(t)
	if len(s.SelfID) != 12 {
		t.Fatalf("server ID %q", s.SelfID)
	}
	again, err := Open(context.Background(), st, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if again.SelfID != s.SelfID {
		t.Errorf("ID changed from %s to %s", s.SelfID, again.SelfID)
	}
	if l := again.List(); len(l) != 1 || !l[0].Self {
		t.Errorf("a new list should hold only this server: %+v", l)
	}
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.5:8080":        "http://192.168.1.5:8080",
		" http://host:8080/ ":     "http://host:8080",
		"https://llm.example.com": "https://llm.example.com",
		"http://host/prefix/":     "http://host/prefix",
		"ftp://host":              "",
		"http://user:pass@host":   "",
		"http://host:8080/?a=b":   "",
		"":                        "",
		"http://":                 "",
	} {
		got, err := NormalizeURL(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q: want an error, got %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestAdd(t *testing.T) {
	s, st := open(t)
	ctx := context.Background()
	peer := newFakePeer(t, "aaaaaaaaaaaa")

	added, err := s.Add(ctx, peer.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if added.ID != "aaaaaaaaaaaa" || added.Name != "Fake" || added.State != Online {
		t.Errorf("added %+v", added)
	}
	if _, err := s.Add(ctx, peer.URL+"/", "again"); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("adding it twice: %v", err)
	}
	if !strings.Contains(st.m["peers"], peer.URL) {
		t.Errorf("not saved: %s", st.m["peers"])
	}

	self := newFakePeer(t, s.SelfID)
	if _, err := s.Add(ctx, self.URL, ""); err == nil || !strings.Contains(err.Error(), "this server") {
		t.Errorf("adding this server: %v", err)
	}

	notNotus := httptest.NewServer(http.NotFoundHandler())
	defer notNotus.Close()
	if _, err := s.Add(ctx, notNotus.URL, ""); err == nil || !strings.Contains(err.Error(), "not notus-swap") {
		t.Errorf("adding something else: %v", err)
	}

	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"version":"2026.01.01"}`))
	}))
	defer old.Close()
	if _, err := s.Add(ctx, old.URL, ""); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Errorf("adding an old notus-swap: %v", err)
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	if _, err := s.Add(ctx, gone.URL, ""); err == nil || !strings.Contains(err.Error(), "Nothing answered") {
		t.Errorf("adding an address that doesn't answer: %v", err)
	}

	reopened, err := Open(ctx, st, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if l := reopened.List(); len(l) != 2 || l[1].ID != "aaaaaaaaaaaa" || l[1].State != Checking {
		t.Errorf("after a restart: %+v", l)
	}
}

func TestOrderAndRemove(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	a := newFakePeer(t, "aaaaaaaaaaaa")
	b := newFakePeer(t, "bbbbbbbbbbbb")
	for _, p := range []*fakePeer{a, b} {
		if _, err := s.Add(ctx, p.URL, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range [][]string{{"aaaaaaaaaaaa", s.SelfID}, {"aaaaaaaaaaaa", "aaaaaaaaaaaa", s.SelfID}, {"x", "aaaaaaaaaaaa", s.SelfID}} {
		if err := s.Order(ctx, bad); err == nil {
			t.Errorf("order %v should be refused", bad)
		}
	}
	if err := s.Order(ctx, []string{"bbbbbbbbbbbb", s.SelfID, "aaaaaaaaaaaa"}); err != nil {
		t.Fatal(err)
	}
	if l := s.List(); l[0].ID != "bbbbbbbbbbbb" || !l[1].Self {
		t.Errorf("order %+v", l)
	}
	if err := s.Remove(ctx, s.SelfID); err == nil {
		t.Error("this server can't be removed")
	}
	if err := s.Remove(ctx, "bbbbbbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	if l := s.List(); len(l) != 2 || !l[0].Self {
		t.Errorf("after removing: %+v", l)
	}
}

func TestEditAddress(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	a := newFakePeer(t, "aaaaaaaaaaaa")
	if _, err := s.Add(ctx, a.URL, ""); err != nil {
		t.Fatal(err)
	}
	other := newFakePeer(t, "cccccccccccc")
	if err := s.Edit(ctx, "aaaaaaaaaaaa", nil, &other.URL); err == nil || !strings.Contains(err.Error(), "different server") {
		t.Errorf("moving to another server's address: %v", err)
	}
	moved := newFakePeer(t, "aaaaaaaaaaaa")
	name := "PC"
	if err := s.Edit(ctx, "aaaaaaaaaaaa", &name, &moved.URL); err != nil {
		t.Fatal(err)
	}
	if l := s.List(); l[1].URL != moved.URL || l[1].Name != "PC" || l[1].State != Checking {
		t.Errorf("after editing: %+v", l[1])
	}
}

func TestProxy(t *testing.T) {
	s, _ := open(t)
	peer := newFakePeer(t, "aaaaaaaaaaaa")
	if _, err := s.Add(context.Background(), peer.URL, ""); err != nil {
		t.Fatal(err)
	}
	home := routed(s)
	defer home.Close()
	base := home.URL + "/notus/peer/aaaaaaaaaaaa"

	res, err := http.Post(base+"/notus/api/models/a%2Fb/load?x=1", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != "POST /notus/api/models/a%2Fb/load?x=1" {
		t.Errorf("forwarded as %q", b)
	}
	if via := peer.last.Load().(*http.Request).Header.Get(ViaHeader); via != s.SelfID {
		t.Errorf("via header %q", via)
	}

	for p, want := range map[string]int{
		"/v1/chat/completions":             http.StatusNotFound,
		"/upstream/m/health":               http.StatusNotFound,
		"/notus/api/%2e%2e/%2e%2e/v1/chat": http.StatusNotFound,
		"/notus/peer/x/notus/api/health":   http.StatusNotFound,
	} {
		res, err := http.Get(base + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != want {
			t.Errorf("%s: %d, want %d", p, res.StatusCode, want)
		}
	}
	res, _ = http.Get(home.URL + "/notus/peer/unknown/notus/api/health")
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown server: %d", res.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, base+"/notus/api/health", nil)
	req.Header.Set(ViaHeader, "someone")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusLoopDetected {
		t.Errorf("a call that came through another server: %d", res.StatusCode)
	}

	// A stream passes on each piece as it comes, without waiting for the end.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, base+"/logs/stream/proxy", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil || line != "first\n" {
		t.Errorf("stream: %q, %v", line, err)
	}
}

func TestOfflineAndBack(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	peer := newFakePeer(t, "aaaaaaaaaaaa")
	if _, err := s.Add(ctx, peer.URL, "PC"); err != nil {
		t.Fatal(err)
	}
	home := routed(s)
	defer home.Close()
	entry := s.list[1]

	// A computer that went to sleep: the connection is open, but nothing
	// answers. An open stream to it must end once a check notices.
	stream, err := http.Get(home.URL + "/notus/peer/aaaaaaaaaaaa/logs/stream/proxy")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	ended := make(chan struct{})
	go func() {
		io.Copy(io.Discard, stream.Body)
		close(ended)
	}()

	peer.silent.Store(true)
	start := time.Now()
	s.check(ctx, entry)
	if st := s.List()[1]; st.State != Offline {
		t.Fatalf("state %s, %s", st.State, st.Error)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the check took %s", d)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Error("the stream to the sleeping server stayed open")
	}

	start = time.Now()
	res, err := http.Get(home.URL + "/notus/peer/aaaaaaaaaaaa/notus/api/status")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(b), "PC is asleep or off") {
		t.Errorf("call to an offline server: %d %s", res.StatusCode, b)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("the offline answer took %s", d)
	}

	peer.silent.Store(false)
	s.check(ctx, entry)
	if st := s.List()[1]; st.State != Online {
		t.Errorf("after waking: %s %s", st.State, st.Error)
	}
}

func TestWrongServer(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	peer := newFakePeer(t, "aaaaaaaaaaaa")
	if _, err := s.Add(ctx, peer.URL, "PC"); err != nil {
		t.Fatal(err)
	}
	peer.id.Store("dddddddddddd")
	s.check(ctx, s.list[1])
	if st := s.List()[1]; st.State != WrongServer {
		t.Fatalf("state %s", st.State)
	}
	home := routed(s)
	defer home.Close()
	res, err := http.Get(home.URL + "/notus/peer/aaaaaaaaaaaa/notus/api/status")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("call to the wrong server: %d", res.StatusCode)
	}
}

// A listener that accepts connections and never answers, like a process
// that hung. Checks must give up on it in time.
func TestSilentListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	s, _ := open(t)
	start := time.Now()
	_, err = s.Test(context.Background(), "http://"+ln.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "no answer in time") {
		t.Errorf("got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s", d)
	}
}
