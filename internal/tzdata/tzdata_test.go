// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzdata

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// --- TZif builders -----------------------------------------------------------

func be32b(v int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}
func be64b(v int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

// tzifBlock builds one TZif data block (magic + counts + data) with the given
// time size. transitions are (when, typeIdx) pairs; types are (offset, isdst,
// abbrIdx); abbrevs is the raw abbreviation table.
type ttype struct {
	off     int32
	dst     bool
	abbrIdx byte
}

func buildBlock(ver byte, timeSize int, whens []int64, tidx []byte, types []ttype, abbr []byte) []byte {
	var b bytes.Buffer
	b.WriteString("TZif")
	b.WriteByte(ver)
	b.Write(make([]byte, 15)) // reserved
	b.Write(be32b(0))         // isutcnt
	b.Write(be32b(0))         // isstdcnt
	b.Write(be32b(0))         // leapcnt
	b.Write(be32b(int32(len(whens))))
	b.Write(be32b(int32(len(types))))
	b.Write(be32b(int32(len(abbr)))) // charcnt
	for _, w := range whens {
		if timeSize == 8 {
			b.Write(be64b(w))
		} else {
			b.Write(be32b(int32(w)))
		}
	}
	b.Write(tidx)
	for _, ty := range types {
		b.Write(be32b(ty.off))
		if ty.dst {
			b.WriteByte(1)
		} else {
			b.WriteByte(0)
		}
		b.WriteByte(ty.abbrIdx)
	}
	b.Write(abbr)
	return b.Bytes()
}

// buildV2 builds a full v1+v2 TZif file (v1 block then a v2 block) with a footer.
func buildV2(whens []int64, tidx []byte, types []ttype, abbr []byte, footer string) []byte {
	v1 := buildBlock('2', 4, whens, tidx, types, abbr)
	v2 := buildBlock('2', 8, whens, tidx, types, abbr)
	out := append([]byte{}, v1...)
	out = append(out, v2...)
	out = append(out, '\n')
	out = append(out, footer...)
	out = append(out, '\n')
	return out
}

// --- parseZip error branches -------------------------------------------------

// makeZip builds a store-method zip of the given entries.
func makeZip(entries map[string][]byte) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		fw, _ := w.CreateHeader(h)
		fw.Write(data)
	}
	w.Close()
	return buf.Bytes()
}

func TestParseZipRoundTrip(t *testing.T) {
	z := makeZip(map[string][]byte{"A": []byte("aa"), "B": []byte("bbb")})
	m, names, err := parseZip(z)
	if err != nil {
		t.Fatal(err)
	}
	if string(m["A"]) != "aa" || string(m["B"]) != "bbb" {
		t.Errorf("data mismatch: %v", m)
	}
	if strings.Join(names, ",") != "A,B" {
		t.Errorf("names = %v", names)
	}
}

func TestParseZipErrors(t *testing.T) {
	// Too small.
	if _, _, err := parseZip([]byte("x")); err == nil {
		t.Error("expected archive-too-small error")
	}
	// Right size but no EOCD signature.
	bad := make([]byte, 22)
	if _, _, err := parseZip(bad); err == nil {
		t.Error("expected missing-EOCD error")
	}
	// Valid EOCD claiming a central-directory entry that isn't there.
	z := makeZip(map[string][]byte{"A": []byte("aa")})
	// Corrupt the central-directory signature.
	cdOff := int(binary.LittleEndian.Uint32(z[len(z)-6:]))
	corrupt := append([]byte{}, z...)
	corrupt[cdOff] ^= 0xFF
	if _, _, err := parseZip(corrupt); err == nil {
		t.Error("expected corrupt-central-directory error")
	}
	// Compression method != store.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, _ := w.CreateHeader(&zip.FileHeader{Name: "C", Method: zip.Deflate})
	fw.Write(bytes.Repeat([]byte("z"), 200))
	w.Close()
	if _, _, err := parseZip(buf.Bytes()); err == nil {
		t.Error("expected unexpected-compression error")
	}
}

func TestParseZipCorruptLocalHeader(t *testing.T) {
	z := makeZip(map[string][]byte{"A": []byte("aa")})
	// Local file header offset is at central-directory field +42. Point it past
	// the file so the local-header walk fails.
	cdOff := int(binary.LittleEndian.Uint32(z[len(z)-6:]))
	corrupt := append([]byte{}, z...)
	binary.LittleEndian.PutUint32(corrupt[cdOff+42:], uint32(len(z)+100))
	if _, _, err := parseZip(corrupt); err == nil {
		t.Error("expected corrupt-local-header error")
	}
}

func TestParseZipTruncatedData(t *testing.T) {
	z := makeZip(map[string][]byte{"A": bytes.Repeat([]byte("x"), 50)})
	// Inflate the recorded uncompressed size in the central directory (+24) so the
	// data slice overruns the archive.
	cdOff := int(binary.LittleEndian.Uint32(z[len(z)-6:]))
	corrupt := append([]byte{}, z...)
	binary.LittleEndian.PutUint32(corrupt[cdOff+24:], uint32(len(z)+1000))
	if _, _, err := parseZip(corrupt); err == nil {
		t.Error("expected truncated-data error")
	}
}

// --- parseTZif / readHeader / parseBlock branches ----------------------------

func TestParseTZifNotTZif(t *testing.T) {
	if _, err := parseTZif([]byte("not a tzif file at all........................")); err == nil {
		t.Error("expected not-a-TZif error")
	}
	if _, err := parseTZif([]byte("short")); err == nil {
		t.Error("expected not-a-TZif error for short input")
	}
}

func TestParseTZifV1Only(t *testing.T) {
	// A version-0 (v1-only) file: a single non-DST offset, one transition.
	blk := buildBlock(0, 4, []int64{0}, []byte{0},
		[]ttype{{off: 3600, dst: false, abbrIdx: 0}}, []byte("ABC\x00"))
	z, err := parseTZif(blk)
	if err != nil {
		t.Fatal(err)
	}
	if len(z.Types) != 1 || z.Types[0].Offset != 3600 || z.Types[0].Abbrev != "ABC" {
		t.Errorf("v1 parse wrong: %+v", z.Types)
	}
}

func TestReadHeaderShort(t *testing.T) {
	if _, err := readHeader(make([]byte, 23)); err == nil {
		t.Error("readHeader should reject <24 bytes")
	}
	if _, err := readHeader(make([]byte, 24)); err != nil {
		t.Errorf("readHeader(24) err = %v", err)
	}
}

func TestParseTZifShortV1Header(t *testing.T) {
	// The 20-byte magic+version prefix is present but the count block that follows
	// is shorter than 24 bytes, so the v1 readHeader call reports it.
	raw := append([]byte("TZif2"), make([]byte, 25)...) // 30 bytes: raw[20:] is 10 bytes
	if _, err := parseTZif(raw); err == nil {
		t.Error("expected v1 short-header error")
	}
}

func TestParseTZifMissingV2(t *testing.T) {
	// A v2-versioned v1 block with no following v2 block.
	blk := buildBlock('2', 4, []int64{0}, []byte{0},
		[]ttype{{off: 0, dst: false, abbrIdx: 0}}, []byte("X\x00"))
	if _, err := parseTZif(blk); err == nil {
		t.Error("expected missing-v2-block error")
	}
}

func TestParseTZifTruncatedBlock(t *testing.T) {
	// Build a valid v2 file, then truncate the tail so the v2 data block is short.
	full := buildV2([]int64{0}, []byte{0}, []ttype{{off: 0, dst: false, abbrIdx: 0}}, []byte("X\x00"), "")
	// Find the second TZif and cut a few bytes off the end of the whole file.
	trunc := full[:len(full)-len("X\x00")-3]
	if _, err := parseTZif(trunc); err == nil {
		t.Error("expected truncated-data-block error")
	}
}

func TestParseTZifV2ShortHeader(t *testing.T) {
	// A v1 block with all-zero counts (skip == 44), then a v2 "TZif" magic whose
	// following count block is shorter than 24 bytes. skip+20 must be <= len (so
	// the missing-v2 guard passes) while raw[skip+20:] stays under 24 bytes,
	// reaching the v2-side readHeader error.
	v1 := buildBlock('2', 4, nil, nil, nil, nil) // 44 bytes, zero counts
	raw := append([]byte{}, v1...)
	raw = append(raw, "TZif2"...)          // magic at offset 44
	raw = append(raw, make([]byte, 30)...) // total 79 bytes: raw[64:] is 15 bytes
	if _, err := parseTZif(raw); err == nil {
		t.Error("expected v2 short-header error")
	}
}

func TestParseTZifV2FirstNonDST(t *testing.T) {
	// A DST type at index 0 and a non-DST type at index 1: First must pick index 1.
	full := buildV2(
		[]int64{0, 100},
		[]byte{0, 1},
		[]ttype{{off: 7200, dst: true, abbrIdx: 4}, {off: 3600, dst: false, abbrIdx: 0}},
		[]byte("STD\x00DST\x00"),
		"STD-1DST,M3.5.0,M10.5.0",
	)
	z, err := parseTZif(full)
	if err != nil {
		t.Fatal(err)
	}
	if z.First != 1 {
		t.Errorf("First = %d, want 1 (first non-DST)", z.First)
	}
}

func TestAbbrevAt(t *testing.T) {
	tab := []byte("EST\x00EDT\x00")
	if got := abbrevAt(tab, 0); got != "EST" {
		t.Errorf("abbrevAt(0) = %q", got)
	}
	if got := abbrevAt(tab, 4); got != "EDT" {
		t.Errorf("abbrevAt(4) = %q", got)
	}
	// Out-of-range index yields empty.
	if got := abbrevAt(tab, 99); got != "" {
		t.Errorf("abbrevAt(99) = %q, want empty", got)
	}
	if got := abbrevAt(tab, -1); got != "" {
		t.Errorf("abbrevAt(-1) = %q, want empty", got)
	}
}

// --- index / Names / Load forwarding -----------------------------------------

func TestNamesAndLoadRealData(t *testing.T) {
	names, err := Names()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 598 {
		t.Errorf("Names len = %d, want 598", len(names))
	}
	if _, err := Load("America/New_York"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("No/Such_Zone"); err != ErrNotFound {
		t.Errorf("Load(unknown) err = %v, want ErrNotFound", err)
	}
}

// withBrokenIndex forces the lazily-built index to hold an error, so the
// error-forwarding branches of Names and Load run, then restores the real index.
func withBrokenIndex(t *testing.T, fn func()) {
	t.Helper()
	savedEntry, savedNames, savedErr := zipEntry, names, indexErr
	savedData := zipData
	// Point the source at a malformed archive and rebuild the index directly.
	zipData = func() []byte { return []byte("too small") }
	zipEntry, names, indexErr = parseZip(zipData())
	defer func() {
		zipEntry, names, indexErr = savedEntry, savedNames, savedErr
		zipData = savedData
	}()
	fn()
}

func TestNamesLoadErrorForwarding(t *testing.T) {
	// Ensure the index is built once (so indexOnce is spent) before we tamper.
	if _, err := Names(); err != nil {
		t.Fatal(err)
	}
	withBrokenIndex(t, func() {
		if _, err := Names(); err == nil {
			t.Error("Names should forward the index error")
		}
		if _, err := Load("America/New_York"); err == nil {
			t.Error("Load should forward the index error")
		}
	})
}

// --- deriveOffsets edge cases ------------------------------------------------

func TestDeriveNoTransitionsDST(t *testing.T) {
	// A degenerate zone whose only (first) type is DST and has no transitions:
	// Base falls back to observed-1h.
	z := &Zone{
		Types: []LocalType{{Offset: 7200, IsDST: true, Abbrev: "X"}},
		First: 0,
	}
	deriveOffsets(z)
	if z.Types[0].Base != 3600 {
		t.Errorf("degenerate DST base = %d, want 3600", z.Types[0].Base)
	}
}

func TestDeriveNextSideAndNoDifference(t *testing.T) {
	// DST period whose base must come from the *next* non-DST offset (no prior
	// non-DST), and a later DST period with no difference on either side (base =
	// observed-1h).
	z := &Zone{
		Types: []LocalType{
			{Offset: 7200, IsDST: true, Abbrev: "D1"},  // 0: DST, no prev std
			{Offset: 3600, IsDST: false, Abbrev: "S1"}, // 1: std
			{Offset: 3600, IsDST: true, Abbrev: "D2"},  // 2: DST equal to std (no diff)
		},
		Transitions: []Transition{
			{When: 0, Type: 0},
			{When: 100, Type: 1},
			{When: 200, Type: 2},
		},
		First: 1,
	}
	// First index is a non-DST type, but make the pre-first a DST so
	// base_utc_offset_from_previous starts unset for the first DST transition.
	z.First = 0
	deriveOffsets(z)
	// Transition 0 (DST 7200) has no previous std, next std is 3600 → diff 3600.
	if z.Types[z.Transitions[0].Type].Base != 3600 {
		t.Errorf("trans0 base = %d, want 3600", z.Types[z.Transitions[0].Type].Base)
	}
	// Transition 2 (DST 3600) equals the surrounding std 3600 → no difference,
	// base = observed-1h = 0.
	if z.Types[z.Transitions[2].Type].Base != 0 {
		t.Errorf("trans2 base = %d, want 0", z.Types[z.Transitions[2].Type].Base)
	}
}

func TestDeriveBothSidesNon3600(t *testing.T) {
	// A DST offset whose nearest non-DST offsets on both sides differ from it by
	// non-zero, non-3600 amounts. The nearer side wins.
	//   prev std = 0, DST observed = 5400 (diffPrev=5400), next std = 7200
	//   (diffNext=1800). diffNext < diffPrev, so base = next = 7200.
	z := &Zone{
		Types: []LocalType{
			{Offset: 0, IsDST: false, Abbrev: "P", Base: 0},
			{Offset: 5400, IsDST: true, Abbrev: "D"},
			{Offset: 7200, IsDST: false, Abbrev: "N", Base: 7200},
		},
		Transitions: []Transition{
			{When: 0, Type: 0},
			{When: 100, Type: 1},
			{When: 200, Type: 2},
		},
		First: 0,
	}
	deriveOffsets(z)
	if got := z.Types[z.Transitions[1].Type].Base; got != 7200 {
		t.Errorf("nearer-next base = %d, want 7200", got)
	}

	// Mirror: prev the nearer side.
	//   prev std = 7200 (diffPrev=1800), DST=5400, next std=0 (diffNext=5400).
	z2 := &Zone{
		Types: []LocalType{
			{Offset: 7200, IsDST: false, Abbrev: "P", Base: 7200},
			{Offset: 5400, IsDST: true, Abbrev: "D"},
			{Offset: 0, IsDST: false, Abbrev: "N", Base: 0},
		},
		Transitions: []Transition{
			{When: 0, Type: 0},
			{When: 100, Type: 1},
			{When: 200, Type: 2},
		},
		First: 0,
	}
	deriveOffsets(z2)
	if got := z2.Types[z2.Transitions[1].Type].Base; got != 7200 {
		t.Errorf("nearer-prev base = %d, want 7200", got)
	}
}

func TestDeriveOnlyNextNonZero(t *testing.T) {
	// A DST period with no previous non-DST (prev unset → diffPrev==0) and a next
	// non-DST that differs by a non-3600 amount, so base comes from next.
	z := &Zone{
		Types: []LocalType{
			{Offset: 5400, IsDST: true, Abbrev: "D"},              // 0: DST, no prior std
			{Offset: 7200, IsDST: false, Abbrev: "N", Base: 7200}, // 1: std
		},
		Transitions: []Transition{
			{When: 0, Type: 0},
			{When: 100, Type: 1},
		},
		First: 0, // First is DST → base_utc_offset_from_previous stays unset.
	}
	deriveOffsets(z)
	if got := z.Types[z.Transitions[0].Type].Base; got != 7200 {
		t.Errorf("only-next base = %d, want 7200", got)
	}
}

func TestDeriveSplitType(t *testing.T) {
	// One DST type observed with two different bases across history forces a split
	// into a second synthesized LocalType.
	z := &Zone{
		Types: []LocalType{
			{Offset: 0, IsDST: false, Abbrev: "A", Base: 0},         // 0
			{Offset: 3600, IsDST: true, Abbrev: "D"},                // 1 (DST)
			{Offset: -3600, IsDST: false, Abbrev: "B", Base: -3600}, // 2
		},
		Transitions: []Transition{
			{When: 0, Type: 0},   // std base 0
			{When: 100, Type: 1}, // DST -> base 0
			{When: 200, Type: 2}, // std base -3600
			{When: 300, Type: 1}, // DST again -> base -3600 (needs split)
		},
		First: 0,
	}
	before := len(z.Types)
	deriveOffsets(z)
	if len(z.Types) <= before {
		t.Errorf("expected a split adding a new type, got %d types", len(z.Types))
	}
	// The two DST transitions must now reference types with different bases.
	b1 := z.Types[z.Transitions[1].Type].Base
	b3 := z.Types[z.Transitions[3].Type].Base
	if b1 == b3 {
		t.Errorf("expected different bases after split, both %d", b1)
	}
}
