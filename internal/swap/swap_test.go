package swap

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBeginAndRestore(t *testing.T) {
	game := t.TempDir()
	bin := filepath.Join(game, "Bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "Cosmoteer.exe"), []byte("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "Cosmoteer.dll"), []byte("original-dll"), 0o644); err != nil {
		t.Fatal(err)
	}
	vanilla := filepath.Join(game, "Standard Mods", "Vanilla")
	if err := os.MkdirAll(vanilla, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vanilla, "mod.rules"), []byte("ID = vanilla.mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pack := t.TempDir()
	yaml := filepath.Join(pack, "Yet Another Mod Loader - 3577650065")
	if err := os.MkdirAll(filepath.Join(yaml, "Loader"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"mod.rules":       "ID = radistmorse.modloader\n",
		"Cosmoteer.dll":   "proxy-dll",
		"ModLoader.dll":   "loader",
		"unmanaged.dll":   "native",
		"winmm.dll":       "winmm",
		"Loader/keep.txt": "keep",
	} {
		if err := os.WriteFile(filepath.Join(yaml, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	emmanim := filepath.Join(pack, "emmanim_lag_fix", "Loader")
	if err := os.MkdirAll(emmanim, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "emmanim_lag_fix", "mod.rules"), []byte("ID = nayuri.emmanim_lag_fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(emmanim, "winmm.dll"), []byte("other-loader"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := filepath.Join(t.TempDir(), "swap-state.json")
	if err := Begin(state, game, pack); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		proxy, err := os.ReadFile(filepath.Join(bin, "winmm.dll"))
		if err != nil {
			t.Fatal(err)
		}
		if string(proxy) != "winmm" {
			t.Fatalf("winmm.dll = %q", proxy)
		}
	} else {
		proxy, err := os.ReadFile(filepath.Join(bin, "Cosmoteer.dll"))
		if err != nil {
			t.Fatal(err)
		}
		if string(proxy) != "proxy-dll" {
			t.Fatalf("dll = %q", proxy)
		}
		original, err := os.ReadFile(filepath.Join(bin, "Cosmoteer_o.dll"))
		if err != nil {
			t.Fatal(err)
		}
		if string(original) != "original-dll" {
			t.Fatalf("Cosmoteer_o.dll = %q", original)
		}
	}
	if _, err := os.Stat(filepath.Join(game, "Standard Mods", "Vanilla")); !os.IsNotExist(err) {
		t.Fatal("vanilla mods stayed in place during the swap")
	}
	if _, err := os.Stat(filepath.Join(game, "Standard Mods", "emmanim_lag_fix", "Loader", "winmm.dll")); !os.IsNotExist(err) {
		t.Fatal("Emmanim Loader was installed")
	}
	if _, err := os.Stat(filepath.Join(game, "Standard Mods", "Yet Another Mod Loader - 3577650065", "Loader", "keep.txt")); err != nil {
		t.Fatal(err)
	}

	if err := Restore(state); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(filepath.Join(bin, "Cosmoteer.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != "original-dll" {
		t.Fatalf("restored dll = %q", restored)
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(filepath.Join(bin, "Cosmoteer_o.dll")); !os.IsNotExist(err) {
			t.Fatal("Cosmoteer_o.dll was left in Bin")
		}
	}
	if _, err := os.Stat(filepath.Join(bin, "ModLoader.dll")); !os.IsNotExist(err) {
		t.Fatal("ModLoader.dll was left in Bin")
	}
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(filepath.Join(bin, "winmm.dll")); !os.IsNotExist(err) {
			t.Fatal("winmm.dll was left in Bin")
		}
	} else if _, err := os.Stat(filepath.Join(bin, "unmanaged.dll")); !os.IsNotExist(err) {
		t.Fatal("unmanaged.dll was left in Bin")
	}
	body, err := os.ReadFile(filepath.Join(game, "Standard Mods", "Vanilla", "mod.rules"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ID = vanilla.mod\n" {
		t.Fatalf("vanilla mod = %q", body)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("state file remained")
	}
}

func TestLinuxProxyLeavesExistingOriginal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows uses winmm.dll")
	}
	game := t.TempDir()
	bin := filepath.Join(game, "Bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "Cosmoteer.exe"), []byte("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "Cosmoteer.dll"), []byte("old-proxy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "Cosmoteer_o.dll"), []byte("real-game"), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := t.TempDir()
	yaml := filepath.Join(pack, "Yet Another Mod Loader - 3577650065")
	if err := os.MkdirAll(yaml, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"mod.rules":     "ID = radistmorse.modloader\n",
		"Cosmoteer.dll": "new-proxy",
		"ModLoader.dll": "loader",
		"unmanaged.dll": "native",
		"winmm.dll":     "winmm",
	} {
		if err := os.WriteFile(filepath.Join(yaml, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(t.TempDir(), "swap-state.json")
	if err := Begin(state, game, pack); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(bin, "Cosmoteer_o.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "real-game" {
		t.Fatalf("original was replaced: %q", got)
	}
	proxy, err := os.ReadFile(filepath.Join(bin, "Cosmoteer.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if string(proxy) != "new-proxy" {
		t.Fatalf("proxy = %q", proxy)
	}
	if err := Restore(state); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(filepath.Join(bin, "Cosmoteer.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != "old-proxy" {
		t.Fatalf("restored proxy = %q", restored)
	}
	kept, err := os.ReadFile(filepath.Join(bin, "Cosmoteer_o.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != "real-game" {
		t.Fatalf("original after restore = %q", kept)
	}
}

func TestShouldRestoreSkipsLiveOwner(t *testing.T) {
	if ShouldRestore(State{OwnerPID: os.Getpid()}, false) {
		t.Fatal("live launcher should keep the swap")
	}
	if ShouldRestore(State{}, true) {
		t.Fatal("running game should keep the swap")
	}
	if !ShouldRestore(State{}, false) {
		t.Fatal("dead session should restore")
	}
}
