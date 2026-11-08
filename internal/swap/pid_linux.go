//go:build linux

package swap

import (
	"fmt"
	"os"
)

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}
