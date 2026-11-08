package swap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"modpack-launcher/internal/fsutil"
	"modpack-launcher/internal/pack"
)

const modsBackupName = "Standard Mods.launcher-vanilla"

// State records a swap so a killed session can be undone on the next start.
type State struct {
	GameRoot   string       `json:"gameRoot"`
	OwnerPID   int          `json:"ownerPid,omitempty"`
	ModsBackup string       `json:"modsBackup,omitempty"`
	BinBackups []FileBackup `json:"binBackups,omitempty"`
	BinAdded   []string     `json:"binAdded,omitempty"`
}

// FileBackup is a Bin file moved aside before the modded run.
type FileBackup struct {
	Original string `json:"original"`
	Backup   string `json:"backup"`
}

// Load reads a swap state file. A missing file is not an error.
func Load(path string) (State, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, false, err
	}
	return state, true, nil
}

func save(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Begin moves the current Standard Mods aside, copies pack mods in, and installs
// the YAML Bin proxy for this operating system. The state file is written before
// the first replacement so the next start can undo a partial swap.
func Begin(statePath, gameRoot, packDir string) error {
	if _, ok, err := Load(statePath); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("a previous modded session was not restored; start the launcher again to restore it")
	}
	root, err := pack.ContentRoot(packDir)
	if err != nil {
		return err
	}
	mods, err := pack.ModsIn(root)
	if err != nil {
		return err
	}
	yamlMod, err := pack.FindYAML(mods)
	if err != nil {
		return err
	}
	bin := filepath.Join(gameRoot, "Bin")
	if _, err := os.Stat(filepath.Join(bin, "Cosmoteer.exe")); err != nil {
		return fmt.Errorf("Cosmoteer Bin not found at %s", bin)
	}

	state := State{GameRoot: gameRoot, OwnerPID: os.Getpid()}
	standard := filepath.Join(gameRoot, "Standard Mods")
	backup := filepath.Join(gameRoot, modsBackupName)
	if _, err := os.Stat(backup); err == nil {
		return fmt.Errorf("%s already exists; restore it before launching", backup)
	}
	if _, err := os.Stat(standard); err == nil {
		state.ModsBackup = backup
	}
	if err := save(statePath, state); err != nil {
		return err
	}

	if state.ModsBackup != "" {
		if err := os.Rename(standard, backup); err != nil {
			return finishErr(statePath, state, err)
		}
	}
	if err := os.MkdirAll(standard, 0o755); err != nil {
		return finishErr(statePath, state, err)
	}
	if err := pack.InstallMods(mods, standard); err != nil {
		return finishErr(statePath, state, err)
	}
	if err := installProxy(statePath, bin, yamlMod.Dir, &state); err != nil {
		return finishErr(statePath, state, err)
	}
	return save(statePath, state)
}

func finishErr(statePath string, state State, cause error) error {
	if undoErr := undo(state); undoErr != nil {
		_ = save(statePath, state)
		return fmt.Errorf("%w (restore failed: %v)", cause, undoErr)
	}
	_ = os.Remove(statePath)
	return cause
}

func installProxy(statePath, bin, yamlDir string, state *State) error {
	var names []string
	if runtime.GOOS == "windows" {
		names = []string{"winmm.dll", "ModLoader.dll"}
	} else {
		names = []string{"Cosmoteer.dll", "ModLoader.dll", "unmanaged.dll"}
	}
	for _, name := range names {
		src := filepath.Join(yamlDir, name)
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("YAML is missing %s", name)
		}
		dst := filepath.Join(bin, name)
		if _, err := os.Stat(dst); err == nil {
			bak := dst + ".launcher-backup"
			if err := os.Rename(dst, bak); err != nil {
				return err
			}
			state.BinBackups = append(state.BinBackups, FileBackup{Original: dst, Backup: bak})
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		} else {
			state.BinAdded = append(state.BinAdded, dst)
		}
		if err := save(statePath, *state); err != nil {
			return err
		}
		if err := fsutil.CopyFile(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// ShouldRestore reports whether a saved swap has no live launcher and the game is closed.
func ShouldRestore(state State, gameRunning bool) bool {
	if pidAlive(state.OwnerPID) || gameRunning {
		return false
	}
	return true
}

func Restore(statePath string) error {
	state, ok, err := Load(statePath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := undo(state); err != nil {
		return err
	}
	return os.Remove(statePath)
}

func undo(state State) error {
	for _, added := range state.BinAdded {
		if err := os.Remove(added); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, backup := range state.BinBackups {
		if err := os.Remove(backup.Original); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := os.Stat(backup.Backup); err == nil {
			if err := os.Rename(backup.Backup, backup.Original); err != nil {
				return err
			}
		}
	}
	if state.GameRoot == "" {
		return nil
	}
	standard := filepath.Join(state.GameRoot, "Standard Mods")
	if state.ModsBackup != "" {
		if _, err := os.Stat(state.ModsBackup); err != nil {
			return nil
		}
		if err := os.RemoveAll(standard); err != nil {
			return err
		}
		return os.Rename(state.ModsBackup, standard)
	}
	return os.RemoveAll(standard)
}
