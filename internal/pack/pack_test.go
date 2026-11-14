package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"modpack-launcher/internal/githubrel"
)

func TestExtractAndMods(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "pack.zip")
	writeZip(t, archive, map[string]string{
		"ModPack/Example/mod.rules":                                   "ID = example.mod\n",
		"ModPack/Example/Loader/secret":                               "nope",
		"ModPack/Yet Another Mod Loader - 3577650065/mod.rules":       "ID = radistmorse.modloader\n",
		"ModPack/Yet Another Mod Loader - 3577650065/Loader/keep.txt": "yes",
	})
	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(archive, dest); err != nil {
		t.Fatal(err)
	}
	root, err := ContentRoot(dest)
	if err != nil {
		t.Fatal(err)
	}
	mods, err := ModsIn(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 2 {
		t.Fatalf("mods = %+v", mods)
	}
	standard := filepath.Join(t.TempDir(), "Standard Mods")
	if err := InstallMods(mods, standard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(standard, "Example", "Loader", "secret")); !os.IsNotExist(err) {
		t.Fatalf("Loader was copied: %v", err)
	}
	keep, err := os.ReadFile(filepath.Join(standard, "Yet Another Mod Loader - 3577650065", "Loader", "keep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(keep) != "yes" {
		t.Fatalf("yaml loader = %q", keep)
	}
}

func TestRejectsZipSlip(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "bad.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	writer, err := zw.Create("../evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("bad")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	file.Close()
	err = Extract(archive, t.TempDir())
	if err == nil {
		t.Fatal("expected zip slip to be rejected")
	}
}

func TestListSelectAndCache(t *testing.T) {
	archiveBytes := zipBytes(t, map[string]string{
		"Demo/mod.rules": "ID = demo.mod\n",
	})
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/packs/releases":
			_, _ = w.Write([]byte(`[{"tag_name":"v1.2.0","assets":[` +
				`{"name":"manifest.json","browser_download_url":"` + server.URL + `/manifest.json"},` +
				`{"name":"demo.zip","browser_download_url":"` + server.URL + `/demo.zip"}` +
				`]}]`))
		case "/manifest.json":
			_, _ = w.Write([]byte(`{"id":"demo","name":"Demo","version":"1.2.0","archive":"demo.zip"}`))
		case "/demo.zip":
			_, _ = w.Write(archiveBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &githubrel.Client{BaseURL: server.URL}
	listed, err := List(context.Background(), client, "acme", "packs")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := Select(listed, "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Manifest.Version != "1.2.0" || selected.Archive.Name != "demo.zip" {
		t.Fatalf("selected = %+v", selected.Manifest)
	}
	cache := t.TempDir()
	dir, how, err := EnsureCached(context.Background(), client, cache, selected, nil)
	if err != nil {
		t.Fatal(err)
	}
	if how != CacheFull {
		t.Fatalf("how = %q, want full", how)
	}
	if _, err := os.Stat(filepath.Join(dir, "Demo", "mod.rules")); err != nil {
		t.Fatal(err)
	}
	again, _, err := EnsureCached(context.Background(), client, cache, selected, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again != dir {
		t.Fatalf("cache path changed: %s vs %s", again, dir)
	}
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.WriteFile(path, zipBytes(t, files), 0o644); err != nil {
		t.Fatal(err)
	}
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestJoinPartsThenExtract(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "pack.zip")
	writeZip(t, archive, map[string]string{
		"Demo/mod.rules": "ID = demo.mod\n",
	})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	mid := len(data) / 2
	dir := t.TempDir()
	partA := filepath.Join(dir, "pack.zip.001")
	partB := filepath.Join(dir, "pack.zip.002")
	if err := os.WriteFile(partA, data[:mid], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partB, data[mid:], 0o644); err != nil {
		t.Fatal(err)
	}
	joined := filepath.Join(dir, "pack.zip")
	if err := Join(joined, []string{partA, partB}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "files")
	if err := Extract(joined, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "Demo", "mod.rules")); err != nil {
		t.Fatal(err)
	}
}

func TestSplitDownloadProgress(t *testing.T) {
	archive := zipBytes(t, map[string]string{
		"Demo/mod.rules": "ID = demo.mod\n",
	})
	mid := len(archive) / 2
	partA := archive[:mid]
	partB := archive[mid:]
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/packs/releases":
			_, _ = w.Write([]byte(`[{"tag_name":"v1","assets":[` +
				`{"name":"manifest.json","browser_download_url":"` + server.URL + `/manifest.json"},` +
				`{"name":"demo.zip.000","size":` + itoa(len(partA)) + `,"browser_download_url":"` + server.URL + `/a"},` +
				`{"name":"demo.zip.001","size":` + itoa(len(partB)) + `,"browser_download_url":"` + server.URL + `/b"}` +
				`]}]`))
		case "/manifest.json":
			_, _ = w.Write([]byte(`{"id":"demo","version":"1","archive":"demo.zip","parts":["demo.zip.000","demo.zip.001"]}`))
		case "/a":
			_, _ = w.Write(partA)
		case "/b":
			_, _ = w.Write(partB)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &githubrel.Client{BaseURL: server.URL}
	listed, err := List(context.Background(), client, "acme", "packs")
	if err != nil {
		t.Fatal(err)
	}
	var snaps []githubrel.Progress
	var previous int64
	_, _, err = EnsureCached(context.Background(), client, t.TempDir(), listed[0], func(p githubrel.Progress) {
		if p.Received < previous {
			t.Fatalf("progress went backwards: %+v after %d", p, previous)
		}
		previous = p.Received
		if p.Total != int64(len(archive)) && !(p.Done && p.Total == int64(len(archive))) {
			t.Fatalf("total = %d, want %d (%+v)", p.Total, len(archive), p)
		}
		snaps = append(snaps, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) == 0 || !snaps[len(snaps)-1].Done {
		t.Fatalf("snaps = %+v", snaps)
	}
	last := snaps[len(snaps)-1]
	if last.Name != "demo.zip" || last.Received != int64(len(archive)) || last.BytesPerSec <= 0 {
		t.Fatalf("last = %+v", last)
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

func TestApplyDeltaAndEnsureCached(t *testing.T) {
	v1 := zipBytes(t, map[string]string{
		"Demo/mod.rules": "ID = demo.mod\n",
		"Demo/old.txt":   "old",
		"Demo/keep.txt":  "keep",
	})
	delta := zipBytes(t, map[string]string{
		"_delta.json":   `{"from":"1.0.0","to":"1.0.1","deleted":["Demo/old.txt"]}`,
		"Demo/mod.rules": "ID = demo.mod\nName = New\n",
		"Demo/new.txt":   "new",
	})
	filesJSON := `{"id":"demo","version":"1.0.1","files":{` +
		`"Demo/mod.rules":{"sha256":"` + shaOf("ID = demo.mod\nName = New\n") + `","size":` + itoa(len("ID = demo.mod\nName = New\n")) + `},` +
		`"Demo/keep.txt":{"sha256":"` + shaOf("keep") + `","size":4},` +
		`"Demo/new.txt":{"sha256":"` + shaOf("new") + `","size":3}` +
		`}}`

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/packs/releases":
			_, _ = w.Write([]byte(`[
			{"tag_name":"v1.0.1","assets":[
				{"name":"manifest.json","browser_download_url":"` + server.URL + `/manifest-1.0.1.json"},
				{"name":"demo-1.0.1.zip","browser_download_url":"` + server.URL + `/full-1.0.1.zip"},
				{"name":"delta-1.0.0-to-1.0.1.zip","browser_download_url":"` + server.URL + `/delta.zip"},
				{"name":"files-1.0.1.json","browser_download_url":"` + server.URL + `/files.json"}
			]},
			{"tag_name":"v1.0.0","assets":[
				{"name":"manifest.json","browser_download_url":"` + server.URL + `/manifest-1.0.0.json"},
				{"name":"demo-1.0.0.zip","browser_download_url":"` + server.URL + `/full-1.0.0.zip"}
			]}]`))
		case "/manifest-1.0.0.json":
			_, _ = w.Write([]byte(`{"id":"demo","version":"1.0.0","archive":"demo-1.0.0.zip"}`))
		case "/manifest-1.0.1.json":
			_, _ = w.Write([]byte(`{"id":"demo","version":"1.0.1","archive":"demo-1.0.1.zip","files":"files-1.0.1.json","deltas":[{"from":"1.0.0","archive":"delta-1.0.0-to-1.0.1.zip"}]}`))
		case "/full-1.0.0.zip":
			_, _ = w.Write(v1)
		case "/full-1.0.1.zip":
			t.Fatal("full 1.0.1 download should not happen when delta applies")
		case "/delta.zip":
			_, _ = w.Write(delta)
		case "/files.json":
			_, _ = w.Write([]byte(filesJSON))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &githubrel.Client{BaseURL: server.URL}
	listed, err := List(context.Background(), client, "acme", "packs")
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	v1pack, err := Select(listed, "demo", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureCached(context.Background(), client, cache, v1pack, nil); err != nil {
		t.Fatal(err)
	}
	v2pack, err := Select(listed, "demo", "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	dir, how, err := EnsureCached(context.Background(), client, cache, v2pack, nil)
	if err != nil {
		t.Fatal(err)
	}
	if how != CacheDelta+":1.0.0" {
		t.Fatalf("how = %q", how)
	}
	body, err := os.ReadFile(filepath.Join(dir, "Demo", "mod.rules"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ID = demo.mod\nName = New\n" {
		t.Fatalf("mod.rules = %q", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "Demo", "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old.txt should be deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Demo", "new.txt")); err != nil {
		t.Fatal(err)
	}
	keep, err := os.ReadFile(filepath.Join(dir, "Demo", "keep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(keep) != "keep" {
		t.Fatalf("keep = %q", keep)
	}
}

func shaOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
