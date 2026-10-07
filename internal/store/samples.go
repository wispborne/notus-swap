package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// Sample tiers. A tier is also its bucket size in seconds: tier 1 holds one
// row per source per second, tier 60 per minute, tier 3600 per hour. Rollup
// averages each tier into the next, and old rows of the finer tiers are
// deleted: 1-second rows after 24 hours, 1-minute rows after 30 days.
// Hourly rows are kept for good.
const (
	TierSecond = 1
	TierMinute = 60
	TierHour   = 3600
)

var tierKeep = map[int]time.Duration{TierSecond: 24 * time.Hour, TierMinute: 30 * 24 * time.Hour}

// Sample is one source's readings for one bucket. Nil means not reported.
//
// Sources are "gpu:<pci address>" (kind "gpu"), "igpu:<pci>" (built-in
// graphics, kind "igpu"), "gpus" (all dedicated cards added up), "cpu", and
// "system" (the at-the-wall estimate).
type Sample struct {
	Source    string   `json:"source"`
	Kind      string   `json:"kind"`
	Watts     *float64 `json:"watts"`
	TempC     *float64 `json:"temp_c"`
	VRAMUsed  *float64 `json:"vram_used"`
	VRAMTotal *float64 `json:"vram_total"`
	Busy      *float64 `json:"busy"`
}

// InsertSamples stores one second's readings.
func (s *Store) InsertSamples(ctx context.Context, at time.Time, rows []Sample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ts := at.Unix()
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO samples (tier, ts, source, kind, watts, temp_c, vram_used, vram_total, busy)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			TierSecond, ts, r.Source, r.Kind, r.Watts, r.TempC, r.VRAMUsed, r.VRAMTotal, r.Busy); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Rollup averages finished minutes of 1-second rows into minute rows, and
// finished hours of minute rows into hour rows, for buckets that start after
// since. Rerunning it over the same window gives the same result. It then
// deletes rows past their tier's keep time.
func (s *Store) Rollup(ctx context.Context, now time.Time, since time.Duration) error {
	for _, step := range []struct{ from, to int }{{TierSecond, TierMinute}, {TierMinute, TierHour}} {
		end := now.Unix() / int64(step.to) * int64(step.to) // start of the unfinished bucket
		start := now.Add(-since).Unix() / int64(step.to) * int64(step.to)
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR REPLACE INTO samples (tier, ts, source, kind, watts, temp_c, vram_used, vram_total, busy)
			 SELECT ?, (ts / ?) * ?, source, MAX(kind), AVG(watts), AVG(temp_c), AVG(vram_used), MAX(vram_total), AVG(busy)
			 FROM samples WHERE tier = ? AND ts >= ? AND ts < ?
			 GROUP BY source, ts / ?`,
			step.to, step.to, step.to, step.from, start, end, step.to); err != nil {
			return err
		}
	}
	for tier, keep := range tierKeep {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM samples WHERE tier = ? AND ts < ?`, tier, now.Add(-keep).Unix()); err != nil {
			return err
		}
	}
	return nil
}

// Point is one bucket of a series. T is unix seconds.
type Point struct {
	T int64 `json:"t"`
	Sample
}

// tierFor picks the finest tier that still holds data back to from.
func tierFor(now, from time.Time) int {
	switch {
	case now.Sub(from) <= tierKeep[TierSecond]:
		return TierSecond
	case now.Sub(from) <= tierKeep[TierMinute]:
		return TierMinute
	}
	return TierHour
}

// Series returns every source's samples between from and to, averaged into
// buckets of at least bucket seconds (rounded up to the tier size). It also
// returns the bucket size used.
func (s *Store) Series(ctx context.Context, now, from, to time.Time, bucket int64) ([]Point, int64, error) {
	tier := int64(tierFor(now, from))
	if bucket < tier {
		bucket = tier
	}
	bucket = (bucket + tier - 1) / tier * tier
	sources, err := s.sampleSources(ctx, tier)
	if err != nil {
		return nil, 0, err
	}
	// One query per source reads the primary key (tier, source, ts) in
	// order. One query for all sources used the (tier, ts) index and looked
	// up each row, which took about 1 s for 24 hours of 1-second rows.
	out := []Point{}
	for _, src := range sources {
		if err := s.seriesOf(ctx, &out, src, tier, from, to, bucket); err != nil {
			return nil, 0, err
		}
	}
	slices.SortStableFunc(out, func(a, b Point) int { return cmp.Compare(a.T, b.T) })
	return out, bucket, nil
}

func (s *Store) seriesOf(ctx context.Context, out *[]Point, source string, tier int64, from, to time.Time, bucket int64) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT (ts / ?) * ? AS b, MAX(kind), AVG(watts), AVG(temp_c), AVG(vram_used), MAX(vram_total), AVG(busy)
		 FROM samples WHERE tier = ? AND source = ? AND ts >= ? AND ts <= ?
		 GROUP BY b`,
		bucket, bucket, tier, source, from.Unix(), to.Unix())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		p := Point{Sample: Sample{Source: source}}
		if err := rows.Scan(&p.T, &p.Kind, &p.Watts, &p.TempC, &p.VRAMUsed, &p.VRAMTotal, &p.Busy); err != nil {
			return err
		}
		*out = append(*out, p)
	}
	return rows.Err()
}

// sampleSources lists the sources stored in a tier, in name order. Each
// step finds the next name from the primary key, so it reads one row per
// source instead of every row.
func (s *Store) sampleSources(ctx context.Context, tier int64) ([]string, error) {
	var out []string
	last := ""
	for {
		var name string
		err := s.db.QueryRowContext(ctx,
			`SELECT source FROM samples WHERE tier = ? AND source > ? ORDER BY source LIMIT 1`, tier, last).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, name)
		last = name
	}
}

// EnergyWh adds up a source's energy between from and to, in watt-hours,
// using the finest tier that covers the whole span.
func (s *Store) EnergyWh(ctx context.Context, now, from, to time.Time, source string) (*float64, error) {
	tier := tierFor(now, from)
	var wh sql.NullFloat64
	err := s.db.QueryRowContext(ctx,
		`SELECT SUM(watts) * ? / 3600.0 FROM samples WHERE tier = ? AND source = ? AND ts >= ? AND ts < ?`,
		tier, tier, source, from.Unix(), to.Unix()).Scan(&wh)
	if err != nil || !wh.Valid {
		return nil, err
	}
	return &wh.Float64, nil
}

// ModelEvent is one model changing state in llama-swap. LoadMs is set on the
// change to "ready" that ends a load.
type ModelEvent struct {
	At     int64  `json:"at"` // unix ms
	Model  string `json:"model"`
	From   string `json:"from"`
	To     string `json:"to"`
	LoadMs *int64 `json:"load_ms,omitempty"`
	// Auto is set on the steps of a load notus-swap started itself, to
	// bring back the default model.
	Auto bool `json:"auto,omitempty"`
}

func (s *Store) AddModelEvent(ctx context.Context, e ModelEvent) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO model_events (at, model, from_state, to_state, load_ms, auto) VALUES (?, ?, ?, ?, ?, ?)`,
		e.At, e.Model, e.From, e.To, e.LoadMs, e.Auto)
	return err
}

// LoadedSince reports whether the model finished a load at or after the given
// time. A request that arrived before then waited for the load.
func (s *Store) LoadedSince(ctx context.Context, model string, since time.Time) bool {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM model_events WHERE model = ? AND to_state = 'ready' AND load_ms IS NOT NULL AND at >= ?`,
		model, since.UnixMilli()).Scan(&n)
	return err == nil && n > 0
}

// ModelEvents returns events between from and to, oldest first.
func (s *Store) ModelEvents(ctx context.Context, from, to time.Time) ([]ModelEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT at, model, from_state, to_state, load_ms, auto FROM model_events WHERE at >= ? AND at <= ? ORDER BY at`,
		from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelEvent{}
	for rows.Next() {
		var e ModelEvent
		if err := rows.Scan(&e.At, &e.Model, &e.From, &e.To, &e.LoadMs, &e.Auto); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Setting returns a stored setting, or "" if it was never set.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, key, value)
	return err
}

// ModelEventsFor returns one model's newest events first, at most limit
// (default 20).
func (s *Store) ModelEventsFor(ctx context.Context, model string, limit int) ([]ModelEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT at, model, from_state, to_state, load_ms, auto FROM model_events WHERE model = ? ORDER BY at DESC LIMIT ?`,
		model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelEvent{}
	for rows.Next() {
		var e ModelEvent
		if err := rows.Scan(&e.At, &e.Model, &e.From, &e.To, &e.LoadMs, &e.Auto); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ModelStat is one model's totals over every request notus-swap recorded,
// with speeds from its most recent requests.
type ModelStat struct {
	Model            string   `json:"model"`
	Requests         int64    `json:"requests"`
	Failed           int64    `json:"failed"`
	LastUsed         *int64   `json:"last_used"` // unix ms
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	CachedTokens     int64    `json:"cached_tokens"`
	// CacheRate is cached tokens over prompt tokens, counting only requests
	// that report a cache count. Nil when none do.
	CacheRate *float64 `json:"cache_rate"`
	EnergyJ   *float64 `json:"energy_j"`
	// Averages over the last RecentSpeeds requests that report speeds.
	GenPerSecond    *float64 `json:"gen_per_second"`
	PromptPerSecond *float64 `json:"prompt_per_second"`
	// Loads counts changes to "ready" that were timed from "starting".
	Loads      int64    `json:"loads"`
	LoadMs     *float64 `json:"load_ms"` // average
	LastLoaded *int64   `json:"last_loaded"`
}

// RecentSpeeds is how many recent requests ModelStat averages speeds over.
const RecentSpeeds = 50

// mostlyFresh is a SQL condition for requests that were at most half cached.
// Requests with more cached are left out of speeds: their prompt speed is
// worked out from only a few tokens, and their time to first token is short.
// Requests that don't report a cache count are kept.
const mostlyFresh = `(cached_tokens IS NULL OR prompt_tokens IS NULL OR cached_tokens * 2 <= prompt_tokens)`

func (s *Store) ModelStats(ctx context.Context) ([]ModelStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH totals AS (
			SELECT model, COUNT(*) AS n, SUM(state = 'failed') AS failed, MAX(started_at) AS last_used,
				COALESCE(SUM(prompt_tokens), 0) AS pt, COALESCE(SUM(completion_tokens), 0) AS ct, SUM(energy_j) AS e,
				COALESCE(SUM(cached_tokens), 0) AS cached,
				SUM(cached_tokens) * 1.0 / NULLIF(SUM(CASE WHEN cached_tokens IS NOT NULL THEN prompt_tokens END), 0) AS rate
			FROM requests WHERE model != '' GROUP BY model
		), recent AS (
			SELECT model, predicted_per_second, prompt_per_second,
				ROW_NUMBER() OVER (PARTITION BY model ORDER BY id DESC) AS i
			FROM requests WHERE model != '' AND predicted_per_second IS NOT NULL AND `+mostlyFresh+`
		), speeds AS (
			SELECT model, AVG(predicted_per_second) AS gen, AVG(prompt_per_second) AS prompt
			FROM recent WHERE i <= ? GROUP BY model
		), loads AS (
			SELECT model, COUNT(load_ms) AS n, AVG(load_ms) AS ms, MAX(at) AS last
			FROM model_events WHERE to_state = 'ready' AND load_ms IS NOT NULL GROUP BY model
		)
		SELECT t.model, t.n, t.failed, t.last_used, t.pt, t.ct, t.cached, t.rate, t.e, s.gen, s.prompt,
			COALESCE(l.n, 0), l.ms, l.last
		FROM totals t LEFT JOIN speeds s USING (model) LEFT JOIN loads l USING (model)
		UNION ALL
		SELECT l.model, 0, 0, NULL, 0, 0, 0, NULL, NULL, NULL, NULL, l.n, l.ms, l.last
		FROM loads l WHERE l.model NOT IN (SELECT model FROM totals)
		ORDER BY 1`, RecentSpeeds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelStat{}
	for rows.Next() {
		var m ModelStat
		if err := rows.Scan(&m.Model, &m.Requests, &m.Failed, &m.LastUsed, &m.PromptTokens, &m.CompletionTokens,
			&m.CachedTokens, &m.CacheRate, &m.EnergyJ, &m.GenPerSecond, &m.PromptPerSecond, &m.Loads, &m.LoadMs, &m.LastLoaded); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Earliest is the time of the oldest thing stored: a request, a sample of
// any tier, or a model event. It is the zero time when nothing is stored.
func (s *Store) Earliest(ctx context.Context) (time.Time, error) {
	var ms sql.NullInt64
	// Each MIN is answered from an index.
	err := s.db.QueryRowContext(ctx, `SELECT MIN(t) FROM (
		SELECT MIN(started_at) AS t FROM requests
		UNION ALL SELECT MIN(at) FROM model_events
		UNION ALL SELECT MIN(ts) * 1000 FROM samples WHERE tier = 1
		UNION ALL SELECT MIN(ts) * 1000 FROM samples WHERE tier = 60
		UNION ALL SELECT MIN(ts) * 1000 FROM samples WHERE tier = 3600)`).Scan(&ms)
	if err != nil || !ms.Valid {
		return time.Time{}, err
	}
	return time.UnixMilli(ms.Int64), nil
}

// BuildStat is one model's speed on one server build and command. Speeds
// are weighted by tokens: total tokens over total time, so long requests
// count for more.
type BuildStat struct {
	Build     string `json:"build"`    // empty for requests with no known build
	CmdHash   string `json:"cmd_hash"` // empty for requests with no known command
	Requests  int64  `json:"requests"`
	FirstUsed int64  `json:"first_used"` // unix ms
	LastUsed  int64  `json:"last_used"`
	// GenPerSecond, PromptPerSecond and TTFTMs count only requests that
	// report timings and were at most half cached (see mostlyFresh). Prompt
	// speed leaves out cached tokens, which aren't processed.
	GenPerSecond    *float64 `json:"gen_per_second"`
	PromptPerSecond *float64 `json:"prompt_per_second"`
	TTFTMs          *float64 `json:"ttft_ms"` // average time to first token
}

// ModelBuilds lists a model's finished requests grouped by server build and
// command, the most recently started group first.
func (s *Store) ModelBuilds(ctx context.Context, model string) ([]BuildStat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(build, ''), COALESCE(cmd_hash, ''), COUNT(*), MIN(started_at), MAX(started_at),
			SUM(CASE WHEN fresh AND predicted_ms > 0 THEN completion_tokens END) * 1000.0 /
				NULLIF(SUM(CASE WHEN fresh AND predicted_ms > 0 AND completion_tokens IS NOT NULL THEN predicted_ms END), 0),
			SUM(CASE WHEN fresh AND prompt_ms > 0 THEN prompt_tokens - COALESCE(cached_tokens, 0) END) * 1000.0 /
				NULLIF(SUM(CASE WHEN fresh AND prompt_ms > 0 AND prompt_tokens IS NOT NULL THEN prompt_ms END), 0),
			AVG(CASE WHEN fresh THEN first_token_at - started_at - COALESCE(queued_ms, 0) END)
		FROM (SELECT *, `+mostlyFresh+` AS fresh FROM requests WHERE model = ? AND state = ?)
		GROUP BY 1, 2 ORDER BY 4 DESC`, model, StateDone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BuildStat{}
	for rows.Next() {
		var b BuildStat
		if err := rows.Scan(&b.Build, &b.CmdHash, &b.Requests, &b.FirstUsed, &b.LastUsed, &b.GenPerSecond, &b.PromptPerSecond, &b.TTFTMs); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SaveModelCmd keeps a command's arguments under its hash, the first time
// the hash is seen.
func (s *Store) SaveModelCmd(ctx context.Context, hash string, args []string, at time.Time) error {
	b, err := json.Marshal(args)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO model_cmds (hash, args, first_seen) VALUES (?, ?, ?)`,
		hash, string(b), at.UnixMilli())
	return err
}

// ModelCmds looks up the arguments of each command hash. Unknown hashes are
// left out.
func (s *Store) ModelCmds(ctx context.Context, hashes []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, h := range hashes {
		var b string
		err := s.db.QueryRowContext(ctx, `SELECT args FROM model_cmds WHERE hash = ?`, h).Scan(&b)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var args []string
		if err := json.Unmarshal([]byte(b), &args); err != nil {
			return nil, err
		}
		out[h] = args
	}
	return out, nil
}
