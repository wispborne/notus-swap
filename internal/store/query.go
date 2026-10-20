package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"math"
	"strconv"
	"unicode/utf8"
)

// ReadOnly is a second connection to the database that can't write. It is
// for running any SELECT sent to the SQL endpoint, and never shares the
// main connection.
type ReadOnly struct{ db *sql.DB }

// OpenReadOnly opens the database at path read-only. The file must already
// exist, so open it with Open first.
func OpenReadOnly(path string) (*ReadOnly, error) {
	// mode=ro makes SQLite open the file read-only, so no statement can
	// write. query_only is a second guard.
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &ReadOnly{db: db}, nil
}

func (r *ReadOnly) Close() error { return r.db.Close() }

// QueryResult is the answer to one query. Each row holds one value per
// column: a number, a string, or nil.
type QueryResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	// Truncated is true when the query had more rows than were returned.
	Truncated bool `json:"truncated"`
}

// Query runs query and returns at most maxRows rows. Bytes come back as a
// string when they are valid UTF-8, and as base64 otherwise.
func (r *ReadOnly) Query(ctx context.Context, query string, maxRows int) (*QueryResult, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	// An earlier query may have turned query_only off.
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only = 1`); err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &QueryResult{Columns: cols, Rows: [][]any{}}
	for rows.Next() {
		if len(res.Rows) == maxRows {
			res.Truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range vals {
			vals[i] = jsonValue(v)
		}
		res.Rows = append(res.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

// jsonValue turns a value from SQLite into one that encoding/json can write.
func jsonValue(v any) any {
	switch v := v.(type) {
	case []byte:
		if utf8.Valid(v) {
			return string(v)
		}
		return base64.StdEncoding.EncodeToString(v)
	case float64:
		// JSON has no infinity or NaN, and SQLite can return them.
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return strconv.FormatFloat(v, 'g', -1, 64)
		}
	}
	return v
}
