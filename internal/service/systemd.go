package service

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Systemd controls llama-swap's systemd unit with systemctl. The polkit rule
// in the install guide lets notus-swap's user do this without a password.
type Systemd struct {
	Unit string
}

func (s *Systemd) Kind() string { return "systemd" }

// State reads systemd's state even when llama-swap's HTTP server is down.
func (s *Systemd) State(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", "--property=ActiveState", "--value", s.Unit).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("reading service state: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Systemd) Start(ctx context.Context) error   { return s.control(ctx, "start") }
func (s *Systemd) Stop(ctx context.Context) error    { return s.control(ctx, "stop") }
func (s *Systemd) Restart(ctx context.Context) error { return s.control(ctx, "restart") }

func (s *Systemd) control(ctx context.Context, action string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", action, s.Unit).CombinedOutput()
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		text = err.Error()
	}
	if strings.Contains(text, "Interactive authentication required") || strings.Contains(text, "Access denied") {
		text += " (the polkit rule in docs/install.md lets notus-swap do this)"
	}
	return errors.New(text)
}
