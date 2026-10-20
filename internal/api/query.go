package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	queryTimeout = 10 * time.Second
	queryMaxRows = 5000
)

// query runs one read-only SQL statement against the database, for looking
// at data without a shell on the server. It answers 404 unless -sql-api
// turned it on.
func (a *API) query(w http.ResponseWriter, r *http.Request) {
	if a.ReadOnly == nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		SQL string `json:"sql"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "the body must be JSON with an sql field: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.SQL) == "" {
		http.Error(w, "sql is empty", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()
	start := time.Now()
	res, err := a.ReadOnly.Query(ctx, body.SQL, queryMaxRows)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			http.Error(w, "the query took longer than 10 seconds", http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSONGzip(w, r, struct {
		Columns   []string `json:"columns"`
		Rows      [][]any  `json:"rows"`
		Truncated bool     `json:"truncated"`
		Ms        int64    `json:"ms"`
	}{res.Columns, res.Rows, res.Truncated, time.Since(start).Milliseconds()})
}
