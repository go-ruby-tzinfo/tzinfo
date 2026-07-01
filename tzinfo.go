// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

// Package tzinfo is a pure-Go (CGO-free), MRI-faithful reimplementation of the
// Ruby TZInfo library (the `tzinfo` gem). It embeds a complete offline copy of
// the compiled IANA time zone database (the TZif files the gem's ZoneinfoDataSource
// reads from the system zoneinfo) and exposes the TZInfo API on top of it: resolving a
// zone by identifier, converting between UTC and local time, computing the
// TimezonePeriod (offset, abbreviation, DST flag, transition boundaries) in force
// at an instant, and handling the spring-forward gap (PeriodNotFound) and
// fall-back overlap (AmbiguousTime) exactly as the gem does.
//
// # Relationship to the gem
//
//	Ruby (tzinfo gem)                      Go (this package)
//	-----------------                      -----------------
//	TZInfo::Timezone.get("America/…")      tzinfo.Get("America/…")
//	TZInfo::Timezone.all_identifiers       tzinfo.AllIdentifiers()
//	tz.utc_to_local(t)                     tz.UTCToLocal(t)
//	tz.local_to_utc(t)                     tz.LocalToUTC(t)
//	tz.period_for_utc(t)                   tz.PeriodForUTC(t)
//	tz.period_for_local(t)                 tz.PeriodForLocal(t)
//	tz.transitions_up_to(to, from)         tz.TransitionsUpTo(to, from)
//	tz.offsets_up_to(to, from)             tz.OffsetsUpTo(to, from)
//	tz.current_period / .now               tz.CurrentPeriod() / tz.Now()
//	tz.canonical_identifier                tz.CanonicalIdentifier()
//	TimezonePeriod / TimezoneOffset        TimezonePeriod / TimezoneOffset
//	TZInfo::Country.get("US")              tzinfo.GetCountry("US")
//
// Every timestamp crossing the API is a Go time.Time; UTC-to-local conversions
// return a time.Time carrying a *time.Location fixed to the period's offset and
// abbreviation, mirroring how TZInfo tags a Time with the resolved offset.
package tzinfo

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/go-ruby-tzinfo/tzinfo/internal/tzdata"
)

// InvalidTimezoneIdentifier is returned by Get when the identifier names no zone
// in the database (mirrors TZInfo::InvalidTimezoneIdentifier).
type InvalidTimezoneIdentifier struct {
	Identifier string
}

func (e *InvalidTimezoneIdentifier) Error() string {
	return fmt.Sprintf("Invalid identifier: %s", e.Identifier)
}

// InvalidCountryCode is returned by GetCountry for an unknown ISO-3166 code
// (mirrors TZInfo::InvalidCountryCode).
type InvalidCountryCode struct {
	Code string
}

func (e *InvalidCountryCode) Error() string {
	return fmt.Sprintf("Invalid country code: %s", e.Code)
}

// PeriodNotFound is returned by PeriodForLocal when the local time falls in a
// spring-forward gap that does not occur in the zone (mirrors
// TZInfo::PeriodNotFound).
type PeriodNotFound struct {
	Local time.Time
}

func (e *PeriodNotFound) Error() string {
	return fmt.Sprintf("%s is an invalid local time.", e.Local.Format("2006-01-02 15:04:05"))
}

// AmbiguousTime is returned by PeriodForLocal when the local time occurs twice
// because of a fall-back overlap and no disambiguation was supplied (mirrors
// TZInfo::AmbiguousTime).
type AmbiguousTime struct {
	Local time.Time
}

func (e *AmbiguousTime) Error() string {
	return fmt.Sprintf("%s is an ambiguous local time.", e.Local.Format("2006-01-02 15:04:05"))
}

// TimezoneOffset describes the offset from UTC in force during a period: the
// base (standard) offset, the additional DST amount, the combined total, and the
// abbreviation. It mirrors TZInfo::TimezoneOffset.
type TimezoneOffset struct {
	// BaseUTCOffset is the standard-time offset east of UTC in seconds
	// (base_utc_offset / utc_offset in the gem).
	BaseUTCOffset int
	// STDOffset is the additional offset applied during daylight-saving time in
	// seconds (std_offset in the gem); zero outside DST.
	STDOffset int
	// Abbreviation is the local abbreviation, e.g. "EST", "EDT", "LMT", "IST".
	Abbreviation string
}

// UTCTotalOffset is the combined offset east of UTC in seconds
// (utc_total_offset in the gem).
func (o TimezoneOffset) UTCTotalOffset() int { return o.BaseUTCOffset + o.STDOffset }

// DST reports whether daylight-saving time is in effect for this offset
// (dst? in the gem).
func (o TimezoneOffset) DST() bool { return o.STDOffset != 0 }

// TimezonePeriod is the interval during which a single TimezoneOffset is in
// force, bounded (where known) by the transitions that begin and end it. It
// mirrors TZInfo::TimezonePeriod.
type TimezonePeriod struct {
	// Offset is the offset that applies throughout the period.
	Offset TimezoneOffset
	// hasStart/startAt and hasEnd/endAt bound the period in UTC. A period with no
	// start (the first, unbounded-past period) has hasStart false; a period with
	// no end (the current, unbounded-future period) has hasEnd false.
	hasStart bool
	startAt  time.Time
	hasEnd   bool
	endAt    time.Time
}

// Abbreviation returns the period's abbreviation (abbreviation / abbr / zone_identifier
// in the gem).
func (p TimezonePeriod) Abbreviation() string { return p.Offset.Abbreviation }

// UTCTotalOffset returns the combined UTC offset in seconds.
func (p TimezonePeriod) UTCTotalOffset() int { return p.Offset.UTCTotalOffset() }

// BaseUTCOffset returns the standard-time offset in seconds.
func (p TimezonePeriod) BaseUTCOffset() int { return p.Offset.BaseUTCOffset }

// STDOffset returns the DST component of the offset in seconds.
func (p TimezonePeriod) STDOffset() int { return p.Offset.STDOffset }

// DST reports whether the period is daylight-saving.
func (p TimezonePeriod) DST() bool { return p.Offset.DST() }

// StartTransition returns the UTC instant the period begins and whether it is
// bounded in the past. When ok is false the period extends to the beginning of
// time.
func (p TimezonePeriod) StartTransition() (t time.Time, ok bool) { return p.startAt, p.hasStart }

// EndTransition returns the UTC instant the period ends and whether it is bounded
// in the future. When ok is false the period is the current, open-ended period.
func (p TimezonePeriod) EndTransition() (t time.Time, ok bool) { return p.endAt, p.hasEnd }

// TimezoneTransition is a single transition instant and the offset that takes
// effect at it, as returned by TransitionsUpTo (mirrors
// TZInfo::TimezoneTransition).
type TimezoneTransition struct {
	// At is the UTC instant of the transition.
	At time.Time
	// Offset is the offset that comes into force at and after At.
	Offset TimezoneOffset
}

// Timezone is a resolved IANA time zone. It mirrors TZInfo::Timezone (specifically
// a DataTimezone); linked identifiers resolve to their canonical zone's data while
// remembering the identifier they were fetched with.
type Timezone struct {
	// identifier is the identifier this Timezone was fetched with (may be a link).
	identifier string
	// canonical is the identifier of the underlying data zone.
	canonical string
	zone      *tzdata.Zone
}

var (
	zoneCacheMu sync.Mutex
	zoneCache   = map[string]*tzdata.Zone{}
)

func loadZone(canonical string) (*tzdata.Zone, error) {
	zoneCacheMu.Lock()
	defer zoneCacheMu.Unlock()
	if z, ok := zoneCache[canonical]; ok {
		return z, nil
	}
	z, err := tzdata.Load(canonical)
	if err != nil {
		return nil, err
	}
	zoneCache[canonical] = z
	return z, nil
}

// Get resolves a zone by IANA identifier (TZInfo::Timezone.get). The embedded
// database (matching the gem's ZoneinfoDataSource) materialises every IANA
// backward-link name — "US/Eastern", "GB", "Zulu", … — as a full data zone, so
// every valid identifier is its own canonical zone; there are no link
// indirections at this layer. An unknown identifier returns
// *InvalidTimezoneIdentifier.
func Get(identifier string) (*Timezone, error) {
	z, err := loadZone(identifier)
	if err != nil {
		return nil, &InvalidTimezoneIdentifier{Identifier: identifier}
	}
	return &Timezone{identifier: identifier, canonical: identifier, zone: z}, nil
}

// Identifier returns the identifier this Timezone was fetched with (identifier in
// the gem).
func (tz *Timezone) Identifier() string { return tz.identifier }

// String returns the fetched identifier (to_s in the gem returns the identifier).
func (tz *Timezone) String() string { return tz.identifier }

// CanonicalIdentifier returns the identifier of the underlying data zone
// (canonical_identifier in the gem). For an unlinked zone it equals Identifier.
func (tz *Timezone) CanonicalIdentifier() string { return tz.canonical }

// offsetFor builds a TimezoneOffset from a data-layer local type, using the base
// (standard) offset the data layer already derived (mirroring how TZInfo splits
// base_utc_offset / std_offset from the compiled data).
func (tz *Timezone) offsetFor(typeIdx int) TimezoneOffset {
	lt := tz.zone.Types[typeIdx]
	return TimezoneOffset{
		BaseUTCOffset: lt.Base,
		STDOffset:     lt.Offset - lt.Base,
		Abbreviation:  lt.Abbrev,
	}
}

// periodAt returns the TimezonePeriod containing UTC instant t.
func (tz *Timezone) periodAt(t time.Time) TimezonePeriod {
	trs := tz.zone.Transitions
	sec := t.Unix()

	if len(trs) == 0 {
		off := tz.offsetFor(tz.zone.First)
		return TimezonePeriod{Offset: off}
	}

	// Find the last transition with When <= sec.
	i := sort.Search(len(trs), func(k int) bool { return trs[k].When > sec }) - 1

	if i < 0 {
		// Before the first transition: the pre-first local type, bounded only by
		// the first transition.
		off := tz.offsetFor(tz.zone.First)
		return TimezonePeriod{
			Offset: off,
			hasEnd: true,
			endAt:  time.Unix(trs[0].When, 0).UTC(),
		}
	}

	typeIdx := trs[i].Type
	off := tz.offsetFor(typeIdx)
	p := TimezonePeriod{
		Offset:   off,
		hasStart: true,
		startAt:  time.Unix(trs[i].When, 0).UTC(),
	}
	if i+1 < len(trs) {
		p.hasEnd = true
		p.endAt = time.Unix(trs[i+1].When, 0).UTC()
	}
	return p
}

// PeriodForUTC returns the TimezonePeriod in force at the given UTC instant
// (period_for_utc). t is interpreted as an absolute instant regardless of its
// Location.
func (tz *Timezone) PeriodForUTC(t time.Time) TimezonePeriod {
	return tz.periodAt(t.UTC())
}

// UTCToLocal converts a UTC instant to local time, returning a time.Time carrying
// a *time.Location fixed to the period's offset and abbreviation (utc_to_local).
func (tz *Timezone) UTCToLocal(t time.Time) time.Time {
	p := tz.periodAt(t.UTC())
	loc := time.FixedZone(p.Abbreviation(), p.UTCTotalOffset())
	return t.UTC().In(loc)
}

// LocalToUTC converts a local wall-clock time to the corresponding UTC instant
// (local_to_utc). The wall-clock fields of t are read as local time in this zone;
// t's own Location is ignored for the wall value. When t falls in a spring-forward
// gap it returns *PeriodNotFound; when it is ambiguous (fall-back overlap) it
// returns *AmbiguousTime unless dst disambiguates (see PeriodForLocal).
func (tz *Timezone) LocalToUTC(t time.Time, dst ...bool) (time.Time, error) {
	p, err := tz.PeriodForLocal(t, dst...)
	if err != nil {
		return time.Time{}, err
	}
	// Reinterpret the wall-clock fields as UTC, then subtract the offset.
	wall := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	return wall.Add(-time.Duration(p.UTCTotalOffset()) * time.Second), nil
}

// PeriodForLocal returns the TimezonePeriod for a local wall-clock time
// (period_for_local). The wall-clock fields of t are read as local time in this
// zone. A gap yields *PeriodNotFound; an overlap yields *AmbiguousTime unless a
// single dst preference is supplied to select the daylight (true) or standard
// (false) period.
func (tz *Timezone) PeriodForLocal(t time.Time, dst ...bool) (TimezonePeriod, error) {
	wall := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
	wallSec := wall.Unix()

	// Candidate periods: a wall time W is valid in a period with offset O iff
	// converting W-O back lands inside that period. Collect all matching periods.
	var matches []TimezonePeriod
	for _, p := range tz.candidatePeriods(wallSec) {
		utcSec := wallSec - int64(p.UTCTotalOffset())
		if tz.periodContains(p, utcSec) {
			matches = append(matches, p)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return TimezonePeriod{}, &PeriodNotFound{Local: wall}
	default:
		if len(dst) == 1 {
			for _, m := range matches {
				if m.DST() == dst[0] {
					return m, nil
				}
			}
		}
		return TimezonePeriod{}, &AmbiguousTime{Local: wall}
	}
}

// candidatePeriods returns the small set of periods whose offset could plausibly
// contain a wall time near wallSec: the periods around the transition bracketing
// wallSec (each period's own offset spans at most a couple hours from a boundary).
func (tz *Timezone) candidatePeriods(wallSec int64) []TimezonePeriod {
	trs := tz.zone.Transitions
	if len(trs) == 0 {
		return []TimezonePeriod{tz.periodAt(time.Unix(wallSec, 0).UTC())}
	}
	// A transition at UTC instant W corresponds to wall times near W+offset on
	// either side. Search over transitions whose UTC instant is within a day of
	// wallSec, and include the surrounding periods.
	lo := sort.Search(len(trs), func(k int) bool { return trs[k].When > wallSec-86400 })
	hi := sort.Search(len(trs), func(k int) bool { return trs[k].When > wallSec+86400 })
	seen := map[int64]bool{}
	var out []TimezonePeriod
	add := func(p TimezonePeriod) {
		var key int64
		if p.hasStart {
			key = p.startAt.Unix()
		} else {
			key = -1 << 62
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, p)
		}
	}
	for k := lo - 1; k <= hi; k++ {
		if k < 0 {
			add(tz.periodAt(time.Unix(trs[0].When-1, 0).UTC()))
			continue
		}
		if k >= len(trs) {
			continue
		}
		add(tz.periodAt(time.Unix(trs[k].When, 0).UTC()))
	}
	// out is always non-empty here: the loop starts at lo-1, and lo = Search(...)
	// is at most len(trs), so k = lo-1 is either negative (adds trs[0]'s pre-period)
	// or a valid index (adds that period).
	return out
}

// periodContains reports whether the UTC instant utcSec lies within period p.
func (tz *Timezone) periodContains(p TimezonePeriod, utcSec int64) bool {
	if p.hasStart && utcSec < p.startAt.Unix() {
		return false
	}
	if p.hasEnd && utcSec >= p.endAt.Unix() {
		return false
	}
	return true
}

// Now returns the current local time in this zone (now), a time.Time in a
// FixedZone for the current period.
func (tz *Timezone) Now() time.Time { return tz.UTCToLocal(time.Now().UTC()) }

// CurrentPeriod returns the period in force at the current instant
// (current_period).
func (tz *Timezone) CurrentPeriod() TimezonePeriod { return tz.PeriodForUTC(time.Now().UTC()) }

// UTCOffset returns the standard (base) UTC offset in seconds currently in force
// (utc_offset returns the current base offset in the gem).
func (tz *Timezone) UTCOffset() int { return tz.CurrentPeriod().BaseUTCOffset() }

// Abbreviation returns the abbreviation in force at UTC instant t (abbreviation).
func (tz *Timezone) Abbreviation(t time.Time) string {
	return tz.PeriodForUTC(t).Abbreviation()
}

// DST reports whether daylight-saving time is in force at UTC instant t (dst?).
func (tz *Timezone) DST(t time.Time) bool { return tz.PeriodForUTC(t).DST() }

// TransitionsUpTo returns the transitions occurring in the half-open UTC window
// [from, to), in chronological order (transitions_up_to). When from is the zero
// time, the window is open at the start.
func (tz *Timezone) TransitionsUpTo(to time.Time, from ...time.Time) []TimezoneTransition {
	toSec := to.UTC().Unix()
	var fromSec int64
	hasFrom := len(from) == 1 && !from[0].IsZero()
	if hasFrom {
		fromSec = from[0].UTC().Unix()
	}
	var out []TimezoneTransition
	trs := tz.zone.Transitions
	for _, tr := range trs {
		if tr.When >= toSec {
			break
		}
		if hasFrom && tr.When < fromSec {
			continue
		}
		out = append(out, TimezoneTransition{
			At:     time.Unix(tr.When, 0).UTC(),
			Offset: tz.offsetFor(tr.Type),
		})
	}
	return out
}

// OffsetsUpTo returns the distinct offsets observed in the half-open UTC window
// [from, to) (offsets_up_to). The offset in force at the start of the window is
// included.
func (tz *Timezone) OffsetsUpTo(to time.Time, from ...time.Time) []TimezoneOffset {
	trans := tz.TransitionsUpTo(to, from...)
	seen := map[TimezoneOffset]bool{}
	var out []TimezoneOffset
	// Include the offset in force at the window start.
	var startPeriod TimezonePeriod
	if len(from) == 1 && !from[0].IsZero() {
		startPeriod = tz.PeriodForUTC(from[0])
	} else {
		startPeriod = tz.periodAt(time.Unix(-1<<62, 0).UTC())
	}
	if !seen[startPeriod.Offset] {
		seen[startPeriod.Offset] = true
		out = append(out, startPeriod.Offset)
	}
	for _, tr := range trans {
		if !seen[tr.Offset] {
			seen[tr.Offset] = true
			out = append(out, tr.Offset)
		}
	}
	return out
}
