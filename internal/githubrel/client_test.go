package githubrel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestListAndDownload(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/repos/acme/packs/releases":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"tag_name":"v2","assets":[` +
				`{"name":"manifest.json","browser_download_url":"` + server.URL + `/manifest.json"},` +
				`{"name":"pack.zip","browser_download_url":"` + server.URL + `/pack.zip","url":"` + server.URL + `/api-pack"}` +
				`]}]`))
		case "/manifest.json":
			_, _ = w.Write([]byte(`{"id":"modpack-2026","version":"2","archive":"pack.zip"}`))
		case "/api-pack":
			_, _ = w.Write([]byte("archive-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL, Token: "secret"}
	releases, err := client.ListReleases(context.Background(), "acme", "packs")
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].TagName != "v2" {
		t.Fatalf("releases = %+v", releases)
	}
	asset, ok := FindAsset(releases[0], "pack.zip")
	if !ok {
		t.Fatal("missing archive asset")
	}
	dest := filepath.Join(t.TempDir(), "pack.zip")
	if err := client.Download(context.Background(), asset, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "archive-bytes" {
		t.Fatalf("downloaded %q", data)
	}
}

func TestDownloadProgress(t *testing.T) {
	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte(i)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	var snaps []Progress
	client := &Client{HTTP: server.Client()}
	asset := Asset{Name: "pack.zip", BrowserDownloadURL: server.URL, Size: int64(len(payload))}
	dest := filepath.Join(t.TempDir(), "pack.zip")
	if err := client.DownloadProgress(context.Background(), asset, dest, func(p Progress) {
		snaps = append(snaps, p)
	}); err != nil {
		t.Fatal(err)
	}
	if len(snaps) == 0 {
		t.Fatal("no progress")
	}
	last := snaps[len(snaps)-1]
	if !last.Done || last.Received != int64(len(payload)) || last.Total != int64(len(payload)) || last.BytesPerSec <= 0 {
		t.Fatalf("last progress = %+v", last)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("wrote %d bytes", len(got))
	}
}

func TestFormatProgress(t *testing.T) {
	got := FormatProgress(Progress{Name: "pack.zip", Received: 25, Total: 100, BytesPerSec: 1500000})
	if got != "pack.zip  25.0%  1.5 MB/s" {
		t.Fatalf("progress = %q", got)
	}
	done := FormatProgress(Progress{Name: "pack.zip", Received: 100, Total: 100, BytesPerSec: 2000000, Done: true})
	if done != "pack.zip  100%  2.0 MB/s" {
		t.Fatalf("done = %q", done)
	}
}
