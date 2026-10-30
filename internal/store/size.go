package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
)

// DiskUsage is how much space the database takes, for the Settings page.
type DiskUsage struct {
	Path string `json:"path"`
	// FileBytes is the main database file. WALBytes is the write-ahead log
	// next to it, which SQLite folds back into the main file from time to time.
	FileBytes int64 `json:"file_bytes"`
	WALBytes  int64 `json:"wal_bytes"`
	// FreeBytes is space inside the file left by deleted rows. SQLite reuses
	// it for new rows, but the file doesn't shrink by itself.
	FreeBytes int64 `json:"free_bytes"`
	// BodiesBytes is the request and response text, the part retention limits.
	BodiesBytes int64 `json:"bodies_bytes"`
	Requests    int64 `json:"requests"`
	WithBodies  int64 `json:"with_bodies"`
	// OldestBody is when the oldest request that still has its text
	// started, in unix ms, or nil when none has.
	OldestBody *int64 `json:"oldest_body"`
}

// DiskUsage reads the file sizes and counts. It doesn't read every page, so
// it stays quick on a large database.
func (s *Store) DiskUsage(ctx context.Context) (DiskUsage, error) {
	u := DiskUsage{Path: s.path}
	if abs, err := filepath.Abs(s.path); err == nil {
		u.Path = abs
	}
	if fi, err := os.Stat(s.path); err == nil {
		u.FileBytes = fi.Size()
	}
	if fi, err := os.Stat(s.path + "-wal"); err == nil {
		u.WALBytes = fi.Size()
	}
	var free, pageSize int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return u, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return u, err
	}
	u.FreeBytes = free * pageSize
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(COALESCE(LENGTH(request_body),0) + COALESCE(LENGTH(response_body),0)), 0), COUNT(*) FROM bodies`,
	).Scan(&u.BodiesBytes, &u.WithBodies); err != nil {
		return u, err
	}
	// Bodies are deleted oldest first, and the lowest ID is the oldest.
	err := s.db.QueryRowContext(ctx,
		`SELECT r.started_at FROM bodies b JOIN requests r ON r.id = b.request_id ORDER BY b.request_id LIMIT 1`,
	).Scan(&u.OldestBody)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return u, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM requests`).Scan(&u.Requests)
	return u, err
}
