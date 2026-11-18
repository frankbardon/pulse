//go:build heavy

package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/spf13/afero"
)

// TestTimeZoneCohort_RunEqualsTimeZoneFieldEveryRunner: every
// per-row-oracle case over the declared cohort equals the same request
// with `time_zone_field` over the undeclared cohort, in every
// single-file execution mode (projected / full / serial buffered
// decode, streaming, both crosstab arms); the fixture's local days
// differ from its UTC days, so a default that fails to reach an arm
// (or is dropped by the projection) fails here. The shard-archive
// runners read a two-shard archive declared through
// Pulse.SetCohortZoneField, so the default reaches both shard arms (and
// inspect reports it) after the whole-archive rewrite.
func TestTimeZoneCohort_RunEqualsTimeZoneFieldEveryRunner(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := perRowRows(0)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "cz.csv", []byte(perRowCSV(rows)), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "cz.csv"})
	if err != nil {
		t.Fatal(err)
	}
	plain := res.Path
	declared := declareZoneField(t, fs, plain, "cz_declared.pulse", "tz")
	_, archive := importPerRow(t, fs, "cza", rows)
	set, err := p.SetCohortZoneField(context.Background(), archive, "tz")
	if err != nil {
		t.Fatalf("SetCohortZoneField(archive): %v", err)
	}
	if !set.Changed || set.Layout != "shard_archive" || set.ShardsRewritten != 2 {
		t.Fatalf("archive set = %+v", set)
	}
	ins, err := p.Inspect(context.Background(), archive)
	if err != nil {
		t.Fatal(err)
	}
	if ins.ZoneField != "tz" {
		t.Fatalf("archive inspect zone_field = %q, want tz", ins.ZoneField)
	}
	runners := perRowRunners(t, fs)
	for _, tc := range perRowCases() {
		want := runFigures(t, p, tc.build(plain, perRowZoning{reqField: "tz"}))
		if tc.reference != nil {
			requireFigures(t, tc.name+" time_zone_field vs time.In reference", want, tc.reference(t, rows))
		}
		for _, z := range zoneEcho(t, p, fs, declared, tc.build(declared, perRowZoning{})) {
			if z.Source != descriptor.ZoneSourceCohort || z.TZField != "tz" {
				t.Fatalf("%s: predict echo %+v, want source cohort tz_field tz", tc.name, z)
			}
		}
		for _, rn := range runners {
			if rn.name == "stream" && tc.crosstab {
				continue
			}
			path := declared
			if rn.archive {
				path = archive
			}
			requireFigures(t, tc.name+"/"+rn.name, rn.run(t, tc.build(path, perRowZoning{})), want)
		}
	}
}
