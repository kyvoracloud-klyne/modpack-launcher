package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"modpack-launcher/internal/config"
	"modpack-launcher/internal/game"
	"modpack-launcher/internal/githubrel"
	"modpack-launcher/internal/launch"
	"modpack-launcher/internal/pack"
	"modpack-launcher/internal/selfupdate"
	"modpack-launcher/internal/ships"
	"modpack-launcher/internal/swap"
)

const LocalPackID = "__local__"

func say(log func(string), format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(msg)
	if log != nil {
		log(msg)
	}
}

// Recover puts the game files back when a previous session died and nothing is still using them.
func Recover(log func(string)) error {
	statePath, err := config.StatePath()
	if err != nil {
		return err
	}
	state, ok, err := swap.Load(statePath)
	if err != nil || !ok {
		return err
	}
	running := game.Running()
	if !swap.ShouldRestore(state, running) {
		if running {
			return fmt.Errorf("a modded session is still running; game files stay swapped until Cosmoteer exits")
		}
		return fmt.Errorf("a modded launch is already in progress")
	}
	say(log, "Restoring game files left behind by an interrupted session")
	return swap.Restore(statePath)
}

// List returns pack releases from the configured repository.
func List(ctx context.Context, cfg config.Config) ([]pack.Published, error) {
	owner, name, err := config.ParseRepo(cfg.PackRepo)
	if err != nil {
		return nil, fmt.Errorf("set packRepo or --pack-repo: %w", err)
	}
	return pack.List(ctx, &githubrel.Client{Token: config.Token()}, owner, name)
}

// Launch downloads the pack when needed, swaps it in, and waits for Cosmoteer to exit.
// packDir skips the download. packID LocalPackID uses cfg.LocalPackDir.
func Launch(cfg config.Config, packID, version, packDir string, log func(string)) error {
	if game.Running() {
		return fmt.Errorf("Cosmoteer is running; close it before a modded launch")
	}
	if err := Recover(log); err != nil {
		return err
	}
	install, err := game.Resolve(cfg.GameRoot)
	if err != nil {
		return err
	}
	dir := packDir
	if packID == LocalPackID {
		dir = cfg.LocalPackDir
	}
	if dir == "" {
		if packID == "" {
			return fmt.Errorf("pass --pack or --pack-dir")
		}
		owner, repo, err := config.ParseRepo(cfg.PackRepo)
		if err != nil {
			return fmt.Errorf("set packRepo or --pack-repo: %w", err)
		}
		client := &githubrel.Client{Token: config.Token()}
		listed, err := pack.List(context.Background(), client, owner, repo)
		if err != nil {
			return err
		}
		selected, err := pack.Select(listed, packID, version)
		if err != nil {
			return err
		}
		cache, err := config.CacheDir()
		if err != nil {
			return err
		}
		say(log, "Downloading %s %s", selected.Manifest.ID, selected.Manifest.Version)
		dir, err = pack.EnsureCached(context.Background(), client, cache, selected)
		if err != nil {
			return err
		}
		say(log, "Pack %s %s is in %s", selected.Manifest.ID, selected.Manifest.Version, dir)
	}
	statePath, err := config.StatePath()
	if err != nil {
		return err
	}
	opts := launch.Options{Install: install, PackDir: dir, StatePath: statePath, Log: log}
	libraries, err := ships.FindProfileLibraries(game.ShipSearchRoots(install))
	if err != nil {
		say(log, "Warning: %v", err)
		say(log, "Continuing with an empty mod profile. Career saves are not copied.")
	} else {
		opts.Ships = libraries
	}
	restore, err := launch.Prepare(opts)
	if err != nil {
		return err
	}
	defer func() {
		if restoreErr := restore(); restoreErr != nil {
			say(log, "Error restoring game files: %v", restoreErr)
		}
	}()
	rootsave, err := launch.RootSaveArgument(install)
	if err != nil {
		return err
	}
	say(log, "Save profile: %s", rootsave)
	if err := launch.Start(install, rootsave, log); err != nil {
		return err
	}
	say(log, "Cosmoteer closed")
	return nil
}

// Restore puts Standard Mods and the Bin files back when this launcher is idle.
func Restore() error {
	statePath, err := config.StatePath()
	if err != nil {
		return err
	}
	state, ok, err := swap.Load(statePath)
	if err != nil {
		return err
	}
	if ok && !swap.ShouldRestore(state, game.Running()) {
		return fmt.Errorf("Cosmoteer or the launcher that swapped it is still running")
	}
	return swap.Restore(statePath)
}

// Update downloads a newer launcher build. exit reports that Windows started a helper and this process must quit.
func Update(cfg config.Config, version string, checkOnly bool, log func(string)) (bool, error) {
	owner, name, err := config.ParseRepo(cfg.LauncherRepo)
	if err != nil {
		return false, fmt.Errorf("set launcherRepo or --launcher-repo: %w", err)
	}
	if err := Recover(log); err != nil {
		return false, err
	}
	client := &githubrel.Client{Token: config.Token()}
	release, newer, err := selfupdate.Check(context.Background(), client, owner, name, version)
	if err != nil {
		return false, err
	}
	say(log, "Installed %s, latest %s", version, release.TagName)
	if !newer {
		say(log, "Already up to date")
		return false, nil
	}
	if checkOnly {
		say(log, "An update is available")
		return false, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	downloaded, err := selfupdate.Download(context.Background(), client, release, exe)
	if err != nil {
		return false, err
	}
	if runtime.GOOS == "windows" {
		script := filepath.Join(filepath.Dir(exe), "modpack-launcher-update.bat")
		if err := os.WriteFile(script, []byte(selfupdate.WindowsHelper(os.Getpid(), downloaded, exe)), 0o644); err != nil {
			return false, err
		}
		cmd := exec.Command("cmd.exe", "/C", "start", "", "/MIN", script)
		if err := cmd.Start(); err != nil {
			return false, err
		}
		say(log, "Update downloaded. This process will exit so the new executable can replace it.")
		return true, nil
	}
	if err := selfupdate.Replace(downloaded, exe); err != nil {
		return false, err
	}
	say(log, "Updated %s to %s", exe, release.TagName)
	return false, nil
}
