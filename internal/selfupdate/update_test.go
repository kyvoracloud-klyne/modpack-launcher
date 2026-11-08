package selfupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNeedsUpdate(t *testing.T) {
	if NeedsUpdate("1.0.0", "v1.0.0") {
		t.Fatal("same version should not update")
	}
	if !NeedsUpdate("1.0.0", "v1.0.1") {
		t.Fatal("newer tag should update")
	}
	if !NeedsUpdate("dev", "v1.0.0") {
		t.Fatal("dev build should update")
	}
	if NeedsUpdate("1.0.0", "") {
		t.Fatal("empty remote should not update")
	}
}

func TestReplace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("replace runs after the windows helper exits")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "modpack-launcher")
	newer := filepath.Join(dir, "modpack-launcher.new")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Replace(newer, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("binary = %q", got)
	}
}

func TestWindowsHelperWaitsForPid(t *testing.T) {
	script := WindowsHelper(4242, `C:\Launcher\modpack-launcher.exe.new`, `C:\Launcher\modpack-launcher.exe`)
	for _, part := range []string{
		`set "PID=4242"`,
		`tasklist /FI "PID eq %PID%"`,
		`move /Y "%SRC%" "%DST%"`,
	} {
		if !strings.Contains(script, part) {
			t.Fatalf("helper missing %q:\n%s", part, script)
		}
	}
}

func TestAssetName(t *testing.T) {
	name, err := AssetName()
	if runtime.GOARCH != "amd64" {
		if err == nil {
			t.Fatal("expected arch error")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" && name != "modpack-launcher-linux-amd64" {
		t.Fatalf("asset = %s", name)
	}
	if runtime.GOOS == "windows" && name != "modpack-launcher-windows-amd64.exe" {
		t.Fatalf("asset = %s", name)
	}
}
