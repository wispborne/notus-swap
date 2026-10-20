package api_test

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wispborne/notus-swap/internal/api"
	"github.com/wispborne/notus-swap/internal/gpu"
	"github.com/wispborne/notus-swap/internal/logbuf"
	"github.com/wispborne/notus-swap/internal/metrics"
	"github.com/wispborne/notus-swap/internal/rapl"
	"github.com/wispborne/notus-swap/internal/store"
)

func TestFlagSettings(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("listen", ":9000", "")
	fs.String("db", "x.db", "")
	fs.String("upstream", "http://127.0.0.1:8081", "")
	fs.Float64("psu-efficiency", 0.9, "")
	fs.String("drm-root", "/sys/class/drm", "")
	fs.String("api-token", "", "")
	fs.String("gitea-url", "https://bot:hunter2@git.example.com", "")
	if err := fs.Parse([]string{"-db", "y.db", "-api-token", "abc123"}); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"NOTUS_LISTEN": ":9000", "NOTUS_DB": "z.db", "NOTUS_PSU_EFFICIENCY": "ninety"}
	envOf := map[string]string{"listen": "NOTUS_LISTEN", "db": "NOTUS_DB", "upstream": "NOTUS_UPSTREAM", "psu-efficiency": "NOTUS_PSU_EFFICIENCY"}
	got := map[string]api.Setting{}
	for _, s := range api.FlagSettings(fs, envOf, func(k string) string { return env[k] }) {
		got[s.Flag] = s
	}
	check := func(name, value, source string) {
		t.Helper()
		if s := got[name]; s.Value != value || s.Source != source {
			t.Errorf("%s: %+v, want value %q from %s", name, s, value, source)
		}
	}
	check("listen", ":9000", "env")
	check("db", "y.db", "flag") // the flag wins over the env
	check("upstream", "http://127.0.0.1:8081", "default")
	check("psu-efficiency", "0.9", "default")
	check("drm-root", "/sys/class/drm", "default")
	check("api-token", "set", "flag")
	if s := got["psu-efficiency"]; s.EnvValue != "ninety" || s.Note == "" {
		t.Errorf("a bad number should be shown with a note: %+v", s)
	}
	if v := got["gitea-url"].Value; strings.Contains(v, "hunter2") || !strings.Contains(v, "bot") {
		t.Errorf("URL password not hidden: %q", v)
	}
}

func TestEnvSettingsHidesSecrets(t *testing.T) {
	out := api.EnvSettings([]string{
		"PATH=/usr/bin", "HOME=/home/someone",
		"NOTUS_LISTEN=:8080", "GITEA_TOKEN=abc123", "GITHUB_TOKEN=", "NOTUS_API_KEY=k",
		"GITEA_PASSWORD=p", "NOTUS_SECRET_THING=s", "GITEA_URL=https://u:pw@git.example.com/", "INVOCATION_ID=42",
	})
	got := map[string]string{}
	for _, e := range out {
		got[e.Name] = e.Value
	}
	want := map[string]string{
		"NOTUS_LISTEN": ":8080", "GITEA_TOKEN": "set", "GITHUB_TOKEN": "unset", "NOTUS_API_KEY": "set",
		"GITEA_PASSWORD": "set", "NOTUS_SECRET_THING": "set", "INVOCATION_ID": "42",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["PATH"]; ok {
		t.Error("PATH should not be listed")
	}
	if strings.Contains(got["GITEA_URL"], "pw") {
		t.Errorf("GITEA_URL password not hidden: %q", got["GITEA_URL"])
	}
	for i := 1; i < len(out); i++ {
		if out[i-1].Name > out[i].Name {
			t.Errorf("not sorted: %v", out)
		}
	}
}

func TestDebugEndpoints(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "do-not-show-this")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	drm := t.TempDir()
	dev := filepath.Join(drm, "card0", "device")
	for rel, v := range map[string]string{
		"vendor": "0x1002\n", "uevent": "PCI_SLOT_NAME=0000:03:00.0\n", "mem_info_vram_total": "34342961152\n",
		"hwmon/hwmon1/power1_average": "100000000\n",
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(dev, rel)), 0o755)
		if err := os.WriteFile(filepath.Join(dev, rel), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := &metrics.Recorder{Store: st, GPUs: gpu.NewSampler(drm), CPU: rapl.NewMeter(t.TempDir()), Log: log}
	rec.MemInfoPath = filepath.Join(t.TempDir(), "meminfo")
	os.WriteFile(rec.MemInfoPath, []byte("MemTotal: 8192 kB\nMemAvailable: 3072 kB\n"), 0o644)
	rec.Sample(context.Background(), time.Now())

	logs := logbuf.New(4096)
	logs.Write([]byte("one\ntwo\nthree\n"))
	a := &api.API{Store: st, Metrics: rec, Logs: logs, Log: log, Version: "test", SelfRestart: "systemd",
		RetentionDefault: api.Retention{Days: 90, GB: 20}, Flags: []api.Setting{{Flag: "listen", Value: ":8080", Source: "default"}}}
	mux := http.NewServeMux()
	a.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	code, body := get("/notus/api/debug/settings")
	if code != 200 || strings.Contains(body, "do-not-show-this") {
		t.Fatalf("settings %d: %s", code, body)
	}
	var settings struct {
		OS          string        `json:"os"`
		ServiceMode string        `json:"service_mode"`
		GoVersion   string        `json:"go_version"`
		Flags       []api.Setting `json:"flags"`
		Env         []api.EnvVar  `json:"env"`
		Retention   api.Retention `json:"retention_in_effect"`
	}
	json.Unmarshal([]byte(body), &settings)
	if settings.OS == "" || settings.ServiceMode != "systemd" || settings.GoVersion == "" || len(settings.Flags) != 1 || settings.Retention.Days != 90 {
		t.Errorf("settings: %+v", settings)
	}
	if !strings.Contains(body, `"GITEA_TOKEN","value":"set"`) {
		t.Errorf("GITEA_TOKEN should show as set: %s", body)
	}

	code, body = get("/notus/api/debug/sensors")
	var sensors struct {
		GPUs   gpu.Inspection  `json:"gpus"`
		CPU    rapl.Inspection `json:"cpu"`
		Memory map[string]any  `json:"memory"`
		Latest map[string]any  `json:"latest"`
	}
	if err := json.Unmarshal([]byte(body), &sensors); code != 200 || err != nil {
		t.Fatalf("sensors %d %v: %s", code, err, body)
	}
	if len(sensors.GPUs.Cards) != 1 || sensors.GPUs.Cards[0].Kind != "dedicated" || sensors.GPUs.Cards[0].PCI != "0000:03:00.0" {
		t.Errorf("cards: %+v", sensors.GPUs.Cards)
	}
	if sensors.CPU.Problem == "" || sensors.Memory["source"] != "meminfo" || sensors.Latest["gpu_watts"] != 100.0 {
		t.Errorf("cpu %+v, memory %v, latest %v", sensors.CPU, sensors.Memory, sensors.Latest)
	}

	for path, want := range map[string]string{
		"/notus/api/logs":         "one\ntwo\nthree\n",
		"/notus/api/logs?tail=2":  "two\nthree\n",
		"/notus/api/logs?tail=0":  "",
		"/notus/api/logs?tail=10": "one\ntwo\nthree\n",
	} {
		if code, body := get(path); code != 200 || body != want {
			t.Errorf("%s: %d %q, want %q", path, code, body, want)
		}
	}
	if code, _ := get("/notus/api/logs?tail=-1"); code != http.StatusBadRequest {
		t.Errorf("tail=-1: %d", code)
	}
}
