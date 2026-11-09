package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modpack-launcher/internal/game"
)

func TestModReferenceUsesProtonLibraryDrive(t *testing.T) {
	base := t.TempDir()
	steamapps := filepath.Join(base, "steamapps")
	root := filepath.Join(steamapps, "common", "Cosmoteer")
	prefix := filepath.Join(base, "pfx")
	devices := filepath.Join(prefix, "dosdevices")
	if err := os.MkdirAll(devices, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(steamapps, filepath.Join(devices, "s:")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/", filepath.Join(devices, "z:")); err != nil {
		t.Fatal(err)
	}
	got, err := ModReference(game.Install{Root: root, Prefix: prefix}, "Demo Mod")
	if err != nil {
		t.Fatal(err)
	}
	want := "S:/common/Cosmoteer/Standard Mods/Demo Mod"
	if got != want {
		t.Fatalf("reference = %s, want %s", got, want)
	}
}

func TestWriteModSettingsPreservesProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.rules")
	original := strings.Join([]string{
		"GameSettings",
		"{",
		"\tLastGameVersion = 0.30.5a",
		"\tEnabledMods[]",
		"\tAutoDisableMods = true",
		"\tFeedbackEmail",
		"\tTrustedLibraries",
		"\t[",
		"\t\t{",
		"\t\t\tKey = \"keep\"",
		"\t\t}",
		"\t]",
		"}",
		"",
	}, "\r\n")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	mods := []string{`S:/common/Cosmoteer/Standard Mods/Demo Mod`}
	if err := WriteModSettings(path, mods); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Contains(text, "\n") && !strings.Contains(text, "\r\n") {
		t.Fatal("newline style changed")
	}
	for _, want := range []string{
		"LastGameVersion = 0.30.5a",
		"FeedbackEmail",
		`Key = "keep"`,
		`"S:/common/Cosmoteer/Standard Mods/Demo Mod"`,
		"AutoDisableMods = false",
		"TrustedMods",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "EnabledMods[]") || strings.Contains(text, "AutoDisableMods = true") {
		t.Fatalf("old mod list survived:\n%s", text)
	}
	if strings.Count(text, "TrustedMods") != 1 {
		t.Fatalf("TrustedMods count:\n%s", text)
	}

	again := []string{`S:/common/Cosmoteer/Standard Mods/Other`}
	if err := WriteModSettings(path, again); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text = string(got)
	if strings.Contains(text, "Demo Mod") || !strings.Contains(text, "Other") {
		t.Fatalf("list was not replaced:\n%s", text)
	}
	if strings.Count(text, "EnabledMods") != 1 || strings.Count(text, "TrustedMods") != 1 {
		t.Fatalf("duplicated lists:\n%s", text)
	}
}

func TestModReferenceFollowsSteamLibrarySymlink(t *testing.T) {
	base := t.TempDir()
	realSteamapps := filepath.Join(base, "real", "steamapps")
	root := filepath.Join(realSteamapps, "common", "Cosmoteer")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	linkParent := filepath.Join(base, "home", ".steam")
	if err := os.MkdirAll(linkParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(linkParent, "steam")); err != nil {
		t.Fatal(err)
	}
	gameRoot := filepath.Join(linkParent, "steam", "steamapps", "common", "Cosmoteer")
	prefix := filepath.Join(base, "pfx")
	devices := filepath.Join(prefix, "dosdevices")
	if err := os.MkdirAll(devices, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realSteamapps, filepath.Join(devices, "s:")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/", filepath.Join(devices, "z:")); err != nil {
		t.Fatal(err)
	}
	got, err := ModReference(game.Install{Root: gameRoot, Prefix: prefix}, "Demo Mod")
	if err != nil {
		t.Fatal(err)
	}
	want := "S:/common/Cosmoteer/Standard Mods/Demo Mod"
	if got != want {
		t.Fatalf("reference = %s, want %s", got, want)
	}
}

func TestModReferenceUsesSavedSteamPath(t *testing.T) {
	base := t.TempDir()
	settings := filepath.Join(base, "pfx", "drive_c", "users", "steamuser", "Saved Games", "Cosmoteer", "1", "settings.rules")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "EnabledMods\n[\n\t\"S:/common/Cosmoteer/Standard Mods/Old Mod\"\n]\n"
	if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	got, err := ModReference(game.Install{Root: root, Prefix: filepath.Join(base, "pfx")}, "huge_crews")
	if err != nil {
		t.Fatal(err)
	}
	want := "S:/common/Cosmoteer/Standard Mods/huge_crews"
	if got != want {
		t.Fatalf("reference = %s, want %s", got, want)
	}
}

func TestWriteModSettingsCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.rules")
	if err := WriteModSettings(path, []string{"C:/Cosmoteer/Standard Mods/A"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "EnabledMods") || !strings.Contains(text, "TrustedMods") || !strings.Contains(text, "AutoDisableMods = false") {
		t.Fatalf("new settings:\n%s", text)
	}
}
