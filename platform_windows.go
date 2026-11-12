//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

func systemDLL(name string) *syscall.LazyDLL {
	buf := make([]uint16, 260)
	k32 := syscall.NewLazyDLL("kernel32.dll")
	n, _, _ := k32.NewProc("GetSystemDirectoryW").Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.NewLazyDLL(syscall.UTF16ToString(buf[:n]) + `\` + name)
}

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	advapi32 = systemDLL("advapi32.dll")
	versionD = systemDLL("version.dll")

	procRegEnumKeyExW              = advapi32.NewProc("RegEnumKeyExW")
	procRegEnumValueW              = advapi32.NewProc("RegEnumValueW")
	procCreateToolhelp32Snapshot   = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW            = kernel32.NewProc("Process32FirstW")
	procProcess32NextW             = kernel32.NewProc("Process32NextW")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procGetFileVersionInfoSizeW    = versionD.NewProc("GetFileVersionInfoSizeW")
	procGetFileVersionInfoW        = versionD.NewProc("GetFileVersionInfoW")
	procVerQueryValueW             = versionD.NewProc("VerQueryValueW")
	procGetLogicalDrives           = kernel32.NewProc("GetLogicalDrives")
	procGetDriveTypeW              = kernel32.NewProc("GetDriveTypeW")
)

const (
	keyRead  = 0x20019
	wow64Key = 0x0100
	wow32Key = 0x0200
)

// AppDataRoots returns the three AppData folders for this account.
func AppDataRoots() []Root {
	var roots []Root
	local := os.Getenv("LOCALAPPDATA")
	if local != "" {
		roots = append(roots, Root{"Local", local})
		roots = append(roots, Root{"LocalLow", filepath.Join(filepath.Dir(local), "LocalLow")})
	}
	if roaming := os.Getenv("APPDATA"); roaming != "" {
		roots = append(roots, Root{"Roaming", roaming})
	}
	return roots
}

// ProgramFilesRoots lists both Program Files folders, once each.
func ProgramFilesRoots() []Root {
	var roots []Root
	seen := map[string]bool{}
	for _, r := range []Root{{"Program Files", os.Getenv("ProgramW6432")}, {"Program Files", os.Getenv("ProgramFiles")}, {"Program Files (x86)", os.Getenv("ProgramFiles(x86)")}} {
		if r.Path == "" || seen[strings.ToLower(filepath.Clean(r.Path))] {
			continue
		}
		seen[strings.ToLower(filepath.Clean(r.Path))] = true
		roots = append(roots, r)
	}
	return roots
}

func DataDir() string {
	if dataDirOverride != "" {
		return dataDirOverride
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserHomeDir()
	}
	dir := filepath.Join(base, "FolderTrace")
	copyOldDataDir(filepath.Join(base, "FolderScout"), dir)
	return dir
}

// ---- Registry, read only ----

func openKey(root syscall.Handle, path string, extra uint32) (syscall.Handle, bool) {
	var k syscall.Handle
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	if syscall.RegOpenKeyEx(root, p, 0, keyRead|extra, &k) != nil {
		return 0, false
	}
	return k, true
}

func regString(k syscall.Handle, name string) string {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return ""
	}
	var size, typ uint32
	if syscall.RegQueryValueEx(k, n, nil, &typ, nil, &size) != nil || size == 0 || size > 1<<16 {
		return ""
	}
	if typ != syscall.REG_SZ && typ != syscall.REG_EXPAND_SZ {
		return ""
	}
	buf := make([]uint16, size/2+1)
	if syscall.RegQueryValueEx(k, n, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &size) != nil {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func subKeys(k syscall.Handle) []string {
	var out []string
	for i := uint32(0); i < 20000; i++ {
		buf := make([]uint16, 512)
		size := uint32(len(buf))
		r, _, _ := procRegEnumKeyExW.Call(uintptr(k), uintptr(i), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0, 0, 0)
		if r != 0 {
			break
		}
		out = append(out, syscall.UTF16ToString(buf[:size]))
	}
	return out
}

func regValues(k syscall.Handle) map[string]string {
	out := map[string]string{}
	for i := uint32(0); i < 2000; i++ {
		name := make([]uint16, 512)
		nameLen := uint32(len(name))
		data := make([]uint16, 2048)
		dataLen := uint32(len(data) * 2)
		var typ uint32
		r, _, _ := procRegEnumValueW.Call(uintptr(k), uintptr(i), uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&nameLen)), 0,
			uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&dataLen)))
		if r == 259 { // no more items
			break
		}
		if r != 0 {
			continue
		}
		if typ == syscall.REG_SZ || typ == syscall.REG_EXPAND_SZ {
			out[syscall.UTF16ToString(name[:nameLen])] = syscall.UTF16ToString(data)
		}
	}
	return out
}

// ---- What's installed ----

// exeDir pulls the folder out of a value like "C:\Apps\x.exe",0 or C:\Apps\x.exe --uninstall.
func exeDir(s string) string {
	s = strings.TrimSpace(os.ExpandEnv(s))
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, `"`) {
		if i := strings.Index(s[1:], `"`); i > 0 {
			s = s[1 : i+1]
		}
	} else if i := strings.Index(strings.ToLower(s), ".exe"); i > 0 {
		s = s[:i+4]
	} else if i := strings.Index(s, ","); i > 0 {
		s = s[:i]
	}
	lower := strings.ToLower(s)
	if !strings.HasSuffix(lower, ".exe") && !strings.HasSuffix(lower, ".ico") {
		return ""
	}
	if strings.Contains(lower, `\windows\`) || strings.Contains(lower, `\package cache\`) || strings.Contains(lower, `\installer\`) {
		return ""
	}
	return filepath.Dir(s)
}

// tooBroad stops a path like C:\ or AppData\Local from matching everything.
func tooBroad(path string, roots []Root) bool {
	p := strings.ToLower(filepath.Clean(path))
	if len(p) <= 3 {
		return true
	}
	for _, r := range roots {
		rp := strings.ToLower(filepath.Clean(r.Path))
		if p == rp || strings.HasPrefix(rp, p+`\`) {
			return true
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramData", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "SystemRoot"} {
		if v := os.Getenv(env); v != "" && strings.EqualFold(filepath.Clean(v), filepath.Clean(path)) {
			return true
		}
	}
	base := strings.ToLower(filepath.Base(p))
	return base == "programs" || base == "common files"
}

func readInstalledPrograms(inv *Inventory, roots []Root) {
	const path = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	type where struct {
		root  syscall.Handle
		extra uint32
	}
	seen := map[string]bool{}
	for _, w := range []where{{syscall.HKEY_LOCAL_MACHINE, wow64Key}, {syscall.HKEY_LOCAL_MACHINE, wow32Key}, {syscall.HKEY_CURRENT_USER, 0}} {
		k, ok := openKey(w.root, path, w.extra)
		if !ok {
			continue
		}
		for _, sub := range subKeys(k) {
			s, ok := openKey(k, sub, w.extra)
			if !ok {
				continue
			}
			name := regString(s, "DisplayName")
			if name == "" {
				_ = syscall.RegCloseKey(s)
				continue
			}
			app := App{Name: name, Publisher: regString(s, "Publisher"), Source: sourceInstalled}
			installed := strings.TrimSpace(os.ExpandEnv(regString(s, "InstallLocation")))
			installed = strings.Trim(installed, `"`)
			if installed != "" && !tooBroad(installed, roots) {
				app.Paths = append(app.Paths, installed)
				if !exists(installed) {
					app.Broken = true
				}
			}
			for _, v := range []string{"DisplayIcon", "UninstallString", "QuietUninstallString"} {
				if d := exeDir(regString(s, v)); d != "" && !tooBroad(d, roots) {
					app.Paths = append(app.Paths, d)
				}
			}
			_ = syscall.RegCloseKey(s)
			key := strings.ToLower(name)
			if seen[key] {
				continue
			}
			seen[key] = true
			if installed != "" && exists(installed) {
				app.Aliases = folderAliases(installed)
			}
			inv.Apps = append(inv.Apps, app)
		}
		_ = syscall.RegCloseKey(k)
	}
}

var (
	vdfPath    = regexp.MustCompile(`(?m)^\s*"path"\s+"([^"]+)"`)
	acfName    = regexp.MustCompile(`(?m)^\s*"name"\s+"([^"]+)"`)
	acfInstall = regexp.MustCompile(`(?m)^\s*"installdir"\s+"([^"]+)"`)
)

func readSteam(inv *Inventory) {
	k, ok := openKey(syscall.HKEY_CURRENT_USER, `Software\Valve\Steam`, 0)
	if !ok {
		return
	}
	steam := filepath.FromSlash(regString(k, "SteamPath"))
	_ = syscall.RegCloseKey(k)
	if steam == "" {
		return
	}
	libs := []string{steam}
	if data, err := os.ReadFile(filepath.Join(steam, "steamapps", "libraryfolders.vdf")); err == nil {
		for _, m := range vdfPath.FindAllStringSubmatch(string(data), -1) {
			libs = append(libs, strings.ReplaceAll(m[1], `\\`, `\`))
		}
	}
	seen := map[string]bool{}
	for _, lib := range libs {
		key := strings.ToLower(filepath.Clean(lib))
		if seen[key] {
			continue
		}
		seen[key] = true
		manifests, _ := filepath.Glob(filepath.Join(lib, "steamapps", "appmanifest_*.acf"))
		for _, path := range manifests {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			name := acfName.FindStringSubmatch(string(data))
			dir := acfInstall.FindStringSubmatch(string(data))
			if name == nil || dir == nil {
				continue
			}
			install := filepath.Join(lib, "steamapps", "common", dir[1])
			inv.Apps = append(inv.Apps, App{Name: name[1], Source: sourceSteam, Paths: []string{install},
				Aliases: append(folderAliases(install), dir[1])})
		}
	}
}

func readEpic(inv *Inventory) {
	base := os.Getenv("ProgramData")
	if base == "" {
		return
	}
	items, _ := filepath.Glob(filepath.Join(base, "Epic", "EpicGamesLauncher", "Data", "Manifests", "*.item"))
	for _, path := range items {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m struct{ DisplayName, InstallLocation string }
		if json.Unmarshal(data, &m) != nil || m.DisplayName == "" {
			continue
		}
		inv.Apps = append(inv.Apps, App{Name: m.DisplayName, Source: sourceEpic, Paths: []string{m.InstallLocation},
			Aliases: folderAliases(m.InstallLocation)})
	}
}

func readGOG(inv *Inventory) {
	k, ok := openKey(syscall.HKEY_LOCAL_MACHINE, `SOFTWARE\GOG.com\Games`, wow32Key)
	if !ok {
		return
	}
	defer syscall.RegCloseKey(k)
	for _, sub := range subKeys(k) {
		s, ok := openKey(k, sub, wow32Key)
		if !ok {
			continue
		}
		name, path := regString(s, "gameName"), regString(s, "path")
		_ = syscall.RegCloseKey(s)
		if name != "" {
			inv.Apps = append(inv.Apps, App{Name: name, Source: sourceGOG, Paths: []string{path}, Aliases: folderAliases(path)})
		}
	}
}

func readStoreApps(inv *Inventory) {
	k, ok := openKey(syscall.HKEY_CURRENT_USER, `Software\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppModel\Repository\Packages`, 0)
	if !ok {
		return
	}
	defer syscall.RegCloseKey(k)
	seen := map[string]bool{}
	for _, full := range subKeys(k) {
		parts := strings.Split(full, "_")
		if len(parts) < 5 {
			continue
		}
		family := parts[0] + "_" + parts[len(parts)-1]
		if seen[family] {
			continue
		}
		seen[family] = true
		name := friendlyPackage(family)
		if s, ok := openKey(k, full, 0); ok {
			if d := resolveIndirect(regString(s, "DisplayName")); d != "" {
				name = d
			}
			_ = syscall.RegCloseKey(s)
		}
		inv.Apps = append(inv.Apps, App{Name: name, Source: sourceStore, Aliases: []string{family}})
	}
}

var procSHLoadIndirectString = systemDLL("shlwapi.dll").NewProc("SHLoadIndirectString")

// resolveIndirect turns a Store app's @{...} resource reference into its real name.
func resolveIndirect(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(s), "ms-resource:") {
		return "" // an unresolved reference, not a name
	}
	if !strings.HasPrefix(s, "@") {
		return s
	}
	src, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 512)
	if r, _, _ := procSHLoadIndirectString.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); r != 0 {
		return ""
	}
	out := syscall.UTF16ToString(buf)
	if strings.HasPrefix(strings.ToLower(out), "ms-resource:") || strings.HasPrefix(out, "@") {
		return ""
	}
	return out
}

// lnkTarget reads the program a Windows shortcut points to. It parses the file itself,
// so nothing is launched or resolved.
func lnkTarget(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return parseLnk(data)
}

func addShortcutTarget(inv *Inventory, name, target string, roots []Root) {
	low := strings.ToLower(target)
	if !strings.HasSuffix(low, ".exe") || strings.Contains(low, `\\windows\\`) {
		return
	}
	dir := filepath.Dir(target)
	if tooBroad(dir, roots) {
		return
	}
	app := App{Name: name, Source: sourceShortcut, Paths: []string{dir}}
	if exists(target) {
		app.Aliases = append(folderAliases(dir), strings.TrimSuffix(filepath.Base(target), filepath.Ext(target)))
		inv.Apps = append(inv.Apps, app)
	} else {
		app.Paths = nil
		app.Aliases = []string{filepath.Base(dir), strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))}
		inv.Gone = append(inv.Gone, app)
	}
}

// readShortcuts checks where Desktop shortcuts point. Plenty of games and portable apps
// have a shortcut but no entry in the installed programs list.
func readShortcuts(inv *Inventory, roots []Root) {
	var dirs []string
	for _, env := range []string{"USERPROFILE", "PUBLIC", "OneDrive"} {
		if v := os.Getenv(env); v != "" {
			dirs = append(dirs, filepath.Join(v, "Desktop"))
		}
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			ext := strings.ToLower(filepath.Ext(name))
			base := strings.TrimSuffix(name, filepath.Ext(name))
			switch ext {
			case ".lnk":
				if t := lnkTarget(filepath.Join(dir, name)); t != "" {
					addShortcutTarget(inv, base, t, roots)
				} else {
					inv.Apps = append(inv.Apps, App{Name: base, Source: sourceShortcut})
				}
			case ".url": // Steam and Epic game shortcuts
				inv.Apps = append(inv.Apps, App{Name: base, Source: sourceShortcut})
			}
		}
	}
}

// readRunHistory reads the list Windows keeps of programs it has run, which is how a
// portable app or a game from any launcher can still be named. It stays on this PC.
func readRunHistory(inv *Inventory, roots []Root) {
	k, ok := openKey(syscall.HKEY_CURRENT_USER, `Software\Classes\Local Settings\Software\Microsoft\Windows\Shell\MuiCache`, 0)
	if !ok {
		return
	}
	defer syscall.RegCloseKey(k)
	seen := map[string]bool{}
	for value, friendly := range regValues(k) {
		const suffix = ".FriendlyAppName"
		if !strings.HasSuffix(value, suffix) {
			continue
		}
		target := strings.TrimSuffix(value, suffix)
		low := strings.ToLower(target)
		if !strings.HasSuffix(low, ".exe") || seen[low] {
			continue
		}
		seen[low] = true
		if strings.Contains(low, `\windows\`) || strings.Contains(low, `\temp\`) || strings.Contains(low, `\downloads\`) ||
			strings.Contains(low, `\$recycle.bin\`) || strings.Contains(low, `setup`) || strings.Contains(low, `install`) {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
		dir := filepath.Dir(target)
		// An unplugged drive doesn't mean the program is gone.
		if vol := filepath.VolumeName(target); vol != "" && !exists(vol+`\`) {
			continue
		}
		lowBase := strings.ToLower(base)
		helper := false
		for _, h := range []string{"anticheat", "battleye", "crash", "unins", "vc_redist", "dxsetup", "bootstrap", "updater", "helper"} {
			helper = helper || strings.Contains(lowBase, h)
		}
		if helper {
			continue
		}
		// The name a program gives itself can be unrelated to it, so it's only trusted
		// when it shares a word with where the program is or what the file is called.
		name := strings.TrimSuffix(strings.TrimSpace(friendly), ".exe")
		place := filepath.Base(dir)
		for strings.EqualFold(place, "Win64") || strings.EqualFold(place, "bin") || strings.EqualFold(place, "Binaries") {
			dir2 := filepath.Dir(dir)
			if dir2 == dir {
				break
			}
			dir = dir2
			place = filepath.Base(dir)
		}
		where := append(tokens(place), tokens(base)...)
		if name == "" || genericProducts[norm(name)] || !sharesWord(tokens(name), where) {
			name = place
		}
		if exists(target) {
			if !tooBroad(dir, roots) {
				inv.Apps = append(inv.Apps, App{Name: name, Source: sourceRan, Paths: []string{dir},
					Aliases: append(folderAliases(dir), base, filepath.Base(dir))})
			}
		} else {
			inv.Gone = append(inv.Gone, App{Name: name, Source: sourceRan, Aliases: []string{base, filepath.Base(dir)}})
		}
	}
}

func readStartMenu(inv *Inventory) {
	roots := AppDataRoots()
	dirs := []string{}
	if v := os.Getenv("APPDATA"); v != "" {
		dirs = append(dirs, filepath.Join(v, `Microsoft\Windows\Start Menu\Programs`))
	}
	if v := os.Getenv("ProgramData"); v != "" {
		dirs = append(dirs, filepath.Join(v, `Microsoft\Windows\Start Menu\Programs`))
	}
	seen := map[string]bool{}
	add := func(name string) {
		low := strings.ToLower(name)
		if strings.HasPrefix(low, "uninstall") || strings.HasPrefix(low, "readme") || strings.HasPrefix(low, "help") || seen[low] {
			return
		}
		seen[low] = true
		inv.Apps = append(inv.Apps, App{Name: name, Source: sourceStart})
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				add(e.Name())
				subs, _ := os.ReadDir(filepath.Join(dir, e.Name()))
				for _, s := range subs {
					if strings.EqualFold(filepath.Ext(s.Name()), ".lnk") {
						base := strings.TrimSuffix(s.Name(), filepath.Ext(s.Name()))
						add(base)
						if t := lnkTarget(filepath.Join(dir, e.Name(), s.Name())); t != "" && !strings.HasPrefix(strings.ToLower(base), "uninstall") {
							addShortcutTarget(inv, base, t, roots)
						}
					}
				}
			} else if strings.EqualFold(filepath.Ext(e.Name()), ".lnk") {
				base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
				add(base)
				if t := lnkTarget(filepath.Join(dir, e.Name())); t != "" && !strings.HasPrefix(strings.ToLower(base), "uninstall") {
					addShortcutTarget(inv, base, t, roots)
				}
			}
		}
	}
}

func hasProgram(dir string, depth int) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".exe") {
			return true
		}
	}
	if depth > 0 {
		for _, e := range entries {
			if e.IsDir() && hasProgram(filepath.Join(dir, e.Name()), depth-1) {
				return true
			}
		}
	}
	return false
}

func readProgramFiles(inv *Inventory) {
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			low := strings.ToLower(e.Name())
			if !e.IsDir() || low == "common files" || low == "windowsapps" || strings.HasPrefix(low, "windows ") || low == "internet explorer" {
				continue
			}
			dir := filepath.Join(base, e.Name())
			if hasProgram(dir, 2) {
				inv.Apps = append(inv.Apps, App{Name: e.Name(), Source: sourceProgFiles, Paths: []string{dir}, Aliases: folderAliases(dir)})
			}
		}
	}
}

// readServices lists Windows services and the folders they run from. Updaters, anti-cheat
// and driver helpers in Program Files are often only services, with no shortcut.
func readServices(inv *Inventory) {
	k, ok := openKey(syscall.HKEY_LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, 0)
	if !ok {
		return
	}
	defer syscall.RegCloseKey(k)
	for _, name := range subKeys(k) {
		sk, ok := openKey(k, name, 0)
		if !ok {
			continue
		}
		image, display := regString(sk, "ImagePath"), regString(sk, "DisplayName")
		_ = syscall.RegCloseKey(sk)
		d := exeDir(image)
		if d == "" || !strings.Contains(strings.ToLower(d), `\program files`) {
			continue
		}
		if display == "" || strings.HasPrefix(display, "@") {
			display = name
		}
		inv.Apps = append(inv.Apps, App{Name: display, Source: sourceService, Paths: []string{d}})
	}
}

func readStartup(inv *Inventory, roots []Root) {
	k, ok := openKey(syscall.HKEY_CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, 0)
	if !ok {
		return
	}
	defer syscall.RegCloseKey(k)
	for name, value := range regValues(k) {
		app := App{Name: name, Source: sourceStartup}
		if d := exeDir(value); d != "" && !tooBroad(d, roots) {
			app.Paths = []string{d}
		}
		inv.Apps = append(inv.Apps, app)
	}
}

type processEntry32 struct {
	size     uint32
	usage    uint32
	pid      uint32
	heap     uintptr
	module   uint32
	threads  uint32
	parent   uint32
	priority int32
	flags    uint32
	exe      [260]uint16
}

const _ = uint(568 - unsafe.Sizeof(processEntry32{}))
const _ = uint(unsafe.Sizeof(processEntry32{}) - 568)

func readRunning(inv *Inventory) {
	snap, _, _ := procCreateToolhelp32Snapshot.Call(2, 0)
	if snap == 0 || snap == ^uintptr(0) {
		return
	}
	defer syscall.CloseHandle(syscall.Handle(snap))
	var e processEntry32
	e.size = uint32(unsafe.Sizeof(e))
	seen := map[string]bool{}
	r, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&e)))
	for r != 0 {
		name := syscall.UTF16ToString(e.exe[:])
		path := ""
		if h, _, _ := procOpenProcess.Call(0x1000, 0, uintptr(e.pid)); h != 0 {
			buf := make([]uint16, 1024)
			size := uint32(len(buf))
			if ok, _, _ := procQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); ok != 0 {
				path = syscall.UTF16ToString(buf[:size])
			}
			_ = syscall.CloseHandle(syscall.Handle(h))
		}
		// FolderTrace itself shouldn't make the folder it was run from look in use.
		if self, err := os.Executable(); err == nil && strings.EqualFold(self, path) {
			path = ""
		}
		if path != "" && strings.Contains(strings.ToLower(path), `\windows\`) {
			path = ""
		}
		key := strings.ToLower(name + "|" + path)
		if !seen[key] && path != "" {
			seen[key] = true
			inv.Running = append(inv.Running, Process{Name: name, Path: path})
		}
		e.size = uint32(unsafe.Sizeof(e))
		r, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&e)))
	}
}

// LoadInventory gathers everything the PC can say about what's installed. Each source
// is read independently, so one failing just means less evidence.
func LoadInventory(forceSearch bool, stop *atomic.Bool, status func(stage, current string)) *Inventory {
	inv := &Inventory{}
	roots := AppDataRoots()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, read := range []func(*Inventory){
		func(i *Inventory) { readInstalledPrograms(i, roots) },
		readSteam, readEpic, readGOG, readStoreApps, readStartMenu, readProgramFiles, readServices,
		func(i *Inventory) { readStartup(i, roots) }, readRunning,
		func(i *Inventory) { readShortcuts(i, roots) }, func(i *Inventory) { readRunHistory(i, roots) },
	} {
		wg.Add(1)
		go func(read func(*Inventory)) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					inv.Warnings = append(inv.Warnings, fmt.Sprint("One source couldn't be read: ", r))
					mu.Unlock()
				}
			}()
			part := &Inventory{}
			read(part)
			mu.Lock()
			inv.Apps = append(inv.Apps, part.Apps...)
			inv.Running = append(inv.Running, part.Running...)
			inv.Gone = append(inv.Gone, part.Gone...)
			mu.Unlock()
		}(read)
	}
	wg.Wait()
	inv.Steam = loadSteamIndex()
	loadPrograms2(inv, forceSearch, stop, status)
	return inv
}

func init() {
	driveConnected = func(p string) bool {
		v := filepath.VolumeName(p)
		return v != "" && exists(v+`\`)
	}
}

// loadSteamIndex reads the game data Steam keeps on this PC, if Steam is installed.
func loadSteamIndex() *SteamIndex {
	k, ok := openKey(syscall.HKEY_CURRENT_USER, `Software\Valve\Steam`, 0)
	if !ok {
		return nil
	}
	steam := filepath.FromSlash(regString(k, "SteamPath"))
	_ = syscall.RegCloseKey(k)
	if steam == "" {
		return nil
	}
	path := filepath.Join(steam, "appcache", "appinfo.vdf")
	if i, err := os.Stat(path); err != nil || i.Size() > 1<<30 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return buildSteamIndex(parseSteamAppInfo(data))
}

// fixedDrives lists hard drives and SSDs. Unplugged and network drives are left alone.
func fixedDrives() []string {
	mask, _, _ := procGetLogicalDrives.Call()
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := syscall.UTF16PtrFromString(root)
		if t, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(p))); t == 3 {
			out = append(out, root)
		}
	}
	return out
}

// loadPrograms2 keeps the list of programs on the drives current. The whole-drive search
// runs the first time and at most weekly. Every other scan rechecks the likely places.
func loadPrograms2(inv *Inventory, force bool, stop *atomic.Bool, status func(stage, current string)) {
	dir := DataDir()
	cache := loadPrograms(dir)
	first := cache.FullScan.IsZero()
	full := force || first || time.Since(cache.FullScan) > fullSearchEvery
	var fresh []ProgramDir
	if full {
		stage := "Searching your drives for programs..."
		if first {
			stage = "First-time setup: searching your drives for programs. This can take a few minutes."
		}
		if status != nil {
			status(stage, "")
		}
		fresh = searchPrograms(fixedDrives(), 8, stop, func(cur string) {
			if status != nil {
				status(stage, cur)
			}
		})
	} else {
		var users []string
		if h := os.Getenv("USERPROFILE"); h != "" {
			users = []string{filepath.Join(h, "Desktop"), filepath.Join(h, "Downloads"), filepath.Join(h, "Documents")}
		}
		if v := os.Getenv("OneDrive"); v != "" {
			users = append(users, filepath.Join(v, "Desktop"), filepath.Join(v, "Documents"))
		}
		if status != nil {
			status("Checking for new programs...", "")
		}
		fresh = searchPrograms(quickRoots(users, cache.Programs), 4, stop, nil)
	}
	if stop != nil && stop.Load() {
		addProgramsToInventory(inv, cache.Programs, nil) // a stopped search isn't saved
		return
	}
	kept, gone := mergePrograms(cache.Programs, fresh, exists, func(p string) bool {
		v := filepath.VolumeName(p)
		return v != "" && exists(v+`\`)
	})
	// A search that found nothing went wrong, so it can't count as done for the week.
	if full && len(fresh) > 0 {
		cache.FullScan = time.Now()
	} else if full {
		inv.Warnings = append(inv.Warnings, "The drive search found no programs, so it will run again next time.")
	}
	cache.Programs = kept
	if err := savePrograms(dir, cache); err != nil {
		inv.Warnings = append(inv.Warnings, "FolderTrace couldn't save its program list, so it will search again next time.")
	}
	addProgramsToInventory(inv, kept, gone)
}

// ReadProduct reads the product name stored inside a program, so a folder called
// "abc123" can still be named if the program in it says what it is.
func ReadProduct(f *Folder) {
	for _, path := range f.Exes {
		product, company, desc := versionStrings(path)
		// Windows' own programs all say "Microsoft Windows Operating System", but the
		// description says what they are: wsl.exe is Windows Subsystem for Linux.
		if product == "" || genericProducts[norm(cleanName(product))] {
			product = desc
		}
		if product != "" && !genericProducts[norm(cleanName(product))] {
			f.Product, f.Company = product, company
			return
		}
	}
}

func versionStrings(path string) (product, company, desc string) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", "", ""
	}
	size, _, _ := procGetFileVersionInfoSizeW.Call(uintptr(unsafe.Pointer(p)), 0)
	if size == 0 || size > 1<<22 {
		return "", "", ""
	}
	data := make([]byte, size)
	if r, _, _ := procGetFileVersionInfoW.Call(uintptr(unsafe.Pointer(p)), 0, size, uintptr(unsafe.Pointer(&data[0]))); r == 0 {
		return "", "", ""
	}
	query := func(sub string) (uintptr, uint32) {
		s, _ := syscall.UTF16PtrFromString(sub)
		var out uintptr
		var n uint32
		if r, _, _ := procVerQueryValueW.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(&out)), uintptr(unsafe.Pointer(&n))); r == 0 {
			return 0, 0
		}
		return out, n
	}
	langs := []string{"040904b0", "040904e4", "000004b0"}
	if t, n := query(`\VarFileInfo\Translation`); t != 0 && n >= 4 {
		pair := *(*[2]uint16)(ptr(t))
		langs = append([]string{fmt.Sprintf("%04x%04x", pair[0], pair[1])}, langs...)
	}
	read := func(lang, field string) string {
		t, n := query(`\StringFileInfo\` + lang + `\` + field)
		if t == 0 || n == 0 {
			return ""
		}
		return strings.TrimSpace(syscall.UTF16ToString(unsafe.Slice((*uint16)(ptr(t)), n)))
	}
	for _, lang := range langs {
		if product == "" {
			product = read(lang, "ProductName")
		}
		if company == "" {
			company = read(lang, "CompanyName")
		}
		if desc == "" {
			desc = read(lang, "FileDescription")
		}
	}
	return product, company, desc
}
