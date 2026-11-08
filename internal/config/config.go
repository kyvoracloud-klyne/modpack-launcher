package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const AppName = "modpack-launcher"

const (
	DefaultPackRepo     = "kyvoracloud-klyne/cosmoteer-modpack-2026"
	DefaultLauncherRepo = "kyvoracloud-klyne/modpack-launcher"
)

// Config is the launcher's saved settings. A GitHub token is never stored here.
type Config struct {
	LauncherRepo string `json:"launcherRepo"`
	PackRepo     string `json:"packRepo"`
	GameRoot     string `json:"gameRoot"`
	LocalPackDir string `json:"localPackDir,omitempty"`
}

func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, AppName), nil
}

func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func StatePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "swap-state.json"), nil
}

func CacheDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cache"), nil
}

// Load reads config.json when it exists. Missing files yield an empty config.
func Load(path string) (Config, error) {
	var cfg Config
	if path == "" {
		var err error
		path, err = Path()
		if err != nil {
			return cfg, err
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes config.json. The GitHub token is not part of Config.
func Save(path string, cfg Config) error {
	if path == "" {
		var err error
		path, err = Path()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Apply overlays non-empty values onto cfg.
func Apply(cfg *Config, launcherRepo, packRepo, gameRoot string) {
	if launcherRepo != "" {
		cfg.LauncherRepo = launcherRepo
	}
	if packRepo != "" {
		cfg.PackRepo = packRepo
	}
	if gameRoot != "" {
		cfg.GameRoot = gameRoot
	}
	if env := os.Getenv("MODPACK_LAUNCHER_REPO"); env != "" && launcherRepo == "" {
		cfg.LauncherRepo = env
	}
	if env := os.Getenv("MODPACK_PACK_REPO"); env != "" && packRepo == "" {
		cfg.PackRepo = env
	}
	if env := os.Getenv("COSMOTEER_DIR"); env != "" && gameRoot == "" {
		cfg.GameRoot = env
	}
}

// FillDefaults sets the GitHub repositories when the config does not name them.
func FillDefaults(cfg *Config) {
	if cfg.PackRepo == "" {
		cfg.PackRepo = DefaultPackRepo
	}
	if cfg.LauncherRepo == "" {
		cfg.LauncherRepo = DefaultLauncherRepo
	}
}

// Token returns a GitHub token from the environment, never from the config file.
func Token() string {
	if token := os.Getenv("MODPACK_GITHUB_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("GITHUB_TOKEN")
}

// ParseRepo checks an owner/name GitHub coordinate.
func ParseRepo(repo string) (owner, name string, err error) {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("github repo must be owner/name, got %q", repo)
	}
	return owner, name, nil
}
