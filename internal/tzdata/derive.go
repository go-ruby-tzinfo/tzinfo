// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzdata

// deriveOffsets fills in the Base (standard-time) offset of every daylight-saving
// local type as observed at each transition, mirroring TZInfo::ZoneinfoReader#
// derive_offsets. Compiled zoneinfo files record only the observed total offset
// and a DST flag for each period, not the split into a base offset plus a DST
// amount; TZInfo derives the base for a DST period from the nearest surrounding
// non-DST period, preferring whichever side differs from the DST offset by exactly
// one hour, then the nearer non-zero side, and finally observed-1h as a fallback.
//
// Because one DST local type can be observed with different derived base offsets
// at different points in history, deriveOffsets may split a type into several
// concrete LocalTypes (one per distinct base), rewriting the affected transitions
// to point at the split type. This exactly reproduces how the gem adds new offset
// entries when an earlier transition already fixed a different base.
func deriveOffsets(z *Zone) {
	trs := z.Transitions
	if len(trs) == 0 {
		// No transitions: the single first type is used throughout. If it is DST
		// (degenerate), fall back to observed-1h so Base is defined.
		ft := &z.Types[z.First]
		if ft.IsDST && ft.Base == 0 {
			ft.Base = ft.Offset - 3600
		}
		return
	}

	// base_utc_offset_from_next: for each transition into a DST offset, the
	// observed offset of the next non-DST period (scanning from the end).
	baseFromNext := make([]int, len(trs))
	haveFromNext := make([]bool, len(trs))
	var nextBase int
	var nextBaseSet bool
	for i := len(trs) - 1; i >= 0; i-- {
		t := &z.Types[trs[i].Type]
		if t.IsDST {
			if nextBaseSet {
				baseFromNext[i] = nextBase
				haveFromNext[i] = true
			}
		} else {
			nextBase = t.Offset
			nextBaseSet = true
		}
	}

	// base_utc_offset_from_previous starts at the first non-DST offset's observed
	// offset (the pre-first period), if any.
	var basePrev int
	var basePrevSet bool
	if !z.Types[z.First].IsDST {
		basePrev = z.Types[z.First].Offset
		basePrevSet = true
	}

	// defined: for a (typeIdx, base) pair already materialised, the concrete type
	// index to reuse, so identical splits share a LocalType (as the gem does).
	type key struct {
		typ  int
		base int
	}
	defined := map[key]int{}

	for i := range trs {
		typeIdx := trs[i].Type
		t := z.Types[typeIdx]
		if !t.IsDST {
			basePrev = t.Offset
			basePrevSet = true
			continue
		}

		observed := t.Offset
		fromNext, fromNextSet := baseFromNext[i], haveFromNext[i]

		effPrev := observed
		if basePrevSet {
			effPrev = basePrev
		}
		effNext := observed
		if fromNextSet {
			effNext = fromNext
		}
		diffPrev := abs(observed - effPrev)
		diffNext := abs(observed - effNext)

		var base int
		switch {
		case diffPrev == 3600:
			base = basePrev
		case diffNext == 3600:
			base = fromNext
		case diffPrev > 0 && diffNext > 0:
			if diffPrev < diffNext {
				base = basePrev
			} else {
				base = fromNext
			}
		case diffPrev > 0:
			base = basePrev
		case diffNext > 0:
			base = fromNext
		default:
			base = observed - 3600
		}

		// Materialise the base on the type, splitting when a different base was
		// already fixed for this type.
		cur := &z.Types[typeIdx]
		if !cur.baseSet {
			cur.Base = base
			cur.baseSet = true
			defined[key{typeIdx, base}] = typeIdx
		} else if cur.Base != base {
			k := key{typeIdx, base}
			ni, ok := defined[k]
			if !ok {
				nt := *cur
				nt.Base = base
				z.Types = append(z.Types, nt)
				ni = len(z.Types) - 1
				defined[k] = ni
			}
			trs[i].Type = ni
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
