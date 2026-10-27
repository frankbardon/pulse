package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func zoneDict(t *testing.T, entries ...string) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for _, e := range entries {
		if _, err := d.Add(e); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// zoneFieldSchema is zoneSchema plus three categorical zone columns: a
// valid one (zone), a second valid one (zone2) and one whose second
// dictionary entry is no zone (badzone).
func zoneFieldSchema(t *testing.T) *encoding.Schema {
	s := zoneSchema()
	s.Fields = append(s.Fields,
		encoding.Field{Name: "zone", Type: encoding.FieldTypeCategoricalU8, Dictionary: zoneDict(t, "America/New_York", "Europe/Berlin", "UTC"), Description: "Site IANA time zone"},
		encoding.Field{Name: "zone2", Type: encoding.FieldTypeCategoricalU16, Dictionary: zoneDict(t, "Asia/Tokyo"), Description: "Other site IANA time zone"},
		encoding.Field{Name: "badzone", Type: encoding.FieldTypeCategoricalU8, Dictionary: zoneDict(t, "Europe/Berlin", "Mars/Base"), Description: "A zone column with a bad entry"},
	)
	return s
}

// TestResolveZones_FieldPrecedence walks slot `tz` → slot `tz_field` →
// request `time_zone_field` → request `time_zone` through the echo: a
// field source echoes source "field", tz_field and a null tz.
func TestResolveZones_FieldPrecedence(t *testing.T) {
	t.Parallel()

	berlin := "Europe/Berlin"
	cases := []struct {
		name                 string
		slotTZ, slotField    string
		reqField, reqTZ, def string
		field                string
		wantTZ               *string
		wantField, wantSrc   string
	}{
		{"slot tz beats every field", "Europe/Berlin", "", "zone", "Asia/Tokyo", "", "ts", &berlin, "", descriptor.ZoneSourceSlot},
		{"slot tz_field", "", "zone", "", "Asia/Tokyo", "Asia/Tokyo", "ts", nil, "zone", descriptor.ZoneSourceField},
		{"slot tz_field beats request field", "", "zone", "zone2", "", "", "ts", nil, "zone", descriptor.ZoneSourceField},
		{"request field beats request zone", "", "", "zone2", "Asia/Tokyo", "Asia/Tokyo", "ts", nil, "zone2", descriptor.ZoneSourceField},
		{"inherited field on a date is not applied", "", "", "zone", "Asia/Tokyo", "", "d", nil, "zone", descriptor.ZoneSourceField},
		{"inherited bad field on a date is not judged", "", "", "badzone", "", "", "d", nil, "badzone", descriptor.ZoneSourceField},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &types.Request{TimeZone: tc.reqTZ, TimeZoneField: tc.reqField,
				Groups: []*types.Group{{Type: types.GROUP_DATE, Field: tc.field, TimeZone: tc.slotTZ, TimeZoneField: tc.slotField}}}
			zs, err := ResolveZones(req, zoneFieldSchema(t), tc.def, nil, nil)
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

// TestResolveZones_FieldRefusals pins every per-row-zone refusal's code
// and details.
func TestResolveZones_FieldRefusals(t *testing.T) {
	t.Parallel()

	grp := func(g types.Group) *types.Request { return &types.Request{Groups: []*types.Group{&g}} }
	cases := []struct {
		name string
		req  *types.Request
		code errors.Code
		want map[string]any
	}{
		{"tz and tz_field on one slot", grp(types.Group{Type: types.GROUP_DATE, Field: "ts", TimeZone: "UTC", TimeZoneField: "zone"}),
			errors.PROCESSING_CONFIG, map[string]any{"slot": "groups[0]", "operator": "GROUP_DATE", "tz": "UTC", "tz_field": "zone"}},
		{"tz_field on a non-capable operator", grp(types.Group{Type: types.GROUP_CATEGORY, Field: "n", TimeZoneField: "zone"}),
			errors.PROCESSING_CONFIG, map[string]any{"slot": "groups[0]", "operator": "GROUP_CATEGORY", "tz_field": "zone"}},
		{"tz_field on a date field", grp(types.Group{Type: types.GROUP_DATE, Field: "d", TimeZoneField: "zone"}),
			errors.PROCESSING_CONFIG, map[string]any{"slot": "groups[0]", "operator": "GROUP_DATE", "tz_field": "zone", "field": "d", "field_type": "date"}},
		{"zone field not categorical", grp(types.Group{Type: types.GROUP_DATE, Field: "ts", TimeZoneField: "n"}),
			errors.PROCESSING_CONFIG, map[string]any{"slot": "groups[0]", "operator": "GROUP_DATE", "tz_field": "n", "field": "n", "field_type": "f64"}},
		{"zone field entry not a zone", grp(types.Group{Type: types.GROUP_DATE, Field: "ts", TimeZoneField: "badzone"}),
			errors.PULSE_TIMEZONE_UNKNOWN, map[string]any{"slot": "groups[0]", "field": "badzone", "tz": "Mars/Base", "dictionary_index": 1}},
		{"inherited zone field entry not a zone", &types.Request{TimeZoneField: "badzone",
			Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts"}}},
			errors.PULSE_TIMEZONE_UNKNOWN, map[string]any{"slot": "filterers[0]", "field": "badzone", "tz": "Mars/Base", "dictionary_index": 1}},
		{"zone field onto a derived field", grp(types.Group{Type: types.GROUP_DATE, Field: "derived_ts", TimeZoneField: "zone"}),
			errors.PROCESSING_CONFIG, map[string]any{"slot": "groups[0]", "operator": "GROUP_DATE", "tz_field": "zone"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveZones(tc.req, zoneFieldSchema(t), "", nil, nil)
			ce, ok := err.(*errors.CodedError)
			if !ok || ce.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			got, _ := json.Marshal(ce.Details)
			want, _ := json.Marshal(tc.want)
			if string(got) != string(want) {
				t.Fatalf("details = %s, want %s", got, want)
			}
		})
	}
}

// TestResolveZones_FieldNilSchemaAndMissing: with no schema the
// field-dependent checks are skipped (the field echo still names the
// field); a zone field absent from a known schema is left to the
// field-reference pass, which refuses it.
func TestResolveZones_FieldNilSchemaAndMissing(t *testing.T) {
	t.Parallel()

	req := &types.Request{Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "d", TimeZoneField: "badzone"}}}
	zs, err := ResolveZones(req, nil, "", nil, nil)
	if err != nil || len(zs) != 1 || zs[0].TZField != "badzone" || zs[0].Source != descriptor.ZoneSourceField {
		t.Fatalf("schema-less: %+v, %v", zs, err)
	}
	// The field-independent refusals still fire schema-less.
	both := &types.Request{Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZone: "UTC", TimeZoneField: "zone"}}}
	if _, err := ResolveZones(both, nil, "", nil, nil); err == nil {
		t.Fatal("schema-less resolution accepted tz + tz_field")
	}
	missing := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}},
		Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZoneField: "nope"}}}
	if _, err := ResolveZones(missing, zoneFieldSchema(t), "", nil, nil); err != nil {
		t.Fatalf("missing zone field refused by the zone pass (the field-reference pass owns it): %v", err)
	}
	refs := FieldRefRefusals(missing, zoneFieldSchema(t), nil)
	if len(refs) != 1 || refs[0].Code != errors.SERVICE_VALIDATION || refs[0].Details["field"] != "nope" || refs[0].Details["slot"] != "groups[0].tz_field" {
		t.Fatalf("field refs = %+v", refs)
	}
}

// TestFieldRefRefusals_ZoneFields: every zone field reference — a
// slot's tz_field and the request's time_zone_field — must name a
// cohort field (a derived column carries no zone dictionary).
func TestFieldRefRefusals_ZoneFields(t *testing.T) {
	t.Parallel()

	agg := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}}
	for name, tc := range map[string]struct {
		req  *types.Request
		slot string
		f    string
	}{
		"request":     {&types.Request{Aggregations: agg, TimeZoneField: "nope"}, "time_zone_field", "nope"},
		"filterer":    {&types.Request{Aggregations: agg, Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", TimeZoneField: "nope"}}}, "filterers[0].tz_field", "nope"},
		"feature":     {&types.Request{Aggregations: agg, Features: []*types.Feature{{Type: types.FEAT_DATE_FEATURES, Field: "ts", TimeZoneField: "nope"}}}, "features[0].tz_field", "nope"},
		"crosstab":    {&types.Request{Crosstab: &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZoneField: "nope"}}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "zone"}}}}, "crosstab.rows[0].tz_field", "nope"},
		"derived col": {&types.Request{Aggregations: agg, Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "z2", Expression: "zone"}, {Type: types.ATTR_DATE_PART, Field: "ts", Label: "yr", Params: json.RawMessage(`{"part":"year"}`), TimeZoneField: "z2"}}}, "attributes[1].tz_field", "z2"},
	} {
		t.Run(name, func(t *testing.T) {
			refs := FieldRefRefusals(tc.req, zoneFieldSchema(t), nil)
			found := false
			for _, r := range refs {
				if r.Code == errors.SERVICE_VALIDATION && r.Details["slot"] == tc.slot && r.Details["field"] == tc.f {
					found = true
				}
			}
			if !found {
				t.Fatalf("refusals = %+v, want %s -> %s", refs, tc.slot, tc.f)
			}
		})
	}
	ok := &types.Request{Aggregations: agg, TimeZoneField: "zone", Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZoneField: "zone2"}}}
	if refs := FieldRefRefusals(ok, zoneFieldSchema(t), nil); len(refs) != 0 {
		t.Fatalf("cohort zone fields refused: %+v", refs)
	}
	facet := &types.FacetRequest{Fields: []string{"n"}, TimeZoneField: "nope"}
	if refs := FacetFieldRefRefusals(facet, zoneFieldSchema(t), nil); len(refs) != 1 || refs[0].Details["slot"] != "time_zone_field" {
		t.Fatalf("facet refusals = %+v", refs)
	}
}

// TestZonedRequest_ZoneField: an inherited request time_zone_field is
// written as tz_field onto a copy of exactly the datetime slots that
// resolved it (top-level, crosstab, MAT_DISTANCE between); a slot with
// its own tz / tz_field, a date slot and the input are untouched, and a
// request whose only field source is the slot's own returns itself.
func TestZonedRequest_ZoneField(t *testing.T) {
	t.Parallel()

	schema := zoneFieldSchema(t)
	req := &types.Request{
		TimeZone:      "Asia/Tokyo",
		TimeZoneField: "zone",
		Filterers:     []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts"}},
		Groups: []*types.Group{
			{Type: types.GROUP_DATE, Field: "d"},
			{Type: types.GROUP_DATE, Field: "ts", TimeZone: "Europe/Berlin"},
			{Type: types.GROUP_DATE, Field: "ts", TimeZoneField: "zone2"},
			{Type: types.GROUP_DATE, Field: "ts"},
		},
		Crosstab: &types.CrosstabSpec{Columns: []*types.Group{{Type: types.GROUP_DATE, Field: "ts"}}},
		Matrices: []types.MatrixSpec{{Type: types.MAT_DISTANCE, Fields: []string{"n"}, Params: json.RawMessage(`{"between":{"type":"GROUP_DATE","field":"ts"},"metric":"cosine"}`)}},
	}
	zs, err := ResolveZones(req, schema, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := ZonedRequest(req, zs, nil)
	if got == req {
		t.Fatal("no copy for an inherited zone field")
	}
	type want struct{ tz, field string }
	for i, w := range []want{{"", ""}, {"Europe/Berlin", ""}, {"", "zone2"}, {"", "zone"}} {
		if g := got.Groups[i]; g.TimeZone != w.tz || g.TimeZoneField != w.field {
			t.Errorf("groups[%d] = tz %q tz_field %q, want %+v", i, g.TimeZone, g.TimeZoneField, w)
		}
	}
	if got.Groups[0] != req.Groups[0] || got.Groups[1] != req.Groups[1] || got.Groups[2] != req.Groups[2] {
		t.Error("an unwritten slot was copied")
	}
	if got.Filterers[0].TimeZoneField != "zone" || got.Filterers[0].TimeZone != "" || got.Crosstab.Columns[0].TimeZoneField != "zone" {
		t.Errorf("filterer %+v, crosstab column %+v", got.Filterers[0], got.Crosstab.Columns[0])
	}
	var p struct {
		Between types.Group `json:"between"`
		Metric  string      `json:"metric"`
	}
	if err := json.Unmarshal(got.Matrices[0].Params, &p); err != nil {
		t.Fatal(err)
	}
	if p.Between.TimeZoneField != "zone" || p.Between.TimeZone != "" || p.Metric != "cosine" {
		t.Errorf("zoned between params %s", got.Matrices[0].Params)
	}
	if req.Filterers[0].TimeZoneField != "" || req.Groups[3].TimeZoneField != "" || req.Crosstab.Columns[0].TimeZoneField != "" {
		t.Error("the input request was mutated")
	}

	own := &types.Request{Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZoneField: "zone"}}}
	ozs, err := ResolveZones(own, schema, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ZonedRequest(own, ozs, nil) != own {
		t.Fatal("a slot-sourced zone field was rewritten")
	}

	freq := &types.FacetRequest{TimeZoneField: "zone", Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts"}}}
	fz, err := ResolveFacetZones(freq, schema, "", nil, nil)
	if err != nil || len(fz) != 1 || fz[0].Source != descriptor.ZoneSourceField {
		t.Fatalf("facet zones %+v, %v", fz, err)
	}
	if fgot := ZonedFacetRequest(freq, fz, nil); fgot == freq || fgot.Filterers[0].TimeZoneField != "zone" || freq.Filterers[0].TimeZoneField != "" {
		t.Fatalf("ZonedFacetRequest: %+v", fgot.Filterers[0])
	}
}

// TestPredictTimeZones_FieldEcho pins the predict JSON of a field
// source: tz null, tz_field named, source "field".
func TestPredictTimeZones_FieldEcho(t *testing.T) {
	t.Parallel()

	data := buildTestPulseFile(t, zoneFieldSchema(t))
	req := &types.Request{
		TimeZoneField: "zone",
		Aggregations:  []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}},
		Filterers:     []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: json.RawMessage(`{"ranges":[{"label":"a","start":"2024-01-01"}]}`)}},
		Groups:        []*types.Group{{Type: types.GROUP_DATE, Field: "d"}},
	}
	env := predictFromBytes(data, req, &PredictOptions{})
	if len(env.Errors) != 0 {
		t.Fatalf("errors: %+v", env.Errors)
	}
	b, _ := json.Marshal(env.Data.(*descriptor.PredictResult).TimeZones)
	want := `[{"slot":"filterers[0]","operator":"FILTER_DATE_RANGES","field_type":"datetime","tz":null,"tz_field":"zone","source":"field"},` +
		`{"slot":"groups[0]","operator":"GROUP_DATE","field_type":"date","tz":null,"tz_field":"zone","source":"field"}]`
	if string(b) != want {
		t.Fatalf("echo\n got %s\nwant %s", b, want)
	}
	req.TimeZoneField = "badzone"
	env = predictFromBytes(data, req, &PredictOptions{})
	if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_TIMEZONE_UNKNOWN) || env.Errors[0].Details["dictionary_index"] != float64(1) && env.Errors[0].Details["dictionary_index"] != 1 {
		t.Fatalf("errors = %+v", env.Errors)
	}
}
