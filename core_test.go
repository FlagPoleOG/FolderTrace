package main

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

type fixture struct {
	t     *testing.T
	roots []Root
	dir   string
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	dir := t.TempDir()
	f := &fixture{t: t, dir: dir, now: time.Now()}
	for _, l := range []string{"Local", "LocalLow", "Roaming"} {
		p := filepath.Join(dir, l)
		_ = os.MkdirAll(p, 0o755)
		f.roots = append(f.roots, Root{l, p})
	}
	return f
}

// file creates a file with a size and age under a root, using \ in the path.
func (f *fixture) file(rel string, size int, ageDays int) {
	f.t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/")))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		f.t.Fatal(err)
	}
	when := f.now.Add(-time.Duration(ageDays) * 24 * time.Hour)
	_ = os.Chtimes(p, when, when)
}

func (f *fixture) path(rel string) string {
	return filepath.Join(f.dir, filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/")))
}

func (f *fixture) scan(inv *Inventory) map[string]*Folder {
	var stop atomic.Bool
	out := map[string]*Folder{}
	for _, x := range Scan(f.roots, inv, &stop, nil, nil) {
		out[x.Rel] = x
	}
	return out
}

func want(t *testing.T, all map[string]*Folder, rel string, v Verdict, owner string) *Folder {
	t.Helper()
	f := all[rel]
	if f == nil {
		var names []string
		for k := range all {
			names = append(names, k)
		}
		t.Fatalf("%s has no row. Rows: %v", rel, names)
	}
	if f.Verdict != v || f.Owner != owner {
		t.Errorf("%s: got %q / %q, want %q / %q\n  why: %s", rel, f.Verdict, f.Owner, v, owner, f.Why)
	}
	return f
}

func TestAPersonsAppData(t *testing.T) {
	f := newFixture(t)
	// Installed in AppData, per the uninstall list.
	f.file(`Local\Discord\app-1.0\Discord.exe`, 5000, 2)
	// Unreal game installed through Steam, with data under its code name.
	f.file(`Local\PioneerGame\Saved\SaveGames\slot1.sav`, 2000, 3)
	// Removed game, with saves, untouched for two years.
	f.file(`Local\Wardogs\Saved\SaveGames\slot1.sav`, 4000, 730)
	// Removed tool, no owner found, a year and a half old.
	f.file(`Roaming\OldToolX\settings.ini`, 900, 540)
	// Unknown and recently changed: must not be called a leftover.
	f.file(`Roaming\MysteryThing\data.bin`, 900, 3)
	// Unknown, has a program, changed recently.
	f.file(`Local\PortableThing\portable.exe`, 900, 5)
	// Windows.
	f.file(`Local\Temp\x.tmp`, 100, 0)
	f.file(`Local\Microsoft\Windows\Explorer\thumbcache.db`, 100, 1)
	f.file(`Local\D3DSCache\a.bin`, 100, 400)
	// Containers.
	f.file(`Local\Google\Chrome\User Data\Default\Cookies`, 3000, 1)
	f.file(`Local\Google\Picasa2\db.bin`, 3000, 1500)
	// Unity game: LocalLow\Company\Product, game removed.
	f.file(`LocalLow\Some Studio\Cool Game\saves\a.dat`, 2500, 400)
	// Store apps.
	f.file(`Local\Packages\Microsoft.WindowsCalculator_8wekyb3d8bbwe\Settings\s.dat`, 10, 10)
	f.file(`Local\Packages\5319275A.WhatsAppDesktop_cv1g1gvanyjgm\LocalCache\a`, 10, 300)
	// Empty.
	_ = os.MkdirAll(f.path(`Roaming\GhostFolder`), 0o755)
	old := f.now.Add(-200 * 24 * time.Hour)
	_ = os.Chtimes(f.path(`Roaming\GhostFolder`), old, old)
	// Empty, but changed recently: an installed program may recreate it.
	_ = os.MkdirAll(f.path(`Roaming\FreshEmpty`), 0o755)
	// App known by name, not installed, old.
	f.file(`Roaming\Spotify\prefs`, 100, 500)
	// Publisher folder.
	f.file(`Roaming\Blue Hen Software\x.dat`, 100, 20)
	// Words that must NOT match anything just because they look similar.
	f.file(`Local\Apple Tree\x.dat`, 100, 600)
	f.file(`Local\Games\x.dat`, 100, 600)
	// Windows lists it, but the app is gone.
	f.file(`Local\BadApp\x.dat`, 100, 200)
	// Running app with no other evidence.
	f.file(`Roaming\Runner\runner.exe`, 100, 200)
	// Loose big files in Local.
	f.file(`Local\IconCache.db`, 2<<20, 1)

	inv := &Inventory{
		Apps: []App{
			{Name: "Discord", Source: sourceInstalled, Paths: []string{f.path(`Local\Discord`)}},
			{Name: "ARC Raiders", Source: sourceSteam, Paths: []string{`D:\Steam\steamapps\common\Arc Raiders`}, Aliases: []string{"PioneerGame", "Arc Raiders"}},
			{Name: "Google Chrome", Publisher: "Google LLC", Source: sourceInstalled, Paths: []string{`C:\Program Files\Google\Chrome`}},
			{Name: "Pineapple Studio", Source: sourceInstalled},
			{Name: "Epic Games Launcher", Publisher: "Epic Games, Inc.", Source: sourceInstalled},
			{Name: "Windows Calculator", Source: sourceStore, Aliases: []string{"Microsoft.WindowsCalculator_8wekyb3d8bbwe"}},
			{Name: "Blue Frog", Publisher: "Blue Hen Software", Source: sourceInstalled},
			{Name: "Bad App", Source: sourceInstalled, Broken: true, Paths: []string{`C:\Program Files\Bad App`}},
		},
		Running: []Process{{Name: "Runner", Path: f.path(`Roaming\Runner\runner.exe`)}},
	}
	all := f.scan(inv)

	x := want(t, all, `Local\Discord`, VerdictInUse, "Discord")
	if x.Confidence != "High" {
		t.Errorf("Discord confidence %s", x.Confidence)
	}
	x = want(t, all, `Local\PioneerGame`, VerdictInUse, "ARC Raiders")
	if !x.Saves {
		t.Error("ARC Raiders saves not noticed")
	}
	x = want(t, all, `Local\Wardogs`, VerdictLeftover, "Wardogs")
	if !x.Saves || !strings.Contains(x.Advice, "backup") {
		t.Errorf("a removed game's saves need a backup warning, got: %s", x.Advice)
	}
	want(t, all, `Roaming\OldToolX`, VerdictLeftover, "OldToolX")
	want(t, all, `Roaming\MysteryThing`, VerdictUnsure, "MysteryThing")
	want(t, all, `Local\PortableThing`, VerdictUnsure, "PortableThing")
	want(t, all, `Local\Temp`, VerdictWindows, "Windows")
	want(t, all, `Local\Microsoft\Windows`, VerdictWindows, "Windows")
	want(t, all, `Local\D3DSCache`, VerdictWindows, "Windows")
	want(t, all, `Local\Google\Chrome`, VerdictInUse, "Google Chrome")
	want(t, all, `Local\Google\Picasa2`, VerdictLeftover, "Picasa2")
	x = want(t, all, `LocalLow\Some Studio\Cool Game`, VerdictLeftover, "Cool Game")
	if !x.Saves {
		t.Error("Unity saves not noticed")
	}
	want(t, all, `Local\Packages\Microsoft.WindowsCalculator_8wekyb3d8bbwe`, VerdictInUse, "Windows Calculator")
	want(t, all, `Local\Packages\5319275A.WhatsAppDesktop_cv1g1gvanyjgm`, VerdictLeftover, "WhatsApp Desktop")
	want(t, all, `Roaming\GhostFolder`, VerdictLeftover, "GhostFolder")
	want(t, all, `Roaming\FreshEmpty`, VerdictUnsure, "FreshEmpty")
	x = want(t, all, `Roaming\Spotify`, VerdictLeftover, "Spotify")
	if !x.Identified {
		t.Error("Spotify should be named")
	}
	want(t, all, `Roaming\Blue Hen Software`, VerdictInUse, "Blue Hen Software")
	want(t, all, `Local\Apple Tree`, VerdictLeftover, "Apple Tree")
	want(t, all, `Local\Games`, VerdictLeftover, "Games")
	want(t, all, `Local\BadApp`, VerdictUnsure, "Bad App")
	want(t, all, `Roaming\Runner`, VerdictInUse, "Runner")
	want(t, all, `Local\(loose files)`, VerdictWindows, "Windows and apps")

	for rel, x := range all {
		if x.Why == "" || x.Advice == "" {
			t.Errorf("%s is missing an explanation", rel)
		}
		if strings.Contains(x.Why, "  ") || strings.Contains(x.Why, "0001") {
			t.Errorf("%s has odd wording: %s", rel, x.Why)
		}
		if len(x.Evidence) == 0 {
			t.Errorf("%s has no evidence", rel)
		}
	}
}

func TestScanChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.file(`Local\A\b\c.exe`, 100, 10)
	f.file(`Roaming\B\x.ini`, 100, 10)
	_ = os.MkdirAll(f.path(`Roaming\Empty`), 0o755)
	snap := func() string {
		var b strings.Builder
		_ = filepath.WalkDir(f.dir, func(p string, d fs.DirEntry, _ error) error {
			i, _ := d.Info()
			fmt.Fprintln(&b, p, i.Size(), i.ModTime().UnixNano(), i.Mode())
			return nil
		})
		return b.String()
	}
	before := snap()
	f.scan(&Inventory{})
	if snap() != before {
		t.Fatal("the scan changed something on disk")
	}
}

// The promise on the app's screen is that it can't delete or change what it scans.
// This fails the build if code that could do that is ever added.
func TestSourceHasNoWayToDelete(t *testing.T) {
	banned := regexp.MustCompile(`os\.Remove|os\.RemoveAll|os\.Rename|os\.Truncate|os\.Chmod|os\.Chtimes|DeleteFile|RemoveDirectory|MoveFile|SHFileOperation|RegDeleteKey|RegDeleteValue|RegSetValue|RegCreateKey|IFileOperation`)
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if name == "recycle_windows.go" {
			// The one way to remove something: the Recycle Bin, never a permanent delete.
			if !strings.Contains(string(data), "fFlags: fofAllowUndo") {
				t.Error("recycle_windows.go must always use the Recycle Bin")
			}
			data = []byte(strings.ReplaceAll(string(data), "SHFileOperation", ""))
		}
		if name == "demo.go" {
			// The demo build makes sample folders. It's only compiled with -tags demo.
			if !strings.HasPrefix(string(data), "//go:build demo\n") {
				t.Error("demo.go must only build with -tags demo")
			}
			data = []byte(strings.ReplaceAll(string(data), "os.Chtimes", ""))
		}
		if m := banned.Find(data); m != nil {
			t.Errorf("%s uses %s", name, m)
		}
		if name != "report.go" && name != "ui_windows.go" && name != "programs.go" && name != "demo.go" && strings.Contains(string(data), "os.WriteFile") {
			t.Errorf("%s writes a file", name)
		}
	}
}

func TestLinksArentFollowed(t *testing.T) {
	f := newFixture(t)
	f.file(`Local\Real\big.bin`, 5000, 10)
	if err := os.Symlink(f.path(`Local\Real`), f.path(`Local\Other`)); err != nil {
		t.Skip("no symlinks here")
	}
	_ = os.MkdirAll(f.path(`Local\Holder`), 0o755)
	if err := os.Symlink(f.path(`Local\Real`), f.path(`Local\Holder\link`)); err != nil {
		t.Skip()
	}
	all := f.scan(&Inventory{})
	if all[`Local\Other`] != nil {
		t.Error("a link got its own row")
	}
	if all[`Local\Holder`].Size != 0 {
		t.Errorf("link's contents were counted: %d", all[`Local\Holder`].Size)
	}
}

func TestStoppingEarly(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 30; i++ {
		f.file(fmt.Sprintf(`Local\F%d\x`, i), 10, 10)
	}
	var stop atomic.Bool
	stop.Store(true)
	if got := Scan(f.roots, &Inventory{}, &stop, nil, nil); len(got) != 0 {
		t.Errorf("a stopped scan labeled %d half-read folders", len(got))
	}
}

func TestMissingRootsDontBreak(t *testing.T) {
	var stop atomic.Bool
	got := Scan([]Root{{"Local", filepath.Join(t.TempDir(), "nope")}}, &Inventory{}, &stop, nil, nil)
	if len(got) != 0 {
		t.Error("expected no rows")
	}
}

func TestNameMatching(t *testing.T) {
	cases := []struct {
		folder, app string
		ok          bool
	}{
		{"EpicGamesLauncher", "Epic Games Launcher", true},
		{"obs-studio", "OBS Studio", true},
		{"Code", "Microsoft Visual Studio Code", true},
		{"vlc", "VLC media player", true},
		{"HandBrake", "HandBrake 1.8.2", true},
		{"Games", "Epic Games Launcher", false},
		{"Data", "Data Recovery Pro", false},
		{"Apple", "Pineapple Studio", false},
		{"Update", "Windows Update Assistant", false},
		{"Studio", "OBS Studio", false},
		{"curseforge-updater", "CurseForge 1.325.0-41594", true},
		{"Banana", "Banana Bugs [PopCap]", false},
		{"Astro", "AstroPop Deluxe [PopCap]", false},
		{"Goldberg SteamEmu Saves", "Steam", false},
		{"SmartSteamEmu", "Steam", false},
		{"Drill_Core", "Core", false},
		{"Voicemod.exe", "Voicemod", true},
		{"HogwartsLegacy", "Windows Media Player Legacy", false},
		{"Local", "Local Send", false},
	}
	for _, c := range cases {
		f := &Folder{Name: c.folder, Rel: `Local\` + c.folder, Path: "/x/" + c.folder}
		inv := &Inventory{Apps: []App{{Name: c.app, Source: sourceInstalled}}}
		if got := bestMatch(f, inv).score >= 60; got != c.ok {
			t.Errorf("%q vs %q: got %v, want %v", c.folder, c.app, got, c.ok)
		}
	}
}

func TestWordingIsReadable(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		days int
		want string
	}{{0, "today"}, {1, "yesterday"}, {5, "5 days ago"}, {90, "3 months ago"}, {800, "2 years ago"}, {31, "31 days ago"}, {365, "12 months ago"}} {
		if got := FormatAge(now.Add(-time.Duration(c.days)*24*time.Hour-time.Hour), now); got != c.want {
			t.Errorf("%d days: %s", c.days, got)
		}
	}
	if got := FormatSize(3 << 30); got != "3.0 GB" {
		t.Error(got)
	}
	if got := friendlyPackage("Microsoft.WindowsCalculator_8wekyb3d8bbwe"); got != "Windows Calculator" {
		t.Error(got)
	}
	if got := friendlyPackage("5319275A.WhatsAppDesktop_cv1g1gvanyjgm"); got != "WhatsApp Desktop" {
		t.Error(got)
	}
}

func TestMarksAndReport(t *testing.T) {
	dir := t.TempDir()
	if got := LoadMarks(dir); len(got) != 0 {
		t.Error("expected no marks")
	}
	f := &Folder{Name: "=cmd", Rel: `Local\=cmd`, Owner: "Unknown", Verdict: VerdictLeftover, Why: "x", Advice: "y", Size: 2048, Newest: time.Now()}
	m := Marks{markKey(f): {Right: false, Actual: "Old game"}}
	if err := SaveMarks(dir, m); err != nil {
		t.Fatal(err)
	}
	m2 := LoadMarks(dir)
	if m2[markKey(f)].Actual != "Old game" {
		t.Error("marks didn't load back")
	}
	if r, tot := MarkScore([]*Folder{f}, m2); r != 0 || tot != 1 {
		t.Error("score", r, tot)
	}
	report := string(ReportCSV([]*Folder{f}, m2, time.Now()))
	for _, want := range []string{"'=cmd", "Wrong", "Old game", "Likely leftover", "2048"} {
		if !strings.Contains(report, want) {
			t.Errorf("report is missing %s:\n%s", want, report)
		}
	}
	_ = os.WriteFile(marksPath(dir), []byte("not json"), 0o644)
	if len(LoadMarks(dir)) != 0 {
		t.Error("a damaged marks file should start fresh")
	}
}

// Each of these came from testing on a real PC.
func TestRealWorldMistakes(t *testing.T) {
	f := newFixture(t)
	f.file(`Local\Pal\Saved\SaveGames\world.sav`, 100, 120)
	f.file(`Local\GZW\Saved\Config\x.ini`, 100, 200)
	f.file(`Local\Google\Chrome\User Data\x`, 100, 1)
	f.file(`Local\Google\Chrome\Application\chrome_proxy.exe`, 100, 1)
	f.file(`Roaming\.minecraft\saves\w\level.dat`, 100, 150)
	f.file(`LocalLow\VRChat\VRChat\Tools\yt-dlp.exe`, 100, 330)
	f.file(`Local\BeamNG\data.bin`, 100, 95)
	f.file(`Local\rjCqvgXSUEaINq\node.exe`, 100, 120)
	f.file(`Local\OldGame\Saved\x.sav`, 100, 300)
	f.file(`Local\NVIDIA\DXCache\a.nvph`, 100, 0)
	f.file(`Local\Microsoft\Other\x`, 100, 120)
	f.file(`Local\Microsoft\NotOnAnyList\x`, 100, 120)
	f.file(`Roaming\NVIDIA\ComputeCache\x`, 100, 11)
	inv := &Inventory{Apps: []App{
		{Name: "Palworld", Source: sourceShortcut, Paths: []string{`E:\Games\Palworld`}, Aliases: []string{"Pal", "Palworld"}},
		{Name: "Gray Zone Warfare", Source: sourceSteam, Paths: []string{`E:\Steam\common\GZW`}, Aliases: []string{"GZW"}},
		{Name: "YouTube", Source: sourceInstalled, Paths: []string{f.path(`Local\Google\Chrome\Application`)}},
		{Name: "Google Chrome", Source: sourceInstalled, Paths: []string{`C:\Program Files\Google\Chrome`}},
		{Name: "Minecraft Dungeons2", Source: sourceInstalled},
	}, Gone: []App{{Name: "Old Game", Source: sourceRan, Aliases: []string{"OldGame"}}}}
	all := f.scan(inv)
	x := want(t, all, `Local\Pal`, VerdictInUse, "Palworld")
	if !x.Saves {
		t.Error("Palworld saves")
	}
	want(t, all, `Local\GZW`, VerdictInUse, "Gray Zone Warfare")
	want(t, all, `Local\Google\Chrome`, VerdictInUse, "Google Chrome")
	want(t, all, `Roaming\.minecraft`, VerdictUnsure, "Minecraft")
	x = want(t, all, `LocalLow\VRChat\VRChat`, VerdictLeftover, "VRChat")
	if !x.Guessed {
		t.Error("VRChat should be marked as a guess")
	}
	want(t, all, `Local\BeamNG`, VerdictUnsure, "BeamNG")
	want(t, all, `Local\rjCqvgXSUEaINq`, VerdictUnsure, "Unknown")
	x = want(t, all, `Local\OldGame`, VerdictLeftover, "Old Game")
	if x.Confidence != "High" {
		t.Error("a missing program, with a folder untouched for most of a year, should be high confidence")
	}
	if !strings.Contains(x.Why, "moved") {
		t.Error("it has to allow for the program having been moved: " + x.Why)
	}
	if x.SaveHint != `Saved\x.sav` {
		t.Error(x.SaveHint)
	}
	x = want(t, all, `Local\NVIDIA\DXCache`, VerdictWindows, "NVIDIA graphics driver")
	if !strings.Contains(x.Advice, "Shader Cache Size") || !strings.HasPrefix(x.Contains(), "Cache") {
		t.Errorf("DXCache: %s / %s", x.Advice, x.Contains())
	}
	want(t, all, `Local\Microsoft\Other`, VerdictWindows, "Microsoft")
	want(t, all, `Local\Microsoft\NotOnAnyList`, VerdictWindows, "Microsoft")
	want(t, all, `Roaming\NVIDIA\ComputeCache`, VerdictWindows, "NVIDIA graphics driver")
}

func TestSecondRoundOfRealWorldMistakes(t *testing.T) {
	f := newFixture(t)
	f.file(`Local\Starfield\a`, 100, 480)
	f.file(`Local\GZW\a`, 100, 13)
	f.file(`LocalLow\Nolla_Games_Noita\save00\a`, 100, 90)
	f.file(`LocalLow\Nolla_Games_Other\save00\a`, 100, 90)
	f.file(`Local\Pal\a`, 100, 90)
	f.file(`Roaming\Cemu\a`, 100, 500)
	inv := &Inventory{Apps: []App{
		{Name: "Gray Zone Warfare", Source: sourceSteam, Aliases: []string{"GZW"}},
		{Name: "GZW", Source: sourceRan},
		{Name: "Noita", Source: sourceStart},
		{Name: "Palworld", Source: sourceShortcut},
	}, Gone: []App{
		{Name: "RapidCRC Unicode", Source: sourceRan, Aliases: []string{"Starfield"}},
		{Name: "Pal", Source: sourceRan, Aliases: []string{"Pal"}},
		{Name: "Cemu_2.6", Source: sourceRan, Aliases: []string{"Cemu_2.6", "Cemu"}},
	}}
	all := f.scan(inv)
	want(t, all, `Local\Starfield`, VerdictLeftover, "Starfield")
	want(t, all, `Local\GZW`, VerdictInUse, "Gray Zone Warfare")
	want(t, all, `LocalLow\Nolla_Games_Noita\save00`, VerdictInUse, "Noita")
	x := want(t, all, `LocalLow\Nolla_Games_Other\save00`, VerdictUnsure, "Nolla Games Other")
	_ = x
	x = want(t, all, `Local\Pal`, VerdictUnsure, "Palworld")
	if !strings.Contains(x.Why, "conflicts") {
		t.Error(x.Why)
	}
	want(t, all, `Roaming\Cemu`, VerdictLeftover, "Cemu (Wii U emulator)")
}

func TestBrokenShortcutGivesTheName(t *testing.T) {
	f := newFixture(t)
	f.file(`Local\Pal\a`, 100, 90)
	all := f.scan(&Inventory{Gone: []App{
		{Name: "Pal", Source: sourceRan, Aliases: []string{"Pal"}},
		{Name: "Palworld", Source: sourceShortcut},
	}})
	want(t, all, `Local\Pal`, VerdictLeftover, "Palworld")
}

func TestCleanNames(t *testing.T) {
	for in, out := range map[string]string{"Ollama version 0.40.13": "Ollama", "CurseForge 1.325.0-41594": "CurseForge",
		"Kryptex 5.3.7": "Kryptex", "Panopto (64-bit)": "Panopto", "Banana Bugs [PopCap]": "Banana Bugs",
		"Microsoft Visual Studio Code (User)": "Microsoft Visual Studio Code", "Dungeons 2": "Dungeons 2", "Battlefield 6": "Battlefield 6"} {
		if got := cleanName(in); got != out {
			t.Errorf("%q: got %q", in, got)
		}
	}
	if !looksRandom("rjCqvgXSUEaINq") || looksRandom("FortniteGame") || looksRandom("Microsoft Edge") {
		t.Error("looksRandom")
	}
}

func TestParseLnk(t *testing.T) {
	// A minimal shortcut: header, no ID list, link info with an ANSI local path.
	d := make([]byte, 76)
	d[0] = 0x4C
	d[20] = 2 // has link info
	info := make([]byte, 28)
	info[4] = 28  // header size
	info[8] = 1   // has a local path
	info[16] = 28 // path offset
	d = append(d, info...)
	d = append(d, []byte(`E:\Games\Palworld\Palworld.exe`)...)
	d = append(d, 0)
	if got := parseLnk(d); got != `E:\Games\Palworld\Palworld.exe` {
		t.Errorf("got %q", got)
	}
	if parseLnk([]byte("not a shortcut")) != "" || parseLnk(d[:80]) != "" {
		t.Error("bad data should give nothing")
	}
}

func TestSaveHintHidesAccountIDs(t *testing.T) {
	if got := shortHint(filepath.Join("Saved", "SaveGames", "76561197960271872", "Level.sav")); strings.Contains(got, "7656") {
		t.Error(got)
	}
}

func TestNoOutsideData(t *testing.T) {
	banned := regexp.MustCompile(`(?i)ludusavi|winapp2|pcgamingwiki|lists\.json`)
	files, _ := filepath.Glob("*")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "rsrc_windows_amd64.syso" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		if banned.Match(data) || banned.MatchString(name) {
			t.Errorf("%s refers to outside data", name)
		}
	}
}

func steamFile(games []steamGame) []byte {
	// Builds a small appinfo.vdf in Steam's newer format, for testing the reader.
	var table []string
	idx := func(s string) []byte {
		for i, v := range table {
			if v == s {
				return binary.LittleEndian.AppendUint32(nil, uint32(i))
			}
		}
		table = append(table, s)
		return binary.LittleEndian.AppendUint32(nil, uint32(len(table)-1))
	}
	str := func(k, v string) []byte {
		return append(append(append([]byte{1}, idx(k)...), v...), 0)
	}
	sec := func(k string, parts ...[]byte) []byte {
		out := append([]byte{0}, idx(k)...)
		for _, p := range parts {
			out = append(out, p...)
		}
		return append(out, 8)
	}
	var apps []byte
	for i, g := range games {
		var launches [][]byte
		for j, e := range g.Exes {
			launches = append(launches, sec(fmt.Sprint(j), str("executable", e)))
		}
		kv := sec("appinfo", sec("common", str("name", g.Name), str("type", "Game")),
			sec("config", str("installdir", g.Dir), sec("launch", launches...)))
		kv = append(kv, 8)
		head := make([]byte, 4+4+8+20+4+20)
		apps = binary.LittleEndian.AppendUint32(apps, uint32(i+10))
		apps = binary.LittleEndian.AppendUint32(apps, uint32(len(head)+len(kv)))
		apps = append(apps, head...)
		apps = append(apps, kv...)
	}
	apps = binary.LittleEndian.AppendUint32(apps, 0)
	out := binary.LittleEndian.AppendUint32(nil, 0x07564429)
	out = binary.LittleEndian.AppendUint32(out, 1)
	out = binary.LittleEndian.AppendUint64(out, uint64(16+len(apps)))
	out = append(out, apps...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(table)))
	for _, s := range table {
		out = append(append(out, s...), 0)
	}
	return out
}

func TestSteamDataNamesCodenames(t *testing.T) {
	parsed := parseSteamAppInfo(steamFile([]steamGame{
		{Name: "Cool Title", Dir: "Cool Title", Exes: []string{`CoolProject\Binaries\Win64\CoolProject-Win64-Shipping.exe`}},
		{Name: "Game One", Dir: "One", Exes: []string{"Shared.exe"}},
		{Name: "Game Two", Dir: "Two", Exes: []string{"Shared.exe"}},
	}))
	if len(parsed) != 3 || parsed[0].Name != "Cool Title" || parsed[0].Dir != "Cool Title" || len(parsed[0].Exes) != 1 {
		t.Fatalf("%+v", parsed)
	}
	if parseSteamAppInfo([]byte("nonsense that is not steam data")) != nil {
		t.Error("bad data should give nothing")
	}
	f := newFixture(t)
	f.file(`Local\CoolProject\Saved\Config\a.ini`, 10, 300)
	f.file(`Roaming\CoolProject2\a`, 10, 300)
	f.file(`Local\Shared\Saved\a`, 10, 300)
	f.file(`Local\One\settings.txt`, 10, 300) // same name as a game, but nothing game-like inside
	all := f.scan(&Inventory{Steam: buildSteamIndex(parsed)})
	x := want(t, all, `Local\CoolProject`, VerdictLeftover, "Cool Title")
	if !x.Identified {
		t.Error("should count as evidence")
	}
	want(t, all, `Local\Shared`, VerdictLeftover, "Shared")
	want(t, all, `Local\One`, VerdictLeftover, "One")
}

func TestPrivateFilesAreNeverOpened(t *testing.T) {
	for _, path := range []string{"user data/default/preferences.json", "cookies.json", "login.ini", "myapp/token.json", "auth/config.json",
		"session.xml", "accounts.json", "wallet/settings.ini", "network/state.json", "licenses.xml"} {
		parts := strings.Split(path, "/")
		if k := clueKind(parts, parts[len(parts)-1], 100); k != "" {
			t.Errorf("%s would be opened as %s", path, k)
		}
	}
	if clueKind([]string{"settings.ini"}, "settings.ini", 1<<30) != "" {
		t.Error("huge settings files shouldn't be read")
	}
	for path, want := range map[string]string{"player.log": clueLog, "saved/logs/game.log": clueLog, "saved/crashes/x/crashcontext.runtime-xml": clueCrash, "config.ini": clueSettings, "run game.lnk": clueShortcut} {
		parts := strings.Split(path, "/")
		if got := clueKind(parts, parts[len(parts)-1], 100); got != want {
			t.Errorf("%s: got %q, want %q", path, got, want)
		}
	}
}

func TestFindingProgramPaths(t *testing.T) {
	f := newFixture(t)
	f.file(`Local\Coolgame\a`, 1, 1)
	write := func(name, body string) string {
		p := f.path(`Local\Coolgame\` + name)
		_ = os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	folder := &Folder{Name: "Coolgame"}
	// A settings file mentioning something unrelated once isn't enough.
	stray := write("a.json", `{"last":"D:\\\\Photos\\\\Editor\\\\editor.exe"}`)
	if d, _ := findProgramPath(folder, []clueFile{{stray, clueSettings}}); d != "" {
		t.Error("stray path accepted: " + d)
	}
	// A related one is, with JSON's doubled backslashes handled.
	related := write("b.json", `{"exe":"D:\\\\Games\\\\Coolgame\\\\coolgame.exe"}`)
	if d, k := findProgramPath(folder, []clueFile{{related, clueSettings}}); d != `D:\Games\Coolgame` || k != clueSettings {
		t.Errorf("%q %q", d, k)
	}
	// So is an unrelated one seen in two files.
	two := write("c.ini", `path=D:\Photos\Editor\editor.exe`)
	if d, _ := findProgramPath(folder, []clueFile{{stray, clueSettings}, {two, clueSettings}}); d != `D:\Photos\Editor` {
		t.Error("two files: " + d)
	}
	// A log wins over settings, and text saved as UTF-16 still reads.
	log := write("x.log", "M\x00o\x00n\x00o\x00 \x00'\x00E\x00:\x00/\x00G\x00/\x00X\x00_\x00D\x00a\x00t\x00a\x00/\x00")
	if d, k := findProgramPath(folder, []clueFile{{related, clueSettings}, {log, clueLog}}); d != `E:\G` || k != clueLog {
		t.Errorf("%q %q", d, k)
	}
	// Shared places like the Steam program itself say nothing.
	if got := pathsInText(`C:\Program Files (x86)\Steam\steam.exe`); len(got) != 0 {
		t.Errorf("%v", got)
	}
	if got := pathsInText(`E:\SteamLibrary\steamapps\common\THE FINALS\Discovery\Content\Paks`); len(got) != 1 || got[0] != `E:\SteamLibrary\steamapps\common\THE FINALS` {
		t.Errorf("%v", got)
	}
}

func TestLogPaths(t *testing.T) {
	for in, out := range map[string]string{
		"Mono path[0] = 'E:/Games/Moon Sweepers/MoonSweepers_Data/Managed'":               `E:\Games\Moon Sweepers`,
		"LogInit: Base Directory: F:/SteamLibrary/common/Sat/FactoryGame/Binaries/Win64/": `F:\SteamLibrary\common\Sat`,
		`Command line: "D:\Tools\Thing\thing.exe" -x`:                                     `D:\Tools\Thing`,
		`C:/Windows/System32/ntdll.dll C:/Users/x/AppData/Local/a.exe`:                    "",
		"no paths here": "",
	} {
		if got := installDirFromLog(in); got != out {
			t.Errorf("%q: got %q, want %q", in, got, out)
		}
	}
}

func TestPortableGameInADesktopFolder(t *testing.T) {
	f := newFixture(t)
	f.file(`LocalLow\DragDropGames\MoonSweepers\Player.log`, 10, 2)
	f.file(`LocalLow\OtherCo\Thing\x`, 10, 300)
	f.file(`Desktop\Moon Sweepers\MoonSweepers.exe`, 10, 5)
	f.file(`Desktop\Moon Sweepers\MoonSweepers_Data\app.info`, 0, 5)
	_ = os.WriteFile(f.path(`Desktop\Moon Sweepers\MoonSweepers_Data\app.info`), []byte("DragDropGames\nMoonSweepers\n"), 0o644)
	f.file(`Desktop\setup-thing.exe`, 10, 5)
	f.file(`Desktop\Notes\a.txt`, 10, 5)
	found := searchPrograms([]string{f.path(`Desktop`)}, 4, nil, nil)
	if len(found) != 1 || found[0].Unity != `DragDropGames\MoonSweepers` {
		t.Fatalf("%+v", found)
	}
	inv := &Inventory{}
	addProgramsToInventory(inv, found, nil)
	all := f.scan(inv)
	x := want(t, all, `LocalLow\DragDropGames\MoonSweepers`, VerdictInUse, "MoonSweepers")
	if !strings.Contains(x.Why, "Moon Sweepers") {
		t.Error(x.Why)
	}
}

func TestNewAndRemovedPortablePrograms(t *testing.T) {
	f := newFixture(t)
	f.file(`Games\Old\old.exe`, 10, 5)
	f.file(`Games\Unplugged\u.exe`, 10, 5)
	found := searchPrograms([]string{f.path(`Games`)}, 3, nil, nil)
	if len(found) != 2 {
		t.Fatalf("%+v", found)
	}
	// A new game turns up beside a known one, without another full search.
	f.file(`Games\New\new.exe`, 10, 1)
	roots := quickRoots(nil, found)
	if len(roots) != 1 || roots[0] != f.path(`Games`) {
		t.Fatalf("%v", roots)
	}
	_ = os.RemoveAll(f.path(`Games\Old`))
	fresh := searchPrograms(roots, 3, nil, nil)
	old := []ProgramDir{{Path: f.path(`Games\Old`), Name: "Old"}, {Path: `Z:\Gone\Drive`, Name: "OnUnplugged"}}
	kept, gone := mergePrograms(old, fresh, exists, func(p string) bool { return !strings.HasPrefix(p, `Z:`) })
	names := map[string]bool{}
	for _, k := range kept {
		names[k.Name] = true
	}
	if !names["New"] || !names["OnUnplugged"] || names["Old"] {
		t.Errorf("kept: %+v", kept)
	}
	if len(gone) != 1 || gone[0].Name != "Old" {
		t.Errorf("gone: %+v", gone)
	}
}

func TestNamesFromPaths(t *testing.T) {
	for in, out := range map[string]string{
		"/x/Desktop/BastionOS/game":         "BastionOS",
		"/x/Launcher/Engine/Binaries/Win64": "Launcher",
		"/x/Melvor.Idle.v1.3.1/game":        "Melvor.Idle",
		"/x/Peglin":                         "Peglin",
	} {
		if got := nameFromPath(in); got != out {
			t.Errorf("%s: got %q, want %q", in, got, out)
		}
	}
}

func TestAShortcutIsNotProofOfAnInstall(t *testing.T) {
	f := newFixture(t)
	f.file(`Local\FactoryGame\Saved\a`, 10, 300)
	all := f.scan(&Inventory{Apps: []App{{Name: "Satisfactory", Source: sourceStart}}})
	x := want(t, all, `Local\FactoryGame`, VerdictInUse, "Satisfactory")
	if x.Confidence != "Medium" || !strings.Contains(x.Why, "shortcut") {
		t.Errorf("%s: %s", x.Confidence, x.Why)
	}
}

func TestOwnerNamesReadWell(t *testing.T) {
	t.Setenv("USERNAME", "alex")
	cases := []struct{ folder, owner, want string }{
		{"Roblox", "Roblox Player for alex", "Roblox Player"},
		{"Kryptex", "kryptex_rigel", "Kryptex"},
		{"HallsOfTorment", "game", "Halls Of Torment"},
		{"LostSkies", "LS", "LostSkies"},
		{"TerraTech", "TerraTechLFsrk", "TerraTech"},
		{"Battle.net", "Battle.net", "Battle.net"},
		{"EA", "EA", "EA"},
		{"BeamNG", "BeamNG", "BeamNG"},
		{"Whatever", "Slots.and.Daggers", "Slots and Daggers"},
		{"Whatever", "tm_™_app", "Tm ™ App"},
		{"Whatever", "émile_game", "Émile Game"},
	}
	for _, c := range cases {
		f := &Folder{Name: c.folder, Rel: `Local\` + c.folder, Owner: c.owner, Why: "You used to run " + c.owner + "."}
		tidyOwner(f)
		if f.Owner != c.want {
			t.Errorf("%s/%s: got %q, want %q", c.folder, c.owner, f.Owner, c.want)
		}
	}
	for _, c := range cases {
		if !utf8.ValidString(readableName(c.owner)) {
			t.Errorf("%q came out as broken text", c.owner)
		}
	}
	if got := cleanName("skate.™"); got != "skate" {
		t.Errorf("got %q", got)
	}
	if got := cleanName("Battlefield™ 6"); got != "Battlefield 6" {
		t.Errorf("got %q", got)
	}
	if got := baseFolderName("gg.hydralauncher.installer"); got != "hydralauncher" {
		t.Errorf("got %q", got)
	}
	if got := baseFolderName("DayZLauncher.exe_Url_qaaevecr3ijx3rk"); got != "DayZLauncher" {
		t.Errorf("got %q", got)
	}
	if got := baseFolderName("Ascendara by tagoWorks"); got != "Ascendara" {
		t.Errorf("got %q", got)
	}
}

func TestLogsShowWhoAFolderWorksFor(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "velopack_Medal.log")
	text := `Running C:\\Users\\me\\AppData\\Local\\Medal\\Update.exe
start C:\Users\me\AppData\Local\Medal\current\Medal.exe (1)
tool C:\Users\me\AppData\Local\Temp\x\a.exe
tool C:\Users\me\AppData\Local\Temp\x\a.exe
once C:\Users\me\AppData\Local\Other\b.exe`
	if err := os.WriteFile(log, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &Folder{Name: "velopack", Rel: `Local\velopack`, Path: dir}
	got := programsInLogs(f, []clueFile{{log, clueLog}})
	if len(got) != 1 || got[0].Name != "Medal" || got[0].Path != `C:\Users\me\AppData\Local\Medal` {
		t.Fatalf("got %+v", got)
	}
	// With the program installed, the folder is in use and named for it.
	f.UsedBy, f.Files, f.Newest = got, 2, time.Now().Add(-90*24*time.Hour)
	inv := &Inventory{Apps: []App{{Name: "Medal", Source: sourceInstalled}}}
	Classify(f, inv, time.Now())
	if f.Owner != "Medal" || f.Verdict != VerdictInUse {
		t.Errorf("installed: got %q %v: %s", f.Owner, f.Verdict, f.Why)
	}
	// Without it, an old folder is a leftover.
	f2 := &Folder{Name: "velopack", Rel: `Local\velopack`, Path: dir, UsedBy: got, Files: 2, Newest: f.Newest}
	Classify(f2, &Inventory{}, time.Now())
	if f2.Owner != "Medal" || f2.Verdict != VerdictLeftover {
		t.Errorf("removed: got %q %v: %s", f2.Owner, f2.Verdict, f2.Why)
	}
}

func TestPublisherFoldersListTheGamesInside(t *testing.T) {
	root := t.TempDir()
	mk := func(parts ...string) {
		if err := os.MkdirAll(filepath.Join(append([]string{root}, parts...)...), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("2K Games", "Mafia III", "Saves")
	mk("Saber", "WWZ")
	mk("Tiny Forge Studios", "Lantern")      // not on any list, but laid out like a publisher
	mk("Cool Software", "logs")              // an app with its data, not a publisher
	mk("Godot", "app_userdata", "Lost Road") // games sit one level further down
	mk("GameAnalytics", "abc")               // a shared tool, not a publisher
	got := map[string]bool{}
	for _, f := range listTargets([]Root{{Path: root, Label: "Local"}}) {
		got[f.Rel] = true
	}
	for _, want := range []string{`Local\2K Games\Mafia III`, `Local\Saber\WWZ`, `Local\Tiny Forge Studios\Lantern`,
		`Local\Cool Software`, `Local\Godot\app_userdata\Lost Road`, `Local\GameAnalytics`} {
		if !got[want] {
			t.Errorf("missing %s; have %v", want, got)
		}
	}
	if got[`Local\2K Games`] {
		t.Error("2K Games should show its games, not itself")
	}
	f := &Folder{Name: "Y504D", Rel: `Local\Saber\Y504D`, Path: filepath.Join(root, "Saber", "Y504D"), Files: 1, Newest: time.Now()}
	Classify(f, &Inventory{}, time.Now())
	if f.Owner != "Y504D (Saber)" {
		t.Errorf("got %q", f.Owner)
	}
	log := &Folder{Name: "log", Rel: `Local\Pearl Abyss\log`, Path: filepath.Join(root, "x"), Files: 1, Newest: time.Now()}
	inv := &Inventory{Apps: []App{{Name: "Eets", Source: sourceDisk, Aliases: []string{"log"}, Paths: []string{`D:\Eets`}}}}
	Classify(log, inv, time.Now())
	if strings.Contains(log.Owner, "Eets") {
		t.Errorf("a folder called log can't name a program: %q", log.Owner)
	}
}

func TestProgramFilesCrossReference(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, files ...string) string {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	medal := mk(`Medal`, "Medal.exe")
	mk(`OldTool`, "settings.ini")
	mk(`Common Files`, "x.dll")
	mk(`Portable`, "portable.exe")
	old := time.Now().Add(-400 * 24 * time.Hour)
	for _, d := range []string{"OldTool", "Portable"} {
		_ = os.Chtimes(filepath.Join(root, d, map[string]string{"OldTool": "settings.ini", "Portable": "portable.exe"}[d]), old, old)
	}
	inv := &Inventory{Apps: []App{{Name: "Medal", Source: sourceInstalled, Paths: []string{medal}}}}
	progs := ScanProgramFiles([]Root{{"Program Files", root}}, inv, nil, nil, nil)
	byName := map[string]*Folder{}
	for _, f := range progs {
		byName[f.Name] = f
	}
	want := map[string]Verdict{"Medal": VerdictInUse, "OldTool": VerdictLeftover, "Common Files": VerdictWindows, "Portable": VerdictLeftover}
	for n, v := range want {
		if f := byName[n]; f == nil || f.Verdict != v {
			t.Errorf("%s: got %+v, want %v", n, f, v)
		}
	}
	// An AppData folder for Medal links to its program folder, and fresh AppData
	// data rescues a program Windows doesn't list.
	appMedal := &Folder{Name: "Medal", Rel: `Local\Medal`, Owner: "Medal", Identified: true, Verdict: VerdictInUse, app: &inv.Apps[0], Newest: time.Now()}
	appPortable := &Folder{Name: "Portable", Rel: `Roaming\Portable`, Owner: "Portable", Verdict: VerdictUnsure, Newest: time.Now()}
	CrossReference([]*Folder{appMedal, appPortable}, progs, time.Now())
	if len(appMedal.Related) != 1 || appMedal.Related[0] != byName["Medal"] {
		t.Errorf("Medal not linked: %v", appMedal.Related)
	}
	if byName["Portable"].Verdict != VerdictInUse {
		t.Errorf("Portable should be in use now: %s", byName["Portable"].Why)
	}
	if !strings.Contains(RelatedText(byName["Medal"], time.Now()), `Local\Medal`) {
		t.Errorf("got %q", RelatedText(byName["Medal"], time.Now()))
	}
}

func TestShortNamesFromInitials(t *testing.T) {
	yes := [][2]string{{"WSL", "Windows Subsystem for Linux"}, {"OBS", "Open Broadcaster Software"}, {"VLC", "VLC media player"}}
	for _, c := range yes[:2] {
		if !isInitials(c[0], c[1]) {
			t.Errorf("%s should match %s", c[0], c[1])
		}
	}
	for _, c := range [][2]string{{"Steam", "Some Thing Else Around Me"}, {"W", "Windows"}, {"AB", "Apple Banana Cherry"}} {
		if isInitials(c[0], c[1]) {
			t.Errorf("%s shouldn't match %s", c[0], c[1])
		}
	}
	f := &Folder{Name: "WSL", Product: "Windows Subsystem for Linux"}
	if got := usableProduct(f); got != "Windows Subsystem for Linux" {
		t.Errorf("got %q", got)
	}
}
