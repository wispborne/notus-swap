package api

import (
	"flag"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// The debug endpoints show settings, sensors and the log as plain data, so
// they can be checked with curl instead of a shell on the server.

func (a *API) registerDebug(r *router) {
	r.handle("GET /notus/api/debug/settings", "Every flag and where its value came from, plus the OS and how restarts work. Secrets show only set or unset.", a.debugSettings)
	r.handle("GET /notus/api/debug/sensors", "What the GPU, CPU and RAM samplers read right now, with the raw values.", a.debugSensors)
	r.handle("GET /notus/api/logs", "notus-swap's own log as plain text. Query: tail (the last N lines).", a.logTail)
}

// Setting is one flag in effect.
type Setting struct {
	Flag string `json:"flag"`
	// Env is the env setting that can set this flag, if there is one.
	Env   string `json:"env,omitempty"`
	Value string `json:"value"`
	// Source is "flag", "env" or "default".
	Source string `json:"source"`
	// EnvValue is the env setting as written, when it is set.
	EnvValue string `json:"env_value,omitempty"`
	Note     string `json:"note,omitempty"`
}

// EnvVar is one environment variable that notus-swap reads.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FlagSettings lists every flag in fs with its value and where the value
// came from. envOf names the env setting behind each flag that has one.
// Flags that read a number from the env ignore a value that isn't one.
func FlagSettings(fs *flag.FlagSet, envOf map[string]string, getenv func(string) string) []Setting {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var out []Setting
	fs.VisitAll(func(f *flag.Flag) {
		s := Setting{Flag: f.Name, Env: envOf[f.Name], Value: f.Value.String(), Source: "default"}
		if s.Env != "" {
			s.EnvValue = getenv(s.Env)
		}
		switch {
		case set[f.Name]:
			s.Source = "flag"
		case s.EnvValue != "" && !isNumber(f.Value, s.EnvValue):
			s.Note = "the env value is not a number, so the default is used"
		case s.EnvValue != "":
			s.Source = "env"
		}
		if secret(f.Name) || secret(s.Env) {
			s.Value, s.EnvValue = setOrUnset(s.Value), setOrUnset(s.EnvValue)
		} else {
			s.Value, s.EnvValue = hideURLPassword(s.Value), hideURLPassword(s.EnvValue)
		}
		out = append(out, s)
	})
	return out
}

// isNumber reports whether v is usable for a flag: any text for a text flag,
// a number for a number flag.
func isNumber(f flag.Value, v string) bool {
	g, ok := f.(flag.Getter)
	if !ok {
		return true
	}
	switch g.Get().(type) {
	case float64:
		_, err := strconv.ParseFloat(v, 64)
		return err == nil
	case int, int64:
		_, err := strconv.ParseInt(v, 10, 64)
		return err == nil
	}
	return true
}

// envPrefixes and envNames pick the environment variables shown.
var (
	envPrefixes = []string{"NOTUS_", "GITEA_", "GITHUB_"}
	envNames    = []string{"INVOCATION_ID", "LISTEN_PID", "LISTEN_FDS"}
)

// EnvSettings lists the environment variables notus-swap reads, from
// environ (as os.Environ gives it), sorted by name. Secrets show only
// whether they are set.
func EnvSettings(environ []string) []EnvVar {
	out := []EnvVar{}
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		if !slices.Contains(envNames, name) && !slices.ContainsFunc(envPrefixes, func(p string) bool { return strings.HasPrefix(name, p) }) {
			continue
		}
		if secret(name) {
			value = setOrUnset(value)
		} else {
			value = hideURLPassword(value)
		}
		out = append(out, EnvVar{Name: name, Value: value})
	}
	slices.SortFunc(out, func(a, b EnvVar) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// secret reports whether a setting's name marks it as a secret.
func secret(name string) bool {
	n := strings.ToUpper(name)
	for _, w := range []string{"TOKEN", "PASSWORD", "SECRET", "KEY"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

func setOrUnset(v string) string {
	if v == "" {
		return "unset"
	}
	return "set"
}

// hideURLPassword hides the password in a URL such as
// https://user:pass@host/. Other text is returned as it is.
func hideURLPassword(v string) string {
	u, err := url.Parse(v)
	if err != nil || u.User == nil {
		return v
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "hidden")
	}
	return u.String()
}

// debugSettings: GET /notus/api/debug/settings. Every flag in effect, the
// environment variables notus-swap reads, and what it runs on.
func (a *API) debugSettings(w http.ResponseWriter, r *http.Request) {
	restarter := a.SelfRestart
	if restarter == "" {
		restarter = "none"
	}
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()
	out := map[string]any{
		"version":    a.Version,
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"go_version": runtime.Version(),
		// What starts notus-swap again after a restart: systemd, supervisor, or none.
		"service_mode": restarter,
		"pid":          os.Getpid(),
		"executable":   exe,
		"working_dir":  cwd,
		"flags":        a.Flags,
		"env":          EnvSettings(os.Environ()),
	}
	if a.Store != nil {
		// The Settings page can change these after start-up.
		out["retention_in_effect"] = a.RetentionSetting(r.Context(), a.RetentionDefault)
	}
	if a.Updater != nil {
		out["update_source"] = a.Updater.Source()
		out["update_source_saved"] = a.Updater.Override().Kind != ""
	}
	writeJSON(w, out)
}

// debugSensors: GET /notus/api/debug/sensors. What each sampler reads right
// now, with the raw files and output behind it.
func (a *API) debugSensors(w http.ResponseWriter, r *http.Request) {
	if a.Metrics == nil {
		http.Error(w, "sensors unavailable", http.StatusServiceUnavailable)
		return
	}
	out := map[string]any{"memory": a.Metrics.InspectMemory()}
	if a.Metrics.GPUs != nil {
		out["gpus"] = a.Metrics.GPUs.Inspect()
	}
	if a.Metrics.CPU != nil {
		out["cpu"] = a.Metrics.CPU.Inspect()
	}
	// What the status bar shows, worked out from the last second's readings.
	l := a.Metrics.Latest()
	out["latest"] = map[string]any{
		"at": l.At, "gpu_watts": l.GPUWatts, "cpu_watts": l.CPUWatts, "cpu_source": l.CPUSource,
		"cpu_problem": l.CPUProblem, "system_watts": l.SystemWatts, "ram_used": l.RAMUsed, "ram_total": l.RAMTotal,
		"system_base_watts": a.Metrics.BaseWatts, "psu_efficiency": a.Metrics.PSUEff,
	}
	writeJSON(w, out)
}

// logTail: GET /notus/api/logs?tail=N. notus-swap's own log as plain text,
// the last N lines, or all of it without tail. Unlike /logs/stream, the
// answer ends.
func (a *API) logTail(w http.ResponseWriter, r *http.Request) {
	if a.Logs == nil {
		http.Error(w, "notus-swap log buffer unavailable", http.StatusServiceUnavailable)
		return
	}
	n := -1
	if t := r.URL.Query().Get("tail"); t != "" {
		v, err := strconv.Atoi(t)
		if err != nil || v < 0 {
			http.Error(w, "tail must be a whole number of lines", http.StatusBadRequest)
			return
		}
		n = v
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(lastLines(a.Logs.Bytes(), n))
}

// lastLines returns the last n lines of b, or all of b when n is negative.
func lastLines(b []byte, n int) []byte {
	if n < 0 {
		return b
	}
	if n == 0 {
		return nil
	}
	end := len(b)
	if end > 0 && b[end-1] == '\n' {
		end--
	}
	for i := end - 1; i >= 0; i-- {
		if b[i] == '\n' {
			if n--; n == 0 {
				return b[i+1:]
			}
		}
	}
	return b
}
