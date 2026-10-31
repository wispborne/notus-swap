//go:build !windows

package awake

import "log/slog"

const supported = false

// startLock returns nil: there is no sleep lock to take here.
func startLock(*slog.Logger) func(on bool) { return nil }
