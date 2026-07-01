// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzinfo

import (
	"sort"
	"sync"

	"github.com/go-ruby-tzinfo/tzinfo/internal/tzdata"
)

var (
	// idsOnce is a pointer so tests can swap in a fresh Once (a sync.Once must not
	// be copied) to re-run initIdentifiers against an injected failing source.
	idsOnce = &sync.Once{}
	dataIDs []string // data zones, sorted
	idsErr  error
)

// initIdentifiers loads the sorted data-zone identifier list once. The embedded
// archive already materialises every IANA backward-link name as a full data zone,
// exactly as the gem's ZoneinfoDataSource does, so this single list is both
// all_identifiers and all_data_zone_identifiers (the gem reports zero linked
// zones and 598 identifiers either way).
func initIdentifiers() {
	dataIDs, idsErr = namesFn()
	if idsErr != nil {
		return
	}
	sort.Strings(dataIDs)
}

// namesFn is the source of the identifier list; a variable so tests can inject a
// failure to exercise the error-forwarding branches below.
var namesFn = tzdata.Names

// identifiers returns a fresh copy of the sorted identifier list, or the load
// error. It underlies both AllIdentifiers and AllDataZoneIdentifiers, which are
// equal because every backward-link name is materialised as a full data zone.
func identifiers() ([]string, error) {
	idsOnce.Do(initIdentifiers)
	if idsErr != nil {
		return nil, idsErr
	}
	out := make([]string, len(dataIDs))
	copy(out, dataIDs)
	return out, nil
}

// AllIdentifiers returns every timezone identifier, sorted
// (TZInfo::Timezone.all_identifiers). Because every backward-link name is a full
// data zone, this equals AllDataZoneIdentifiers.
func AllIdentifiers() ([]string, error) {
	return identifiers()
}

// AllDataZoneIdentifiers returns only the canonical (data) zone identifiers,
// sorted (TZInfo::Timezone.all_data_zone_identifiers).
func AllDataZoneIdentifiers() ([]string, error) {
	return identifiers()
}

// All returns a resolved Timezone for every identifier (TZInfo::Timezone.all).
func All() ([]*Timezone, error) {
	ids, err := AllIdentifiers()
	if err != nil {
		return nil, err
	}
	out := make([]*Timezone, 0, len(ids))
	for _, id := range ids {
		tz, err := Get(id)
		if err != nil {
			return nil, err
		}
		out = append(out, tz)
	}
	return out, nil
}
