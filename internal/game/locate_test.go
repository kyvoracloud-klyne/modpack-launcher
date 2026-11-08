package game

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExplicitBin(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "Bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "Cosmoteer.exe"), []byte("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "Cosmoteer.dll"), []byte("dll"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(filepath.Join(bin, "Cosmoteer.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Bin != bin || got.Root != root {
		t.Fatalf("install = %+v", got)
	}
	if ProfileDir(root) != filepath.Join(root, "ModPackProfile") {
		t.Fatal(ProfileDir(root))
	}
}
