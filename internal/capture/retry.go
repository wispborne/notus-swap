package capture

import (
	"bytes"
	"context"
	"errors"
	"net/http"
)

// ErrCancelled is the cause given to a request's context when it is
// cancelled from the web UI. It is also the error stored with the request.
var ErrCancelled = errors.New("cancelled in notus-swap")

// Cancelled reports whether r was cancelled from the web UI, rather than by
// its client hanging up.
func Cancelled(r *http.Request) bool {
	return errors.Is(context.Cause(r.Context()), ErrCancelled)
}

type retryKey struct{}

// retrying is set on a request that Retry sends. The capture handler puts the
// new request's ID on id once it is stored.
type retrying struct {
	of int64
	id chan int64
}

// ErrNotStored is returned by Retry when the request could not be stored, so
// it wasn't sent.
var ErrNotStored = errors.New("the request could not be stored")

// Retry sends a stored request body to path again, through h, which must be
// the capture handler. It runs in the background, so it continues after the
// caller returns, and the answer is only stored. It returns the new
// request's ID once the request is stored. The original request's headers
// aren't stored, so the new one only has Content-Type: application/json.
func Retry(h http.Handler, method, path string, body []byte, of int64) (int64, error) {
	ctx := context.WithValue(context.Background(), retryKey{}, &retrying{of: of, id: make(chan int64, 1)})
	rt := ctx.Value(retryKey{}).(*retrying)
	r, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	r.Header.Set("Content-Type", "application/json")
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		// The proxy panics with http.ErrAbortHandler when a stream breaks.
		// There is no client to tell, and the capture handler has already
		// stored the request.
		defer func() {
			if rec := recover(); rec != nil && rec != http.ErrAbortHandler {
				panic(rec)
			}
		}()
		h.ServeHTTP(&discard{header: http.Header{}}, r)
	}()
	select {
	case id := <-rt.id:
		return id, nil
	case <-ended:
		select {
		case id := <-rt.id:
			return id, nil
		default:
			return 0, ErrNotStored
		}
	}
}

// discard is the response writer for a retried request. Its answer is read
// from the store and the live feed, so the bytes themselves are dropped.
type discard struct{ header http.Header }

func (d *discard) Header() http.Header         { return d.header }
func (d *discard) Write(p []byte) (int, error) { return len(p), nil }
func (d *discard) WriteHeader(int)             {}
func (d *discard) Flush()                      {}
