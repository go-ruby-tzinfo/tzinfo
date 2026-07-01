// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzdata

import (
	"errors"
	"strconv"
	"strings"
)

// The compact TZif files shipped by IANA (and re-embedded here from Go's
// time/tzdata) list explicit transitions only up to a cut-off year (often ~2037
// or, for the US, 2007) and encode all later transitions as a POSIX TZ string in
// the v2+ block's trailing footer, e.g. "EST5EDT,M3.2.0,M11.1.0". This file
// parses that footer and expands it into concrete future transitions so
// PeriodForUTC / TransitionsUpTo behave correctly for future dates, exactly as
// TZInfo (whose tzinfo-data pre-expands the same rules) does.

// posixRule is a parsed POSIX TZ string with a DST rule.
type posixRule struct {
	stdAbbr string
	stdOff  int // seconds east of UTC
	dstAbbr string
	dstOff  int // seconds east of UTC
	start   posixDate
	end     posixDate
}

// posixDateKind distinguishes the three POSIX transition-date forms.
type posixDateKind int

const (
	// dateMonthWeekDay is the Mm.w.d form (month, week 1..5 with 5=last, weekday).
	dateMonthWeekDay posixDateKind = iota
	// dateJulian is the Jn form: one-based Julian day 1..365 that never counts
	// 29 February (so day 60 is always 1 March).
	dateJulian
	// dateAbsolute is the n form: zero-based day 0..365 that does count 29
	// February (day 59 is 29 February on a leap year).
	dateAbsolute
)

// posixDate is one POSIX transition date. For dateMonthWeekDay, month/week/day
// are set; for dateJulian/dateAbsolute, dayOfYear is set. time is the seconds
// after local midnight the transition occurs (default 02:00).
type posixDate struct {
	kind             posixDateKind
	month, week, day int
	dayOfYear        int
	time             int
}

// parsePosixTZ parses a POSIX TZ footer. It returns ok=false when the footer has
// no DST rule (a zone with a fixed offset needs no future expansion) or is empty.
func parsePosixTZ(s string) (posixRule, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return posixRule{}, false
	}
	stdName, rest, err := readName(s)
	if err != nil {
		return posixRule{}, false
	}
	stdOff, rest, err := readOffset(rest)
	if err != nil {
		return posixRule{}, false
	}
	// POSIX offsets are given as time to ADD to local to reach UTC, i.e. the sign
	// is inverted relative to "east of UTC".
	var r posixRule
	r.stdAbbr = stdName
	r.stdOff = -stdOff
	if rest == "" {
		return posixRule{}, false // no DST section — fixed offset
	}
	dstName, rest2, err := readName(rest)
	if err != nil {
		return posixRule{}, false
	}
	r.dstAbbr = dstName
	// DST offset is optional; default is std + 1h.
	if rest2 != "" && rest2[0] != ',' {
		dstOff, r2, err := readOffset(rest2)
		if err != nil {
			return posixRule{}, false
		}
		r.dstOff = -dstOff
		rest2 = r2
	} else {
		r.dstOff = r.stdOff + 3600
	}
	if rest2 == "" || rest2[0] != ',' {
		return posixRule{}, false
	}
	parts := strings.SplitN(rest2[1:], ",", 2)
	if len(parts) != 2 {
		return posixRule{}, false
	}
	start, ok := parseTransDate(parts[0])
	if !ok {
		return posixRule{}, false
	}
	end, ok := parseTransDate(parts[1])
	if !ok {
		return posixRule{}, false
	}
	r.start = start
	r.end = end
	return r, true
}

// readName reads a POSIX abbreviation, either <...> bracketed or a run of
// alphabetic characters.
func readName(s string) (name, rest string, err error) {
	if s == "" {
		return "", "", errors.New("empty name")
	}
	if s[0] == '<' {
		i := strings.IndexByte(s, '>')
		if i < 0 {
			return "", "", errors.New("unterminated <name>")
		}
		return s[1:i], s[i+1:], nil
	}
	i := 0
	for i < len(s) && (s[i] == '-' || s[i] == '+' || (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
		// A leading sign only belongs to an offset, not a name; stop at the first
		// digit-adjacent sign.
		if (s[i] == '-' || s[i] == '+') && i > 0 {
			break
		}
		if s[i] == '-' || s[i] == '+' {
			break
		}
		i++
	}
	if i == 0 {
		return "", "", errors.New("no name")
	}
	return s[:i], s[i:], nil
}

// readOffset reads a signed [+|-]hh[:mm[:ss]] offset (POSIX sign convention) and
// returns the seconds and the remaining string.
func readOffset(s string) (secs int, rest string, err error) {
	if s == "" {
		return 0, "", errors.New("empty offset")
	}
	sign := 1
	if s[0] == '+' {
		s = s[1:]
	} else if s[0] == '-' {
		sign = -1
		s = s[1:]
	}
	// Read digits/colons for the offset.
	i := 0
	for i < len(s) && (s[i] == ':' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return 0, "", errors.New("no offset digits")
	}
	fields := strings.Split(s[:i], ":")
	h, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", err
	}
	secs = h * 3600
	if len(fields) > 1 && fields[1] != "" {
		m, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0, "", err
		}
		secs += m * 60
	}
	if len(fields) > 2 && fields[2] != "" {
		sec, err := strconv.Atoi(fields[2])
		if err != nil {
			return 0, "", err
		}
		secs += sec
	}
	return sign * secs, s[i:], nil
}

// parseTransDate parses a POSIX transition date in any of the three forms the
// standard defines: Mm.w.d (month/week/weekday), Jn (Julian, no leap day), or n
// (absolute, with leap day). An optional /time (default 02:00) sets the local
// time-of-day of the transition.
func parseTransDate(s string) (posixDate, bool) {
	var d posixDate
	d.time = 2 * 3600 // default 02:00
	slash := strings.IndexByte(s, '/')
	datePart := s
	if slash >= 0 {
		datePart = s[:slash]
		off, _, err := readOffset(s[slash+1:])
		if err != nil {
			return posixDate{}, false
		}
		d.time = off
	}
	if datePart == "" {
		return posixDate{}, false
	}
	switch datePart[0] {
	case 'M':
		nums := strings.Split(datePart[1:], ".")
		if len(nums) != 3 {
			return posixDate{}, false
		}
		m, err1 := strconv.Atoi(nums[0])
		w, err2 := strconv.Atoi(nums[1])
		day, err3 := strconv.Atoi(nums[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return posixDate{}, false
		}
		d.kind = dateMonthWeekDay
		d.month, d.week, d.day = m, w, day
		return d, true
	case 'J':
		n, err := strconv.Atoi(datePart[1:])
		if err != nil {
			return posixDate{}, false
		}
		d.kind = dateJulian
		d.dayOfYear = n
		return d, true
	default:
		n, err := strconv.Atoi(datePart)
		if err != nil {
			return posixDate{}, false
		}
		d.kind = dateAbsolute
		d.dayOfYear = n
		return d, true
	}
}

// posixFooter extracts the TZ string between the trailing newlines of a v2+ TZif
// blob, or "" if absent.
func posixFooter(raw []byte) string {
	// The footer is "\n<TZ>\n" at the very end of the file.
	if len(raw) < 2 || raw[len(raw)-1] != '\n' {
		return ""
	}
	end := len(raw) - 1
	start := end - 1
	for start >= 0 && raw[start] != '\n' {
		start--
	}
	if start < 0 {
		return ""
	}
	return string(raw[start+1 : end])
}
