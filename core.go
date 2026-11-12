// FolderTrace explains what each folder in AppData belongs to. Scanning never
// deletes, moves, or changes anything. The only removal is to the Recycle Bin, for
// folders the person checks and confirms.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf16"
)

type Verdict int

const (
	VerdictInUse Verdict = iota
	VerdictWindows
	VerdictLeftover
	VerdictUnsure
)

func (v Verdict) String() string {
	switch v {
	case VerdictInUse:
		return "In use"
	case VerdictWindows:
		return "Windows or shared"
	case VerdictLeftover:
		return "Likely leftover"
	}
	return "Not sure"
}

// App is something the PC says is installed, or that a launcher knows about.
type App struct {
	Name      string
	Publisher string
	Source    string   // shown to the person, such as "your installed programs list"
	Paths     []string // folders the app is installed in
	Aliases   []string // other names, such as folders inside a game's install folder
	Broken    bool     // Windows lists it, but its install folder is gone
	Unity     string   // lowercase Company\\Product, for an exact LocalLow match
}

type Process struct{ Name, Path string }

// Inventory is everything known about what's on this PC.
type Inventory struct {
	Apps     []App
	Gone     []App // programs Windows remembers, whose files are no longer there
	Steam    *SteamIndex
	Running  []Process
	Warnings []string
}

// Root is one of the AppData folders.
type Root struct{ Label, Path string }

type Folder struct {
	Path       string
	Root       string
	Rel        string // such as Local\Google\Chrome
	Name       string
	Loose      bool // the files sitting directly in a container, not a real subfolder
	Owner      string
	Identified bool
	Guessed    bool // Owner is only the folder's own name
	Verdict    Verdict
	Confidence string
	Why        string
	Advice     string
	Evidence   []string
	Size       int64
	Files      int
	Newest     time.Time
	Unreadable int
	Saves      bool
	SaveHint   string
	Profile    bool
	CacheBytes int64
	ProgBytes  int64
	ConfBytes  int64
	Exes       []string
	clueFiles  []clueFile
	GameShaped bool     // has Unreal's Saved folder or Unity's Player.log
	RanFrom    string   // where a file in this folder says the program ran from
	UsedBy     []logApp // programs in AppData that this folder's logs show it working for
	RanSource  string   // what kind of file said so
	Product    string
	Company    string
	kb         *kbEntry
	app        *App      // what Classify matched it to, if anything
	Related    []*Folder // folders elsewhere that belong to the same program
	Program    bool      // a Program Files folder, not an AppData one
}

// Contains is a short summary of what's inside, biggest first.
func (f *Folder) Contains() string {
	if f.Files == 0 {
		if f.Unreadable > 0 {
			return "Couldn't read"
		}
		return "Empty"
	}
	type part struct {
		name string
		n    int64
	}
	parts := []part{{"cache and logs", f.CacheBytes}, {"program files", f.ProgBytes}, {"settings", f.ConfBytes}}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].n > parts[j].n })
	var out []string
	if f.Saves {
		out = append(out, "game saves")
	}
	if f.Profile {
		out = append(out, "profile data")
	}
	for _, p := range parts {
		if p.n > 0 {
			out = append(out, p.name)
		}
	}
	if len(out) == 0 {
		out = append(out, "app data")
	}
	if len(out) > 2 {
		out = out[:2]
	}
	out[0] = strings.ToUpper(out[0][:1]) + out[0][1:]
	return strings.Join(out, ", ")
}

// ---- Finding the folders to report on ----

var containerNames = map[string]bool{
	"microsoft": true, "packages": true, "programs": true, "google": true, "adobe": true, "apple": true,
	"applecomputer": true, "jetbrains": true, "mozilla": true, "nvidia": true, "nvidiacorporation": true,
	"intel": true, "amd": true, "larianstudios": true, "cdprojektred": true, "electronicarts": true,
}

func isLink(d fs.DirEntry) bool {
	t := d.Type()
	return t&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

func subDirs(path string) (dirs []fs.DirEntry, files []fs.DirEntry) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if isLink(e) {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	return dirs, files
}

// listTargets decides which folders get a row. Folders like Microsoft or Google are
// only containers, so what matters is what's inside them. LocalLow is usually
// Company\Game, so every folder there is treated the same way.
func listTargets(roots []Root) []*Folder {
	var out []*Folder
	for _, root := range roots {
		dirs, files := subDirs(root.Path)
		var looseSize int64
		for _, f := range files {
			if i, err := f.Info(); err == nil {
				looseSize += i.Size()
			}
		}
		if looseSize >= 1<<20 {
			out = append(out, &Folder{Path: root.Path, Root: root.Label, Rel: root.Label + `\(loose files)`, Name: "(loose files)", Loose: true})
		}
		for _, d := range dirs {
			name := d.Name()
			path := filepath.Join(root.Path, name)
			rel := root.Label + `\` + name
			if containerNames[norm(name)] || strings.EqualFold(root.Label, "LocalLow") || isPublisherFolder(name, path) {
				kids, kidFiles := subDirs(path)
				if len(kids) > 0 {
					for _, k := range kids {
						// Godot keeps each game one level further down, in app_userdata.
						if strings.EqualFold(k.Name(), "app_userdata") {
							if games, _ := subDirs(filepath.Join(path, k.Name())); len(games) > 0 {
								for _, g := range games {
									out = append(out, &Folder{Path: filepath.Join(path, k.Name(), g.Name()), Root: root.Label,
										Rel: rel + `\` + k.Name() + `\` + g.Name(), Name: g.Name()})
								}
								continue
							}
						}
						out = append(out, &Folder{Path: filepath.Join(path, k.Name()), Root: root.Label,
							Rel: rel + `\` + k.Name(), Name: k.Name()})
					}
					var size int64
					for _, f := range kidFiles {
						if i, err := f.Info(); err == nil {
							size += i.Size()
						}
					}
					if size >= 1<<20 {
						out = append(out, &Folder{Path: path, Root: root.Label, Rel: rel + `\(loose files)`, Name: "(loose files)", Loose: true})
					}
					continue
				}
			}
			out = append(out, &Folder{Path: path, Root: root.Label, Rel: rel, Name: name})
		}
	}
	return out
}

// Words that mark a folder as a company's, with its games one level down.
var publisherWords = map[string]bool{"games": true, "entertainment": true, "interactive": true, "studio": true,
	"studios": true, "software": true, "publishing": true}

// Listed as groups, but what's inside them isn't one game per folder.
var notPublisher = map[string]bool{"defaultcompany": true, "myproject": true, "unity": true, "unitytechnologies": true,
	"gameanalytics": true, "unisdk": true}

func publisherName(name string) bool {
	n := norm(name)
	if e, ok := knownFolders[n]; ok {
		return e.Group && !notPublisher[n]
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if publisherWords[w] {
			return true
		}
	}
	return false
}

// isPublisherFolder finds folders like Local\2K Games, where the useful answer is the
// game inside. A folder only counts if it's laid out that way: other folders and no
// files of its own, none of them named like an app's data.
func isPublisherFolder(name, path string) bool {
	if !publisherName(name) {
		return false
	}
	if e, ok := knownFolders[norm(name)]; ok && e.Group {
		return true
	}
	kids, files := subDirs(path)
	if len(kids) == 0 || len(kids) > 25 || len(files) > 0 {
		return false
	}
	for _, k := range kids {
		if cacheDirs[strings.ToLower(k.Name())] || dataFolderName.MatchString(strings.ReplaceAll(k.Name(), "_", " ")) {
			return false
		}
	}
	return true
}

func parentName(f *Folder) string {
	parts := strings.Split(f.Rel, `\`)
	if len(parts) >= 3 {
		return parts[len(parts)-2]
	}
	return ""
}

// ---- Measuring what's inside ----

var cacheDirs = map[string]bool{"cache": true, "caches": true, "code cache": true, "gpucache": true, "dawncache": true,
	"grshadercache": true, "shadercache": true, "shader cache": true, "cacheddata": true, "cachedextensions": true,
	"temp": true, "tmp": true, "crashdumps": true, "crash reports": true, "crashes": true, "logs": true, "log": true, "webcache": true}
var saveDirs = map[string]bool{"save": true, "saves": true, "savegame": true, "savegames": true, "savedgames": true,
	"saved games": true, "savedata": true, "save data": true, "profiles_saves": true, "autosaves": true, "worlds": true}
var saveExts = map[string]bool{".sav": true, ".save": true, ".savegame": true, ".sl2": true, ".es3": true, ".ess": true,
	".rpgsave": true, ".rvdata2": true, ".gamesave": true}
var confExts = map[string]bool{".ini": true, ".cfg": true, ".conf": true, ".toml": true, ".yaml": true, ".yml": true, ".reg": true}
var confDirs = map[string]bool{"config": true, "configs": true, "settings": true, "preferences": true, "prefs": true}
var progExts = map[string]bool{".exe": true, ".dll": true, ".pak": true, ".asar": true, ".msi": true, ".sys": true}
var profileNames = map[string]bool{"user data": true, "login data": true, "logins.json": true, "places.sqlite": true, "key4.db": true}
var skipExes = []string{"unins", "uninstall", "update", "squirrel", "setup", "crashhandler", "crashpad", "helper", "elevate", "vc_redist", "dxsetup"}

// shortHint keeps the start of a path. Deeper parts of save paths often hold account IDs,
// which don't belong on the screen or in a report.
func shortHint(rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 2 {
		return parts[0] + `\` + parts[1] + `\...`
	}
	return strings.Join(parts, `\`)
}

type exeFile struct {
	path string
	size int64
}

// measure reads file sizes and dates only. It doesn't open files, except to read an
// .exe's version details later.
func measure(f *Folder, stop *atomic.Bool) {
	var exes []exeFile
	var dirMod time.Time
	if i, err := os.Stat(f.Path); err == nil {
		dirMod = i.ModTime()
	}
	add := func(rel string, info fs.FileInfo) {
		size := info.Size()
		f.Size += size
		f.Files++
		if m := info.ModTime(); m.After(f.Newest) && m.Before(time.Now().Add(48*time.Hour)) {
			f.Newest = m
		}
		lower := strings.ToLower(rel)
		parts := strings.Split(filepath.ToSlash(lower), "/")
		base := parts[len(parts)-1]
		ext := filepath.Ext(base)
		isCache, isSave, isConf := false, false, false
		for _, p := range parts[:len(parts)-1] {
			isCache = isCache || cacheDirs[p]
			isSave = isSave || saveDirs[p]
			isConf = isConf || confDirs[p]
			if profileNames[p] {
				f.Profile = true
			}
		}
		if profileNames[base] {
			f.Profile = true
		}
		if isSave || saveExts[ext] {
			f.Saves = true
			if f.SaveHint == "" {
				f.SaveHint = shortHint(rel)
			}
		}
		switch {
		case isCache || ext == ".log" || ext == ".tmp" || ext == ".dmp":
			f.CacheBytes += size
		case progExts[ext]:
			f.ProgBytes += size
		case isConf || confExts[ext]:
			f.ConfBytes += size
		}
		if base == "player.log" || (len(parts) > 1 && parts[0] == "saved") {
			f.GameShaped = true
		}
		if k := clueKind(parts, base, size); k != "" && countClues(f.clueFiles, k) < clueLimit[k] {
			f.clueFiles = append(f.clueFiles, clueFile{filepath.Join(f.Path, rel), k})
		}
		if ext == ".exe" {
			skip := false
			for _, s := range skipExes {
				skip = skip || strings.Contains(base, s)
			}
			if !skip {
				exes = append(exes, exeFile{filepath.Join(f.Path, rel), size})
			}
		}
	}
	if f.Loose {
		_, files := subDirs(f.Path)
		for _, e := range files {
			if i, err := e.Info(); err == nil {
				add(e.Name(), i)
			} else {
				f.Unreadable++
			}
		}
	} else {
		_ = filepath.WalkDir(f.Path, func(path string, d fs.DirEntry, err error) error {
			if stop != nil && stop.Load() {
				return filepath.SkipAll
			}
			if err != nil {
				f.Unreadable++
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if isLink(d) { // don't follow links, or files get counted twice
				return nil
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				f.Unreadable++
				return nil
			}
			rel, _ := filepath.Rel(f.Path, path)
			add(rel, info)
			return nil
		})
	}
	if f.Newest.IsZero() {
		f.Newest = dirMod
	}
	f.RanFrom, f.RanSource = findProgramPath(f, f.clueFiles)
	f.UsedBy = programsInLogs(f, f.clueFiles)
	f.clueFiles = nil
	sort.Slice(exes, func(i, j int) bool { return exes[i].size > exes[j].size })
	for i := 0; i < len(exes) && i < 3; i++ {
		f.Exes = append(f.Exes, exes[i].path)
	}
}

// ---- Matching names ----

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// noise words don't help identify anything and are ignored entirely.
var noiseWords = map[string]bool{"inc": true, "llc": true, "ltd": true, "gmbh": true, "corp": true, "corporation": true,
	"co": true, "com": true, "net": true, "org": true, "the": true, "x64": true, "x86": true, "64": true, "32": true, "bit": true,
	"amd64": true, "arm64": true, "version": true, "for": true, "and": true, "of": true, "exe": true}

// genericWords are real parts of a name, but a match on them alone means nothing.
var genericWords = map[string]bool{"game": true, "games": true, "data": true, "app": true, "apps": true, "update": true, "updater": true,
	"software": true, "tool": true, "tools": true, "common": true, "cache": true, "local": true, "temp": true, "windows": true,
	"microsoft": true, "system": true, "file": true, "files": true, "program": true, "programs": true, "client": true,
	"launcher": true, "service": true, "services": true, "user": true, "users": true, "config": true, "studio": true, "studios": true,
	"desktop": true, "player": true, "center": true, "centre": true, "helper": true, "runtime": true, "driver": true, "drivers": true,
	"manager": true, "online": true, "pro": true, "free": true, "edition": true, "install": true, "installer": true, "setup": true,
	"support": true, "web": true, "view": true, "media": true, "video": true, "audio": true, "network": true, "security": true,
	"plugin": true, "plugins": true, "crash": true, "logs": true, "entertainment": true, "interactive": true, "technologies": true,
	"labs": true, "team": true, "group": true, "company": true, "international": true, "browser": true, "code": false}

func tokens(s string) []string {
	var cur []rune
	var out []string
	flush := func() {
		if len(cur) > 0 {
			t := strings.ToLower(string(cur))
			allDigit := true
			for _, r := range t {
				allDigit = allDigit && unicode.IsDigit(r)
			}
			if !allDigit && !noiseWords[t] {
				out = append(out, t)
			}
			cur = nil
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(rs[i-1]) {
			flush() // EpicGames -> epic games
		}
		if i > 0 && unicode.IsUpper(r) && unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1]) {
			flush() // NVIDIACorp -> nvidia corp
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

func distinctive(words []string) []string {
	var out []string
	for _, t := range words {
		if !genericWords[t] {
			out = append(out, t)
		}
	}
	return out
}

// partOfName reports whether a folder's name is really part of an app's name. Generic
// words like "updater" are ignored, so curseforge-updater still matches CurseForge. A
// one-word folder has to cover a fair share of the app's name, so "Banana" doesn't match
// "Banana Bugs PopCap".
func partOfName(folder, app []string) bool {
	words := distinctive(folder)
	if len(words) == 0 {
		return false
	}
	set := map[string]bool{}
	for i, t := range app {
		set[t] = true
		if i+1 < len(app) {
			set[t+app[i+1]] = true // curseforge also matches Curse Forge
		}
	}
	for _, t := range words {
		if !set[t] {
			return false
		}
		if len(t) < 4 && (len(app) == 0 || app[0] != t) {
			return false
		}
	}
	appWords := distinctive(app)
	return len(words) > 1 || len(appWords) <= 2
}

// holdsName reports whether an app's whole name appears in the folder's, such as
// Voicemod in Voicemod.exe, allowing at most one extra word.
func holdsName(folder, app []string) bool {
	words := distinctive(app)
	if len(words) == 0 {
		return false
	}
	set := map[string]bool{}
	for _, t := range folder {
		set[t] = true
	}
	for _, t := range words {
		if !set[t] || len(t) < 5 {
			return false
		}
	}
	// "Windows Media Player Legacy" is mostly generic words, so "Legacy" alone can't stand in for it.
	if len(words)*2 <= len(app) {
		return false
	}
	return len(distinctive(folder)) <= len(words)+1
}

func sharesWord(a, b []string) bool {
	set := map[string]bool{}
	for _, t := range distinctive(b) {
		set[t] = true
	}
	for _, t := range distinctive(a) {
		if set[t] {
			return true
		}
	}
	return false
}

func samePathOrInside(a, b string) bool {
	a, b = strings.ToLower(filepath.Clean(a)), strings.ToLower(filepath.Clean(b))
	sep := string(filepath.Separator)
	return a == b || strings.HasPrefix(a, b+sep)
}

type match struct {
	app    *App
	score  int
	how    string
	reason string
}

func bestMatch(f *Folder, inv *Inventory) match {
	var best match
	name := norm(f.Name)
	folderTokens := tokens(f.Name)
	parent := parentName(f)
	joined := norm(parent + f.Name)
	consider := func(a *App, score int, how string) {
		// A name from the run history is often just a file name, so a game library's
		// proper name beats it. GZW should read as Gray Zone Warfare.
		if (a.Source == sourceRan || a.Source == sourceDisk) && score < 92 && score > 85 {
			score = 85
		}
		if score > best.score || (score == best.score && best.app != nil &&
			(sourceRank(a.Source) > sourceRank(best.app.Source) ||
				(sourceRank(a.Source) == sourceRank(best.app.Source) && len(a.Name) < len(best.app.Name)))) {
			best = match{app: a, score: score, how: how}
		}
	}
	for i := range inv.Apps {
		a := &inv.Apps[i]
		appTokens := tokens(a.Name)
		if !f.Loose {
			// A path match counts for less when the names have nothing in common. Chrome
			// lists web apps like YouTube as programs installed inside its own folder.
			related := sharesWord(folderTokens, appTokens) || len(folderTokens) == 0
			for _, p := range a.Paths {
				if p == "" {
					continue
				}
				if samePathOrInside(p, f.Path) {
					if related {
						consider(a, 100, "installed here")
					} else {
						consider(a, 80, "installed here")
					}
				} else if samePathOrInside(f.Path, p) {
					if related {
						consider(a, 95, "inside its install folder")
					} else {
						consider(a, 78, "inside its install folder")
					}
				}
			}
		}
		if name == "" || f.Loose {
			continue
		}
		if a.Unity != "" && strings.HasSuffix(strings.ToLower(f.Rel), `\`+a.Unity) {
			consider(a, 92, "unity")
		}
		appName := norm(a.Name)
		if appName == name || (parent != "" && appName == joined) {
			consider(a, 90, "same name")
		}
		for _, al := range a.Aliases {
			if norm(al) == name && len(name) >= 3 && !genericWords[name] {
				consider(a, 88, "alias:"+al)
			}
		}
		if a.Source == sourceDisk {
			continue // a folder name on a drive is too loose to match on just part of a name
		}
		if partOfName(folderTokens, appTokens) {
			consider(a, 70, "name is part of it")
		} else if a.Source != sourceProgFiles && holdsName(folderTokens, appTokens) {
			consider(a, 62, "its name is part of the folder's")
		}
		if a.Publisher != "" && norm(a.Publisher) == name && !genericWords[name] {
			consider(a, 65, "same publisher")
		} else if a.Publisher != "" && partOfName(folderTokens, tokens(a.Publisher)) {
			consider(a, 60, "same publisher")
		}
	}
	// A folder FolderTrace already knows by name isn't matched to a different product
	// that happens to share a word, such as .minecraft and a game called Minecraft Dungeons.
	if best.app != nil && best.score < 85 && f.kb != nil {
		known := map[string]bool{}
		for _, t := range tokens(f.kb.Owner) {
			known[t] = true
		}
		for _, t := range distinctive(tokens(best.app.Name)) {
			if !known[t] {
				return match{}
			}
		}
	}
	return best
}

const steamEvidence = "Named by the game data Steam keeps on this PC"

const (
	sourceInstalled = "your installed programs list"
	sourceSteam     = "your Steam library"
	sourceEpic      = "your Epic Games library"
	sourceGOG       = "your GOG library"
	sourceStore     = "your Microsoft Store apps"
	sourceStart     = "your Start menu"
	sourceProgFiles = "your Program Files folder"
	sourceStartup   = "your startup programs"
	sourceShortcut  = "a shortcut on your Desktop or Start menu"
	sourceRan       = "programs Windows remembers you running"
	sourceDisk      = "a program folder on your drives"
	sourceService   = "your Windows services"
)

func sourceRank(s string) int {
	switch s {
	case sourceInstalled, sourceSteam, sourceEpic, sourceGOG, sourceStore, sourceService:
		return 3
	case sourceStartup, sourceShortcut:
		return 2
	case sourceProgFiles, sourceRan, sourceDisk:
		return 1
	}
	return 0
}

// ---- Deciding what to tell the person ----

// Going months without touching a game doesn't mean it's gone, so a folder needs to sit
// untouched for a long time before age alone makes it a likely leftover.
const leftoverAge = 180 * 24 * time.Hour

var genericProducts = map[string]bool{"nodejs": true, "chromium": true, "electron": true, "win": true, "windows": true,
	"windowsinternetexplorer": true, "microsoftwindowsoperatingsystem": true, "ffmpeg": true, "python": true, "squirrel": true,
	"java": true, "openjdk": true, "microsoftnetframework": true, "microsoftedgewebview2": true, "unrealengine": true, "unity": true}

var smallWords = map[string]bool{"for": true, "of": true, "the": true, "and": true, "a": true, "an": true, "to": true, "on": true, "in": true}

// isInitials spots a short name made from a longer one's first letters: WSL is
// Windows Subsystem for Linux, OBS is Open Broadcaster Software.
func isInitials(short, long string) bool {
	s := norm(short)
	// Only names written as initials count. Steam spells "Some Thing Else Around Me" too.
	if len(s) < 2 || len(s) > 5 || strings.ToUpper(short) != short {
		return false
	}
	var all, big strings.Builder
	for _, w := range strings.FieldsFunc(cleanName(long), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		first := strings.ToLower(string([]rune(w)[:1]))
		all.WriteString(first)
		if !smallWords[strings.ToLower(w)] {
			big.WriteString(first)
		}
	}
	return (all.Len() >= 2 && all.String() == s) || (big.Len() >= 2 && big.String() == s)
}

// usableProduct decides whether the name stored in a program can name the whole folder.
// A helper program is often called something else entirely, like yt-dlp inside VRChat.
func usableProduct(f *Folder) string {
	if f.Product == "" || genericProducts[norm(f.Product)] {
		return ""
	}
	if sharesWord(tokens(f.Name), tokens(f.Product)) || looksRandom(f.Name) || isInitials(f.Name, f.Product) {
		return cleanName(f.Product)
	}
	return ""
}

var dataFolderName = regexp.MustCompile(`(?i)^(saves?|save[\d_ ].*|data|config|settings|logs?|default|profiles?|shared|temp|temporary|cache|dumpcache|screenshots?|crash(es|dumps)?)\d*$`)

// bestGuess reads a likely owner from the path. A folder called save00 says nothing, but
// the folder holding it, Nolla_Games_Noita, does.
func bestGuess(f *Folder) string {
	if p := parentName(f); p != "" && dataFolderName.MatchString(strings.ReplaceAll(f.Name, "_", " ")) {
		return guessFromName(p)
	}
	return guessFromName(f.Name)
}

// shortcutNamed finds a shortcut whose name starts with the folder's, such as Palworld
// for a folder called Pal. It's too weak to prove a match, but it's enough to stop
// FolderTrace from sounding sure the program is gone.
func shortcutNamed(f *Folder, inv *Inventory) string {
	name := norm(f.Name)
	if len(name) < 3 {
		return ""
	}
	for i := range inv.Apps {
		a := &inv.Apps[i]
		if a.Source == sourceShortcut && strings.HasPrefix(norm(a.Name), name) {
			return a.Name
		}
	}
	return ""
}

var genericPathNames = map[string]bool{"game": true, "games": true, "install": true, "bin": true, "bin64": true, "win64": true, "win32": true,
	"binaries": true, "content": true, "app": true, "x64": true, "x86": true, "release": true, "client": true, "engine": true, "program": true, "windowsnoeditor": true,
	"commonredist": true, "_commonredist": true, "redist": true, "vcredist": true, "directx": true, "prerequisites": true, "installers": true, "setup": true}

// nameFromPath finds a program's name in its path, skipping folders named things like
// game or Win64: Desktop\BastionOS\game is BastionOS.
func nameFromPath(dir string) string {
	for i := 0; i < 5; i++ {
		base := filepath.Base(strings.ReplaceAll(dir, `\`, string(filepath.Separator)))
		if base == "" || strings.HasSuffix(base, ":") || base == string(filepath.Separator) {
			return ""
		}
		if !genericPathNames[strings.ToLower(base)] {
			return cleanName(base)
		}
		dir = filepath.Dir(strings.ReplaceAll(dir, `\`, string(filepath.Separator)))
	}
	return ""
}

// looksRandom spots generated folder names like rjCqvgXSUEaINq.
func looksRandom(name string) bool {
	if strings.ContainsAny(name, " -_.") || len(name) < 10 {
		return false
	}
	flips, digits := 0, 0
	rs := []rune(name)
	for i, r := range rs {
		if unicode.IsDigit(r) {
			digits++
		}
		if i > 0 && unicode.IsUpper(r) != unicode.IsUpper(rs[i-1]) {
			flips++
		}
	}
	if flips >= 5 && digits == 0 {
		// HallsOfTorment flips case a lot too, but it's made of real words.
		words := true
		for _, w := range strings.Fields(camelBreak.ReplaceAllString(name, "$1 $2")) {
			if len(w) < 2 || !strings.ContainsAny(strings.ToLower(w), "aeiouy") || strings.ToLower(w) != w && strings.ToUpper(w) == w {
				words = false
			}
		}
		if words {
			return false
		}
	}
	return flips >= 5 || digits >= 4
}

// guessFromName turns a folder's own name into a readable guess at its owner.
func guessFromName(name string) string {
	if looksRandom(name) {
		return ""
	}
	return strings.TrimLeft(baseFolderName(name), ". ")
}

var (
	folderSuffix = regexp.MustCompile(`(?i)[-_. ]+(updater|update|installer|setup)$`)
	folderDomain = regexp.MustCompile(`(?i)^(com|gg|io|org|net|app|dev)\.`)
	dotNetConfig = regexp.MustCompile(`(?i)^(.+?)\.exe_(url|strongname|eid)_\w+$`)
	folderBy     = regexp.MustCompile(`(?i)\s+by\s+\S.*$`)
	longHex      = regexp.MustCompile(`[0-9A-Fa-f]{16}`)
	camelBreak   = regexp.MustCompile(`([a-z])([A-Z])`)
	domainEnd    = regexp.MustCompile(`(?i)\.(net|com|io|gg|ai|org)$`)
)

// baseFolderName removes what's around a program's real name: the updater or installer
// part, a reversed web address at the front, or "by" and the company at the end.
func baseFolderName(name string) string {
	// .NET programs keep settings in folders like DayZLauncher.exe_Url_qaaevecr3ijx.
	if m := dotNetConfig.FindStringSubmatch(name); m != nil {
		name = m[1]
	}
	out := folderBy.ReplaceAllString(folderSuffix.ReplaceAllString(folderDomain.ReplaceAllString(name, ""), ""), "")
	if len(norm(out)) < 3 {
		return name
	}
	return out
}

// readableName turns a raw name such as ez-slots or HallsOfTorment into words.
func readableName(s string) string {
	if strings.Contains(s, " ") {
		return s
	}
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	if !domainEnd.MatchString(s) && !strings.Contains(s, " ") && strings.Contains(s, ".") {
		s = strings.ReplaceAll(s, ".", " ")
	}
	if s == strings.ToLower(s) {
		words := strings.Fields(s)
		for i, w := range words {
			r := []rune(w)
			words[i] = strings.ToUpper(string(r[:1])) + string(r[1:])
		}
		s = strings.Join(words, " ")
	}
	return s
}

// tidyOwner fixes names that came out of paths and installers looking wrong.
func tidyOwner(f *Folder) {
	old := f.Owner
	if old == "" || old == "Unknown" {
		return
	}
	owner := old
	// Roblox Player for alex is the installer naming the Windows account.
	if u := os.Getenv("USERNAME"); u != "" && strings.HasSuffix(strings.ToLower(owner), " for "+strings.ToLower(u)) {
		owner = strings.TrimSpace(owner[:len(owner)-len(u)-5])
	}
	base := baseFolderName(f.Name)
	if p := parentName(f); p != "" && dataFolderName.MatchString(strings.ReplaceAll(f.Name, "_", " ")) {
		base = p
	}
	n, b := norm(owner), norm(base)
	switch {
	case n == b && strings.Contains(base, " ") && !strings.Contains(owner, " "):
		owner = base // mafiatheoldcountry in a folder called Mafia The Old Country
	case genericPathNames[strings.ToLower(owner)] && !looksRandom(base):
		owner = camelBreak.ReplaceAllString(readableName(strings.TrimLeft(base, ". ")), "$1 $2")
	case len(n) <= 2 && !strings.Contains(b, n) && !looksRandom(base):
		owner = readableName(strings.TrimLeft(base, ". "))
	case len(b) >= 4 && len(n) > len(b) && strings.HasPrefix(n, b) && !strings.Contains(owner, " ") && !looksRandom(base):
		// kryptex_rigel in a folder called Kryptex: the folder has the cleaner name.
		owner = readableName(strings.TrimLeft(base, ". "))
	case f.Guessed:
		owner = readableName(owner)
	case !strings.Contains(owner, " ") && (owner == strings.ToLower(owner) || strings.ContainsAny(owner, "._-")) && !domainEnd.MatchString(owner):
		owner = readableName(owner)
	}
	// A guess is more useful with the company around it: Y504D (Saber).
	if p := parentName(f); f.Guessed && p != "" && !strings.EqualFold(f.Root, "LocalLow") && publisherName(p) && !strings.Contains(norm(owner), norm(p)) {
		owner += " (" + p + ")"
	}
	// Windows' own packages aren't apps anyone installed or can tidy up.
	if isPackageFolder(f) {
		l := strings.ToLower(f.Name)
		if strings.HasPrefix(l, "microsoftwindows.") || strings.HasPrefix(l, "windows.") || strings.HasPrefix(l, "microsoft.windows.") {
			if len(n) <= 3 {
				owner = "Windows"
			}
			f.Verdict, f.Confidence = VerdictWindows, "High"
			f.Why = "This is part of Windows itself, not an app you installed."
		}
	}
	if owner != old {
		f.Owner = owner
		if re, err := regexp.Compile(`\b` + regexp.QuoteMeta(old) + `\b`); err == nil {
			f.Why = re.ReplaceAllLiteralString(f.Why, owner)
		}
	}
}

// cleanName removes version numbers and notes from a display name.
func cleanName(s string) string {
	s = strings.NewReplacer("™", "", "®", "", "©", "").Replace(s)
	s = nameNotes.ReplaceAllString(s, "")
	s = nameVersion.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimRight(s, ". ")
}

var (
	nameNotes   = regexp.MustCompile(`\s*[\[(][^\])]*[\])]`)
	nameVersion = regexp.MustCompile(`(?i)[\s_.]+(version\s+)?v?\d+(\.\d+)+[-\w.]*$`)
)

// nameOnly is true when all that's known is a shortcut's name. Shortcuts can outlive the
// program, so that's not proof it's installed.
func nameOnly(a *App) bool {
	return len(a.Paths) == 0 && (a.Source == sourceStart || a.Source == sourceShortcut)
}

func isMicrosoftChild(f *Folder) bool {
	p := norm(parentName(f))
	return p == "microsoft"
}

func isNvidiaChild(f *Folder) bool {
	p := norm(parentName(f))
	return p == "nvidia" || p == "nvidiacorporation"
}

// bestGone looks for a program Windows remembers but that's no longer on the PC.
func bestGone(f *Folder, inv *Inventory) *App {
	if f.Loose {
		return nil
	}
	name := norm(f.Name)
	words := tokens(f.Name)
	for i := range inv.Gone {
		a := &inv.Gone[i]
		if norm(a.Name) == name && len(name) >= 3 {
			return a
		}
		for _, al := range a.Aliases {
			if norm(al) == name && len(name) >= 4 && !genericWords[name] {
				return a
			}
		}
		if partOfName(words, tokens(a.Name)) {
			return a
		}
	}
	return nil
}

func isPackageFolder(f *Folder) bool {
	return parentName(f) != "" && strings.EqualFold(parentName(f), "Packages")
}

func runningMatch(f *Folder, inv *Inventory) (Process, bool) {
	name := norm(f.Name)
	for _, p := range inv.Running {
		if !f.Loose && p.Path != "" && samePathOrInside(p.Path, f.Path) {
			return p, true
		}
		if !f.Loose && len(name) >= 4 && !genericWords[name] && norm(strings.TrimSuffix(strings.ToLower(p.Name), ".exe")) == name {
			return p, true
		}
	}
	return Process{}, false
}

// Classify fills in who a folder probably belongs to and what that means.
func Classify(f *Folder, inv *Inventory, now time.Time) {
	defer tidyOwner(f)
	f.kb = lookupKB(f)
	if f.kb != nil && f.kb.Cache {
		f.CacheBytes = f.Size // the whole folder is a cache, whatever the files are called
		f.ProgBytes, f.ConfBytes = 0, 0
	}
	if f.Loose {
		f.Owner, f.Identified = "Windows and apps", true
		f.Verdict, f.Confidence = VerdictWindows, "Medium"
		f.Why = "These are files sitting loose in " + filepath.Base(f.Path) + ", not in their own folder. They're usually shared caches or app databases, and no single program can be named."
		f.Advice = "Leave these alone. Windows or other apps may rely on them."
		f.Evidence = append(f.Evidence, "Listed together because they aren't in a subfolder")
		return
	}
	if isPackageFolder(f) && strings.HasPrefix(strings.ToLower(f.Name), "cr.sb.") {
		f.Owner, f.Identified = "Chrome or another Chromium browser", true
		f.Verdict, f.Confidence = VerdictWindows, "High"
		f.Why = "This is a sandbox folder made by Chrome, Edge, or another browser built on Chromium. It isn't a Microsoft Store app, even though it's stored with them."
		f.Advice = "Leave this alone. The browser manages it."
		f.Evidence = append(f.Evidence, "The folder name starts with cr.sb, Chromium's name for its sandboxes")
		return
	}
	if p := norm(parentName(f)); p == "temp" || p == "temporary" {
		f.Owner, f.Identified = "Temporary files", true
		f.Verdict, f.Confidence = VerdictWindows, "Medium"
		f.Why = "This is inside a temporary folder, so whatever made it doesn't count on it staying."
		f.Advice = "Leave this for now. Apps may still be using temporary files."
		f.Evidence = append(f.Evidence, "Located in a temporary folder")
		return
	}
	days := int(now.Sub(f.Newest).Hours() / 24)
	stale := !f.Newest.IsZero() && now.Sub(f.Newest) >= leftoverAge
	empty := f.Files == 0 && f.Unreadable == 0
	age := FormatAge(f.Newest, now)
	if f.Newest.IsZero() {
		age = "at an unknown time"
	}
	changed := "It last changed " + age + "."
	if age == "today" {
		changed = "It changed today."
	}
	evidence := func(s string) { f.Evidence = append(f.Evidence, s) }

	m := bestMatch(f, inv)
	if isMicrosoftChild(f) && strings.HasPrefix(m.how, "alias:") {
		m = match{} // a folder called Templates inside some program says nothing here
	}
	if strings.HasPrefix(m.how, "alias:") && dataFolderName.MatchString(strings.ReplaceAll(f.Name, "_", " ")) {
		m = match{} // nor does one called log or save
	}
	if m.app == nil && parentName(f) != "" && !isPackageFolder(f) && !isMicrosoftChild(f) && !isNvidiaChild(f) && !strings.EqualFold(parentName(f), "Programs") {
		// LocalLow\Nolla_Games_Noita\save00 belongs to whatever owns the folder around it.
		if dataFolderName.MatchString(strings.ReplaceAll(f.Name, "_", " ")) {
			up := &Folder{Name: parentName(f), Rel: f.Root + `\` + parentName(f), Path: filepath.Dir(f.Path)}
			m = bestMatch(up, inv)
		}
	}
	if base := baseFolderName(f.Name); m.app == nil && base != f.Name && !isPackageFolder(f) {
		// hydralauncher-updater and Ascendara by tagoWorks belong to what they're named after.
		up := *f
		up.Name = base
		m = bestMatch(&up, inv)
	}
	proc, running := runningMatch(f, inv)

	appName := ""
	if m.app != nil {
		appName = cleanName(m.app.Name)
		// A name FolderTrace knows is better than whatever a run-history path was called.
		if f.kb != nil && !f.kb.Group && !f.kb.Shared {
			appName = f.kb.Owner
		}
	}
	gone := bestGone(f, inv)
	listed := ""
	if !isPackageFolder(f) && (f.kb == nil || !f.kb.Group) {
		listed = cleanName(inv.Steam.steamName(f))
	}
	namedBy := steamEvidence
	if listed == "" && f.kb != nil && !f.kb.Shared && !f.kb.Group {
		// A name FolderTrace already knows can still be checked against what's installed.
		listed, namedBy = f.kb.Owner, "FolderTrace recognizes this name from its built-in list"
	}
	if m.app != nil && m.score >= 60 {
		f.app = m.app
	}
	var listedApp *App
	if listed != "" {
		listedApp = appNamed(inv, listed)
		if f.app == nil {
			f.app = listedApp
		}
		// A loose match on part of a name shouldn't beat Steam naming this exact folder.
		if m.app != nil && m.score < 85 && !sharesWord(tokens(listed), tokens(m.app.Name)) {
			m = match{}
		}
	}
	// proper picks the best available name for a folder with no installed match.
	proper := func() (string, bool) {
		switch {
		case f.kb != nil:
			return f.kb.Owner, true
		case listed != "":
			return listed, true
		}
		if p := usableProduct(f); p != "" {
			return p, true
		}
		return "", false
	}

	switch {
	case f.kb != nil && f.kb.Shared:
		f.Owner, f.Identified = f.kb.Owner, true
		f.Verdict, f.Confidence = VerdictWindows, "High"
		f.Why = f.kb.Note
		evidence("FolderTrace recognizes this name from its built-in list")
	case running:
		f.Owner, f.Identified = proc.Name, true
		if m.app != nil {
			f.Owner = appName
		} else if f.kb != nil {
			f.Owner = f.kb.Owner
		} else if p := usableProduct(f); p != "" {
			f.Owner = p
		}
		f.Verdict, f.Confidence = VerdictInUse, "High"
		f.Why = f.Owner + " is running right now and uses this folder."
		evidence("Running program: " + proc.Name)
		if m.app != nil {
			evidence("Also found in " + m.app.Source)
		}
	case m.app != nil && m.app.Broken && m.score >= 60:
		f.Owner, f.Identified = appName, true
		f.Verdict, f.Confidence = VerdictUnsure, "Medium"
		f.Why = "Windows still lists " + appName + " as installed, but its install folder is gone, so it may have been removed badly. " + changed
		evidence("Found in " + m.app.Source + ", but the install folder is missing")
	case m.app != nil && m.score >= 60:
		f.Owner, f.Identified = appName, true
		f.Verdict = VerdictInUse
		f.Confidence = "Medium"
		if m.score >= 85 {
			f.Confidence = "High"
		}
		switch {
		case m.how == "installed here":
			f.Why = appName + " is installed in this folder."
		case m.how == "inside its install folder":
			f.Why = "This folder is part of " + appName + "'s install folder."
		case strings.HasPrefix(m.how, "alias:"):
			f.Why = "This matches a folder or program inside " + appName + ", which is installed. Games often store data under a name like this."
		case m.how == "same publisher":
			f.Owner = cleanName(m.app.Publisher)
			f.Why = "This matches the company behind " + appName + ", which is installed. Another app from the same company could also be using it."
		case nameOnly(m.app):
			f.Confidence = "Medium"
			f.Why = "There's a shortcut called " + appName + ", so it's probably still installed, but FolderTrace couldn't find its program files."
		case m.score == 90:
			f.Why = appName + " is installed, and this folder has the same name."
		default:
			f.Why = "This looks like it belongs to " + appName + ", which is installed. The names are similar, but not identical."
		}
		evidence("Found in " + m.app.Source)
		if m.app.Source == sourceDisk && len(m.app.Paths) > 0 {
			f.Why = appName + " is in " + m.app.Paths[0] + ", and it uses this folder."
		}
		if strings.HasPrefix(m.how, "alias:") {
			evidence("Matching name inside it: " + strings.TrimPrefix(m.how, "alias:"))
		}
	case listedApp != nil && nameOnly(listedApp):
		f.Owner, f.Identified = listed, true
		f.Verdict, f.Confidence = VerdictInUse, "Medium"
		f.Why = "This is " + listed + "'s folder. There's a shortcut for it, so it's probably still installed, but FolderTrace couldn't find its program files."
		evidence(namedBy)
		evidence("Found in " + listedApp.Source)
	case listedApp != nil:
		f.Owner, f.Identified = listed, true
		f.Verdict, f.Confidence = VerdictInUse, "High"
		f.Why = "This is " + listed + "'s folder, and " + listed + " is installed."
		evidence(namedBy)
		evidence("Found in " + listedApp.Source)
	case f.kb == nil && listed == "" && len(f.UsedBy) > 0:
		// velopack's logs are full of Medal's install folder, so that's who it works for.
		var names []string
		installed := false
		for _, u := range f.UsedBy {
			name := u.Name
			if a := appNamed(inv, u.Name); a != nil {
				name, installed = cleanName(a.Name), true
			} else if exists(u.Path) {
				installed = true
			}
			names = append(names, name)
		}
		f.Owner, f.Identified = strings.Join(names, ", "), true
		evidence("Read from a log file in this folder")
		switch {
		case installed:
			f.Verdict, f.Confidence = VerdictInUse, "High"
			f.Why = "Its log files show it works for " + f.Owner + ", which is on this PC. Folders like this are usually an installer or updater that the program still relies on."
		case !empty && days > 30:
			f.Verdict, f.Confidence = VerdictLeftover, "Medium"
			f.Why = "Its log files show it worked for " + f.Owner + ", which is no longer on this PC. " + changed
		default:
			f.Verdict, f.Confidence = VerdictUnsure, "Medium"
			f.Why = "Its log files show it worked for " + f.Owner + ", which is no longer on this PC, but this folder changed recently. " + changed
		}
	case f.RanFrom != "" && exists(f.RanFrom):
		owner, known := proper()
		if !known {
			owner = nameFromPath(f.RanFrom)
			if owner == "" {
				owner = bestGuess(f)
			}
		}
		f.Owner, f.Identified = owner, true
		f.Verdict, f.Confidence = VerdictInUse, "High"
		f.Why = "Its " + f.RanSource + " says it ran from " + f.RanFrom + ", and that folder is still there."
		evidence("Read from a " + f.RanSource + " in this folder")
	case f.RanFrom != "" && !driveConnected(f.RanFrom):
		owner, known := proper()
		if !known {
			owner = nameFromPath(f.RanFrom)
			if owner == "" {
				owner = bestGuess(f)
			}
		}
		f.Owner, f.Identified = owner, true
		f.Verdict, f.Confidence = VerdictUnsure, "Medium"
		f.Why = "Its " + f.RanSource + " says it ran from " + f.RanFrom + ", but that drive isn't connected right now."
		evidence("Read from a " + f.RanSource + " in this folder")
	case f.RanFrom != "" && !empty && days > 30:
		owner, known := proper()
		if !known {
			owner = nameFromPath(f.RanFrom)
			if owner == "" {
				owner = bestGuess(f)
			}
		}
		f.Owner, f.Identified = owner, true
		f.Verdict, f.Confidence = VerdictLeftover, "High"
		if f.RanSource == clueSettings {
			f.Confidence = "Medium" // settings can hold old paths long after a move
		}
		f.Why = "Its " + f.RanSource + " says it ran from " + f.RanFrom + ", but that folder is gone. " + changed
		evidence("Read from a " + f.RanSource + " in this folder")
	case isMicrosoftChild(f):
		f.Owner, f.Identified = "Microsoft", true
		f.Verdict, f.Confidence = VerdictWindows, "Medium"
		f.Why = "This is inside the Microsoft folder. It's part of Windows or a Microsoft app, but FolderTrace can't tell which one."
		evidence("Located in a Microsoft folder")
	case isNvidiaChild(f):
		f.Owner, f.Identified = "NVIDIA graphics driver", true
		f.Verdict, f.Confidence = VerdictWindows, "Medium"
		f.Why = "This is inside the NVIDIA folder, so your graphics driver or NVIDIA apps use it."
		evidence("Located in an NVIDIA folder")
	case gone != nil && !empty && days > 30:
		// The remembered name only counts if it's really about this folder. A checksum
		// tool run from a Starfield folder doesn't make the folder that tool's.
		f.Owner, f.Identified = cleanName(gone.Name), true
		if listed != "" {
			f.Owner = listed
			evidence(namedBy)
		} else if f.kb != nil {
			f.Owner = f.kb.Owner
		} else if !partOfName(tokens(f.Name), tokens(gone.Name)) && norm(f.Name) != norm(gone.Name) {
			if g := bestGuess(f); g != "" {
				f.Owner = g
			}
		}
		// A broken shortcut can still give the proper name: Palworld for a folder called Pal.
		if name := norm(f.Name); len(name) >= 3 {
			for i := range inv.Gone {
				if a := &inv.Gone[i]; a.Source == sourceShortcut && strings.HasPrefix(norm(a.Name), name) {
					f.Owner = cleanName(a.Name)
					evidence("A shortcut called " + a.Name + " points to a program that's no longer there")
					break
				}
			}
		}
		if s := shortcutNamed(f, inv); s != "" {
			f.Owner = s
			f.Verdict, f.Confidence = VerdictUnsure, "Medium"
			f.Why = "The evidence conflicts. There's a shortcut called " + s + ", but the program isn't where Windows last saw it. " + changed
			evidence("Windows remembers running it from a location that's now gone")
			evidence("Shortcut found: " + s)
			break
		}
		f.Verdict, f.Confidence = VerdictLeftover, "Medium"
		if stale {
			f.Confidence = "High"
		}
		f.Why = "You used to run " + f.Owner + ", but it isn't where Windows last saw it, so it was probably removed or moved. " + changed
		evidence("Windows remembers running it, but the program is no longer in that location")
	case f.kb != nil && f.kb.Group:
		f.Owner, f.Identified = f.kb.Owner, true
		f.Verdict, f.Confidence = VerdictUnsure, "Medium"
		f.Why = "Several programs from the same company or built with the same tools can share this folder, so FolderTrace can't tie it to just one. " + changed
		evidence("FolderTrace recognizes this name from its built-in list")
	case isPackageFolder(f):
		f.Owner, f.Identified = friendlyPackage(f.Name), true
		if looksRandom(strings.SplitN(f.Owner, " ", 2)[0]) || longHex.MatchString(f.Name) {
			f.Owner, f.Identified = "A Microsoft Store app", false
		}
		if strings.HasPrefix(strings.ToLower(f.Name), "microsoft.") {
			f.Verdict, f.Confidence = VerdictUnsure, "Medium"
			f.Why = "This is data for a Microsoft Store app that doesn't show as installed for your account. Some of these are built into Windows. " + changed
		} else if !empty && days <= 30 {
			// Something touched it lately, so "not installed" can't be the whole story.
			f.Verdict, f.Confidence = VerdictUnsure, "Medium"
			f.Why = "This is data for a Microsoft Store app that doesn't show as installed for your account, but something changed it recently. " + changed
		} else {
			f.Verdict, f.Confidence = VerdictLeftover, "High"
			f.Why = "This is data for a Microsoft Store app that doesn't show as installed for your account. " + changed
		}
		evidence("The folder name is a Microsoft Store package ID")
		evidence("Not in your Microsoft Store apps")
	case empty:
		f.Owner, f.Identified = "Unknown", false
		if f.kb != nil {
			f.Owner, f.Identified = f.kb.Owner, true
		} else if g := bestGuess(f); g != "" {
			f.Owner, f.Guessed = g, true
		}
		if days <= 30 {
			// A program that's still installed often recreates its empty folder.
			f.Verdict, f.Confidence = VerdictUnsure, "Medium"
			f.Why = "This folder is empty, but something changed it recently, so a program may still use it. Removing it frees no space."
		} else {
			f.Verdict, f.Confidence = VerdictLeftover, "High"
			f.Why = "This folder is empty, so nothing is using anything inside it."
		}
		evidence("0 files inside")
	default:
		known := f.kb != nil
		guess := ""
		if known {
			f.Owner, f.Identified = f.kb.Owner, true
			evidence("FolderTrace recognizes this name from its built-in list")
		} else if listed != "" {
			f.Owner, f.Identified = listed, true
			evidence(namedBy)
		} else if p := usableProduct(f); p != "" {
			f.Owner, f.Identified = p, true
			evidence("Named by a program inside: " + filepath.Base(f.Exes[0]))
		} else if guess = bestGuess(f); guess != "" {
			f.Owner, f.Identified = guess, false
			f.Guessed = true
			evidence("The owner shown is only a guess from the folder's name")
		} else {
			f.Owner, f.Identified = "Unknown", false
		}
		if !f.Identified && f.Product != "" && !genericProducts[norm(f.Product)] {
			evidence("Has a program inside called " + cleanName(f.Product))
		}
		subject := "No installed program, game, shortcut, or running app matches this folder."
		if f.Identified {
			subject = f.Owner + " doesn't show as installed."
		}
		evidence("No match in your installed programs, game libraries, shortcuts, or running apps")
		hasProgram := len(f.Exes) > 0
		switch {
		case hasProgram && !stale:
			f.Verdict, f.Confidence = VerdictUnsure, "Low"
			f.Why = subject + " It has a program inside and changed recently, so it may be a portable app you still use. " + changed
		case !stale:
			f.Verdict, f.Confidence = VerdictUnsure, "Low"
			f.Why = subject + " But something changed it " + age + ", so it may still be in use."
			if age == "today" {
				f.Why = subject + " But something changed it today, so it may still be in use."
			}
		case days > 365:
			f.Verdict, f.Confidence = VerdictLeftover, "Medium"
			f.Why = subject + " " + changed
		default:
			f.Verdict, f.Confidence = VerdictLeftover, "Low"
			f.Why = subject + " " + changed
		}
	}
	if f.Unreadable > 0 {
		evidence(fmt.Sprintf("%d items couldn't be read, so the size may be low", f.Unreadable))
	}
	if f.Saves {
		evidence("Possible save data: " + f.SaveHint)
	}
	f.Advice = advice(f)
}

func advice(f *Folder) string {
	owner := f.Owner
	if !f.Identified && !f.Guessed {
		owner = "whatever made this"
	}
	if f.kb != nil && f.kb.Advice != "" {
		return f.kb.Advice
	}
	switch f.Verdict {
	case VerdictWindows:
		return "Leave this alone. Windows or other apps may rely on it."
	case VerdictInUse:
		if f.Size > 50<<20 && f.CacheBytes*10 >= f.Size*7 {
			return "Keep the folder. Most of it is cache, so look for a clear-cache option in " + owner + " if you need the space."
		}
		return "Keep this. Removing it could reset " + owner + " or sign you out."
	case VerdictLeftover:
		if f.Saves {
			return "Don't remove this without a backup. It looks like it holds game saves you can't download again."
		}
		if f.Profile {
			return "Back this up before removing it. It looks like it holds a profile, with things like logins or history."
		}
		if f.Files == 0 {
			return "Safe to review. There's nothing inside."
		}
		return "Worth reviewing. If you don't use " + owner + " anymore, this is a removal candidate. Check it and choose Delete checked to move it to the Recycle Bin."
	}
	if f.Saves {
		return "Leave this for now. It may hold game saves, so back it up before you remove it."
	}
	return "Leave this for now. Open it and see if you recognize anything inside."
}

// friendlyPackage turns a package ID into a readable name:
// Microsoft.WindowsCalculator_8wekyb3d8bbwe -> Windows Calculator.
func friendlyPackage(id string) string {
	name := id
	if i := strings.IndexByte(name, '_'); i > 0 {
		name = name[:i]
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 && i+1 < len(name) {
		name = name[i+1:]
	}
	var b strings.Builder
	rs := []rune(name)
	for i, r := range rs {
		if i > 1 && unicode.IsUpper(r) && (unicode.IsLower(rs[i-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	out := b.String()
	for from, to := range map[string]string{"Whats App": "WhatsApp", "You Tube": "YouTube", "Git Hub": "GitHub", "Tik Tok": "TikTok", "Open AI": "OpenAI"} {
		out = strings.ReplaceAll(out, from, to)
	}
	return out
}

// ---- Running a scan ----

type Progress struct {
	Done, Total int
	Current     string
}

// Scan measures and labels every folder. Stopping early returns what's done so far.
func Scan(roots []Root, inv *Inventory, stop *atomic.Bool, report func(Progress), product func(*Folder)) []*Folder {
	return scanWith(listTargets(roots), Classify, inv, stop, report, product)
}

func scanWith(targets []*Folder, classify func(*Folder, *Inventory, time.Time), inv *Inventory, stop *atomic.Bool, report func(Progress), product func(*Folder)) []*Folder {
	now := time.Now()
	jobs := make(chan *Folder)
	var wg sync.WaitGroup
	var done atomic.Int64
	var mu sync.Mutex
	var finished []*Folder
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				measure(f, stop)
				if stop != nil && stop.Load() {
					continue
				}
				if product != nil {
					product(f)
				}
				classify(f, inv, now)
				mu.Lock()
				finished = append(finished, f)
				mu.Unlock()
				n := done.Add(1)
				if report != nil {
					report(Progress{int(n), len(targets), f.Rel})
				}
			}
		}()
	}
	for _, f := range targets {
		if stop != nil && stop.Load() {
			break
		}
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	sort.Slice(finished, func(i, j int) bool { return finished[i].Size > finished[j].Size })
	return finished
}

// ---- Totals and formatting ----

type Summary struct {
	Folders, Named, Guessed int
	Size, LeftoverSize      int64
	Counts                  [4]int
	Unreadable              int
}

func Summarize(all []*Folder) Summary {
	var s Summary
	for _, f := range all {
		s.Folders++
		s.Size += f.Size
		s.Counts[f.Verdict]++
		if f.Identified {
			s.Named++
		}
		if f.Guessed {
			s.Guessed++
		}
		if f.Verdict == VerdictLeftover {
			s.LeftoverSize += f.Size
		}
		if f.Unreadable > 0 {
			s.Unreadable++
		}
	}
	return s
}

func FormatSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	case n == 0:
		return "0 KB"
	}
	return "1 KB"
}

func FormatAge(t, now time.Time) string {
	if t.IsZero() {
		return "Unknown"
	}
	d := now.Sub(t)
	days := int(d.Hours() / 24)
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit + " ago"
		}
		return fmt.Sprintf("%d %ss ago", n, unit)
	}
	switch {
	case days <= 0:
		return "today"
	case days < 2:
		return "yesterday"
	case days < 60:
		return plural(days, "day")
	case days < 730:
		return plural(days/30, "month")
	}
	return plural(days/365, "year")
}

// parseLnk reads the local target path from a Windows .lnk file's bytes.
func parseLnk(d []byte) string {
	u16 := func(o int) int {
		if o+2 > len(d) || o < 0 {
			return -1
		}
		return int(d[o]) | int(d[o+1])<<8
	}
	u32 := func(o int) int {
		if o+4 > len(d) || o < 0 {
			return -1
		}
		return int(d[o]) | int(d[o+1])<<8 | int(d[o+2])<<16 | int(d[o+3])<<24
	}
	if len(d) < 76 || u32(0) != 0x4C {
		return ""
	}
	flags := u32(20)
	pos := 76
	if flags&1 != 0 { // skip the target ID list
		n := u16(pos)
		if n < 0 {
			return ""
		}
		pos += 2 + n
	}
	if flags&2 == 0 { // no link info
		return ""
	}
	info := pos
	headerSize := u32(info + 4)
	infoFlags := u32(info + 8)
	if infoFlags&1 == 0 {
		return ""
	}
	if headerSize >= 0x24 {
		if off := u32(info + 28); off > 0 && info+off < len(d) {
			var units []uint16
			for o := info + off; ; o += 2 {
				c := u16(o)
				if c <= 0 {
					break
				}
				units = append(units, uint16(c))
			}
			return string(utf16.Decode(units))
		}
	}
	off := u32(info + 16)
	if off <= 0 || info+off >= len(d) {
		return ""
	}
	end := info + off
	for end < len(d) && d[end] != 0 {
		end++
	}
	return string(d[info+off : end])
}

// ---- Clues inside the folder ----

const (
	clueLog      = "log file"
	clueCrash    = "crash report"
	clueSettings = "settings file"
	clueShortcut = "shortcut"
)

type clueFile struct{ path, kind string }

var clueLimit = map[string]int{clueLog: 4, clueCrash: 3, clueSettings: 10, clueShortcut: 3}

func countClues(files []clueFile, kind string) int {
	n := 0
	for _, f := range files {
		if f.kind == kind {
			n++
		}
	}
	return n
}

// These never get opened. AppData holds logins and browsing data, and nothing in a path
// like these can help name a program anyway.
var privateWords = []string{"cookie", "login", "password", "passwd", "credential", "token", "secret", "session", "wallet", "history", "bookmark",
	"autofill", "webdata", "web data", "key4", "cert9", "leveldb", "local storage", "indexeddb", "user data", "profiles", "network", "account", "auth",
	"license", "licence", ".ssh", "gnupg", "keychain", "vault", "mail", "message", "chat"}

// clueKind decides whether a file is worth opening, using only its path and size.
func clueKind(parts []string, base string, size int64) string {
	for _, part := range parts {
		for _, w := range privateWords {
			if strings.Contains(part, w) {
				return ""
			}
		}
	}
	ext := filepath.Ext(base)
	switch {
	case base == "player.log" || base == "player-prev.log" || base == "output_log.txt":
		return clueLog
	case ext == ".log" && size <= 64<<20:
		return clueLog
	case strings.HasSuffix(base, ".runtime-xml") || (strings.Contains(base, "crashcontext") && size <= 4<<20):
		return clueCrash
	case ext == ".lnk" && size <= 64<<10:
		return clueShortcut
	case size <= 256<<10 && (ext == ".ini" || ext == ".cfg" || ext == ".conf" || ext == ".json" || ext == ".xml" || ext == ".yml" || ext == ".yaml" || ext == ".vdf" || ext == ".toml"):
		return clueSettings
	}
	return ""
}

var (
	logPathRE   = regexp.MustCompile(`(?i)[a-z]:[\\/][^\r\n'"<>|*?:]*?(?:_Data|[\\/]Binaries[\\/]Win64|\.exe)`)
	steamPathRE = regexp.MustCompile(`(?i)[a-z]:[\\/][^\r\n'"<>|*?:]*?[\\/]steamapps[\\/]common[\\/][^\\/\r\n'"<>|*?:]+`)
)

// Places that tell us nothing about who owns a folder. Almost anything can mention these.
var ignoredPathWords = []string{`\windows\`, `\appdata\`, `\programdata\`, `\common files\`, `\unity\hub\`, `\system32`, `\microsoft`, `\nodejs\`, `\python`,
	`\java\`, `\jdk`, `\temp\`, `\tmp\`, `\downloads\`, `\$recycle.bin\`, `\nvidia`, `\overwolf`, `\google\chrome`, `\mozilla`, `\epic games\launcher`,
	`\steam\steam.exe`, `\steam\steamwebhelper`, `\steam\bin\`, `\git\`, `\users\public`}

// pathsInText pulls every program location out of a file's text, as install folders.
func pathsInText(text string) []string {
	text = strings.ReplaceAll(text, `\\\\`, `\`) // JSON writes one backslash as two
	var out []string
	add := func(dir string) {
		if len(dir) > 3 {
			out = append(out, dir)
		}
	}
	for _, m := range steamPathRE.FindAllString(text, 20) {
		p := strings.ReplaceAll(m, "/", `\`)
		if !strings.HasSuffix(strings.ToLower(p), ".exe") {
			add(p)
		}
	}
	for _, m := range logPathRE.FindAllString(text, 40) {
		p := strings.ReplaceAll(m, "/", `\`)
		low := strings.ToLower(p) + `\`
		skip := false
		for _, w := range ignoredPathWords {
			skip = skip || strings.Contains(low, w)
		}
		if skip {
			continue
		}
		low = strings.ToLower(p)
		switch {
		case strings.HasSuffix(low, "_data"):
			add(p[:strings.LastIndexByte(p, '\\')])
		case strings.HasSuffix(low, `\binaries\win64`):
			dir := p[:len(p)-len(`\Binaries\Win64`)] // Game\Project
			if i := strings.LastIndexByte(dir, '\\'); i > 2 {
				dir = dir[:i] // Game
			}
			add(dir)
		case strings.HasSuffix(low, ".exe"):
			if i := strings.LastIndexByte(p, '\\'); i > 2 && !strings.HasSuffix(strings.ToLower(p[:i]), `\binaries\win64`) {
				add(p[:i])
			}
		}
	}
	return out
}

// logApp is a program in AppData that a folder's logs keep mentioning.
type logApp struct {
	Name, Path string
	count      int
}

var appDataExeRE = regexp.MustCompile(`(?i)([a-z]:[\\/]+users[\\/]+[^\\/"'\r\n<>|*?:]+[\\/]+appdata[\\/]+(?:local|roaming)[\\/]+(?:programs[\\/]+)?)([^\\/"'\r\n<>|*?:]+)[\\/]+[^"'\r\n<>|*?:]*?\.exe\b`)

// Folders under AppData that say nothing about who a log belongs to.
var notAnAppDataProgram = map[string]bool{"temp": true, "microsoft": true, "packages": true, "programs": true, "google": true,
	"mozilla": true, "nvidia": true, "nvidiacorporation": true, "crashdumps": true, "squirreltemp": true, "bravesoftware": true}

// programsInLogs reads which AppData programs a folder's logs are about. It needs a name
// to turn up more than once, and only the program's name and location are kept.
func programsInLogs(f *Folder, files []clueFile) []logApp {
	found := map[string]*logApp{}
	total := 0
	own := norm(f.Name)
	for _, c := range files {
		if c.kind != clueLog && c.kind != clueCrash {
			continue
		}
		for _, m := range appDataExeRE.FindAllStringSubmatch(readStart(c.path, 128<<10), -1) {
			n := norm(m[2])
			if n == "" || n == own || notAnAppDataProgram[n] || strings.Contains(n, own) && len(own) >= 4 {
				continue
			}
			a := found[n]
			if a == nil {
				a = &logApp{Name: m[2], Path: logAppPath(m[1], m[2])}
				found[n] = a
			}
			a.count++
			total++
		}
	}
	var out []logApp
	for _, a := range found {
		if a.count >= 2 && a.count*4 >= total {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func logAppPath(prefix, name string) string {
	parts := strings.FieldsFunc(prefix, func(r rune) bool { return r == '\\' || r == '/' })
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(append(parts, name), `\`)
}

// installDirFromLog returns the first program location in a log's text.
func installDirFromLog(text string) string {
	if paths := pathsInText(text); len(paths) > 0 {
		return paths[0]
	}
	return ""
}

func readStart(path string, limit int) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	buf := make([]byte, limit)
	n, _ := file.Read(buf)
	return strings.ReplaceAll(string(buf[:n]), "\x00", "") // UTF-16 text reads fine without its zero bytes
}

// findProgramPath looks through the chosen files for where the program ran from. Nothing
// read here is kept except the program location. Logs and crash reports describe the
// program itself, so they're trusted. A settings file can mention anything, so its path
// has to share a word with the folder or turn up in at least two files.
func findProgramPath(f *Folder, files []clueFile) (string, string) {
	for _, kind := range []string{clueCrash, clueLog} {
		for _, c := range files {
			if c.kind == kind {
				if d := installDirFromLog(readStart(c.path, 128<<10)); d != "" {
					return d, kind
				}
			}
		}
	}
	for _, c := range files {
		if c.kind == clueShortcut {
			data, err := os.ReadFile(c.path)
			if err != nil {
				continue
			}
			target := parseLnk(data)
			if strings.HasSuffix(strings.ToLower(target), ".exe") {
				if ds := pathsInText(target); len(ds) > 0 {
					return ds[0], clueShortcut
				}
			}
		}
	}
	votes := map[string]int{}
	names := map[string]string{}
	for _, c := range files {
		if c.kind != clueSettings {
			continue
		}
		seen := map[string]bool{}
		for _, d := range pathsInText(readStart(c.path, 64<<10)) {
			k := strings.ToLower(d)
			if !seen[k] {
				seen[k] = true
				votes[k]++
				names[k] = d
			}
		}
	}
	best, bestVotes := "", 0
	folderWords := tokens(f.Name)
	for k, n := range votes {
		related := sharesWord(folderWords, tokens(names[k]))
		if !related && n < 2 {
			continue
		}
		score := n
		if related {
			score += 10
		}
		if score > bestVotes || (score == bestVotes && k < best) {
			best, bestVotes = k, score
		}
	}
	if best != "" {
		return names[best], clueSettings
	}
	return "", ""
}

// appNamed finds an installed program with this name.
func appNamed(inv *Inventory, name string) *App {
	want := norm(name)
	words := tokens(name)
	var best *App
	for i := range inv.Apps {
		a := &inv.Apps[i]
		if a.Broken {
			continue
		}
		if norm(a.Name) == want || norm(cleanName(a.Name)) == want {
			return a
		}
		if best == nil && a.Source != sourceRan && a.Source != sourceDisk && a.Source != sourceProgFiles && partOfName(words, tokens(a.Name)) && len(distinctive(words)) > 1 {
			best = a
		}
	}
	return best
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// driveConnected reports whether a path's drive is plugged in. It's set per platform.
var driveConnected = func(string) bool { return true }
