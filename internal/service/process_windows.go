package service

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// ownGroup starts llama-swap in its own process group, so Ctrl-C in
// notus-swap's window doesn't reach it.
func ownGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// stopProcess ends pid and the programs it started. Windows has no gentle
// way to ask another console program to stop, so this always forces it.
// llama-swap's model servers keep no state, so nothing is lost.
func stopProcess(pid int, force bool) error {
	out, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
