// Package memory reads system RAM use from Linux's meminfo.
package memory

import (
	"os"
	"strconv"
	"strings"
)

// Read returns used and total RAM in bytes. Linux counts reclaimable cache
// as available, so used is MemTotal minus MemAvailable.
func Read(path string) (used, total *int64) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var capacity, available int64
	var hasTotal, hasAvailable bool
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "kB" {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || value < 0 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			capacity, hasTotal = value, true
		case "MemAvailable:":
			available, hasAvailable = value, true
		}
	}
	if !hasTotal || !hasAvailable || capacity <= 0 || available > capacity {
		return nil, nil
	}
	usedBytes := (capacity - available) * 1024
	totalBytes := capacity * 1024
	return &usedBytes, &totalBytes
}
