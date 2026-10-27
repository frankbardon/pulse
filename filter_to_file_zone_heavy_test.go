//go:build heavy

package pulse_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// TestFilterToFile_DateRangesMatchesProcess: FILTER_DATE_RANGES through
// FilterToFileWithRequest keeps exactly the rows Process keeps with the
// same filterers and request zone — over a `date` field, a `datetime`
// field in UTC, under the request zone, a per-filterer `tz`, a per-row
// `tz_field`, and beside a translated built-in and an extension filterer
// — on a single file, a parallel-decode instance and a shard archive.
// Each zoned case keeps a different row count than its UTC twin, so a
// path that dropped the zone would fail.
func TestFilterToFile_DateRangesMatchesProcess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	params := ftzRanges(t)
	dr := func(field, tz, tzField string) *types.Filterer {
		return &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: field, Params: params, TimeZone: tz, TimeZoneField: tzField}
	}
	cases := []struct {
		name      string
		filterers []*types.Filterer
		tz        string
		zoned     bool // the zone changes the kept rows
	}{
		{"date_field", []*types.Filterer{dr("d", "", "")}, "", false},
		{"date_field_request_zone", []*types.Filterer{dr("d", "", "")}, "America/New_York", false},
		{"datetime_utc", []*types.Filterer{dr("ts", "", "")}, "", false},
		{"datetime_request_zone", []*types.Filterer{dr("ts", "", "")}, "America/New_York", true},
		{"datetime_slot_tz", []*types.Filterer{dr("ts", "Pacific/Honolulu", "")}, "", true},
		{"datetime_slot_tz_over_request", []*types.Filterer{dr("ts", "UTC", "")}, "America/New_York", false},
		{"datetime_tz_field", []*types.Filterer{dr("ts", "", "cat")}, "", true},
		{"with_translated_and_extension", []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "y", Values: []string{"5", "50"}},
			dr("ts", "", ""),
			{Type: ftzExtFilter, Field: "x"},
		}, "America/New_York", true},
	}
	for mode, m := range ftzModes(t) {
		for _, c := range cases {
			t.Run(mode+"/"+c.name, func(t *testing.T) {
				want := ftzCount(t, m, c.filterers, c.tz)
				if want == 0 || want == int64(m.rows) {
					t.Fatalf("Process kept %d of %d rows: the filter does not separate the cohort", want, m.rows)
				}
				if utc := ftzCount(t, m, ftzUTC(c.filterers), ""); c.zoned == (utc == want) {
					t.Fatalf("Process kept %d rows zoned, %d in UTC; zoned=%v", want, utc, c.zoned)
				}
				out := "ftz-" + mode + "-" + c.name
				if err := m.p.Fs().MkdirAll(out, 0o755); err != nil {
					t.Fatal(err)
				}
				res, err := m.p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{
					SourcePath: m.path, Filterers: c.filterers, TimeZone: c.tz, OutputDir: out,
				})
				if err != nil {
					t.Fatalf("FilterToFileWithRequest: %v", err)
				}
				if res.RowCount != want {
					t.Fatalf("filter_to_file kept %d rows, Process %d", res.RowCount, want)
				}
				if all := ftzCount(t, ftzMode{m.p, res.OutputPath, 0}, nil, ""); all != want {
					t.Fatalf("output holds %d rows, want %d", all, want)
				}
			})
		}
	}
}
