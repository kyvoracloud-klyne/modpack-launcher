package pack

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"modpack-launcher/internal/fsutil"
	"modpack-launcher/internal/githubrel"
)

const yamlModID = "radistmorse.modloader"

// Manifest is the small release file that names a pack archive.
type Manifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Archive     string   `json:"archive"`
	Description string   `json:"description,omitempty"`
	Parts       []string `json:"parts,omitempty"`
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

// EnsureCached downloads and extracts a pack when that version is not already cached.
func EnsureCached(ctx context.Context, client *githubrel.Client, cacheRoot string, item Published) (string, error) {
	dest := filepath.Join(cacheRoot, item.Manifest.ID, item.Manifest.Version)
	stamp := filepath.Join(dest, ".extracted")
	extractDir := filepath.Join(dest, "files")
	if _, err := os.Stat(stamp); err == nil {
		return extractDir, nil
	}
	archivePath := filepath.Join(dest, item.Manifest.Archive)
	if len(item.Manifest.Parts) == 0 {
		if err := client.Download(ctx, item.Archive, archivePath); err != nil {
			return "", err
		}
	} else if err := downloadParts(ctx, client, dest, item, archivePath); err != nil {
		return "", err
	}
	if err := os.RemoveAll(extractDir); err != nil {
		return "", err
	}
	if err := Extract(archivePath, extractDir); err != nil {
		return "", err
	}
	if err := os.WriteFile(stamp, []byte(item.Manifest.Version+"\n"), 0o644); err != nil {
		return "", err
	}
	return extractDir, nil
}

func downloadParts(ctx context.Context, client *githubrel.Client, dest string, item Published, archivePath string) error {
	var paths []string
	for _, name := range item.Manifest.Parts {
		asset, ok := githubrel.FindAsset(item.Release, name)
		if !ok {
			return fmt.Errorf("part %q is not attached to the release", name)
		}
		partPath := filepath.Join(dest, name)
		if err := client.Download(ctx, asset, partPath); err != nil {
			return err
		}
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
