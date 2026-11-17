//go:build !windows

package engine

import (
	"os"
	"syscall"
)

// pidAlive reports whether a process exists (signal 0 checks without sending).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
