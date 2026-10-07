package store

import (
	"context"
	"time"
)

// SpeedRow is a finished request whose speeds notus-swap may have measured
// itself, with what is needed to measure them again.
type SpeedRow struct {
	ID               int64
	StartedAt        time.Time
	FirstByteAt      time.Time
	FirstTokenAt     time.Time
	FinishedAt       time.Time
	PromptTokens     *int64
	CompletionTokens *int64
	CachedTokens     *int64
	PromptMs         *float64
	QueuedMs         *int64
	Body             []byte
}

// SpeedRows returns up to 20 finished Chat Completions requests after id
// whose first byte came before their first token and whose whole body is
// still kept. Responses API paths are left out: their first event comes
// before the prompt is done.
func (s *Store) SpeedRows(ctx context.Context, after int64) ([]SpeedRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.started_at, r.first_byte_at, r.first_token_at, r.finished_at, r.prompt_tokens,
			r.completion_tokens, r.cached_tokens, r.prompt_ms, r.queued_ms, b.response_body
		 FROM requests r JOIN bodies b ON b.request_id = r.id
		 WHERE r.id > ? AND r.state = ? AND r.first_byte_at < r.first_token_at
			AND r.path NOT LIKE '%/responses' AND b.response_body IS NOT NULL AND NOT b.truncated
		 ORDER BY r.id LIMIT 20`, after, StateDone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpeedRow
	for rows.Next() {
		var r SpeedRow
		var started, firstByte, firstToken, finished int64
		if err := rows.Scan(&r.ID, &started, &firstByte, &firstToken, &finished, &r.PromptTokens,
			&r.CompletionTokens, &r.CachedTokens, &r.PromptMs, &r.QueuedMs, &r.Body); err != nil {
			return nil, err
		}
		r.StartedAt, r.FirstByteAt = time.UnixMilli(started), time.UnixMilli(firstByte)
		r.FirstTokenAt, r.FinishedAt = time.UnixMilli(firstToken), time.UnixMilli(finished)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetSpeeds stores a request's first token time and measured speeds.
func (s *Store) SetSpeeds(ctx context.Context, id int64, firstTokenAt time.Time, promptMs, promptPerSecond, predictedMs, predictedPerSecond *float64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE requests SET first_token_at = ?, prompt_ms = ?, prompt_per_second = ?, predicted_ms = ?,
			predicted_per_second = ? WHERE id = ?`,
		firstTokenAt.UnixMilli(), promptMs, promptPerSecond, predictedMs, predictedPerSecond, id)
	return err
}
