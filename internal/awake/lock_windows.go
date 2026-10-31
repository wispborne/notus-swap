package awake

import (
	"log/slog"
	"runtime"

	"golang.org/x/sys/windows"
)

const supported = true

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

// startLock starts the goroutine that takes and releases the lock. Windows
// keeps the execution state per OS thread, and Go moves goroutines between
// threads, so one goroutine locked to its thread makes every call. The
// display may still turn off (no ES_DISPLAY_REQUIRED).
func startLock(log *slog.Logger) func(on bool) {
	ch := make(chan bool)
	go func() {
		runtime.LockOSThread()
		for on := range ch {
			flags := uintptr(esContinuous)
			if on {
				flags |= esSystemRequired
			}
			if r, _, err := setThreadExecutionState.Call(flags); r == 0 {
				log.Warn("SetThreadExecutionState failed", "on", on, "err", err)
			}
		}
	}()
	return func(on bool) { ch <- on }
}
