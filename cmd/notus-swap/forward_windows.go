package main

import "os"

// forward does nothing on Windows. Ctrl-C and closing the window reach every
// program in the console, the worker included, and Windows has no way to
// send another program a signal.
func forward(p *os.Process, sig os.Signal) {}
