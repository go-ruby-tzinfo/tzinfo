<p align="center"><img src="https://raw.githubusercontent.com/go-ruby-tzinfo/brand/main/social/go-ruby-tzinfo-tzinfo.png" alt="go-ruby-tzinfo/tzinfo" width="720"></p>

# tzinfo — go-ruby-tzinfo

[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

**A pure-Go (no cgo) reimplementation of Ruby's
[`TZInfo`](https://github.com/tzinfo/tzinfo) library** — the `tzinfo` gem's
timezone engine, minus the Ruby runtime. It embeds a complete offline copy of the
compiled IANA time zone database and exposes the **TZInfo API** on top of it:
resolving a zone by identifier, converting between UTC and local time, computing
the `TimezonePeriod` (offset, abbreviation, DST flag, transition boundaries) in
force at any instant — historical, present, or far future — and handling the
spring-forward gap (`PeriodNotFound`) and fall-back overlap (`AmbiguousTime`)
exactly as the gem does.

It is a sibling of the other pure-Go Ruby front-ends
([go-ruby-regexp](https://github.com/go-ruby-regexp/regexp) — the Onigmo engine,
[go-ruby-erb](https://github.com/go-ruby-erb/erb) — the ERB compiler,
[go-ruby-yaml](https://github.com/go-ruby-yaml/yaml) — the Psych YAML core),
and the timezone backend for
[go-embedded-ruby](https://github.com/go-embedded-ruby/ruby). It is a
**standalone, reusable** module with no dependency on the Ruby runtime.

> **What it is — and isn't.** Resolving zones, computing offsets, and finding the
> period for an instant is fully deterministic and needs **no interpreter**, so it
> lives here as pure Go. The one input it needs is the compiled IANA zoneinfo,
> which is **committed in-repo** (`go:embed`) so the library is complete offline
> with no filesystem or network dependency.

## Features

Faithful port of the TZInfo API, validated against the `tzinfo` gem (Ruby ≥ 4.0)
on every supported platform:

- **`Get`** — resolve any of the **598** IANA identifiers, including former
  backward-link names (`US/Eastern`, `GB`, `Zulu`, …), matching the gem's
  `ZoneinfoDataSource` (every name is a full data zone; zero linked zones).
- **Offsets and periods** — `PeriodForUTC` / `PeriodForLocal`, `UTCToLocal` /
  `LocalToUTC`, `CurrentPeriod`, `Now`, plus `Abbreviation`, `UTCOffset` and
  `DST` at an instant. Every period exposes the gem's exact
  `base_utc_offset` / `std_offset` split (derived from the surrounding standard
  periods, negative-DST zones included), `utc_total_offset`, `abbreviation` and
  its bounding transitions.
- **Correct DST math** — EST↔EDT-style transitions for historical dates
  (pre-1970 LMT, wartime double DST) and future dates generated from the TZif
  POSIX-TZ footer rules (`Mm.w.d`, `Jn`, and `n` date forms).
- **Ambiguity handling** — `PeriodForLocal` returns `*PeriodNotFound` for a
  spring-forward gap and `*AmbiguousTime` for a fall-back overlap, with an
  optional `dst` preference to disambiguate — exactly like the gem.
- **Transitions & offsets** — `TransitionsUpTo` and `OffsetsUpTo` over a UTC
  window, chronological and de-duplicated.
- **`TZInfo::Country`** — `GetCountry`, `Code` / `Name` / `ZoneIdentifiers` /
  `Zones`, `AllCountryCodes` (**249** ISO-3166 countries), from the IANA
  `iso3166.tab` / `zone1970.tab`.

CGO-free, dependency-free, **100% test coverage**, `gofmt` + `go vet` clean, and
green across the six 64-bit Go targets (amd64, arm64, riscv64, loong64, ppc64le,
s390x) and three operating systems.

## Install

```sh
go get github.com/go-ruby-tzinfo/tzinfo
```

## Usage

```go
package main

import (
	"fmt"
	"time"

	"github.com/go-ruby-tzinfo/tzinfo"
)

func main() {
	tz, _ := tzinfo.Get("America/New_York")

	// Period in force at a UTC instant (summer → EDT).
	p := tz.PeriodForUTC(time.Date(2023, 6, 1, 12, 0, 0, 0, time.UTC))
	fmt.Println(p.Abbreviation(), p.UTCTotalOffset(), p.DST())
	// EDT -14400 true

	// UTC → local, tagged with the resolved offset.
	fmt.Println(tz.UTCToLocal(time.Date(2023, 6, 1, 12, 0, 0, 0, time.UTC)))
	// 2023-06-01 08:00:00 -0400 EDT

	// Local → UTC, with gap / ambiguity errors.
	if _, err := tz.LocalToUTC(time.Date(2023, 3, 12, 2, 30, 0, 0, time.UTC)); err != nil {
		fmt.Println(err) // spring-forward gap: *PeriodNotFound
	}
	if _, err := tz.LocalToUTC(time.Date(2023, 11, 5, 1, 30, 0, 0, time.UTC)); err != nil {
		fmt.Println(err) // fall-back overlap: *AmbiguousTime
	}
	// Disambiguate the overlap by asking for the daylight period.
	utc, _ := tz.LocalToUTC(time.Date(2023, 11, 5, 1, 30, 0, 0, time.UTC), true)
	fmt.Println(utc)
}
```

## API ↔ gem

| Ruby (`tzinfo` gem)                   | Go (this package)                    |
| ------------------------------------- | ------------------------------------ |
| `TZInfo::Timezone.get("America/…")`   | `tzinfo.Get("America/…")`            |
| `TZInfo::Timezone.all_identifiers`    | `tzinfo.AllIdentifiers()`            |
| `TZInfo::Timezone.all`                | `tzinfo.All()`                       |
| `tz.utc_to_local(t)` / `local_to_utc` | `tz.UTCToLocal(t)` / `LocalToUTC`    |
| `tz.period_for_utc(t)` / `_for_local` | `tz.PeriodForUTC(t)` / `PeriodForLocal` |
| `tz.transitions_up_to(to, from)`      | `tz.TransitionsUpTo(to, from)`       |
| `tz.offsets_up_to(to, from)`          | `tz.OffsetsUpTo(to, from)`           |
| `tz.current_period` / `.now`          | `tz.CurrentPeriod()` / `tz.Now()`    |
| `tz.abbreviation(t)` / `dst?(t)`      | `tz.Abbreviation(t)` / `tz.DST(t)`   |
| `TimezonePeriod` / `TimezoneOffset`   | `TimezonePeriod` / `TimezoneOffset`  |
| `TZInfo::PeriodNotFound` / `AmbiguousTime` | `*PeriodNotFound` / `*AmbiguousTime` |
| `TZInfo::Country.get("US")`           | `tzinfo.GetCountry("US")`            |

## Time zone data

The `internal/tzdata` package embeds the compiled IANA zoneinfo (the same TZif
files the gem's `ZoneinfoDataSource` reads from the system) as a committed,
store-method zip, and parses the TZif v2/v3 binary format itself. Because the base
(standard) offset for a DST period is not stored in a zoneinfo file, it is derived
from the surrounding non-DST periods exactly as TZInfo's `ZoneinfoReader` does, so
the `base_utc_offset` / `std_offset` split matches the gem byte-for-byte.

## Tests & coverage

The suite is two-layer. **Deterministic, ruby-free golden vectors** (captured from
the gem and committed) reproduce exact parity on offsets, abbreviations, DST
flags, periods and local-time resolution across zones and transition instants, and
alone hold **100% statement coverage** — so the arch/qemu and Windows lanes stay
green with no Ruby present. On the ubuntu/macos lanes an **oracle** additionally
runs the live `tzinfo` gem (version-gated to Ruby ≥ 4.0) and diffs it against this
package.

```sh
go test -race -coverpkg=./... -coverprofile=cover.out ./...
go tool cover -func=cover.out | tail -1   # total: 100.0%
```

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-ruby-tzinfo/tzinfo authors.

## WebAssembly

Being pure Go (CGO=0), this library also compiles to **WebAssembly** — both
`GOOS=js GOARCH=wasm` (browser / Node.js) and `GOOS=wasip1 GOARCH=wasm` (WASI).
CI builds both targets on every push, alongside the six 64-bit native/qemu arches.

```sh
GOOS=js     GOARCH=wasm go build ./...   # browser / Node
GOOS=wasip1 GOARCH=wasm go build ./...   # WASI (wasmtime, wasmer, wasmedge, …)
```
