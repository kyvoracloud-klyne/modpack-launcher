package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modpack-launcher/internal/config"
	"modpack-launcher/internal/githubrel"
	"modpack-launcher/internal/pack"
)

type fakeRunner struct {
	launched []string
	updated  bool
	release  chan struct{}
}

func (f *fakeRunner) List(context.Context, config.Config) ([]pack.Published, error) {
	return []pack.Published{{
		Manifest: pack.Manifest{ID: "demo", Name: "Demo Pack", Version: "1.2.0", Archive: "demo.zip", Description: "A demo pack."},
	}}, nil
}

func (f *fakeRunner) Launch(_ config.Config, id, version string, log func(string), progress githubrel.ProgressFunc) error {
	f.launched = append(f.launched, id+"@"+version)
	if progress != nil {
		progress(githubrel.Progress{Name: "demo.zip", Received: 50, Total: 100, BytesPerSec: 1500000})
		progress(githubrel.Progress{Name: "demo.zip", Received: 100, Total: 100, BytesPerSec: 2000000, Done: true})
	}
	log("started " + id)
	if f.release != nil {
		<-f.release
	}
	return nil
}

func (f *fakeRunner) Restore() error { return nil }

func (f *fakeRunner) Update(config.Config, string, func(string), githubrel.ProgressFunc) (bool, error) {
	f.updated = true
	return false, nil
}

func (f *fakeRunner) Recover(func(string)) error { return nil }

func TestWindowListsAndPlays(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	runner := &fakeRunner{}
	server := New("9.9.9", path, config.Config{PackRepo: "acme/packs"}, runner)
	server.RefreshPacks()
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	page, err := http.Get(httpServer.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(body), "Play") || !strings.Contains(string(body), "Uninstall") || !strings.Contains(string(body), "PACKS") || !strings.Contains(string(body), "progressbar") {
		t.Fatal("window is missing the pack controls")
	}

	stateRes, err := http.Get(httpServer.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Version string `json:"version"`
		Packs   []PackInfo
	}
	if err := json.NewDecoder(stateRes.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	stateRes.Body.Close()
	if state.Version != "9.9.9" || len(state.Packs) != 1 || state.Packs[0].Name != "Demo Pack" {
		t.Fatalf("state = %+v", state)
	}

	launchRes, err := http.Post(httpServer.URL+"/api/launch", "application/json", strings.NewReader(`{"id":"demo","version":"1.2.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	launchRes.Body.Close()
	if launchRes.StatusCode != http.StatusAccepted {
		t.Fatalf("launch status %d", launchRes.StatusCode)
	}
	waitUntil(t, func() bool { return len(runner.launched) == 1 })
	if runner.launched[0] != "demo@1.2.0" {
		t.Fatalf("launched %#v", runner.launched)
	}

	saveRes, err := http.Post(httpServer.URL+"/api/config", "application/json", strings.NewReader(`{"packRepo":"acme/other","gameRoot":"/games/Cosmoteer"}`))
	if err != nil {
		t.Fatal(err)
	}
	saveRes.Body.Close()
	if saveRes.StatusCode != http.StatusOK {
		t.Fatalf("save status %d", saveRes.StatusCode)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.PackRepo != "acme/other" || saved.GameRoot != "/games/Cosmoteer" {
		t.Fatalf("saved = %+v", saved)
	}

	bad, err := http.Post(httpServer.URL+"/api/config", "application/json", strings.NewReader(`{"packRepo":"not a repo"}`))
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad repo status %d", bad.StatusCode)
	}
	_ = os.Remove(filepath.Join(dir, "config.json.partial"))
}

func TestLaunchShowsDownloadProgress(t *testing.T) {
	dir := t.TempDir()
	release := make(chan struct{})
	runner := &fakeRunner{release: release}
	server := New("9.9.9", filepath.Join(dir, "config.json"), config.Config{PackRepo: "acme/packs"}, runner)
	server.RefreshPacks()
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	defer close(release)

	launchRes, err := http.Post(httpServer.URL+"/api/launch", "application/json", strings.NewReader(`{"id":"demo","version":"1.2.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	launchRes.Body.Close()
	if launchRes.StatusCode != http.StatusAccepted {
		t.Fatalf("launch status %d", launchRes.StatusCode)
	}

	waitUntil(t, func() bool {
		stateRes, err := http.Get(httpServer.URL + "/api/state")
		if err != nil {
			return false
		}
		defer stateRes.Body.Close()
		var state struct {
			Download *DownloadStatus `json:"download"`
			Logs     []string        `json:"logs"`
		}
		if err := json.NewDecoder(stateRes.Body).Decode(&state); err != nil {
			return false
		}
		if state.Download == nil || !state.Download.Done || state.Download.BytesPerSec != 2000000 {
			return false
		}
		if state.Download.Received != 100 || state.Download.Total != 100 {
			t.Fatalf("progress = %+v", state.Download)
		}
		return strings.Join(state.Logs, "\n") == "Play demo\nDownloaded demo.zip  100%  2.0 MB/s\nstarted demo" ||
			strings.Contains(strings.Join(state.Logs, "\n"), "Downloaded demo.zip  100%  2.0 MB/s")
	})
}

func waitUntil(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}
