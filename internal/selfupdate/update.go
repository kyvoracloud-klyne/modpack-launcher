package selfupdate

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"modpack-launcher/internal/githubrel"
)

// AssetName is the release file for this operating system.
func AssetName() (string, error) {
	if runtime.GOARCH != "amd64" {
		return "", fmt.Errorf("self-update supports amd64, this process is %s", runtime.GOARCH)
	}
	if runtime.GOOS == "windows" {
		return "modpack-launcher-windows-amd64.exe", nil
	}
	if runtime.GOOS == "linux" {
		return "modpack-launcher-linux-amd64", nil
	}
	return "", fmt.Errorf("self-update supports linux and windows, this process is %s", runtime.GOOS)
}

// NormalizeVersion strips a leading v so tags and build versions compare equal.
func NormalizeVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

// NeedsUpdate reports whether the release tag is a different build.
// A local "dev" build updates whenever a release exists.
func NeedsUpdate(local, remote string) bool {
	local = NormalizeVersion(local)
	remote = NormalizeVersion(remote)
	if remote == "" {
		return false
	}
	if local == "dev" || local == "" {
		return true
	}
	return local != remote
}

// Check downloads nothing. It returns the latest release and whether it differs.
func Check(ctx context.Context, client *githubrel.Client, owner, repo, local string) (githubrel.Release, bool, error) {
	release, err := client.LatestRelease(ctx, owner, repo)
	if err != nil {
		return githubrel.Release{}, false, err
	}
	return release, NeedsUpdate(local, release.TagName), nil
}

// Download fetches the asset for this OS next to the running executable.
func Download(ctx context.Context, client *githubrel.Client, release githubrel.Release, exePath string) (string, error) {
	name, err := AssetName()
	if err != nil {
		return "", err
	}
	asset, ok := githubrel.FindAsset(release, name)
	if !ok {
		return "", fmt.Errorf("release %s has no %s", release.TagName, name)
	}
	dest := exePath + ".new"
	if err := client.Download(ctx, asset, dest); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dest, 0o755); err != nil {
			return "", err
		}
	}
	return dest, nil
}

// Replace swaps newPath into targetPath. On Linux the running binary can be replaced.
// On Windows the caller must exit first and use a helper, because the exe is locked.
func Replace(newPath, targetPath string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("windows cannot replace a running executable; use a helper")
	}
	backup := targetPath + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(targetPath, backup); err != nil {
		return err
	}
	if err := os.Rename(newPath, targetPath); err != nil {
		_ = os.Rename(backup, targetPath)
		return err
	}
	return nil
}

// WindowsHelper is a batch file that waits for pid, then moves the new exe into place.
func WindowsHelper(pid int, source, target string) string {
	return fmt.Sprintf(`@echo off
set "PID=%d"
set "SRC=%s"
set "DST=%s"
:wait
tasklist /FI "PID eq %%PID%%" | find "%%PID%%" >nul
if %%ERRORLEVEL%%==0 (
  ping -n 2 127.0.0.1 >nul
  goto wait
)
move /Y "%%SRC%%" "%%DST%%"
`, pid, quoteBat(source), quoteBat(target))
}

func quoteBat(path string) string {
	return strings.ReplaceAll(path, `"`, `""`)
}
