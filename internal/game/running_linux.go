//go:build linux

package game

import (
	"os"
	"path/filepath"
	"strings"
)

// Running reports whether Cosmoteer.exe is a live process.
func Running() bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(comm)) == "Cosmoteer.exe" {
			return true
		}
	}
	return false
}
