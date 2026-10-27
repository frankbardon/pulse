//go:build heavy

package pulse_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestTimeZoneField_PerRowEqualsSingleZoneSlotsEveryMode: every
// zone-capable operator reading a per-row zone field — from the slot's
// `tz_field` or the request's `time_zone_field` — equals the same
// request run once per site with that site's zone as a slot `tz`,
// combined, and equals a time.In reference, in every execution mode
// (projected and full buffered decode, serial decode, streaming, both
// crosstab arms — the fused one asserted taken — and a shard archive
// reduced in parallel and serially). The fixture's local days differ
// from its UTC days, so a zone field that fails to reach an arm (or
// decodes as null because the projection dropped it) fails here.
func TestTimeZoneField_PerRowEqualsSingleZoneSlotsEveryMode(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := perRowRows(0)
	cohort, archive := importPerRow(t, fs, "perrow", rows)
	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	runners := perRowRunners(t, fs)

	for _, tc := range perRowCases() {
		// The oracle: one single-zone run per site, combined.
		oracle := map[string]float64{}
		for _, st := range perRowSites {
			req := tc.build(cohort, perRowZoning{tz: st.zone, site: st.site})
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("%s oracle %s: %v", tc.name, st.site, err)
			}
			for k, v := range perRowFigures(t, resp, req.Groups) {
				oracle[k] += v
			}
		}
		if tc.reference != nil {
			requireFigures(t, tc.name+" oracle vs time.In reference", oracle, tc.reference(t, rows))
		}
		for _, zoning := range []struct {
			name string
			z    perRowZoning
		}{{"tz_field", perRowZoning{tzField: "tz"}}, {"time_zone_field", perRowZoning{reqField: "tz"}}} {
			if tc.crosstab {
				t.Run(tc.name+"/"+zoning.name+"/crosstab-fused", func(t *testing.T) {
					pr, err := p.Predict(ctx, tc.build(cohort, zoning.z))
					if err != nil || !pr.Valid || pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
						t.Fatalf("predict: the fused arm is not taken (err=%v)", err)
					}
					requireFigures(t, "fused", runners[0].run(t, tc.build(cohort, zoning.z)), oracle)
				})
			}
			for _, rn := range runners {
				if rn.name == "stream" && tc.crosstab {
					continue // a crosstab has no row stream
				}
				t.Run(tc.name+"/"+zoning.name+"/"+rn.name, func(t *testing.T) {
					path := cohort
					if rn.archive {
						path = archive
					}
					requireFigures(t, rn.name, rn.run(t, tc.build(path, zoning.z)), oracle)
				})
			}
		}
	}
}

// TestTimeZoneField_NullZoneCellIsNullLocalValue: a row whose zone cell
// is null has a null LOCAL value — never the UTC one. GROUP_DATE and
// GROUP_DATE_RANGES key it into no bucket (not even the unmatched one)
// and count it in the grouper's n_null; ATTR_DATE_PART and every
// FEAT_DATE_FEATURES column are null on it (the aggregation's n_null);
// FILTER_DATE_RANGES drops it, like a null date, and counts it in the
// slot's n_null_input. Every other row matches the time.In reference,
// on a single file, a shard archive (parallel and serial) and the fused
// crosstab.
func TestTimeZoneField_NullZoneCellIsNullLocalValue(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := perRowRows(7)
	cohort, archive := importPerRow(t, fs, "nullzone", rows)
	ctx := context.Background()
	nulls := 0
	for _, r := range rows {
		if r.zone == "" {
			nulls++
		}
	}
	if nulls == 0 || nulls == len(rows) {
		t.Fatalf("fixture: %d null zone cells of %d", nulls, len(rows))
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	pSerial, err := pulse.New(pulse.Options{FS: fs, ShardWorkers: 1, DecodeWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	z := perRowZoning{tzField: "tz"}
	cases := perRowCases()
	byName := map[string]perRowCase{}
	for _, c := range cases {
		byName[c.name] = c
	}
	for _, arm := range []struct {
		name string
		p    *pulse.Pulse
		path string
	}{{"single", p, cohort}, {"single-serial", pSerial, cohort}, {"archive", p, archive}, {"archive-serial", pSerial, archive}} {
		run := func(t *testing.T, name string) (*types.Response, map[string]float64) {
			t.Helper()
			req := byName[name].build(arm.path, z)
			resp, err := arm.p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			got := perRowFigures(t, resp, req.Groups)
			requireFigures(t, name, got, byName[name].reference(t, rows))
			return resp, got
		}
		t.Run(arm.name+"/groupers", func(t *testing.T) {
			for _, name := range []string{"site-day", "site-hour", "site-hour-of-day", "site-date-ranges"} {
				resp, _ := run(t, name)
				g := resp.Components.Groupers[1]
				if g.NNull != nulls || g.TotalN != len(rows)-nulls {
					t.Fatalf("%s grouper floor {total_n %d, n_null %d}, want {%d, %d}", name, g.TotalN, g.NNull, len(rows)-nulls, nulls)
				}
			}
		})
		t.Run(arm.name+"/attribute-and-features", func(t *testing.T) {
			resp, _ := run(t, "date-part-and-features")
			for _, a := range resp.Components.Aggregations {
				want := nulls
				if a.Label == "count" {
					want = 0
				}
				if a.NNull != want {
					t.Fatalf("aggregation %s n_null = %d, want %d", a.Label, a.NNull, want)
				}
			}
		})
		t.Run(arm.name+"/filter", func(t *testing.T) {
			resp, _ := run(t, "filter-date-ranges")
			f := resp.Components.Filterers[0]
			if f.NNullInput != nulls || f.NIn != len(rows) {
				t.Fatalf("filterer floor {n_in %d, n_null_input %d}, want {%d, %d}", f.NIn, f.NNullInput, len(rows), nulls)
			}
		})
	}
	t.Run("crosstab-fused", func(t *testing.T) {
		req := byName["crosstab-day-by-site"].build(cohort, z)
		pr, err := p.Predict(ctx, req)
		if err != nil || !pr.Valid || pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
			t.Fatalf("predict: the fused arm is not taken (err=%v)", err)
		}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		requireFigures(t, "fused", perRowFigures(t, resp, nil), byName["crosstab-day-by-site"].reference(t, rows))
	})
}
