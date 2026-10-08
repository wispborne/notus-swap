//go:build !windows

package service

import "syscall"

// ownGroup starts llama-swap in its own process group, so Ctrl-C in
// notus-swap's terminal doesn't reach it, and stopping the group also stops
// the model servers llama-swap started.
func ownGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// stopProcess asks pid's process group to stop (SIGTERM), or kills it when
// force is set. llama-swap stops its model servers on SIGTERM. A llama-swap
// that notus-swap didn't start may not lead its own group; then only it gets
// the signal.
func stopProcess(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(-pid, sig); err == nil {
		return nil
	}
	return syscall.Kill(pid, sig)
}
