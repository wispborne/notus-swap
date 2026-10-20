//go:build !windows

package memory

// System returns used and total RAM in bytes, read from meminfo (normally
// /proc/meminfo). Systems without it, such as macOS, report nothing.
func System(meminfo string) (used, total *int64) { return Read(meminfo) }

// InspectSystem says where System reads from, and what it read.
func InspectSystem(meminfo string) Inspection { return InspectFile(meminfo) }
