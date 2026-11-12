//go:build !demo

package main

const demoMode = false

func demoSetup() (appRoots, progRoots []Root, inv *Inventory) { return nil, nil, nil }

func demoPolish(all ...[]*Folder) {}

func demoStart(hwnd uintptr) {}
