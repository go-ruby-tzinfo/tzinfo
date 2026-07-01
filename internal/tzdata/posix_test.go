// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzdata

import (
	"testing"
	"time"
)

func TestParsePosixTZValid(t *testing.T) {
	r, ok := parsePosixTZ("EST5EDT,M3.2.0,M11.1.0")
	if !ok {
		t.Fatal("expected a valid rule")
	}
	if r.stdAbbr != "EST" || r.dstAbbr != "EDT" {
		t.Errorf("abbrs = %q/%q", r.stdAbbr, r.dstAbbr)
	}
	if r.stdOff != -5*3600 || r.dstOff != -4*3600 {
		t.Errorf("offsets = %d/%d", r.stdOff, r.dstOff)
	}
	// Bracketed names, explicit DST offset with minutes/seconds, and /time.
	r2, ok := parsePosixTZ("<+0330>-3:30<+0430>-4:30,J1/1:2:3,J365/23")
	if !ok {
		t.Fatal("expected a valid bracketed rule")
	}
	if r2.stdAbbr != "+0330" || r2.stdOff != 3*3600+30*60 {
		t.Errorf("bracketed std wrong: %q %d", r2.stdAbbr, r2.stdOff)
	}
	if r2.start.time != 1*3600+2*60+3 {
		t.Errorf("start time = %d", r2.start.time)
	}
}

func TestParsePosixTZInvalid(t *testing.T) {
	cases := []string{
		"",                    // empty
		",bad",                // no std name
		"EST",                 // no std offset
		"EST5",                // fixed offset, no DST (ok=false)
		"EST5EDT",             // DST but no rule section
		"EST5EDT,M3.2.0",      // only one rule
		"EST5EDT,bad,M11.1.0", // bad start rule
		"EST5EDT,M3.2.0,bad",  // bad end rule
		"EST5EDT99999999999999999999,M3.2.0,M11.1.0", // overflowing dst offset
		"EST99999999999999999999,M3.2.0,M11.1.0",     // overflowing std offset
		"<unterminated",                              // unterminated bracket name
		"EST5<unterminated",                          // unterminated dst bracket name
		"EST5EDTM3.2.0,M11.1.0",                      // missing comma before rules
	}
	for _, c := range cases {
		if _, ok := parsePosixTZ(c); ok {
			t.Errorf("parsePosixTZ(%q) = ok, want !ok", c)
		}
	}
}

func TestReadNameErrors(t *testing.T) {
	if _, _, err := readName(""); err == nil {
		t.Error("empty name should error")
	}
	if _, _, err := readName("<abc"); err == nil {
		t.Error("unterminated bracket should error")
	}
	if _, _, err := readName("5foo"); err == nil {
		t.Error("leading digit is not a name")
	}
	n, rest, err := readName("<ab>rest")
	if err != nil || n != "ab" || rest != "rest" {
		t.Errorf("bracket name parse: %q %q %v", n, rest, err)
	}
	// A leading sign (i == 0) is not a name.
	if _, _, err := readName("-x"); err == nil {
		t.Error("leading sign should not be a name")
	}
	if _, _, err := readName("+x"); err == nil {
		t.Error("leading plus should not be a name")
	}
}

// TestReadOffsetBadFields covers the strconv.Atoi failure branches: an empty
// hour field (from a leading colon) and out-of-range minute/second fields that
// overflow an int.
func TestReadOffsetBadFields(t *testing.T) {
	if _, _, err := readOffset(":30"); err == nil {
		t.Error("empty hour field should error")
	}
	big := "99999999999999999999"
	if _, _, err := readOffset("4:" + big); err == nil {
		t.Error("overflowing minute field should error")
	}
	if _, _, err := readOffset("4:5:" + big); err == nil {
		t.Error("overflowing second field should error")
	}
	// A trailing empty second field is simply skipped.
	if _, _, err := readOffset("4:5:"); err != nil {
		t.Errorf("trailing empty second field is skipped, got %v", err)
	}
}

func TestReadOffsetForms(t *testing.T) {
	if _, _, err := readOffset(""); err == nil {
		t.Error("empty offset should error")
	}
	if _, _, err := readOffset("+"); err == nil {
		t.Error("sign with no digits should error")
	}
	if _, _, err := readOffset(",x"); err == nil {
		t.Error("no digits should error")
	}
	s, rest, err := readOffset("-4:30:15rest")
	if err != nil || s != -(4*3600+30*60+15) || rest != "rest" {
		t.Errorf("hms parse: %d %q %v", s, rest, err)
	}
	s, _, err = readOffset("+7")
	if err != nil || s != 7*3600 {
		t.Errorf("plus parse: %d %v", s, err)
	}
}

func TestParseTransDateForms(t *testing.T) {
	// Absolute day-of-year form (n) with a /time.
	d, ok := parseTransDate("59/1:30")
	if !ok || d.kind != dateAbsolute || d.dayOfYear != 59 || d.time != 3600+1800 {
		t.Errorf("absolute parse: %+v ok=%v", d, ok)
	}
	// Julian form.
	d, ok = parseTransDate("J60")
	if !ok || d.kind != dateJulian || d.dayOfYear != 60 {
		t.Errorf("julian parse: %+v ok=%v", d, ok)
	}
	// Month-week-day form.
	d, ok = parseTransDate("M3.2.0")
	if !ok || d.kind != dateMonthWeekDay || d.month != 3 || d.week != 2 || d.day != 0 {
		t.Errorf("mwd parse: %+v ok=%v", d, ok)
	}
	// Invalid forms.
	if _, ok := parseTransDate(""); ok {
		t.Error("empty date should fail")
	}
	if _, ok := parseTransDate("M3.2"); ok {
		t.Error("incomplete M rule should fail")
	}
	if _, ok := parseTransDate("Mx.2.0"); ok {
		t.Error("non-numeric M rule should fail")
	}
	if _, ok := parseTransDate("Jxx"); ok {
		t.Error("non-numeric Julian should fail")
	}
	if _, ok := parseTransDate("xx"); ok {
		t.Error("non-numeric absolute should fail")
	}
	if _, ok := parseTransDate("M3.2.0/xx"); ok {
		t.Error("bad /time should fail")
	}
}

func TestPosixFooter(t *testing.T) {
	if got := posixFooter([]byte("no newline")); got != "" {
		t.Errorf("footer without trailing newline = %q", got)
	}
	if got := posixFooter([]byte("")); got != "" {
		t.Errorf("empty footer = %q", got)
	}
	// Without a preceding newline (start scans off the front) the footer is empty.
	if got := posixFooter([]byte("ABC\n")); got != "" {
		t.Errorf("footer without leading newline = %q, want empty", got)
	}
	// A well-formed "\n<TZ>\n" footer.
	if got := posixFooter([]byte("\nABC\n")); got != "ABC" {
		t.Errorf("footer = %q, want ABC", got)
	}
	if got := posixFooter([]byte("head\nEST5EDT,M3.2.0,M11.1.0\n")); got != "EST5EDT,M3.2.0,M11.1.0" {
		t.Errorf("footer = %q", got)
	}
}

func TestInstantForms(t *testing.T) {
	// Julian day 60 is always 1 March. Verify local midnight for a leap and a
	// non-leap year at UTC offset 0.
	jul := posixDate{kind: dateJulian, dayOfYear: 60, time: 0}
	leapMar1 := jul.instant(2024, 0) // 2024 is a leap year
	if leapMar1 != mustUnix(2024, 3, 1) {
		t.Errorf("julian 60 leap = %d, want %d", leapMar1, mustUnix(2024, 3, 1))
	}
	nonleapMar1 := jul.instant(2023, 0)
	if nonleapMar1 != mustUnix(2023, 3, 1) {
		t.Errorf("julian 60 non-leap = %d, want %d", nonleapMar1, mustUnix(2023, 3, 1))
	}
	// Absolute day 59 is 29 Feb on a leap year, 1 March otherwise.
	abs := posixDate{kind: dateAbsolute, dayOfYear: 59, time: 0}
	if abs.instant(2024, 0) != mustUnix(2024, 2, 29) {
		t.Errorf("absolute 59 leap should be 29 Feb")
	}
	if abs.instant(2023, 0) != mustUnix(2023, 3, 1) {
		t.Errorf("absolute 59 non-leap should be 1 March")
	}
}

func TestIsLeapAndAbs(t *testing.T) {
	for _, c := range []struct {
		y  int
		ok bool
	}{{2000, true}, {1900, false}, {2024, true}, {2023, false}, {2100, false}} {
		if isLeap(c.y) != c.ok {
			t.Errorf("isLeap(%d) = %v, want %v", c.y, isLeap(c.y), c.ok)
		}
	}
	if abs(-5) != 5 || abs(5) != 5 || abs(0) != 0 {
		t.Errorf("abs wrong")
	}
}

func mustUnix(y, m, d int) int64 {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).Unix()
}
