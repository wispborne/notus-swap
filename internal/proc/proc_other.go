//go:build !linux && !windows

package proc

import (
	"os/exec"
	"strconv"
	"strings"
)

// Exes maps each running process's PID to the file it was started from, as
// ps reports it. On macOS, ps gives the full path.
func Exes() map[int]string {
	out := map[int]string{}
	b, err := exec.Command("ps", "-A", "-o", "pid=,comm=").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		pid, exe, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(pid); err == nil {
			out[n] = strings.TrimSpace(exe)
		}
	}
	return out
}
