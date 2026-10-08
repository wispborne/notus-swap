package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/wispborne/notus-swap/internal/proc"
)

// Process runs llama-swap as a program that notus-swap starts itself, for
// systems without systemd. llama-swap is found by its binary's path, so one
// started by an earlier notus-swap, or by hand, is found too.
//
// llama-swap gets its own process group, so Ctrl-C in notus-swap's window
// doesn't reach it. notus-swap stops it when notus-swap shuts down, and
// leaves it running when notus-swap restarts.
type Process struct {
	Bin    string
	Config string
	Listen string   // such as 127.0.0.1:8081
	Args   []string // added after -config and -listen, such as -watch-config
	Log    *slog.Logger
	// Where llama-swap's output goes. Use files such as os.Stdout, so the
	// output goes straight there and keeps going after notus-swap exits.
	Stdout, Stderr *os.File

	op sync.Mutex // one Start or Stop at a time

	mu       sync.Mutex
	changing string // "activating" or "deactivating" while Start or Stop runs
	cmd      *exec.Cmd
	wanted   bool            // false after Stop, so its exit isn't restarted
	ctx      context.Context // from Run; no restarts once it ends
}

func (p *Process) Kind() string { return "process" }

// stopWait is how long llama-swap gets to stop before it is killed.
const stopWait = 15 * time.Second

// Run starts llama-swap unless it is already running.
func (p *Process) Run(ctx context.Context) {
	p.mu.Lock()
	p.ctx = ctx
	p.mu.Unlock()
	if pids := p.running(); len(pids) > 0 {
		p.Log.Info("llama-swap is already running; using it", "pid", pids)
		return
	}
	if err := p.Start(ctx); err != nil {
		p.Log.Error("starting llama-swap", "err", err)
	}
}

func (p *Process) running() []int { return proc.Under(p.Bin) }

func (p *Process) State(ctx context.Context) (string, error) {
	p.mu.Lock()
	changing := p.changing
	p.mu.Unlock()
	if changing != "" {
		return changing, nil
	}
	if len(p.running()) > 0 {
		return "active", nil
	}
	return "inactive", nil
}

func (p *Process) setChanging(s string) {
	p.mu.Lock()
	p.changing = s
	p.mu.Unlock()
}

// Start starts llama-swap, unless it is already running. It returns an
// error if llama-swap exits within a second.
func (p *Process) Start(ctx context.Context) error {
	p.op.Lock()
	defer p.op.Unlock()
	if len(p.running()) > 0 {
		return nil
	}
	if p.Config == "" {
		return errors.New("set NOTUS_LLAMA_SWAP_CONFIG, so notus-swap knows how to start llama-swap")
	}
	if _, err := os.Stat(p.Bin); err != nil {
		return fmt.Errorf("llama-swap binary not found at %s (set NOTUS_LLAMA_SWAP_BIN)", p.Bin)
	}
	p.setChanging("activating")
	defer p.setChanging("")

	args := append([]string{"-config", p.Config, "-listen", p.Listen}, p.Args...)
	cmd := exec.Command(p.Bin, args...)
	cmd.Stdout, cmd.Stderr = discardNil(p.Stdout), discardNil(p.Stderr)
	cmd.SysProcAttr = ownGroup()
	if err := cmd.Start(); err != nil {
		return err
	}
	p.Log.Info("started llama-swap", "pid", cmd.Process.Pid, "bin", p.Bin, "listen", p.Listen)
	exited := make(chan error, 1)
	p.mu.Lock()
	p.cmd, p.wanted = cmd, true
	p.mu.Unlock()
	go p.watch(cmd, exited, time.Now())

	select {
	case err := <-exited:
		return fmt.Errorf("llama-swap stopped as soon as it started (%v). Its output is in notus-swap's window or log", err)
	case <-time.After(time.Second):
		return nil
	case <-ctx.Done():
		return nil
	}
}

// watch waits for llama-swap to exit. If it wasn't stopped on purpose, it
// starts it again, unless it ran for under 10 seconds: then something such as
// the config is wrong, and starting it again would fail the same way.
func (p *Process) watch(cmd *exec.Cmd, exited chan<- error, began time.Time) {
	err := cmd.Wait()
	if err == nil {
		err = errors.New("exit status 0")
	}
	exited <- err
	p.mu.Lock()
	// A restart may already have started a newer llama-swap.
	current := p.cmd == cmd
	if current {
		p.cmd = nil
	}
	wanted, ctx := p.wanted && current, p.ctx
	p.mu.Unlock()
	if !wanted || ctx == nil || ctx.Err() != nil {
		return
	}
	ran := time.Since(began)
	if ran < 10*time.Second {
		p.Log.Error("llama-swap stopped on its own soon after starting; not starting it again", "err", err, "ran", ran.Round(time.Millisecond))
		return
	}
	p.Log.Warn("llama-swap stopped on its own; starting it again", "err", err)
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Second):
	}
	if err := p.Start(ctx); err != nil {
		p.Log.Error("starting llama-swap again", "err", err)
	}
}

// Stop stops every llama-swap started from Bin, and the model servers it
// started.
func (p *Process) Stop(ctx context.Context) error {
	p.op.Lock()
	defer p.op.Unlock()
	p.mu.Lock()
	p.wanted = false
	p.mu.Unlock()
	pids := p.running()
	if len(pids) == 0 {
		return nil
	}
	p.setChanging("deactivating")
	defer p.setChanging("")
	for _, pid := range pids {
		if err := stopProcess(pid, false); err != nil {
			p.Log.Warn("stopping llama-swap", "pid", pid, "err", err)
		}
	}
	if p.waitGone(ctx, stopWait) {
		return nil
	}
	for _, pid := range p.running() {
		stopProcess(pid, true)
	}
	if p.waitGone(ctx, 5*time.Second) {
		return nil
	}
	return fmt.Errorf("llama-swap is still running (PID %v)", p.running())
}

func (p *Process) waitGone(ctx context.Context, limit time.Duration) bool {
	until := time.Now().Add(limit)
	for time.Now().Before(until) {
		if len(p.running()) == 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return len(p.running()) == 0
		case <-time.After(200 * time.Millisecond):
		}
	}
	return len(p.running()) == 0
}

func (p *Process) Restart(ctx context.Context) error {
	if err := p.Stop(ctx); err != nil {
		return err
	}
	return p.Start(ctx)
}

func discardNil(f *os.File) io.Writer {
	if f == nil {
		return io.Discard
	}
	return f
}
