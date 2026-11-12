//go:build demo && windows

package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"
	"unsafe"
)

// In the demo build, the window saves a picture of itself every 1.5 seconds to
// shots\\latest.png beside the .exe, for the Store listing.

var (
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procGetDC            = user32.NewProc("GetDC")
	procReleaseDC        = user32.NewProc("ReleaseDC")
	procPrintWindow      = user32.NewProc("PrintWindow")
	procSelectObjectDemo = gdi32.NewProc("SelectObject")
	procDeleteDCDemo     = gdi32.NewProc("DeleteDC")
	procDeleteObjectDemo = gdi32.NewProc("DeleteObject")
	procGetDIBits        = gdi32.NewProc("GetDIBits")
)

type demoRect struct{ L, T, R, B int32 }

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func demoStart(hwnd uintptr) {
	go func() {
		for {
			time.Sleep(1500 * time.Millisecond)
			demoCapture(hwnd, filepath.Join(demoShotDir(), "latest.png"))
		}
	}()
}

func demoCapture(hwnd uintptr, path string) {
	var r demoRect
	_, _, _ = procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	w, h := r.R-r.L, r.B-r.T
	if w <= 0 || h <= 0 {
		return
	}
	screen, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	defer procDeleteDCDemo.Call(mem)
	bmp, _, _ := procCreateCompatibleBitmap.Call(screen, uintptr(w), uintptr(h))
	defer procDeleteObjectDemo.Call(bmp)
	old, _, _ := procSelectObjectDemo.Call(mem, bmp)
	_, _, _ = procPrintWindow.Call(hwnd, mem, 2) // PW_RENDERFULLCONTENT
	_, _, _ = procSelectObjectDemo.Call(mem, old)
	bi := bitmapInfoHeader{Width: w, Height: -h, Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	buf := make([]byte, int(w)*int(h)*4)
	_, _, _ = procGetDIBits.Call(mem, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0)
	img := image.NewNRGBA(image.Rect(0, 0, int(w), int(h)))
	for i := 0; i < len(buf); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = buf[i+2], buf[i+1], buf[i], 255
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	_ = png.Encode(f, img)
}

// demoShotDir is a shots folder beside the demo .exe.
func demoShotDir() string {
	exe, err := os.Executable()
	if err != nil {
		return filepath.Join(demoBase, "shots")
	}
	return filepath.Join(filepath.Dir(exe), "shots")
}
