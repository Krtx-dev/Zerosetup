//go:build windows

package engine

import "os"

// pidAlive reports whether a process exists. On Windows, FindProcess opens a
// handle and fails if the process does not exist.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
