// Copyright (c) the go-ruby-tzinfo/tzinfo authors
//
// SPDX-License-Identifier: BSD-3-Clause

package tzinfo

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestIdentifiersErrorForwarding forces the identifier loader to fail and checks
// that initIdentifiers skips the sort and AllIdentifiers, AllDataZoneIdentifiers
// and All all propagate the error. It resets the sync.Once so the failing loader
// actually runs through initIdentifiers.
func TestIdentifiersErrorForwarding(t *testing.T) {
	// Ensure the real list is loaded first (so the success path is also covered).
	if _, err := AllIdentifiers(); err != nil {
		t.Fatal(err)
	}

	savedIDs, savedErr, savedFn, savedOnce := dataIDs, idsErr, namesFn, idsOnce
	sentinel := errors.New("injected names failure")
	namesFn = func() ([]string, error) { return nil, sentinel }
	idsOnce = &sync.Once{} // fresh Once so initIdentifiers runs with the failing source
	defer func() {
		dataIDs, idsErr, namesFn, idsOnce = savedIDs, savedErr, savedFn, savedOnce
	}()

	if _, err := AllIdentifiers(); !errors.Is(err, sentinel) {
		t.Errorf("AllIdentifiers err = %v, want sentinel", err)
	}
	if _, err := AllDataZoneIdentifiers(); !errors.Is(err, sentinel) {
		t.Errorf("AllDataZoneIdentifiers err = %v, want sentinel", err)
	}
	if _, err := All(); !errors.Is(err, sentinel) {
		t.Errorf("All err = %v, want sentinel", err)
	}
}

// TestAllGetError covers the per-identifier Get failure inside All by injecting
// an identifier list containing an unresolvable zone.
func TestAllGetError(t *testing.T) {
	savedIDs, savedErr, savedOnce := dataIDs, idsErr, idsOnce
	dataIDs, idsErr = []string{"No/Such_Zone"}, nil
	spent := &sync.Once{}
	spent.Do(func() {}) // mark spent so identifiers() reads dataIDs directly
	idsOnce = spent
	defer func() {
		dataIDs, idsErr, idsOnce = savedIDs, savedErr, savedOnce
	}()

	if _, err := All(); err == nil {
		t.Error("All should fail when an identifier does not resolve")
	}
}

// TestCountryZonesError covers the Zones error path by handing a Country an
// identifier that resolves to no zone.
func TestCountryZonesError(t *testing.T) {
	c := &Country{code: "XX", name: "Test", zoneIDs: []string{"No/Such_Zone"}}
	if _, err := c.Zones(); err == nil {
		t.Error("Zones should fail for an unresolvable identifier")
	}
}

// TestPeriodForLocalPreFirst exercises the candidatePeriods branches for a local
// wall time before the zone's first transition (the k<0 path and the
// unbounded-past dedup key).
func TestPeriodForLocalPreFirst(t *testing.T) {
	tz, err := Get("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 1850 is before New York's first recorded transition (LMT era).
	early := time.Date(1850, 6, 1, 12, 0, 0, 0, time.UTC)
	p, err := tz.PeriodForLocal(early)
	if err != nil {
		t.Fatalf("pre-first PeriodForLocal err = %v", err)
	}
	if p.Abbreviation() != "LMT" {
		t.Errorf("pre-first abbr = %q, want LMT", p.Abbreviation())
	}
	// LocalToUTC through the same early path.
	if _, err := tz.LocalToUTC(early); err != nil {
		t.Errorf("pre-first LocalToUTC err = %v", err)
	}
}
