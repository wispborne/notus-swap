package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Exes maps each running process's PID to the file it was started from.
// Processes it isn't allowed to read are left out.
func Exes() map[int]string {
	out := map[int]string{}
	links, _ := filepath.Glob("/proc/[0-9]*/exe")
	for _, l := range links {
		pid, err := strconv.Atoi(filepath.Base(filepath.Dir(l)))
		if err != nil {
			continue
		}
		if exe, err := os.Readlink(l); err == nil {
			out[pid] = strings.TrimSuffix(exe, " (deleted)")
		}
	}
	return out
}
