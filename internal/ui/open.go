package ui

import (
	"os/exec"
	"runtime"
)

// Open shows the pack window in an app-style browser window.
func Open(url string) error {
	candidates := [][]string{
		{"google-chrome", "--app=" + url},
		{"google-chrome-stable", "--app=" + url},
		{"chromium", "--app=" + url},
		{"chromium-browser", "--app=" + url},
		{"microsoft-edge", "--app=" + url},
		{"msedge", "--app=" + url},
		{"firefox", "--new-window", url},
	}
	if runtime.GOOS == "windows" {
		candidates = [][]string{
			{"msedge", "--app=" + url},
			{"chrome", "--app=" + url},
			{"firefox", "--new-window", url},
		}
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, candidate[1:]...)
		if err := cmd.Start(); err == nil {
			return nil
		}
	}
	if runtime.GOOS == "windows" {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}
