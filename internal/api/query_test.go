package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wispborne/notus-swap/internal/api"
	"github.com/wispborne/notus-swap/internal/store"
)

type queryAnswer struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated"`
	Ms        *int64   `json:"ms"`
}

// queryServer serves the API over a fresh database. With on false, the SQL
// endpoint is off.
func queryServer(t *testing.T, on bool) (*store.Store, func(sql string) (int, string)) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := &api.API{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if on {
		ro, err := store.OpenReadOnly(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ro.Close() })
		a.ReadOnly = ro
	}
	mux := http.NewServeMux()
	a.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return st, func(sql string) (int, string) {
		body, _ := json.Marshal(map[string]string{"sql": sql})
		resp, err := http.Post(srv.URL+"/notus/api/query", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
}

func TestQuerySelect(t *testing.T) {
	st, query := queryServer(t, true)
	if err := st.SetSetting(context.Background(), "app_title", `"hello"`); err != nil {
		t.Fatal(err)
	}
	code, body := query(`SELECT key, value, 1.5 AS n, NULL AS z, x'00ff' AS raw, CAST('text' AS BLOB) AS b FROM settings`)
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var got queryAnswer
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Columns, ",") != "key,value,n,z,raw,b" {
		t.Errorf("columns = %v", got.Columns)
	}
	want := []any{"app_title", `"hello"`, 1.5, nil, "AP8=", "text"}
	if len(got.Rows) != 1 || len(got.Rows[0]) != len(want) {
		t.Fatalf("rows = %v", got.Rows)
	}
	for i, v := range want {
		if got.Rows[0][i] != v {
			t.Errorf("column %s = %#v, want %#v", got.Columns[i], got.Rows[0][i], v)
		}
	}
	if got.Truncated || got.Ms == nil {
		t.Errorf("truncated = %v, ms = %v", got.Truncated, got.Ms)
	}
}

func TestQueryRefusesWrites(t *testing.T) {
	st, query := queryServer(t, true)
	ctx := context.Background()
	if err := st.SetSetting(ctx, "app_title", `"hello"`); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`INSERT INTO settings (key, value) VALUES ('x', '1')`,
		`UPDATE settings SET value = '"changed"'`,
		`DELETE FROM settings`,
		`PRAGMA user_version = 99`,
		`CREATE TABLE t (a)`,
		// query_only can be turned off, but the file is still read-only.
		`PRAGMA query_only = 0; DELETE FROM settings`,
	} {
		if code, body := query(sql); code != 400 {
			t.Errorf("%s: status %d, want 400: %s", sql, code, body)
		}
	}
	// A refused write turned query_only off on the connection; the next
	// query turns it on again.
	if code, body := query(`DELETE FROM settings`); code != 400 {
		t.Errorf("delete after turning query_only off: status %d: %s", code, body)
	}
	v, err := st.Setting(ctx, "app_title")
	if err != nil || v != `"hello"` {
		t.Errorf("setting = %q, %v; the writes changed it", v, err)
	}
	_, body := query(`SELECT count(*) FROM settings`)
	if !strings.Contains(body, `"rows":[[1]]`) {
		t.Errorf("settings count: %s", body)
	}
}

func TestQueryRowCap(t *testing.T) {
	_, query := queryServer(t, true)
	code, body := query(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 6000) SELECT i FROM n`)
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var got queryAnswer
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 5000 || !got.Truncated {
		t.Errorf("%d rows, truncated %v; want 5000 and true", len(got.Rows), got.Truncated)
	}

	_, body = query(`SELECT 1 WHERE 0`)
	if !strings.Contains(body, `"rows":[]`) || !strings.Contains(body, `"truncated":false`) {
		t.Errorf("empty result: %s", body)
	}
}

func TestQueryErrors(t *testing.T) {
	_, query := queryServer(t, true)
	if code, body := query(`SELECT * FROM no_such_table`); code != 400 || !strings.Contains(body, "no_such_table") {
		t.Errorf("status %d: %s", code, body)
	}
	if code, _ := query(`  `); code != 400 {
		t.Errorf("empty sql: status %d", code)
	}
}

func TestQueryOffByDefault(t *testing.T) {
	_, query := queryServer(t, false)
	if code, body := query(`SELECT 1`); code != 404 {
		t.Errorf("status %d, want 404: %s", code, body)
	}
}
