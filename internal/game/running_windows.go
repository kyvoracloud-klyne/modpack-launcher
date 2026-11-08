//go:build windows

package game

import (
	"os/exec"
	"strings"
)

// Running reports whether Cosmoteer.exe is a live process.
func Running() bool {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq Cosmoteer.exe", "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "cosmoteer.exe")
}
