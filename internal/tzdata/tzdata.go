// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

// Package tzdata embeds a complete, offline copy of the compiled IANA time zone
// database (the TZif files the tzinfo gem's ZoneinfoDataSource reads) and
// parses the TZif (zoneinfo) v2/v3 format into transition tables. It is the raw
// data layer under the TZInfo API: it knows nothing about the Ruby API, it only
// turns a zone identifier into the ordered list of UTC transitions and the local
// time-type (offset, abbreviation, DST flag) in force between them.
package tzdata

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"sort"
	"sync"
)

// zipBytes is the embedded IANA zoneinfo archive: an uncompressed (store-method)
// zip whose entries are the TZif files, keyed by zone identifier
// (e.g. "America/New_York"). Committed in-repo so the library is complete offline
// with no filesystem or network dependency.
//
//go:embed tzdata.zip
var zipBytes []byte

// ErrNotFound is returned by Load when the requested identifier is not present in
// the embedded database.
var ErrNotFound = errors.New("tzdata: zone not found")

// LocalType is one local time type: the total (observed) offset east of UTC in
// seconds, its abbreviation (e.g. "EST", "EDT", "LMT"), whether it is
// daylight-saving time, and the derived base (standard-time) offset. For a
// non-DST type Base == Offset; for a DST type Base is the standard offset from
// which the DST amount (Offset-Base) is measured, derived exactly as the tzinfo
// gem's ZoneinfoDataSource derives it (see derive.go). Zoneinfo files do not
// store the base offset for DST periods, so it must be inferred.
type LocalType struct {
	Offset int
	Abbrev string
	IsDST  bool
	Base   int
	// baseSet records that Base has been fixed by deriveOffsets, so a later
	// transition observing the same type with a different base triggers a split.
	baseSet bool
}

// Transition is a single instant (Unix seconds, UTC) at and after which LocalType
// index Type is in force.
type Transition struct {
	When int64
	Type int
}

// Zone is a fully parsed TZif zone: its ordered transition list and the local
// time types they index into. First is the local type in force before the first
// transition (the earliest recorded, typically LMT). Raw is the exact TZif byte
// content, used to detect linked (byte-identical) zones.
type Zone struct {
	Transitions []Transition
	Types       []LocalType
	First       int
	Raw         []byte
}

// zipIndex lazily maps every entry name to its stored (uncompressed) byte slice.
var (
	indexOnce sync.Once
	zipEntry  map[string][]byte
	names     []string
	indexErr  error
)

// zipData is the archive parseZip reads; it defaults to the embedded bytes but is
// a variable so tests can substitute a deliberately malformed archive to exercise
// the error-forwarding paths of index/Names/Load.
var zipData = func() []byte { return zipBytes }

// buildIndex parses the central directory of the embedded store-method zip once.
func buildIndex() {
	zipEntry, names, indexErr = parseZip(zipData())
}

// parseZip parses a store-method (uncompressed) zip archive into a name→bytes
// map plus the sorted name list. It is factored out of buildIndex so its every
// malformed-archive branch is exercisable with crafted inputs.
func parseZip(z []byte) (map[string][]byte, []string, error) {
	entries := map[string][]byte{}
	var names []string
	const (
		eocdSig     = 0x06054b50
		cdSig       = 0x02014b50
		localSig    = 0x04034b50
		eocdSize    = 22
		localHeader = 30
	)
	if len(z) < eocdSize {
		return nil, nil, errors.New("tzdata: archive too small")
	}
	// End-of-central-directory is at the tail (no zip comment in our archive).
	idx := len(z) - eocdSize
	if le32(z[idx:]) != eocdSig {
		return nil, nil, errors.New("tzdata: missing end-of-central-directory")
	}
	n := int(le16(z[idx+10:]))
	cd := int(le32(z[idx+16:]))
	for i := 0; i < n; i++ {
		if cd+46 > len(z) || le32(z[cd:]) != cdSig {
			return nil, nil, errors.New("tzdata: corrupt central directory")
		}
		meth := le16(z[cd+10:])
		size := int(le32(z[cd+24:]))
		namelen := int(le16(z[cd+28:]))
		xlen := int(le16(z[cd+30:]))
		fclen := int(le16(z[cd+32:]))
		off := int(le32(z[cd+42:]))
		name := string(z[cd+46 : cd+46+namelen])
		cd += 46 + namelen + xlen + fclen
		if meth != 0 {
			return nil, nil, errors.New("tzdata: unexpected compression for " + name)
		}
		// Walk the local file header to reach the data.
		if off+localHeader > len(z) || le32(z[off:]) != localSig {
			return nil, nil, errors.New("tzdata: corrupt local header for " + name)
		}
		lnamelen := int(le16(z[off+26:]))
		lxlen := int(le16(z[off+28:]))
		data := off + localHeader + lnamelen + lxlen
		if data+size > len(z) {
			return nil, nil, errors.New("tzdata: truncated data for " + name)
		}
		entries[name] = z[data : data+size]
		names = append(names, name)
	}
	sort.Strings(names)
	return entries, names, nil
}

func index() (map[string][]byte, error) {
	indexOnce.Do(buildIndex)
	return zipEntry, indexErr
}

// Names returns every zone identifier present in the embedded database, sorted.
func Names() ([]string, error) {
	if _, err := index(); err != nil {
		return nil, err
	}
	out := make([]string, len(names))
	copy(out, names)
	return out, nil
}

// Load parses the zone with the given identifier from the embedded database.
func Load(name string) (*Zone, error) {
	m, err := index()
	if err != nil {
		return nil, err
	}
	raw, ok := m[name]
	if !ok {
		return nil, ErrNotFound
	}
	return parseTZif(raw)
}

func le16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

// tzHeader mirrors the fixed TZif header counts.
type tzHeader struct {
	isutcnt, isstdcnt, leapcnt, timecnt, typecnt, charcnt int
}

// parseTZif parses a TZif v1/v2/v3 blob. It prefers the 64-bit v2+ block (which
// every zone in this archive carries) so transitions beyond 2038 are exact.
func parseTZif(raw []byte) (*Zone, error) {
	// Require the 20-byte magic+version+reserved prefix; readHeader below enforces
	// the 24-byte count block, so a truncated header is reported there.
	if len(raw) < 20 || !bytes.Equal(raw[:4], []byte("TZif")) {
		return nil, errors.New("tzdata: not a TZif file")
	}
	ver := raw[4]
	// Parse the first (32-bit) block header to know how far to skip to reach v2.
	h1, err := readHeader(raw[20:])
	if err != nil {
		return nil, err
	}
	if ver == 0 {
		// v1-only: parse the 32-bit block directly.
		z, _, err := parseBlock(raw[20:], h1, 4)
		return z, err
	}
	// Skip the entire v1 data block to reach the v2+ header + block.
	skip := 44 +
		h1.timecnt*4 + h1.timecnt*1 +
		h1.typecnt*6 + h1.charcnt +
		h1.leapcnt*8 + h1.isstdcnt + h1.isutcnt
	if skip+20 > len(raw) || !bytes.Equal(raw[skip:skip+4], []byte("TZif")) {
		return nil, errors.New("tzdata: missing v2 block")
	}
	h2, err := readHeader(raw[skip+20:])
	if err != nil {
		return nil, err
	}
	z, _, err := parseBlock(raw[skip+20:], h2, 8)
	if err != nil {
		return nil, err
	}
	z.Raw = raw
	// Derive the base (standard) offset for every DST transition from the
	// surrounding non-DST offsets, mirroring the tzinfo gem's ZoneinfoDataSource.
	deriveOffsets(z)
	applyPosix(z, raw)
	return z, nil
}

// readHeader reads the six counts that follow the 20-byte magic+version+padding.
func readHeader(b []byte) (tzHeader, error) {
	if len(b) < 24 {
		return tzHeader{}, errors.New("tzdata: short header")
	}
	return tzHeader{
		isutcnt:  int(be32(b[0:])),
		isstdcnt: int(be32(b[4:])),
		leapcnt:  int(be32(b[8:])),
		timecnt:  int(be32(b[12:])),
		typecnt:  int(be32(b[16:])),
		charcnt:  int(be32(b[20:])),
	}, nil
}

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func be64(b []byte) uint64 { return binary.BigEndian.Uint64(b) }

// parseBlock parses one TZif data block. hdrAt points at the counts (i.e. after
// the 20-byte magic+version+reserved). timeSize is 4 (v1) or 8 (v2+).
func parseBlock(hdrAt []byte, h tzHeader, timeSize int) (*Zone, int, error) {
	// Data begins 24 bytes after the counts start.
	p := 24
	base := hdrAt
	need := p + h.timecnt*timeSize + h.timecnt +
		h.typecnt*6 + h.charcnt +
		h.leapcnt*(timeSize+4) + h.isstdcnt + h.isutcnt
	if need > len(base) {
		return nil, 0, errors.New("tzdata: truncated data block")
	}
	trans := make([]int64, h.timecnt)
	for i := 0; i < h.timecnt; i++ {
		if timeSize == 8 {
			trans[i] = int64(be64(base[p:]))
			p += 8
		} else {
			trans[i] = int64(int32(be32(base[p:])))
			p += 4
		}
	}
	idxs := make([]int, h.timecnt)
	for i := 0; i < h.timecnt; i++ {
		idxs[i] = int(base[p])
		p++
	}
	// ttinfo records: 4-byte offset, 1-byte isdst, 1-byte abbrev index.
	type tt struct {
		off   int
		dst   bool
		abbri int
	}
	tts := make([]tt, h.typecnt)
	for i := 0; i < h.typecnt; i++ {
		tts[i] = tt{
			off:   int(int32(be32(base[p:]))),
			dst:   base[p+4] != 0,
			abbri: int(base[p+5]),
		}
		p += 6
	}
	abbrevs := base[p : p+h.charcnt]
	p += h.charcnt

	types := make([]LocalType, h.typecnt)
	for i, t := range tts {
		// Non-DST types carry Base == Offset; DST types get Base derived later by
		// deriveOffsets (leave it zero for now, resolved per transition).
		types[i] = LocalType{Offset: t.off, Abbrev: abbrevAt(abbrevs, t.abbri), IsDST: t.dst}
		if !t.dst {
			types[i].Base = t.off
		}
	}

	transitions := make([]Transition, h.timecnt)
	for i := range trans {
		transitions[i] = Transition{When: trans[i], Type: idxs[i]}
	}

	// The local type in force before the first transition: per RFC 8536, use the
	// first non-DST type; fall back to type 0.
	first := 0
	for i, t := range tts {
		if !t.dst {
			first = i
			break
		}
	}

	return &Zone{Transitions: transitions, Types: types, First: first}, p, nil
}

// abbrevAt returns the NUL-terminated abbreviation starting at i in the abbrev
// table.
func abbrevAt(tab []byte, i int) string {
	if i < 0 || i >= len(tab) {
		return ""
	}
	end := i
	for end < len(tab) && tab[end] != 0 {
		end++
	}
	return string(tab[i:end])
}
