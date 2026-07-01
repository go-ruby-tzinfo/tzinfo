// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzinfo

import (
	"errors"
	"testing"
	"time"
)

// TestGetInvalid checks the InvalidTimezoneIdentifier path and its message.
func TestGetInvalid(t *testing.T) {
	_, err := Get("Not/A_Zone")
	var inv *InvalidTimezoneIdentifier
	if !errors.As(err, &inv) {
		t.Fatalf("err = %v, want *InvalidTimezoneIdentifier", err)
	}
	if inv.Identifier != "Not/A_Zone" {
		t.Errorf("Identifier = %q", inv.Identifier)
	}
	if got, want := inv.Error(), "Invalid identifier: Not/A_Zone"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestGetCached exercises the zone cache hit path (a second Get of the same id).
func TestGetCached(t *testing.T) {
	a, err := Get("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Get("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	if a.CanonicalIdentifier() != b.CanonicalIdentifier() {
		t.Errorf("cache returned different canonical")
	}
}

// TestIdentifiers covers AllIdentifiers, AllDataZoneIdentifiers and All.
func TestIdentifiers(t *testing.T) {
	ids, err := AllIdentifiers()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 598 {
		t.Errorf("AllIdentifiers len = %d, want 598", len(ids))
	}
	data, err := AllDataZoneIdentifiers()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != len(ids) {
		t.Errorf("AllDataZoneIdentifiers len = %d, want %d", len(data), len(ids))
	}
	// Mutating the returned slice must not corrupt the cached list.
	ids[0] = "MUTATED"
	again, _ := AllIdentifiers()
	if again[0] == "MUTATED" {
		t.Errorf("AllIdentifiers returned a shared slice")
	}
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(again) {
		t.Errorf("All len = %d, want %d", len(all), len(again))
	}
	if all[0].Identifier() != again[0] {
		t.Errorf("All[0] = %q, want %q", all[0].Identifier(), again[0])
	}
}

// TestPeriodAccessors covers StartTransition, EndTransition and the offset/period
// accessor helpers over a known zone.
func TestPeriodAccessors(t *testing.T) {
	tz, err := Get("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// A bounded period (summer 2023): both transitions present.
	p := tz.PeriodForUTC(time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC))
	if _, ok := p.StartTransition(); !ok {
		t.Errorf("summer period should have a start transition")
	}
	if _, ok := p.EndTransition(); !ok {
		t.Errorf("summer period should have an end transition")
	}
	if p.BaseUTCOffset() != -18000 || p.STDOffset() != 3600 || !p.DST() {
		t.Errorf("unexpected summer offset split")
	}
	if p.Offset.UTCTotalOffset() != -14400 {
		t.Errorf("offset total = %d", p.Offset.UTCTotalOffset())
	}

	// The pre-first (earliest) period has no start transition.
	early := tz.PeriodForUTC(time.Date(1700, 1, 1, 0, 0, 0, 0, time.UTC))
	if _, ok := early.StartTransition(); ok {
		t.Errorf("earliest period should have no start transition")
	}
	if _, ok := early.EndTransition(); !ok {
		t.Errorf("earliest period should have an end transition")
	}

	// Beyond the last generated (POSIX-expanded) transition the period is
	// open-ended in the future.
	future := tz.PeriodForUTC(time.Date(2600, 1, 15, 0, 0, 0, 0, time.UTC))
	if _, ok := future.EndTransition(); ok {
		t.Errorf("post-horizon period should have no end transition")
	}
}

// TestNowCurrentAndScalars covers Now, CurrentPeriod, UTCOffset, Abbreviation
// and DST at a time.
func TestNowCurrentAndScalars(t *testing.T) {
	tz, err := Get("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := tz.Now()
	cur := tz.CurrentPeriod()
	if now.IsZero() {
		t.Errorf("Now returned zero time")
	}
	// UTCOffset returns the current base offset (EST base is -18000 for NY).
	if tz.UTCOffset() != cur.BaseUTCOffset() {
		t.Errorf("UTCOffset = %d, want %d", tz.UTCOffset(), cur.BaseUTCOffset())
	}
	// Abbreviation and DST at a fixed winter instant.
	winter := time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC)
	if got := tz.Abbreviation(winter); got != "EST" {
		t.Errorf("winter abbr = %q, want EST", got)
	}
	if tz.DST(winter) {
		t.Errorf("winter should not be DST")
	}
	summer := time.Date(2023, 7, 15, 12, 0, 0, 0, time.UTC)
	if !tz.DST(summer) {
		t.Errorf("summer should be DST")
	}
}

// TestTransitionsUpTo covers the windowed transition listing, including the
// from-bounded and unbounded variants.
func TestTransitionsUpTo(t *testing.T) {
	tz, err := Get("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	to := time.Date(1971, 1, 1, 0, 0, 0, 0, time.UTC)
	all := tz.TransitionsUpTo(to)
	if len(all) == 0 {
		t.Fatalf("expected transitions before 1971")
	}
	for i := 1; i < len(all); i++ {
		if !all[i].At.After(all[i-1].At) {
			t.Errorf("transitions not chronological at %d", i)
		}
	}
	// A from bound trims the early transitions.
	from := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	bounded := tz.TransitionsUpTo(to, from)
	if len(bounded) >= len(all) {
		t.Errorf("from-bounded window should be smaller: %d vs %d", len(bounded), len(all))
	}
	for _, tr := range bounded {
		if tr.At.Before(from) {
			t.Errorf("transition %v before from bound", tr.At)
		}
	}
	// A zero from behaves like the unbounded form.
	if len(tz.TransitionsUpTo(to, time.Time{})) != len(all) {
		t.Errorf("zero from should equal unbounded")
	}
}

// TestOffsetsUpTo covers the distinct-offset listing, both unbounded and
// from-bounded.
func TestOffsetsUpTo(t *testing.T) {
	tz, err := Get("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	to := time.Date(1971, 1, 1, 0, 0, 0, 0, time.UTC)
	offs := tz.OffsetsUpTo(to)
	if len(offs) == 0 {
		t.Fatalf("expected some offsets")
	}
	seen := map[TimezoneOffset]int{}
	for _, o := range offs {
		seen[o]++
		if seen[o] > 1 {
			t.Errorf("duplicate offset %+v", o)
		}
	}
	// From-bounded: includes the offset in force at the window start.
	from := time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC)
	fb := tz.OffsetsUpTo(to, from)
	if len(fb) == 0 {
		t.Errorf("from-bounded offsets empty")
	}
}

// TestFixedZone covers a fixed-offset zone with no transitions (UTC) across the
// no-transition code paths in periodAt / candidatePeriods.
func TestFixedZone(t *testing.T) {
	tz, err := Get("Etc/UTC")
	if err != nil {
		t.Fatal(err)
	}
	p := tz.PeriodForUTC(time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC))
	if p.UTCTotalOffset() != 0 || p.DST() {
		t.Errorf("UTC should be a fixed zero offset")
	}
	if _, ok := p.StartTransition(); ok {
		t.Errorf("UTC period should be unbounded in the past")
	}
	// local_to_utc through the no-transition candidate path.
	utc, err := tz.LocalToUTC(time.Date(2023, 6, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !utc.Equal(time.Date(2023, 6, 1, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("UTC local_to_utc mismatch: %v", utc)
	}
	pl, err := tz.PeriodForLocal(time.Date(2023, 6, 1, 12, 0, 0, 0, time.UTC))
	if err != nil || pl.UTCTotalOffset() != 0 {
		t.Errorf("UTC period_for_local mismatch: %v %v", pl, err)
	}
}

// TestLocalToUTCErrors covers the error propagation from LocalToUTC when the
// local time is in a gap or ambiguous.
func TestLocalToUTCErrors(t *testing.T) {
	tz, _ := Get("America/New_York")
	// Spring-forward gap.
	_, err := tz.LocalToUTC(time.Date(2023, 3, 12, 2, 30, 0, 0, time.UTC))
	var pnf *PeriodNotFound
	if !errors.As(err, &pnf) {
		t.Errorf("gap err = %v, want *PeriodNotFound", err)
	}
	if pnf.Error() == "" {
		t.Errorf("empty PeriodNotFound message")
	}
	// Fall-back overlap.
	_, err = tz.LocalToUTC(time.Date(2023, 11, 5, 1, 30, 0, 0, time.UTC))
	var amb *AmbiguousTime
	if !errors.As(err, &amb) {
		t.Errorf("overlap err = %v, want *AmbiguousTime", err)
	}
	if amb.Error() == "" {
		t.Errorf("empty AmbiguousTime message")
	}
}

// TestCountry covers the TZInfo::Country surface and the invalid-code path.
func TestCountry(t *testing.T) {
	c, err := GetCountry("us") // case-insensitive input
	if err != nil {
		t.Fatal(err)
	}
	if c.Code() != "US" {
		t.Errorf("Code = %q, want US", c.Code())
	}
	if c.Name() != "United States" {
		t.Errorf("Name = %q", c.Name())
	}
	ids := c.ZoneIdentifiers()
	if len(ids) == 0 {
		t.Fatalf("US has no zones")
	}
	// Returned slice is a copy.
	ids[0] = "X"
	if c.ZoneIdentifiers()[0] == "X" {
		t.Errorf("ZoneIdentifiers returned shared slice")
	}
	zones, err := c.Zones()
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != len(c.ZoneIdentifiers()) {
		t.Errorf("Zones len mismatch")
	}
	codes := AllCountryCodes()
	if len(codes) != 249 {
		t.Errorf("AllCountryCodes len = %d, want 249", len(codes))
	}

	// Invalid code path and message.
	_, err = GetCountry("ZZ")
	var icc *InvalidCountryCode
	if !errors.As(err, &icc) {
		t.Fatalf("err = %v, want *InvalidCountryCode", err)
	}
	if icc.Error() != "Invalid country code: ZZ" {
		t.Errorf("Error() = %q", icc.Error())
	}
}

// TestHalfHourAndLinkedNames covers a half-hour zone and a backward-link name
// (materialised as its own data zone under the zoneinfo source).
func TestHalfHourAndLinkedNames(t *testing.T) {
	kol, _ := Get("Asia/Kolkata")
	if kol.PeriodForUTC(time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC)).UTCTotalOffset() != 19800 {
		t.Errorf("Kolkata should be +05:30")
	}
	// A former "link" name resolves and is its own canonical id here.
	us, err := Get("US/Eastern")
	if err != nil {
		t.Fatal(err)
	}
	if us.CanonicalIdentifier() != "US/Eastern" {
		t.Errorf("US/Eastern canonical = %q", us.CanonicalIdentifier())
	}
	if us.Abbreviation(time.Date(2023, 1, 15, 12, 0, 0, 0, time.UTC)) != "EST" {
		t.Errorf("US/Eastern winter abbr wrong")
	}
}
