package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/wispborne/notus-swap/internal/api"
)

// A copy of this test binary stands in for systemctl, including on Windows.
func TestMain(m *testing.M) {
	if path := os.Getenv("NOTUS_TEST_SYSTEMCTL_ARGS"); path != "" {
		args, _ := json.Marshal(os.Args[1:])
		if err := os.WriteFile(path, args, 0o600); err != nil {
			os.Exit(2)
		}
		if os.Getenv("NOTUS_TEST_SYSTEMCTL_FAIL") == "1" {
			fmt.Fprintln(os.Stderr, "Access denied")
			os.Exit(1)
		}
		if os.Args[1] == "show" {
			fmt.Println("inactive")
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLlamaSwapServiceControls(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "systemctl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), binary, 0o755); err != nil {
		t.Fatal(err)
	}
	argsPath := filepath.Join(dir, "args.json")
	t.Setenv("PATH", dir)
	t.Setenv("NOTUS_TEST_SYSTEMCTL_ARGS", argsPath)
	mux := http.NewServeMux()
	(&api.API{LlamaSwapUnit: "custom-llama.service", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Register(mux)

	for _, tc := range []struct {
		method, path string
		args         []string
		code         int
	}{
		{"GET", "/notus/api/service/llama-swap", []string{"show", "--property=ActiveState", "--value", "custom-llama.service"}, http.StatusOK},
		{"POST", "/notus/api/start/llama-swap", []string{"start", "custom-llama.service"}, http.StatusNoContent},
		{"POST", "/notus/api/stop/llama-swap", []string{"stop", "custom-llama.service"}, http.StatusNoContent},
		{"POST", "/notus/api/restart/llama-swap", []string{"restart", "custom-llama.service"}, http.StatusNoContent},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.code {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			data, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatal(err)
			}
			var args []string
			if err := json.Unmarshal(data, &args); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(args, tc.args) {
				t.Fatalf("systemctl args = %v, want %v", args, tc.args)
			}
			if tc.method == "GET" && !strings.Contains(w.Body.String(), `"state":"inactive"`) {
				t.Fatalf("service state: %s", w.Body.String())
			}
			t.Setenv("NOTUS_TEST_SYSTEMCTL_FAIL", "1")
			w = httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "Access denied") {
				t.Fatalf("failed command: status %d, body %s", w.Code, w.Body.String())
			}
		})
	}
}
