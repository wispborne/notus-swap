package peers

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
)

// forwarded lists the paths passed on to other servers: notus-swap's own API
// and llama-swap's log streams. Model calls (/v1/, /upstream/) are not, so a
// request is never recorded twice.
var forwarded = []string{"/notus/api/", "/logs/"}

// ServeHTTP forwards /notus/peer/{id}/{path...} to that server's {path}.
func (s *Set) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(ViaHeader) != "" {
		http.Error(w, "This call already came through another notus-swap. Servers only pass calls on one step.", http.StatusLoopDetected)
		return
	}
	id := r.PathValue("id")
	// The escaped path keeps an encoded "/" inside a model's name as it is.
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/notus/peer/"+url.PathEscape(id))
	p, err := url.PathUnescape(raw)
	if err != nil || !allowed(p) {
		http.Error(w, "Only notus-swap's API and log streams are passed on to other servers.", http.StatusNotFound)
		return
	}
	target, state, name, ok := s.target(id)
	if !ok {
		http.Error(w, "No server with that ID is in this server's list.", http.StatusNotFound)
		return
	}
	if !state.forwards() {
		http.Error(w, unreachable(name, state), http.StatusServiceUnavailable)
		return
	}

	browser := r.Context()
	ctx, done := s.track(id, browser)
	defer done()
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Path, pr.Out.URL.RawPath = p, raw
			pr.SetURL(target)
			pr.SetXForwarded()
			pr.Out.Header.Set(ViaHeader, s.SelfID)
		},
		Transport: s.transport,
		// Streamed answers, such as the live feed and logs, pass on at once.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if browser.Err() != nil {
				return // the browser left
			}
			if ctx.Err() != nil {
				// A check found the server gone and ended its calls.
				http.Error(w, unreachable(name, Offline), http.StatusServiceUnavailable)
				return
			}
			s.CheckSoon(id)
			http.Error(w, fmt.Sprintf("Couldn't reach %s: %s.", name, reason(err)), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r.WithContext(ctx))
}

// allowed says whether p is passed on. p must be clean, so "/notus/api/.."
// can't reach other paths.
func allowed(p string) bool {
	c := path.Clean(p)
	if strings.HasSuffix(p, "/") && c != "/" {
		c += "/"
	}
	if c != p {
		return false
	}
	for _, f := range forwarded {
		if strings.HasPrefix(p, f) {
			return true
		}
	}
	return false
}

func unreachable(name string, state State) string {
	switch state {
	case WrongServer:
		return fmt.Sprintf("%s's address now answers as a different notus-swap. Change its address on the Settings page.", name)
	case NotNotus:
		return fmt.Sprintf("%s's address doesn't answer as notus-swap.", name)
	}
	return fmt.Sprintf("%s is asleep or off.", name)
}
