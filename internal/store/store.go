// Package store keeps captured requests in SQLite.
//
// Metadata (timing, tokens, status) lives in the requests table and is kept
// for good. Request and response bodies live in a separate bodies table so
// retention can delete them without touching the metadata.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Request states.
const (
	StateInFlight    = "in_flight"
	StateDone        = "done"
	StateFailed      = "failed"      // upstream error or 5xx
	StateClientGone  = "client_gone" // the client disconnected before the end
	StateInterrupted = "interrupted" // notus-swap stopped while it was in flight
	StateCancelled   = "cancelled"   // cancelled from the web UI
)

type Store struct {
	db   *sql.DB
	path string
}

// migrations[i] moves the schema from version i to i+1.
// Append new entries; never edit old ones.
var migrations = []string{
	`CREATE TABLE requests (
		id                   INTEGER PRIMARY KEY,
		started_at           INTEGER NOT NULL, -- unix ms
		first_byte_at        INTEGER,
		first_token_at       INTEGER,
		finished_at          INTEGER,
		method               TEXT NOT NULL,
		path                 TEXT NOT NULL,
		model                TEXT NOT NULL DEFAULT '',
		streaming            INTEGER NOT NULL DEFAULT 0,
		state                TEXT NOT NULL,
		status_code          INTEGER,
		error                TEXT,
		prompt_tokens        INTEGER,
		completion_tokens    INTEGER,
		prompt_ms            REAL,
		predicted_ms         REAL,
		prompt_per_second    REAL,
		predicted_per_second REAL,
		request_bytes        INTEGER NOT NULL DEFAULT 0,
		response_bytes       INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX requests_started_at ON requests(started_at);
	CREATE TABLE bodies (
		request_id    INTEGER PRIMARY KEY REFERENCES requests(id) ON DELETE CASCADE,
		request_body  BLOB,
		response_body BLOB,
		truncated     INTEGER NOT NULL DEFAULT 0
	);`,
	`ALTER TABLE requests ADD COLUMN prompt_preview TEXT NOT NULL DEFAULT '';
	CREATE INDEX requests_model ON requests(model);`,
	`CREATE TABLE samples (
		tier       INTEGER NOT NULL, -- bucket size in seconds: 1, 60 or 3600
		ts         INTEGER NOT NULL, -- unix seconds, start of the bucket
		source     TEXT NOT NULL,
		kind       TEXT NOT NULL,
		watts      REAL,
		temp_c     REAL,
		vram_used  REAL,
		vram_total REAL,
		busy       REAL,
		PRIMARY KEY (tier, source, ts)
	) WITHOUT ROWID;
	CREATE INDEX samples_tier_ts ON samples(tier, ts);
	CREATE TABLE model_events (
		id         INTEGER PRIMARY KEY,
		at         INTEGER NOT NULL, -- unix ms
		model      TEXT NOT NULL,
		from_state TEXT NOT NULL,
		to_state   TEXT NOT NULL,
		load_ms    INTEGER
	);
	CREATE INDEX model_events_at ON model_events(at);
	CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	ALTER TABLE requests ADD COLUMN energy_j REAL;`,
	`ALTER TABLE requests ADD COLUMN cached_tokens INTEGER;`,
	`ALTER TABLE requests ADD COLUMN finish_reason TEXT;
	ALTER TABLE requests ADD COLUMN n_ctx INTEGER; -- the model's context size per slot, when known
	CREATE TABLE request_issues (
		request_id INTEGER NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
		kind       TEXT NOT NULL,
		level      TEXT NOT NULL, -- warning or info
		detail     TEXT NOT NULL DEFAULT '{}', -- JSON: the numbers behind the issue
		PRIMARY KEY (request_id, kind)
	) WITHOUT ROWID;
	CREATE INDEX request_issues_kind ON request_issues(kind);
	CREATE TABLE model_props (
		model      TEXT PRIMARY KEY, -- a model ID or alias
		n_ctx      INTEGER NOT NULL,
		n_predict  INTEGER,
		slots      INTEGER,
		updated_at INTEGER NOT NULL -- unix ms
	);`,
	// build is the server's build as it reports it in system_fingerprint,
	// such as llama.cpp's "b11146-7fe450e19".
	`ALTER TABLE requests ADD COLUMN build TEXT;`,
	// cmd_hash names the command llama-swap started the model with; the
	// command's arguments are kept once per hash in model_cmds.
	`ALTER TABLE requests ADD COLUMN cmd_hash TEXT;
	CREATE TABLE model_cmds (
		hash       TEXT PRIMARY KEY,
		args       TEXT NOT NULL, -- JSON list of strings
		first_seen INTEGER NOT NULL -- unix ms
	);`,
	// build is the server's build as llama-server reports it in /props, for
	// answers that don't name it, such as the Responses API's.
	`ALTER TABLE model_props ADD COLUMN build TEXT;`,
	// tags is a JSON list of capture.Tag for the Notable column; NULL means not yet tagged.
	`ALTER TABLE requests ADD COLUMN tags TEXT;`,
	// queued_ms is how long the request waited while the server worked on
	// other requests. NULL for requests stored before it was measured.
	`ALTER TABLE requests ADD COLUMN queued_ms INTEGER;`,
	// auto is 1 for the steps of a load notus-swap started itself, because
	// no model had been loaded for a while (the default model setting).
	`ALTER TABLE model_events ADD COLUMN auto INTEGER NOT NULL DEFAULT 0;`,
	// retry_of is the request this one was sent again from, with the web
	// UI's Retry button.
	`ALTER TABLE requests ADD COLUMN retry_of INTEGER;`,
	// client_ip and user_agent say where a request came from. client_ip is
	// the first address in X-Forwarded-For when a reverse proxy set one.
	`ALTER TABLE requests ADD COLUMN client_ip TEXT;
	ALTER TABLE requests ADD COLUMN user_agent TEXT;`,
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// One connection serialises writes, which avoids SQLITE_BUSY. Traffic is
	// a handful of requests a minute, so this is not a bottleneck.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for ; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Begin is what is known when a request arrives.
type Begin struct {
	StartedAt    time.Time
	Method       string
	Path         string
	Model        string
	Streaming    bool
	Preview      string // the start of the last user message, for lists
	RequestBody  []byte // may be cut short; see Truncated
	RequestBytes int64  // full size, even when RequestBody is cut short
	Truncated    bool
	Tags         string // JSON list of the tags known when the request arrives
	RetryOf      int64  // the request this one retries, or 0
	ClientIP     string // the client's address, see ClientIP in package capture
	UserAgent    string
}

// Finish is what is known when the response ends.
type Finish struct {
	FirstByteAt        time.Time // zero if no body was written
	FirstTokenAt       time.Time // zero if no token was seen
	FinishedAt         time.Time
	State              string
	StatusCode         int
	Error              string
	PromptTokens       *int64
	CompletionTokens   *int64
	CachedTokens       *int64
	PromptMs           *float64
	PredictedMs        *float64
	PromptPerSecond    *float64
	PredictedPerSecond *float64
	EnergyJ            *float64 // GPU energy used, in joules
	QueuedMs           *int64   // time spent waiting behind other requests
	ResponseBody       []byte   // may be cut short; see Truncated
	ResponseBytes      int64
	Truncated          bool
	FinishReason       string
	NCtx               *int64 // the model's context size, when known
	Build              string // the server's build, from system_fingerprint
	CmdHash            string // the model's command, see model_cmds
	Tags               string // JSON list of every tag, see Begin
	Issues             []Issue
}

func (s *Store) Begin(ctx context.Context, b Begin) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO requests (started_at, method, path, model, streaming, state, request_bytes, prompt_preview, tags, retry_of, client_ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.StartedAt.UnixMilli(), b.Method, b.Path, b.Model, b.Streaming, StateInFlight, b.RequestBytes, b.Preview, nilIfEmpty(b.Tags), nilIfZero(b.RetryOf),
		nilIfEmpty(b.ClientIP), nilIfEmpty(b.UserAgent))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO bodies (request_id, request_body, truncated) VALUES (?, ?, ?)`,
		id, b.RequestBody, b.Truncated); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) Finish(ctx context.Context, id int64, f Finish) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE requests SET first_byte_at = ?, first_token_at = ?, finished_at = ?, state = ?,
		   status_code = ?, error = ?, prompt_tokens = ?, completion_tokens = ?, prompt_ms = ?,
		   predicted_ms = ?, prompt_per_second = ?, predicted_per_second = ?, response_bytes = ?,
		   energy_j = ?, queued_ms = ?, cached_tokens = ?, finish_reason = ?, n_ctx = ?, build = ?, cmd_hash = ?,
		   tags = COALESCE(?, tags)
		 WHERE id = ?`,
		msOrNil(f.FirstByteAt), msOrNil(f.FirstTokenAt), f.FinishedAt.UnixMilli(), f.State,
		f.StatusCode, nilIfEmpty(f.Error), f.PromptTokens, f.CompletionTokens, f.PromptMs,
		f.PredictedMs, f.PromptPerSecond, f.PredictedPerSecond, f.ResponseBytes, f.EnergyJ, f.QueuedMs, f.CachedTokens,
		nilIfEmpty(f.FinishReason), f.NCtx, nilIfEmpty(f.Build), nilIfEmpty(f.CmdHash), nilIfEmpty(f.Tags), id); err != nil {
		return err
	}
	if err := writeIssues(ctx, tx, id, f.Issues); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE bodies SET response_body = ?, truncated = truncated OR ? WHERE request_id = ?`,
		f.ResponseBody, f.Truncated, id); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkInterrupted closes out requests left in flight by a previous run.
//
// The exact time the previous run stopped isn't known. The end time is set to
// the last 1-second sample before the first gap of over 5 s after the request
// started, since sampling stops when notus-swap does. Without samples, the end
// is the start. This also fills rows marked interrupted before end times were
// set, as long as their samples are still kept.
func (s *Store) MarkInterrupted(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE requests SET state = ? WHERE state = ?`, StateInterrupted, StateInFlight)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE requests SET finished_at = MAX(started_at, COALESCE((
			SELECT MIN(a.ts) * 1000 FROM samples a
			WHERE a.tier = 1 AND a.ts >= requests.started_at / 1000
			  AND NOT EXISTS (SELECT 1 FROM samples b WHERE b.tier = 1 AND b.ts > a.ts AND b.ts <= a.ts + 5)
		), started_at))
		WHERE state = ? AND finished_at IS NULL`, StateInterrupted); err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneBodies deletes the bodies of requests that started before cutoff, then
// deletes the oldest remaining bodies until they total at most maxBytes.
// Metadata rows are never deleted. It returns how many bodies were deleted.
func (s *Store) PruneBodies(ctx context.Context, cutoff time.Time, maxBytes int64) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM bodies WHERE request_id IN (SELECT id FROM requests WHERE started_at < ?)`,
		cutoff.UnixMilli())
	if err != nil {
		return 0, err
	}
	deleted, _ := res.RowsAffected()

	var total int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(COALESCE(LENGTH(request_body),0) + COALESCE(LENGTH(response_body),0)), 0) FROM bodies`,
	).Scan(&total); err != nil {
		return deleted, err
	}
	if total <= maxBytes {
		return deleted, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT request_id, COALESCE(LENGTH(request_body),0) + COALESCE(LENGTH(response_body),0)
		 FROM bodies ORDER BY request_id`)
	if err != nil {
		return deleted, err
	}
	var lastID int64
	for total > maxBytes && rows.Next() {
		var size int64
		if err := rows.Scan(&lastID, &size); err != nil {
			rows.Close()
			return deleted, err
		}
		total -= size
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return deleted, err
	}
	res, err = s.db.ExecContext(ctx, `DELETE FROM bodies WHERE request_id <= ?`, lastID)
	if err != nil {
		return deleted, err
	}
	n, _ := res.RowsAffected()
	return deleted + n, nil
}

// Record is one captured request. Get fills in the bodies, if they are
// still kept; List leaves them empty.
type Record struct {
	ID                 int64
	StartedAt          time.Time
	FirstByteAt        *time.Time
	FirstTokenAt       *time.Time
	FinishedAt         *time.Time
	Method, Path       string
	Model              string
	Streaming          bool
	State              string
	StatusCode         *int
	Error              *string
	PromptTokens       *int64
	CompletionTokens   *int64
	CachedTokens       *int64
	PromptMs           *float64
	PredictedMs        *float64
	PromptPerSecond    *float64
	PredictedPerSecond *float64
	EnergyJ            *float64
	QueuedMs           *int64
	RequestBytes       int64
	ResponseBytes      int64
	Preview            string
	HasBodies          bool
	RequestBody        []byte
	ResponseBody       []byte
	Truncated          bool
	FinishReason       *string
	NCtx               *int64
	Build              *string
	CmdHash            *string
	// Tags is a JSON list of capture.Tag, or nil if the request has not been tagged.
	Tags *string
	// RetryOf is the request this one retries, or nil.
	RetryOf *int64
	// ClientIP and UserAgent say where the request came from; nil for
	// requests stored before they were kept.
	ClientIP  *string
	UserAgent *string
	// Issues lists the kinds of issue found, leaving out muted ones. Only
	// List fills it in; Store.Issues has the details.
	Issues []string
}

var ErrNotFound = errors.New("request not found")

const metaCols = `r.id, r.started_at, r.first_byte_at, r.first_token_at, r.finished_at, r.method, r.path,
	r.model, r.streaming, r.state, r.status_code, r.error, r.prompt_tokens, r.completion_tokens,
	r.prompt_ms, r.predicted_ms, r.prompt_per_second, r.predicted_per_second, r.request_bytes,
	r.response_bytes, r.prompt_preview, r.energy_j, r.cached_tokens, r.finish_reason, r.n_ctx, r.build, r.cmd_hash, r.tags, r.queued_ms, r.retry_of,
	r.client_ip, r.user_agent`

type scanner interface{ Scan(dest ...any) error }

// scanMeta reads metaCols, then any extra columns into extra.
func scanMeta(row scanner, r *Record, extra ...any) error {
	var started int64
	var firstByte, firstToken, finished sql.NullInt64
	dest := append([]any{&r.ID, &started, &firstByte, &firstToken, &finished, &r.Method, &r.Path,
		&r.Model, &r.Streaming, &r.State, &r.StatusCode, &r.Error, &r.PromptTokens, &r.CompletionTokens,
		&r.PromptMs, &r.PredictedMs, &r.PromptPerSecond, &r.PredictedPerSecond, &r.RequestBytes,
		&r.ResponseBytes, &r.Preview, &r.EnergyJ, &r.CachedTokens, &r.FinishReason, &r.NCtx, &r.Build, &r.CmdHash, &r.Tags, &r.QueuedMs, &r.RetryOf,
		&r.ClientIP, &r.UserAgent}, extra...)
	if err := row.Scan(dest...); err != nil {
		return err
	}
	r.StartedAt = time.UnixMilli(started)
	r.FirstByteAt = timeOrNil(firstByte)
	r.FirstTokenAt = timeOrNil(firstToken)
	r.FinishedAt = timeOrNil(finished)
	return nil
}

func (s *Store) Get(ctx context.Context, id int64) (*Record, error) {
	var r Record
	var bodyID, truncated sql.NullInt64
	err := scanMeta(s.db.QueryRowContext(ctx,
		`SELECT `+metaCols+`, b.request_id, b.request_body, b.response_body, b.truncated
		 FROM requests r LEFT JOIN bodies b ON b.request_id = r.id WHERE r.id = ?`, id),
		&r, &bodyID, &r.RequestBody, &r.ResponseBody, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.HasBodies = bodyID.Valid
	r.Truncated = truncated.Int64 != 0
	return &r, nil
}

// ListOptions filters List. Zero values mean "no filter".
type ListOptions struct {
	BeforeID int64 // only requests older than this one, for paging
	Limit    int   // default 100, at most 500
	Model    string
	State    string
	Search   string // matched against the prompt preview and the model
	// Since and Until limit the start time; zero means no limit.
	Since, Until time.Time
	// Issue keeps only requests with an issue of this kind, or with any
	// issue if it is "any". Muted issues don't count.
	Issue string
	Mutes []IssueMute
}

// List returns requests newest first, without bodies.
func (s *Store) List(ctx context.Context, o ListOptions) ([]Record, error) {
	mutes, err := json.Marshal(o.Mutes)
	if err != nil {
		return nil, err
	}
	if o.Mutes == nil {
		mutes = []byte("[]")
	}
	q := `SELECT ` + metaCols + `, b.request_id IS NOT NULL,
		(SELECT group_concat(i.kind) FROM request_issues i WHERE i.request_id = r.id AND ` + unmuted + `)
		FROM requests r LEFT JOIN bodies b ON b.request_id = r.id WHERE 1=1`
	args := []any{string(mutes)}
	if o.Issue == "any" {
		q += ` AND EXISTS (SELECT 1 FROM request_issues i WHERE i.request_id = r.id AND ` + unmuted + `)`
		args = append(args, string(mutes))
	} else if o.Issue != "" {
		q += ` AND EXISTS (SELECT 1 FROM request_issues i WHERE i.request_id = r.id AND i.kind = ? AND ` + unmuted + `)`
		args = append(args, o.Issue, string(mutes))
	}
	if o.BeforeID > 0 {
		q += ` AND r.id < ?`
		args = append(args, o.BeforeID)
	}
	if o.Model != "" {
		q += ` AND r.model = ?`
		args = append(args, o.Model)
	}
	if o.State != "" {
		q += ` AND r.state = ?`
		args = append(args, o.State)
	}
	if !o.Since.IsZero() {
		q += ` AND r.started_at >= ?`
		args = append(args, o.Since.UnixMilli())
	}
	if !o.Until.IsZero() {
		q += ` AND r.started_at <= ?`
		args = append(args, o.Until.UnixMilli())
	}
	if o.Search != "" {
		q += ` AND (r.prompt_preview LIKE ? ESCAPE '\' OR r.model LIKE ? ESCAPE '\')`
		like := "%" + likeEscaper.Replace(o.Search) + "%"
		args = append(args, like, like)
	}
	if o.Limit <= 0 {
		o.Limit = 100
	}
	if o.Limit > 20000 {
		o.Limit = 20000
	}
	q += ` ORDER BY r.id DESC LIMIT ?`
	args = append(args, o.Limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		var kinds sql.NullString
		if err := scanMeta(rows, &r, &r.HasBodies, &kinds); err != nil {
			return nil, err
		}
		r.Issues = []string{}
		if kinds.String != "" {
			r.Issues = strings.Split(kinds.String, ",")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Timing is the part of a request the Dashboard draws. Times are unix ms.
// The JSON names match the Requests list's, so the UI reads both alike.
type Timing struct {
	ID                 int64    `json:"id"`
	StartedAt          int64    `json:"started_at"`
	FirstTokenAt       *int64   `json:"first_token_at"`
	FinishedAt         *int64   `json:"finished_at"`
	Model              string   `json:"model"`
	State              string   `json:"state"`
	PromptTokens       *int64   `json:"prompt_tokens"`
	CompletionTokens   *int64   `json:"completion_tokens"`
	CachedTokens       *int64   `json:"cached_tokens"`
	PromptMs           *float64 `json:"prompt_ms"`
	PromptPerSecond    *float64 `json:"prompt_per_second"`
	PredictedPerSecond *float64 `json:"predicted_per_second"`
	EnergyJ            *float64 `json:"energy_j"`
	QueuedMs           *int64   `json:"queued_ms"`
}

// Timings returns the requests started between from and to, newest first,
// at most limit. It reads only the columns in Timing, so it is much cheaper
// than List over a long range.
func (s *Store) Timings(ctx context.Context, from, to time.Time, limit int) ([]Timing, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, started_at, first_token_at, finished_at, model, state, prompt_tokens, completion_tokens,
			cached_tokens, prompt_ms, prompt_per_second, predicted_per_second, energy_j, queued_ms
		 FROM requests WHERE started_at >= ? AND started_at <= ? ORDER BY id DESC LIMIT ?`,
		from.UnixMilli(), to.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Timing{}
	for rows.Next() {
		var t Timing
		if err := rows.Scan(&t.ID, &t.StartedAt, &t.FirstTokenAt, &t.FinishedAt, &t.Model, &t.State, &t.PromptTokens,
			&t.CompletionTokens, &t.CachedTokens, &t.PromptMs, &t.PromptPerSecond, &t.PredictedPerSecond, &t.EnergyJ, &t.QueuedMs); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Models lists every model name seen, for filters.
func (s *Store) Models(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT model FROM requests WHERE model != '' ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func msOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func timeOrNil(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64)
	return &t
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nilIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// FillCachedTokens sets cached_tokens on requests stored before that column
// existed, by reading each response body with cached. It runs once: a
// setting records that it finished. It returns how many rows it filled.
func (s *Store) FillCachedTokens(ctx context.Context, cached func(body []byte) *int64) (int, error) {
	return s.fillFromBodies(ctx, "filled_cached_tokens", "cached_tokens", func(b []byte) any {
		if n := cached(b); n != nil {
			return *n
		}
		return nil
	})
}

// FillBuilds sets build on requests stored before that column existed, by
// reading each response body with build. It runs once, like FillCachedTokens.
func (s *Store) FillBuilds(ctx context.Context, build func(body []byte) string) (int, error) {
	return s.fillFromBodies(ctx, "filled_builds", "build", func(b []byte) any {
		if v := build(b); v != "" {
			return v
		}
		return nil
	})
}

// fillFromBodies sets column on each finished request where it is NULL and
// the body is still kept, to value(response body). A nil value leaves the row
// as it is. The setting done records that it has run.
func (s *Store) fillFromBodies(ctx context.Context, done, column string, value func(body []byte) any) (int, error) {
	if v, err := s.Setting(ctx, done); err != nil || v != "" {
		return 0, err
	}
	type row struct {
		id   int64
		body []byte
	}
	filled := 0
	var after int64
	for {
		// Read a small batch and close it before writing, because the store
		// has a single connection.
		rows, err := s.db.QueryContext(ctx,
			`SELECT r.id, b.response_body FROM requests r JOIN bodies b ON b.request_id = r.id
			 WHERE r.id > ? AND r.`+column+` IS NULL AND r.state != ? AND b.response_body IS NOT NULL
			 ORDER BY r.id LIMIT 20`, after, StateInFlight)
		if err != nil {
			return filled, err
		}
		var batch []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.body); err != nil {
				rows.Close()
				return filled, err
			}
			batch = append(batch, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return filled, err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			after = r.id
			v := value(r.body)
			if v == nil {
				continue
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE requests SET `+column+` = ? WHERE id = ?`, v, r.id); err != nil {
				return filled, err
			}
			filled++
		}
	}
	return filled, s.SetSetting(ctx, done, "true")
}
