package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func zoneSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "ts", Type: encoding.FieldTypeDateTime, Description: "Event timestamp in UTC seconds"},
		{Name: "d", Type: encoding.FieldTypeDate, Description: "Calendar date of the event"},
		{Name: "n", Type: encoding.FieldTypeF64, Description: "Numeric measurement value"},
	}}
}

func predictZones(t *testing.T, req *types.Request, defaultZone string) *descriptor.Envelope {
	t.Helper()
	data := buildTestPulseFile(t, zoneSchema())
	return predictFromBytes(data, req, &PredictOptions{DefaultTimeZone: defaultZone})
}

// TestPredictTimeZones_Precedence walks every precedence source —
// slot → request → options → default — through the predict echo. The
// distinct names are all UTC-equivalent so none is refused, and each
// echo keeps the caller's spelling.
func TestPredictTimeZones_Precedence(t *testing.T) {
	cases := []struct {
		name                  string
		slot, request, option string
		wantTZ, wantSource    string
	}{
		{"slot beats all", "Etc/Zulu", "Etc/UCT", "Etc/GMT", "Etc/Zulu", "slot"},
		{"request beats options", "", "Etc/UCT", "Etc/GMT", "Etc/UCT", "request"},
		{"options beats default", "", "", "Etc/GMT", "Etc/GMT", "options"},
		{"default is UTC", "", "", "", "UTC", "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &types.Request{
				TimeZone:     tc.request,
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}},
				Groups:       []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZone: tc.slot}},
			}
			env := predictZones(t, req, tc.option)
			if len(env.Errors) != 0 {
				t.Fatalf("errors: %+v", env.Errors[0])
			}
			got := env.Data.(*descriptor.PredictResult).TimeZones
			if len(got) != 1 {
				t.Fatalf("TimeZones = %+v, want one entry", got)
			}
			z := got[0]
			if z.Slot != "groups[0]" || z.Operator != "GROUP_DATE" || z.FieldType != "datetime" ||
				z.TZ == nil || *z.TZ != tc.wantTZ || z.Source != tc.wantSource {
				t.Fatalf("got %+v (tz %v), want tz %s source %s", z, z.TZ, tc.wantTZ, tc.wantSource)
			}
		})
	}
}

// TestPredictTimeZones_EchoShape pins the JSON of the echo: request
// order filterers → features → attributes → groups, a null tz for an
// inherited zone on a date field, and an empty array (never null) for
// a request with no zone-capable slot.
func TestPredictTimeZones_EchoShape(t *testing.T) {
	req := &types.Request{
		TimeZone:     "Etc/UTC",
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}},
		Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: json.RawMessage(`{"ranges":[{"label":"a","start":"2024-01-01"}]}`)}},
		Attributes:   []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: "d", Label: "yr", Params: json.RawMessage(`{"part":"year"}`)}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "yr"}},
	}
	env := predictZones(t, req, "")
	if len(env.Errors) != 0 {
		t.Fatalf("errors: %+v", env.Errors[0])
	}
	b, _ := json.Marshal(env.Data.(*descriptor.PredictResult).TimeZones)
	want := `[{"slot":"filterers[0]","operator":"FILTER_DATE_RANGES","field_type":"datetime","tz":"Etc/UTC","source":"request"},` +
		`{"slot":"attributes[0]","operator":"ATTR_DATE_PART","field_type":"date","tz":null,"source":"request"}]`
	if string(b) != want {
		t.Fatalf("echo\n got %s\nwant %s", b, want)
	}

	env = predictZones(t, &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}}}, "")
	b, _ = json.Marshal(env.Data.(*descriptor.PredictResult).TimeZones)
	if string(b) != `[]` {
		t.Fatalf("zone-free echo = %s, want []", b)
	}
}

// TestPredictTimeZones_RefusalsAreErrors: every runtime refusal surfaces
// as a predict error under the runtime's own code.
func TestPredictTimeZones_RefusalsAreErrors(t *testing.T) {
	agg := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}}
	cases := []struct {
		name string
		req  *types.Request
		opt  string
		code errors.Code
	}{
		{"non-UTC slot on datetime", &types.Request{Aggregations: agg,
			Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZone: "Europe/Berlin"}}}, "", errors.PROCESSING_CONFIG},
		{"non-UTC options on datetime", &types.Request{Aggregations: agg,
			Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts"}}}, "Asia/Tokyo", errors.PROCESSING_CONFIG},
		{"non-UTC on derived column", &types.Request{Aggregations: agg, TimeZone: "Europe/Berlin",
			Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "not_in_schema"}}}, "", errors.PROCESSING_CONFIG},
		{"explicit tz on date", &types.Request{Aggregations: agg,
			Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "d", TimeZone: "UTC"}}}, "", errors.PROCESSING_CONFIG},
		{"tz on non-capable", &types.Request{Aggregations: agg,
			Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "n", Interval: 10, TimeZone: "UTC"}}}, "", errors.PROCESSING_CONFIG},
		{"unknown request zone", &types.Request{Aggregations: agg, TimeZone: "EST5EDT"}, "", errors.PULSE_TIMEZONE_UNKNOWN},
		{"unknown slot zone", &types.Request{Aggregations: agg,
			Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZone: "Nowhere/Land"}}}, "", errors.PULSE_TIMEZONE_UNKNOWN},
		{"unknown options zone", &types.Request{Aggregations: agg}, "+05:00", errors.PULSE_TIMEZONE_UNKNOWN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := predictZones(t, tc.req, tc.opt)
			if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) {
				t.Fatalf("errors = %+v, want first %s", env.Errors, tc.code)
			}
			if env.Data.(*descriptor.PredictResult).Valid {
				t.Fatal("Valid = true on a refused request")
			}
		})
	}
}

// TestResolveZones_RefusalDetails pins the U03 refusal's details keys.
func TestResolveZones_RefusalDetails(t *testing.T) {
	req := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "n"}},
		Columns: []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "ts"}},
	}, TimeZone: "America/New_York"}
	_, err := ResolveZones(req, zoneSchema(), "", nil, nil)
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PROCESSING_CONFIG {
		t.Fatalf("err = %v", err)
	}
	want := map[string]any{"slot": "crosstab.columns[0]", "operator": "GROUP_DATE_RANGES", "tz": "America/New_York"}
	if len(ce.Details) != len(want) {
		t.Fatalf("details = %v, want %v", ce.Details, want)
	}
	for k, v := range want {
		if ce.Details[k] != v {
			t.Fatalf("details = %v, want %v", ce.Details, want)
		}
	}

	if zs, err := ResolveZones(nil, nil, "", nil, nil); err != nil || zs == nil || len(zs) != 0 {
		t.Fatalf("nil request: %v %v", zs, err)
	}
	if zs, err := ResolveFacetZones(nil, nil, "", nil, nil); err != nil || zs == nil || len(zs) != 0 {
		t.Fatalf("nil facet request: %v %v", zs, err)
	}
	fz, err := ResolveFacetZones(&types.FacetRequest{Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts"}}}, zoneSchema(), "Etc/UTC", nil, nil)
	if err != nil || len(fz) != 1 || fz[0].Source != "options" || *fz[0].TZ != "Etc/UTC" {
		t.Fatalf("facet resolve = %+v, %v", fz, err)
	}
}

// TestResolveZones_EmptyOperatorSkipped: a slot with no operator Type
// is a type error, not a zone error — the resolver leaves it to type
// validation even when it carries a `tz`.
func TestResolveZones_EmptyOperatorSkipped(t *testing.T) {
	req := &types.Request{
		Groups:    []*types.Group{{Field: "ts", TimeZone: "Europe/Berlin"}},
		Filterers: []*types.Filterer{{Field: "d", TimeZone: "UTC"}},
	}
	zs, err := ResolveZones(req, zoneSchema(), "", nil, nil)
	if err != nil || len(zs) != 0 {
		t.Fatalf("ResolveZones = %+v, %v; want no slot and no refusal", zs, err)
	}
}

// TestResolveZones_NilSchemaFieldIndependentOnly: with no schema the
// field-dependent refusals are not decided (a schema-holding runtime
// may accept), while the field-independent ones still fire.
func TestResolveZones_NilSchemaFieldIndependentOnly(t *testing.T) {
	inherited := &types.Request{TimeZone: "Asia/Tokyo", Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "d"}}}
	if _, err := ResolveZones(inherited, nil, "", nil, nil); err != nil {
		t.Fatalf("schema-less inherited zone refused: %v", err)
	}
	explicit := &types.Request{Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "d", TimeZone: "UTC"}}}
	if _, err := ResolveZones(explicit, nil, "", nil, nil); err != nil {
		t.Fatalf("schema-less explicit tz refused: %v", err)
	}
	for name, req := range map[string]*types.Request{
		"non-capable":  {Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "n", TimeZone: "UTC"}}},
		"unknown slot": {Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", TimeZone: "Mars/Base"}}},
		"unknown req":  {TimeZone: "EST"},
	} {
		if _, err := ResolveZones(req, nil, "", nil, nil); err == nil {
			t.Errorf("%s: schema-less resolution accepted a field-independent refusal", name)
		}
	}
}

// TestRefusalAt: the location key joins the copied details; code
// and message are unchanged and the input is not mutated.
func TestRefusalAt(t *testing.T) {
	src := errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, "m", map[string]any{"slot": "groups[0]"})
	got := RefusalAt(src, "stage", 2)
	ce, ok := got.(*errors.CodedError)
	if !ok || ce.Code != errors.PROCESSING_CONFIG || ce.Message != "m" || ce.Details["stage"] != 2 || ce.Details["slot"] != "groups[0]" {
		t.Fatalf("RefusalAt = %#v", got)
	}
	if _, leaked := src.Details["stage"]; leaked {
		t.Fatal("RefusalAt mutated its input")
	}
	plain := json.Unmarshal([]byte("{"), new(any))
	if RefusalAt(plain, "stage", 0) != plain {
		t.Fatal("an uncoded error must pass through unchanged")
	}
}

// TestPredict_EmptyTypeRefused: predict reports a slot still without a
// Type with the runtime's own PROCESSING_CONFIG message, and under
// DisableDefaults validates the request as written (DefaultsApplied
// still lists what would apply).
func TestPredict_EmptyTypeRefused(t *testing.T) {
	data := buildTestPulseFile(t, zoneSchema())
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}},
		Groups: []*types.Group{{Field: "ts"}}}
	if env := predictFromBytes(data, req, &PredictOptions{}); len(env.Errors) != 0 {
		t.Fatalf("defaults on: errors %+v", env.Errors)
	}
	env := predictFromBytes(data, req, &PredictOptions{DisableDefaults: true})
	if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PROCESSING_CONFIG) ||
		env.Errors[0].Message != "unknown group type: " || env.Errors[0].Details["slot"] != "groups[0]" {
		t.Fatalf("DisableDefaults errors = %+v", env.Errors)
	}
	if pr := env.Data.(*descriptor.PredictResult); len(pr.DefaultsApplied) != 1 || pr.Valid {
		t.Fatalf("DefaultsApplied = %+v valid=%v", pr.DefaultsApplied, pr.Valid)
	}
	if req.Groups[0].Type != "" {
		t.Fatal("predict mutated the caller's request")
	}
}
