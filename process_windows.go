//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func processAlive(pid int) bool {
	if pid < 1 {
		return false
	}
	output, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		// When process status cannot be checked, keep the lock rather than risk
		// allowing two Gala builds to write the same output directory.
		return true
	}
	needle := `","` + strconv.Itoa(pid) + `","`
	return strings.Contains(string(output), needle)
}
