//go:build !windows

package main

import "os"

// forward passes a signal on to the worker. One sent by the terminal, such as
// Ctrl-C, reaches the worker twice; it ignores the second.
func forward(p *os.Process, sig os.Signal) { p.Signal(sig) }
