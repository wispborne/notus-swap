package metrics

import (
	"context"
	"slices"
	"time"

	"github.com/wispborne/notus-swap/internal/store"
)

// Share works out one request's part of the GPU energy in power, and how
// long it waited while the server worked on other requests.
//
// At each moment, the power is split evenly between the requests the server
// is working on. A request waiting its turn gets none of it. When the server
// is working on nothing, such as while a model loads, the power is split
// between the requests waiting for it.
//
// r.Began must be set. Requests in others that have not begun are waiting.
func Share(power []store.Power, r store.Work, others []store.Work) (joules float64, queued time.Duration) {
	// Who is working or waiting only changes at these times.
	cuts := []time.Time{r.Arrived, r.Began, r.Ended}
	for _, o := range others {
		for _, t := range []time.Time{o.Arrived, o.Began, o.Ended} {
			if !t.IsZero() && t.After(r.Arrived) && t.Before(r.Ended) {
				cuts = append(cuts, t)
			}
		}
	}
	slices.SortFunc(cuts, func(a, b time.Time) int { return a.Compare(b) })
	cuts = slices.CompactFunc(cuts, time.Time.Equal)

	for i := 0; i+1 < len(cuts); i++ {
		a, b := cuts[i], cuts[i+1]
		mid := a.Add(b.Sub(a) / 2)
		working, present := 0, 0
		for _, o := range others {
			if o.Working(mid) {
				working++
			}
			if o.Present(mid) {
				present++
			}
		}
		var part float64
		switch {
		case r.Working(mid):
			part = 1 / float64(working+1)
		case working > 0:
			queued += b.Sub(a)
			continue
		default:
			part = 1 / float64(present+1)
		}
		joules += part * energyBetween(power, a, b)
	}
	return joules, queued
}

// energyBetween adds up the energy in power between a and b, in joules.
func energyBetween(power []store.Power, a, b time.Time) float64 {
	var j float64
	for _, p := range power {
		lo, hi := later(p.From, a), earlier(p.To, b)
		if hi.After(lo) {
			j += p.Watts * hi.Sub(lo).Seconds()
		}
	}
	return j
}

// Recount works out the energy and time queued again for requests stored
// before Share, when every request that overlapped another took an equal
// share of the power, even while it only waited. Requests that overlapped
// none keep the energy they have. It runs once: a setting records that it
// finished. It returns how many requests it changed.
func Recount(ctx context.Context, st *store.Store, now time.Time) (int, error) {
	const done = "recounted_energy"
	if v, err := st.Setting(ctx, done); err != nil || v != "" {
		return 0, err
	}
	rows, err := st.WorkRows(ctx)
	if err != nil {
		return 0, err
	}
	changed := 0
	// Rows are sorted by arrival. Each group holds requests that overlap,
	// directly or through each other.
	for i := 0; i < len(rows); {
		end := rows[i].Ended
		j := i + 1
		for ; j < len(rows) && rows[j].Arrived.Before(end); j++ {
			if rows[j].Ended.After(end) {
				end = rows[j].Ended
			}
		}
		group := rows[i:j]
		i = j
		if len(group) == 1 {
			if err := st.SetQueued(ctx, group[0].ID, 0); err != nil {
				return changed, err
			}
			continue
		}
		power, err := st.GPUPower(ctx, now, group[0].Arrived, end)
		if err != nil {
			return changed, err
		}
		works := make([]store.Work, len(group))
		for k, g := range group {
			works[k] = g.Work
		}
		for k, g := range group {
			others := append(slices.Clone(works[:k]), works[k+1:]...)
			j, queued := Share(power, g.Work, others)
			energy := g.EnergyJ
			// Without power readings (such as before notus-swap sampled
			// them), keep what was stored.
			if energy != nil && len(power) > 0 {
				energy = &j
			}
			if err := st.SetShare(ctx, g.ID, energy, queued.Milliseconds()); err != nil {
				return changed, err
			}
			changed++
		}
	}
	return changed, st.SetSetting(ctx, done, now.UTC().Format(time.RFC3339))
}
