package ships

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyMissingKeepsDestinationBytes(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	payload := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff, 0x0d, 0x0a}
	if err := os.WriteFile(filepath.Join(src, "kept.png"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "new.png"), []byte("new-ship"), 0o644); err != nil {
		t.Fatal(err)
	}
	existing := []byte("already-there")
	if err := os.WriteFile(filepath.Join(dst, "kept.png"), existing, 0o644); err != nil {
		t.Fatal(err)
	}

	copied, err := CopyMissing(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if copied != 1 {
		t.Fatalf("copied %d files", copied)
	}
	got, err := os.ReadFile(filepath.Join(dst, "kept.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, existing) {
		t.Fatalf("destination was overwritten: %q", got)
	}
	ship, err := os.ReadFile(filepath.Join(dst, "nested", "new.png"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ship) != "new-ship" {
		t.Fatalf("new ship = %q", ship)
	}
}

func TestFindProfileLibrariesPicksNewest(t *testing.T) {
	root := t.TempDir()
	older := filepath.Join(root, "drive_c", "users", "steamuser", "Saved Games", "Cosmoteer", "111")
	newer := filepath.Join(root, "drive_c", "users", "steamuser", "Saved Games", "Cosmoteer", "222")
	for _, dir := range []string{older, newer} {
		if err := os.MkdirAll(filepath.Join(dir, "Saved Ships"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "Lost Ships"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "Saved Games"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Saved Games", "career.sav"), []byte("career"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldFile := filepath.Join(older, "Saved Ships", "old.png")
	newFile := filepath.Join(newer, "Saved Ships", "new.png")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldFile, past, past); err != nil {
		t.Fatal(err)
	}

	got, err := FindProfileLibraries([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got.SavedShips != filepath.Join(newer, "Saved Ships") {
		t.Fatalf("saved = %s", got.SavedShips)
	}

	profile := t.TempDir()
	if _, err := CopyLibraries(got, profile); err != nil {
		t.Fatal(err)
	}
	save, err := os.ReadFile(filepath.Join(profile, "Saved Games", "career.sav"))
	if err != nil {
		t.Fatal(err)
	}
	if string(save) != "career" {
		t.Fatalf("save = %q", save)
	}
	if _, err := os.Stat(filepath.Join(profile, "settings.rules")); !os.IsNotExist(err) {
		t.Fatal("settings were copied")
	}
	if _, err := os.Stat(filepath.Join(profile, "Saved Ships", "new.png")); err != nil {
		t.Fatal(err)
	}
}

func TestCopyLibrariesKeepsExistingSave(t *testing.T) {
	src := t.TempDir()
	games := filepath.Join(src, "Saved Games")
	if err := os.MkdirAll(filepath.Join(games, "Auto Saves"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(games, "Career.savedgame"), []byte("from-steam"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(games, "Auto Saves", "Auto-Save.savedgame"), []byte("auto"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(games, "steam_autocloud.vdf"), []byte("cloud"), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	existing := []byte("already-in-mod-profile")
	if err := os.MkdirAll(filepath.Join(profile, "Saved Games"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "Saved Games", "Career.savedgame"), existing, 0o644); err != nil {
		t.Fatal(err)
	}

	copied, err := CopyLibraries(Libraries{SavedGames: games}, profile)
	if err != nil {
		t.Fatal(err)
	}
	if copied != 1 {
		t.Fatalf("copied %d files", copied)
	}
	got, err := os.ReadFile(filepath.Join(profile, "Saved Games", "Career.savedgame"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, existing) {
		t.Fatalf("existing save was overwritten: %q", got)
	}
	auto, err := os.ReadFile(filepath.Join(profile, "Saved Games", "Auto Saves", "Auto-Save.savedgame"))
	if err != nil {
		t.Fatal(err)
	}
	if string(auto) != "auto" {
		t.Fatalf("auto save = %q", auto)
	}
	if _, err := os.Stat(filepath.Join(profile, "Saved Games", "steam_autocloud.vdf")); !os.IsNotExist(err) {
		t.Fatal("steam cloud file was copied")
	}
}
