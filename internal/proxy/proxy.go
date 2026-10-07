// Package proxy forwards requests to llama-swap.
package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/wispborne/notus-swap/internal/capture"
)

// New returns a reverse proxy to llama-swap. Responses are flushed as soon as
// they arrive, so streamed tokens are not held back. When llama-swap cannot
// be reached, clients get a 503 with an OpenAI-style error body.
func New(upstream *url.URL, log *slog.Logger) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.SetXForwarded()
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if capture.Cancelled(r) {
				// A 4xx, so clients don't send it again on their own.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(499)
				w.Write([]byte(`{"error":{"message":"cancelled in notus-swap","type":"cancelled","code":499}}`))
				return
			}
			if r.Context().Err() != nil {
				return // the client left; nobody to answer
			}
			log.Warn("llama-swap unreachable", "path", r.URL.Path, "err", err)
			capture.SetError(r, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":{"message":"llama-swap is not reachable","type":"upstream_unavailable","code":503}}`))
		},
	}
}
