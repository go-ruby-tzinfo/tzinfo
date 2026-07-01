// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzdata

import "time"

// expandFutureYear is the last calendar year (inclusive) for which POSIX-rule
// transitions are generated. TZInfo's tzinfo-data likewise pre-generates future
// transitions to a far horizon; 2500 comfortably covers any realistic query while
// keeping the in-memory table small.
const expandFutureYear = 2500

// applyPosix extends z with the future transitions implied by the POSIX TZ footer,
// starting after the last explicit transition. It is a no-op when there is no DST
// rule (fixed-offset zones need none). The two rule offsets are appended as local
// types (reusing an existing identical type when possible) and one std/dst
// transition pair is generated per year through expandFutureYear.
func applyPosix(z *Zone, raw []byte) {
	footer := posixFooter(raw)
	rule, ok := parsePosixTZ(footer)
	if !ok {
		return
	}

	// Mirror TZInfo's POSIX rule offsets: the standard offset has Base == its own
	// offset; the DST offset's Base is the standard offset (so its std_offset
	// component is dstOff-stdOff), exactly as PosixTimeZoneParser builds them.
	stdType := z.ensureType(LocalType{Offset: rule.stdOff, Abbrev: rule.stdAbbr, IsDST: false, Base: rule.stdOff, baseSet: true})
	dstType := z.ensureType(LocalType{Offset: rule.dstOff, Abbrev: rule.dstAbbr, IsDST: true, Base: rule.stdOff, baseSet: true})

	// The cutoff is the last explicit (in-file) transition. Rule-generated
	// transitions at or before it are already covered by the file and are
	// dropped; everything strictly after it is appended in chronological order.
	// This mirrors TZInfo generating rule transitions from 1970 and keeping only
	// those beyond the defined ones.
	var cutoff int64
	haveCutoff := false
	if n := len(z.Transitions); n > 0 {
		cutoff = z.Transitions[n-1].When
		haveCutoff = true
	}
	startYear := 1970
	if haveCutoff {
		startYear = time.Unix(cutoff, 0).UTC().Year()
	}

	for y := startYear; y <= expandFutureYear; y++ {
		startAt := rule.start.instant(y, rule.stdOff)
		endAt := rule.end.instant(y, rule.dstOff)
		// Emit both yearly transitions in chronological order (spring-forward
		// before fall-back in the northern hemisphere, the reverse in the
		// southern; negative-DST zones may have them adjacent at a year boundary).
		type ev struct {
			at  int64
			typ int
		}
		evs := []ev{{startAt, dstType}, {endAt, stdType}}
		if evs[0].at > evs[1].at {
			evs[0], evs[1] = evs[1], evs[0]
		}
		for _, e := range evs {
			if haveCutoff && e.at <= cutoff {
				continue
			}
			z.Transitions = append(z.Transitions, Transition{When: e.at, Type: e.typ})
		}
	}
}

// ensureType returns the index of a local type equal to lt, appending it if not
// already present.
func (z *Zone) ensureType(lt LocalType) int {
	for i, t := range z.Types {
		if t == lt {
			return i
		}
	}
	z.Types = append(z.Types, lt)
	return len(z.Types) - 1
}

// instant computes the UTC Unix second of this rule in year y. offset is the
// local offset (east of UTC, seconds) in force just before the transition, used
// to convert the rule's local wall time to UTC. It mirrors TZInfo's
// TransitionRule#at for each of the three date forms.
func (d posixDate) instant(y, offset int) int64 {
	var localMidnight int64
	switch d.kind {
	case dateJulian:
		// One-based Julian day, never counting 29 February (day 60 == 1 March).
		// Reference 29 February (which rolls to 1 March on non-leap years), then
		// add (n*86400 - 60*86400), stepping over 29 February on leap years.
		feb29 := time.Date(y, time.February, 29, 0, 0, 0, 0, time.UTC)
		diff := int64(d.dayOfYear)*86400 - 60*86400
		if diff >= 0 && isLeap(y) {
			diff += 86400
		}
		localMidnight = feb29.Unix() + diff
	case dateAbsolute:
		// Zero-based day of the year, counting 29 February.
		jan1 := time.Date(y, time.January, 1, 0, 0, 0, 0, time.UTC)
		localMidnight = jan1.Unix() + int64(d.dayOfYear)*86400
	default: // dateMonthWeekDay
		first := time.Date(y, time.Month(d.month), 1, 0, 0, 0, 0, time.UTC)
		firstWeekday := int(first.Weekday()) // 0=Sun
		// Day-of-month of the first occurrence of weekday d.day.
		day := 1 + ((d.day - firstWeekday + 7) % 7)
		// Advance by (week-1) weeks; week 5 means "last", clamp to the month.
		day += (d.week - 1) * 7
		dim := daysInMonth(y, d.month)
		for day > dim {
			day -= 7
		}
		localMidnight = time.Date(y, time.Month(d.month), day, 0, 0, 0, 0, time.UTC).Unix()
	}
	local := localMidnight + int64(d.time)
	return local - int64(offset)
}

// daysInMonth returns the number of days in month m (1..12) of year y.
func daysInMonth(y, m int) int {
	return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// isLeap reports whether y is a Gregorian leap year.
func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}
