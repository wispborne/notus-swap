package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

// listener returns the socket systemd passed in, if notus-swap.socket
// started it. Otherwise it listens on addr. systemd keeps its socket open
// while notus-swap restarts, so requests wait for the new process instead
// of being refused.
func listener(addr string) (ln net.Listener, fromSystemd bool, err error) {
	n := systemdSockets(os.Getenv, os.Getpid())
	if n == 0 {
		ln, err = net.Listen("tcp", addr)
		return ln, false, err
	}
	// Programs notus-swap runs must not think the sockets are theirs.
	os.Unsetenv("LISTEN_PID")
	os.Unsetenv("LISTEN_FDS")
	os.Unsetenv("LISTEN_FDNAMES")
	if n > 1 {
		return nil, true, fmt.Errorf("systemd passed in %d sockets; notus-swap.socket should have one ListenStream line", n)
	}
	// systemd's sockets start at file descriptor 3.
	f := os.NewFile(3, "systemd socket")
	defer f.Close()
	ln, err = net.FileListener(f)
	if err != nil {
		return nil, true, fmt.Errorf("using the socket from systemd: %w", err)
	}
	return ln, true, nil
}

// systemdSockets is how many sockets systemd passed to this process: the
// LISTEN_FDS variable, when LISTEN_PID names this process.
func systemdSockets(getenv func(string) string, pid int) int {
	if p, err := strconv.Atoi(getenv("LISTEN_PID")); err != nil || p != pid {
		return 0
	}
	n, err := strconv.Atoi(getenv("LISTEN_FDS"))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
