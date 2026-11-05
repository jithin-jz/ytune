//go:build !windows

// Package proc holds the process handling that differs between platforms.
package proc

import "os/exec"

// KillChildrenOnExit does nothing here: mpv is stopped by ytune's normal cleanup.
func KillChildrenOnExit() {}

// Hide does nothing here: child processes have no console window to hide.
func Hide(*exec.Cmd) {}

// WaitIfOwnConsole does nothing here: ytune is always started from a terminal.
func WaitIfOwnConsole() {}
