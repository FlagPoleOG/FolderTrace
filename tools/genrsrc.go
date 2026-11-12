//go:build ignore

// genrsrc builds rsrc_windows_amd64.syso, which gives the .exe its icon and Windows
// manifest. Run it with: go run tools/genrsrc.go
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
)

const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity version="0.2.0.0" processorArchitecture="amd64" name="FolderTrace" type="win32"/>
  <description>FolderTrace AppData folder scanner</description>
  <dependency><dependentAssembly>
    <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
  </dependentAssembly></dependency>
  <application xmlns="urn:schemas-microsoft-com:asm.v3"><windowsSettings>
    <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/PM</dpiAware>
    <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2</dpiAwareness>
  </windowsSettings></application>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v2"><security><requestedPrivileges>
    <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
  </requestedPrivileges></security></trustInfo>
</assembly>
`

// icon draws a folder with a magnifying glass.
func icon(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	folder := color.NRGBA{0x2F, 0x7F, 0xA8, 255}
	tab := color.NRGBA{0x17, 0x50, 0x6E, 255}
	glass := color.NRGBA{0xF2, 0xB6, 0x5C, 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := (float64(x)+.5)/s, (float64(y)+.5)/s
			var c color.NRGBA
			if fx > .06 && fx < .46 && fy > .14 && fy < .30 {
				c = tab
			}
			if fx > .06 && fx < .94 && fy > .24 && fy < .86 {
				c = folder
			}
			dx, dy := fx-.60, fy-.56
			d := dx*dx + dy*dy
			if d < .056 {
				c = color.NRGBA{0xFF, 0xFF, 0xFF, 255}
			}
			if d < .056 && d > .030 {
				c = glass
			}
			// Handle
			if fx > .72 && fy > .68 && fx < .95 && fy < .95 {
				if v := (fx - fy); v > -.05 && v < .05 {
					c = glass
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func main() {
	sizes := []int{16, 32, 48, 256}
	var icons [][]byte
	for _, s := range sizes {
		icons = append(icons, icon(s))
	}
	le := binary.LittleEndian
	// RT_GROUP_ICON
	var grp bytes.Buffer
	_ = binary.Write(&grp, le, []uint16{0, 1, uint16(len(icons))})
	for i, d := range icons {
		w := byte(sizes[i])
		if sizes[i] == 256 {
			w = 0
		}
		grp.Write([]byte{w, w, 0, 0})
		_ = binary.Write(&grp, le, []uint16{1, 32})
		_ = binary.Write(&grp, le, uint32(len(d)))
		_ = binary.Write(&grp, le, uint16(i+1))
	}

	type leaf struct {
		id   uint32
		data []byte
	}
	types := []struct {
		id     uint32
		leaves []leaf
	}{
		{3, nil},                            // RT_ICON
		{14, []leaf{{1, grp.Bytes()}}},      // RT_GROUP_ICON
		{24, []leaf{{1, []byte(manifest)}}}, // RT_MANIFEST
	}
	for i, d := range icons {
		types[0].leaves = append(types[0].leaves, leaf{uint32(i + 1), d})
	}

	// Lay out the directory tree: root -> type -> name -> language -> data entry.
	const dirSize, entrySize, dataSize = 16, 8, 16
	root := dirSize + len(types)*entrySize
	offset := root
	typeDirs := make([]int, len(types))
	for i, t := range types {
		typeDirs[i] = offset
		offset += dirSize + len(t.leaves)*entrySize
	}
	nameDirs := make([][]int, len(types))
	for i, t := range types {
		nameDirs[i] = make([]int, len(t.leaves))
		for j := range t.leaves {
			nameDirs[i][j] = offset
			offset += dirSize + entrySize
		}
	}
	dataEntries := make([][]int, len(types))
	for i, t := range types {
		dataEntries[i] = make([]int, len(t.leaves))
		for j := range t.leaves {
			dataEntries[i][j] = offset
			offset += dataSize
		}
	}
	dataStart := offset
	total := dataStart
	dataOffs := make([][]int, len(types))
	for i, t := range types {
		dataOffs[i] = make([]int, len(t.leaves))
		for j, l := range t.leaves {
			dataOffs[i][j] = total
			total += (len(l.data) + 7) &^ 7
		}
	}
	sec := make([]byte, total)
	putDir := func(off, ids int) { le.PutUint16(sec[off+14:], uint16(ids)) }
	putEntry := func(off int, id, target uint32) {
		le.PutUint32(sec[off:], id)
		le.PutUint32(sec[off+4:], target)
	}
	putDir(0, len(types))
	var relocs []uint32
	for i, t := range types {
		putEntry(dirSize+i*entrySize, t.id, uint32(typeDirs[i])|0x80000000)
		putDir(typeDirs[i], len(t.leaves))
		for j, l := range t.leaves {
			putEntry(typeDirs[i]+dirSize+j*entrySize, l.id, uint32(nameDirs[i][j])|0x80000000)
			putDir(nameDirs[i][j], 1)
			putEntry(nameDirs[i][j]+dirSize, 0x0409, uint32(dataEntries[i][j]))
			le.PutUint32(sec[dataEntries[i][j]:], uint32(dataOffs[i][j]))
			le.PutUint32(sec[dataEntries[i][j]+4:], uint32(len(l.data)))
			copy(sec[dataOffs[i][j]:], l.data)
			relocs = append(relocs, uint32(dataEntries[i][j]))
		}
	}

	// COFF object: header, one section header, section data, relocations, symbol.
	const fileHdr, secHdr, relSize, symSize = 20, 40, 10, 18
	secOff := fileHdr + secHdr
	relOff := secOff + len(sec)
	symOff := relOff + len(relocs)*relSize
	var out bytes.Buffer
	_ = binary.Write(&out, le, struct {
		Machine, NSec      uint16
		Time, SymPtr, NSym uint32
		OptSize, Chars     uint16
	}{0x8664, 1, 0, uint32(symOff), 1, 0, 0x104})
	name := [8]byte{'.', 'r', 's', 'r', 'c'}
	out.Write(name[:])
	_ = binary.Write(&out, le, struct {
		VSize, VAddr, Size, Ptr, RelPtr, LinePtr uint32
		NRel, NLine                              uint16
		Chars                                    uint32
	}{0, 0, uint32(len(sec)), uint32(secOff), uint32(relOff), 0, uint16(len(relocs)), 0, 0x40000040})
	out.Write(sec)
	for _, r := range relocs {
		_ = binary.Write(&out, le, r)
		_ = binary.Write(&out, le, uint32(0))
		_ = binary.Write(&out, le, uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	}
	out.Write(name[:])
	_ = binary.Write(&out, le, uint32(0))
	_ = binary.Write(&out, le, int16(1))
	_ = binary.Write(&out, le, uint16(0))
	out.Write([]byte{3, 0})
	_ = binary.Write(&out, le, uint32(0)) // empty string table
	if err := os.WriteFile("rsrc_windows_amd64.syso", out.Bytes(), 0o644); err != nil {
		panic(err)
	}
}
