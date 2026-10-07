package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Issue is a problem found in a request's output, such as an answer cut off
// by the token limit. capture.Check finds them.
type Issue struct {
	Kind  string `json:"kind"`
	Level string `json:"level"` // "warning" or "info"
	// Detail holds the numbers behind the issue, as a JSON object.
	Detail json.RawMessage `json:"detail"`
}

// IssueMute stops one kind of issue being flagged for one model. The
// issues are still stored.
type IssueMute struct {
	Kind  string `json:"kind"`
	Model string `json:"model"`
}

// unmuted is a condition on request_issues i, joined to requests r, that
// leaves out muted issues. It takes one argument: the mutes as a JSON list.
const unmuted = `NOT EXISTS (SELECT 1 FROM json_each(?) m
	WHERE json_extract(m.value, '$.kind') = i.kind AND json_extract(m.value, '$.model') = r.model)`

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// writeIssues replaces a request's issues.
func writeIssues(ctx context.Context, db execer, id int64, issues []Issue) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM request_issues WHERE request_id = ?`, id); err != nil {
		return err
	}
	for _, is := range issues {
		detail := is.Detail
		if len(detail) == 0 {
			detail = json.RawMessage("{}")
		}
		if _, err := db.ExecContext(ctx,
			`INSERT OR REPLACE INTO request_issues (request_id, kind, level, detail) VALUES (?, ?, ?, ?)`,
			id, is.Kind, is.Level, string(detail)); err != nil {
			return err
		}
	}
	return nil
}

// Issues returns one request's issues, muted ones included.
func (s *Store) Issues(ctx context.Context, id int64) ([]Issue, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, level, detail FROM request_issues WHERE request_id = ? ORDER BY level DESC, kind`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Issue{}
	for rows.Next() {
		var is Issue
		var detail string
		if err := rows.Scan(&is.Kind, &is.Level, &detail); err != nil {
			return nil, err
		}
		is.Detail = json.RawMessage(detail)
		out = append(out, is)
	}
	return out, rows.Err()
}

// IssueCounts counts stored issues by kind, muted ones included.
func (s *Store) IssueCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, COUNT(*) FROM request_issues GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// IssueMutes reads the issue_mutes setting.
func (s *Store) IssueMutes(ctx context.Context) ([]IssueMute, error) {
	v, err := s.Setting(ctx, "issue_mutes")
	if err != nil || v == "" {
		return nil, err
	}
	var out []IssueMute
	if json.Unmarshal([]byte(v), &out) != nil {
		return nil, nil // a bad value mutes nothing
	}
	return out, nil
}

// ModelProps is what a model's llama-server reports about its limits and build.
type ModelProps struct {
	NCtx     int64  `json:"n_ctx"`     // context size per slot
	NPredict int64  `json:"n_predict"` // the server's default output limit; 0 or less for none
	Slots    int64  `json:"slots"`
	Build    string `json:"build,omitempty"` // such as "b11146-7fe450e19"; "" when not known
}

// SaveModelProps stores a model's props under each of its names (its ID and
// aliases), since requests may use any of them.
func (s *Store) SaveModelProps(ctx context.Context, names []string, p ModelProps, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, n := range names {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO model_props (model, n_ctx, n_predict, slots, build, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			n, p.NCtx, p.NPredict, p.Slots, nilIfEmpty(p.Build), at.UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetModelProps returns the last props saved for a model name, or nil.
func (s *Store) GetModelProps(ctx context.Context, model string) (*ModelProps, error) {
	var p ModelProps
	var predict, slots sql.NullInt64
	var build sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT n_ctx, n_predict, slots, build FROM model_props WHERE model = ?`, model).
		Scan(&p.NCtx, &predict, &slots, &build)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.NPredict, p.Slots, p.Build = predict.Int64, slots.Int64, build.String
	return &p, nil
}

// CheckRow is a stored request with what the issue checks read.
type CheckRow struct {
	ID           int64
	Path, Model  string
	State        string
	StatusCode   int
	Truncated    bool
	NCtx         *int64 // the context size stored with the request, if known
	RequestBody  []byte
	ResponseBody []byte
}

// CheckRows returns up to limit finished requests after id that still have
// their bodies, oldest first. With models set, only those models' requests
// whose context size isn't known are returned.
func (s *Store) CheckRows(ctx context.Context, after int64, limit int, models []string) ([]CheckRow, error) {
	q := `SELECT r.id, r.path, r.model, r.state, COALESCE(r.status_code, 0), b.truncated, r.n_ctx, b.request_body, b.response_body
		FROM requests r JOIN bodies b ON b.request_id = r.id
		WHERE r.id > ? AND r.state != ? AND b.response_body IS NOT NULL`
	args := []any{after, StateInFlight}
	if models != nil {
		names, _ := json.Marshal(models)
		q += ` AND r.n_ctx IS NULL AND r.model IN (SELECT value FROM json_each(?))`
		args = append(args, string(names))
	}
	q += ` ORDER BY r.id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CheckRow
	for rows.Next() {
		var r CheckRow
		if err := rows.Scan(&r.ID, &r.Path, &r.Model, &r.State, &r.StatusCode, &r.Truncated, &r.NCtx, &r.RequestBody, &r.ResponseBody); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetChecked stores the result of checking a request again.
func (s *Store) SetChecked(ctx context.Context, id int64, finishReason string, nCtx *int64, issues []Issue) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE requests SET finish_reason = ?, n_ctx = COALESCE(?, n_ctx) WHERE id = ?`,
		nilIfEmpty(finishReason), nCtx, id); err != nil {
		return err
	}
	if err := writeIssues(ctx, tx, id, issues); err != nil {
		return err
	}
	return tx.Commit()
}

// TagRow is a stored request with what the tags read.
type TagRow struct {
	ID           int64
	Path         string
	Streaming    bool
	Truncated    bool
	PromptTokens *int64
	CachedTokens *int64
	RequestBody  []byte
	ResponseBody []byte
}

// TagRows returns up to limit finished requests after id that still have
// their bodies, oldest first.
func (s *Store) TagRows(ctx context.Context, after int64, limit int) ([]TagRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.path, r.streaming, b.truncated, r.prompt_tokens, r.cached_tokens, b.request_body, b.response_body
		 FROM requests r JOIN bodies b ON b.request_id = r.id
		 WHERE r.id > ? AND r.state != ? ORDER BY r.id LIMIT ?`, after, StateInFlight, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagRow
	for rows.Next() {
		var r TagRow
		if err := rows.Scan(&r.ID, &r.Path, &r.Streaming, &r.Truncated, &r.PromptTokens, &r.CachedTokens, &r.RequestBody, &r.ResponseBody); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetTags stores a request's tags, a JSON list.
func (s *Store) SetTags(ctx context.Context, id int64, tags string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE requests SET tags = ? WHERE id = ?`, tags, id)
	return err
}
