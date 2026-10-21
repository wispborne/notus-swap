package memory

import (
	"os"
	"strings"
)

// Inspection says where RAM use is read from, and what was read.
type Inspection struct {
	// Source is "meminfo" (a file such as /proc/meminfo) or
	// "GlobalMemoryStatusEx" (Windows).
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	// Lines holds the meminfo lines the reading uses, as written.
	Lines map[string]string `json:"lines,omitempty"`
	Error string            `json:"error,omitempty"`
	Used  *int64            `json:"used"`
	Total *int64            `json:"total"`
}

// InspectFile reads RAM use from a meminfo file and keeps the lines used.
func InspectFile(path string) Inspection {
	in := Inspection{Source: "meminfo", Path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		in.Error = err.Error()
		return in
	}
	in.Lines = map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if key, value, ok := strings.Cut(line, ":"); ok && (key == "MemTotal" || key == "MemAvailable") {
			in.Lines[key] = strings.TrimSpace(value)
		}
	}
	in.Used, in.Total = Read(path)
	if in.Used == nil {
		in.Error = "MemTotal or MemAvailable is missing or doesn't make sense"
	}
	return in
}
