//go:build windows

package swap

import "syscall"

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel.NewProc("OpenProcess")
	closeHandle := kernel.NewProc("CloseHandle")
	const processQueryLimitedInformation = 0x1000
	handle, _, _ := openProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 {
		return false
	}
	_, _, _ = closeHandle.Call(handle)
	return true
}
