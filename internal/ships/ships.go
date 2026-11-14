package ships

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"modpack-launcher/internal/fsutil"
)

// Libraries is the ship folders and game saves from one profile.
type Libraries struct {
	SavedShips string
	LostShips  string
	SavedGames string
}

// CopyMissing copies files that are not already in the destination.
// Files that exist at the destination are never overwritten.
func CopyMissing(src, dst string) (int, error) {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("%s is not a directory", src)
	}
	copied := 0
	err = filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		fileInfo, err := d.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() || fileInfo.Name() == "steam_autocloud.vdf" {
			return nil
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := fsutil.CopyFile(path, target); err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, err
}

// CopyLibraries byte-copies Saved Ships, Lost Ships, and Saved Games into the
// mod profile. A file already in the mod profile is left unchanged. Settings
// and logs are not part of Libraries.
func CopyLibraries(src Libraries, profile string) (int, error) {
	copied := 0
	pairs := []struct{ src, name string }{
		{src.SavedShips, "Saved Ships"},
		{src.LostShips, "Lost Ships"},
		{src.SavedGames, "Saved Games"},
	}
	for _, pair := range pairs {
		if pair.src == "" {
			continue
		}
		n, err := CopyMissing(pair.src, filepath.Join(profile, pair.name))
		copied += n
		if err != nil {
			return copied, err
		}
	}
	return copied, nil
}

// FindProfileLibraries searches Saved Games/Cosmoteer trees and returns the
// Steam profile with the newest ship library. roots are directories that
// directly contain the Cosmoteer save folder or a users tree to scan.
func FindProfileLibraries(roots []string) (Libraries, error) {
	var best Libraries
	var bestTime time.Time
	found := false
	for _, root := range roots {
		candidates, err := candidateProfiles(root)
		if err != nil {
			return Libraries{}, err
		}
		for _, candidate := range candidates {
			when := newest(candidate.SavedShips, candidate.LostShips, candidate.SavedGames)
			if !found || when.After(bestTime) {
				best = candidate
				bestTime = when
				found = true
			}
		}
	}
	if !found {
		return Libraries{}, fmt.Errorf("no Steam Cosmoteer profile with ships or game saves was found")
	}
	return best, nil
}

func candidateProfiles(root string) ([]Libraries, error) {
	var out []Libraries
	cosmoteerDirs, err := findCosmoteerDirs(root)
	if err != nil {
		return nil, err
	}
	for _, dir := range cosmoteerDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "ModPackProfile" {
				continue
			}
			profile := filepath.Join(dir, entry.Name())
			saved := filepath.Join(profile, "Saved Ships")
			lost := filepath.Join(profile, "Lost Ships")
			games := filepath.Join(profile, "Saved Games")
			if _, err := os.Stat(saved); err != nil {
				if _, lostErr := os.Stat(lost); lostErr != nil {
					if _, gamesErr := os.Stat(games); gamesErr != nil {
						continue
					}
				}
			}
			out = append(out, Libraries{SavedShips: saved, LostShips: lost, SavedGames: games})
		}
	}
	return out, nil
}

func findCosmoteerDirs(root string) ([]string, error) {
	var dirs []string
	direct := filepath.Join(root, "Saved Games", "Cosmoteer")
	if info, err := os.Stat(direct); err == nil && info.IsDir() {
		dirs = append(dirs, direct)
	}
	users := filepath.Join(root, "drive_c", "users")
	entries, err := os.ReadDir(users)
	if err != nil {
		if len(dirs) > 0 || os.IsNotExist(err) {
			return dirs, nil
		}
		// root itself may be a Proton prefix or a user profile parent.
		return dirs, nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(users, entry.Name(), "Saved Games", "Cosmoteer")
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			dirs = append(dirs, candidate)
		}
	}
	return dirs, nil
}

func newest(paths ...string) time.Time {
	var latest time.Time
	for _, path := range paths {
		_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
			return nil
		})
	}
	return latest
}
