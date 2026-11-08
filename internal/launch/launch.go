package launch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"modpack-launcher/internal/game"
	"modpack-launcher/internal/ships"
	"modpack-launcher/internal/swap"
)

// Options selects the game, pack, and state file for one modded run.
type Options struct {
	Install   game.Install
	PackDir   string
	StatePath string
	Ships     ships.Libraries
	SkipShips bool
	Log       func(string)
}

func say(log func(string), format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(msg)
	if log != nil {
		log(msg)
	}
}

// Prepare copies missing ships into ModPackProfile and swaps in the pack.
// The returned function restores the game files. The caller must refuse while
// Cosmoteer is already running.
func Prepare(opts Options) (func() error, error) {
	profile := game.ProfileDir(opts.Install.Root)
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return nil, err
	}
	if !opts.SkipShips && (opts.Ships.SavedShips != "" || opts.Ships.LostShips != "") {
		copied, err := ships.CopyLibraries(opts.Ships, profile)
		if err != nil {
			return nil, err
		}
		say(opts.Log, "Copied %d missing ship file(s) into %s", copied, profile)
	}
	if err := swap.Begin(opts.StatePath, opts.Install.Root, opts.PackDir); err != nil {
		return nil, err
	}
	say(opts.Log, "Installed the pack for this run under %s", filepath.Join(opts.Install.Root, "Standard Mods"))
	return func() error {
		say(opts.Log, "Restoring Standard Mods and the game Bin")
		return swap.Restore(opts.StatePath)
	}, nil
}

// RootSaveArgument is the Windows path passed to --rootsavepath.
func RootSaveArgument(install game.Install) (string, error) {
	profile := game.ProfileDir(install.Root)
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return profile, nil
	}
	return protonWindowsPath(install, profile)
}

func protonWindowsPath(install game.Install, profile string) (string, error) {
	if install.Prefix != "" {
		if converted, err := winepath(install.Prefix, profile); err == nil && converted != "" {
			return converted, nil
		}
	}
	return "Z:" + strings.ReplaceAll(profile, "/", `\`), nil
}

func winepath(prefix, unixPath string) (string, error) {
	wine, err := protonWine(prefix)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, wine, "winepath", "-w", unixPath)
	cmd.Env = append(os.Environ(), "WINEPREFIX="+prefix, "WINEDEBUG=-all")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	converted := strings.TrimSpace(string(out))
	if converted == "" {
		return "", fmt.Errorf("winepath returned an empty path")
	}
	return converted, nil
}

func protonWine(prefix string) (string, error) {
	steamapps := filepath.Dir(filepath.Dir(prefix))
	common := filepath.Join(steamapps, "common")
	entries, err := os.ReadDir(common)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "Proton") {
			continue
		}
		wine := filepath.Join(common, entry.Name(), "files", "bin", "wine")
		if _, err := os.Stat(wine); err == nil {
			return wine, nil
		}
	}
	return "", fmt.Errorf("proton wine was not found")
}

// Start launches Cosmoteer and waits until it exits.
func Start(install game.Install, rootsave string, log func(string)) error {
	if runtime.GOOS == "windows" {
		return startWindows(install, rootsave, log)
	}
	return startLinux(rootsave, log)
}

func startWindows(install game.Install, rootsave string, log func(string)) error {
	exe := filepath.Join(install.Bin, "Cosmoteer.exe")
	cmd := exec.Command(exe, "--rootsavepath", rootsave)
	cmd.Dir = install.Bin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	say(log, "Starting %s --rootsavepath %s", exe, rootsave)
	return cmd.Run()
}

func startLinux(rootsave string, log func(string)) error {
	steam, err := game.SteamExecutable()
	if err != nil {
		return err
	}
	cmd := exec.Command(steam, "-applaunch", game.AppID, "--rootsavepath", rootsave)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	say(log, "Starting steam -applaunch %s --rootsavepath %s", game.AppID, rootsave)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return waitForGame(log)
}

func waitForGame(log func(string)) error {
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if game.Running() {
			break
		}
		time.Sleep(time.Second)
	}
	if !game.Running() {
		return fmt.Errorf("Cosmoteer did not start within 3 minutes")
	}
	say(log, "Cosmoteer is running; game files stay swapped until it exits")
	for game.Running() {
		time.Sleep(time.Second)
	}
	return nil
}
