package store

import (
	"context"
	"time"
)

// Work is when one request was in the server's hands.
type Work struct {
	ID      int64
	Arrived time.Time // when the request reached notus-swap
	// Began is when the server started on the request's prompt. It is
	// zero while a request in flight is still waiting.
	Began time.Time
	Ended time.Time // zero while in flight
}

// Working reports whether the server was working on the request at t.
func (w Work) Working(t time.Time) bool {
	return !w.Began.IsZero() && !t.Before(w.Began) && (w.Ended.IsZero() || t.Before(w.Ended))
}

// Present reports whether the request had arrived and not yet ended at t.
func (w Work) Present(t time.Time) bool {
	return !t.Before(w.Arrived) && (w.Ended.IsZero() || t.Before(w.Ended))
}

// BeganAt works out when the server started on a finished request: its
// prompt and generation times, counted back from when it ended. It returns
// the zero time when the request has no timings.
func BeganAt(arrived, ended time.Time, promptMs, predictedMs *float64) time.Time {
	if promptMs == nil || predictedMs == nil {
		return time.Time{}
	}
	began := ended.Add(-time.Duration((*promptMs + *predictedMs) * float64(time.Millisecond)))
	if began.Before(arrived) {
		return arrived
	}
	return began
}

// Power is the GPUs' average power over a stretch of time.
type Power struct {
	From, To time.Time
	Watts    float64
}

// WorkRow is a finished request's times and stored energy.
type WorkRow struct {
	Work
	EnergyJ *float64
}

// WorkRows lists every finished request, oldest first. Began is Arrived for
// requests without timings.
func (s *Store) WorkRows(ctx context.Context) ([]WorkRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, started_at, finished_at, prompt_ms, predicted_ms, energy_j
		 FROM requests WHERE finished_at IS NOT NULL ORDER BY started_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkRow
	for rows.Next() {
		var r WorkRow
		var started, finished int64
		var prompt, predicted *float64
		if err := rows.Scan(&r.ID, &started, &finished, &prompt, &predicted, &r.EnergyJ); err != nil {
			return nil, err
		}
		r.Arrived, r.Ended = time.UnixMilli(started), time.UnixMilli(finished)
		if r.Began = BeganAt(r.Arrived, r.Ended, prompt, predicted); r.Began.IsZero() {
			r.Began = r.Arrived
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GPUPower returns the dedicated GPUs' power between from and to, from the
// finest tier that still covers from.
func (s *Store) GPUPower(ctx context.Context, now, from, to time.Time) ([]Power, error) {
	tier := tierFor(now, from)
	rows, err := s.db.QueryContext(ctx,
		`SELECT ts, watts FROM samples WHERE tier = ? AND source = 'gpus' AND ts >= ? AND ts <= ? AND watts IS NOT NULL ORDER BY ts`,
		tier, from.Unix()-int64(tier), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Power
	for rows.Next() {
		var ts int64
		var w float64
		if err := rows.Scan(&ts, &w); err != nil {
			return nil, err
		}
		// A 1-second row is read at ts and covers the second before it. A
		// rolled-up row starts at ts.
		p := Power{From: time.Unix(ts, 0), To: time.Unix(ts+int64(tier), 0), Watts: w}
		if tier == TierSecond {
			p.From, p.To = time.Unix(ts-1, 0), time.Unix(ts, 0)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetShare stores a request's energy and time spent queued.
func (s *Store) SetShare(ctx context.Context, id int64, energyJ *float64, queuedMs int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE requests SET energy_j = ?, queued_ms = ? WHERE id = ?`, energyJ, queuedMs, id)
	return err
}

// SetQueued stores a request's time spent queued.
func (s *Store) SetQueued(ctx context.Context, id int64, queuedMs int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE requests SET queued_ms = ? WHERE id = ?`, queuedMs, id)
	return err
}
