package peers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Follow passes every other server's live feed on to out, for as long as ctx
// lasts. Each event gets a "server" field naming its server, and a server
// that stops answering sends {"type": "peer_down"}; its next "hello" lists
// its requests in flight again. Servers added to or removed from the list
// while it runs are followed or dropped within a few seconds.
func (s *Set) Follow(ctx context.Context, out chan<- []byte) {
	running := map[string]context.CancelFunc{}
	defer func() {
		for _, cancel := range running {
			cancel()
		}
	}()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		ids := map[string]bool{}
		for _, e := range s.entries() {
			if e.ID == s.SelfID {
				continue
			}
			ids[e.ID] = true
			if running[e.ID] == nil {
				c, cancel := context.WithCancel(ctx)
				running[e.ID] = cancel
				go s.follow(c, e.ID, out)
			}
		}
		for id, cancel := range running {
			if !ids[id] {
				cancel()
				delete(running, id)
				send(ctx, out, down(id))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Set) entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.list...)
}

// follow keeps one server's feed open: while the server answers, and again
// each time it comes back.
func (s *Set) follow(ctx context.Context, id string, out chan<- []byte) {
	for ctx.Err() == nil {
		if _, state, _, ok := s.target(id); !ok {
			return
		} else if state.forwards() {
			if s.followOnce(ctx, id, out) {
				send(ctx, out, down(id))
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(s.retryEvery):
		}
	}
}

// followOnce reads the server's feed until it ends. up says whether any
// event came through, so the browser has rows from it to clear.
func (s *Set) followOnce(ctx context.Context, id string, out chan<- []byte) (up bool) {
	target, _, _, ok := s.target(id)
	if !ok {
		return false
	}
	ctx, done := s.track(id, ctx)
	defer done()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(target.String(), "/")+"/notus/api/live", nil)
	if err != nil {
		return false
	}
	req.Header.Set(ViaHeader, s.SelfID)
	req.Header.Set("Accept", "text/event-stream")
	res, err := s.transport.RoundTrip(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false
	}

	// The feed sends a ping every 20 seconds. A computer that went to sleep
	// sends nothing and doesn't close the connection, so a long silence ends
	// it.
	lines := make(chan string)
	readErr := make(chan error, 1)
	go func() {
		r := bufio.NewReader(res.Body)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				readErr <- err
				return
			}
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	silence := time.NewTimer(s.silentAfter)
	defer silence.Stop()
	for {
		select {
		case <-ctx.Done():
			return up
		case <-readErr:
			return up
		case <-silence.C:
			s.Log.Info("live feed went silent; reconnecting", "id", id)
			return up
		case line := <-lines:
			silence.Reset(s.silentAfter)
			data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data: ")
			if !ok {
				continue // a ping, or the blank line after an event
			}
			b, err := tag(id, []byte(data))
			if err != nil {
				continue
			}
			up = true
			if !send(ctx, out, b) {
				return up
			}
		}
	}
}

// tag adds "server": id to an event, which is a JSON object.
func tag(id string, event []byte) ([]byte, error) {
	if len(event) < 2 || event[0] != '{' || event[len(event)-1] != '}' {
		return nil, errors.New("not a JSON object")
	}
	name, _ := json.Marshal(id)
	b := fmt.Appendf(nil, `{"server":%s`, name)
	if strings.TrimSpace(string(event[1:len(event)-1])) != "" {
		b = append(b, ',')
	}
	return append(b, event[1:]...), nil
}

func down(id string) []byte {
	b, _ := json.Marshal(map[string]any{"type": "peer_down", "server": id, "at": time.Now().UnixMilli()})
	return b
}

func send(ctx context.Context, out chan<- []byte, b []byte) bool {
	select {
	case out <- b:
		return true
	case <-ctx.Done():
		return false
	}
}
