//go:build heavy

package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestTimeZoneField_JoinPerRowEqualsSingleZoneSlots (#481): every
// per-row oracle case run through a join — the zone column on the left
// cohort, or on the right (`r_tz`, every zone-oracle field `r_`
// prefixed), the other side a wrong-zone decoy — with the zone named
// by a slot `tz_field` or the request `time_zone_field`, and through a
// left cohort DECLARING `tz` its default zone field with no request
// zone, equals the per-site single-`tz` runs over the plain cohort
// combined (and the time.In reference), in every runner — a join runs
// its own arm (processWithJoin / processCrosstabWithJoin), which the
// stream and shard entry points reach too, under each decode setting;
// predict echoes the joined zone field each run read. The shard
// runners put the zone cohort's two-shard archive on its side.
func TestTimeZoneField_JoinPerRowEqualsSingleZoneSlots(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := perRowRows(0)
	cohort, archive := importPerRow(t, fs, "jperrow", rows)
	decoy := importPerRowFile(t, fs, "jperrow_decoy", perRowDecoy(rows))
	declared := declareZoneField(t, fs, cohort, "jperrow_declared.pulse", "tz")
	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	runners := perRowRunners(t, fs)

	for _, tc := range perRowCases() {
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
		type zoning struct {
			name   string
			z      perRowZoning
			source string
			// left overrides the zone cohort (the declared twin).
			left string
		}
		for _, j := range perRowJoins {
			zonings := []zoning{
				{"tz_field", perRowZoning{tzField: "tz"}, descriptor.ZoneSourceField, ""},
				{"time_zone_field", perRowZoning{reqField: "tz"}, descriptor.ZoneSourceField, ""},
			}
			if j.zoneField == "tz" {
				zonings = append(zonings, zoning{"cohort-default", perRowZoning{}, descriptor.ZoneSourceCohort, declared})
			}
			for _, zn := range zonings {
				zoneSide := cohort
				if zn.left != "" {
					zoneSide = zn.left
				}
				mk := func(zone string) func() *types.Request {
					return func() *types.Request { return j.rewrite(tc.build("", zn.z), zone, decoy) }
				}
				requireJoinEcho(t, p, fs, mk(zoneSide)(), zn.source, j.zoneField)
				for _, rn := range runners {
					if rn.name == "stream" && tc.crosstab {
						continue
					}
					if rn.archive && zn.left != "" {
						continue // the declared twin is a single file
					}
					t.Run(tc.name+"/"+j.name+"/"+zn.name+"/"+rn.name, func(t *testing.T) {
						path := zoneSide
						if rn.archive {
							path = archive
						}
						requireFigures(t, rn.name, rn.run(t, mk(path)()), oracle)
					})
				}
			}
		}
	}
}

// TestTimeZoneField_JoinNullZoneCellIsNullLocalValue (#481): the zone
// side's null zone cells give a null local value through a join, on
// either side — every per-row case equals the time.In reference (which
// skips null-zone rows), and the date-part / feature columns count
// them in n_null — single file and shard archive.
func TestTimeZoneField_JoinNullZoneCellIsNullLocalValue(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := perRowRows(7)
	cohort, archive := importPerRow(t, fs, "jnull", rows)
	decoy := importPerRowFile(t, fs, "jnull_decoy", perRowDecoy(rows))
	nulls := 0
	for _, r := range rows {
		if r.zone == "" {
			nulls++
		}
	}
	if nulls == 0 || nulls == len(rows) {
		t.Fatalf("fixture: %d null zone cells of %d", nulls, len(rows))
	}
	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range perRowJoins {
		for _, side := range []struct{ name, path string }{{"single", cohort}, {"archive", archive}} {
			for _, tc := range perRowCases() {
				if tc.reference == nil {
					continue
				}
				t.Run(j.name+"/"+side.name+"/"+tc.name, func(t *testing.T) {
					req := j.rewrite(tc.build("", perRowZoning{tzField: "tz"}), side.path, decoy)
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					requireFigures(t, tc.name, perRowFigures(t, resp, req.Groups), tc.reference(t, rows))
					if tc.name != "date-part-and-features" {
						return
					}
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
			}
		}
	}
}
