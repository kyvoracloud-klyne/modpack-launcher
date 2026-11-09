package launch

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"modpack-launcher/internal/game"
	"modpack-launcher/internal/pack"
)

var standardModsPath = regexp.MustCompile(`"([^"\r\n]*Standard Mods[/\\][^"\r\n]*)"`)

// EnablePackMods records every mod in the pack as enabled and trusted in the
// mod profile's settings.rules. The Steam profile is left untouched. YAML reads
// this file before the game creates a default one, so it has to exist first.
// The returned path is one stored reference, for the launch log.
func EnablePackMods(install game.Install, profile, packDir string) (string, error) {
	root, err := pack.ContentRoot(packDir)
	if err != nil {
		return "", err
	}
	mods, err := pack.ModsIn(root)
	if err != nil {
		return "", err
	}
	refs := make([]string, 0, len(mods))
	for _, mod := range mods {
		ref, err := ModReference(install, mod.Name)
		if err != nil {
			return "", err
		}
		refs = append(refs, ref)
	}
	if err := WriteModSettings(filepath.Join(profile, "settings.rules"), refs); err != nil {
		return "", err
	}
	if len(refs) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%d mods, for example %s", len(refs), refs[0]), nil
}

// ClearEnabledMods removes the enabled and trusted lists from a mod profile.
// A missing settings file is left missing. The Steam profile is not opened.
func ClearEnabledMods(settingsPath string) error {
	if _, err := os.Stat(settingsPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return WriteModSettings(settingsPath, nil)
}

// ModReference is the path Cosmoteer stores for a Standard Mods folder.
// A path already saved in the Steam profile wins, because that is the form the
// game matched. Otherwise Proton's drive letter is used, after symlinks.
func ModReference(install game.Install, folder string) (string, error) {
	if prefix, ok := standardModsPrefix(install); ok {
		return prefix + "/" + folder, nil
	}
	root := install.Root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	absolute := filepath.Join(root, "Standard Mods", folder)
	if install.Prefix == "" {
		return strings.ReplaceAll(absolute, `\`, `/`), nil
	}
	letter, driveRoot, err := protonDrive(install.Prefix, absolute)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(driveRoot, absolute)
	if err != nil {
		return "", err
	}
	rel = strings.ReplaceAll(rel, `\`, `/`)
	return letter + ":/" + rel, nil
}

func protonDrive(prefix, absolute string) (letter, root string, err error) {
	dir := filepath.Join(prefix, "dosdevices")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", fmt.Errorf("proton drive map: %w", err)
	}
	bestLen := -1
	for _, entry := range entries {
		name := entry.Name()
		if len(name) != 2 || name[1] != ':' {
			continue
		}
		target, err := filepath.EvalSymlinks(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if !pathUnder(target, absolute) {
			continue
		}
		if len(target) < bestLen {
			continue
		}
		if len(target) == bestLen && letter != "" && (letter != "Z" || strings.EqualFold(name[:1], "z")) {
			continue
		}
		bestLen = len(target)
		letter = strings.ToUpper(name[:1])
		root = target
	}
	if letter == "" {
		return "", "", fmt.Errorf("no proton drive contains %s", absolute)
	}
	return letter, root, nil
}

func standardModsPrefix(install game.Install) (string, bool) {
	if install.Prefix == "" {
		return "", false
	}
	counts := map[string]int{}
	for _, file := range steamSettings(install.Prefix) {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, match := range standardModsPath.FindAllSubmatch(data, -1) {
			path := strings.ReplaceAll(string(match[1]), `\`, `/`)
			const marker = "/Standard Mods/"
			i := strings.Index(path, marker)
			if i < 0 {
				continue
			}
			counts[path[:i+len(marker)-1]]++
		}
	}
	best, n := "", 0
	for prefix, count := range counts {
		if count > n {
			best, n = prefix, count
		}
	}
	return best, best != ""
}

func steamSettings(prefix string) []string {
	users := filepath.Join(prefix, "drive_c", "users")
	entries, err := os.ReadDir(users)
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cosmoteer := filepath.Join(users, entry.Name(), "Saved Games", "Cosmoteer")
		profiles, err := os.ReadDir(cosmoteer)
		if err != nil {
			continue
		}
		for _, profile := range profiles {
			if !profile.IsDir() || profile.Name() == "ModPackProfile" {
				continue
			}
			file := filepath.Join(cosmoteer, profile.Name(), "settings.rules")
			if _, err := os.Stat(file); err == nil {
				files = append(files, file)
			}
		}
	}
	return files
}

func pathUnder(root, path string) bool {
	if root == string(os.PathSeparator) {
		return strings.HasPrefix(path, root)
	}
	return path == root || strings.HasPrefix(path, root+string(os.PathSeparator))
}

// WriteModSettings puts paths into EnabledMods and TrustedMods.
// An existing settings file keeps its other values. AutoDisableMods is turned
// off so a game update does not clear the list the launcher just wrote.
func WriteModSettings(path string, modPaths []string) error {
	data, err := os.ReadFile(path)
	var content string
	if err == nil {
		content = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}
	updated := applyModLists(content, modPaths)
	if updated == content && err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, []byte(updated), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func applyModLists(content string, modPaths []string) string {
	if content == "" {
		return newSettings(modPaths)
	}
	bom := ""
	if strings.HasPrefix(content, "\uFEFF") {
		bom = "\uFEFF"
		content = strings.TrimPrefix(content, "\uFEFF")
	}
	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
		content = strings.ReplaceAll(content, "\r\n", "\n")
	}
	lines := strings.Split(content, "\n")
	indent := "\t"
	if i, ok := findKey(lines, "EnabledMods"); ok {
		indent = lineIndent(lines[i])
	}
	enabled := listBlock(indent, "EnabledMods", modPaths)
	trusted := listBlock(indent, "TrustedMods", modPaths)
	lines = replaceList(lines, "EnabledMods", enabled)
	if _, ok := findKey(lines, "TrustedMods"); ok {
		lines = replaceList(lines, "TrustedMods", trusted)
	} else if i, ok := findKey(lines, "EnabledMods"); ok {
		_, end := blockSpan(lines, i)
		lines = insertLines(lines, end+1, trusted)
	} else {
		lines = insertLines(lines, len(lines), enabled)
		lines = insertLines(lines, len(lines), trusted)
	}
	lines = disableAuto(lines)
	return bom + strings.Join(lines, nl)
}

func newSettings(modPaths []string) string {
	var b strings.Builder
	b.WriteString("GameSettings\n{\n")
	b.WriteString("\tIsFirstBoot = false\n")
	for _, line := range listBlock("\t", "EnabledMods", modPaths) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("\tAutoDisableMods = false\n")
	for _, line := range listBlock("\t", "TrustedMods", modPaths) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return b.String()
}

func listBlock(indent, key string, paths []string) []string {
	lines := []string{indent + key, indent + "["}
	for _, path := range paths {
		lines = append(lines, indent+"\t"+quoteRules(path))
	}
	lines = append(lines, indent+"]")
	return lines
}

func quoteRules(path string) string {
	path = strings.ReplaceAll(path, `\`, `\\`)
	path = strings.ReplaceAll(path, `"`, `\"`)
	return `"` + path + `"`
}

func replaceList(lines []string, key string, block []string) []string {
	i, ok := findKey(lines, key)
	if !ok {
		return lines
	}
	_, end := blockSpan(lines, i)
	out := append([]string{}, lines[:i]...)
	out = append(out, block...)
	return append(out, lines[end+1:]...)
}

func blockSpan(lines []string, keyLine int) (start, end int) {
	if strings.HasSuffix(strings.TrimRight(strings.TrimLeft(lines[keyLine], " \t"), " \t"), "[]") {
		return keyLine, keyLine
	}
	for i := keyLine + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "]" {
			return keyLine, i
		}
	}
	return keyLine, keyLine
}

func insertLines(lines []string, at int, block []string) []string {
	if at > len(lines) {
		at = len(lines)
	}
	out := append([]string{}, lines[:at]...)
	out = append(out, block...)
	return append(out, lines[at:]...)
}

func disableAuto(lines []string) []string {
	for i, line := range lines {
		indent := lineIndent(line)
		rest := strings.TrimSpace(line)
		if !strings.HasPrefix(rest, "AutoDisableMods") {
			continue
		}
		if rest == "AutoDisableMods = false" {
			return lines
		}
		lines[i] = indent + "AutoDisableMods = false"
		return lines
	}
	return lines
}

func findKey(lines []string, key string) (int, bool) {
	for i, line := range lines {
		rest := strings.TrimSpace(line)
		if rest == key || rest == key+"[]" {
			return i, true
		}
	}
	return 0, false
}

func lineIndent(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}
