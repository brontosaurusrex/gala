//go:build !windows

package main

import (
	"errors"
	"syscall"
)

func processAlive(pid int) bool {
	if pid < 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
