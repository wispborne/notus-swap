package main

import (
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wispborne/notus-swap/internal/selfupdate"
)

// Without systemd, nothing would start notus-swap again after the Restart
// button, an update, or a roll back. So notus-swap starts a copy of itself as
// a child process (the worker) and waits. When the worker exits with
// restartCode, it starts it again.
//
// The parent keeps running from the file it started as. An update renames
// that file to .prev and puts the new version in its place, so the next
// worker runs the new version.

// restartCode is the exit code a worker uses to ask to be started again.
const restartCode = 75

// supervisedEnv is set to "1" in a worker's environment.
const supervisedEnv = "NOTUS_SUPERVISED"

func supervised() bool { return os.Getenv(supervisedEnv) == "1" }

// underSystemd reports whether systemd started this process. systemd sets
// INVOCATION_ID for every service it starts.
func underSystemd() bool { return os.Getenv("INVOCATION_ID") != "" }

// wantSupervisor reports whether this process should supervise a worker.
// NOTUS_SUPERVISE=0 turns it off, for when something else restarts
// notus-swap.
func wantSupervisor() bool {
	return !supervised() && !underSystemd() && os.Getenv("NOTUS_SUPERVISE") != "0"
}

// selfRestart names what starts notus-swap again after a restart:
// "supervisor", "systemd", or "" for something else.
func selfRestart() string {
	switch {
	case supervised():
		return "supervisor"
	case underSystemd():
		return "systemd"
	}
	return ""
}

// restartExitCode is the exit code that gets notus-swap started again: under
// the supervisor, restartCode. systemd's Restart=always restarts after any
// exit, so there it is 0, as before.
func restartExitCode() int {
	if supervised() {
		return restartCode
	}
	return 0
}

// supervise runs workers until one exits without asking for a restart, and
// returns that worker's exit code.
func supervise(log *slog.Logger) int {
	exe, err := os.Executable()
	if err != nil {
		log.Error("finding notus-swap's own binary", "err", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// Ctrl-C reaches the worker on its own: it shares this console or
	// terminal. A signal sent to this process alone is passed on.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	updates := &selfupdate.Updater{Exe: exe}
	failures := 0 // failed exits in a row while an update is being confirmed
	for {
		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Env = append(os.Environ(), supervisedEnv+"=1")
		if err := cmd.Start(); err != nil {
			// Such as a new version built for another system.
			if updates.Pending() != nil && updates.RollBackFailed("new version could not start: "+err.Error()) == nil {
				log.Error("the new version of notus-swap could not start; put the previous version back", "err", err)
				continue
			}
			log.Error("starting notus-swap", "err", err)
			return 1
		}
		code := wait(cmd, sigs)
		switch {
		case code == restartCode:
			failures = 0
			log.Info("restarting notus-swap")
		case code != 0 && updates.Pending() != nil:
			// A new version stopped before confirming itself. Each start
			// counts, and its third start puts the previous version back. If
			// it stops before it can count its starts, this puts it back.
			failures++
			if failures > selfupdate.MaxAttempts {
				if err := updates.RollBackFailed("new version kept stopping as soon as it started"); err != nil {
					log.Error("putting the previous version of notus-swap back", "err", err)
					return code
				}
				log.Error("the new version of notus-swap kept stopping; put the previous version back")
				failures = 0
				continue
			}
			log.Warn("the new version of notus-swap stopped; starting it again", "exit", code)
			time.Sleep(time.Second)
		default:
			return code
		}
	}
}

// wait waits for the worker to exit, passing on signals, and returns its
// exit code.
func wait(cmd *exec.Cmd, sigs <-chan os.Signal) int {
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	for {
		select {
		case <-done:
			if code := cmd.ProcessState.ExitCode(); code >= 0 {
				return code
			}
			return 1 // ended by a signal
		case sig := <-sigs:
			forward(cmd.Process, sig)
		}
	}
}
