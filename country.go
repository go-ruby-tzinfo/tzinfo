// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzinfo

import (
	"sort"
	"strings"
)

// Country is an ISO-3166 country and the timezone identifiers observed within it
// (TZInfo::Country). The data derives from the IANA zone1970.tab / iso3166.tab.
type Country struct {
	code    string
	name    string
	zoneIDs []string
}

// GetCountry resolves a country by its ISO-3166 alpha-2 code
// (TZInfo::Country.get). The lookup is case-insensitive on input but the gem's
// codes are upper-case. An unknown code returns *InvalidCountryCode.
func GetCountry(code string) (*Country, error) {
	up := strings.ToUpper(code)
	rec, ok := countryData[up]
	if !ok {
		return nil, &InvalidCountryCode{Code: code}
	}
	ids := make([]string, len(rec.zones))
	copy(ids, rec.zones)
	return &Country{code: up, name: rec.name, zoneIDs: ids}, nil
}

// Code returns the ISO-3166 alpha-2 code (code).
func (c *Country) Code() string { return c.code }

// Name returns the country's name (name).
func (c *Country) Name() string { return c.name }

// ZoneIdentifiers returns the timezone identifiers for the country in the IANA
// data's order (zone_identifiers).
func (c *Country) ZoneIdentifiers() []string {
	out := make([]string, len(c.zoneIDs))
	copy(out, c.zoneIDs)
	return out
}

// Zones returns a resolved Timezone for each of the country's identifiers
// (zones). It returns an error if any identifier fails to resolve (it will not,
// for the shipped data).
func (c *Country) Zones() ([]*Timezone, error) {
	out := make([]*Timezone, 0, len(c.zoneIDs))
	for _, id := range c.zoneIDs {
		tz, err := Get(id)
		if err != nil {
			return nil, err
		}
		out = append(out, tz)
	}
	return out, nil
}

// AllCountryCodes returns every known ISO-3166 code, sorted
// (TZInfo::Country.all_codes).
func AllCountryCodes() []string {
	out := make([]string, 0, len(countryData))
	for code := range countryData {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}
