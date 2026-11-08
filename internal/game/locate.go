package game

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const AppID = "799600"

// Install is a Cosmoteer game directory.
type Install struct {
	Root   string
	Bin    string
	Prefix string
}

// Resolve finds the game. explicit may be the game root, Bin, or Cosmoteer.exe.
func Resolve(explicit string) (Install, error) {
	if explicit != "" {
		return fromInput(explicit)
	}
	if env := os.Getenv("COSMOTEER_DIR"); env != "" {
		return fromInput(env)
	}
	return search()
}

func fromInput(input string) (Install, error) {
	input = strings.TrimRight(input, `/\`)
	base := filepath.Base(input)
	if base == "Cosmoteer.exe" || base == "Cosmoteer.dll" {
		input = filepath.Dir(input)
	}
	if isBin(input) {
		return installFromBin(input), nil
	}
	if isBin(filepath.Join(input, "Bin")) {
		return installFromBin(filepath.Join(input, "Bin")), nil
	}
	return Install{}, fmt.Errorf("could not find Cosmoteer Bin in %s", input)
}

func isBin(dir string) bool {
	_, exeErr := os.Stat(filepath.Join(dir, "Cosmoteer.exe"))
	_, dllErr := os.Stat(filepath.Join(dir, "Cosmoteer.dll"))
	return exeErr == nil && dllErr == nil
}

func installFromBin(bin string) Install {
	root := filepath.Dir(bin)
	return Install{Root: root, Bin: bin, Prefix: protonPrefix(root)}
}

func search() (Install, error) {
	var roots []string
	if runtime.GOOS == "windows" {
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
			if value := os.Getenv(env); value != "" {
				roots = append(roots, filepath.Join(value, "Steam"))
			}
		}
	} else {
		home, _ := os.UserHomeDir()
		roots = append(roots,
			filepath.Join(home, ".steam", "steam"),
			filepath.Join(home, ".steam", "root"),
			filepath.Join(home, ".steam", "debian-installation"),
			filepath.Join(home, ".local", "share", "Steam"),
			filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", "data", "Steam"),
		)
	}
	seen := map[string]bool{}
	for _, root := range roots {
		for _, library := range libraries(root) {
			if seen[library] {
				continue
			}
			seen[library] = true
			bin := filepath.Join(library, "steamapps", "common", "Cosmoteer", "Bin")
			if isBin(bin) {
				return installFromBin(bin), nil
			}
		}
	}
	return Install{}, fmt.Errorf("could not find Cosmoteer; pass --game")
}

func libraries(steamRoot string) []string {
	libs := []string{steamRoot}
	vdf := filepath.Join(steamRoot, "steamapps", "libraryfolders.vdf")
	file, err := os.Open(vdf)
	if err != nil {
		return libs
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, `"path"`) {
			continue
		}
		_, value, ok := strings.Cut(line, `"path"`)
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		value = strings.ReplaceAll(value, `\\`, `\`)
		if value != "" {
			libs = append(libs, value)
		}
	}
	return libs
}

func protonPrefix(gameRoot string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	steamapps := filepath.Dir(filepath.Dir(gameRoot))
	if filepath.Base(steamapps) != "steamapps" {
		return ""
	}
	return filepath.Join(steamapps, "compatdata", AppID, "pfx")
}

// ProfileDir is the save root passed to --rootsavepath.
func ProfileDir(gameRoot string) string {
	return filepath.Join(gameRoot, "ModPackProfile")
}

// ShipSearchRoots are directories that may contain the default Steam profile.
func ShipSearchRoots(install Install) []string {
	var roots []string
	if install.Prefix != "" {
		roots = append(roots, install.Prefix)
	}
	if runtime.GOOS == "windows" {
		if home, err := os.UserHomeDir(); err == nil {
			roots = append(roots, home)
		}
	}
	return roots
}

// SteamExecutable is the Steam client used for a Proton launch.
func SteamExecutable() (string, error) {
	candidates := []string{"/usr/games/steam", "/usr/bin/steam"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".steam", "steam", "steam.sh"),
			filepath.Join(home, ".local", "share", "Steam", "steam.sh"),
		)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find the steam client")
}
