package metrics

import (
	"context"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/gpu"
	"github.com/wispborne/notus-swap/internal/llamaswap"
	"github.com/wispborne/notus-swap/internal/rapl"
	"github.com/wispborne/notus-swap/internal/store"
)

func recorder(t *testing.T) *Recorder {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Recorder{Store: st, GPUs: gpu.NewSampler(t.TempDir()), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestAccountExtrapolates(t *testing.T) {
	r := recorder(t)
	t0 := time.Unix(1_800_000_000, 0)
	r.ring = []second{
		{at: t0.Add(1 * time.Second), gpuWatts: 200},
		{at: t0.Add(2 * time.Second), gpuWatts: 200},
	}
	// 2 s of samples, then 2 s past the newest one at its reading: 800 J.
	w := store.Work{ID: 1, Arrived: t0, Began: t0, Ended: t0.Add(4 * time.Second)}
	if j, q := r.Account(w); j == nil || math.Abs(*j-800) > 0.01 || q != 0 {
		t.Errorf("got %v J, queued %v", j, q)
	}
}

func TestAccountSplitsWithRequestsInFlight(t *testing.T) {
	r := recorder(t)
	t0 := time.Unix(1_800_000_000, 0)
	for i := 1; i <= 4; i++ {
		r.ring = append(r.ring, second{at: t0.Add(time.Duration(i) * time.Second), gpuWatts: 200})
	}
	// Request 2 is still in flight and has been worked on since t0+2.
	r.InFlight = func() []store.Work {
		return []store.Work{{ID: 1, Arrived: t0}, {ID: 2, Arrived: t0, Began: t0.Add(2 * time.Second)}}
	}
	// 2 s alone at 200 W, then 2 s shared: 400 + 200 = 600 J.
	w := store.Work{ID: 1, Arrived: t0, Began: t0, Ended: t0.Add(4 * time.Second)}
	if j, _ := r.Account(w); j == nil || math.Abs(*j-600) > 0.01 {
		t.Errorf("got %v J", j)
	}
}

func TestShare(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	// 100 W the whole time.
	power := []store.Power{{From: at(0), To: at(100), Watts: 100}}

	// One slot: A runs 0-30. B arrives at 1 and waits until A ends.
	a := store.Work{ID: 1, Arrived: at(0), Began: at(0), Ended: at(30)}
	b := store.Work{ID: 2, Arrived: at(1), Began: at(30), Ended: at(32)}
	if j, q := Share(power, a, []store.Work{b}); math.Abs(j-3000) > 0.01 || q != 0 {
		t.Errorf("A: %v J, queued %v", j, q)
	}
	if j, q := Share(power, b, []store.Work{a}); math.Abs(j-200) > 0.01 || q != 29*time.Second {
		t.Errorf("B: %v J, queued %v", j, q)
	}

	// Two slots: both run 0-10, then A alone until 20.
	a = store.Work{ID: 1, Arrived: at(0), Began: at(0), Ended: at(20)}
	b = store.Work{ID: 2, Arrived: at(0), Began: at(0), Ended: at(10)}
	if j, _ := Share(power, a, []store.Work{b}); math.Abs(j-1500) > 0.01 {
		t.Errorf("two slots A: %v J", j)
	}
	if j, _ := Share(power, b, []store.Work{a}); math.Abs(j-500) > 0.01 {
		t.Errorf("two slots B: %v J", j)
	}

	// A model load 0-10 while two requests wait: they split it. Then A
	// runs 10-20 while B waits, and B runs 20-25.
	a = store.Work{ID: 1, Arrived: at(0), Began: at(10), Ended: at(20)}
	b = store.Work{ID: 2, Arrived: at(0), Began: at(20), Ended: at(25)}
	if j, q := Share(power, a, []store.Work{b}); math.Abs(j-1500) > 0.01 || q != 0 {
		t.Errorf("load A: %v J, queued %v", j, q)
	}
	if j, q := Share(power, b, []store.Work{a}); math.Abs(j-1000) > 0.01 || q != 10*time.Second {
		t.Errorf("load B: %v J, queued %v", j, q)
	}
}

func TestLoadTimes(t *testing.T) {
	r := recorder(t)
	t0 := time.Unix(1_800_000_000, 0)
	r.OnTransition(llamaswap.Transition{Model: "qwen", From: "stopped", To: "starting", At: t0})
	r.OnTransition(llamaswap.Transition{Model: "qwen", From: "starting", To: "ready", At: t0.Add(14 * time.Second)})
	es, _ := r.Store.ModelEvents(context.Background(), t0.Add(-time.Minute), t0.Add(time.Minute))
	if len(es) != 2 || es[1].LoadMs == nil || *es[1].LoadMs != 14000 {
		t.Errorf("events %+v", es)
	}
}

func TestCPUFromSocketSensor(t *testing.T) {
	// A dedicated card plus AMD built-in graphics whose power sensor is labelled PPT.
	drm := t.TempDir()
	write := func(rel, v string) {
		p := filepath.Join(drm, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(v+"\n"), 0o644)
	}
	write("card0/device/vendor", "0x1002")
	write("card0/device/uevent", "PCI_SLOT_NAME=0000:03:00.0")
	write("card0/device/mem_info_vram_total", "34342961152")
	write("card0/device/hwmon/hwmon4/power1_average", "245000000")
	write("card1/device/vendor", "0x1002")
	write("card1/device/uevent", "PCI_SLOT_NAME=0000:0e:00.0")
	write("card1/device/mem_info_vram_total", "536870912")
	write("card1/device/hwmon/hwmon5/power1_input", "62000000")
	write("card1/device/hwmon/hwmon5/power1_label", "PPT")

	r := recorder(t)
	r.GPUs = gpu.NewSampler(drm)
	r.CPU = rapl.NewMeter(t.TempDir()) // no RAPL
	r.BaseWatts, r.PSUEff = 35, 0.9
	s := r.Sample(context.Background(), time.Now())
	if s.CPUSource != "ppt" || s.CPUWatts == nil || *s.CPUWatts != 62 || s.CPUProblem != "" {
		t.Fatalf("cpu %v source %q problem %q", s.CPUWatts, s.CPUSource, s.CPUProblem)
	}
	if s.GPUWatts == nil || *s.GPUWatts != 245 {
		t.Errorf("GPU watts should be the dedicated card only: %v", s.GPUWatts)
	}
	if want := (245.0 + 62 + 35) / 0.9; s.SystemWatts == nil || math.Abs(*s.SystemWatts-want) > 0.01 {
		t.Errorf("system %v, want %v", s.SystemWatts, want)
	}
}

func TestSampleSystemMemory(t *testing.T) {
	r := recorder(t)
	r.MemInfoPath = filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(r.MemInfoPath, []byte("MemTotal: 16384 kB\nMemAvailable: 6144 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := r.Sample(context.Background(), time.Now())
	if s.RAMUsed == nil || s.RAMTotal == nil || *s.RAMUsed != 10240*1024 || *s.RAMTotal != 16384*1024 {
		t.Fatalf("RAM used %v total %v", s.RAMUsed, s.RAMTotal)
	}
}

func TestRecount(t *testing.T) {
	ctx := context.Background()
	r := recorder(t)
	st := r.Store
	t0 := time.Unix(1_800_000_000, 0)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	for i := 1; i <= 40; i++ {
		w := 100.0
		if err := st.InsertSamples(ctx, at(i), []store.Sample{{Source: "gpus", Kind: "gpus", Watts: &w}}); err != nil {
			t.Fatal(err)
		}
	}
	add := func(start, end int, promptMs, predictedMs float64, energy float64) int64 {
		id, err := st.Begin(ctx, store.Begin{StartedAt: at(start), Method: "POST", Path: "/v1/chat/completions", Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Finish(ctx, id, store.Finish{FinishedAt: at(end), State: store.StateDone, StatusCode: 200,
			PromptMs: &promptMs, PredictedMs: &predictedMs, EnergyJ: &energy}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// A runs 0-30. B arrives at 1, waits, and runs 30-32. The old even
	// split gave each half of 1-30.
	a := add(0, 30, 1000, 29000, 1550)
	b := add(1, 32, 500, 1500, 1650)
	// C overlaps nothing and keeps its energy.
	c := add(35, 37, 500, 1500, 123)

	n, err := Recount(ctx, st, at(60))
	if err != nil || n != 2 {
		t.Fatalf("recounted %d, err %v", n, err)
	}
	for _, want := range []struct {
		id     int64
		energy float64
		queued int64
	}{{a, 3000, 0}, {b, 200, 29000}, {c, 123, 0}} {
		rec, err := st.Get(ctx, want.id)
		if err != nil {
			t.Fatal(err)
		}
		if rec.EnergyJ == nil || math.Abs(*rec.EnergyJ-want.energy) > 0.01 || rec.QueuedMs == nil || *rec.QueuedMs != want.queued {
			t.Errorf("request %d: energy %v, queued %v; want %v, %v", want.id, rec.EnergyJ, rec.QueuedMs, want.energy, want.queued)
		}
	}
	// It runs once.
	if n, _ := Recount(ctx, st, at(60)); n != 0 {
		t.Errorf("ran again: %d", n)
	}
}
