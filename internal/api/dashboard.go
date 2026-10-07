package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/wispborne/notus-swap/internal/gpu"
	"github.com/wispborne/notus-swap/internal/llamaswap"
	"github.com/wispborne/notus-swap/internal/store"
)

// Status is what the status bar shows.
type Status struct {
	Version   string           `json:"version"`
	LlamaSwap llamaswap.Status `json:"llama_swap"`
	GPUs      []gpu.Reading    `json:"gpus"`
	// TotalWatts adds up every card, built-in graphics included, except a
	// card whose sensor reads the whole CPU socket (that is CPU power).
	TotalWatts *float64 `json:"total_watts"`
	InFlight   int      `json:"in_flight"`
	CPUWatts   *float64 `json:"cpu_watts"`
	CPUSource  string   `json:"cpu_source,omitempty"` // "rapl" or "ppt"
	CPUProblem string   `json:"cpu_problem,omitempty"`
	// SystemWatts estimates the whole machine at the wall. See metrics.Snapshot.
	SystemWatts     *float64 `json:"system_watts"`
	SystemBaseWatts float64  `json:"system_base_watts"`
	PSUEfficiency   float64  `json:"psu_efficiency"`
	RAMUsed         *int64   `json:"ram_used"`
	RAMTotal        *int64   `json:"ram_total"`
	// Privacy rides along so every open page learns of changes within 2 s.
	Privacy Privacy `json:"privacy"`
	// UpdateAvailable is true when the last check found a newer release of
	// notus-swap, llama-swap, or llama.cpp. The sidebar puts a dot on System.
	UpdateAvailable bool `json:"update_available"`
}

// status: GET /notus/api/status. It answers from values the background
// monitors keep, so polling it is cheap.
func (a *API) status(w http.ResponseWriter, r *http.Request) {
	snap := a.Metrics.Latest()
	s := Status{
		Version: a.Version, LlamaSwap: a.LlamaSwap.Status(), GPUs: snap.GPUs, InFlight: a.Hub.Count(),
		CPUWatts: snap.CPUWatts, CPUSource: snap.CPUSource, CPUProblem: snap.CPUProblem, SystemWatts: snap.SystemWatts,
		SystemBaseWatts: a.Metrics.BaseWatts, PSUEfficiency: a.Metrics.PSUEff, Privacy: a.privacy(r.Context()),
		RAMUsed: snap.RAMUsed, RAMTotal: snap.RAMTotal, UpdateAvailable: a.Updater != nil && a.Updater.UpdateAvailable() || a.LlamaUpdates != nil && a.LlamaUpdates.UpdateAvailable(),
	}
	if s.GPUs == nil {
		s.GPUs = []gpu.Reading{}
	}
	for _, g := range s.GPUs {
		if g.Watts != nil && !g.SocketPower() {
			t := *g.Watts
			if s.TotalWatts != nil {
				t += *s.TotalWatts
			}
			s.TotalWatts = &t
		}
	}
	writeJSON(w, s)
}

var ranges = map[string]time.Duration{
	"15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour,
	"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour,
}

// Dashboard is everything the Dashboard page draws, for one time range.
type Dashboard struct {
	From    int64 `json:"from"` // unix ms
	To      int64 `json:"to"`
	BucketS int64 `json:"bucket_s"`
	// Series holds every source's samples, one Point per source per bucket.
	Series []store.Point `json:"series"`
	// Cards names each "gpu:<pci>" source, from the latest reading.
	Cards       []Card             `json:"cards"`
	Requests    []Summary          `json:"requests"`
	ModelEvents []store.ModelEvent `json:"model_events"`
	Energy      Energy             `json:"energy"`
}

type Card struct {
	Source     string `json:"source"`
	Card       string `json:"card"`
	Name       string `json:"name,omitempty"`
	Integrated bool   `json:"integrated"`
}

// Energy is in watt-hours. "Today" starts at local midnight on the server.
type Energy struct {
	TodayGPU    *float64 `json:"today_gpu_wh"`
	TodaySystem *float64 `json:"today_system_wh"`
	RangeGPU    *float64 `json:"range_gpu_wh"`
	RangeSystem *float64 `json:"range_system_wh"`
}

// dashboard: GET /notus/api/dashboard?range=1h (15m, 1h, 6h, 24h, 7d, 30d,
// or all: from the oldest thing stored until now)
func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	span, ok := ranges[r.URL.Query().Get("range")]
	if !ok {
		span = time.Hour
	}
	if r.URL.Query().Get("range") == "all" {
		first, err := a.Store.Earliest(ctx)
		if err != nil {
			a.fail(w, err)
			return
		}
		span = max(now.Sub(first), 15*time.Minute)
	}
	from := now.Add(-span)
	d := Dashboard{From: from.UnixMilli(), To: now.UnixMilli(), Cards: []Card{}, Requests: []Summary{}}

	var err error
	// About 600 buckets across the range.
	if d.Series, d.BucketS, err = a.Store.Series(ctx, now, from, now, int64(span.Seconds())/600); err != nil {
		a.fail(w, err)
		return
	}
	for _, g := range a.Metrics.Latest().GPUs {
		src := "gpu:"
		if g.Integrated {
			src = "igpu:"
		}
		if g.PCI != "" {
			src += g.PCI
		} else {
			src += g.Card
		}
		d.Cards = append(d.Cards, Card{Source: src, Card: g.Card, Name: g.Name, Integrated: g.Integrated})
	}

	recs, err := a.Store.List(ctx, store.ListOptions{Since: from, Until: now, Limit: 20000})
	if err != nil {
		a.fail(w, err)
		return
	}
	for i := range recs {
		s := summary(&recs[i])
		s.Preview = "" // not drawn; keeps the answer small
		d.Requests = append(d.Requests, s)
	}
	if d.ModelEvents, err = a.Store.ModelEvents(ctx, from, now); err != nil {
		a.fail(w, err)
		return
	}

	y, m, day := now.Date()
	midnight := time.Date(y, m, day, 0, 0, 0, 0, now.Location())
	energy := func(since time.Time, source string) *float64 {
		wh, err := a.Store.EnergyWh(ctx, now, since, now, source)
		if err != nil {
			a.Log.Error("energy", "err", err)
		}
		return wh
	}
	d.Energy = Energy{
		TodayGPU: energy(midnight, "gpus"), TodaySystem: energy(midnight, "system"),
		RangeGPU: energy(from, "gpus"), RangeSystem: energy(from, "system"),
	}
	writeJSON(w, d)
}

// Settings the UI may read and write. Values are JSON.
var settingKeys = map[string]bool{"dashboard_layout": true, "hidden_models": true, "show_hidden": true, "retention": true, "issue_mutes": true}

// Privacy is which models the UI keeps off screen. See CLAUDE.md, "Hidden
// models". Everything is still captured; only the UI filters.
type Privacy struct {
	HiddenModels []string `json:"hidden_models"`
	ShowHidden   bool     `json:"show_hidden"`
}

func (a *API) privacy(ctx context.Context) Privacy {
	p := Privacy{HiddenModels: []string{}}
	if v, err := a.Store.Setting(ctx, "hidden_models"); err == nil && v != "" {
		json.Unmarshal([]byte(v), &p.HiddenModels)
	}
	if v, err := a.Store.Setting(ctx, "show_hidden"); err == nil && v != "" {
		json.Unmarshal([]byte(v), &p.ShowHidden)
	}
	if p.HiddenModels == nil {
		p.HiddenModels = []string{}
	}
	return p
}

// getSetting: GET /notus/api/settings/{key}. Returns null when unset.
func (a *API) getSetting(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !settingKeys[key] {
		http.Error(w, "Setting not found", http.StatusNotFound)
		return
	}
	v, err := a.Store.Setting(r.Context(), key)
	if err != nil {
		a.fail(w, err)
		return
	}
	if v == "" {
		v = "null"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(v))
}

// putSetting: PUT /notus/api/settings/{key} with a JSON body.
func (a *API) putSetting(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !settingKeys[key] {
		http.Error(w, "Setting not found", http.StatusNotFound)
		return
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || !json.Valid(b) {
		http.Error(w, "Expected a JSON request body", http.StatusBadRequest)
		return
	}
	if err := a.Store.SetSetting(r.Context(), key, string(b)); err != nil {
		a.fail(w, err)
		return
	}
	if key == "retention" && a.PruneNow != nil {
		a.PruneNow()
	}
	w.WriteHeader(http.StatusNoContent)
}

// loadModel: POST /notus/api/models/{model}/load. llama-swap loads a model
// when a request for it arrives, so this sends one (to llama-server's
// /health) in the background and answers at once.
func (a *API) loadModel(w http.ResponseWriter, r *http.Request) {
	model := r.PathValue("model")
	go func() {
		if err := a.LoadModel(context.Background(), model); err != nil {
			a.Log.Warn("loading model", "model", model, "err", err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

// LoadModel asks llama-swap to load a model, and returns once it is loaded
// or has failed, waiting at most 10 minutes.
func (a *API) LoadModel(ctx context.Context, model string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", a.Upstream.JoinPath("upstream", model, "health").String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("llama-swap answered %s: %s", resp.Status, body)
	}
	return nil
}

// unloadModel: POST /notus/api/models/{model}/unload
func (a *API) unloadModel(w http.ResponseWriter, r *http.Request) {
	model := r.PathValue("model")
	req, _ := http.NewRequestWithContext(r.Context(), "POST", a.Upstream.JoinPath("api", "models", "unload", model).String(), nil)
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
