package main

import (
	"encoding/binary"
	"path/filepath"
	"regexp"
	"strings"
)

// Steam keeps a file on each PC, appcache\appinfo.vdf, describing games that account has
// seen, including ones that aren't installed anymore. Reading it needs no list, no
// account, and no internet, and it only ever describes that person's own Steam.

type steamGame struct {
	Name  string
	Dir   string
	Exes  []string
	Cloud []string // AppData locations from Steam Cloud, like local\factorygame
}

type kvReader struct {
	d     []byte
	p     int
	table []string
	v29   bool
}

func (r *kvReader) cstr() string {
	e := r.p
	for e < len(r.d) && r.d[e] != 0 {
		e++
	}
	s := string(r.d[r.p:e])
	r.p = e + 1
	return s
}

func (r *kvReader) key() string {
	if r.v29 {
		if r.p+4 > len(r.d) {
			panic("short")
		}
		i := binary.LittleEndian.Uint32(r.d[r.p:])
		r.p += 4
		if int(i) >= len(r.table) {
			panic("bad key")
		}
		return r.table[i]
	}
	return r.cstr()
}

// read parses one nested section of Steam's binary key-value format.
func (r *kvReader) read() map[string]any {
	out := map[string]any{}
	for {
		if r.p >= len(r.d) {
			panic("short")
		}
		t := r.d[r.p]
		r.p++
		if t == 8 {
			return out
		}
		k := r.key()
		switch t {
		case 0:
			out[k] = r.read()
		case 1:
			out[k] = r.cstr()
		case 2:
			r.p += 4
		case 7:
			r.p += 8
		default:
			panic("unknown type")
		}
	}
}

func section(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		next, ok := m[k].(map[string]any)
		if !ok {
			return nil
		}
		m = next
	}
	return m
}

func text(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// parseSteamAppInfo reads appinfo.vdf. Steam changes this format from time to time, so
// anything unexpected returns what was read so far rather than failing the scan.
func parseSteamAppInfo(d []byte) (games []steamGame) {
	defer func() { _ = recover() }()
	if len(d) < 16 {
		return nil
	}
	magic := binary.LittleEndian.Uint32(d)
	var table []string
	start := 8
	v29 := false
	switch magic {
	case 0x07564429, 0x0756442A:
		v29 = true
		off := int(binary.LittleEndian.Uint64(d[8:]))
		if off <= 0 || off+4 > len(d) {
			return nil
		}
		count := int(binary.LittleEndian.Uint32(d[off:]))
		p := off + 4
		for i := 0; i < count && p < len(d); i++ {
			e := p
			for e < len(d) && d[e] != 0 {
				e++
			}
			table = append(table, string(d[p:e]))
			p = e + 1
		}
		start = 16
	case 0x07564428:
	default:
		return nil
	}
	p := start
	for p+8 <= len(d) {
		id := binary.LittleEndian.Uint32(d[p:])
		if id == 0 {
			break
		}
		size := int(binary.LittleEndian.Uint32(d[p+4:]))
		next := p + 8 + size
		if size < 60 || next > len(d) {
			break
		}
		r := &kvReader{d: d[:next], p: p + 8 + 4 + 4 + 8 + 20 + 4 + 20, table: table, v29: v29}
		info := r.read()
		if a := section(info, "appinfo"); a != nil {
			info = a
		}
		p = next
		common := section(info, "common")
		if common == nil {
			continue
		}
		switch strings.ToLower(text(common, "type")) {
		case "game", "demo", "application":
		default:
			continue
		}
		g := steamGame{Name: text(common, "name")}
		if g.Name == "" {
			continue
		}
		if cfg := section(info, "config"); cfg != nil {
			g.Dir = text(cfg, "installdir")
			if launch := section(cfg, "launch"); launch != nil {
				for _, v := range launch {
					entry, ok := v.(map[string]any)
					if !ok {
						continue
					}
					if c := section(entry, "config"); c != nil {
						if os := strings.ToLower(text(c, "oslist")); os != "" && !strings.Contains(os, "windows") {
							continue
						}
					}
					if e := text(entry, "executable"); strings.HasSuffix(strings.ToLower(e), ".exe") {
						g.Exes = append(g.Exes, e)
					}
				}
			}
		}
		if saves := section(info, "ufs", "savefiles"); saves != nil {
			for _, v := range saves {
				entry, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if key := steamCloudKey(text(entry, "root"), text(entry, "path")); key != "" {
					g.Cloud = append(g.Cloud, key)
				}
			}
		}
		games = append(games, g)
	}
	return games
}

// steamCloudKey turns a Steam Cloud save location into an AppData folder key.
func steamCloudKey(root, path string) string {
	var label string
	switch strings.ToLower(root) {
	case "winappdatalocal":
		label = "local"
	case "winappdatalocallow":
		label = "locallow"
	case "winappdataroaming":
		label = "roaming"
	default:
		return ""
	}
	parts := strings.FieldsFunc(strings.ReplaceAll(path, `\`, "/"), func(r rune) bool { return r == '/' })
	if len(parts) == 0 || strings.ContainsAny(parts[0], "{}*") {
		return ""
	}
	key := label + `\` + strings.ToLower(parts[0])
	if label == "locallow" && len(parts) > 1 && !strings.ContainsAny(parts[1], "{}*") {
		key += `\` + strings.ToLower(parts[1])
	}
	return key
}

var steamGenericNames = map[string]bool{"game": true, "games": true, "launcher": true, "launch": true, "start": true, "play": true, "run": true,
	"client": true, "bin": true, "binaries": true, "win64": true, "win32": true, "x64": true, "x86": true, "shipping": true, "data": true, "app": true,
	"main": true, "setup": true, "install": true, "engine": true, "windows": true, "windowsnoeditor": true, "retail": true, "release": true, "system": true,
	"pc": true, "steam": true, "nw": true, "electron": true, "index": true, "project": true, "myproject": true, "unity": true, "godot": true, "renpy": true,
	"default": true, "test": true, "demo": true, "content": true, "build": true, "bootstrap": true, "bootstrapper": true, "update": true, "updater": true,
	"local": true, "epic": true, "common": true, "shared": true, "temp": true, "saved": true, "config": true, "settings": true, "saves": true}

var exeSuffix = regexp.MustCompile(`(?i)(-win64-shipping|-win64-test|-wingdk-shipping|_?win64|_?x64|_?steam|_?client|_?launcher)$`)

// SteamIndex answers "which game does a folder with this name belong to?"
type SteamIndex struct {
	byName  map[string]string // from launch programs and install folders. Names shared by several games are removed.
	byCloud map[string]string // exact AppData locations from Steam Cloud
}

func buildSteamIndex(games []steamGame) *SteamIndex {
	idx := &SteamIndex{byName: map[string]string{}, byCloud: map[string]string{}}
	owners := map[string]map[string]bool{}
	for _, g := range games {
		add := func(s string) {
			k := norm(s)
			if len(k) < 3 || steamGenericNames[k] {
				return
			}
			if owners[k] == nil {
				owners[k] = map[string]bool{}
			}
			owners[k][g.Name] = true
		}
		add(g.Dir)
		for _, e := range g.Exes {
			parts := strings.FieldsFunc(strings.ReplaceAll(e, `\`, "/"), func(r rune) bool { return r == '/' })
			if len(parts) == 0 {
				continue
			}
			base := strings.TrimSuffix(parts[len(parts)-1], filepath.Ext(parts[len(parts)-1]))
			add(base)
			add(exeSuffix.ReplaceAllString(base, ""))
			for _, dir := range parts[:len(parts)-1] {
				add(dir)
			}
		}
		for _, c := range g.Cloud {
			if cur, ok := idx.byCloud[c]; ok && cur != g.Name {
				idx.byCloud[c] = "" // several games save here, so it says nothing
			} else {
				idx.byCloud[c] = g.Name
			}
		}
	}
	for k, names := range owners {
		if len(names) == 1 {
			for n := range names {
				idx.byName[k] = n
			}
		}
	}
	return idx
}

// looksLikeAGame is required before a name match counts. Lots of folders happen to share
// a name with some game on Steam, but few of them also have Unreal or Unity files.
func looksLikeAGame(f *Folder) bool {
	return f.GameShaped || f.Saves
}

// steamName returns the game a folder belongs to, if Steam's data on this PC says so.
func (s *SteamIndex) steamName(f *Folder) string {
	if s == nil {
		return ""
	}
	if n := s.byCloud[strings.ToLower(f.Rel)]; n != "" {
		return n
	}
	if !looksLikeAGame(f) {
		return ""
	}
	return s.byName[norm(f.Name)]
}
