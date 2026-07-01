// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzinfo

import (
	"errors"
	"testing"
	"time"
)

// parseUTC parses an RFC3339 "…Z" instant, failing the test on a bad literal.
func parseUTC(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse utc %q: %v", s, err)
	}
	return v.UTC()
}

// parseWall parses a "2006-01-02T15:04:05" wall-clock literal into a UTC-tagged
// time.Time carrying those fields (the Location is irrelevant for local input).
func parseWall(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse("2006-01-02T15:04:05", s)
	if err != nil {
		t.Fatalf("parse wall %q: %v", s, err)
	}
	return v
}

// TestGoldenUTC replays every period_for_utc / utc_to_local golden vector
// captured from the tzinfo gem and requires an exact match on the offset split,
// abbreviation, DST flag and rendered local time.
func TestGoldenUTC(t *testing.T) {
	for _, v := range goldenUTCVectors {
		tz, err := Get(v.zone)
		if err != nil {
			t.Fatalf("Get(%q): %v", v.zone, err)
		}
		u := parseUTC(t, v.utc)
		p := tz.PeriodForUTC(u)
		if got := p.Abbreviation(); got != v.abbr {
			t.Errorf("%s @ %s abbr = %q, want %q", v.zone, v.utc, got, v.abbr)
		}
		if got := p.UTCTotalOffset(); got != v.total {
			t.Errorf("%s @ %s total = %d, want %d", v.zone, v.utc, got, v.total)
		}
		if got := p.BaseUTCOffset(); got != v.base {
			t.Errorf("%s @ %s base = %d, want %d", v.zone, v.utc, got, v.base)
		}
		if got := p.STDOffset(); got != v.std {
			t.Errorf("%s @ %s std = %d, want %d", v.zone, v.utc, got, v.std)
		}
		if got := p.DST(); got != v.dst {
			t.Errorf("%s @ %s dst = %v, want %v", v.zone, v.utc, got, v.dst)
		}
		if got := tz.UTCToLocal(u).Format("2006-01-02T15:04:05"); got != v.local {
			t.Errorf("%s @ %s local = %q, want %q", v.zone, v.utc, got, v.local)
		}
		// The offset's own accessors must agree with the period's.
		if p.Offset.UTCTotalOffset() != v.total || p.Offset.DST() != v.dst {
			t.Errorf("%s @ %s offset accessors disagree", v.zone, v.utc)
		}
	}
}

// TestGoldenLocal replays every period_for_local / local_to_utc golden vector,
// covering the normal, spring-forward gap (PeriodNotFound), fall-back overlap
// (AmbiguousTime) and DST-disambiguated cases.
func TestGoldenLocal(t *testing.T) {
	for _, v := range goldenLocalVectors {
		tz, err := Get(v.zone)
		if err != nil {
			t.Fatalf("Get(%q): %v", v.zone, err)
		}
		w := parseWall(t, v.local)

		var dstArg []bool
		switch v.disamb {
		case "dst_true":
			dstArg = []bool{true}
		case "dst_false":
			dstArg = []bool{false}
		}

		p, perr := tz.PeriodForLocal(w, dstArg...)
		utc, uerr := tz.LocalToUTC(w, dstArg...)

		switch v.kind {
		case "ok":
			if perr != nil {
				t.Errorf("%s local %s (%s): PeriodForLocal err = %v, want ok", v.zone, v.local, v.disamb, perr)
				continue
			}
			if uerr != nil {
				t.Errorf("%s local %s (%s): LocalToUTC err = %v, want ok", v.zone, v.local, v.disamb, uerr)
				continue
			}
			if got := p.UTCTotalOffset(); got != v.total {
				t.Errorf("%s local %s (%s): total = %d, want %d", v.zone, v.local, v.disamb, got, v.total)
			}
			if got := utc.UTC().Format("2006-01-02T15:04:05Z"); got != v.utc {
				t.Errorf("%s local %s (%s): utc = %q, want %q", v.zone, v.local, v.disamb, got, v.utc)
			}
		case "gap":
			var pnf *PeriodNotFound
			if !errors.As(perr, &pnf) {
				t.Errorf("%s local %s: err = %v, want *PeriodNotFound", v.zone, v.local, perr)
			}
			if !errors.As(uerr, &pnf) {
				t.Errorf("%s local %s: LocalToUTC err = %v, want *PeriodNotFound", v.zone, v.local, uerr)
			}
		case "ambig":
			var amb *AmbiguousTime
			if !errors.As(perr, &amb) {
				t.Errorf("%s local %s: err = %v, want *AmbiguousTime", v.zone, v.local, perr)
			}
			if !errors.As(uerr, &amb) {
				t.Errorf("%s local %s: LocalToUTC err = %v, want *AmbiguousTime", v.zone, v.local, uerr)
			}
		}
	}
}

// TestGoldenCanonical checks CanonicalIdentifier for the sampled zones (which,
// under the gem's ZoneinfoDataSource, always equals the fetched identifier).
func TestGoldenCanonical(t *testing.T) {
	for id, canon := range goldenCanonical {
		tz, err := Get(id)
		if err != nil {
			t.Fatalf("Get(%q): %v", id, err)
		}
		if got := tz.CanonicalIdentifier(); got != canon {
			t.Errorf("%s canonical = %q, want %q", id, got, canon)
		}
		if tz.Identifier() != id || tz.String() != id {
			t.Errorf("%s Identifier/String mismatch", id)
		}
	}
}
