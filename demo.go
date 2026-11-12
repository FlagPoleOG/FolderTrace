//go:build demo

// The demo build scans a made-up AppData and Program Files instead of the real ones,
// so Store screenshots show sample data and nothing from the person's PC. It is only
// compiled with -tags demo, never into the release app.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const demoMode = true

var demoBase = filepath.Join(os.TempDir(), "FolderTrace Demo")

type demoFile struct {
	rel     string // under the demo base, using \
	ageDays int
}

// Sizes shown in the screenshots. The files on disk are tiny.
var demoSizes = map[string]struct {
	bytes int64
	files int
}{
	`Local\Spotify`:                          {3_650_000_000, 1840},
	`Local\NVIDIA\DXCache`:                   {4_480_000_000, 212},
	`Local\Google\Chrome`:                    {1_910_000_000, 6120},
	`Local\Discord`:                          {431_000_000, 388},
	`LocalLow\Mossbyte Games\Lantern Rush`:   {1_270_000_000, 64},
	`Roaming\obs-studio`:                     {88_000_000, 141},
	`Roaming\Clipmate Pro`:                   {12_400_000, 9},
	`Roaming\Code`:                           {214_000_000, 930},
	`Local\Programs\Microsoft VS Code`:       {512_000_000, 2210},
	`Local\EpicGamesLauncher`:                {640_000_000, 1180},
	`Local\Battle.net`:                       {96_000_000, 77},
	`Roaming\Nimbus Notes`:                   {41_000_000, 120},
	`Local\Temp`:                             {2_300_000_000, 5400},
	`Local\Microsoft\Windows`:                {1_120_000_000, 3900},
	`Local\CrashDumps`:                       {610_000_000, 14},
	`Roaming\Tapestry Launcher`:              {9_800_000, 31},
	`Program Files\Mozilla Firefox`:          {270_000_000, 110},
	`Program Files (x86)\Steam`:              {1_480_000_000, 2600},
	`Program Files\Orbit VPN`:                {118_000_000, 85},
	`Program Files (x86)\Retro Paint Studio`: {64_000_000, 40},
}

var demoFiles = []demoFile{
	// Installed and in use.
	{`Local\Discord\app-1.0.9\Discord.exe`, 2},
	{`Local\Google\Chrome\User Data\Default\Cookies`, 1},
	{`Local\Programs\Microsoft VS Code\Code.exe`, 6},
	{`Roaming\Code\User\settings.json`, 1},
	{`Local\EpicGamesLauncher\Saved\webcache\index`, 3},
	{`Local\Battle.net\Cache\data`, 4},
	// Shared and Windows.
	{`Local\NVIDIA\DXCache\shader.bin`, 0},
	{`Local\Temp\setup.tmp`, 0},
	{`Local\Microsoft\Windows\Explorer\thumbcache.db`, 1},
	{`Local\CrashDumps\game.dmp`, 40},
	// Leftovers from removed programs.
	{`Local\Spotify\Storage\data.bnk`, 420},
	{`LocalLow\Mossbyte Games\Lantern Rush\saves\slot1.dat`, 610},
	{`Roaming\obs-studio\basic\scenes\Untitled.json`, 380},
	{`Roaming\Clipmate Pro\settings.ini`, 540},
	// Not sure: unknown, changed recently.
	{`Roaming\Nimbus Notes\cache.db`, 3},
	{`Roaming\Tapestry Launcher\state.json`, 9},
	// Program Files.
	{`Program Files\Mozilla Firefox\firefox.exe`, 8},
	{`Program Files (x86)\Steam\steam.exe`, 1},
	{`Program Files\Orbit VPN\orbitvpn.exe`, 700},
	{`Program Files (x86)\Retro Paint Studio\rpaint.exe`, 900},
}

func init() {
	dataDirOverride = filepath.Join(demoBase, "data")
}

// demoSetup writes the sample folders and returns their roots and a matching
// list of installed programs.
func demoSetup() (appRoots, progRoots []Root, inv *Inventory) {
	now := time.Now()
	for _, f := range demoFiles {
		p := filepath.Join(demoBase, filepath.FromSlash(strings.ReplaceAll(f.rel, `\`, "/")))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("FolderTrace demo file"), 0o644)
		when := now.Add(-time.Duration(f.ageDays) * 24 * time.Hour)
		// Folders take the age of what's inside them.
		for d := p; strings.HasPrefix(d, demoBase) && d != demoBase; d = filepath.Dir(d) {
			_ = os.Chtimes(d, when, when)
		}
	}
	for _, l := range []string{"Local", "LocalLow", "Roaming"} {
		appRoots = append(appRoots, Root{l, filepath.Join(demoBase, l)})
	}
	for _, l := range []string{"Program Files", "Program Files (x86)"} {
		progRoots = append(progRoots, Root{l, filepath.Join(demoBase, l)})
	}
	at := func(rel string) string {
		return filepath.Join(demoBase, filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/")))
	}
	src := "your installed programs list"
	inv = &Inventory{Apps: []App{
		{Name: "Discord", Publisher: "Discord Inc.", Source: src, Paths: []string{at(`Local\Discord`)}},
		{Name: "Google Chrome", Publisher: "Google LLC", Source: src, Paths: []string{at(`Program Files\Google\Chrome`)}},
		{Name: "Microsoft Visual Studio Code", Publisher: "Microsoft Corporation", Source: src, Paths: []string{at(`Local\Programs\Microsoft VS Code`)}, Aliases: []string{"Code"}},
		{Name: "Epic Games Launcher", Publisher: "Epic Games, Inc.", Source: src, Paths: []string{at(`Program Files (x86)\Epic Games`)}, Aliases: []string{"EpicGamesLauncher"}},
		{Name: "Battle.net", Publisher: "Blizzard Entertainment", Source: src, Paths: []string{at(`Program Files (x86)\Battle.net`)}},
		{Name: "Mozilla Firefox", Publisher: "Mozilla", Source: src, Paths: []string{at(`Program Files\Mozilla Firefox`)}},
		{Name: "Steam", Publisher: "Valve Corporation", Source: src, Paths: []string{at(`Program Files (x86)\Steam`)}},
		{Name: "NVIDIA Graphics Driver", Publisher: "NVIDIA Corporation", Source: src},
	}}
	return appRoots, progRoots, inv
}

// demoPolish gives the sample folders realistic sizes and paths for the screenshots.
func demoPolish(all ...[]*Folder) {
	for _, list := range all {
		for _, f := range list {
			if s, ok := demoSizes[f.Rel]; ok {
				f.Size, f.Files = s.bytes, s.files
			}
			if strings.HasPrefix(f.Rel, "Program Files") {
				f.Path = `C:\` + f.Rel
			} else {
				f.Path = `C:\Users\Alex\AppData\` + f.Rel
			}
		}
	}
}
