package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestZonedExpressions: the inherited zone (request, else options
// default) is written onto exactly the expression slots that call
// to_day — a formula, an expression filter, an attribute whose params
// nest one — never onto a zone-capable or explicit-tz slot; a UTC,
// unknown or absent zone, or no to_day call, returns req itself; req is
// never mutated.
func TestZonedExpressions(t *testing.T) {
	mk := func() *types.Request {
		return &types.Request{
			TimeZone: "America/New_York",
			Filterers: []*types.Filterer{
				{Type: types.FILTER_EXPRESSION, Expression: "weekday(to_day(ts)) < 6"},
				{Type: types.FILTER_EXPRESSION, Expression: "x > 1"},
				{Type: types.FILTER_DATE_RANGES, Field: "ts"},
			},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Label: "a", Expression: "to_day(ts) - d"},
				{Type: types.ATTR_FORMULA, Label: "b", Expression: "x * 2"},
				{Type: types.ATTR_MAHALANOBIS, Label: "m", Fields: []string{"x", "y"},
					Params: json.RawMessage(`{"fit_on":[{"type":"FILTER_EXPRESSION","expression":"to_day(ts) == d"}]}`)},
				{Type: types.ATTR_DATE_PART, Label: "p", Field: "ts", Params: json.RawMessage(`{"part":"to_day"}`)},
				{Type: types.ATTR_FORMULA, Label: "c", Expression: "x / total(to_day(ts))"},
			},
		}
	}
	req := mk()
	got := ZonedExpressions(req, "", nil)
	if got == req {
		t.Fatal("a New York request returned req itself")
	}
	wantF := []string{"America/New_York", "", ""}
	for i, f := range got.Filterers {
		if f.TimeZone != wantF[i] {
			t.Errorf("filterers[%d].tz = %q, want %q", i, f.TimeZone, wantF[i])
		}
	}
	wantA := []string{"America/New_York", "", "America/New_York", "", "America/New_York"}
	for i, a := range got.Attributes {
		if a.TimeZone != wantA[i] {
			t.Errorf("attributes[%d].tz = %q, want %q", i, a.TimeZone, wantA[i])
		}
	}
	for i, f := range req.Filterers {
		if f.TimeZone != "" {
			t.Errorf("req.filterers[%d] mutated", i)
		}
	}
	for i, a := range req.Attributes {
		if a.TimeZone != "" {
			t.Errorf("req.attributes[%d] mutated", i)
		}
	}
	if got.Filterers[1] != req.Filterers[1] || got.Attributes[1] != req.Attributes[1] {
		t.Error("an untouched slot was copied")
	}

	// The options default applies when the request names none; the
	// request's zone wins over it.
	r2 := mk()
	r2.TimeZone = ""
	if g := ZonedExpressions(r2, "Asia/Tokyo", nil); g.Attributes[0].TimeZone != "Asia/Tokyo" {
		t.Errorf("default zone: tz = %q", g.Attributes[0].TimeZone)
	}
	if g := ZonedExpressions(mk(), "Asia/Tokyo", nil); g.Attributes[0].TimeZone != "America/New_York" {
		t.Errorf("request zone over default: tz = %q", g.Attributes[0].TimeZone)
	}

	// Identity cases.
	for name, r := range map[string]*types.Request{
		"utc":     {TimeZone: "UTC", Attributes: mk().Attributes},
		"etc utc": {TimeZone: "Etc/UTC", Attributes: mk().Attributes},
		"unknown": {TimeZone: "Mars/Olympus", Attributes: mk().Attributes},
		"absent":  {Attributes: mk().Attributes},
		"no to_day": {TimeZone: "America/New_York",
			Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "x"}},
			Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x > 0"}}},
	} {
		if g := ZonedExpressions(r, "", nil); g != r {
			t.Errorf("%s: want req itself", name)
		}
	}
	// An explicit slot tz is kept.
	r3 := mk()
	r3.Attributes[0].TimeZone = "Europe/Berlin"
	if g := ZonedExpressions(r3, "", nil); g.Attributes[0].TimeZone != "Europe/Berlin" {
		t.Errorf("explicit tz overwritten: %q", g.Attributes[0].TimeZone)
	}
	if ZonedExpressions(nil, "UTC", nil) != nil {
		t.Error("nil request")
	}
}

// TestZonedFacetExpressions: the facet twin writes the inherited zone
// onto its to_day filterers only.
func TestZonedFacetExpressions(t *testing.T) {
	req := &types.FacetRequest{TimeZone: "America/New_York", Filterers: []*types.Filterer{
		{Type: types.FILTER_EXPRESSION, Expression: "to_day(ts) == d"},
		{Type: types.FILTER_EXPRESSION, Expression: "x > 0"},
	}}
	got := ZonedFacetExpressions(req, "", nil)
	if got == req || got.Filterers[0].TimeZone != "America/New_York" || got.Filterers[1].TimeZone != "" || req.Filterers[0].TimeZone != "" {
		t.Fatalf("facet zoning: %+v / %+v", got.Filterers[0], got.Filterers[1])
	}
	req.TimeZone = ""
	if ZonedFacetExpressions(req, "", nil) != req {
		t.Error("zone-free facet request was copied")
	}
	if ZonedFacetExpressions(nil, "", nil) != nil {
		t.Error("nil facet request")
	}
}

// TestExpressionZone: the zone a to_day-calling expression reads —
// request zone, else default — and "" for an expression that never
// calls to_day or a UTC-equivalent, unknown or absent zone.
func TestExpressionZone(t *testing.T) {
	for _, c := range []struct {
		src, req, def, want string
	}{
		{"to_day(ts) == d", "", "America/New_York", "America/New_York"},
		{"to_day(ts) == d", "Asia/Tokyo", "America/New_York", "Asia/Tokyo"},
		{"weekday(to_day(ts)) < 6", "", "Asia/Tokyo", "Asia/Tokyo"},
		{"x > 1", "", "America/New_York", ""},
		{"to_day(ts) == d", "", "", ""},
		{"to_day(ts) == d", "", "Etc/UTC", ""},
		{"to_day(ts) == d", "", "Not/AZone", ""},
		{"", "", "America/New_York", ""},
	} {
		if got := ExpressionZone(c.src, c.req, c.def, nil); got != c.want {
			t.Errorf("ExpressionZone(%q, %q, %q) = %q, want %q", c.src, c.req, c.def, got, c.want)
		}
	}
}

// TestZonedExpressions_TargetEncodeFitOn: a feature that is not
// zone-capable whose params carry a to_day expression
// (FEAT_TARGET_ENCODE params.fit_on) inherits the zone; one without
// to_day, or with an explicit tz, does not.
func TestZonedExpressions_TargetEncodeFitOn(t *testing.T) {
	req := &types.Request{
		TimeZone: "America/New_York",
		Features: []*types.Feature{
			{Type: types.FEAT_TARGET_ENCODE, Field: "g", Params: json.RawMessage(`{"target":"y","fit_on":"to_day(ts) < d"}`)},
			{Type: types.FEAT_TARGET_ENCODE, Field: "g", Params: json.RawMessage(`{"target":"y","fit_on":"split == 0"}`)},
			{Type: types.FEAT_TARGET_ENCODE, Field: "g", TimeZone: "Asia/Tokyo", Params: json.RawMessage(`{"target":"y","fit_on":"to_day(ts) < d"}`)},
		},
	}
	got := ZonedExpressions(req, "", nil)
	want := []string{"America/New_York", "", "Asia/Tokyo"}
	for i, f := range got.Features {
		if f.TimeZone != want[i] {
			t.Errorf("features[%d].tz = %q, want %q", i, f.TimeZone, want[i])
		}
	}
	if req.Features[0].TimeZone != "" {
		t.Error("req mutated")
	}
	if got.Features[1] != req.Features[1] {
		t.Error("an untouched feature was copied")
	}
}
