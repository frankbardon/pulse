package synth

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The fidelity report reads a datetime's shape on the clock its spec
// generated on (#594, U76 E3-S3): the same zone or zone field capture
// read, never UTC behind a local capture.

// shapeStub gives every marginal a result so attachTimeShapes scores it;
// the shape never reads the test result itself.
func shapeStub(_, _ *encoding.Schema, _ []byte, _ *types.Test) (*types.TestResult, error) {
	return &types.TestResult{}, nil
}

// zonedTaggedCohort writes zoneCaptureSpec's source, captures it with
// opts, rebuilds the twin (mutate edits it first) and returns the tagged
// source-plus-twin cohort with the twin spec.
func zonedTaggedCohort(t *testing.T, opts ProfileOptions, mutate func(*Spec)) (*encoding.Schema, []byte, *Spec) {
	t.Helper()
	src := zoneCaptureSource(t)
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/src.pulse", src, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := ProfileBytes(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	twin, _ := SpecFromProfile(p, 4000)
	if mutate != nil {
		mutate(twin)
	}
	_, tagged, err := AugmentWithFidelityCohort(fs, twin, "/src.pulse", "/out.pulse", Options{Seed: 77})
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(tagged)
	s, _, err := encx.ReadPreamble(r)
	if err != nil {
		t.Fatal(err)
	}
	return s, tagged[len(tagged)-r.Len():], twin
}

func shapeOf(t *testing.T, r *FidelityReport, name string) *TimeShapeFidelity {
	t.Helper()
	for _, e := range r.Fields {
		if e.Field == name {
			if e.Shape == nil {
				t.Fatalf("%s: no shape", name)
			}
			return e.Shape
		}
	}
	t.Fatalf("%s: not in the report", name)
	return nil
}

// TestFidelityShape_ZoneFieldReadsTheLocalClock: a twin rebuilt from a
// per-row-zone capture scores small on the local clock and names it. Its
// zone mix is then skewed: every local reading stays an evening one, so
// the local shape stays small — but the UTC instants move with the mix,
// and the same cohort read on UTC is measurably worse. That gap is the
// clock having moved.
func TestFidelityShape_ZoneFieldReadsTheLocalClock(t *testing.T) {
	t.Parallel()
	schema, records, twin := zonedTaggedCohort(t, ProfileOptions{TimeZoneField: "site"}, nil)
	local := BuildFidelityReportForSpec(schema, records, 4000, 4000, shapeStub, twin)
	s := shapeOf(t, local, "ts")
	if s.Clock != TimeShapeClockLocal || s.TimeZoneField != "site" || s.TimeZone != "" {
		t.Errorf("clock %q zone %q field %q; want local on site", s.Clock, s.TimeZone, s.TimeZoneField)
	}
	if s.HourTVD > 0.05 || s.WeekdayTVD > 0.05 || s.MonthTVD > 0.05 {
		t.Errorf("faithful local shape %+v; want every tvd ≤ 0.05", s)
	}

	schema, records, twin = zonedTaggedCohort(t, ProfileOptions{TimeZoneField: "site"}, func(sp *Spec) {
		for i := range sp.Fields {
			if sp.Fields[i].Name == "site" {
				sp.Fields[i].Params = map[string]any{
					"values":  []any{"America/New_York", "Asia/Kolkata", "Australia/Sydney"},
					"weights": []any{0.9, 0.05, 0.05}}
			}
		}
	})
	skewed := shapeOf(t, BuildFidelityReportForSpec(schema, records, 4000, 4000, shapeStub, twin), "ts")
	onUTC := shapeOf(t, BuildFidelityReport(schema, records, 4000, 4000, shapeStub), "ts")
	if onUTC.Clock != TimeShapeClockUTC {
		t.Errorf("spec-free report clock %q, want utc", onUTC.Clock)
	}
	if skewed.HourTVD > 0.05 || onUTC.HourTVD < skewed.HourTVD+0.3 {
		t.Errorf("skewed-mix hour_tvd local %.4f, utc %.4f; want local ≤ 0.05 and utc ≥ local+0.3",
			skewed.HourTVD, onUTC.HourTVD)
	}
}

// TestFidelityShape_FixedZoneNamesTheZone: a fixed-zone capture's shape
// is read on that zone and says so.
func TestFidelityShape_FixedZoneNamesTheZone(t *testing.T) {
	t.Parallel()
	schema, records, twin := zonedTaggedCohort(t, ProfileOptions{TimeZone: "America/New_York"}, nil)
	s := shapeOf(t, BuildFidelityReportForSpec(schema, records, 4000, 4000, shapeStub, twin), "ny")
	if s.Clock != TimeShapeClockLocal || s.TimeZone != "America/New_York" || s.TimeZoneField != "" {
		t.Errorf("clock %q zone %q field %q; want local America/New_York", s.Clock, s.TimeZone, s.TimeZoneField)
	}
	if s.HourTVD > 0.05 || s.WeekdayTVD > 0.05 {
		t.Errorf("faithful fixed-zone shape %+v; want hour / weekday tvd ≤ 0.05", s)
	}
}

// TestFidelityShape_NoZoneIsByteIdentical: a spec naming no zone reads
// UTC and writes the report the spec-free call writes, byte for byte.
func TestFidelityShape_NoZoneIsByteIdentical(t *testing.T) {
	t.Parallel()
	schema, records, twin := zonedTaggedCohort(t, ProfileOptions{}, nil)
	a, _ := json.Marshal(BuildFidelityReport(schema, records, 4000, 4000, shapeStub))
	b, _ := json.Marshal(BuildFidelityReportForSpec(schema, records, 4000, 4000, shapeStub, twin))
	if !bytes.Equal(a, b) {
		t.Errorf("unzoned spec changes the report:\n%s\n%s", a, b)
	}
	if bytes.Contains(b, []byte(`"time_zone`)) || !bytes.Contains(b, []byte(`"clock":"utc"`)) {
		t.Errorf("unzoned report names a zone or no utc clock: %s", b)
	}
}

// TestFidelityShape_NullZoneCell: a source reading whose zone cell is
// null has no local clock — the capture left it out, and so does the
// report, with a warning carrying the per-partition count — never
// binned on UTC.
func TestFidelityShape_NullZoneCell(t *testing.T) {
	t.Parallel()
	schema, records, twin := zonedTaggedCohort(t, ProfileOptions{TimeZoneField: "alt_zone"}, nil)
	report := BuildFidelityReportForSpec(schema, records, 4000, 4000, shapeStub, twin)
	s := shapeOf(t, report, "ny")
	if s.Clock != TimeShapeClockLocal || s.TimeZoneField != "alt_zone" {
		t.Errorf("clock %q field %q; want local on alt_zone", s.Clock, s.TimeZoneField)
	}
	if s.HourTVD > 0.03 {
		t.Errorf("hour_tvd %.4f; want ≤ 0.03 with the unzoned readings left out", s.HourTVD)
	}
	warned := false
	for _, w := range report.Warnings {
		if strings.Contains(w, `"ny"`) && strings.Contains(w, "null zone") && !strings.Contains(w, " 0 source") &&
			strings.Contains(w, " 0 synthetic") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no null-zone warning with a source count in %v", report.Warnings)
	}
}
