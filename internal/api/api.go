// Package api serves the JSON and live-feed endpoints under /notus/api/ that
// the web UI uses.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/wispborne/notus-swap/internal/autoload"
	"github.com/wispborne/notus-swap/internal/capture"
	"github.com/wispborne/notus-swap/internal/llamaconfig"
	"github.com/wispborne/notus-swap/internal/llamaswap"
	"github.com/wispborne/notus-swap/internal/llamaupdate"
	"github.com/wispborne/notus-swap/internal/logbuf"
	"github.com/wispborne/notus-swap/internal/metrics"
	"github.com/wispborne/notus-swap/internal/restart"
	"github.com/wispborne/notus-swap/internal/selfupdate"
	"github.com/wispborne/notus-swap/internal/store"
)

type API struct {
	Store     *store.Store
	Hub       *capture.Hub
	LlamaSwap *llamaswap.Monitor
	Metrics   *metrics.Recorder
	Config    *llamaconfig.Editor
	// ConfigHeld holds a config save until no requests are in flight.
	ConfigHeld *llamaconfig.Held
	Updater    *selfupdate.Updater
	// LlamaUpdates checks for and installs llama-swap and llama.cpp releases.
	LlamaUpdates *llamaupdate.Manager
	// Logs holds notus-swap's own recent log, for the Logs page.
	Logs *logbuf.Buffer
	// Restart shuts notus-swap down cleanly once no requests are in flight;
	// systemd starts it again.
	Restart *restart.Plan
	// PruneNow runs body retention at once (after its setting changes).
	PruneNow         func()
	RetentionDefault Retention
	LlamaSwapUnit    string
	// AutoLoad loads the default model after a while with nothing loaded.
	AutoLoad *autoload.Loader
	// Upstream is llama-swap's base URL, for loading and unloading models.
	Upstream *url.URL
	Log      *slog.Logger
	Version  string
	// Capture is the capturing proxy that inference requests go through.
	// Retried requests are sent through it.
	Capture http.Handler
}

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /notus/api/health", a.health)
	mux.HandleFunc("GET /notus/api/status", a.status)
	mux.HandleFunc("GET /notus/api/requests", a.list)
	mux.HandleFunc("GET /notus/api/requests/{id}", a.get)
	mux.HandleFunc("POST /notus/api/requests/{id}/cancel", a.cancel)
	mux.HandleFunc("POST /notus/api/requests/{id}/retry", a.retry)
	mux.HandleFunc("GET /notus/api/live", a.live)
	mux.HandleFunc("GET /notus/api/dashboard", a.dashboard)
	mux.HandleFunc("GET /notus/api/settings/{key}", a.getSetting)
	mux.HandleFunc("PUT /notus/api/settings/{key}", a.putSetting)
	mux.HandleFunc("POST /notus/api/models/{model}/load", a.loadModel)
	mux.HandleFunc("POST /notus/api/models/{model}/unload", a.unloadModel)
	a.registerConfig(mux)
	a.registerSystem(mux)
	a.registerLlamaUpdates(mux)
	a.registerModels(mux)
	a.registerAutoLoad(mux)
}

// Summary is one request without its bodies. Times are unix milliseconds.
type Summary struct {
	ID                 int64    `json:"id"`
	StartedAt          int64    `json:"started_at"`
	FirstByteAt        *int64   `json:"first_byte_at"`
	FirstTokenAt       *int64   `json:"first_token_at"`
	FinishedAt         *int64   `json:"finished_at"`
	Method             string   `json:"method"`
	Path               string   `json:"path"`
	Model              string   `json:"model"`
	Streaming          bool     `json:"streaming"`
	State              string   `json:"state"`
	StatusCode         *int     `json:"status_code"`
	Error              *string  `json:"error"`
	PromptTokens       *int64   `json:"prompt_tokens"`
	CompletionTokens   *int64   `json:"completion_tokens"`
	CachedTokens       *int64   `json:"cached_tokens"`
	PromptMs           *float64 `json:"prompt_ms"`
	PredictedMs        *float64 `json:"predicted_ms"`
	PromptPerSecond    *float64 `json:"prompt_per_second"`
	PredictedPerSecond *float64 `json:"predicted_per_second"`
	EnergyJ            *float64 `json:"energy_j"`
	// QueuedMs is how long the request waited while the server worked on
	// other requests. Nil for requests stored before it was measured.
	QueuedMs      *int64  `json:"queued_ms"`
	RequestBytes  int64   `json:"request_bytes"`
	ResponseBytes int64   `json:"response_bytes"`
	Preview       string  `json:"preview"`
	HasBodies     bool    `json:"has_bodies"`
	FinishReason  *string `json:"finish_reason"`
	NCtx          *int64  `json:"n_ctx"` // the model's context size per slot, when known
	Build         *string `json:"build"` // the server's build, from system_fingerprint
	// Tags is a list of capture.Tag, or null if the request has not been tagged.
	Tags json.RawMessage `json:"tags"`
	// Issues lists the kinds of issue found in the output, leaving out muted ones.
	Issues []string `json:"issues"`
	// RetryOf is the request this one was sent again from, or null.
	RetryOf *int64 `json:"retry_of"`
	// ClientIP and UserAgent say where the request came from, or null for
	// requests stored before they were kept.
	ClientIP  *string `json:"client_ip"`
	UserAgent *string `json:"user_agent"`
}

func summary(r *store.Record) Summary {
	ms := func(t *time.Time) *int64 {
		if t == nil {
			return nil
		}
		v := t.UnixMilli()
		return &v
	}
	var tags json.RawMessage
	if r.Tags != nil {
		tags = json.RawMessage(*r.Tags)
	}
	return Summary{
		Tags: tags, ID: r.ID, StartedAt: r.StartedAt.UnixMilli(), FirstByteAt: ms(r.FirstByteAt),
		FirstTokenAt: ms(r.FirstTokenAt), FinishedAt: ms(r.FinishedAt), Method: r.Method, Path: r.Path,
		Model: r.Model, Streaming: r.Streaming, State: r.State, StatusCode: r.StatusCode, Error: r.Error,
		PromptTokens: r.PromptTokens, CompletionTokens: r.CompletionTokens, CachedTokens: r.CachedTokens, PromptMs: r.PromptMs,
		PredictedMs: r.PredictedMs, PromptPerSecond: r.PromptPerSecond, PredictedPerSecond: r.PredictedPerSecond,
		EnergyJ: r.EnergyJ, QueuedMs: r.QueuedMs, RequestBytes: r.RequestBytes, ResponseBytes: r.ResponseBytes, Preview: r.Preview, HasBodies: r.HasBodies,
		FinishReason: r.FinishReason, NCtx: r.NCtx, Build: r.Build, Issues: r.Issues, RetryOf: r.RetryOf,
		ClientIP: r.ClientIP, UserAgent: r.UserAgent,
	}
}

// health: GET /notus/api/health. "restarting" is set while a restart waits
// for requests to finish; the Settings page uses it to tell the old process
// from the new one.
func (a *API) health(w http.ResponseWriter, r *http.Request) {
	h := map[string]any{"ok": true, "version": a.Version}
	if p := a.pendingRestart(); p != nil {
		h["restarting"] = p
	}
	writeJSON(w, h)
}

// list: GET /notus/api/requests?before=ID&limit=N&model=M&state=S&q=TEXT&issue=KIND
// issue=any keeps requests with any issue. The first page (no "before") also
// lists every model seen, for the filter.
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	mutes, err := a.Store.IssueMutes(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	recs, err := a.Store.List(r.Context(), store.ListOptions{
		BeforeID: before, Limit: limit, Model: q.Get("model"), State: q.Get("state"), Search: q.Get("q"),
		Issue: q.Get("issue"), Mutes: mutes,
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	out := struct {
		Requests []Summary `json:"requests"`
		Models   []string  `json:"models,omitempty"`
	}{Requests: make([]Summary, len(recs))}
	for i := range recs {
		out.Requests[i] = summary(&recs[i])
	}
	if before == 0 {
		if out.Models, err = a.Store.Models(r.Context()); err != nil {
			a.fail(w, err)
			return
		}
	}
	writeJSON(w, out)
}

// Detail is one request with its bodies.
type Detail struct {
	Summary
	// Request is the request body as JSON, or a JSON string if it isn't JSON.
	Request json.RawMessage `json:"request"`
	// ResponseRaw is the response body as sent, for the raw view.
	ResponseRaw string         `json:"response_raw"`
	Response    capture.Parsed `json:"response"`
	Truncated   bool           `json:"truncated"`
	// Live is set while the request is in flight. Response then holds the
	// output so far, and Chunks counts it.
	Live   bool `json:"live"`
	Chunks int  `json:"chunks,omitempty"`
	// IssueDetails is every issue found, muted ones included and marked.
	IssueDetails []IssueOut `json:"issue_details"`
}

// IssueOut is one issue found in a request's output.
type IssueOut struct {
	store.Issue
	Muted bool `json:"muted"`
}

// get: GET /notus/api/requests/{id}
func (a *API) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid request ID", http.StatusBadRequest)
		return
	}
	rec, err := a.Store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	d := Detail{Summary: summary(rec), Truncated: rec.Truncated, Request: asJSON(rec.RequestBody)}
	issues, err := a.Store.Issues(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	mutes, err := a.Store.IssueMutes(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	d.Issues, d.IssueDetails = []string{}, []IssueOut{}
	for _, is := range issues {
		muted := slices.Contains(mutes, store.IssueMute{Kind: is.Kind, Model: rec.Model})
		d.IssueDetails = append(d.IssueDetails, IssueOut{Issue: is, Muted: muted})
		if !muted {
			d.Issues = append(d.Issues, is.Kind)
		}
	}
	if f, ok := a.Hub.Get(id); ok && rec.State == store.StateInFlight {
		d.Live, d.Response, d.Chunks = true, f.Output, f.Chunks
		if f.FirstTokenAt != 0 {
			d.FirstTokenAt = &f.FirstTokenAt
		}
	} else {
		d.ResponseRaw = string(rec.ResponseBody)
		d.Response = capture.ParseStored(rec.ResponseBody)
	}
	writeJSON(w, d)
}

// cancel: POST /notus/api/requests/{id}/cancel ends a request in flight. Its
// client gets an error, or a stream that stops.
func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid request ID", http.StatusBadRequest)
		return
	}
	if !a.Hub.Cancel(id) {
		http.Error(w, "That request is not in flight", http.StatusConflict)
		return
	}
	a.Log.Info("request cancelled from the web UI", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

// retry: POST /notus/api/requests/{id}/retry sends a finished request's
// stored body again. The new request is captured like any other, and its ID
// is returned as {"id": N}.
func (a *API) retry(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid request ID", http.StatusBadRequest)
		return
	}
	rec, err := a.Store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "Request not found", http.StatusNotFound)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	switch {
	case rec.State == store.StateInFlight:
		http.Error(w, "That request is still in flight", http.StatusConflict)
		return
	case !rec.HasBodies:
		http.Error(w, "That request's body has been deleted by retention", http.StatusGone)
		return
	case int64(len(rec.RequestBody)) < rec.RequestBytes:
		http.Error(w, "That request's body was too large to store in full", http.StatusConflict)
		return
	}
	newID, err := capture.Retry(a.Capture, rec.Method, rec.Path, rec.RequestBody, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.Log.Info("request retried from the web UI", "id", id, "new_id", newID)
	writeJSON(w, map[string]int64{"id": newID})
}

// live: GET /notus/api/live, a server-sent event stream. The first event,
// "hello", lists the requests in flight with their output so far. After that
// each event is a capture.Event (start, delta, finish).
func (a *API) live(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	events, inFlight, cancel := a.Hub.Subscribe()
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	if inFlight == nil {
		inFlight = []capture.Flight{}
	}
	if !sendEvent(w, rc, map[string]any{"type": "hello", "in_flight": inFlight}) {
		return
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-events:
			if !ok {
				return // fell behind; the browser reconnects and gets a fresh hello
			}
			if !sendEvent(w, rc, e) {
				return
			}
		case <-ping.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

func sendEvent(w http.ResponseWriter, rc *http.ResponseController, v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	if _, err := w.Write(append(append([]byte("data: "), b...), '\n', '\n')); err != nil {
		return false
	}
	return rc.Flush() == nil
}

func asJSON(b []byte) json.RawMessage {
	if len(b) > 0 && json.Valid(b) {
		return b
	}
	s, _ := json.Marshal(string(b))
	return s
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func (a *API) fail(w http.ResponseWriter, err error) {
	a.Log.Error("api", "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
