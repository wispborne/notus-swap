// Package peers keeps the Servers list: this notus-swap and the other
// notus-swap servers it shows. It checks whether each one is up and forwards
// the UI's calls to them (proxy.go).
//
// The browser only talks to the server whose page it opened. That server
// talks to the others, one step at most: a request that already came through
// another notus-swap is never forwarded again.
package peers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// APIVersion is the version of what servers ask of each other. A server on
// another version shows as needing an update.
const APIVersion = 1

// ViaHeader names the server a forwarded request came through. A server
// never forwards a request that has it.
const ViaHeader = "X-Notus-Via"

// Settings is where the list and this server's ID are kept.
type Settings interface {
	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Entry is one row of the Servers list, as stored. This server's row has no
// URL. The first row is the primary.
type Entry struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// State is what the last check found.
type State string

const (
	// Checking: added or started, and not checked yet.
	Checking State = "checking"
	Online   State = "online"
	// Offline: nothing answered. A computer that sleeps shows this.
	Offline State = "offline"
	// WrongServer: the address answers as a different notus-swap, such as
	// when another machine got the peer's old IP address.
	WrongServer State = "wrong_server"
	// NotNotus: something answered, but not notus-swap.
	NotNotus State = "not_notus"
	// NeedsUpdate: notus-swap on another APIVersion. Calls are still
	// forwarded, so it can be updated from here.
	NeedsUpdate State = "needs_update"
)

// forwards says whether calls are passed on to a server in this state.
func (s State) forwards() bool {
	return s == Checking || s == Online || s == NeedsUpdate
}

// Server is one row of the list with its last check. Times are unix ms.
type Server struct {
	Entry
	Self  bool  `json:"self"`
	State State `json:"state"`
	// Since is when the server entered State.
	Since int64 `json:"since,omitempty"`
	// LastSeen is when the server last answered.
	LastSeen int64  `json:"last_seen,omitempty"`
	Version  string `json:"version,omitempty"`
	// ClockGapMs is how far the server's clock is ahead of this one's.
	ClockGapMs *int64 `json:"clock_gap_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Health is what GET /notus/api/health answers, as far as peers need it.
type Health struct {
	Version  string `json:"version"`
	ServerID string `json:"server_id"`
	PeerAPI  int    `json:"peer_api"`
	// Name is the server's app title, or "" for the default.
	Name string `json:"name"`
	Now  int64  `json:"now"` // unix ms
}

// Probe is what checking an address found.
type Probe struct {
	Health
	URL        string `json:"url"`
	LatencyMs  int64  `json:"latency_ms"`
	ClockGapMs int64  `json:"clock_gap_ms"`
}

// Error is a problem to show the user. Code is the HTTP status to answer
// with, and State the state a saved server gets for it.
type Error struct {
	Code  int
	State State
	Msg   string
}

func (e *Error) Error() string { return e.Msg }

func problem(code int, state State, format string, args ...any) *Error {
	return &Error{Code: code, State: state, Msg: fmt.Sprintf(format, args...)}
}

// Set is the list, each server's state, and the client that reaches them.
type Set struct {
	// SelfID is this server's ID, made the first time it starts.
	SelfID string
	Log    *slog.Logger

	settings  Settings
	transport *http.Transport
	// How long a check may take, and how often servers are checked while
	// online and while not. Tests shorten them.
	checkTimeout, onlineEvery, offlineEvery time.Duration

	mu     sync.Mutex
	list   []Entry
	status map[string]*Server
	next   map[string]time.Time // when each server is checked next
	busy   map[string]bool      // checks running
	// streams cancels the calls forwarded to each server, so a server that
	// goes to sleep doesn't leave streams open that never end.
	streams map[string]map[uint64]context.CancelFunc
	seq     uint64
	wake    chan struct{}
}

// Open reads the list and this server's ID, making the ID if it has none.
func Open(ctx context.Context, settings Settings, log *slog.Logger) (*Set, error) {
	s := &Set{
		Log: log, settings: settings,
		checkTimeout: 5 * time.Second, onlineEvery: 15 * time.Second, offlineEvery: 5 * time.Second,
		status: map[string]*Server{}, next: map[string]time.Time{}, busy: map[string]bool{},
		streams: map[string]map[uint64]context.CancelFunc{}, wake: make(chan struct{}, 1),
	}
	// A computer that is asleep never answers a connection, so without a
	// limit each try would wait 20 seconds or more.
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 15 * time.Second}
	s.transport = &http.Transport{
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		// Streams have no limit once they start. This one only covers an
		// answer that never starts, such as on a connection to a server that
		// went to sleep; checks usually notice that sooner.
		ResponseHeaderTimeout: time.Minute,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   8,
		ForceAttemptHTTP2:     true,
	}

	v, err := settings.Setting(ctx, "server_id")
	if err != nil {
		return nil, err
	}
	if v != "" {
		json.Unmarshal([]byte(v), &s.SelfID)
	}
	if s.SelfID == "" {
		b := make([]byte, 6)
		rand.Read(b)
		s.SelfID = hex.EncodeToString(b)
		j, _ := json.Marshal(s.SelfID)
		if err := settings.SetSetting(ctx, "server_id", string(j)); err != nil {
			return nil, err
		}
	}

	if v, err = settings.Setting(ctx, "peers"); err != nil {
		return nil, err
	}
	if v != "" {
		if err := json.Unmarshal([]byte(v), &s.list); err != nil {
			log.Error("the Servers list couldn't be read; starting with this server only", "err", err)
			s.list = nil
		}
	}
	if !slices.ContainsFunc(s.list, func(e Entry) bool { return e.ID == s.SelfID }) {
		s.list = append([]Entry{{ID: s.SelfID}}, s.list...)
	}
	now := time.Now()
	for _, e := range s.list {
		if e.ID != s.SelfID {
			s.status[e.ID] = &Server{Entry: e, State: Checking, Since: now.UnixMilli()}
		}
	}
	return s, nil
}

// List is every server in order, with its last check.
func (s *Set) List() []Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Server, len(s.list))
	for i, e := range s.list {
		if e.ID == s.SelfID {
			out[i] = Server{Entry: e, Self: true, State: Online}
			continue
		}
		out[i] = *s.status[e.ID]
		out[i].Entry = e
	}
	return out
}

// Has says whether there are other servers in the list.
func (s *Set) Has() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.list) > 1
}

func (s *Set) save(ctx context.Context) error {
	b, _ := json.Marshal(s.list)
	return s.settings.SetSetting(ctx, "peers", string(b))
}

// NormalizeURL turns what the user typed into a base URL, such as
// "192.168.1.5:8080" into "http://192.168.1.5:8080".
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", problem(http.StatusBadRequest, "", "Enter the server's address, such as http://192.168.1.5:8080.")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", problem(http.StatusBadRequest, "", "%q isn't an address. Use one such as http://192.168.1.5:8080.", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", problem(http.StatusBadRequest, "", "The address must start with http:// or https://.")
	}
	if u.User != nil {
		return "", problem(http.StatusBadRequest, "", "The address can't hold a user name or password.")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", problem(http.StatusBadRequest, "", "The address can't have a ? or # part.")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

// Test checks an address: whether notus-swap answers there, and which one.
func (s *Set) Test(ctx context.Context, raw string) (Probe, error) {
	base, err := NormalizeURL(raw)
	if err != nil {
		return Probe{}, err
	}
	p, err := s.probe(ctx, base)
	if err != nil {
		return p, err
	}
	if p.ServerID == s.SelfID {
		return p, problem(http.StatusBadRequest, "", "That address is this server.")
	}
	return p, nil
}

// probe asks base's /notus/api/health who answers there.
func (s *Set) probe(ctx context.Context, base string) (Probe, error) {
	p := Probe{URL: base}
	ctx, cancel := context.WithTimeout(ctx, s.checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/notus/api/health", nil)
	if err != nil {
		return p, problem(http.StatusBadRequest, NotNotus, "%q isn't an address: %v", base, err)
	}
	req.Header.Set(ViaHeader, s.SelfID)
	sent := time.Now()
	res, err := s.transport.RoundTrip(req)
	got := time.Now()
	if err != nil {
		return p, problem(http.StatusBadGateway, Offline, "Nothing answered at %s: %s.", base, reason(err))
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return p, problem(http.StatusBadGateway, Offline, "%s stopped answering: %s.", base, reason(err))
	}
	if res.StatusCode != http.StatusOK || json.Unmarshal(b, &p.Health) != nil || p.Version == "" {
		return p, problem(http.StatusBadRequest, NotNotus, "Something answered at %s, but not notus-swap.", base)
	}
	if p.ServerID == "" {
		return p, problem(http.StatusBadRequest, NeedsUpdate, "That's notus-swap %s, which is too old to show here. Update it first.", p.Version)
	}
	p.LatencyMs = got.Sub(sent).Milliseconds()
	// The server read its clock about halfway through the round trip.
	p.ClockGapMs = p.Now - sent.Add(got.Sub(sent)/2).UnixMilli()
	return p, nil
}

// reason is a short form of a connection error, for the user.
func reason(err error) string {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
		return "no answer in time"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		err = oe.Err
	}
	// Windows' messages end with a period; the callers add their own.
	return strings.TrimRight(err.Error(), ".")
}

// Add tests an address and adds the server there to the end of the list.
// An empty name takes the server's app title, or else its host name.
func (s *Set) Add(ctx context.Context, raw, name string) (Server, error) {
	p, err := s.Test(ctx, raw)
	if err != nil {
		return Server{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = p.Name
	}
	if name == "" {
		u, _ := url.Parse(p.URL)
		name = u.Hostname()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := slices.IndexFunc(s.list, func(e Entry) bool { return e.ID == p.ServerID }); i >= 0 {
		return Server{}, problem(http.StatusConflict, "", "That server is already in the list, as %s.", s.list[i].Name)
	}
	e := Entry{ID: p.ServerID, Name: name, URL: p.URL}
	s.list = append(s.list, e)
	if err := s.save(ctx); err != nil {
		s.list = s.list[:len(s.list)-1]
		return Server{}, err
	}
	now := time.Now()
	st := &Server{Entry: e, State: Online, Since: now.UnixMilli(), LastSeen: now.UnixMilli(), Version: p.Version, ClockGapMs: &p.ClockGapMs}
	if p.PeerAPI != APIVersion {
		st.State = NeedsUpdate
	}
	s.status[e.ID] = st
	s.next[e.ID] = now.Add(s.onlineEvery)
	s.Log.Info("server added", "name", name, "url", p.URL, "id", e.ID)
	return *st, nil
}

// Edit renames a server or changes its address. A new address must answer
// as the same server.
func (s *Set) Edit(ctx context.Context, id string, name, raw *string) error {
	var p Probe
	if raw != nil {
		var err error
		if p, err = s.Test(ctx, *raw); err != nil {
			return err
		}
		if p.ServerID != id {
			return problem(http.StatusBadRequest, "", "That address answers as a different server.")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.list, func(e Entry) bool { return e.ID == id })
	if i < 0 || id == s.SelfID {
		return problem(http.StatusNotFound, "", "No other server with that ID is in the list.")
	}
	old := s.list[i]
	if name != nil {
		if n := strings.TrimSpace(*name); n != "" {
			s.list[i].Name = n
		}
	}
	if raw != nil {
		s.list[i].URL = p.URL
	}
	if err := s.save(ctx); err != nil {
		s.list[i] = old
		return err
	}
	st := s.status[id]
	st.Entry = s.list[i]
	if raw != nil && p.URL != old.URL {
		st.State, st.Since = Checking, time.Now().UnixMilli()
		s.endStreams(id)
		s.next[id] = time.Time{}
		s.poke()
	}
	return nil
}

// Remove takes a server off the list. This server can't be removed.
func (s *Set) Remove(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.list, func(e Entry) bool { return e.ID == id })
	if i < 0 || id == s.SelfID {
		return problem(http.StatusNotFound, "", "No other server with that ID is in the list.")
	}
	old := s.list
	s.list = slices.Delete(slices.Clone(s.list), i, i+1)
	if err := s.save(ctx); err != nil {
		s.list = old
		return err
	}
	s.endStreams(id)
	delete(s.status, id)
	delete(s.next, id)
	s.Log.Info("server removed", "name", old[i].Name, "id", id)
	return nil
}

// Order puts the list in the order of ids, which must name every server once.
func (s *Set) Order(ctx context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) != len(s.list) {
		return problem(http.StatusBadRequest, "", "The new order must name every server once.")
	}
	byID := map[string]Entry{}
	for _, e := range s.list {
		byID[e.ID] = e
	}
	next := make([]Entry, 0, len(ids))
	for _, id := range ids {
		e, ok := byID[id]
		if !ok {
			return problem(http.StatusBadRequest, "", "The new order must name every server once.")
		}
		delete(byID, id)
		next = append(next, e)
	}
	old := s.list
	s.list = next
	if err := s.save(ctx); err != nil {
		s.list = old
		return err
	}
	return nil
}

// Run checks each server: every 15 seconds while it answers, and every 5
// seconds while it doesn't, so a computer that wakes up is back quickly.
func (s *Set) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		s.checkDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.wake:
		}
	}
}

func (s *Set) checkDue(ctx context.Context) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.list {
		if e.ID == s.SelfID || s.busy[e.ID] || now.Before(s.next[e.ID]) {
			continue
		}
		s.busy[e.ID] = true
		go s.check(ctx, e)
	}
}

// poke makes Run check the servers that are due at once.
func (s *Set) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// CheckSoon checks a server at once, such as after a call to it failed.
func (s *Set) CheckSoon(id string) {
	s.mu.Lock()
	s.next[id] = time.Time{}
	s.mu.Unlock()
	s.poke()
}

func (s *Set) check(ctx context.Context, e Entry) {
	p, err := s.probe(ctx, e.URL)
	now := time.Now()
	state, msg := Online, ""
	var pe *Error
	switch {
	case errors.As(err, &pe):
		state, msg = pe.State, pe.Msg
	case err != nil:
		state, msg = Offline, err.Error()
	case p.ServerID != e.ID:
		state, msg = WrongServer, fmt.Sprintf("%s now answers as a different notus-swap.", e.URL)
	case p.PeerAPI != APIVersion:
		state = NeedsUpdate
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy[e.ID] = false
	st, ok := s.status[e.ID]
	if !ok || st.URL != e.URL {
		return // removed, or its address changed, while this check ran
	}
	every := s.onlineEvery
	if state != Online {
		every = s.offlineEvery
	}
	s.next[e.ID] = now.Add(every)
	if state != st.State {
		if st.State != Checking || state != Online {
			s.Log.Info("server state changed", "name", e.Name, "url", e.URL, "from", st.State, "to", state, "reason", msg)
		}
		st.State, st.Since = state, now.UnixMilli()
		if !state.forwards() {
			s.endStreams(e.ID)
			// A pooled connection to a computer that went to sleep would
			// hang the next call.
			s.transport.CloseIdleConnections()
		}
	}
	st.Error = msg
	if err == nil || state == NeedsUpdate && p.Version != "" {
		st.LastSeen, st.Version = now.UnixMilli(), p.Version
	}
	if err == nil {
		st.ClockGapMs = &p.ClockGapMs
	}
}

// target is where calls to a server go, and its state and name.
func (s *Set) target(id string) (u *url.URL, state State, name string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.status[id]
	if !ok {
		return nil, "", "", false
	}
	u, err := url.Parse(st.URL)
	if err != nil {
		return nil, "", "", false
	}
	return u, st.State, st.Name, true
}

// track registers a forwarded call, so endStreams can end it. done must be
// called when the call ends.
func (s *Set) track(id string, parent context.Context) (ctx context.Context, done func()) {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.seq++
	n := s.seq
	if s.streams[id] == nil {
		s.streams[id] = map[uint64]context.CancelFunc{}
	}
	s.streams[id][n] = cancel
	s.mu.Unlock()
	return ctx, func() {
		s.mu.Lock()
		delete(s.streams[id], n)
		s.mu.Unlock()
		cancel()
	}
}

// endStreams ends every call forwarded to a server. s.mu must be held.
func (s *Set) endStreams(id string) {
	for _, cancel := range s.streams[id] {
		cancel()
	}
	delete(s.streams, id)
}
