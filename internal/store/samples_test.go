package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func f(v float64) *float64 { return &v }

func TestSamplesRollupSeriesAndEnergy(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Two minutes of 1-second samples, starting on a minute boundary:
	// 100 W for the first minute, 200 W for the second.
	start := time.Unix(1_800_000_000/60*60, 0)
	for i := 0; i < 120; i++ {
		w := 100.0
		if i >= 60 {
			w = 200
		}
		if err := st.InsertSamples(ctx, start.Add(time.Duration(i)*time.Second), []Sample{
			{Source: "gpus", Kind: "gpus", Watts: f(w)},
			{Source: "gpu:0000:03:00.0", Kind: "gpu", Watts: f(w), TempC: f(60), VRAMTotal: f(32e9)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	now := start.Add(125 * time.Second)

	// Energy: 60 s at 100 W + 60 s at 200 W = 5 Wh.
	wh, err := st.EnergyWh(ctx, now, start, now, "gpus")
	if err != nil || wh == nil || *wh < 4.99 || *wh > 5.01 {
		t.Fatalf("energy %v err %v", wh, err)
	}

	// Series in 60-second buckets from the 1-second tier.
	pts, bucket, err := st.Series(ctx, now, start, now, 60)
	if err != nil || bucket != 60 {
		t.Fatalf("bucket %d err %v", bucket, err)
	}
	var gpus []float64
	for _, p := range pts {
		if p.Source == "gpus" {
			gpus = append(gpus, *p.Watts)
		}
	}
	if len(gpus) != 2 || gpus[0] != 100 || gpus[1] != 200 {
		t.Errorf("series %v", gpus)
	}
	// Oldest bucket first, and sources in name order within a bucket.
	if len(pts) != 4 || pts[0].Source != "gpu:0000:03:00.0" || pts[1].Source != "gpus" || pts[0].T != pts[1].T || pts[2].T <= pts[1].T ||
		pts[0].TempC == nil || *pts[0].TempC != 60 || pts[0].Kind != "gpu" || pts[1].Kind != "gpus" {
		t.Errorf("series order %+v", pts)
	}

	// Rollup makes minute rows; running it twice changes nothing.
	for range 2 {
		if err := st.Rollup(ctx, now, 3*time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	var avg float64
	st.db.QueryRow(`SELECT COUNT(*), AVG(watts) FROM samples WHERE tier = 60 AND source = 'gpus'`).Scan(&n, &avg)
	if n != 2 || avg != 150 {
		t.Errorf("minute rows %d, average %v", n, avg)
	}

	// A day later the 1-second rows are gone, and energy comes from minute rows.
	later := now.Add(25 * time.Hour)
	if err := st.Rollup(ctx, later, time.Hour); err != nil {
		t.Fatal(err)
	}
	st.db.QueryRow(`SELECT COUNT(*) FROM samples WHERE tier = 1`).Scan(&n)
	if n != 0 {
		t.Errorf("%d one-second rows left after a day", n)
	}
	wh, _ = st.EnergyWh(ctx, later, start, later, "gpus")
	if wh == nil || *wh < 4.99 || *wh > 5.01 {
		t.Errorf("energy from minute rows %v", wh)
	}
}

func TestModelEventsAndSettings(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	load := int64(12000)
	st.AddModelEvent(ctx, ModelEvent{At: 1000, Model: "qwen", From: "stopped", To: "starting"})
	st.AddModelEvent(ctx, ModelEvent{At: 13000, Model: "qwen", From: "starting", To: "ready", LoadMs: &load})
	es, err := st.ModelEvents(ctx, time.UnixMilli(0), time.UnixMilli(20000))
	if err != nil || len(es) != 2 || *es[1].LoadMs != 12000 {
		t.Errorf("events %+v err %v", es, err)
	}
	if v, _ := st.Setting(ctx, "x"); v != "" {
		t.Errorf("unset setting %q", v)
	}
	st.SetSetting(ctx, "x", "1")
	st.SetSetting(ctx, "x", "2")
	if v, _ := st.Setting(ctx, "x"); v != "2" {
		t.Errorf("setting %q", v)
	}
}
