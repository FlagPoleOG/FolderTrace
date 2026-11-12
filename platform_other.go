//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
)

func LoadInventory(bool, *atomic.Bool, func(string, string)) *Inventory { return &Inventory{} }
func AppDataRoots() []Root                                              { return nil }
func ProgramFilesRoots() []Root                                         { return nil }
func DataDir() string                                                   { return filepath.Join(os.TempDir(), "FolderTrace") }
func ReadProduct(*Folder)                                               {}
func runUI()                                                            {}
