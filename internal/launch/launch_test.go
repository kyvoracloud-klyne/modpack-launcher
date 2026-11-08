package launch

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"modpack-launcher/internal/game"
	"modpack-launcher/internal/ships"
)

func TestPrepareCopiesShipsAndRestores(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "Bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "Cosmoteer.exe"), []byte("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte{0x89, 'P', 'N', 'G', 0x00}
	if err := os.WriteFile(filepath.Join(bin, "Cosmoteer.dll"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Standard Mods", "Mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Standard Mods", "Mine", "mod.rules"), []byte("ID = mine.mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	library := t.TempDir()
	saved := filepath.Join(library, "Saved Ships")
	lost := filepath.Join(library, "Lost Ships")
	if err := os.MkdirAll(saved, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lost, 0o755); err != nil {
		t.Fatal(err)
	}
	ship := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x00, 0xff}
	if err := os.WriteFile(filepath.Join(saved, "ship.png"), ship, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lost, "wreck.png"), []byte("lost"), 0o644); err != nil {
		t.Fatal(err)
	}
	profileShip := filepath.Join(game.ProfileDir(root), "Saved Ships", "ship.png")
	if err := os.MkdirAll(filepath.Dir(profileShip), 0o755); err != nil {
		t.Fatal(err)
	}
	edited := []byte("edited-in-profile")
	if err := os.WriteFile(profileShip, edited, 0o644); err != nil {
		t.Fatal(err)
	}

	pack := filepath.Join(t.TempDir(), "pack")
	yaml := filepath.Join(pack, "Yet Another Mod Loader - 3577650065")
	if err := os.MkdirAll(yaml, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"mod.rules":     "ID = radistmorse.modloader\n",
		"Cosmoteer.dll": "proxy",
		"ModLoader.dll": "loader",
		"unmanaged.dll": "native",
		"winmm.dll":     "winmm",
	} {
		if err := os.WriteFile(filepath.Join(yaml, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	restore, err := Prepare(Options{
		Install:   game.Install{Root: root, Bin: bin},
		PackDir:   pack,
		StatePath: filepath.Join(t.TempDir(), "state.json"),
		Ships:     ships.Libraries{SavedShips: saved, LostShips: lost},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(profileShip)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, edited) {
		t.Fatalf("profile ship was overwritten: %q", got)
	}
	lostCopy, err := os.ReadFile(filepath.Join(game.ProfileDir(root), "Lost Ships", "wreck.png"))
	if err != nil {
		t.Fatal(err)
	}
	if string(lostCopy) != "lost" {
		t.Fatalf("lost ship = %q", lostCopy)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	dll, err := os.ReadFile(filepath.Join(bin, "Cosmoteer.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dll, original) {
		t.Fatalf("dll = %q", dll)
	}
	if _, err := os.Stat(filepath.Join(root, "Standard Mods", "Mine", "mod.rules")); err != nil {
		t.Fatal(err)
	}
}

func TestRootSaveArgument(t *testing.T) {
	root := t.TempDir()
	got, err := RootSaveArgument(game.Install{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	profile := game.ProfileDir(root)
	if runtime.GOOS == "windows" {
		if got != profile {
			t.Fatalf("rootsavepath = %s", got)
		}
		return
	}
	want := "Z:" + strings.ReplaceAll(profile, "/", `\`)
	if got != want {
		t.Fatalf("rootsavepath = %s, want %s", got, want)
	}
}
