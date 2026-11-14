package pack

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"modpack-launcher/internal/fsutil"
	"modpack-launcher/internal/githubrel"
)

const yamlModID = "radistmorse.modloader"

// Delta describes a zip that upgrades one cached pack version to this release.
type Delta struct {
	From    string `json:"from"`
	Archive string `json:"archive"`
}

// Manifest is the small release file that names a pack archive.
type Manifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Archive     string   `json:"archive"`
	Description string   `json:"description,omitempty"`
	Parts       []string `json:"parts,omitempty"`
	Files       string   `json:"files,omitempty"`
	Deltas      []Delta  `json:"deltas,omitempty"`
}

// fileIndex is the optional per-file hash list attached to a release.
type fileIndex struct {
	ID      string                `json:"id"`
	Version string                `json:"version"`
	Files   map[string]fileEntry  `json:"files"`
}

type fileEntry struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// deltaMeta is stored inside a delta zip as _delta.json.
type deltaMeta struct {
	From    string   `json:"from"`
	To      string   `json:"to"`
	Deleted []string `json:"deleted"`
}

// Mod is one top-level mod folder.
type Mod struct {
	Dir  string
	Name string
	ID   string
}

var idPattern = regexp.MustCompile(`(?m)^[ \t]*ID[ \t]*=[ \t]*"?([^"[:space:]]+)"?`)

// Published is a pack version listed in a GitHub release.
type Published struct {
	Manifest Manifest
	Release  githubrel.Release
	Archive  githubrel.Asset
}

// List reads manifest.json from each release that publishes one.
func List(ctx context.Context, client *githubrel.Client, owner, repo string) ([]Published, error) {
	releases, err := client.ListReleases(ctx, owner, repo)
	if err != nil {
		return nil, err
	}
	var packs []Published
	for _, release := range releases {
		asset, ok := githubrel.FindAsset(release, "manifest.json")
		if !ok {
			continue
		}
		manifest, err := readManifestAsset(ctx, client, asset)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", release.TagName, err)
		}
		if manifest.Version == "" {
			manifest.Version = strings.TrimPrefix(release.TagName, "v")
		}
		var archive githubrel.Asset
		if len(manifest.Parts) == 0 {
			var ok bool
			archive, ok = githubrel.FindAsset(release, manifest.Archive)
			if !ok {
				return nil, fmt.Errorf("%s: archive %q is not attached to the release", manifest.ID, manifest.Archive)
			}
		} else {
			for _, name := range manifest.Parts {
				if _, ok := githubrel.FindAsset(release, name); !ok {
					return nil, fmt.Errorf("%s: part %q is not attached to the release", manifest.ID, name)
				}
			}
		}
		packs = append(packs, Published{Manifest: manifest, Release: release, Archive: archive})
	}
	return packs, nil
}

func readManifestAsset(ctx context.Context, client *githubrel.Client, asset githubrel.Asset) (Manifest, error) {
	tmp, err := os.CreateTemp("", "pack-manifest-*.json")
	if err != nil {
		return Manifest{}, err
	}
	name := tmp.Name()
	tmp.Close()
	defer os.Remove(name)
	if err := client.Download(ctx, asset, name); err != nil {
		return Manifest{}, err
	}
	return ReadManifest(name)
}

// ReadManifest parses a manifest file.
func ReadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.ID == "" || manifest.Archive == "" {
		return Manifest{}, fmt.Errorf("manifest needs id and archive")
	}
	return manifest, nil
}

// Select picks a pack by id. version empty means the newest listed copy.
func Select(packs []Published, id, version string) (Published, error) {
	for _, item := range packs {
		if item.Manifest.ID != id {
			continue
		}
		if version == "" || item.Manifest.Version == version {
			return item, nil
		}
	}
	if version == "" {
		return Published{}, fmt.Errorf("pack %q was not found", id)
	}
	return Published{}, fmt.Errorf("pack %q version %q was not found", id, version)
}

// How EnsureCached filled the extract directory.
const (
	CacheHit   = "cached"
	CacheDelta = "delta"
	CacheFull  = "full"
)

// EnsureCached downloads and extracts a pack when that version is not already cached.
// progress may be nil. For a split archive it reports one transfer across every part.
// When the manifest lists deltas and a matching older version is already cached, only the
// delta zip is downloaded and applied. how is CacheHit, CacheDelta+":"+from, or CacheFull.
// Old launchers that ignore deltas still use the full archive.
func EnsureCached(ctx context.Context, client *githubrel.Client, cacheRoot string, item Published, progress githubrel.ProgressFunc) (dir string, how string, err error) {
	dest := filepath.Join(cacheRoot, item.Manifest.ID, item.Manifest.Version)
	stamp := filepath.Join(dest, ".extracted")
	extractDir := filepath.Join(dest, "files")
	if _, err := os.Stat(stamp); err == nil {
		return extractDir, CacheHit, nil
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", "", err
	}
	if applied, from, deltaPath, err := tryDeltaCache(ctx, client, cacheRoot, dest, extractDir, item, progress); err != nil {
		_ = os.RemoveAll(extractDir)
		return "", "", err
	} else if applied {
		if err := verifyDeltaResult(ctx, client, dest, extractDir, deltaPath, item); err != nil {
			_ = os.RemoveAll(extractDir)
			return "", "", err
		}
		if err := os.WriteFile(stamp, []byte(item.Manifest.Version+"\n"), 0o644); err != nil {
			return "", "", err
		}
		return extractDir, CacheDelta + ":" + from, nil
	}
	archivePath := filepath.Join(dest, item.Manifest.Archive)
	if len(item.Manifest.Parts) == 0 {
		if err := client.DownloadProgress(ctx, item.Archive, archivePath, progress); err != nil {
			return "", "", err
		}
	} else if err := downloadParts(ctx, client, dest, item, archivePath, progress); err != nil {
		return "", "", err
	}
	if err := os.RemoveAll(extractDir); err != nil {
		return "", "", err
	}
	if err := Extract(archivePath, extractDir); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(stamp, []byte(item.Manifest.Version+"\n"), 0o644); err != nil {
		return "", "", err
	}
	return extractDir, CacheFull, nil
}

func tryDeltaCache(ctx context.Context, client *githubrel.Client, cacheRoot, dest, extractDir string, item Published, progress githubrel.ProgressFunc) (bool, string, string, error) {
	delta, baseDir, ok := findDeltaBase(cacheRoot, item)
	if !ok {
		return false, "", "", nil
	}
	asset, ok := githubrel.FindAsset(item.Release, delta.Archive)
	if !ok {
		return false, "", "", nil
	}
	if err := os.RemoveAll(extractDir); err != nil {
		return false, "", "", err
	}
	if err := cloneTree(baseDir, extractDir); err != nil {
		return false, "", "", fmt.Errorf("clone %s: %w", delta.From, err)
	}
	deltaPath := filepath.Join(dest, delta.Archive)
	if err := client.DownloadProgress(ctx, asset, deltaPath, progress); err != nil {
		return false, "", "", err
	}
	if err := ApplyDelta(deltaPath, extractDir); err != nil {
		return false, "", "", err
	}
	return true, delta.From, deltaPath, nil
}

func findDeltaBase(cacheRoot string, item Published) (Delta, string, bool) {
	for _, delta := range item.Manifest.Deltas {
		if delta.From == "" || delta.Archive == "" {
			continue
		}
		base := filepath.Join(cacheRoot, item.Manifest.ID, delta.From)
		if _, err := os.Stat(filepath.Join(base, ".extracted")); err != nil {
			continue
		}
		files := filepath.Join(base, "files")
		if _, err := os.Stat(files); err != nil {
			continue
		}
		return delta, files, true
	}
	return Delta{}, "", false
}

// ApplyDelta overlays a delta zip onto an existing extracted pack directory.
func ApplyDelta(deltaPath, extractDir string) error {
	reader, err := zip.OpenReader(deltaPath)
	if err != nil {
		return err
	}
	defer reader.Close()
	var meta deltaMeta
	for _, file := range reader.File {
		if file.Name != "_delta.json" {
			continue
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(in)
		in.Close()
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &meta); err != nil {
			return fmt.Errorf("delta meta: %w", err)
		}
		break
	}
	for _, rel := range meta.Deleted {
		target, err := safeJoin(extractDir, rel)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(target); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for _, file := range reader.File {
		if file.Name == "_delta.json" || strings.HasSuffix(file.Name, "/") {
			continue
		}
		target, err := safeJoin(extractDir, file.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		err = writeReplace(target, in, file.Mode())
		in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func cloneTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.Link(path, target); err == nil {
			return nil
		}
		return fsutil.CopyFile(path, target)
	})
}

func writeReplace(path string, r io.Reader, mode os.FileMode) error {
	_ = os.Remove(path)
	return writeNew(path, r, mode)
}

func verifyDeltaResult(ctx context.Context, client *githubrel.Client, dest, extractDir, deltaPath string, item Published) error {
	if item.Manifest.Files == "" {
		return nil
	}
	asset, ok := githubrel.FindAsset(item.Release, item.Manifest.Files)
	if !ok {
		return fmt.Errorf("files index %q is not attached to the release", item.Manifest.Files)
	}
	indexPath := filepath.Join(dest, item.Manifest.Files)
	if err := client.Download(ctx, asset, indexPath); err != nil {
		return err
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}
	var index fileIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("files index: %w", err)
	}
	reader, err := zip.OpenReader(deltaPath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		if file.Name == "_delta.json" || strings.HasSuffix(file.Name, "/") {
			continue
		}
		entry, ok := index.Files[file.Name]
		if !ok {
			return fmt.Errorf("delta file %s missing from files index", file.Name)
		}
		target, err := safeJoin(extractDir, file.Name)
		if err != nil {
			return err
		}
		info, err := os.Stat(target)
		if err != nil {
			return fmt.Errorf("missing %s: %w", file.Name, err)
		}
		if entry.Size > 0 && info.Size() != entry.Size {
			return fmt.Errorf("%s: size %d, want %d", file.Name, info.Size(), entry.Size)
		}
		sum, err := fileSHA256(target)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, entry.SHA256) {
			return fmt.Errorf("%s: sha256 mismatch", file.Name)
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func downloadParts(ctx context.Context, client *githubrel.Client, dest string, item Published, archivePath string, progress githubrel.ProgressFunc) error {
	var planned int64
	sized := true
	for _, name := range item.Manifest.Parts {
		asset, ok := githubrel.FindAsset(item.Release, name)
		if !ok {
			return fmt.Errorf("part %q is not attached to the release", name)
		}
		if asset.Size <= 0 {
			sized = false
		}
		planned += asset.Size
	}
	if !sized {
		planned = 0
	}
	var base int64
	started := time.Now()
	var paths []string
	for i, name := range item.Manifest.Parts {
		asset, ok := githubrel.FindAsset(item.Release, name)
		if !ok {
			return fmt.Errorf("part %q is not attached to the release", name)
		}
		partPath := filepath.Join(dest, name)
		last := i == len(item.Manifest.Parts)-1
		err := client.DownloadProgress(ctx, asset, partPath, func(p githubrel.Progress) {
			if progress == nil {
				return
			}
			got := base + p.Received
			total := planned
			done := last && p.Done
			label := fmt.Sprintf("%s (%d/%d)", name, i+1, len(item.Manifest.Parts))
			if done {
				label = item.Manifest.Archive
				if total == 0 {
					total = got
				}
			}
			elapsed := time.Since(started).Seconds()
			var rate float64
			if elapsed > 0 && got > 0 {
				rate = float64(got) / elapsed
			}
			progress(githubrel.Progress{
				Name:        label,
				Received:    got,
				Total:       total,
				BytesPerSec: rate,
				Done:        done,
			})
		})
		if err != nil {
			return err
		}
		info, err := os.Stat(partPath)
		if err != nil {
			return err
		}
		base += info.Size()
		paths = append(paths, partPath)
	}
	return Join(archivePath, paths)
}

// Join concatenates parts into one archive, in order.
func Join(dest string, parts []string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	for _, part := range parts {
		in, err := os.Open(part)
		if err != nil {
			out.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		in.Close()
		if copyErr != nil {
			out.Close()
			os.Remove(dest)
			return copyErr
		}
	}
	return out.Close()
}

// Extract unpacks a .zip, .tar.gz, or .tgz archive. Paths that escape dest are rejected.
func Extract(archivePath, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(archivePath, dest)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(archivePath, dest)
	default:
		return fmt.Errorf("unsupported archive %s", filepath.Base(archivePath))
	}
}

func extractZip(archivePath, dest string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		target, err := safeJoin(dest, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		if err := writeNew(target, in, file.Mode()); err != nil {
			in.Close()
			return err
		}
		in.Close()
	}
	return nil
}

func extractTarGz(archivePath, dest string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeNew(target, tr, os.FileMode(header.Mode)); err != nil {
				return err
			}
		}
	}
}

func writeNew(path string, r io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, r)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(path)
		return copyErr
	}
	return closeErr
}

func safeJoin(dest, name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("archive path escapes destination: %s", name)
	}
	return filepath.Join(dest, clean), nil
}

// ContentRoot finds the directory whose children are mod folders.
// An archive that wraps the pack in one extra directory is accepted.
func ContentRoot(dir string) (string, error) {
	mods, err := ModsIn(dir)
	if err != nil {
		return "", err
	}
	if len(mods) > 0 {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	if len(dirs) == 1 {
		sub := filepath.Join(dir, dirs[0])
		mods, err = ModsIn(sub)
		if err != nil {
			return "", err
		}
		if len(mods) > 0 {
			return sub, nil
		}
	}
	return "", fmt.Errorf("no mod.rules folders in %s", dir)
}

// ModsIn lists top-level directories that contain mod.rules.
func ModsIn(dir string) ([]Mod, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var mods []Mod
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		modDir := filepath.Join(dir, entry.Name())
		rules := filepath.Join(modDir, "mod.rules")
		if _, err := os.Stat(rules); err != nil {
			continue
		}
		id, err := ModID(rules)
		if err != nil {
			return nil, err
		}
		mods = append(mods, Mod{Dir: modDir, Name: entry.Name(), ID: id})
	}
	return mods, nil
}

// ModID reads the ID field from a mod.rules file.
func ModID(rulesPath string) (string, error) {
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		return "", err
	}
	match := idPattern.FindSubmatch(data)
	if match == nil {
		return "", nil
	}
	return string(match[1]), nil
}

// FindYAML returns the Yet Another Mod Loader directory in a pack.
func FindYAML(mods []Mod) (Mod, error) {
	for _, mod := range mods {
		if mod.ID == yamlModID {
			return mod, nil
		}
	}
	return Mod{}, fmt.Errorf("pack has no Yet Another Mod Loader (%s)", yamlModID)
}

// InstallMods copies mod folders into Standard Mods.
// Emmanim's Loader directory is omitted from every mod except Yet Another Mod Loader.
func InstallMods(mods []Mod, standardMods string) error {
	if err := os.MkdirAll(standardMods, 0o755); err != nil {
		return err
	}
	for _, mod := range mods {
		var skip func(string) bool
		if mod.ID != yamlModID {
			skip = fsutil.SkipLoader
		}
		if err := fsutil.CopyTree(mod.Dir, filepath.Join(standardMods, mod.Name), skip); err != nil {
			return fmt.Errorf("copy %s: %w", mod.Name, err)
		}
	}
	return nil
}
