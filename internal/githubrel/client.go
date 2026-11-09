package githubrel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Progress is one snapshot of a file download.
// BytesPerSec is the average since this transfer started.
type Progress struct {
	Name        string
	Received    int64
	Total       int64
	BytesPerSec float64
	Done        bool
}

// ProgressFunc receives download snapshots. It may be called from the download goroutine.
type ProgressFunc func(Progress)

const DefaultBaseURL = "https://api.github.com"

// Release is the subset of a GitHub release the launcher uses.
type Release struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	Assets  []Asset `json:"assets"`
}

// Asset is one file attached to a release.
type Asset struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Client talks to the GitHub releases API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (c *Client) base() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{
		// No client-wide deadline. A pack is several gigabytes, and the timeout
		// would cover the body read as well as the headers.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
			}
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}
}

// ListReleases returns the newest releases first.
func (c *Client) ListReleases(ctx context.Context, owner, repo string) ([]Release, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=30", c.base(), owner, repo)
	var releases []Release
	if err := c.getJSON(ctx, endpoint, &releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// LatestRelease returns the repository's latest release.
func (c *Client) LatestRelease(ctx context.Context, owner, repo string) (Release, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.base(), owner, repo)
	var release Release
	if err := c.getJSON(ctx, endpoint, &release); err != nil {
		return Release{}, err
	}
	return release, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "modpack-launcher")
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

// Download fetches an asset into dest, creating parent directories.
func (c *Client) Download(ctx context.Context, asset Asset, dest string) error {
	return c.DownloadProgress(ctx, asset, dest, nil)
}

// DownloadProgress fetches an asset and reports bytes as they are written.
func (c *Client) DownloadProgress(ctx context.Context, asset Asset, dest string, progress ProgressFunc) error {
	url := asset.BrowserDownloadURL
	if c.Token != "" && asset.URL != "" {
		url = asset.URL
	}
	if url == "" {
		return fmt.Errorf("asset %s has no download url", asset.Name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "modpack-launcher")
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("download %s: %s", asset.Name, strings.TrimSpace(string(body)))
	}
	total := asset.Size
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".partial"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	started := time.Now()
	emit(progress, Progress{Name: asset.Name, Total: total}, started)
	body := &countReader{r: resp.Body, name: asset.Name, total: total, started: started, progress: progress}
	_, copyErr := io.Copy(out, body)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dest)
}

type countReader struct {
	r        io.Reader
	name     string
	total    int64
	read     int64
	started  time.Time
	last     time.Time
	progress ProgressFunc
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.read += int64(n)
	}
	if n > 0 || err == io.EOF {
		c.emit(err == io.EOF)
	}
	return n, err
}

func (c *countReader) emit(done bool) {
	if c.progress == nil {
		return
	}
	now := time.Now()
	if !done && !c.last.IsZero() && now.Sub(c.last) < 200*time.Millisecond {
		return
	}
	c.last = now
	emit(c.progress, Progress{Name: c.name, Received: c.read, Total: c.total, Done: done}, c.started)
}

func emit(progress ProgressFunc, p Progress, started time.Time) {
	if progress == nil {
		return
	}
	elapsed := time.Since(started).Seconds()
	if elapsed > 0 && p.Received > 0 {
		p.BytesPerSec = float64(p.Received) / elapsed
	}
	progress(p)
}

func (c *Client) authorize(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

// FindAsset returns the asset with the given file name.
func FindAsset(release Release, name string) (Asset, bool) {
	for _, asset := range release.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return Asset{}, false
}
