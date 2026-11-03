// mkres generates a .syso file with a program's Windows resources: icon, manifest and
// version information. The Go linker includes it only when building for Windows, so the
// .exe carries its icon (Explorer, taskbar, shortcuts) without external tools.
//
//	go run ./tools/mkres -ico app.ico -o cmd/omniupdate/rsrc_windows_amd64.syso -name OmniUpdate ...
//	go run ./tools/mkres -check dist/OmniUpdate.exe      # checks the resources of an .exe
package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24
	langEnUS    = 0x0409
)

type res struct {
	typ, id uint16
	data    []byte
}

func main() {
	ico := flag.String("ico", "", "icono .ico")
	out := flag.String("o", "", "archivo .syso de salida")
	name := flag.String("name", "OmniUpdate", "nombre del producto")
	desc := flag.String("desc", "", "descripción del archivo")
	orig := flag.String("orig", "", "nombre original del .exe")
	ver := flag.String("version", "1.0.0", "versión (a.b.c)")
	admin := flag.Bool("admin", false, "el manifiesto pide permisos de administrador")
	check := flag.String("check", "", "comprobar los recursos de un .exe y salir")
	flag.Parse()
	if *check != "" {
		if err := checkExe(*check); err != nil {
			fmt.Fprintln(os.Stderr, "ERROR:", err)
			os.Exit(1)
		}
		return
	}
	if *ico == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	icoData, err := os.ReadFile(*ico)
	if err != nil {
		fatal(err)
	}
	rs, err := buildResources(icoData, manifest(*name, *admin), versionInfo(*name, *desc, *orig, *ver))
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*out, buildCOFF(rs), 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "ERROR:", err); os.Exit(1) }

// ------------------------------------------------------------------ resources

func buildResources(ico, manifestXML, verInfo []byte) ([]res, error) {
	if len(ico) < 6 || binary.LittleEndian.Uint16(ico[2:]) != 1 {
		return nil, fmt.Errorf("no es un archivo .ico válido")
	}
	n := int(binary.LittleEndian.Uint16(ico[4:]))
	if n == 0 || len(ico) < 6+16*n {
		return nil, fmt.Errorf(".ico sin imágenes")
	}
	var rs []res
	grp := new(bytes.Buffer)
	binary.Write(grp, binary.LittleEndian, [3]uint16{0, 1, uint16(n)})
	for i := 0; i < n; i++ {
		e := ico[6+16*i : 6+16*(i+1)]
		size := binary.LittleEndian.Uint32(e[8:])
		off := binary.LittleEndian.Uint32(e[12:])
		if int(off)+int(size) > len(ico) {
			return nil, fmt.Errorf(".ico dañado (imagen %d)", i)
		}
		id := uint16(i + 1)
		rs = append(rs, res{rtIcon, id, ico[off : off+size]})
		grp.Write(e[:8])                             // width, height, colors, reserved, planes, bits
		binary.Write(grp, binary.LittleEndian, size) // image bytes
		binary.Write(grp, binary.LittleEndian, id)   // RT_ICON resource identifier
	}
	rs = append(rs, res{rtGroupIcon, 1, grp.Bytes()}, res{rtVersion, 1, verInfo}, res{rtManifest, 1, manifestXML})
	return rs, nil
}

func manifest(name string, admin bool) []byte {
	level := "asInvoker"
	if admin {
		level = "requireAdministrator"
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="` + name + `" version="1.0.0.0"/>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security><requestedPrivileges><requestedExecutionLevel level="` + level + `" uiAccess="false"/></requestedPrivileges></security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
      <supportedOS Id="{1f676c76-80e1-4239-95bb-83d0f6d0da78}"/>
    </application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2, PerMonitor</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
    </windowsSettings>
  </application>
</assembly>
`)
}

// ------------------------------------------------------------------ version information

func u16(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*(len(u)+1))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

func pad4(b *bytes.Buffer) {
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
}

// vblock writes a VS_VERSIONINFO block: length, value length, type, key, value and children.
func vblock(key string, valLen uint16, text bool, value []byte, children ...[]byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0, 0})
	binary.Write(&b, binary.LittleEndian, valLen)
	t := uint16(0)
	if text {
		t = 1
	}
	binary.Write(&b, binary.LittleEndian, t)
	b.Write(u16(key))
	pad4(&b)
	b.Write(value)
	for _, c := range children {
		pad4(&b)
		b.Write(c)
	}
	out := b.Bytes()
	binary.LittleEndian.PutUint16(out[0:], uint16(len(out)))
	return out
}

func versionInfo(name, desc, orig, ver string) []byte {
	parts := strings.Split(ver, ".")
	var v [4]uint16
	for i := 0; i < 4 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		v[i] = uint16(n)
	}
	full := fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], v[3])
	fixed := new(bytes.Buffer)
	for _, x := range []uint32{0xFEEF04BD, 0x00010000, uint32(v[0])<<16 | uint32(v[1]), uint32(v[2])<<16 | uint32(v[3]),
		uint32(v[0])<<16 | uint32(v[1]), uint32(v[2])<<16 | uint32(v[3]), 0x3F, 0, 0x00040004, 1, 0, 0, 0} {
		binary.Write(fixed, binary.LittleEndian, x)
	}
	kv := [][2]string{{"CompanyName", name}, {"FileDescription", desc}, {"FileVersion", full}, {"InternalName", name},
		{"LegalCopyright", "MIT License"}, {"OriginalFilename", orig}, {"ProductName", name}, {"ProductVersion", ver}}
	var strs [][]byte
	for _, p := range kv {
		if p[1] == "" {
			continue
		}
		val := u16(p[1])
		strs = append(strs, vblock(p[0], uint16(len(val)/2), true, val))
	}
	table := vblock("040904B0", 0, true, nil, strs...)
	sfi := vblock("StringFileInfo", 0, true, nil, table)
	trans := []byte{0x09, 0x04, 0xB0, 0x04}
	vfi := vblock("VarFileInfo", 0, true, nil, vblock("Translation", 4, false, trans))
	return vblock("VS_VERSION_INFO", uint16(fixed.Len()), false, fixed.Bytes(), sfi, vfi)
}

// ------------------------------------------------------------------ COFF object (.syso)

type dirEntry struct {
	id     uint16
	child  *dirNode
	dataIx int // data index (if it is a leaf)
}
type dirNode struct{ entries []dirEntry }

func buildCOFF(rs []res) []byte {
	// tree: type → id → language → data
	byType := map[uint16]map[uint16]int{}
	for i, r := range rs {
		if byType[r.typ] == nil {
			byType[r.typ] = map[uint16]int{}
		}
		byType[r.typ][r.id] = i
	}
	var types []int
	for t := range byType {
		types = append(types, int(t))
	}
	sort.Ints(types)

	// 1st pass: sizes. Directories = 16 bytes + 8 per entry.
	dirSize := func(n int) int { return 16 + 8*n }
	size := dirSize(len(types))
	typeOff := map[uint16]int{}
	idOff := map[[2]uint16]int{}
	langOff := map[[2]uint16]int{}
	ids := map[uint16][]int{}
	for _, t := range types {
		typeOff[uint16(t)] = size
		var l []int
		for id := range byType[uint16(t)] {
			l = append(l, int(id))
		}
		sort.Ints(l)
		ids[uint16(t)] = l
		size += dirSize(len(l))
	}
	for _, t := range types {
		for _, id := range ids[uint16(t)] {
			idOff[[2]uint16{uint16(t), uint16(id)}] = size
			size += dirSize(1)
		}
	}
	entryOff := map[int]int{}
	for _, t := range types {
		for _, id := range ids[uint16(t)] {
			ix := byType[uint16(t)][uint16(id)]
			langOff[[2]uint16{uint16(t), uint16(id)}] = size
			entryOff[ix] = size // the leaf (IMAGE_RESOURCE_DATA_ENTRY) is written in the 3rd pass
		}
	}
	// the data entries (16 bytes) follow the directories
	dataEntries := size
	size += 16 * len(rs)
	dataOff := make([]int, len(rs))
	for i, r := range rs {
		size = (size + 3) &^ 3
		dataOff[i] = size
		size += len(r.data)
	}
	buf := make([]byte, size)
	le := binary.LittleEndian
	writeDir := func(off, n int) {
		le.PutUint16(buf[off+14:], uint16(n)) // entries per id
	}
	put := func(off int, id uint16, target int, isDir bool) {
		le.PutUint32(buf[off:], uint32(id))
		v := uint32(target)
		if isDir {
			v |= 0x80000000
		}
		le.PutUint32(buf[off+4:], v)
	}
	writeDir(0, len(types))
	for i, t := range types {
		put(16+8*i, uint16(t), typeOff[uint16(t)], true)
		writeDir(typeOff[uint16(t)], len(ids[uint16(t)]))
		for j, id := range ids[uint16(t)] {
			put(typeOff[uint16(t)]+16+8*j, uint16(id), idOff[[2]uint16{uint16(t), uint16(id)}], true)
		}
	}
	// language directories and data entries
	relocs := []int{}
	for _, t := range types {
		for _, id := range ids[uint16(t)] {
			ix := byType[uint16(t)][uint16(id)]
			lo := idOff[[2]uint16{uint16(t), uint16(id)}]
			writeDir(lo, 1)
			de := dataEntries + 16*ix
			put(lo+16, langEnUS, de, false)
			le.PutUint32(buf[de:], uint32(dataOff[ix])) // RVA: completed by the relocation
			le.PutUint32(buf[de+4:], uint32(len(rs[ix].data)))
			relocs = append(relocs, de)
			copy(buf[dataOff[ix]:], rs[ix].data)
		}
	}
	_ = langOff
	_ = entryOff

	// COFF
	const hdr, sec = 20, 40
	secRaw := hdr + sec
	relocOff := secRaw + len(buf)
	symOff := relocOff + 10*len(relocs)
	out := new(bytes.Buffer)
	binary.Write(out, le, [2]uint16{0x8664, 1})            // AMD64 machine, 1 section
	binary.Write(out, le, [3]uint32{0, uint32(symOff), 1}) // date, symbol table, number of symbols
	binary.Write(out, le, [2]uint16{0, 0})                 // optional header, characteristics
	name := [8]byte{'.', 'r', 's', 'r', 'c'}
	out.Write(name[:])
	binary.Write(out, le, [4]uint32{0, 0, uint32(len(buf)), uint32(secRaw)})
	binary.Write(out, le, uint32(relocOff))
	binary.Write(out, le, uint32(0))
	binary.Write(out, le, uint16(len(relocs)))
	binary.Write(out, le, uint16(0))
	binary.Write(out, le, uint32(0x40000040)) // initialized data, read
	out.Write(buf)
	for _, r := range relocs {
		binary.Write(out, le, uint32(r))
		binary.Write(out, le, uint32(0)) // symbol 0 (.rsrc)
		binary.Write(out, le, uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	}
	sym := [8]byte{'.', 'r', 's', 'r', 'c'}
	out.Write(sym[:])
	binary.Write(out, le, uint32(0)) // value
	binary.Write(out, le, uint16(1)) // section 1
	binary.Write(out, le, uint16(0)) // type
	out.WriteByte(3)                 // class: static
	out.WriteByte(0)                 // no auxiliary symbols
	binary.Write(out, le, uint32(4)) // empty string table
	return out.Bytes()
}

// ------------------------------------------------------------------ checking an .exe

func checkExe(path string) error {
	f, err := pe.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var s *pe.Section
	for _, x := range f.Sections {
		if x.Name == ".rsrc" {
			s = x
		}
	}
	if s == nil {
		return fmt.Errorf("el .exe no tiene sección .rsrc: no lleva icono ni manifiesto")
	}
	data, err := s.Data()
	if err != nil {
		return err
	}
	le := binary.LittleEndian
	count := func(off int) int { return int(le.Uint16(data[off+12:])) + int(le.Uint16(data[off+14:])) }
	found := map[uint32]int{}
	n := count(0)
	for i := 0; i < n; i++ {
		id := le.Uint32(data[16+8*i:])
		sub := int(le.Uint32(data[20+8*i:]) & 0x7FFFFFFF)
		found[id] = count(sub)
	}
	for _, need := range []struct {
		id   uint32
		name string
	}{{rtIcon, "RT_ICON"}, {rtGroupIcon, "RT_GROUP_ICON"}, {rtVersion, "RT_VERSION"}, {rtManifest, "RT_MANIFEST"}} {
		if found[need.id] == 0 {
			return fmt.Errorf("falta el recurso %s", need.name)
		}
	}
	fmt.Printf("OK: %s lleva %d imágenes de icono, grupo de icono, versión y manifiesto\n", path, found[rtIcon])
	return nil
}
