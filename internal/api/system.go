package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/wispborne/notus-swap/internal/idlewait"
	"github.com/wispborne/notus-swap/internal/restart"
	"github.com/wispborne/notus-swap/internal/selfupdate"
	"github.com/wispborne/notus-swap/internal/service"
	"github.com/wispborne/notus-swap/internal/store"
)

// Retention is how long request and response bodies are kept. Metadata is
// kept for good either way.
type Retention struct {
	Days int   `json:"days"`
	GB   int64 `json:"gb"`
}

// RetentionSetting reads the retention setting, falling back to def.
func (a *API) RetentionSetting(ctx context.Context, def Retention) Retention {
	r := def
	if v, err := a.Store.Setting(ctx, "retention"); err == nil && v != "" {
		json.Unmarshal([]byte(v), &r)
	}
	if r.Days < 1 {
		r.Days = def.Days
	}
	if r.GB < 1 {
		r.GB = def.GB
	}
	return r
}

func (a *API) registerSystem(r *router) {
	r.handle("GET /notus/api/system", "Version, update state, changelog, service mode, retention and database size.", a.system)
	r.handle("POST /notus/api/update", "Update notus-swap and restart once no requests are in flight.", a.update)
	r.handle("POST /notus/api/update/rollback", "Roll notus-swap back to the previous version.", a.rollback)
	r.handle("POST /notus/api/restart/notus-swap", "Restart notus-swap once no requests are in flight.", a.restartSelf)
	r.handle("POST /notus/api/restart/notus-swap/now", "Restart notus-swap now, without waiting.", a.restartSelfNow)
	r.handle("POST /notus/api/restart/notus-swap/cancel", "Cancel a waiting restart, update or roll back.", a.restartSelfCancel)
	r.handle("POST /notus/api/restart/llama-swap", "Restart llama-swap.", a.restartLlamaSwap)
	r.handle("GET /notus/api/service/llama-swap", "How llama-swap is run (systemd, process or none) and whether it is running.", a.llamaSwapService)
	r.handle("POST /notus/api/start/llama-swap", "Start llama-swap.", a.startLlamaSwap)
	r.handle("POST /notus/api/stop/llama-swap", "Stop llama-swap.", a.stopLlamaSwap)
}

// SystemInfo is what the Settings page shows about updates and settings.
type SystemInfo struct {
	Version      string               `json:"version"`
	Configured   bool                 `json:"update_configured"`
	UpdateSource string               `json:"update_source,omitempty"` // such as "GitHub (wispborne/notus-swap)"
	Releases     []selfupdate.Release `json:"releases"`                // newest first, down to the running one
	Changelog    []selfupdate.Release `json:"changelog"`               // the newest releases, whatever is running
	CurrentKnown bool                 `json:"current_known"`
	ReleaseError string               `json:"release_error,omitempty"`
	HasPrevious  bool                 `json:"has_previous"`
	Pending      *selfupdate.Marker   `json:"pending,omitempty"`
	LastResult   *selfupdate.Result   `json:"last_result,omitempty"`
	Retention    Retention            `json:"retention"`
	// LlamaSwapService is how llama-swap is run: "systemd", "process", or ""
	// when notus-swap can't control it.
	LlamaSwapService string `json:"llama_swap_service"`
	// SelfRestart is what starts notus-swap again after a restart:
	// "systemd" or "supervisor".
	SelfRestart string           `json:"self_restart"`
	Database    *store.DiskUsage `json:"database,omitempty"`
	Restarting  *PendingRestart  `json:"restarting,omitempty"`
}

// system: GET /notus/api/system
func (a *API) system(w http.ResponseWriter, r *http.Request) {
	info := SystemInfo{
		Version: a.Version, Configured: a.Updater.Configured(), UpdateSource: a.Updater.Source(), HasPrevious: a.Updater.HasPrevious(),
		Pending: a.Updater.Pending(), LastResult: a.Updater.LastResult(), Releases: []selfupdate.Release{},
		Changelog:  []selfupdate.Release{},
		Retention:  a.RetentionSetting(r.Context(), a.RetentionDefault),
		Restarting: a.pendingRestart(), SelfRestart: a.SelfRestart,
	}
	if a.LlamaSwapService != nil {
		info.LlamaSwapService = a.LlamaSwapService.Kind()
	}
	if u, err := a.Store.DiskUsage(r.Context()); err == nil {
		info.Database = &u
	} else {
		a.Log.Warn("reading database size", "err", err)
	}
	if info.Configured {
		rs, err := a.Updater.Releases(r.Context())
		if err != nil {
			info.ReleaseError = err.Error()
		}
		info.Changelog = rs[:min(len(rs), 15)]
		// Releases newer than the running one, plus the running one itself.
		for _, rel := range rs {
			info.Releases = append(info.Releases, rel)
			if rel.Tag == a.Version {
				info.CurrentKnown = true
				break
			}
		}
		if !info.CurrentKnown && len(info.Releases) > 5 {
			info.Releases = info.Releases[:5]
		}
	}
	writeJSON(w, info)
}

// update: POST /notus/api/update {"tag": "..."}; an empty tag means the
// newest release. On success notus-swap restarts into the new version.
func (a *API) update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tag string `json:"tag"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if a.refuseWhileRestarting(w) {
		return
	}
	if body.Tag == "" {
		rs, err := a.Updater.Releases(r.Context())
		if err != nil || len(rs) == 0 {
			http.Error(w, "could not find the newest release: "+errText(err), http.StatusBadGateway)
			return
		}
		body.Tag = rs[0].Tag
	}
	if body.Tag == a.Version {
		http.Error(w, "that version is already running", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	if err := a.Updater.Install(ctx, body.Tag); err != nil {
		a.Log.Error("update failed", "tag", body.Tag, "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	a.Log.Info("update installed", "from", a.Version, "to", body.Tag)
	a.restartWhenIdle("update to "+body.Tag, a.Updater.UndoInstall)
	writeJSON(w, map[string]string{"installing": body.Tag})
}

// rollback: POST /notus/api/update/rollback
func (a *API) rollback(w http.ResponseWriter, r *http.Request) {
	if a.refuseWhileRestarting(w) {
		return
	}
	if err := a.Updater.Rollback(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	a.Log.Info("rolled back by hand")
	a.restartWhenIdle("roll back", a.Updater.UndoRollback)
	w.WriteHeader(http.StatusAccepted)
}

// restartSelf: POST /notus/api/restart/notus-swap
func (a *API) restartSelf(w http.ResponseWriter, r *http.Request) {
	a.restartWhenIdle("restart button", nil)
	w.WriteHeader(http.StatusAccepted)
}

// restartSelfNow: POST /notus/api/restart/notus-swap/now restarts without
// waiting for requests in flight; they are cut off after 10 seconds.
func (a *API) restartSelfNow(w http.ResponseWriter, r *http.Request) {
	if a.Restart == nil {
		http.Error(w, "restarting isn't available", http.StatusServiceUnavailable)
		return
	}
	a.Log.Info("restart without waiting asked for from the web UI")
	a.Restart.Now("restart now button")
	w.WriteHeader(http.StatusAccepted)
}

// restartSelfCancel: POST /notus/api/restart/notus-swap/cancel
// Restores the running binary before cancelling an update or rollback.
// Returns 409 if no restart is waiting.
func (a *API) restartSelfCancel(w http.ResponseWriter, r *http.Request) {
	if a.Restart == nil {
		http.Error(w, "restarting isn't available", http.StatusServiceUnavailable)
		return
	}
	reason := ""
	if p := a.Restart.Pending(); p != nil {
		reason = p.Reason
	}
	switch err := a.Restart.Cancel(); {
	case errors.Is(err, idlewait.ErrNotWaiting):
		http.Error(w, "no restart is waiting", http.StatusConflict)
	case err != nil:
		a.Log.Error("could not cancel the restart", "err", err)
		http.Error(w, "could not cancel the restart: "+err.Error(), http.StatusInternalServerError)
	default:
		a.Log.Info("restart cancelled from the web UI", "reason", reason)
		w.WriteHeader(http.StatusNoContent)
	}
}

// restartWhenIdle asks for a restart once no requests are in flight. The
// server keeps answering until then, and the answer to this request still
// goes out. systemd (Restart=always) or notus-swap's own supervisor starts
// notus-swap again.
// Cancel calls undo to reverse changes made for the restart.
func (a *API) restartWhenIdle(reason string, undo func() error) {
	if a.Restart == nil {
		return
	}
	if n := a.Hub.Count(); n > 0 {
		a.Log.Info("restart asked for; waiting for requests to finish", "reason", reason, "in_flight", n)
	} else {
		a.Log.Info("restart asked for", "reason", reason)
	}
	a.Restart.Ask(reason, undo)
}

// refuseWhileRestarting answers 409 if a restart is already waiting. The
// binary may have been swapped already, and swapping again would leave the
// wrong one as the previous version.
func (a *API) refuseWhileRestarting(w http.ResponseWriter) bool {
	if p := a.pendingRestart(); p != nil {
		http.Error(w, "a restart is already waiting for requests to finish ("+p.Reason+")", http.StatusConflict)
		return true
	}
	return false
}

// PendingRestart is a restart that is waiting for requests to finish.
type PendingRestart struct {
	restart.Pending
	InFlight int `json:"in_flight"`
}

func (a *API) pendingRestart() *PendingRestart {
	if a.Restart == nil {
		return nil
	}
	p := a.Restart.Pending()
	if p == nil {
		return nil
	}
	return &PendingRestart{Pending: *p, InFlight: a.Hub.Count()}
}

// llamaSwapControl runs one of the service actions, answering 503 when
// notus-swap has no way to control llama-swap.
func (a *API) llamaSwapControl(w http.ResponseWriter, r *http.Request, verb, done string, act func(service.Controller) error) {
	if a.LlamaSwapService == nil {
		http.Error(w, "notus-swap isn't set up to control llama-swap", http.StatusServiceUnavailable)
		return
	}
	if err := act(a.LlamaSwapService); err != nil {
		a.Log.Warn(verb+" llama-swap failed", "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	a.Log.Info("llama-swap " + done + " from the web UI")
	w.WriteHeader(http.StatusNoContent)
}

// restartLlamaSwap: POST /notus/api/restart/llama-swap
func (a *API) restartLlamaSwap(w http.ResponseWriter, r *http.Request) {
	a.llamaSwapControl(w, r, "restarting", "restarted", func(s service.Controller) error { return s.Restart(r.Context()) })
}

func (a *API) startLlamaSwap(w http.ResponseWriter, r *http.Request) {
	a.llamaSwapControl(w, r, "starting", "started", func(s service.Controller) error { return s.Start(r.Context()) })
}

func (a *API) stopLlamaSwap(w http.ResponseWriter, r *http.Request) {
	a.llamaSwapControl(w, r, "stopping", "stopped", func(s service.Controller) error { return s.Stop(r.Context()) })
}

func (a *API) llamaSwapService(w http.ResponseWriter, r *http.Request) {
	if a.LlamaSwapService == nil {
		http.Error(w, "notus-swap isn't set up to control llama-swap", http.StatusServiceUnavailable)
		return
	}
	state, err := a.LlamaSwapService.State(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"state": state})
}

func errText(err error) string {
	if err == nil {
		return "no releases"
	}
	return err.Error()
}
