package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// ProgramDir is a folder with a program in it that FolderTrace found on a drive,
// whether or not it was ever installed.
type ProgramDir struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Unity   string   `json:"unity,omitempty"` // Company\Product from a Unity game's app.info
	Aliases []string `json:"aliases,omitempty"`
}

type programCache struct {
	FullScan time.Time    `json:"fullScan"`
	Programs []ProgramDir `json:"programs"`
}

const fullSearchEvery = 7 * 24 * time.Hour

var skipSearch = map[string]bool{"windows": true, "$recycle.bin": true, "system volume information": true, "programdata": true,
	"appdata": true, "node_modules": true, ".git": true, "perflogs": true, "windowsapps": true, "winsxs": true, "__pycache__": true,
	"$windows.~bt": true, "$windows.~ws": true, "recovery": true, "msocache": true, "redist": true, "_commonredist": true,
	"redistributable": true, "vcredist": true, "directx": true, "dotnet": true, "go": true, "site-packages": true, "common files": true}

var helperExes = []string{"unins", "setup", "install", "redist", "dxsetup", "crash", "anticheat", "battleye", "update", "helper", "bootstrap"}

// Folders where a loose .exe doesn't mean the folder itself is a program.
var holdingFolders = map[string]bool{"desktop": true, "downloads": true, "documents": true, "games": true, "program files": true,
	"program files (x86)": true, "users": true, "steamapps": true, "common": true, "public": true}

func isHelperExe(name string) bool {
	low := strings.ToLower(name)
	for _, h := range helperExes {
		if strings.Contains(low, h) {
			return true
		}
	}
	return false
}

// unityInfo reads a Unity game's app.info, which holds its company and product on two
// lines. That's the same Company\Product it uses in LocalLow.
func unityInfo(dir string, entries []os.DirEntry) string {
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), "_Data") {
			data, err := os.ReadFile(filepath.Join(dir, e.Name(), "app.info"))
			if err != nil || len(data) > 4096 {
				continue
			}
			lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
			if len(lines) >= 2 && strings.TrimSpace(lines[0]) != "" && strings.TrimSpace(lines[1]) != "" {
				return strings.TrimSpace(lines[0]) + `\` + strings.TrimSpace(lines[1])
			}
		}
	}
	return ""
}

// searchPrograms walks folders looking for programs. Once a folder holds a real program,
// it doesn't go deeper, because everything below belongs to that program.
func searchPrograms(roots []string, maxDepth int, stop *atomic.Bool, report func(string)) []ProgramDir {
	var found []ProgramDir
	visited := 0
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if stop != nil && stop.Load() {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		visited++
		if report != nil && visited%200 == 0 {
			report(dir)
		}
		base := strings.ToLower(filepath.Base(dir))
		holding := holdingFolders[base] || len(filepath.Clean(dir)) <= 3
		hasProgram := false
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".exe") && !isHelperExe(e.Name()) {
				hasProgram = true
				break
			}
		}
		if hasProgram && !holding {
			found = append(found, ProgramDir{Path: dir, Name: nameFromPath(dir), Unity: unityInfo(dir, entries), Aliases: folderAliases(dir)})
			return
		}
		if depth <= 0 {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || isLink(e) || skipSearch[strings.ToLower(e.Name())] {
				continue
			}
			walk(filepath.Join(dir, e.Name()), depth-1)
		}
	}
	for _, r := range roots {
		walk(r, maxDepth)
	}
	return found
}

// quickRoots lists what's rechecked on every scan: the usual places people unzip things,
// plus every folder where a program has turned up before. A new game in E:\Games is
// found because E:\Games already held one.
func quickRoots(userFolders []string, known []ProgramDir) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		k := strings.ToLower(filepath.Clean(p))
		if p != "" && !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	for _, p := range userFolders {
		add(p)
	}
	for _, k := range known {
		if parent := filepath.Dir(k.Path); len(filepath.Clean(parent)) > 3 {
			add(parent)
		}
	}
	return out
}

// mergePrograms combines the last search with a new one. A program that's vanished from a
// connected drive is returned as gone. One on an unplugged drive is kept as it was.
func mergePrograms(old, fresh []ProgramDir, pathExists, driveExists func(string) bool) (kept, gone []ProgramDir) {
	seen := map[string]bool{}
	for _, p := range fresh {
		seen[strings.ToLower(filepath.Clean(p.Path))] = true
		kept = append(kept, p)
	}
	for _, p := range old {
		if seen[strings.ToLower(filepath.Clean(p.Path))] {
			continue
		}
		switch {
		case pathExists(p.Path):
			kept = append(kept, p)
		case driveExists(p.Path):
			gone = append(gone, p)
		default:
			kept = append(kept, p)
		}
	}
	return kept, gone
}

func programsPath(dir string) string { return filepath.Join(dir, "program-list.json") }

func loadPrograms(dir string) programCache {
	var c programCache
	if data, err := os.ReadFile(programsPath(dir)); err == nil {
		if json.Unmarshal(data, &c) != nil {
			return programCache{}
		}
	}
	return c
}

// savePrograms writes only to FolderTrace's own folder.
func savePrograms(dir string, c programCache) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(programsPath(dir), data, 0o644)
}

func addProgramsToInventory(inv *Inventory, kept, gone []ProgramDir) {
	for _, p := range kept {
		name := p.Name
		unity := ""
		if p.Unity != "" {
			unity = strings.ToLower(p.Unity)
			if i := strings.IndexByte(p.Unity, '\\'); i >= 0 {
				name = p.Unity[i+1:]
			}
		}
		inv.Apps = append(inv.Apps, App{Name: name, Source: sourceDisk, Paths: []string{p.Path}, Aliases: p.Aliases, Unity: unity})
	}
	for _, p := range gone {
		inv.Gone = append(inv.Gone, App{Name: p.Name, Source: sourceDisk, Aliases: append(p.Aliases, filepath.Base(p.Path))})
	}
}

var ignoredAliases = map[string]bool{"engine": true, "binaries": true, "content": true, "redist": true, "commonredist": true,
	"data": true, "bin": true, "lib": true, "libs": true, "resources": true, "plugins": true, "locales": true, "tools": true,
	"docs": true, "licenses": true, "support": true, "update": true, "updater": true, "launcher": true, "installers": true,
	"directx": true, "vcredist": true, "dotnet": true, "assets": true, "config": true, "cache": true, "temp": true, "logs": true,
	"common": true, "shared": true, "steam": true, "app": true, "game": true, "main": true, "setup": true, "uninstall": true,
	"log": true, "screenshots": true, "screenshot": true, "saves": true, "save": true, "mods": true, "battleye": true, "easyanticheat": true, "eac": true, "crashes": true, "crashdumps": true, "settings": true, "profiles": true, "backup": true, "backups": true}

// folderAliases lists names inside an install folder. An Unreal game called ARC Raiders
// might be installed with a PioneerGame folder and keep its data in Local\PioneerGame.
func folderAliases(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name = strings.TrimSuffix(name, "_Data") // Unity games
		} else if strings.EqualFold(filepath.Ext(name), ".exe") {
			name = strings.TrimSuffix(name, filepath.Ext(name))
		} else {
			continue
		}
		if n := norm(name); len(n) >= 3 && !ignoredAliases[n] && !strings.HasPrefix(n, "unins") {
			out = append(out, name)
		}
		if len(out) >= 60 {
			break
		}
	}
	return out
}
