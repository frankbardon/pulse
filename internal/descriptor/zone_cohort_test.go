package descriptor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// cohortZoneSchema is zoneFieldSchema declaring "zone" the cohort's
// default zone field.
func cohortZoneSchema(t *testing.T) *encoding.Schema {
	s := zoneFieldSchema(t)
	s.ZoneField = "zone"
	return s
}

// TestResolveZones_CohortDefaultPrecedence pins the cohort default's
// rung, case by case: slot `tz` > slot `tz_field` > request
// `time_zone_field` > request `time_zone` > cohort default field >
// Options.DefaultTimeZone > UTC. Each case is a pair of adjacent rungs
// set together; the higher one wins.
func TestResolveZones_CohortDefaultPrecedence(t *testing.T) {
	t.Parallel()

	berlin, tokyo := "Europe/Berlin", "Asia/Tokyo"
	cases := []struct {
		name                 string
		cohort               string
		slotTZ, slotField    string
		reqField, reqTZ, def string
		field                string
		wantTZ               *string
		wantField, wantSrc   string
	}{
		{"slot tz beats cohort", "zone", "Europe/Berlin", "", "", "", "", "ts", &berlin, "", descriptor.ZoneSourceSlot},
		{"slot tz_field beats cohort", "zone", "", "zone2", "", "", "", "ts", nil, "zone2", descriptor.ZoneSourceField},
		{"request field beats cohort", "zone", "", "", "zone2", "", "", "ts", nil, "zone2", descriptor.ZoneSourceField},
		{"request zone beats cohort", "zone", "", "", "", "Asia/Tokyo", "", "ts", &tokyo, "", descriptor.ZoneSourceRequest},
		{"cohort beats options", "zone", "", "", "", "", "Asia/Tokyo", "ts", nil, "zone", descriptor.ZoneSourceCohort},
		{"cohort beats UTC", "zone", "", "", "", "", "", "ts", nil, "zone", descriptor.ZoneSourceCohort},
		{"undeclared: options", "", "", "", "", "", "Asia/Tokyo", "ts", &tokyo, "", descriptor.ZoneSourceOptions},
		{"cohort on a date is not applied", "zone", "", "", "", "", "Asia/Tokyo", "d", nil, "zone", descriptor.ZoneSourceCohort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := zoneFieldSchema(t)
			s.ZoneField = tc.cohort
			req := &types.Request{TimeZone: tc.reqTZ, TimeZoneField: tc.reqField,
				Groups: []*types.Group{{Type: types.GROUP_DATE, Field: tc.field, TimeZone: tc.slotTZ, TimeZoneField: tc.slotField}}}
			zs, err := ResolveZones(req, s, tc.def, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(zs) != 1 {
				t.Fatalf("zones = %+v", zs)
			}
			z := zs[0]
			if (z.TZ == nil) != (tc.wantTZ == nil) || (z.TZ != nil && *z.TZ != *tc.wantTZ) || z.TZField != tc.wantField || z.Source != tc.wantSrc {
				t.Fatalf("got %+v (tz %v), want tz %v tz_field %q source %s", z, z.TZ, tc.wantTZ, tc.wantField, tc.wantSrc)
			}
		})
	}
}

// TestResolveZones_CohortDefaultInheritance: the cohort default inherits
// like `time_zone_field` — a non-capable operator neither echoes nor is
// refused, a nil schema (no cohort in reach) skips it and never
// refuses, the facet pass honours it, and a derived slot field is
// refused with the override advice.
func TestResolveZones_CohortDefaultInheritance(t *testing.T) {
	t.Parallel()

	t.Run("non-capable operator", func(t *testing.T) {
		req := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "n"}}}
		zs, err := ResolveZones(req, cohortZoneSchema(t), "", nil, nil)
		if err != nil || len(zs) != 0 {
			t.Fatalf("zones = %+v, err = %v", zs, err)
		}
	})
	t.Run("nil schema", func(t *testing.T) {
		req := &types.Request{Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "derived_ts"}}}
		zs, err := ResolveZones(req, nil, "", nil, nil)
		if err != nil || len(zs) != 1 || zs[0].Source != descriptor.ZoneSourceDefault {
			t.Fatalf("zones = %+v, err = %v", zs, err)
		}
	})
	t.Run("facet", func(t *testing.T) {
		req := &types.FacetRequest{Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts"}}}
		zs, err := ResolveFacetZones(req, cohortZoneSchema(t), "Asia/Tokyo", nil, nil)
		if err != nil || len(zs) != 1 || zs[0].Source != descriptor.ZoneSourceCohort || zs[0].TZField != "zone" {
			t.Fatalf("zones = %+v, err = %v", zs, err)
		}
	})
	t.Run("derived slot field", func(t *testing.T) {
		req := &types.Request{Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "derived_ts"}}}
		_, err := ResolveZones(req, cohortZoneSchema(t), "", nil, nil)
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PROCESSING_CONFIG || !strings.Contains(ce.Message, `request "time_zone"`) {
			t.Fatalf("err = %v", err)
		}
		got, _ := json.Marshal(ce.Details)
		if string(got) != `{"operator":"GROUP_DATE","slot":"groups[0]","tz_field":"zone"}` {
			t.Fatalf("details = %s", got)
		}
		// A request zone overrides the default, so the slot is accepted.
		req.TimeZone = "UTC"
		if _, err := ResolveZones(req, cohortZoneSchema(t), "", nil, nil); err != nil {
			t.Fatalf("request UTC override refused: %v", err)
		}
	})
}

// TestZonedRequest_CohortDefaultWritesTZField: the runtime carries a
// cohort-default zone onto the executing slot exactly as an inherited
// `time_zone_field` — written into `tz_field` on a datetime slot, never
// onto a calendar-date slot — and the caller's request is not mutated.
func TestZonedRequest_CohortDefaultWritesTZField(t *testing.T) {
	t.Parallel()

	req := &types.Request{Groups: []*types.Group{
		{Type: types.GROUP_DATE, Field: "ts"},
		{Type: types.GROUP_DATE, Field: "d"},
	}}
	zs, err := ResolveZones(req, cohortZoneSchema(t), "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := ZonedRequest(req, zs, nil)
	if got == req || got.Groups[0].TimeZoneField != "zone" || got.Groups[1].TimeZoneField != "" {
		t.Fatalf("zoned groups = %+v / %+v", got.Groups[0], got.Groups[1])
	}
	if req.Groups[0].TimeZoneField != "" {
		t.Fatal("caller's request mutated")
	}
}

// TestPredictTimeZones_CohortDefaultEcho: predict reads the default out
// of the cohort header and echoes source "cohort".
func TestPredictTimeZones_CohortDefaultEcho(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := encx.WritePreamble(&buf, cohortZoneSchema(t)); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}},
		Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"month"}`)}}}
	env := predictFromBytes(data, req, &PredictOptions{DefaultTimeZone: "Asia/Tokyo"})
	if len(env.Errors) != 0 {
		t.Fatalf("errors: %+v", env.Errors)
	}
	zs := env.Data.(*descriptor.PredictResult).TimeZones
	if len(zs) != 1 || zs[0].Source != descriptor.ZoneSourceCohort || zs[0].TZField != "zone" || zs[0].TZ != nil {
		t.Fatalf("echo = %+v", zs)
	}
}

// TestJoinedSchema_CarriesLeftZoneDefault: a join carries the LEFT
// cohort's default zone field; the right cohort's is never inherited.
func TestJoinedSchema_CarriesLeftZoneDefault(t *testing.T) {
	t.Parallel()

	left, right := cohortZoneSchema(t), cohortZoneSchema(t)
	right.ZoneField = "zone2"
	j, err := encx.JoinedSchema(left, right, "r_")
	if err != nil {
		t.Fatal(err)
	}
	if j.ZoneField != "zone" {
		t.Fatalf("joined ZoneField = %q, want the left's %q", j.ZoneField, "zone")
	}
	left.ZoneField = ""
	if j, err = encx.JoinedSchema(left, right, "r_"); err != nil || j.ZoneField != "" {
		t.Fatalf("right default inherited: %q (err %v)", j.ZoneField, err)
	}
}
