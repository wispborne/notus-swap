package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/wispborne/notus-swap/internal/store"
)

func (a *API) registerModels(mux *http.ServeMux) {
	mux.HandleFunc("GET /notus/api/models", a.models)
	mux.HandleFunc("GET /notus/api/models/{model}/events", a.modelEvents)
	mux.HandleFunc("GET /notus/api/models/{model}/builds", a.modelBuilds)
	mux.HandleFunc("GET /notus/api/speed-filter", a.getSpeedFilter)
	mux.HandleFunc("PUT /notus/api/speed-filter", a.putSpeedFilter)
	mux.HandleFunc("POST /notus/api/models/unload", a.unloadAll)
	mux.HandleFunc("PUT /notus/api/models/{model}/mark", a.putMark)
	mux.HandleFunc("GET /notus/api/logs/stream", a.logStream)
}

// models: GET /notus/api/models. notus-swap's own numbers for every model it
// has seen, by model ID: requests that used an alias count toward its model.
// The model list and states come from the status poll.
func (a *API) models(w http.ResponseWriter, r *http.Request) {
	stats, err := a.Store.ModelStats(r.Context(), a.SpeedFilter(r.Context()), a.modelID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"stats": stats})
}

// modelEvents: GET /notus/api/models/{model}/events?limit=N, newest first.
func (a *API) modelEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	evs, err := a.Store.ModelEventsFor(r.Context(), a.modelID(r.PathValue("model")), limit)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, evs)
}

// modelBuilds: GET /notus/api/models/{model}/builds. The model's speed on
// each server build and command, most recent first, and the arguments of
// each command. ?filter=<JSON> uses that SpeedFilter instead of the saved
// one, so the Speed tab can show a change before it is saved.
func (a *API) modelBuilds(w http.ResponseWriter, r *http.Request) {
	f := a.SpeedFilter(r.Context())
	if q := r.URL.Query().Get("filter"); q != "" {
		f = store.DefaultSpeedFilter()
		if err := json.Unmarshal([]byte(q), &f); err != nil || !f.Valid() {
			http.Error(w, "Expected filter to be a valid speed filter", http.StatusBadRequest)
			return
		}
	}
	builds, err := a.Store.ModelBuilds(r.Context(), a.modelNames(r.PathValue("model")), f)
	if err != nil {
		a.fail(w, err)
		return
	}
	var hashes []string
	for _, b := range builds.Rows {
		if b.CmdHash != "" && !slices.Contains(hashes, b.CmdHash) {
			hashes = append(hashes, b.CmdHash)
		}
	}
	cmds, err := a.Store.ModelCmds(r.Context(), hashes)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, map[string]any{"builds": builds, "cmds": cmds})
}

// SpeedFilter is the speed_filter setting: which requests count toward
// speeds. It is the default until one is saved.
func (a *API) SpeedFilter(ctx context.Context) store.SpeedFilter {
	f := store.DefaultSpeedFilter()
	if v, err := a.Store.Setting(ctx, "speed_filter"); err == nil && v != "" {
		saved := store.DefaultSpeedFilter()
		if json.Unmarshal([]byte(v), &saved) == nil && saved.Valid() {
			f = saved
		}
	}
	if f.ExcludeClients == nil {
		f.ExcludeClients = []string{}
	}
	return f
}

// getSpeedFilter: GET /notus/api/speed-filter. The filter in use and the default.
func (a *API) getSpeedFilter(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"filter": a.SpeedFilter(r.Context()), "default": store.DefaultSpeedFilter()})
}

// putSpeedFilter: PUT /notus/api/speed-filter with a SpeedFilter.
func (a *API) putSpeedFilter(w http.ResponseWriter, r *http.Request) {
	f := store.DefaultSpeedFilter()
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&f); err != nil || !f.Valid() {
		http.Error(w, "Expected a speed filter: token counts of 0 or more, a context range whose end is after its start, and structured set to \"\", \"exclude\" or \"only\"", http.StatusBadRequest)
		return
	}
	if f.ExcludeClients == nil {
		f.ExcludeClients = []string{}
	}
	b, _ := json.Marshal(f)
	if err := a.Store.SetSetting(r.Context(), "speed_filter", string(b)); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
