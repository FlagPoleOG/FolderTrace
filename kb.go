package main

import "strings"

// kbEntry is a folder name FolderTrace knows without checking what's installed.
type kbEntry struct {
	Owner  string
	Shared bool // part of Windows or shared between apps, so it's never a leftover
	Note   string
	Cache  bool   // everything in it is disposable cache
	Advice string // replaces the usual advice
	Group  bool   // a publisher or engine folder that several programs may share
}

func group(owner string) kbEntry { return kbEntry{Owner: owner, Group: true} }

func shared(owner, note string) kbEntry { return kbEntry{Owner: owner, Shared: true, Note: note} }

// graphicsCache is a shader cache: big, safe to rebuild, and best cleared by its owner.
func graphicsCache(owner, note, advice string) kbEntry {
	return kbEntry{Owner: owner, Shared: true, Note: note, Cache: true, Advice: advice}
}

// toolCache is a download cache for a developer tool. It's not tied to anything installed.
func toolCache(owner string) kbEntry {
	return kbEntry{Owner: owner, Cache: true}
}

const nvidiaCacheAdvice = "Don't delete this by hand while games are open. To use less space, lower Shader Cache Size in the NVIDIA Control Panel. Games may stutter briefly while it rebuilds."

const antiCheatNote = "This is an anti-cheat service that several games share, so it can't be tied to one game. Leave it unless you no longer play online games that use it."
const winNote = "This is a standard Windows folder."

// knownFolders is FolderTrace's own corrections list. It's hand-written from general
// knowledge, not copied from any outside list, and is meant to stay short. Most naming
// comes from evidence on each PC instead.
var knownFolders = map[string]kbEntry{
	// Windows
	"temp":                      shared("Windows", "This is the Windows temporary files folder. Apps and Windows use it constantly."),
	"microsoft":                 shared("Windows", winNote),
	"packages":                  shared("Windows", "This holds data for Microsoft Store apps."),
	"programs":                  shared("Windows", "This is where apps install themselves for just your account."),
	"connecteddevicesplatform":  shared("Windows", "Windows uses this for connected device features."),
	"comms":                     shared("Windows", "Windows uses this for built-in mail and calendar data."),
	"d3dscache":                 graphicsCache("Windows", "This is a graphics cache that Windows and games share. It rebuilds itself.", "To clear this, use Disk Cleanup and select DirectX Shader Cache. Games may stutter briefly while it rebuilds."),
	"crashdumps":                shared("Windows", "Windows saves app crash reports here."),
	"peerdistrepub":             shared("Windows", "Windows uses this for update sharing."),
	"publishers":                shared("Windows", "Windows uses this for Store app data shared by one publisher."),
	"virtualstore":              shared("Windows", "Windows redirects older apps' files here. Some older apps may rely on it."),
	"history":                   shared("Windows", "This is a standard Windows folder."),
	"placeholdertilelogofolder": shared("Windows", winNote),
	"microsoftcorporation":      shared("Windows", winNote),
	"elevateddiagnostics":       shared("Windows", "Windows uses this for diagnostic reports."),
	"diagnostics":               shared("Windows", "Windows uses this for diagnostic reports."),
	"windows":                   shared("Windows", winNote),
	"windowsapps":               shared("Windows", "This holds shortcuts for Microsoft Store apps."),
	"windowssidebar":            shared("Windows", winNote),
	"internetexplorer":          shared("Windows", winNote),
	"credentials":               shared("Windows", "Windows stores saved sign-ins here."),
	"crypto":                    shared("Windows", "Windows stores security keys here."),
	"protect":                   shared("Windows", "Windows stores security keys here. Removing it can break saved passwords."),
	"systemcertificates":        shared("Windows", "Windows stores security certificates here."),
	"vault":                     shared("Windows", "Windows stores saved sign-ins here."),
	"spelling":                  shared("Windows", winNote),
	"inputpersonalization":      shared("Windows", winNote),
	"feeds":                     shared("Windows", winNote),
	"feedscache":                shared("Windows", winNote),
	"clrv40":                    shared("Windows (.NET)", "This is shared by apps built with .NET."),
	"clrv20":                    shared("Windows (.NET)", "This is shared by apps built with .NET."),
	"clrv4032":                  shared("Windows (.NET)", "This is shared by apps built with .NET."),
	"gamedvr":                   shared("Windows", "This holds Windows game capture settings."),
	"mediaplayer":               shared("Windows", winNote),
	"msipc":                     shared("Windows", winNote),
	"powershell":                shared("Windows", "This holds PowerShell settings and history."),
	"tiles":                     shared("Windows", winNote),
	"tokenbroker":               shared("Windows", "Windows uses this for app sign-ins."),
	"terminalserverclient":      shared("Windows", "Remote Desktop keeps its cache here."),
	"windowsterminal":           shared("Windows", "This holds Windows Terminal data."),
	"common":                    shared("Shared", "Several apps often share this folder."),
	// Shared components
	"nvidia":            shared("NVIDIA graphics driver", "Your NVIDIA driver and apps use this. Shader caches here rebuild themselves."),
	"nvidiacorporation": shared("NVIDIA graphics driver", "Your NVIDIA driver and apps use this."),
	"glcache":           graphicsCache("NVIDIA graphics driver", "This is a graphics cache. It rebuilds itself.", nvidiaCacheAdvice),
	"dxcache":           graphicsCache("NVIDIA graphics driver", "This is a graphics cache that holds compiled shaders for your games. It rebuilds itself.", nvidiaCacheAdvice),
	"computecache":      graphicsCache("NVIDIA graphics driver", "This is a graphics cache for GPU compute apps. It rebuilds itself.", nvidiaCacheAdvice),
	"packagecache":      shared("App installers", "Installers keep copies here so apps can be repaired or removed later."),
	"cryptneturlcache":  shared("Windows", "Windows keeps security certificate downloads here."),
	"unrealengine":      shared("Unreal Engine", "Games built with Unreal Engine share this folder."),
	"cef":               shared("Shared browser component", "Apps built on Chromium may share this folder."),
	"squirreltemp":      shared("App installers", "App installers use this as temporary space."),
	"foldertrace":       shared("FolderTrace", "This is FolderTrace's own folder. It holds your right and wrong marks."),
	"folderscout":       shared("FolderTrace", "This is FolderTrace's folder from when it was called FolderScout. FolderTrace copied your marks to its new folder, so this one is no longer used."),
	// Publisher, engine, and launcher folders. Public lists tie these to single games, which is wrong.
	"ubisoft":                            group("Ubisoft games"),
	"rockstargames":                      group("Rockstar games"),
	"activision":                         group("Activision games"),
	"sega":                               group("SEGA games"),
	"bandainamcoentertainment":           group("Bandai Namco games"),
	"warnerbrosinteractiveentertainment": group("Warner Bros. games"),
	"2kgames":                            group("2K games"),
	"iointeractive":                      group("IO Interactive games"),
	"bethesdasoftworks":                  group("Bethesda games"),
	"bethesdanet":                        group("Bethesda games"),
	"squareenix":                         group("Square Enix games"),
	"capcom":                             group("Capcom games"),
	"konami":                             group("Konami games"),
	"paradoxinteractive":                 group("Paradox games"),
	"focusentertainment":                 group("Focus Entertainment games"),
	"thqnordic":                          group("THQ Nordic games"),
	"devolverdigital":                    group("Devolver Digital games"),
	"saber":                              group("Saber Interactive games"),
	"pearlabyss":                         group("Pearl Abyss games"),
	"ubereentertainment":                 group("Uber Entertainment games"),
	"uberentertainment":                  group("Uber Entertainment games"),
	"hangar13games":                      group("Hangar 13 games"),
	"netease":                            group("NetEase games"),
	"krafton":                            group("Krafton games"),
	"tencent":                            group("Tencent games"),
	"sonyinteractiveentertainment":       group("PlayStation PC games"),
	"playstation":                        group("PlayStation PC games"),
	"deepsilver":                         group("Deep Silver games"),
	"kochmedia":                          group("Koch Media games"),
	"tinybuild":                          group("tinyBuild games"),
	"annapurnainteractive":               group("Annapurna Interactive games"),
	"bungie":                             group("Bungie games"),
	"wargamingnet":                       group("Wargaming games"),
	"jagex":                              group("Jagex games"),
	"defaultcompany":                     group("Unity games with no company name set"),
	"myproject":                          group("Games with a default project name"),
	"godot":                              group("Games built with Godot"),
	"unity":                              group("Unity games and tools"),
	"unitytechnologies":                  group("Unity games and tools"),
	"gameanalytics":                      group("Game analytics shared by several games"),
	"unisdk":                             group("A game SDK shared by several games"),
	"punkbuster":                         {Owner: "PunkBuster anti-cheat", Shared: true, Note: antiCheatNote},
	"easyanticheat":                      {Owner: "Easy Anti-Cheat", Shared: true, Note: antiCheatNote},
	"battleye":                           {Owner: "BattlEye anti-cheat", Shared: true, Note: antiCheatNote},
	"eaanticheatinstallertool":           {Owner: "EA anti-cheat", Shared: true, Note: antiCheatNote},
	"hydralauncher":                      {Owner: "Hydra"},
	"hydralauncherupdater":               {Owner: "Hydra"},
	"gghydralauncherinstaller":           {Owner: "Hydra"},
	"ascendara":                          {Owner: "Ascendara"},
	"ascendarabytagoworks":               {Owner: "Ascendara"},
	"battlefieldgamedatakinreleasewin32": {Owner: "Battlefield 6"},
	"riotclientux":                       {Owner: "Riot Client"},
	"owelectron":                         {Owner: "Overwolf"},
	"steelseriesggclient":                {Owner: "SteelSeries GG"},
	"ciscosparklauncher":                 {Owner: "Webex"},
	"ciscospark":                         {Owner: "Webex"},
	"beamngdrive":                        {Owner: "BeamNG.drive"},
	"wwz":                                {Owner: "World War Z"},
	"roadcraftgame":                      {Owner: "RoadCraft"},
	"hitman3":                            {Owner: "HITMAN World of Assassination"},
	"pearlabyss/cd":                      {Owner: "Crimson Desert"},
	"epicgames":                          {Owner: "Epic Games"},
	"rockstargames/gtav":                 {Owner: "Grand Theft Auto V"},
	"rockstargames/gtavenhanced":         {Owner: "Grand Theft Auto V"},
	"kryptex":                            {Owner: "Kryptex"},
	"epiconlineservicesuihelper":         {Owner: "Epic Online Services"},
	"link2ea":                            {Owner: "EA app"},
	"robloxplayer":                       {Owner: "Roblox"},
	"roblox":                             {Owner: "Roblox"},
	"rockstargameslauncher":              {Owner: "Rockstar Games Launcher"},
	"lutris":                             {Owner: "Lutris"},
	"playnite":                           {Owner: "Playnite"},
	"wemod":                              {Owner: "WeMod"},
	"vortex":                             {Owner: "Vortex Mod Manager"},
	"modorganizer":                       {Owner: "Mod Organizer"},
	"wabbajack":                          {Owner: "Wabbajack"},
	"cemu":                               {Owner: "Cemu (Wii U emulator)"},
	"ryujinx":                            {Owner: "Ryujinx (Switch emulator)"},
	"yuzu":                               {Owner: "yuzu (Switch emulator)"},
	"dolphinemu":                         {Owner: "Dolphin (GameCube emulator)"},
	"rpcs3":                              {Owner: "RPCS3 (PS3 emulator)"},
	"pcsx2":                              {Owner: "PCSX2 (PS2 emulator)"},
	"retroarch":                          {Owner: "RetroArch"},
	"bluestacks":                         {Owner: "BlueStacks"},
	"genymobile":                         {Owner: "Genymotion (Android emulator)"},
	"nox":                                {Owner: "NoxPlayer"},
	"blender":                            {Owner: "Blender"},
	"blenderfoundation":                  {Owner: "Blender"},
	"gimp":                               {Owner: "GIMP"},
	"inkscape":                           {Owner: "Inkscape"},
	"krita":                              {Owner: "Krita"},
	"paintnet":                           {Owner: "Paint.NET"},
	"davinciresolve":                     {Owner: "DaVinci Resolve"},
	"blackmagicdesign":                   {Owner: "DaVinci Resolve"},
	"capcut":                             {Owner: "CapCut"},
	"ableton":                            {Owner: "Ableton Live"},
	"flstudio":                           {Owner: "FL Studio"},
	"imageline":                          {Owner: "FL Studio"},
	"plex":                               {Owner: "Plex"},
	"plexmediaserver":                    {Owner: "Plex Media Server"},
	"jellyfin":                           {Owner: "Jellyfin"},
	"kodi":                               {Owner: "Kodi"},
	"stremio":                            {Owner: "Stremio"},
	"mpv":                                {Owner: "mpv"},
	"mpchc":                              {Owner: "MPC-HC"},
	"itunes":                             {Owner: "iTunes"},
	"applecomputer":                      {Owner: "Apple apps"},
	"signaldesktop":                      {Owner: "Signal"},
	"signal":                             {Owner: "Signal"},
	"whatsapp":                           {Owner: "WhatsApp"},
	"skype":                              {Owner: "Skype"},
	"ciscowebexlauncher":                 {Owner: "Webex"},
	"webex":                              {Owner: "Webex"},
	"grammarly":                          {Owner: "Grammarly"},
	"notion":                             {Owner: "Notion"},
	"obsidian":                           {Owner: "Obsidian"},
	"dropbox":                            {Owner: "Dropbox"},
	"googledrivefs":                      {Owner: "Google Drive"},
	"box":                                {Owner: "Box"},
	"1password":                          {Owner: "1Password"},
	"bitwarden":                          {Owner: "Bitwarden"},
	"keepassxc":                          {Owner: "KeePassXC"},
	"lastpass":                           {Owner: "LastPass"},
	"nordvpn":                            {Owner: "NordVPN"},
	"protonvpn":                          {Owner: "Proton VPN"},
	"mullvadvpn":                         {Owner: "Mullvad VPN"},
	"malwarebytes":                       {Owner: "Malwarebytes"},
	"avastsoftware":                      {Owner: "Avast"},
	"avg":                                {Owner: "AVG"},
	"adguard":                            {Owner: "AdGuard"},
	"adguardsoftwarelimited":             {Owner: "AdGuard"},
	"openvpn":                            {Owner: "OpenVPN"},
	"wireshark":                          {Owner: "Wireshark"},
	"putty":                              {Owner: "PuTTY"},
	"filezilla":                          {Owner: "FileZilla"},
	"virtualbox":                         {Owner: "VirtualBox"},
	"vmware":                             {Owner: "VMware"},
	"hyper":                              {Owner: "Hyper terminal"},
	"github":                             {Owner: "GitHub tools"},
	"git":                                {Owner: "Git"},
	"gitkraken":                          {Owner: "GitKraken"},
	"sublimetext":                        {Owner: "Sublime Text"},
	"androidstudio":                      {Owner: "Android Studio"},
	"insomnia":                           {Owner: "Insomnia"},
	"nodejs":                             {Owner: "Node.js"},
	"node":                               {Owner: "Node.js"},
	"rust":                               {Owner: "Rust tools"},
	"ultimakerbv":                        {Owner: "Ultimaker Cura"},
	"cura":                               {Owner: "Ultimaker Cura"},
	"freecad":                            {Owner: "FreeCAD"},
	"steelseriesgg":                      {Owner: "SteelSeries GG"},
	"icue":                               {Owner: "Corsair iCUE"},
	"voicemod":                           {Owner: "Voicemod"},
	"voicemodv3":                         {Owner: "Voicemod"},
	"soundpad":                           {Owner: "Soundpad"},
	"wallpaperengine":                    {Owner: "Wallpaper Engine"},
	"powertoys":                          {Owner: "PowerToys"},
	"everything":                         {Owner: "Everything search"},
	"ccleaner":                           {Owner: "CCleaner"},
	"windirstat":                         {Owner: "WinDirStat"},
	"bcuninstaller":                      {Owner: "Bulk Crap Uninstaller"},
	"anthropicclaude":                    {Owner: "Claude"},
	"openai":                             {Owner: "OpenAI apps"},
	"jxbrowser":                          {Owner: "JxBrowser (a browser component used by other apps)"},
	"jxbrowserx64":                       {Owner: "JxBrowser (a browser component used by other apps)"},
	"onvue":                              {Owner: "Pearson OnVUE (online exams)"},
	// Well-known game codenames that don't look like the game's title.
	"tslgame":        {Owner: "PUBG: Battlegrounds"},
	"factorygame":    {Owner: "Satisfactory"},
	"fortnitegame":   {Owner: "Fortnite"},
	"pal":            {Owner: "Palworld"},
	"discovery":      {Owner: "The Finals"},
	"pioneergame":    {Owner: "ARC Raiders"},
	"athena":         {Owner: "Sea of Thieves"},
	"dungeoncrawler": {Owner: "Dark and Darker"},
	"oakgame":        {Owner: "Borderlands 3"},
	"squadgame":      {Owner: "Squad"},
	"hll":            {Owner: "Hell Let Loose"},
	"marvel":         {Owner: "Marvel Rivals"},
	"gzw":            {Owner: "Gray Zone Warfare"},
	"olk":            {Owner: "Microsoft Outlook"},
	// Apps and tools. Being on this list only names the owner.
	"steam":                 {Owner: "Steam"},
	"epicgameslauncher":     {Owner: "Epic Games Launcher"},
	"battlenet":             {Owner: "Battle.net"},
	"blizzardentertainment": {Owner: "Blizzard"},
	"origin":                {Owner: "EA Origin"},
	"eadesktop":             {Owner: "EA app"},
	"electronicarts":        {Owner: "Electronic Arts"},
	"ubisoftgamelauncher":   {Owner: "Ubisoft Connect"},
	"gogcom":                {Owner: "GOG Galaxy"},
	"riotgames":             {Owner: "Riot Games"},
	"overwolf":              {Owner: "Overwolf"},
	"medal":                 {Owner: "Medal.tv"},
	"curseforge":            {Owner: "CurseForge"},
	"minecraft":             {Owner: "Minecraft"},
	"prismlauncher":         {Owner: "Prism Launcher"},
	"atlauncher":            {Owner: "ATLauncher"},
	"discord":               {Owner: "Discord"},
	"spotify":               {Owner: "Spotify"},
	"slack":                 {Owner: "Slack"},
	"zoom":                  {Owner: "Zoom"},
	"telegramdesktop":       {Owner: "Telegram"},
	"teamviewer":            {Owner: "TeamViewer"},
	"anydesk":               {Owner: "AnyDesk"},
	"qbittorrent":           {Owner: "qBittorrent"},
	"vlc":                   {Owner: "VLC media player"},
	"obsstudio":             {Owner: "OBS Studio"},
	"handbrake":             {Owner: "HandBrake"},
	"audacity":              {Owner: "Audacity"},
	"chrome":                {Owner: "Google Chrome"},
	"edge":                  {Owner: "Microsoft Edge"},
	"edgewebview":           {Owner: "Microsoft Edge WebView"},
	"firefox":               {Owner: "Mozilla Firefox"},
	"thunderbird":           {Owner: "Mozilla Thunderbird"},
	"operasoftware":         {Owner: "Opera"},
	"bravesoftware":         {Owner: "Brave"},
	"vivaldi":               {Owner: "Vivaldi"},
	"onedrive":              {Owner: "Microsoft OneDrive"},
	"teams":                 {Owner: "Microsoft Teams"},
	"office":                {Owner: "Microsoft Office"},
	"visualstudio":          {Owner: "Visual Studio"},
	"code":                  {Owner: "Visual Studio Code"},
	"cursor":                {Owner: "Cursor"},
	"docker":                {Owner: "Docker"},
	"dockerdesktop":         {Owner: "Docker Desktop"},
	"githubdesktop":         {Owner: "GitHub Desktop"},
	"postman":               {Owner: "Postman"},
	"notepad":               {Owner: "Notepad++"},
	"7zip":                  {Owner: "7-Zip"},
	"winrar":                {Owner: "WinRAR"},
	"npm":                   toolCache("npm (Node.js)"),
	"npmcache":              toolCache("npm (Node.js)"),
	"nodegyp":               {Owner: "Node.js build tools"},
	"yarn":                  toolCache("Yarn"),
	"pnpm":                  toolCache("pnpm"),
	"pip":                   toolCache("pip (Python)"),
	"python":                {Owner: "Python"},
	"nuget":                 toolCache("NuGet (.NET)"),
	"gobuild":               toolCache("Go build cache"),
	"ollama":                {Owner: "Ollama"},
	"lmstudio":              {Owner: "LM Studio"},
	"android":               {Owner: "Android tools"},
	"razer":                 {Owner: "Razer"},
	"corsair":               {Owner: "Corsair"},
	"lghub":                 {Owner: "Logitech G HUB"},
	"logitech":              {Owner: "Logitech"},
	"steelseries":           {Owner: "SteelSeries"},
	"droptrace":             {Owner: "DropTrace"},
	"dropline":              {Owner: "DropTrace (old name)"},
}

func lookupKB(f *Folder) *kbEntry {
	if f.Loose {
		return nil
	}
	name := norm(f.Name)
	// A short name can be certain inside the right company's folder.
	if e, ok := knownFolders[norm(parentName(f))+"/"+name]; ok {
		return &e
	}
	if _, ok := knownFolders[name]; !ok {
		if base := norm(baseFolderName(f.Name)); base != "" {
			name = base // the program an updater or settings folder is named after
		}
	}
	if e, ok := knownFolders[name]; ok {
		// Short codenames only count when the folder also looks like a game's.
		if len(name) <= 7 && e.Owner != "" && !e.Shared && !e.Group && gameCodenames[name] && !looksLikeAGame(f) {
			return nil
		}
		// Only trust short, common words when they're where that name is expected.
		if e.Shared && (name == "history" || name == "feeds" || name == "common" || name == "windows" || name == "diagnostics" || name == "temp" || name == "packages" || name == "programs" || name == "microsoft" || name == "publishers" || name == "tiles" || name == "vault" || name == "credentials" || name == "crypto" || name == "protect") {
			p := strings.ToLower(parentName(f))
			if p != "" && p != "microsoft" && p != "programs" {
				return nil
			}
		}
		return &e
	}
	return nil
}

var gameCodenames = map[string]bool{"pal": true, "athena": true, "marvel": true, "hll": true, "gzw": true}
