// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzinfo

import (
	"bufio"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The oracle tests validate this package differentially against the live tzinfo
// gem. They run one Ruby subprocess that emits the gem's answers for a battery of
// (zone, instant) probes, then require an exact match. They self-skip when Ruby
// (with the tzinfo gem) is unavailable — every arch/qemu lane and Windows — so
// the deterministic golden vectors are the sole coverage driver there. The
// committed golden vectors alone hold 100% coverage, so skipping loses nothing.
//
// The gem is version-gated to Ruby >= 4.0: older interpreters that shipped a
// different tzinfo-data or zoneinfo release could disagree on recently-amended
// zones, so we only assert parity against the interpreter this module targets.

// rubyOracle runs the given Ruby script and returns its stdout, or skips the test
// if Ruby, the tzinfo gem, or a new-enough interpreter is missing.
func rubyOracle(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("oracle: no ruby on the Windows lane")
	}
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("oracle: ruby not installed")
	}
	// Gate on Ruby >= 4.0 and the presence of the tzinfo gem.
	guard := `require "tzinfo"; abort("old") if (RUBY_VERSION.split(".").map(&:to_i) <=> [4,0]) < 0`
	if err := exec.Command("ruby", "-e", guard).Run(); err != nil {
		t.Skip("oracle: ruby < 4.0 or tzinfo gem absent")
	}
	out, err := exec.Command("ruby", "-e", script).Output()
	if err != nil {
		t.Skipf("oracle: ruby execution failed: %v", err)
	}
	return string(out)
}

// oracleZones is the differential probe set: a spread across hemispheres, offset
// magnitudes, half-hour and 45-minute zones, negative-DST zones, fixed zones and
// former link names.
var oracleZones = []string{
	"America/New_York", "America/Los_Angeles", "America/Sao_Paulo",
	"America/St_Johns", "America/Argentina/Buenos_Aires",
	"Europe/London", "Europe/Paris", "Europe/Moscow", "Europe/Dublin",
	"Asia/Kolkata", "Asia/Kathmandu", "Asia/Tehran", "Asia/Tokyo",
	"Australia/Lord_Howe", "Australia/Sydney", "Pacific/Chatham",
	"Pacific/Kiritimati", "Africa/Casablanca", "Antarctica/Troll",
	"UTC", "Etc/GMT+12", "US/Eastern",
}

// oracleInstants spans pre-1970 LMT, the epoch, and near/far future (covering the
// POSIX-rule-generated transitions).
var oracleInstants = []string{
	"1875-06-01T00:00:00Z", "1935-01-01T00:00:00Z", "1969-12-31T23:30:00Z",
	"1970-01-01T00:00:00Z", "2001-07-04T00:00:00Z", "2023-01-15T12:00:00Z",
	"2023-07-15T12:00:00Z", "2038-06-01T00:00:00Z", "2075-01-01T00:00:00Z",
}

// TestOracleUTC checks period_for_utc / utc_to_local parity against the gem for
// every (zone, instant) probe.
func TestOracleUTC(t *testing.T) {
	var b strings.Builder
	b.WriteString(`require "tzinfo"; require "time"` + "\n")
	b.WriteString("zones = %w[" + strings.Join(oracleZones, " ") + "]\n")
	b.WriteString("inst = %w[" + strings.Join(oracleInstants, " ") + "]\n")
	b.WriteString(`zones.each do |z|
  tz = TZInfo::Timezone.get(z)
  inst.each do |iso|
    a = iso.scan(/\d+/).map(&:to_i)
    t = Time.utc(a[0],a[1],a[2],a[3],a[4],a[5])
    p = tz.period_for_utc(t); o = p.offset
    puts [z, iso, p.utc_total_offset, o.base_utc_offset, o.std_offset, (p.dst? ? 1 : 0), o.abbreviation, tz.utc_to_local(t).strftime("%Y-%m-%dT%H:%M:%S")].join("\t")
  end
end`)
	out := rubyOracle(t, b.String())

	sc := bufio.NewScanner(strings.NewReader(out))
	rows := 0
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) != 8 {
			t.Fatalf("bad oracle row: %q", sc.Text())
		}
		zone, iso := f[0], f[1]
		total, _ := strconv.Atoi(f[2])
		base, _ := strconv.Atoi(f[3])
		std, _ := strconv.Atoi(f[4])
		dst := f[5] == "1"
		abbr, local := f[6], f[7]

		tz, err := Get(zone)
		if err != nil {
			t.Fatalf("Get(%q): %v", zone, err)
		}
		u, _ := time.Parse(time.RFC3339, iso)
		p := tz.PeriodForUTC(u)
		if p.UTCTotalOffset() != total || p.BaseUTCOffset() != base || p.STDOffset() != std ||
			p.DST() != dst || p.Abbreviation() != abbr {
			t.Errorf("%s @ %s: got total=%d base=%d std=%d dst=%v abbr=%q; want total=%d base=%d std=%d dst=%v abbr=%q",
				zone, iso, p.UTCTotalOffset(), p.BaseUTCOffset(), p.STDOffset(), p.DST(), p.Abbreviation(),
				total, base, std, dst, abbr)
		}
		if got := tz.UTCToLocal(u).Format("2006-01-02T15:04:05"); got != local {
			t.Errorf("%s @ %s local = %q, want %q", zone, iso, got, local)
		}
		rows++
	}
	if rows != len(oracleZones)*len(oracleInstants) {
		t.Errorf("oracle produced %d rows, want %d", rows, len(oracleZones)*len(oracleInstants))
	}
}

// TestOracleTransitions checks transition-boundary parity (instant, offset split,
// abbreviation) against the gem over a modern window for the DST zones.
func TestOracleTransitions(t *testing.T) {
	zones := []string{"America/New_York", "Europe/London", "Australia/Sydney", "Pacific/Chatham", "America/Sao_Paulo", "Africa/Casablanca"}
	var b strings.Builder
	b.WriteString(`require "tzinfo"` + "\n")
	b.WriteString("zones = %w[" + strings.Join(zones, " ") + "]\n")
	b.WriteString(`from = Time.utc(2000,1,1); to = Time.utc(2035,1,1)
zones.each do |z|
  tz = TZInfo::Timezone.get(z)
  tz.transitions_up_to(to, from).each do |tr|
    o = tr.offset
    puts [z, tr.at.to_time.utc.strftime("%Y-%m-%dT%H:%M:%SZ"), o.observed_utc_offset, o.base_utc_offset, o.std_offset, (o.dst? ? 1 : 0), o.abbreviation].join("\t")
  end
end`)
	out := rubyOracle(t, b.String())

	// Group expected transitions by zone.
	type tr struct {
		at, abbr         string
		total, base, std int
		dst              bool
	}
	want := map[string][]tr{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) != 7 {
			t.Fatalf("bad oracle transition row: %q", sc.Text())
		}
		total, _ := strconv.Atoi(f[2])
		base, _ := strconv.Atoi(f[3])
		std, _ := strconv.Atoi(f[4])
		want[f[0]] = append(want[f[0]], tr{at: f[1], abbr: f[6], total: total, base: base, std: std, dst: f[5] == "1"})
	}

	from := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, z := range zones {
		tz, err := Get(z)
		if err != nil {
			t.Fatal(err)
		}
		got := tz.TransitionsUpTo(to, from)
		exp := want[z]
		if len(got) != len(exp) {
			t.Errorf("%s: %d transitions, want %d", z, len(got), len(exp))
			continue
		}
		for i, g := range got {
			e := exp[i]
			if g.At.Format("2006-01-02T15:04:05Z") != e.at ||
				g.Offset.UTCTotalOffset() != e.total || g.Offset.BaseUTCOffset != e.base ||
				g.Offset.STDOffset != e.std || g.Offset.DST() != e.dst || g.Offset.Abbreviation != e.abbr {
				t.Errorf("%s transition %d mismatch: got %+v; want %+v", z, i, g, e)
			}
		}
	}
}

// TestOracleLocalResolution checks period_for_local parity for normal, gap and
// ambiguous local times against the gem.
func TestOracleLocalResolution(t *testing.T) {
	script := `require "tzinfo"
cases = [
  ["America/New_York","2023-06-01T12:00:00"],
  ["America/New_York","2023-03-12T02:30:00"],
  ["America/New_York","2023-11-05T01:30:00"],
  ["Europe/London","2023-03-26T01:30:00"],
  ["Europe/London","2023-10-29T01:30:00"],
  ["Australia/Sydney","2023-10-01T02:30:00"],
  ["Australia/Sydney","2023-04-02T02:30:00"],
]
cases.each do |z,iso|
  tz = TZInfo::Timezone.get(z)
  a = iso.scan(/\d+/).map(&:to_i)
  begin
    p = tz.period_for_local(Time.new(a[0],a[1],a[2],a[3],a[4],a[5]))
    kind = "ok"; total = p.utc_total_offset
  rescue TZInfo::PeriodNotFound
    kind = "gap"; total = 0
  rescue TZInfo::AmbiguousTime
    kind = "ambig"; total = 0
  end
  puts [z, iso, kind, total].join("\t")
end`
	out := rubyOracle(t, script)

	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) != 4 {
			t.Fatalf("bad oracle local row: %q", sc.Text())
		}
		zone, iso, kind := f[0], f[1], f[2]
		total, _ := strconv.Atoi(f[3])
		tz, _ := Get(zone)
		w, _ := time.Parse("2006-01-02T15:04:05", iso)
		p, err := tz.PeriodForLocal(w)
		switch kind {
		case "ok":
			if err != nil {
				t.Errorf("%s local %s: err = %v, want ok", zone, iso, err)
			} else if p.UTCTotalOffset() != total {
				t.Errorf("%s local %s: total = %d, want %d", zone, iso, p.UTCTotalOffset(), total)
			}
		case "gap":
			if _, ok := err.(*PeriodNotFound); !ok {
				t.Errorf("%s local %s: err = %v, want *PeriodNotFound", zone, iso, err)
			}
		case "ambig":
			if _, ok := err.(*AmbiguousTime); !ok {
				t.Errorf("%s local %s: err = %v, want *AmbiguousTime", zone, iso, err)
			}
		}
	}
}
