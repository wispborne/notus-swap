package api

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"
)

func (a *API) registerModels(mux *http.ServeMux) {
	mux.HandleFunc("GET /notus/api/models", a.models)
	mux.HandleFunc("GET /notus/api/models/{model}/events", a.modelEvents)
	mux.HandleFunc("GET /notus/api/models/{model}/builds", a.modelBuilds)
	mux.HandleFunc("POST /notus/api/models/unload", a.unloadAll)
	mux.HandleFunc("GET /notus/api/logs/stream", a.logStream)
}

// models: GET /notus/api/models. notus-swap's own numbers for every model it
// has seen. The model list and states come from the status poll.
func (a *API) models(w http.ResponseWriter, r *http.Request) {
	stats, err := a.Store.ModelStats(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"stats": stats})
}

// modelEvents: GET /notus/api/models/{model}/events?limit=N, newest first.
func (a *API) modelEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	evs, err := a.Store.ModelEventsFor(r.Context(), r.PathValue("model"), limit)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, evs)
}

// modelBuilds: GET /notus/api/models/{model}/builds. The model's speed on
// each server build and command, most recent first, and the arguments of
// each command.
func (a *API) modelBuilds(w http.ResponseWriter, r *http.Request) {
	builds, err := a.Store.ModelBuilds(r.Context(), r.PathValue("model"))
	if err != nil {
		a.fail(w, err)
		return
	}
	var hashes []string
	for _, b := range builds {
		if b.CmdHash != "" && !slices.Contains(hashes, b.CmdHash) {
			hashes = append(hashes, b.CmdHash)
		}
	}
	cmds, err := a.Store.ModelCmds(r.Context(), hashes)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"rows": builds, "cmds": cmds})
}

// unloadAll: POST /notus/api/models/unload, passed on to llama-swap.
func (a *API) unloadAll(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", a.Upstream.JoinPath("api", "models", "unload").String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "Can't reach llama-swap", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		http.Error(w, "llama-swap answered "+resp.Status+": "+string(body), http.StatusBadGateway)
		return
	}
	a.unloadedByHand()
	w.WriteHeader(http.StatusNoContent)
}

// logStream: GET /notus/api/logs/stream. notus-swap's own log as plain text:
// what is kept in memory, then each new line as it is written. The same
// format as llama-swap's /logs/stream, so the Logs page reads both the same way.
func (a *API) logStream(w http.ResponseWriter, r *http.Request) {
	if a.Logs == nil {
		http.Error(w, "notus-swap log buffer unavailable", http.StatusServiceUnavailable)
		return
	}
	rc := http.NewResponseController(w)
	hist, writes, cancel := a.Logs.Subscribe()
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Write(hist)
	if rc.Flush() != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case p := <-writes:
			if _, err := w.Write(p); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
