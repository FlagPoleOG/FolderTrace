package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// ---- Program Files ----
//
// The Program Files tab reads the same way as the AppData one, but asks a different
// question: is anything installed still pointing at this folder? Then each Program Files
// folder is matched with the AppData folders of the same program, so either side can
// confirm or question the other.

// Folders that come with Windows or are shared by many programs.
var windowsProgramFolders = map[string]bool{
	"commonfiles": true, "windowsapps": true, "modifiablewindowsapps": true, "internetexplorer": true,
	"windowsdefender": true, "windowsdefenderadvancedthreatprotection": true, "windowsmail": true,
	"windowsmediaplayer": true, "windowsnt": true, "windowsphotoviewer": true, "windowsportabledevices": true,
	"windowssecurity": true, "windowssidebar": true, "windowspowershell": true, "windowsmultimediaplatform": true,
	"windowsjournal": true, "windowskits": true, "referenceassemblies": true, "msbuild": true, "dotnet": true,
	"microsoftnet": true, "uninstallinformation": true, "microsoftupdatehealthtools": true, "microsoftsdks": true,
	"installshieldinstallationinformation": true, "packagecache": true, "dotnetcore": true,
}

// Sources that prove a program is set up to run from a folder.
var strongSources = map[string]bool{sourceService: true, sourceInstalled: true, sourceSteam: true, sourceEpic: true, sourceGOG: true, sourceStore: true}

func isWindowsProgramFolder(f *Folder) bool {
	n := norm(f.Name)
	if windowsProgramFolders[n] || strings.HasPrefix(strings.ToLower(f.Name), "windows ") {
		return true
	}
	return norm(parentName(f)) == "commonfiles"
}

// appsPointingAt lists what's known to run from inside a folder, best evidence first.
func appsPointingAt(f *Folder, inv *Inventory) []*App {
	var out []*App
	for i := range inv.Apps {
		a := &inv.Apps[i]
		if a.Source == sourceProgFiles || a.Source == sourceDisk || a.Broken {
			continue
		}
		for _, p := range a.Paths {
			if samePathOrInside(p, f.Path) {
				out = append(out, a)
				break
			}
		}
	}
	rank := func(a *App) int {
		switch {
		case strongSources[a.Source]:
			return 0
		case a.Source == sourceStartup || a.Source == sourceShortcut || a.Source == sourceStart:
			return 1
		}
		return 2
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

// installedByName finds an installed program with this folder's name, wherever it is.
func installedByName(f *Folder, inv *Inventory) *App {
	want := norm(f.Name)
	if len(want) < 2 {
		return nil
	}
	for i := range inv.Apps {
		a := &inv.Apps[i]
		if !strongSources[a.Source] || a.Broken {
			continue
		}
		if n := norm(cleanName(a.Name)); (len(want) >= 4 && (n == want || n == norm(cleanName(f.Name)) || norm(a.Name) == want)) || (len(want) >= 5 && strings.HasPrefix(n, want)) || isInitials(f.Name, a.Name) {
			return a
		}
	}
	return nil
}

// ClassifyProgram labels a folder in Program Files.
func ClassifyProgram(f *Folder, inv *Inventory, now time.Time) {
	f.Program = true
	defer tidyOwner(f)
	evidence := func(s string) { f.Evidence = append(f.Evidence, s) }
	days := int(now.Sub(f.Newest).Hours() / 24)
	stale := !f.Newest.IsZero() && now.Sub(f.Newest) >= leftoverAge
	empty := f.Files == 0 && f.Unreadable == 0
	age := FormatAge(f.Newest, now)
	changed := "Its files last changed " + age + "."
	if f.Newest.IsZero() {
		changed = ""
	}
	name := func() (string, bool) {
		if p := usableProduct(f); p != "" {
			return p, true
		}
		if e := lookupKB(f); e != nil && !e.Shared && !e.Group {
			return e.Owner, true
		}
		return readableName(f.Name), false
	}
	uninstallAdvice := "If you don't want it, uninstall it from Settings > Apps instead of deleting this folder."

	if f.Loose || isWindowsProgramFolder(f) {
		f.Owner, f.Identified = "Windows", true
		if norm(f.Name) == "commonfiles" || norm(parentName(f)) == "commonfiles" {
			f.Owner = "Shared program files"
		}
		f.Verdict, f.Confidence = VerdictWindows, "High"
		f.Why = "This comes with Windows or is shared by many programs."
		f.Advice = "Leave this alone."
		evidence("A standard Windows or shared folder name")
		return
	}
	if empty {
		f.Owner, f.Guessed = readableName(f.Name), true
		f.Verdict, f.Confidence = VerdictLeftover, "High"
		f.Why = "This folder is empty, so nothing is installed in it."
		f.Advice = "Safe to remove. Deleting from Program Files needs administrator permission."
		evidence("0 files inside")
		return
	}
	if apps := appsPointingAt(f, inv); len(apps) > 0 {
		a := apps[0]
		f.app = a
		f.Owner, f.Identified = cleanName(a.Name), true
		f.Verdict = VerdictInUse
		switch {
		case a.Source == sourceService:
			f.Confidence = "High"
			f.Why = "A Windows service, " + f.Owner + ", runs from here."
			f.Advice = "Leave this. It goes away when you uninstall the program that added the service."
		case strongSources[a.Source]:
			f.Confidence = "High"
			f.Why = f.Owner + " is installed here."
			f.Advice = uninstallAdvice
		case a.Source == sourceRan:
			f.Confidence = "Medium"
			f.Why = "Windows remembers you running " + f.Owner + " from here, and it's still here. It isn't in your installed programs list, so it may have been copied here by hand."
			f.Advice = "If you don't use it anymore, check Settings > Apps first. If it isn't listed there, this folder can be removed by hand."
		default:
			f.Confidence = "High"
			f.Why = "A shortcut or startup entry runs " + f.Owner + " from here."
			f.Advice = uninstallAdvice
		}
		evidence("Found in " + a.Source)
		return
	}
	if proc, running := runningMatch(f, inv); running {
		f.Owner, f.Identified = cleanName(strings.TrimSuffix(proc.Name, ".exe")), true
		if n, ok := name(); ok {
			f.Owner = n
		}
		f.Verdict, f.Confidence = VerdictInUse, "High"
		f.Why = f.Owner + " is running right now from here."
		f.Advice = uninstallAdvice
		evidence("Running program: " + proc.Name)
		return
	}
	if hasDriverFiles(f.Path, 3) {
		f.Owner, f.Identified = readableName(f.Name), true
		if n, ok := name(); ok {
			f.Owner = n
		}
		f.Verdict, f.Confidence = VerdictWindows, "Medium"
		f.Why = "This holds driver files, which Windows loads for your hardware even when nothing else points here."
		f.Advice = "Leave this. Remove drivers through Device Manager or the maker's uninstaller, not by deleting files."
		evidence("Has driver files (.sys or .inf)")
		return
	}
	// Updaters and helpers count too: GoogleUpdater's only program is updater.exe.
	hasProgram := f.ProgBytes > 0 || len(f.Exes) > 0
	recent := !f.Newest.IsZero() && now.Sub(f.Newest) < 30*24*time.Hour
	veryOld := !f.Newest.IsZero() && now.Sub(f.Newest) >= 365*24*time.Hour
	if a := installedByName(f, inv); a != nil {
		f.app = a
		f.Owner, f.Identified = cleanName(a.Name), true
		elsewhere := ""
		for _, p := range a.Paths {
			if exists(p) {
				elsewhere = p
				break
			}
		}
		switch {
		case elsewhere == "":
			f.Verdict, f.Confidence = VerdictInUse, "Medium"
			f.Why = f.Owner + " is installed, and this folder has its name. Windows doesn't say where it's installed, so this is probably it."
			f.Advice = uninstallAdvice
		case hasProgram:
			f.Verdict, f.Confidence = VerdictUnsure, "Medium"
			f.Why = f.Owner + " is installed, but in " + elsewhere + ", not here. This may be an older copy. " + changed
			f.Advice = "Check which copy you actually open before removing either one."
		default:
			f.Verdict, f.Confidence = VerdictLeftover, "Medium"
			f.Why = f.Owner + " is installed in " + elsewhere + ". This folder has no program in it, so it's probably left from an older install. " + changed
			f.Advice = "Back up anything you want to keep, then this can be removed. Deleting from Program Files needs administrator permission."
		}
		evidence("Found in " + a.Source)
		return
	}
	owner, known := name()
	f.Owner, f.Identified, f.Guessed = owner, known, !known
	if known {
		evidence("Named by a program inside it")
	} else {
		evidence("The owner shown is only the folder's name")
	}
	evidence("Not in your installed programs, game libraries, shortcuts, or run history")
	switch {
	case recent:
		f.Verdict, f.Confidence = VerdictUnsure, "Medium"
		f.Why = "Nothing installed points here, but something changed it " + age + ", so it's probably still used by something. Programs that update in the background often look like this."
		f.Advice = "Leave this unless you know what it is."
	case !hasProgram:
		f.Verdict, f.Confidence = VerdictLeftover, "Medium"
		if stale {
			f.Confidence = "High"
		}
		f.Why = "Nothing installed points here, and there's no program left in it. Uninstallers often leave folders like this behind. " + changed
		f.Advice = "Back up any settings you want to keep, then this can be removed. Deleting from Program Files needs administrator permission."
	case veryOld:
		f.Verdict, f.Confidence = VerdictLeftover, "Medium"
		f.Why = "It has programs in it, but nothing installed points here, there's no shortcut to it, and Windows has no record of you running it. " + changed
		f.Advice = "Check Settings > Apps first. If it isn't listed there and you don't use it, it can be removed by hand."
	default:
		f.Verdict, f.Confidence = VerdictUnsure, "Medium"
		f.Why = "It has programs in it, but Windows doesn't list it as installed. It may be something copied here by hand, or a recent install that registers itself differently. " + changed
		f.Advice = "Leave this unless you know what it is."
	}
	_, _ = days, stale
}

func hasDriverFiles(dir string, depth int) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !e.IsDir() && (ext == ".sys" || ext == ".inf") {
			return true
		}
	}
	if depth > 0 {
		for _, e := range entries {
			if e.IsDir() && hasDriverFiles(filepath.Join(dir, e.Name()), depth-1) {
				return true
			}
		}
	}
	return false
}

// ScanProgramFiles reads Program Files the same way as AppData.
func ScanProgramFiles(roots []Root, inv *Inventory, stop *atomic.Bool, report func(Progress), product func(*Folder)) []*Folder {
	return scanWith(listTargets(roots), ClassifyProgram, inv, stop, report, product)
}

// Owners too broad to tie two folders together.
var vagueOwners = map[string]bool{"windows": true, "microsoft": true, "unknown": true, "nvidiagraphicsdriver": true,
	"temporaryfiles": true, "windowsandapps": true, "sharedprogramfiles": true, "amicrosoftstoreapp": true}

// CrossReference pairs Program Files folders with the AppData folders of the same
// program, then lets each side check the other.
func CrossReference(appData, progs []*Folder, now time.Time) {
	for _, a := range appData {
		a.Related = nil
	}
	for _, p := range progs {
		p.Related = nil
	}
	linked := func(a, p *Folder) bool {
		if a.Verdict == VerdictWindows || p.Verdict == VerdictWindows {
			return false
		}
		if a.app != nil {
			for _, path := range a.app.Paths {
				if samePathOrInside(path, p.Path) {
					return true
				}
			}
			if p.app == a.app {
				return true
			}
		}
		if a.RanFrom != "" && samePathOrInside(a.RanFrom, p.Path) {
			return true
		}
		an, pn := norm(a.Owner), norm(p.Owner)
		if len(an) >= 4 && an == pn && !vagueOwners[an] {
			return true
		}
		// A guessed name on the Program Files side is still the folder's own name.
		return len(an) >= 5 && an == norm(p.Name) && !vagueOwners[an]
	}
	for _, p := range progs {
		for _, a := range appData {
			if linked(a, p) {
				p.Related = append(p.Related, a)
				a.Related = append(a.Related, p)
			}
		}
	}
	recent := func(f *Folder) bool { return !f.Newest.IsZero() && now.Sub(f.Newest) <= 30*24*time.Hour }
	for _, p := range progs {
		if p.Verdict != VerdictLeftover && p.Verdict != VerdictUnsure {
			continue
		}
		// Data being written lately is a sign the program still runs.
		for _, a := range p.Related {
			if recent(a) && len(p.Exes) > 0 {
				p.Verdict, p.Confidence = VerdictInUse, "Medium"
				p.Why = "Windows doesn't list it as installed, but its AppData folder " + a.Rel + " changed " + FormatAge(a.Newest, now) + ", so the program is probably still used."
				p.Advice = "Leave this unless you know you don't use it."
				p.Evidence = append(p.Evidence, "Its AppData folder changed recently")
				break
			}
		}
	}
	for _, a := range appData {
		if a.Verdict != VerdictInUse || a.app == nil || (a.app.Source != sourceProgFiles && a.app.Source != sourceDisk) {
			continue
		}
		// It was only called in use because its program folder exists. If that folder
		// looks left behind itself, so might this.
		for _, p := range a.Related {
			if p.Verdict == VerdictLeftover && !recent(a) {
				a.Verdict, a.Confidence = VerdictUnsure, "Medium"
				a.Why = fmt.Sprintf("Its program folder %s is still there, but nothing installed points to it and it looks left behind, so this may be left over too. It last changed %s.", p.Path, FormatAge(a.Newest, now))
				a.Evidence = append(a.Evidence, "Its Program Files folder looks left over")
				break
			}
		}
	}
}

// RelatedText describes the matching folders for the details panel and the report.
func RelatedText(f *Folder, now time.Time) string {
	if len(f.Related) == 0 {
		return ""
	}
	var parts []string
	for i, r := range f.Related {
		if i == 4 {
			parts = append(parts, fmt.Sprintf("and %d more", len(f.Related)-4))
			break
		}
		where := r.Rel
		if r.Program {
			where = r.Path
		}
		parts = append(parts, fmt.Sprintf("%s (%s, %s)", where, FormatSize(r.Size), strings.ToLower(r.Verdict.String())))
	}
	label := "Its AppData: "
	if !f.Program {
		label = "Its program folder: "
		if len(f.Related) > 1 {
			label = "Its program folders: "
		}
	}
	return label + strings.Join(parts, "; ")
}
