// Package proc lists running programs by the file each was started from.
// Linux reads /proc, Windows asks the system for each process's image file,
// and other systems run ps.
package proc

import (
	"path/filepath"
	"runtime"
	"strings"
)

// Under returns the PIDs of programs started from a file whose path begins
// with prefix. A program whose file was renamed after it started is listed
// under the new name, so prefix "/opt/llama-swap" also finds one now named
// "/opt/llama-swap.bak".
func Under(prefix string) []int {
	prefix = clean(prefix)
	var pids []int
	for pid, exe := range Exes() {
		if strings.HasPrefix(clean(exe), prefix) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// InDir reports whether a running program was started from inside dir.
func InDir(dir string) bool {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return len(Under(strings.TrimRight(dir, `/\`)+string(filepath.Separator))) > 0
}

// clean makes paths comparable. Windows paths ignore case.
func clean(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}
