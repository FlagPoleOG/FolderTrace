//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// This is the only place FolderTrace removes anything, and only folders the person
// checked and confirmed. They go to the Recycle Bin, so they can be restored.

var procSHFileOperationW = shell32.NewProc("SHFileOperationW")

type shFileOp struct {
	hwnd          uintptr
	wFunc         uint32
	pFrom         *uint16
	pTo           *uint16
	fFlags        uint16
	fAnyAborted   int32
	hNameMappings uintptr
	progressTitle *uint16
}

const _ = uint(56 - unsafe.Sizeof(shFileOp{}))
const _ = uint(unsafe.Sizeof(shFileOp{}) - 56)

const (
	foDelete       = 3
	fofAllowUndo   = 0x40 // Recycle Bin, never a permanent delete
	fofNoConfirm   = 0x10 // FolderTrace already asked
	fofWantNukeWar = 0x4000
)

// recycle moves one folder to the Recycle Bin. Windows shows its own progress, and asks
// for administrator permission itself when a folder needs it. It reports whether the
// folder is gone afterwards.
func recycle(hwnd uintptr, path string) bool {
	from, err := syscall.UTF16FromString(path)
	if err != nil {
		return false
	}
	from = append(from, 0) // the list ends with two zeros
	op := shFileOp{hwnd: hwnd, wFunc: foDelete, pFrom: &from[0], fFlags: fofAllowUndo | fofNoConfirm | fofWantNukeWar}
	_, _, _ = procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	_, err = os.Stat(path)
	return os.IsNotExist(err)
}
