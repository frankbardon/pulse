package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/spf13/afero"
)

// TestTimeZoneField_GroupedFilterPrecompute (TODO-735): over a
// parent-grouped (0x02) cohort whose group holds BOTH the filtered
// datetime and its per-row zone field, a FILTER_DATE_RANGES reading the
// zone through a slot `tz_field` or the request `time_zone_field` is
// evaluated once per group entry by the filter precompute — and its
// answer equals the flat (0x01) cohort's per-row answer and the
// time.In reference on every arm that routes through the precompute.
// The fixture's local days differ from its UTC days, and every seventh
// entry's zone cell is null, so a precompute that decoded the entry
// without its zone member (the filter's second input) drops or keeps
// the wrong rows here.
func TestTimeZoneField_GroupedFilterPrecompute(t *testing.T) {
	// serial: reads the process-wide processing.FilterPrecomputeStats counter exactly
	ctx := context.Background()
	fs := afero.NewMemMapFs()

	// Two child rows per (instant, site) entry: the group key (ts, site)
	// determines the zone, so ts and tz are members of ONE group.
	entries := perRowRows(7)
	var rows []perRowRow
	for _, r := range entries {
		rows = append(rows, r, r)
	}
	flat := importPerRowFile(t, fs, "grouped_zone_flat", rows)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	const grouped = "grouped_zone.pulse"
	if _, err := p.Dedup(ctx, flat, pulse.DedupOptions{
		Groups:     []pio.GroupDecl{{Key: []string{"ts", "site"}, Members: []string{"tz"}}},
		Out:        grouped,
		RatioFloor: 1,
	}); err != nil {
		t.Fatalf("Dedup: %v", err)
	}
	c, err := p.Open(ctx, grouped)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Schema()
	if !s.HasGroups() || s.GroupEntryCount(0) != len(entries) {
		t.Fatalf("grouped fixture: groups=%v entries=%d, want one group of %d entries", s.HasGroups(), s.GroupEntryCount(0), len(entries))
	}

	var filterCase perRowCase
	for _, tc := range perRowCases() {
		if tc.name == "filter-date-ranges" {
			filterCase = tc
		}
	}
	want := filterCase.reference(t, rows)
	if len(want) != len(perRowSites) {
		t.Fatalf("reference covers %d sites, want %d", len(want), len(perRowSites))
	}

	// The arms that route a grouped cohort through the precompute.
	routed := map[string]bool{"buffered-projected": true, "buffered-full-decode": true, "buffered-serial-decode": true, "stream": true}
	for _, zoning := range []struct {
		name string
		z    perRowZoning
	}{{"tz_field", perRowZoning{tzField: "tz"}}, {"time_zone_field", perRowZoning{reqField: "tz"}}} {
		for _, rn := range perRowRunners(t, fs) {
			if !routed[rn.name] {
				continue
			}
			t.Run(zoning.name+"/"+rn.name, func(t *testing.T) {
				before := processing.FilterPrecomputeStats().EntryEvaluations
				requireFigures(t, "flat", rn.run(t, filterCase.build(flat, zoning.z)), want)
				if d := processing.FilterPrecomputeStats().EntryEvaluations - before; d != 0 {
					t.Fatalf("flat cohort made %d entry evaluations; no precompute may run on 0x01", d)
				}
				before = processing.FilterPrecomputeStats().EntryEvaluations
				requireFigures(t, "grouped", rn.run(t, filterCase.build(grouped, zoning.z)), want)
				if d := processing.FilterPrecomputeStats().EntryEvaluations - before; d != int64(len(entries)) {
					t.Fatalf("grouped cohort made %d entry evaluations, want %d (one per entry): the filter did not take the precompute", d, len(entries))
				}
			})
		}
	}
}
