// Package service starts, stops and restarts llama-swap, for the Settings
// page and for llama-swap updates. On Linux, systemd usually runs llama-swap
// (Systemd). Elsewhere, notus-swap runs llama-swap itself (Process).
package service

import "context"

// Controller runs llama-swap.
type Controller interface {
	// Kind is "systemd" or "process".
	Kind() string
	// State uses systemd's words: "active", "inactive", "activating",
	// "deactivating" or "failed".
	State(ctx context.Context) (string, error)
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
}
