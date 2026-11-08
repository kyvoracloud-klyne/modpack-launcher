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
