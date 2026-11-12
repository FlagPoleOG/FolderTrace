//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const windowClassName = "FolderTraceMainWindow"

var (
	user32   = systemDLL("user32.dll")
	shell32  = systemDLL("shell32.dll")
	gdi32    = systemDLL("gdi32.dll")
	comctl32 = systemDLL("comctl32.dll")
	comdlg32 = systemDLL("comdlg32.dll")
	uxtheme  = systemDLL("uxtheme.dll")
	dwmapi   = systemDLL("dwmapi.dll")

	procRegisterClassExW       = user32.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDefWindowProcW         = user32.NewProc("DefWindowProcW")
	procCallWindowProcW        = user32.NewProc("CallWindowProcW")
	procSetWindowLongPtrW      = user32.NewProc("SetWindowLongPtrW")
	procGetMessageW            = user32.NewProc("GetMessageW")
	procTranslateMessage       = user32.NewProc("TranslateMessage")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")
	procIsDialogMessageW       = user32.NewProc("IsDialogMessageW")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procSendMessageW           = user32.NewProc("SendMessageW")
	procPostQuitMessage        = user32.NewProc("PostQuitMessage")
	procShowWindow             = user32.NewProc("ShowWindow")
	procMoveWindow             = user32.NewProc("MoveWindow")
	procSetWindowPos           = user32.NewProc("SetWindowPos")
	procGetClientRect          = user32.NewProc("GetClientRect")
	procInvalidateRect         = user32.NewProc("InvalidateRect")
	procBeginPaint             = user32.NewProc("BeginPaint")
	procEndPaint               = user32.NewProc("EndPaint")
	procFillRect               = user32.NewProc("FillRect")
	procDrawTextW              = user32.NewProc("DrawTextW")
	procGetDpiForWindow        = user32.NewProc("GetDpiForWindow")
	procGetDpiForSystem        = user32.NewProc("GetDpiForSystem")
	procLoadCursorW            = user32.NewProc("LoadCursorW")
	procTrackMouseEvent        = user32.NewProc("TrackMouseEvent")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW   = user32.NewProc("GetWindowTextLengthW")
	procSetWindowTextW         = user32.NewProc("SetWindowTextW")
	procSetFocus               = user32.NewProc("SetFocus")
	procMessageBoxW            = user32.NewProc("MessageBoxW")
	procOpenClipboard          = user32.NewProc("OpenClipboard")
	procCloseClipboard         = user32.NewProc("CloseClipboard")
	procEmptyClipboard         = user32.NewProc("EmptyClipboard")
	procSetClipboardData       = user32.NewProc("SetClipboardData")
	procExtractIconExW         = shell32.NewProc("ExtractIconExW")
	procShellExecuteW          = shell32.NewProc("ShellExecuteW")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	procSetTextColor           = gdi32.NewProc("SetTextColor")
	procSetBkColor             = gdi32.NewProc("SetBkColor")
	procSetBkMode              = gdi32.NewProc("SetBkMode")
	procCreateFontW            = gdi32.NewProc("CreateFontW")
	procInitCommonControlsEx   = comctl32.NewProc("InitCommonControlsEx")
	procGetSaveFileNameW       = comdlg32.NewProc("GetSaveFileNameW")
	procSetWindowTheme         = uxtheme.NewProc("SetWindowTheme")
	procDwmSetWindowAttribute  = dwmapi.NewProc("DwmSetWindowAttribute")
	procGlobalAlloc            = kernel32.NewProc("GlobalAlloc")
	procGlobalLock             = kernel32.NewProc("GlobalLock")
	procGlobalUnlock           = kernel32.NewProc("GlobalUnlock")
	procGlobalFree             = kernel32.NewProc("GlobalFree")
	procGetModuleHandleW       = kernel32.NewProc("GetModuleHandleW")
	procCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	procAppendMenuW            = user32.NewProc("AppendMenuW")
	procTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	procDestroyMenu            = user32.NewProc("DestroyMenu")
	procGetWindowRect          = user32.NewProc("GetWindowRect")
)

const (
	wmDestroy       = 0x0002
	wmSize          = 0x0005
	wmPaint         = 0x000F
	wmClose         = 0x0010
	wmEraseBkgnd    = 0x0014
	wmSettingChange = 0x001A
	wmGetMinMaxInfo = 0x0024
	wmSetFont       = 0x0030
	wmDrawItem      = 0x002B
	wmNotify        = 0x004E
	wmCommand       = 0x0111
	wmCtlColorEdit  = 0x0133
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmLButtonDbl    = 0x0203
	wmMouseLeave    = 0x02A3
	wmDpiChanged    = 0x02E0
	wmApp           = 0x8000
	wmScanProgress  = wmApp + 1
	wmScanDone      = wmApp + 2

	wsChild, wsVisible, wsTabStop = 0x40000000, 0x10000000, 0x00010000
	wsBorder, wsClipChildren      = 0x00800000, 0x02000000
	wsOverlappedWindow            = 0x00CF0000
	wsExControlParent             = 0x00010000
	cwUseDefault                  = 0x80000000
	bsOwnerDraw                   = 0xB
	esAutoHScroll                 = 0x80
	swHide, swShow                = 0, 5
	emSetCueBanner                = 0x1501
	enChange                      = 0x0300
	enKillFocus                   = 0x0200

	dtLeft, dtCenter, dtRight = 0, 1, 2
	dtVCenter, dtSingleLine   = 4, 0x20
	dtWordBreak, dtCalcRect   = 0x10, 0x400
	dtNoPrefix, dtEndEllipsis = 0x800, 0x8000
	dtPathEllipsis            = 0x4000

	odsSelected, odsFocus = 0x1, 0x10
	odsNoFocusRect        = 0x200
	tmeLeave              = 0x2
	gwlpWndProc           = ^uintptr(3) // -4

	lvsReport, lvsSingleSel, lvsShowSel, lvsOwnerData = 0x1, 0x4, 0x8, 0x1000
	lvmSetBkColor                                     = 0x1001
	lvmGetNextItem                                    = 0x100C
	lvmEnsureVisible                                  = 0x1013
	lvmRedrawItems                                    = 0x1015
	lvmSetColumnWidth                                 = 0x101E
	lvmGetHeader                                      = 0x101F
	lvmSetTextColor                                   = 0x1024
	lvmSetTextBkColor                                 = 0x1026
	lvmSetItemState                                   = 0x102B
	lvmSetItemCount                                   = 0x102F
	lvmSetExtStyle                                    = 0x1036
	lvmInsertColumnW                                  = 0x1061

	nmDblClk, nmReturn, nmCustomDraw = 0xFFFFFFFD, 0xFFFFFFFC, 0xFFFFFFF4
	nmClick, lvnKeyDown              = 0xFFFFFFFE, 0xFFFFFF65
	lvnItemChanged, lvnColumnClick   = 0xFFFFFF9B, 0xFFFFFF94
	lvnGetDispInfoW                  = 0xFFFFFF4F
)

const (
	kindSeg = iota
	kindPrimary
	kindSecondary
	kindCheck
)

const (
	idAll = 100 + iota
	idLeftover
	idUnsure
	idInUse
	idWindows
	idSaves
	idRescan
	idSave
	idStop
	idSearchDrives
	idOpen
	idCopy
	idRight
	idWrong
	idSearch
	idNote
	idList
	idTabApp
	idTabProg
	idMore
	idDelete
	idFeedback
	idMoreApps
)

type winRect struct{ l, t, r, b int32 }

func (r winRect) w() int32 { return r.r - r.l }
func (r winRect) h() int32 { return r.b - r.t }

type point struct{ x, y int32 }

type paintStruct struct {
	hdc      uintptr
	fErase   int32
	rc       winRect
	fRest    int32
	fUpdate  int32
	reserved [32]byte
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

type drawItemStruct struct {
	ctlType, ctlID, itemID, itemAction, itemState uint32
	hwndItem, hDC                                 uintptr
	rc                                            winRect
	itemData                                      uintptr
}

type lvColumn struct {
	mask             uint32
	fmt              int32
	cx               int32
	pszText          *uint16
	cchTextMax       int32
	iSubItem         int32
	iImage, iOrder   int32
	cxMin, cxDefault int32
	cxIdeal          int32
}

type lvItem struct {
	mask             uint32
	iItem, iSubItem  int32
	state, stateMask uint32
	pszText          *uint16
	cchTextMax       int32
	iImage           int32
	lParam           uintptr
	iIndent          int32
	iGroupId         int32
	cColumns         uint32
	puColumns        uintptr
	piColFmt         uintptr
	iGroup           int32
}

type openFileName struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	Flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	FlagsEx           uint32
}

type trackMouse struct {
	cbSize, dwFlags uint32
	hwnd            uintptr
	hover           uint32
}

type initCommon struct{ size, icc uint32 }

// These fail to compile if a Win32 structure doesn't have its documented 64-bit size.
const (
	_ = uint(80 - unsafe.Sizeof(wndClassEx{}))
	_ = uint(unsafe.Sizeof(wndClassEx{}) - 80)
	_ = uint(48 - unsafe.Sizeof(winMsg{}))
	_ = uint(unsafe.Sizeof(winMsg{}) - 48)
	_ = uint(64 - unsafe.Sizeof(drawItemStruct{}))
	_ = uint(unsafe.Sizeof(drawItemStruct{}) - 64)
	_ = uint(56 - unsafe.Sizeof(lvColumn{}))
	_ = uint(unsafe.Sizeof(lvColumn{}) - 56)
	_ = uint(88 - unsafe.Sizeof(lvItem{}))
	_ = uint(unsafe.Sizeof(lvItem{}) - 88)
	_ = uint(152 - unsafe.Sizeof(openFileName{}))
	_ = uint(unsafe.Sizeof(openFileName{}) - 152)
	_ = uint(72 - unsafe.Sizeof(paintStruct{}))
	_ = uint(unsafe.Sizeof(paintStruct{}) - 72)
)

func ptr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }
func wstr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(strings.ReplaceAll(s, "\x00", ""))
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

//go:uintptrescapes
func send(h uintptr, msg uint32, w, l uintptr) uintptr {
	r, _, _ := procSendMessageW.Call(h, uintptr(msg), w, l)
	return r
}

type palette struct {
	bg, panel, ink, muted, line, track, accent, accentInk, up, warn, unk, down uint32
	dark                                                                       bool
}

var lightTheme = palette{bg: 0xF0F3F5, panel: 0xFFFFFF, ink: 0x14222C, muted: 0x51616D, line: 0xC8D2D9, track: 0xDAE2E7,
	accent: 0x17506E, accentInk: 0xFFFFFF, up: 0x17744A, warn: 0x9A5700, unk: 0x5F6E78, down: 0xC02E25}
var darkTheme = palette{bg: 0x172229, panel: 0x1F2D35, ink: 0xE9EFF2, muted: 0xA3B2BC, line: 0x3A4B56, track: 0x2E3E48,
	accent: 0x7CC0E3, accentInk: 0x0F222C, up: 0x54C991, warn: 0xF2B65C, unk: 0xA9B7C0, down: 0xFF8378, dark: true}

func colorRef(c uint32) uintptr { return uintptr((c>>16)&0xFF | c&0xFF00 | (c&0xFF)<<16) }

func systemUsesDark() bool {
	k, ok := openKey(syscall.HKEY_CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, 0)
	if !ok {
		return false
	}
	defer syscall.RegCloseKey(k)
	var v, size uint32 = 1, 4
	if syscall.RegQueryValueEx(k, wstr("AppsUseLightTheme"), nil, nil, (*byte)(unsafe.Pointer(&v)), &size) != nil {
		return false
	}
	return v == 0
}

type control struct {
	hwnd  uintptr
	id    int
	kind  int
	text  string
	on    bool
	hover bool
	orig  uintptr
}

// Columns in the list.
const (
	colPick = iota
	colOwner
	colStatus
	colSize
	colChanged
	colFolder
)

var colTitles = []string{"☐", "Belongs to", "Status", "Size", "Last changed", "Folder"}

type winUI struct {
	hwnd, instance, list, header, listOrig uintptr
	search, note                           uintptr
	dpi, width, height                     int32
	theme                                  palette
	bgBrush, editBrush                     uintptr
	fH2, fFig, fBody, fBold, fSmall        uintptr
	controls                               map[int]*control
	byHwnd                                 map[uintptr]*control

	rcStats, rcList, rcDetails winRect

	dataDir string
	marks   Marks
	all     []*Folder // the folders on the tab being shown
	allApp  []*Folder
	allProg []*Folder
	checked map[string]bool // paths the person picked to remove
	tab     int             // idTabApp or idTabProg
	view    []*Folder
	sum     Summary
	warns   []string
	selPath string

	filter    int // an id from idAll to idWindows
	savesOnly bool
	sortCol   int
	sortAsc   bool
	loadingUI bool

	scanning     bool
	stop         *atomic.Bool
	progMu       sync.Mutex
	prog         Progress
	stage        string
	progPending  atomic.Bool
	scanResult   []*Folder
	scanInv      *Inventory
	scanProgs    []*Folder
	scanCanceled bool
	scanErr      string
	msg          string
	msgErr       bool
	now          time.Time
}

var theUI *winUI

func fatal(msg string) {
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(wstr(msg))), uintptr(unsafe.Pointer(wstr("FolderTrace"))), 0x10)
	os.Exit(1)
}

func (u *winUI) S(v int32) int32 { return v * u.dpi / 96 }

func runUI() {
	runtime.LockOSThread()
	u := &winUI{checked: map[string]bool{}, controls: map[int]*control{}, byHwnd: map[uintptr]*control{}, filter: idLeftover, tab: idTabApp, sortCol: colSize, dpi: 96, now: time.Now()}
	u.dataDir = DataDir()
	u.marks = LoadMarks(u.dataDir)
	theUI = u
	icc := initCommon{size: 8, icc: 0x1 | 0x4000}
	_, _, _ = procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))
	u.instance, _, _ = procGetModuleHandleW.Call(0)
	if procGetDpiForSystem.Find() == nil {
		if d, _, _ := procGetDpiForSystem.Call(); d != 0 {
			u.dpi = int32(d)
		}
	}
	u.loadTheme()
	var large, small uintptr
	if exe, err := os.Executable(); err == nil {
		_, _, _ = procExtractIconExW.Call(uintptr(unsafe.Pointer(wstr(exe))), 0,
			uintptr(unsafe.Pointer(&large)), uintptr(unsafe.Pointer(&small)), 1)
	}
	cursor, _, _ := procLoadCursorW.Call(0, 32512)
	wc := wndClassEx{style: 0x3, lpfnWndProc: syscall.NewCallback(windowProc), hInstance: u.instance,
		lpszClassName: wstr(windowClassName), hIcon: large, hIconSm: small, hCursor: cursor}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		fatal("FolderTrace couldn't create its window: " + err.Error())
	}
	cw := uintptr(cwUseDefault)
	hwnd, _, err := procCreateWindowExW.Call(wsExControlParent, uintptr(unsafe.Pointer(wstr(windowClassName))),
		uintptr(unsafe.Pointer(wstr("FolderTrace"))), wsOverlappedWindow|wsClipChildren,
		cw, cw, uintptr(u.S(1240)), uintptr(u.S(820)), 0, 0, u.instance, 0)
	if hwnd == 0 {
		fatal("FolderTrace couldn't create its window: " + err.Error())
	}
	u.hwnd = hwnd
	if procGetDpiForWindow.Find() == nil {
		if d, _, _ := procGetDpiForWindow.Call(hwnd); d != 0 {
			u.dpi = int32(d)
		}
	}
	u.applyTitleBar()
	u.makeFonts()
	u.createControls()
	u.applyListTheme()
	u.layout()
	_, _, _ = procShowWindow.Call(hwnd, swShow)
	demoStart(hwnd)
	u.startScan(false)

	var msg winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		if d, _, _ := procIsDialogMessageW.Call(u.hwnd, uintptr(unsafe.Pointer(&msg))); d != 0 {
			continue
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (u *winUI) loadTheme() {
	u.theme = lightTheme
	if systemUsesDark() {
		u.theme = darkTheme
	}
	if u.bgBrush != 0 {
		_, _, _ = procDeleteObject.Call(u.bgBrush)
		_, _, _ = procDeleteObject.Call(u.editBrush)
	}
	u.bgBrush, _, _ = procCreateSolidBrush.Call(colorRef(u.theme.bg))
	u.editBrush, _, _ = procCreateSolidBrush.Call(colorRef(u.theme.panel))
}

func (u *winUI) applyTitleBar() {
	var dark uint32
	if u.theme.dark {
		dark = 1
	}
	if procDwmSetWindowAttribute.Find() == nil {
		_, _, _ = procDwmSetWindowAttribute.Call(u.hwnd, 20, uintptr(unsafe.Pointer(&dark)), 4)
	}
}

func (u *winUI) applyListTheme() {
	name := "Explorer"
	if u.theme.dark {
		name = "DarkMode_Explorer"
	}
	if procSetWindowTheme.Find() == nil && u.list != 0 {
		_, _, _ = procSetWindowTheme.Call(u.list, uintptr(unsafe.Pointer(wstr(name))), 0)
	}
	t := u.theme
	send(u.list, lvmSetBkColor, 0, colorRef(t.panel))
	send(u.list, lvmSetTextBkColor, 0, colorRef(t.panel))
	send(u.list, lvmSetTextColor, 0, colorRef(t.ink))
}

func (u *winUI) makeFont(face string, size, weight int32) uintptr {
	h, _, _ := procCreateFontW.Call(uintptr(int64(-int64(u.S(size)))), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(wstr(face))))
	return h
}

func (u *winUI) makeFonts() {
	for _, f := range []uintptr{u.fH2, u.fFig, u.fBody, u.fBold, u.fSmall} {
		if f != 0 {
			_, _, _ = procDeleteObject.Call(f)
		}
	}
	u.fH2 = u.makeFont("Bahnschrift SemiBold", 22, 600)
	u.fFig = u.makeFont("Bahnschrift SemiBold", 24, 600)
	u.fBody = u.makeFont("Segoe UI", 15, 400)
	u.fBold = u.makeFont("Segoe UI Semibold", 15, 600)
	u.fSmall = u.makeFont("Segoe UI", 13, 400)
	for _, c := range u.controls {
		send(c.hwnd, wmSetFont, u.fBody, 1)
	}
	for _, e := range []uintptr{u.search, u.note, u.list} {
		if e != 0 {
			send(e, wmSetFont, u.fBody, 1)
		}
	}
}

// ---- Controls ----

var buttonProc = syscall.NewCallback(func(hwnd, msg, wParam, lParam uintptr) uintptr {
	u := theUI
	c := u.byHwnd[hwnd]
	if c == nil {
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
		return r
	}
	switch uint32(msg) {
	case wmMouseMove:
		if !c.hover {
			c.hover = true
			tm := trackMouse{dwFlags: tmeLeave, hwnd: hwnd}
			tm.cbSize = uint32(unsafe.Sizeof(tm))
			_, _, _ = procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tm)))
			_, _, _ = procInvalidateRect.Call(hwnd, 0, 0)
		}
	case wmMouseLeave:
		c.hover = false
		_, _, _ = procInvalidateRect.Call(hwnd, 0, 0)
	case wmEraseBkgnd:
		return 1
	case wmLButtonDbl:
		msg = wmLButtonDown
	}
	r, _, _ := procCallWindowProcW.Call(c.orig, hwnd, msg, wParam, lParam)
	return r
})

var listProc = syscall.NewCallback(func(hwnd, msg, wParam, lParam uintptr) uintptr {
	u := theUI
	if uint32(msg) == wmNotify {
		from := *(*uintptr)(ptr(lParam))
		code := *(*uint32)(ptr(lParam + 16))
		if from == u.header && code == nmCustomDraw {
			switch *(*uint32)(ptr(lParam + 24)) {
			case 1:
				// Paint the whole strip, so the area past the last column isn't left white.
				u.fill(*(*uintptr)(ptr(lParam + 32)), *(*winRect)(ptr(lParam + 40)), u.theme.panel)
				return 0x20 | 0x10
			case 2:
				// Windows paints the strip past the last column white in dark mode, so
				// cover it once everything else is drawn.
				hdc := *(*uintptr)(ptr(lParam + 32))
				var last, all winRect
				send(u.header, 0x1207, uintptr(len(colTitles)-1), uintptr(unsafe.Pointer(&last)))
				_, _, _ = procGetClientRect.Call(u.header, uintptr(unsafe.Pointer(&all)))
				if last.r < all.r {
					u.fill(hdc, winRect{last.r, all.t, all.r, all.b}, u.theme.panel)
				}
				return 0
			case 0x10001:
				hdc := *(*uintptr)(ptr(lParam + 32))
				rc := *(*winRect)(ptr(lParam + 40))
				item := int(*(*uintptr)(ptr(lParam + 56)))
				u.fill(hdc, rc, u.theme.panel)
				if item < len(colTitles) {
					title := colTitles[item]
					if item == u.sortCol {
						if u.sortAsc {
							title += " ▲"
						} else {
							title += " ▼"
						}
					}
					text := rc
					text.l += u.S(6)
					text.b--
					u.text(hdc, title, u.fBold, u.theme.ink, text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
				}
				u.fill(hdc, winRect{rc.l, rc.b - 1, rc.r, rc.b}, u.theme.line)
				return 4
			}
		}
	}
	r, _, _ := procCallWindowProcW.Call(u.listOrig, hwnd, msg, wParam, lParam)
	return r
})

func (u *winUI) addButton(id, kind int, text string) *control {
	h, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(wstr("BUTTON"))), uintptr(unsafe.Pointer(wstr(text))),
		wsChild|wsVisible|wsTabStop|bsOwnerDraw, 0, 0, 10, 10, u.hwnd, uintptr(id), u.instance, 0)
	c := &control{hwnd: h, id: id, kind: kind, text: text}
	u.controls[id] = c
	u.byHwnd[h] = c
	c.orig, _, _ = procSetWindowLongPtrW.Call(h, gwlpWndProc, buttonProc)
	send(h, wmSetFont, u.fBody, 1)
	return c
}

func (u *winUI) addEdit(id int, cue string) uintptr {
	h, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(wstr("EDIT"))), uintptr(unsafe.Pointer(wstr(""))),
		wsChild|wsVisible|wsTabStop|wsBorder|esAutoHScroll, 0, 0, 10, 10, u.hwnd, uintptr(id), u.instance, 0)
	send(h, wmSetFont, u.fBody, 1)
	send(h, emSetCueBanner, 1, uintptr(unsafe.Pointer(wstr(cue))))
	return h
}

func (u *winUI) createControls() {
	u.addButton(idTabApp, kindSeg, "AppData").on = true
	u.addButton(idTabProg, kindSeg, "Program Files")
	u.addButton(idLeftover, kindSeg, "Left over").on = true
	u.addButton(idUnsure, kindSeg, "Not sure")
	u.addButton(idAll, kindSeg, "Everything")
	u.addButton(idRescan, kindSecondary, "Scan again")
	u.addButton(idStop, kindSecondary, "Stop")
	u.addButton(idMore, kindSecondary, "•••")
	u.addButton(idOpen, kindPrimary, "Open folder")
	u.addButton(idDelete, kindPrimary, "Delete checked")
	u.addButton(idRight, kindSeg, "Right")
	u.addButton(idWrong, kindSeg, "Wrong")
	u.search = u.addEdit(idSearch, "Search")
	u.note = u.addEdit(idNote, "What is it really? (optional)")

	h, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(wstr("SysListView32"))), uintptr(unsafe.Pointer(wstr(""))),
		wsChild|wsVisible|wsTabStop|lvsReport|lvsSingleSel|lvsShowSel|lvsOwnerData, 0, 0, 10, 10, u.hwnd, idList, u.instance, 0)
	u.list = h
	send(h, wmSetFont, u.fBody, 1)
	send(h, lvmSetExtStyle, 0, 0x20|0x10000) // full row select, double buffer
	for i, title := range colTitles {
		col := lvColumn{mask: 0x1 | 0x2 | 0x4 | 0x8, cx: u.S(100), pszText: wstr(title), iSubItem: int32(i)}
		if i == colSize {
			col.fmt = 0 // headers are drawn left aligned, so keep values under them
		}
		send(h, lvmInsertColumnW, uintptr(i), uintptr(unsafe.Pointer(&col)))
	}
	u.header = send(h, lvmGetHeader, 0, 0)
	u.listOrig, _, _ = procSetWindowLongPtrW.Call(h, gwlpWndProc, listProc)
}

func (u *winUI) move(h uintptr, r winRect) {
	_, _, _ = procMoveWindow.Call(h, uintptr(r.l), uintptr(r.t), uintptr(r.w()), uintptr(r.h()), 1)
}

func (u *winUI) show(h uintptr, on bool) {
	cmd := uintptr(swHide)
	if on {
		cmd = swShow
	}
	_, _, _ = procShowWindow.Call(h, cmd)
}

func (u *winUI) layout() {
	var rc winRect
	_, _, _ = procGetClientRect.Call(u.hwnd, uintptr(unsafe.Pointer(&rc)))
	u.width, u.height = rc.r, rc.b
	if u.width == 0 || u.list == 0 {
		return
	}
	m := u.S(20)
	bw, bh := u.S(120), u.S(34)
	has := !u.scanning || len(u.all) > 0
	// Top row: title, the two tabs, then Scan again and the menu.
	top := u.S(14)
	r := u.width - m
	mw := u.S(46)
	u.move(u.controls[idMore].hwnd, winRect{r - mw, top, r, top + bh})
	u.move(u.controls[idRescan].hwnd, winRect{r - mw - u.S(8) - bw, top, r - mw - u.S(8), top + bh})
	u.move(u.controls[idStop].hwnd, winRect{r - mw - u.S(8) - bw, top, r - mw - u.S(8), top + bh})
	u.show(u.controls[idRescan].hwnd, !u.scanning)
	u.show(u.controls[idStop].hwnd, u.scanning)
	u.show(u.controls[idMore].hwnd, !u.scanning)
	tx := m + u.S(170)
	u.move(u.controls[idTabApp].hwnd, winRect{tx, top, tx + u.S(110) + 1, top + bh})
	u.move(u.controls[idTabProg].hwnd, winRect{tx + u.S(110), top, tx + u.S(250), top + bh})
	u.show(u.controls[idTabApp].hwnd, has)
	u.show(u.controls[idTabProg].hwnd, has)
	// The summary sits under the top row, then the filters.
	u.rcStats = winRect{m, u.S(62), u.width - m, u.S(118)}
	y := u.S(128)
	x := m
	for _, it := range []struct {
		id int
		w  int32
	}{{idLeftover, 140}, {idUnsure, 120}, {idAll, 140}} {
		u.move(u.controls[it.id].hwnd, winRect{x, y, x + u.S(it.w) + 1, y + bh})
		u.show(u.controls[it.id].hwnd, has)
		x += u.S(it.w)
	}
	sw := u.S(220)
	u.move(u.search, winRect{u.width - m - sw, y + u.S(3), u.width - m, y + bh - u.S(3)})
	u.show(u.search, has)
	dw := u.S(250)
	u.move(u.controls[idDelete].hwnd, winRect{u.width - m - sw - u.S(10) - dw, y, u.width - m - sw - u.S(10), y + bh})
	u.show(u.controls[idDelete].hwnd, !u.scanning && len(u.checked) > 0)
	// Details and list
	detailsH := u.S(176)
	u.rcDetails = winRect{m, u.height - detailsH - m, u.width - m, u.height - m}
	u.rcList = winRect{m, y + bh + u.S(10), u.width - m, u.rcDetails.t - u.S(12)}
	u.move(u.list, u.rcList)
	u.fitColumns()
	// Buttons in the details panel
	f := u.selected()
	pad := u.S(14)
	colW := u.S(220)
	colX := u.rcDetails.r - pad - colW
	half := (colW - u.S(8)) / 2
	u.move(u.controls[idOpen].hwnd, winRect{colX, u.rcDetails.t + pad, colX + colW, u.rcDetails.t + pad + bh})
	by := u.rcDetails.b - pad - bh
	u.move(u.controls[idRight].hwnd, winRect{colX, by, colX + half + 1, by + bh})
	u.move(u.controls[idWrong].hwnd, winRect{colX + half, by, colX + 2*half + u.S(8), by + bh})
	u.move(u.note, winRect{colX, by - u.S(36), colX + colW, by - u.S(8)})
	on := f != nil && !u.scanning
	for _, id := range []int{idRight, idWrong, idOpen} {
		u.show(u.controls[id].hwnd, on)
	}
	mk, marked := Mark{}, false
	if f != nil {
		mk, marked = u.marks[markKey(f)]
	}
	u.show(u.note, on && marked && !mk.Right)
	u.show(u.list, !u.scanning && len(u.view) > 0)
}

// fitColumns sizes the columns to exactly fill the list, so there's no gap beside the
// last header. The list's client width already allows for the scrollbar.
func (u *winUI) fitColumns() {
	var rc winRect
	_, _, _ = procGetClientRect.Call(u.list, uintptr(unsafe.Pointer(&rc)))
	total := rc.w()
	if total <= 0 {
		return
	}
	pick, status, size, changed := u.S(36), u.S(130), u.S(90), u.S(120)
	rest := total - pick - status - size - changed
	owner := rest * 42 / 100
	widths := []int32{pick, owner, status, size, changed, rest - owner}
	for i, w := range widths {
		send(u.list, lvmSetColumnWidth, uintptr(i), uintptr(w))
	}
}

// ---- Filtering and sorting ----

func (u *winUI) selected() *Folder {
	for _, f := range u.view {
		if f.Rel == u.selPath {
			return f
		}
	}
	return nil
}

func (u *winUI) editText(h uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(h)
	buf := make([]uint16, n+1)
	_, _, _ = procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

func (u *winUI) checkText(f *Folder) string {
	if m, ok := u.marks[markKey(f)]; ok {
		if m.Right {
			return "Right"
		}
		return "Wrong"
	}
	return ""
}

func (u *winUI) rebuildView() {
	query := strings.ToLower(strings.TrimSpace(u.editText(u.search)))
	view := u.view[:0:0]
	for _, f := range u.all {
		switch u.filter {
		case idLeftover:
			if f.Verdict != VerdictLeftover {
				continue
			}
		case idUnsure:
			if f.Verdict != VerdictUnsure {
				continue
			}
		case idInUse:
			if f.Verdict != VerdictInUse {
				continue
			}
		case idWindows:
			if f.Verdict != VerdictWindows {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(f.Rel), query) && !strings.Contains(strings.ToLower(f.Owner), query) {
			continue
		}
		view = append(view, f)
	}
	less := func(a, b *Folder) bool {
		switch u.sortCol {
		case colPick:
			return !u.checked[a.Path] && u.checked[b.Path]
		case colFolder:
			return strings.ToLower(a.Rel) < strings.ToLower(b.Rel)
		case colOwner:
			return strings.ToLower(a.Owner) < strings.ToLower(b.Owner)
		case colStatus:
			return a.Verdict.String() < b.Verdict.String()
		case colChanged:
			return a.Newest.Before(b.Newest)
		}
		return a.Size < b.Size
	}
	sort.SliceStable(view, func(i, j int) bool {
		if u.sortAsc {
			return less(view[i], view[j])
		}
		return less(view[j], view[i])
	})
	u.view = view
	send(u.list, lvmSetItemCount, uintptr(len(view)), 0x2)
	found := -1
	for i, f := range view {
		if f.Rel == u.selPath {
			found = i
			break
		}
	}
	if found < 0 && len(view) > 0 {
		found = 0
		u.selPath = view[0].Rel
	} else if found < 0 {
		u.selPath = ""
	}
	if found >= 0 {
		it := lvItem{stateMask: 0x3, state: 0x3}
		send(u.list, lvmSetItemState, uintptr(found), uintptr(unsafe.Pointer(&it)))
		send(u.list, lvmEnsureVisible, uintptr(found), 0)
	}
	_, _, _ = procInvalidateRect.Call(u.list, 0, 0)
	_, _, _ = procInvalidateRect.Call(u.header, 0, 0)
	u.syncSelected()
	u.layout()
	u.fitColumns() // the scrollbar may have just appeared or gone away
	u.invalidateAll()
}

func (u *winUI) syncSelected() {
	u.loadingUI = true
	text := ""
	if f := u.selected(); f != nil {
		m, ok := u.marks[markKey(f)]
		u.controls[idRight].on = ok && m.Right
		u.controls[idWrong].on = ok && !m.Right
		text = m.Actual
	} else {
		u.controls[idRight].on, u.controls[idWrong].on = false, false
	}
	_, _, _ = procSetWindowTextW.Call(u.note, uintptr(unsafe.Pointer(wstr(text))))
	u.loadingUI = false
	_, _, _ = procInvalidateRect.Call(u.controls[idRight].hwnd, 0, 0)
	_, _, _ = procInvalidateRect.Call(u.controls[idWrong].hwnd, 0, 0)
}

func (u *winUI) updateCounts() {
	u.sum = Summarize(u.all)
	set := func(id int, label string, n int) {
		c := u.controls[id]
		c.text = fmt.Sprintf("%s  %d", label, n)
		_, _, _ = procInvalidateRect.Call(c.hwnd, 0, 0)
	}
	set(idLeftover, "Left over", u.sum.Counts[VerdictLeftover])
	set(idUnsure, "Not sure", u.sum.Counts[VerdictUnsure])
	set(idAll, "Everything", u.sum.Folders)
}

// ---- Scanning ----

func (u *winUI) startScan(forceSearch bool) {
	if u.scanning {
		return
	}
	u.scanning, u.msg, u.msgErr = true, "", false
	u.stop = &atomic.Bool{}
	u.stage = "Checking what's installed on this PC..."
	u.prog = Progress{}
	u.layout()
	u.invalidateAll()
	stop := u.stop
	go func() {
		var inv *Inventory
		var result, progs []*Folder
		errText := ""
		func() {
			defer func() {
				if r := recover(); r != nil {
					errText = fmt.Sprint(r)
				}
			}()
			appRoots, progRoots := AppDataRoots(), ProgramFilesRoots()
			if demoMode {
				appRoots, progRoots, inv = demoSetup()
			} else {
				inv = LoadInventory(forceSearch, stop, func(stage, current string) {
					u.progMu.Lock()
					u.stage = stage
					u.prog = Progress{Current: current}
					u.progMu.Unlock()
					u.postProgress()
				})
			}
			u.progMu.Lock()
			u.stage = "Reading AppData folders..."
			u.progMu.Unlock()
			u.postProgress()
			report := func(p Progress) {
				u.progMu.Lock()
				u.prog = p
				u.progMu.Unlock()
				u.postProgress()
			}
			result = Scan(appRoots, inv, stop, report, ReadProduct)
			if !stop.Load() {
				u.progMu.Lock()
				u.stage = "Reading Program Files folders..."
				u.prog = Progress{}
				u.progMu.Unlock()
				u.postProgress()
				progs = ScanProgramFiles(progRoots, inv, stop, report, ReadProduct)
				CrossReference(result, progs, time.Now())
				demoPolish(result, progs)
			}
		}()
		u.progMu.Lock()
		u.scanResult, u.scanProgs, u.scanInv, u.scanCanceled, u.scanErr = result, progs, inv, stop.Load(), errText
		u.progMu.Unlock()
		_, _, _ = procPostMessageW.Call(u.hwnd, wmScanDone, 0, 0)
	}()
}

func (u *winUI) postProgress() {
	if u.progPending.CompareAndSwap(false, true) {
		_, _, _ = procPostMessageW.Call(u.hwnd, wmScanProgress, 0, 0)
	}
}

func (u *winUI) finishScan() {
	u.progMu.Lock()
	result, progs, inv, canceled, errText := u.scanResult, u.scanProgs, u.scanInv, u.scanCanceled, u.scanErr
	u.progMu.Unlock()
	u.scanning = false
	u.now = time.Now()
	switch {
	case errText != "":
		u.msg, u.msgErr = "The scan stopped because of an error: "+errText, true
	case canceled:
		u.msg, u.msgErr = "Scan stopped. The list shows only the folders that finished.", false
	case len(result) == 0:
		u.msg, u.msgErr = "FolderTrace couldn't find any AppData folders to read.", true
	}
	u.allApp, u.allProg = result, progs
	u.checked = map[string]bool{}
	u.all = result
	if u.tab == idTabProg {
		u.all = progs
	}
	u.warns = nil
	if inv != nil {
		u.warns = inv.Warnings
	}
	if len(u.warns) > 0 && u.msg == "" {
		u.msg, u.msgErr = u.warns[0], true
	}
	u.updateCounts()
	u.rebuildView()
}

// ---- Painting ----

func (u *winUI) fill(dc uintptr, r winRect, c uint32) {
	b, _, _ := procCreateSolidBrush.Call(colorRef(c))
	_, _, _ = procFillRect.Call(dc, uintptr(unsafe.Pointer(&r)), b)
	_, _, _ = procDeleteObject.Call(b)
}

func (u *winUI) text(dc uintptr, s string, font uintptr, color uint32, r winRect, flags uintptr) {
	_, _, _ = procSelectObject.Call(dc, font)
	_, _, _ = procSetTextColor.Call(dc, colorRef(color))
	_, _, _ = procSetBkMode.Call(dc, 1)
	_, _, _ = procDrawTextW.Call(dc, uintptr(unsafe.Pointer(wstr(s))), ^uintptr(0), uintptr(unsafe.Pointer(&r)), flags|dtNoPrefix)
}

// wrapped draws wrapping text and returns how tall it was.
func (u *winUI) wrapped(dc uintptr, s string, font uintptr, color uint32, r winRect) int32 {
	calc := r
	_, _, _ = procSelectObject.Call(dc, font)
	_, _, _ = procDrawTextW.Call(dc, uintptr(unsafe.Pointer(wstr(s))), ^uintptr(0), uintptr(unsafe.Pointer(&calc)), dtWordBreak|dtCalcRect|dtNoPrefix)
	u.text(dc, s, font, color, r, dtWordBreak)
	return calc.h()
}

func (u *winUI) invalidateAll() {
	_, _, _ = procInvalidateRect.Call(u.hwnd, 0, 0)
}

func (u *winUI) paint() {
	var ps paintStruct
	dc, _, _ := procBeginPaint.Call(u.hwnd, uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(u.hwnd, uintptr(unsafe.Pointer(&ps)))
	mem, _, _ := procCreateCompatibleDC.Call(dc)
	bmp, _, _ := procCreateCompatibleBitmap.Call(dc, uintptr(u.width), uintptr(u.height))
	old, _, _ := procSelectObject.Call(mem, bmp)
	u.fill(mem, winRect{0, 0, u.width, u.height}, u.theme.bg)
	u.paintHeader(mem)
	u.paintStats(mem)
	if u.scanning {
		u.paintScanning(mem)
	} else if len(u.view) == 0 {
		u.paintEmptyList(mem)
	}
	u.paintDetails(mem)
	_, _, _ = procBitBlt.Call(dc, 0, 0, uintptr(u.width), uintptr(u.height), mem, 0, 0, 0xCC0020)
	_, _, _ = procSelectObject.Call(mem, old)
	_, _, _ = procDeleteObject.Call(bmp)
	_, _, _ = procDeleteDC.Call(mem)
}

func (u *winUI) paintHeader(dc uintptr) {
	t := u.theme
	m := u.S(20)
	u.text(dc, "FolderTrace", u.fH2, t.ink, winRect{m, u.S(10), m + u.S(170), u.S(52)}, dtLeft|dtVCenter|dtSingleLine)
}

func (u *winUI) paintStats(dc uintptr) {
	t := u.theme
	r := u.rcStats
	big, sub := "Scanning...", "Nothing is removed unless you check it and choose Delete checked. Nothing leaves this PC."
	if !u.scanning {
		s := u.sum
		where := "AppData"
		if u.tab == idTabProg {
			where = "Program Files"
		}
		switch {
		case len(u.all) == 0:
			big, sub = "No results", "Choose Scan again to try once more."
		case s.Counts[VerdictLeftover] == 0:
			big = "Nothing in " + where + " looks left over"
			sub = fmt.Sprintf("%s in %d folders checked", FormatSize(s.Size), s.Folders)
		default:
			big = fmt.Sprintf("%s in %s looks left over", FormatSize(s.LeftoverSize), where)
			sub = fmt.Sprintf("%d of %d folders. Read why before removing anything.", s.Counts[VerdictLeftover], s.Folders)
		}
		if right, total := MarkScore(u.all, u.marks); total > 0 {
			sub += fmt.Sprintf("   Your check: %d of %d right.", right, total)
		}
	}
	u.text(dc, big, u.fFig, t.ink, winRect{r.l, r.t, r.r, r.t + u.S(32)}, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	u.text(dc, sub, u.fSmall, t.muted, winRect{r.l, r.t + u.S(32), r.r, r.b}, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
}

func (u *winUI) paintScanning(dc uintptr) {
	t := u.theme
	u.progMu.Lock()
	p, stage := u.prog, u.stage
	u.progMu.Unlock()
	r := u.rcList
	u.fill(dc, r, t.line)
	u.fill(dc, winRect{r.l + 1, r.t + 1, r.r - 1, r.b - 1}, t.panel)
	cy := r.t + r.h()/2
	u.text(dc, stage, u.fBold, t.ink, winRect{r.l, cy - u.S(50), r.r, cy - u.S(22)}, dtCenter|dtVCenter|dtSingleLine)
	barW := min(u.S(480), r.w()-u.S(40))
	bar := winRect{r.l + (r.w()-barW)/2, cy - u.S(12), r.l + (r.w()+barW)/2, cy}
	u.fill(dc, bar, t.track)
	if p.Total == 0 && p.Current != "" {
		u.text(dc, p.Current, u.fSmall, t.muted, winRect{r.l + u.S(20), cy + u.S(6), r.r - u.S(20), cy + u.S(28)},
			dtCenter|dtVCenter|dtSingleLine|dtPathEllipsis)
	}
	if p.Total > 0 {
		done := bar
		done.r = bar.l + int32(int64(barW)*int64(p.Done)/int64(p.Total))
		u.fill(dc, done, t.accent)
		u.text(dc, fmt.Sprintf("%d of %d folders", p.Done, p.Total), u.fSmall, t.muted,
			winRect{r.l, cy + u.S(6), r.r, cy + u.S(28)}, dtCenter|dtVCenter|dtSingleLine)
		u.text(dc, p.Current, u.fSmall, t.muted, winRect{r.l + u.S(20), cy + u.S(28), r.r - u.S(20), cy + u.S(50)},
			dtCenter|dtVCenter|dtSingleLine|dtPathEllipsis)
	}
}

func (u *winUI) paintEmptyList(dc uintptr) {
	t := u.theme
	r := u.rcList
	u.fill(dc, r, t.line)
	u.fill(dc, winRect{r.l + 1, r.t + 1, r.r - 1, r.b - 1}, t.panel)
	s := "No folders match this filter."
	if q := strings.TrimSpace(u.editText(u.search)); q != "" {
		s = "No folders match \"" + q + "\". Press Esc to clear the search."
	}
	if len(u.all) == 0 {
		s = "There are no results yet. Choose Scan again to try once more."
	}
	u.text(dc, s, u.fBody, t.muted, r, dtCenter|dtVCenter|dtSingleLine)
}

func commas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func (u *winUI) verdictColor(v Verdict) uint32 {
	switch v {
	case VerdictInUse:
		return u.theme.up
	case VerdictLeftover:
		return u.theme.warn
	case VerdictUnsure:
		return u.theme.unk
	}
	return u.theme.muted
}

func (u *winUI) paintDetails(dc uintptr) {
	t := u.theme
	r := u.rcDetails
	u.fill(dc, r, t.line)
	u.fill(dc, winRect{r.l + 1, r.t + 1, r.r - 1, r.b - 1}, t.panel)
	pad := u.S(14)
	colW := u.S(220)
	in := winRect{r.l + pad, r.t + u.S(10), r.r - pad*2 - colW, r.b - u.S(8)}
	msgH := int32(0)
	if u.msg != "" {
		msgH = u.S(20)
	}
	if u.msg != "" {
		col := t.muted
		if u.msgErr {
			col = t.down
		}
		u.text(dc, u.msg, u.fSmall, col, winRect{in.l, in.b - msgH, in.r, in.b}, dtLeft|dtVCenter|dtSingleLine|dtPathEllipsis)
	}
	f := u.selected()
	if f == nil || u.scanning {
		s := "Select a folder to see who it probably belongs to and why."
		if u.scanning {
			s = "You can keep using your PC while this runs. FolderTrace reads names, sizes, dates, what's installed, and small log and settings files, but never browser data or passwords. Nothing leaves this PC."
		}
		u.wrapped(dc, s, u.fBody, t.muted, in)
		return
	}
	y := in.t
	head := f.Owner
	if f.Guessed {
		head = f.Owner + " (guessed from the folder's name)"
	} else if !f.Identified {
		head = "Unknown owner"
	}
	u.text(dc, head, u.fBold, t.ink, winRect{in.l, y, in.r - u.S(180), y + u.S(22)}, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	status := fmt.Sprintf("%s · %s confidence", f.Verdict, strings.ToLower(f.Confidence))
	u.text(dc, status, u.fBold, u.verdictColor(f.Verdict), winRect{in.r - u.S(280), y, in.r, y + u.S(22)}, dtRight|dtVCenter|dtSingleLine)
	y += u.S(22)
	files := "files"
	if f.Files == 1 {
		files = "file"
	}
	u.text(dc, fmt.Sprintf("%s in %s %s · %s · %s", FormatSize(f.Size), commas(f.Files), files, f.Contains(), f.Path), u.fSmall, t.muted,
		winRect{in.l, y, in.r, y + u.S(18)}, dtLeft|dtVCenter|dtSingleLine|dtPathEllipsis)
	y += u.S(24)
	y += u.wrapped(dc, f.Why, u.fBody, t.ink, winRect{in.l, y, in.r, in.b}) + u.S(4)
	y += u.wrapped(dc, f.Advice, u.fBold, t.ink, winRect{in.l, y, in.r, in.b}) + u.S(4)
	if rel := RelatedText(f, u.now); rel != "" && y+u.S(18) <= in.b-msgH {
		u.text(dc, rel, u.fSmall, t.ink, winRect{in.l, y, in.r, y + u.S(18)}, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		y += u.S(20)
	}
	colX := r.r - pad - colW
	u.text(dc, "Is this label right?", u.fSmall, t.muted, winRect{colX, r.b - pad - u.S(34) - u.S(22), colX + colW, r.b - pad - u.S(34) - u.S(2)}, dtLeft|dtVCenter|dtSingleLine)
}

func (u *winUI) drawButton(di *drawItemStruct) {
	c := u.byHwnd[di.hwndItem]
	if c == nil {
		return
	}
	t := u.theme
	dc := di.hDC
	r := di.rc
	pressed := di.itemState&odsSelected != 0
	focus := di.itemState&odsFocus != 0 && di.itemState&odsNoFocusRect == 0
	switch c.kind {
	case kindSeg:
		bg, ink, font := t.panel, t.ink, u.fBody
		if c.on {
			bg, ink, font = t.accent, t.accentInk, u.fBold
		} else if c.hover {
			bg = t.track
		}
		u.fill(dc, r, t.line)
		u.fill(dc, winRect{r.l + 1, r.t + 1, r.r - 1, r.b - 1}, bg)
		u.text(dc, c.text, font, ink, r, dtCenter|dtVCenter|dtSingleLine)
	case kindPrimary, kindSecondary:
		bg, ink, edge := t.accent, t.accentInk, t.accent
		if c.kind == kindSecondary {
			bg, ink = t.panel, t.accent
			if c.hover {
				bg = t.track
			}
		}
		if pressed {
			bg = t.line
			if c.kind == kindPrimary {
				bg, ink = t.ink, t.bg
			}
		}
		u.fill(dc, r, edge)
		u.fill(dc, winRect{r.l + 1, r.t + 1, r.r - 1, r.b - 1}, bg)
		u.text(dc, c.text, u.fBold, ink, r, dtCenter|dtVCenter|dtSingleLine)
	case kindCheck:
		u.fill(dc, r, t.bg)
		size := u.S(18)
		box := winRect{r.l + u.S(2), r.t + (r.h()-size)/2, r.l + u.S(2) + size, r.t + (r.h()-size)/2 + size}
		edge := t.muted
		if c.hover {
			edge = t.ink
		}
		inner := t.panel
		if c.on {
			edge, inner = t.accent, t.accent
		}
		u.fill(dc, box, edge)
		u.fill(dc, winRect{box.l + 1, box.t + 1, box.r - 1, box.b - 1}, inner)
		if c.on {
			u.text(dc, "✓", u.fBold, t.accentInk, box, dtCenter|dtVCenter|dtSingleLine)
		}
		tr := r
		tr.l = box.r + u.S(8)
		u.text(dc, c.text, u.fBody, t.ink, tr, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	}
	if focus {
		for _, e := range []winRect{{r.l, r.t, r.r, r.t + 2}, {r.l, r.b - 2, r.r, r.b}, {r.l, r.t, r.l + 2, r.b}, {r.r - 2, r.t, r.r, r.b}} {
			col := t.accent
			if c.on && c.kind != kindCheck {
				col = t.accentInk
			}
			u.fill(dc, e, col)
		}
	}
}

// ---- Actions ----

func copyUTF16(dst []uint16, s string) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

func (u *winUI) setMsg(s string, isErr bool) {
	u.msg, u.msgErr = s, isErr
	_, _, _ = procInvalidateRect.Call(u.hwnd, 0, 0)
}

func (u *winUI) copyPath(s string) bool {
	text, err := syscall.UTF16FromString(s)
	if err != nil {
		return false
	}
	if r, _, _ := procOpenClipboard.Call(u.hwnd); r == 0 {
		return false
	}
	defer procCloseClipboard.Call()
	_, _, _ = procEmptyClipboard.Call()
	size := uintptr(len(text) * 2)
	h, _, _ := procGlobalAlloc.Call(2, size)
	if h == 0 {
		return false
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return false
	}
	copy(unsafe.Slice((*uint16)(ptr(p)), len(text)), text)
	_, _, _ = procGlobalUnlock.Call(h)
	if r, _, _ := procSetClipboardData.Call(13, h); r == 0 {
		_, _, _ = procGlobalFree.Call(h)
		return false
	}
	return true
}

// openLink opens one of FolderTrace's own web pages in the browser. The app
// itself still never connects to the internet.
func (u *winUI) openLink(url string) {
	r, _, _ := procShellExecuteW.Call(u.hwnd, uintptr(unsafe.Pointer(wstr("open"))), uintptr(unsafe.Pointer(wstr(url))), 0, 0, 1)
	if r <= 32 {
		u.setMsg("Windows couldn't open your browser. The page is "+url, true)
	}
}

func (u *winUI) openFolder(f *Folder) {
	if _, err := os.Stat(f.Path); err != nil {
		u.setMsg("That folder is no longer there. Choose Scan again to refresh the list.", true)
		return
	}
	r, _, _ := procShellExecuteW.Call(u.hwnd, uintptr(unsafe.Pointer(wstr("open"))), uintptr(unsafe.Pointer(wstr(f.Path))), 0, 0, 1)
	if r <= 32 {
		u.setMsg("Windows couldn't open that folder.", true)
	}
}

func (u *winUI) saveDialog() (string, bool) {
	buf := make([]uint16, 1024)
	copyUTF16(buf, "FolderTrace report.csv")
	filter, _ := syscall.UTF16FromString("CSV report (*.csv)\x01*.csv\x01")
	for i := range filter {
		if filter[i] == 1 {
			filter[i] = 0
		}
	}
	filter = append(filter, 0)
	ofn := openFileName{hwndOwner: u.hwnd, lpstrFilter: &filter[0], nFilterIndex: 1, lpstrFile: &buf[0], nMaxFile: uint32(len(buf)),
		lpstrTitle: wstr("Save report"), lpstrDefExt: wstr("csv"), Flags: 0x2 | 0x8 | 0x800}
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	r, _, _ := procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	return syscall.UTF16ToString(buf), r != 0
}

func (u *winUI) saveMarks() {
	if err := SaveMarks(u.dataDir, u.marks); err != nil {
		u.setMsg("Your check couldn't be saved: "+err.Error(), true)
	}
}

func (u *winUI) setMark(right bool) {
	f := u.selected()
	if f == nil {
		return
	}
	key := markKey(f)
	cur, had := u.marks[key]
	if had && cur.Right == right {
		delete(u.marks, key) // pressing the same answer again clears it
	} else {
		cur.Right = right
		if right {
			cur.Actual = ""
		}
		u.marks[key] = cur
	}
	u.saveMarks()
	u.syncSelected()
	u.layout()
	send(u.list, lvmRedrawItems, 0, uintptr(len(u.view)))
	u.invalidateAll()
	if m, ok := u.marks[key]; ok && !m.Right {
		_, _, _ = procSetFocus.Call(u.note)
	}
}

// setTab switches between AppData and Program Files. Each keeps the same filters.
func (u *winUI) setTab(id int) {
	if u.scanning {
		return
	}
	u.tab = id
	u.all = u.allApp
	if id == idTabProg {
		u.all = u.allProg
	}
	for _, other := range []int{idTabApp, idTabProg} {
		c := u.controls[other]
		c.on = other == id
		_, _, _ = procInvalidateRect.Call(c.hwnd, 0, 0)
	}
	u.updateCounts()
	u.rebuildView()
	u.invalidateAll()
}

func (u *winUI) setFilter(id int) {
	u.filter = id
	for _, other := range []int{idAll, idLeftover, idUnsure} {
		c := u.controls[other]
		c.on = other == id
		_, _, _ = procInvalidateRect.Call(c.hwnd, 0, 0)
	}
	u.rebuildView()
}

func (u *winUI) onCommand(id int) {
	switch id {
	case 2: // Esc
		_, _, _ = procSetWindowTextW.Call(u.search, uintptr(unsafe.Pointer(wstr(""))))
	case idAll, idLeftover, idUnsure, idInUse, idWindows:
		u.setFilter(id)
	case idTabApp, idTabProg:
		u.setTab(id)
	case idMore:
		u.showMenu()
	case idDelete:
		u.deleteChecked()
	case idRescan:
		u.startScan(false)
	case idSearchDrives:
		u.startScan(true)
	case idStop:
		if u.stop != nil {
			u.stop.Store(true)
			u.progMu.Lock()
			u.stage = "Stopping..."
			u.progMu.Unlock()
			u.invalidateAll()
		}
	case idSave:
		both := append(append([]*Folder{}, u.allApp...), u.allProg...)
		if len(both) == 0 {
			u.setMsg("There's nothing to save yet.", true)
			return
		}
		if path, ok := u.saveDialog(); ok {
			if err := os.WriteFile(path, ReportCSV(both, u.marks, u.now), 0o644); err != nil {
				u.setMsg("The report couldn't be saved: "+err.Error(), true)
			} else {
				u.setMsg("Report saved to "+path, false)
			}
		}
	case idOpen:
		if f := u.selected(); f != nil {
			u.openFolder(f)
		}
	case idFeedback:
		u.openLink(FeedbackURL)
	case idMoreApps:
		u.openLink(MoreAppsURL)
	case idCopy:
		if f := u.selected(); f != nil {
			if u.copyPath(f.Path) {
				u.setMsg("Path copied.", false)
			} else {
				u.setMsg("The path couldn't be copied. Another app may be using the clipboard.", true)
			}
		}
	case idRight:
		u.setMark(true)
	case idWrong:
		u.setMark(false)
	}
}

// toggle checks or unchecks one folder. Windows' own folders can't be checked.
func (u *winUI) toggle(f *Folder) {
	if f.Verdict == VerdictWindows {
		u.setMsg("Windows and shared folders can't be removed from here.", true)
		return
	}
	if u.checked[f.Path] {
		delete(u.checked, f.Path)
	} else {
		u.checked[f.Path] = true
	}
	u.afterToggle()
}

// toggleAll checks every folder in view, or unchecks them if they're all checked.
func (u *winUI) toggleAll() {
	all := true
	for _, f := range u.view {
		if f.Verdict != VerdictWindows && !u.checked[f.Path] {
			all = false
		}
	}
	for _, f := range u.view {
		if f.Verdict == VerdictWindows {
			continue
		}
		if all {
			delete(u.checked, f.Path)
		} else {
			u.checked[f.Path] = true
		}
	}
	u.afterToggle()
}

func (u *winUI) checkedFolders() []*Folder {
	var out []*Folder
	for _, f := range append(append([]*Folder{}, u.allApp...), u.allProg...) {
		if u.checked[f.Path] {
			out = append(out, f)
		}
	}
	return out
}

func (u *winUI) afterToggle() {
	picked := u.checkedFolders()
	var size int64
	for _, f := range picked {
		size += f.Size
	}
	c := u.controls[idDelete]
	c.text = fmt.Sprintf("Delete checked (%d, %s)", len(picked), FormatSize(size))
	send(u.list, lvmRedrawItems, 0, uintptr(len(u.view)))
	u.layout()
	_, _, _ = procInvalidateRect.Call(c.hwnd, 0, 0)
	_, _, _ = procInvalidateRect.Call(u.header, 0, 0)
	u.invalidateAll()
}

// deleteChecked confirms, then moves the checked folders to the Recycle Bin.
func (u *winUI) deleteChecked() {
	picked := u.checkedFolders()
	if len(picked) == 0 {
		return
	}
	var size int64
	inUse, saves, progs := 0, 0, 0
	for _, f := range picked {
		size += f.Size
		if f.Verdict == VerdictInUse {
			inUse++
		}
		if f.Saves || f.Profile {
			saves++
		}
		if f.Program {
			progs++
		}
	}
	text := fmt.Sprintf("Move %d folders (%s) to the Recycle Bin?", len(picked), FormatSize(size))
	if len(picked) == 1 {
		text = fmt.Sprintf("Move %s (%s) to the Recycle Bin?", picked[0].Rel, FormatSize(size))
	}
	var warn []string
	if inUse > 0 {
		warn = append(warn, fmt.Sprintf("%d of them belong to programs that are still installed. Those programs may break or lose settings.", inUse))
	}
	if saves > 0 {
		warn = append(warn, fmt.Sprintf("%d may hold game saves or sign-ins.", saves))
	}
	if progs > 0 {
		warn = append(warn, "Program Files folders need administrator permission, so Windows may ask.")
	}
	if len(warn) > 0 {
		text += "\n\n" + strings.Join(warn, "\n")
	}
	if len(picked) == 1 {
		text += "\n\nYou can put it back from the Recycle Bin."
	} else {
		text += "\n\nYou can put them back from the Recycle Bin."
	}
	r, _, _ := procMessageBoxW.Call(u.hwnd, uintptr(unsafe.Pointer(wstr(text))), uintptr(unsafe.Pointer(wstr("Delete checked folders"))), 0x1|0x30|0x100)
	if r != 1 { // OK
		return
	}
	var failed []string
	var freed int64
	gone := map[*Folder]bool{}
	for _, f := range picked {
		if recycle(u.hwnd, f.Path) {
			gone[f] = true
			freed += f.Size
			delete(u.checked, f.Path)
		} else {
			failed = append(failed, f.Rel)
		}
	}
	keep := func(list []*Folder) []*Folder {
		out := list[:0:0]
		for _, f := range list {
			if !gone[f] {
				out = append(out, f)
			}
		}
		return out
	}
	u.allApp, u.allProg = keep(u.allApp), keep(u.allProg)
	u.all = u.allApp
	if u.tab == idTabProg {
		u.all = u.allProg
	}
	u.updateCounts()
	u.rebuildView()
	u.afterToggle()
	switch {
	case len(failed) == 0:
		noun := "folders"
		if len(gone) == 1 {
			noun = "folder"
		}
		u.setMsg(fmt.Sprintf("Moved %d %s (%s) to the Recycle Bin.", len(gone), noun, FormatSize(freed)), false)
	default:
		u.setMsg(fmt.Sprintf("Moved %d to the Recycle Bin. %d couldn't be removed, maybe because a program is using them: %s", len(gone), len(failed), strings.Join(failed, ", ")), true)
	}
}

// showMenu holds the things people need now and then, so they don't crowd the screen.
func (u *winUI) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	add := func(id int, text string) {
		_, _, _ = procAppendMenuW.Call(menu, 0, uintptr(id), uintptr(unsafe.Pointer(wstr(text))))
	}
	add(idSave, "Save report...")
	if u.selected() != nil {
		add(idCopy, "Copy folder path")
	}
	_, _, _ = procAppendMenuW.Call(menu, 0x800, 0, 0) // separator
	add(idSearchDrives, "Search all drives for programs")
	_, _, _ = procAppendMenuW.Call(menu, 0x800, 0, 0) // separator
	add(idFeedback, "Send feedback")
	add(idMoreApps, "More from Second Shift Software")
	var rc winRect
	_, _, _ = procGetWindowRect.Call(u.controls[idMore].hwnd, uintptr(unsafe.Pointer(&rc)))
	cmd, _, _ := procTrackPopupMenu.Call(menu, 0x100|0x8, uintptr(rc.r), uintptr(rc.b), 0, u.hwnd, 0)
	if cmd != 0 {
		u.onCommand(int(cmd))
	}
}

func (u *winUI) cellText(f *Folder, col int) string {
	switch col {
	case colPick:
		if f.Verdict == VerdictWindows {
			return ""
		}
		if u.checked[f.Path] {
			return "☑"
		}
		return "☐"
	case colFolder:
		return f.Rel
	case colOwner:
		return f.Owner
	case colStatus:
		return f.Verdict.String()
	case colSize:
		return FormatSize(f.Size)
	case colChanged:
		return FormatAge(f.Newest, u.now)
	}
	return ""
}

func (u *winUI) onNotify(lParam uintptr) uintptr {
	id := *(*uintptr)(ptr(lParam + 8))
	code := *(*uint32)(ptr(lParam + 16))
	if id != idList {
		return 0
	}
	switch code {
	case lvnGetDispInfoW:
		it := (*lvItem)(ptr(lParam + 24))
		if it.mask&1 != 0 && int(it.iItem) < len(u.view) && it.iItem >= 0 && it.pszText != nil && it.cchTextMax > 0 {
			copyUTF16(unsafe.Slice(it.pszText, it.cchTextMax), u.cellText(u.view[it.iItem], int(it.iSubItem)))
		}
	case lvnItemChanged:
		i := int32(int64(send(u.list, lvmGetNextItem, ^uintptr(0), 2)))
		if i >= 0 && int(i) < len(u.view) && u.view[i].Rel != u.selPath {
			u.selPath = u.view[i].Rel
			u.msg, u.msgErr = "", false // an old message would read as being about this folder
			u.syncSelected()
			u.layout()
			_, _, _ = procInvalidateRect.Call(u.hwnd, 0, 0)
		}
	case nmClick:
		item := int(*(*int32)(ptr(lParam + 24)))
		if sub := *(*int32)(ptr(lParam + 28)); sub == colPick && item >= 0 && item < len(u.view) {
			u.toggle(u.view[item])
		}
	case lvnKeyDown:
		if *(*uint16)(ptr(lParam + 24)) == 0x20 { // Space
			if f := u.selected(); f != nil {
				u.toggle(f)
			}
		}
	case lvnColumnClick:
		col := int(*(*int32)(ptr(lParam + 28)))
		if col == colPick {
			u.toggleAll()
			return 0
		}
		if u.sortCol == col {
			u.sortAsc = !u.sortAsc
		} else {
			u.sortCol = col
			u.sortAsc = col == colFolder || col == colOwner || col == colStatus || col == colChanged
		}
		u.rebuildView()
	case nmDblClk, nmReturn:
		if f := u.selected(); f != nil {
			u.openFolder(f)
		}
	case nmCustomDraw:
		switch *(*uint32)(ptr(lParam + 24)) {
		case 1:
			return 0x20
		case 0x10001:
			return 0x20
		case 0x30001:
			item := int(*(*uintptr)(ptr(lParam + 56)))
			sub := *(*int32)(ptr(lParam + 88))
			color := u.theme.ink
			if item < len(u.view) {
				f := u.view[item]
				switch {
				case sub == colStatus:
					color = u.verdictColor(f.Verdict)
				case sub == colFolder:
					color = u.theme.muted
				case sub == colPick && u.checked[f.Path]:
					color = u.theme.accent
				case sub == colOwner && !f.Identified:
					color = u.theme.muted // a guess or unknown, so it doesn't read as a fact
				}
			}
			*(*uint32)(ptr(lParam + 80)) = uint32(colorRef(color))
			return 0x2
		}
	}
	return 0
}

func windowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	u := theUI
	if u == nil || u.hwnd == 0 {
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
		return r
	}
	switch uint32(msg) {
	case wmPaint:
		u.paint()
		return 0
	case wmEraseBkgnd:
		return 1
	case wmSize:
		u.layout()
		u.invalidateAll()
		return 0
	case wmGetMinMaxInfo:
		info := (*[10]int32)(ptr(lParam))
		info[6], info[7] = u.S(1040), u.S(720)
		return 0
	case wmDrawItem:
		u.drawButton((*drawItemStruct)(ptr(lParam)))
		return 1
	case wmNotify:
		return u.onNotify(lParam)
	case wmCommand:
		code := (wParam >> 16) & 0xFFFF
		id := int(wParam & 0xFFFF)
		switch {
		case code == enChange && id == idSearch && !u.scanning:
			u.rebuildView()
		case code == enChange && id == idNote && !u.loadingUI:
			if f := u.selected(); f != nil {
				if m, ok := u.marks[markKey(f)]; ok && !m.Right {
					m.Actual = strings.TrimSpace(u.editText(u.note))
					u.marks[markKey(f)] = m
				}
			}
		case code == enKillFocus && id == idNote:
			u.saveMarks()
		case code == 0:
			u.onCommand(id)
		}
		return 0
	case wmCtlColorEdit:
		_, _, _ = procSetTextColor.Call(wParam, colorRef(u.theme.ink))
		_, _, _ = procSetBkColor.Call(wParam, colorRef(u.theme.panel))
		return u.editBrush
	case wmDpiChanged:
		u.dpi = int32(wParam & 0xFFFF)
		r := (*winRect)(ptr(lParam))
		_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(r.l), uintptr(r.t), uintptr(r.w()), uintptr(r.h()), 0x4|0x10)
		u.makeFonts()
		u.layout()
		u.invalidateAll()
		return 0
	case wmSettingChange:
		if lParam != 0 && syscall.UTF16ToString((*[64]uint16)(ptr(lParam))[:]) == "ImmersiveColorSet" {
			u.loadTheme()
			u.applyTitleBar()
			u.applyListTheme()
			for _, c := range u.controls {
				_, _, _ = procInvalidateRect.Call(c.hwnd, 0, 0)
			}
			u.invalidateAll()
		}
	case wmScanProgress:
		u.progPending.Store(false)
		_, _, _ = procInvalidateRect.Call(hwnd, 0, 0)
		return 0
	case wmScanDone:
		u.finishScan()
		return 0
	case wmClose:
		if u.stop != nil {
			u.stop.Store(true)
		}
		if u.note != 0 {
			u.saveMarks()
		}
	case wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}
