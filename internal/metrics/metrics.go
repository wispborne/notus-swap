// Package metrics turns each second's sensor readings into stored samples,
// keeps the latest values for the status bar, records model loads, and
// works out how much GPU energy each request used.
package metrics

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/wispborne/notus-swap/internal/gpu"
	"github.com/wispborne/notus-swap/internal/llamaswap"
	"github.com/wispborne/notus-swap/internal/memory"
	"github.com/wispborne/notus-swap/internal/rapl"
	"github.com/wispborne/notus-swap/internal/store"
)

// Snapshot is the latest reading of everything.
type Snapshot struct {
	At       time.Time
	GPUs     []gpu.Reading
	GPUWatts *float64 // dedicated cards added up
	CPUWatts *float64
	// CPUSource says where CPUWatts came from: "rapl" (the CPU's energy
	// counter) or "ppt" (the built-in graphics' socket power sensor).
	CPUSource  string
	CPUProblem string
	// SystemWatts estimates the whole machine at the wall:
	// (GPUWatts + CPUWatts + BaseWatts) / PSUEfficiency. Built-in graphics
	// are inside the CPU socket reading already. Nil without a CPU reading.
	SystemWatts *float64
	RAMUsed     *int64
	RAMTotal    *int64
}

// second is one entry of the energy ring: GPU power over the second
// before at.
type second struct {
	at       time.Time
	gpuWatts float64
}

type Recorder struct {
	Store *store.Store
	GPUs  *gpu.Sampler
	CPU   *rapl.Meter
	// InFlight lists the requests in flight, for splitting energy between them.
	InFlight    func() []store.Work
	Log         *slog.Logger
	BaseWatts   float64
	PSUEff      float64
	MemInfoPath string // defaults to /proc/meminfo

	mu       sync.Mutex
	latest   Snapshot
	ring     []second     // the last hour, oldest first
	finished []store.Work // requests finished recently, for Account
	starting map[string]time.Time
}

const ringLen = 3600

// Run samples every second and rolls stored samples up every minute, until
// ctx ends.
func (r *Recorder) Run(ctx context.Context) {
	if err := r.Store.Rollup(ctx, time.Now(), 30*24*time.Hour); err != nil {
		r.Log.Error("rolling up samples", "err", err)
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastRollup := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			r.Sample(ctx, now)
			if now.Sub(lastRollup) >= time.Minute {
				lastRollup = now
				if err := r.Store.Rollup(ctx, now, 3*time.Hour); err != nil && ctx.Err() == nil {
					r.Log.Error("rolling up samples", "err", err)
				}
			}
		}
	}
}

// Sample reads every sensor once, stores the readings, and updates Latest.
func (r *Recorder) Sample(ctx context.Context, now time.Time) Snapshot {
	s := Snapshot{At: now, GPUs: r.GPUs.Sample()}
	memInfoPath := r.MemInfoPath
	if memInfoPath == "" {
		memInfoPath = "/proc/meminfo"
	}
	s.RAMUsed, s.RAMTotal = memory.Read(memInfoPath)
	if r.CPU != nil {
		r.CPU.Sample()
		s.CPUWatts, s.CPUProblem = r.CPU.Watts()
		if s.CPUWatts != nil {
			s.CPUSource = "rapl"
		}
	}
	// Without RAPL, AMD built-in graphics' PPT sensor gives the same thing:
	// the whole CPU socket's power.
	if s.CPUWatts == nil {
		for _, g := range s.GPUs {
			if g.SocketPower() && g.Watts != nil {
				w := *g.Watts
				s.CPUWatts, s.CPUSource, s.CPUProblem = &w, "ppt", ""
				break
			}
		}
	}
	var rows []store.Sample
	for _, g := range s.GPUs {
		row := store.Sample{Source: "gpu:" + id(g), Kind: "gpu", Watts: g.Watts, TempC: g.TempC,
			VRAMUsed: fp(g.VRAMUsed), VRAMTotal: fp(g.VRAMTotal), Busy: g.BusyPercent}
		if g.Integrated {
			row.Source, row.Kind = "igpu:"+id(g), "igpu"
		} else if g.Watts != nil {
			s.GPUWatts = add(s.GPUWatts, *g.Watts)
		}
		rows = append(rows, row)
	}
	if s.CPUWatts != nil && r.PSUEff > 0 {
		dc := *s.CPUWatts + r.BaseWatts
		if s.GPUWatts != nil {
			dc += *s.GPUWatts
		}
		wall := dc / r.PSUEff
		s.SystemWatts = &wall
	}
	if s.GPUWatts != nil {
		rows = append(rows, store.Sample{Source: "gpus", Kind: "gpus", Watts: s.GPUWatts})
	}
	if s.CPUWatts != nil {
		rows = append(rows, store.Sample{Source: "cpu", Kind: "cpu", Watts: s.CPUWatts})
	}
	if s.SystemWatts != nil {
		rows = append(rows, store.Sample{Source: "system", Kind: "system", Watts: s.SystemWatts})
	}
	if len(rows) > 0 {
		if err := r.Store.InsertSamples(ctx, now, rows); err != nil && ctx.Err() == nil {
			r.Log.Error("storing samples", "err", err)
		}
	}

	r.mu.Lock()
	r.latest = s
	if s.GPUWatts != nil {
		r.ring = append(r.ring, second{at: now, gpuWatts: *s.GPUWatts})
		if len(r.ring) > ringLen {
			r.ring = r.ring[len(r.ring)-ringLen:]
		}
	}
	r.mu.Unlock()
	return s
}

func (r *Recorder) Latest() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.latest
}

// Account works out a finished request's GPU energy and how long it waited
// behind other requests, using Share. Time after the newest sample uses that
// sample's reading. The energy is nil when there are no GPU readings.
func (r *Recorder) Account(w store.Work) (*float64, time.Duration) {
	var others []store.Work
	if r.InFlight != nil {
		for _, o := range r.InFlight() {
			if o.ID != w.ID {
				others = append(others, o)
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Keep requests that could still overlap one in flight. The ring only
	// covers an hour, so older ones would not change any energy.
	r.finished = slices.DeleteFunc(r.finished, func(f store.Work) bool { return w.Ended.Sub(f.Ended) > 2*time.Hour })
	others = append(others, r.finished...)
	r.finished = append(r.finished, w)

	power := make([]store.Power, 0, len(r.ring)+1)
	for _, s := range r.ring {
		power = append(power, store.Power{From: s.at.Add(-time.Second), To: s.at, Watts: s.gpuWatts})
	}
	if n := len(r.ring); n > 0 && w.Ended.After(r.ring[n-1].at) {
		last := r.ring[n-1]
		power = append(power, store.Power{From: last.at, To: w.Ended, Watts: last.gpuWatts})
	}
	j, queued := Share(power, w, others)
	if len(r.ring) == 0 {
		return nil, queued
	}
	return &j, queued
}

// OnTransition records a model changing state. A load is timed from
// "starting" to "ready".
func (r *Recorder) OnTransition(t llamaswap.Transition) {
	e := store.ModelEvent{At: t.At.UnixMilli(), Model: t.Model, From: t.From, To: t.To, Auto: t.Auto}
	r.mu.Lock()
	if r.starting == nil {
		r.starting = map[string]time.Time{}
	}
	switch t.To {
	case "starting":
		r.starting[t.Model] = t.At
	case "ready":
		if began, ok := r.starting[t.Model]; ok {
			ms := t.At.Sub(began).Milliseconds()
			e.LoadMs = &ms
		}
		delete(r.starting, t.Model)
	default:
		delete(r.starting, t.Model)
	}
	r.mu.Unlock()
	if err := r.Store.AddModelEvent(context.Background(), e); err != nil {
		r.Log.Error("storing model event", "err", err)
	}
}

// id names a card by its PCI address, which stays the same across reboots
// (card numbers can change).
func id(g gpu.Reading) string {
	if g.PCI != "" {
		return g.PCI
	}
	return g.Card
}

func fp(v *int64) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v)
	return &f
}

func add(total *float64, v float64) *float64 {
	if total != nil {
		v += *total
	}
	return &v
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
