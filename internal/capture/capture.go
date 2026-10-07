// Package capture records inference requests as they pass through the proxy.
//
// The response is copied into memory while it streams to the client, so the
// client sees every chunk as soon as it would without notus-swap. Capture
// problems are logged and never break the request itself.
package capture

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wispborne/notus-swap/internal/store"
)

// MaxBody is how much of each request and response body is stored.
// Anything past it still reaches the client but is not stored.
const MaxBody = 32 << 20

// ShouldCapture reports whether a request is an inference call: a POST under
// /v1/ or /upstream/. Everything else (llama-swap's UI, log streams, model
// lists, llama.cpp's web UI files) passes through unrecorded.
func ShouldCapture(r *http.Request) bool {
	return r.Method == http.MethodPost &&
		(strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/upstream/"))
}

type errKey struct{}

// SetError attaches an upstream error to a captured request, so it is stored
// with the request. The proxy's error handler calls it.
func SetError(r *http.Request, err error) {
	if p, ok := r.Context().Value(errKey{}).(*string); ok && err != nil {
		*p = err.Error()
	}
}

// Handler wraps next and records every inference request in st. Progress of
// requests in flight goes to hub.
func Handler(st *store.Store, hub *Hub, next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ShouldCapture(r) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()

		reqBody, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			http.Error(w, "could not read request body", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(reqBody))
		r.ContentLength = int64(len(reqBody))
		r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(reqBody)), nil }
		// Ask for an uncompressed response, so the stored copy is readable.
		r.Header.Del("Accept-Encoding")

		model, streaming, preview := peekRequest(reqBody)
		if model == "" {
			model = upstreamModel(r.URL.Path)
		}
		stored, truncated := cut(reqBody)
		tags := RequestTags(r.URL.Path, reqBody, streaming)
		if tags == nil {
			tags = []Tag{}
		}
		retry, _ := r.Context().Value(retryKey{}).(*retrying)
		var retryOf int64
		if retry != nil {
			retryOf = retry.of
		}
		id, err := st.Begin(r.Context(), store.Begin{
			StartedAt: start, Method: r.Method, Path: r.URL.Path, Model: model, Streaming: streaming,
			Preview: preview, RequestBody: stored, RequestBytes: int64(len(reqBody)), Truncated: truncated,
			Tags: MarshalTags(tags), RetryOf: retryOf,
		})
		if err != nil {
			log.Error("capture: could not store request", "err", err)
			next.ServeHTTP(w, r)
			return
		}

		// The web UI's Cancel button cancels this context, which ends the
		// request to llama-swap and the answer to the client.
		ctx, cancel := context.WithCancelCause(r.Context())
		defer cancel(nil)
		hub.start(Start{ID: id, StartedAt: start.UnixMilli(), Method: r.Method, Path: r.URL.Path,
			Model: model, Streaming: streaming, Preview: preview, RequestBytes: int64(len(reqBody)), Tags: tags,
			RetryOf: retryOf}, cancel)
		if retry != nil {
			retry.id <- id
		}

		var upstreamErr string
		r = r.WithContext(context.WithValue(ctx, errKey{}, &upstreamErr))
		cw := &writer{ResponseWriter: w, id: id, hub: hub}

		// ReverseProxy panics with http.ErrAbortHandler when a stream breaks
		// part way. Record the request anyway, then let the panic continue.
		defer func() {
			rec := recover()
			finish(st, hub, log, id, start, r, reqBody, model, streaming, cw, upstreamErr, rec != nil)
			hub.finish(id)
			if rec != nil {
				panic(rec)
			}
		}()
		next.ServeHTTP(cw, r)
	})
}

func finish(st *store.Store, hub *Hub, log *slog.Logger, id int64, start time.Time, r *http.Request, reqBody []byte, model string, streaming bool, cw *writer, upstreamErr string, aborted bool) {
	end := time.Now()
	f := store.Finish{
		FirstByteAt:   cw.firstByte,
		FirstTokenAt:  cw.firstToken,
		FinishedAt:    end,
		StatusCode:    cw.status,
		Error:         upstreamErr,
		ResponseBody:  cw.buf.Bytes(),
		ResponseBytes: cw.total,
		Truncated:     cw.truncated,
	}
	switch {
	// Some clients, such as Codex, hang up as soon as they read the last
	// event, before the server closes the stream. The answer still arrived.
	case cw.ended && cw.status < 500:
		f.State = store.StateDone
	case errors.Is(context.Cause(r.Context()), ErrCancelled):
		f.State = store.StateCancelled
		f.Error = ErrCancelled.Error()
	case r.Context().Err() != nil:
		f.State = store.StateClientGone
	case aborted:
		f.State = store.StateFailed
		if f.Error == "" {
			f.Error = "stream ended early"
		}
	case cw.status >= 500:
		f.State = store.StateFailed
	default:
		f.State = store.StateDone
	}
	if hub.Cmd != nil {
		f.CmdHash = hub.Cmd(model)
	}
	if !cw.truncated {
		p := Parse(cw.buf.Bytes(), cw.Header().Get("Content-Type"))
		f.PromptTokens, f.CompletionTokens, f.CachedTokens = p.PromptTokens(), p.CompletionTokens(), p.CachedTokens()
		if t := p.Timings; t != nil {
			f.PromptMs, f.PredictedMs = t.PromptMs, t.PredictedMs
			f.PromptPerSecond, f.PredictedPerSecond = t.PromptPerSecond, t.PredictedPerSecond
		}
		f.FinishReason, f.Build = p.FinishReason, p.Build
		var props *store.ModelProps
		if hub.Props != nil {
			props = hub.Props(model)
		}
		if props != nil {
			f.NCtx = &props.NCtx
			// Responses API answers don't name the build, but /props does.
			if f.Build == "" {
				f.Build = props.Build
			}
		}
		f.Issues = Check(CheckInput{Path: r.URL.Path, State: f.State, StatusCode: cw.status,
			Request: reqBody, Response: p, Props: props})
		f.Tags = MarshalTags(Tags(r.URL.Path, reqBody, streaming, &p, f.PromptTokens, f.CachedTokens))
	} else {
		f.Tags = MarshalTags(Tags(r.URL.Path, reqBody, streaming, nil, nil, nil))
	}
	// A request that waited for its model to load has the load in its prompt time.
	loaded := model != "" && st.LoadedSince(context.WithoutCancel(r.Context()), model, start)
	if hub.Account != nil {
		w := store.Work{ID: id, Arrived: start, Ended: end, Began: store.BeganAt(start, end, f.PromptMs, f.PredictedMs)}
		// Without llama.cpp's timings, the server started no later than
		// the first progress or output seen, or else when the request arrived.
		if w.Began.IsZero() {
			w.Began = hub.began(id)
		}
		if w.Began.IsZero() {
			w.Began = start
		}
		var queued time.Duration
		f.EnergyJ, queued = hub.Account(w)
		ms := queued.Milliseconds()
		f.QueuedMs = &ms
		measureSpeeds(&f, start, cw.firstChunk, queued, loaded)
	} else {
		measureSpeeds(&f, start, cw.firstChunk, 0, loaded)
	}
	if err := st.Finish(context.WithoutCancel(r.Context()), id, f); err != nil {
		log.Error("capture: could not store response", "id", id, "err", err)
	}
}

// measureSpeeds fills in the prompt and generation speeds from the clock when
// the server gave no llama.cpp timings, as vLLM doesn't. The prompt time runs
// from arrival to the first token, less the time spent waiting behind other
// requests. It is skipped when the request waited for a model load, which
// would make the speed look far too low. The generation time runs from the
// first token to the end, so it needs a streamed answer. It runs after the
// energy is worked out, because that uses the prompt and generation times to
// find when work began.
//
// firstChunk is when the first Chat Completions chunk arrived. vLLM sends it
// once the prompt is done and the first token is made, even when it holds no
// text. A stream can stall after it and then deliver everything in a second,
// which would give speeds of thousands of tokens a second, so without timings
// the first token time is moved back to it.
func measureSpeeds(f *store.Finish, start, firstChunk time.Time, queued time.Duration, loaded bool) {
	if f.State != store.StateDone || f.FirstTokenAt.IsZero() {
		return
	}
	if f.PromptMs == nil && f.PredictedMs == nil && !firstChunk.IsZero() && firstChunk.Before(f.FirstTokenAt) {
		f.FirstTokenAt = firstChunk
	}
	if f.PredictedPerSecond == nil && f.PredictedMs == nil && f.CompletionTokens != nil && *f.CompletionTokens > 1 {
		// The first token comes with the wait, so it is left out of the count.
		if ms := float64(f.FinishedAt.Sub(f.FirstTokenAt).Milliseconds()); ms > 0 {
			perSec := float64(*f.CompletionTokens-1) * 1000 / ms
			f.PredictedMs, f.PredictedPerSecond = &ms, &perSec
		}
	}
	if !loaded && f.PromptPerSecond == nil && f.PromptMs == nil && f.PromptTokens != nil {
		processed := *f.PromptTokens
		if f.CachedTokens != nil {
			processed -= *f.CachedTokens
		}
		if ms := float64((f.FirstTokenAt.Sub(start) - queued).Milliseconds()); ms > 0 && processed > 0 {
			perSec := float64(processed) * 1000 / ms
			f.PromptMs, f.PromptPerSecond = &ms, &perSec
		}
	}
}

// upstreamModel reads the model from a llama-swap /upstream/<model>/... path.
func upstreamModel(path string) string {
	rest, ok := strings.CutPrefix(path, "/upstream/")
	if !ok {
		return ""
	}
	model, _, _ := strings.Cut(rest, "/")
	return model
}

func cut(b []byte) ([]byte, bool) {
	if len(b) > MaxBody {
		return b[:MaxBody], true
	}
	return b, false
}

// writer passes the response through and keeps a copy of it.
type writer struct {
	http.ResponseWriter
	status     int
	firstByte  time.Time
	firstToken time.Time
	firstChunk time.Time // the first chunk with choices, see measureSpeeds
	buf        bytes.Buffer
	total      int64
	truncated  bool
	scanned    int  // how much of buf has been split into SSE lines
	ended      bool // the stream's last event arrived
	id         int64
	hub        *Hub
}

func (w *writer) WriteHeader(code int) {
	if w.status == 0 && code >= 200 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *writer) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	now := time.Now()
	if w.firstByte.IsZero() {
		w.firstByte = now
	}
	w.total += int64(len(p))
	if room := MaxBody - w.buf.Len(); room < len(p) {
		w.buf.Write(p[:max(room, 0)])
		w.truncated = true
	} else {
		w.buf.Write(p)
	}
	w.scanLines(now)
	return w.ResponseWriter.Write(p)
}

// scanLines reads each complete SSE line not yet seen. It records the first
// token time and passes streamed output to the hub.
func (w *writer) scanLines(now time.Time) {
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		return
	}
	b := w.buf.Bytes()
	for {
		i := bytes.IndexByte(b[w.scanned:], '\n')
		if i < 0 {
			return
		}
		line := b[w.scanned : w.scanned+i]
		w.scanned += i + 1
		data, ok := sseData(line)
		if !ok {
			w.ended = w.ended || endsStream(line, body{})
			continue
		}
		x, content, reasoning, output := chunk(data)
		w.ended = w.ended || endsStream(line, x)
		if output && w.firstToken.IsZero() {
			w.firstToken = now
		}
		if len(x.Choices) > 0 && w.firstChunk.IsZero() {
			w.firstChunk = now
		}
		w.hub.chunk(w.id, x, content, reasoning, output, now)
	}
}

func (w *writer) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }
