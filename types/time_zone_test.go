package types_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestTimeZoneSlots_RoundTrip: every new zone slot survives a JSON
// round trip under its wire key.
func TestTimeZoneSlots_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   any
		out  func() any
		key  string
		get  func(any) string
	}{
		{"Request", &types.Request{TimeZone: "Europe/Berlin"}, func() any { return &types.Request{} }, `"time_zone":"Europe/Berlin"`,
			func(v any) string { return v.(*types.Request).TimeZone }},
		{"FacetRequest", &types.FacetRequest{Fields: []string{"x"}, TimeZone: "Europe/Berlin"}, func() any { return &types.FacetRequest{} }, `"time_zone":"Europe/Berlin"`,
			func(v any) string { return v.(*types.FacetRequest).TimeZone }},
		{"Group", &types.Group{Type: types.GROUP_DATE, Field: "d", TimeZone: "Europe/Berlin"}, func() any { return &types.Group{} }, `"tz":"Europe/Berlin"`,
			func(v any) string { return v.(*types.Group).TimeZone }},
		{"Filterer", &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "d", TimeZone: "Europe/Berlin"}, func() any { return &types.Filterer{} }, `"tz":"Europe/Berlin"`,
			func(v any) string { return v.(*types.Filterer).TimeZone }},
		{"Attribute", &types.Attribute{Type: types.ATTR_DATE_PART, Field: "d", TimeZone: "Europe/Berlin"}, func() any { return &types.Attribute{} }, `"tz":"Europe/Berlin"`,
			func(v any) string { return v.(*types.Attribute).TimeZone }},
		{"Feature", &types.Feature{Type: types.FEAT_DATE_FEATURES, Field: "d", TimeZone: "Europe/Berlin"}, func() any { return &types.Feature{} }, `"tz":"Europe/Berlin"`,
			func(v any) string { return v.(*types.Feature).TimeZone }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(b), tc.key) {
				t.Fatalf("wire form %s lacks %s", b, tc.key)
			}
			got := tc.out()
			if err := json.Unmarshal(b, got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if tc.get(got) != "Europe/Berlin" {
				t.Fatalf("round trip lost the zone: %s", b)
			}
		})
	}
}

// TestTimeZoneSlots_AbsentByteIdentical: an unset zone slot never
// reaches the wire, so pre-zone payloads serialize exactly as before.
func TestTimeZoneSlots_AbsentByteIdentical(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"Request", &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"}}, `{"cohort":{"filename":"c.pulse"}}`},
		{"FacetRequest", &types.FacetRequest{Fields: []string{"x"}}, `{"fields":["x"],"histogram_range":[0,0]}`},
		{"Group", &types.Group{Type: types.GROUP_DATE, Field: "d"}, `{"type":"GROUP_DATE","field":"d"}`},
		{"Filterer", &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "d"}, `{"type":"FILTER_DATE_RANGES","field":"d"}`},
		{"Attribute", &types.Attribute{Type: types.ATTR_DATE_PART, Field: "d"}, `{"type":"ATTR_DATE_PART","field":"d"}`},
		{"Feature", &types.Feature{Type: types.FEAT_DATE_FEATURES, Field: "d"}, `{"type":"FEAT_DATE_FEATURES","field":"d"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Fatalf("wire form = %s, want %s", b, tc.want)
			}
		})
	}
}

// TestTimeZoneSlots_NotOnExcludedRoots: the zone slots are deliberately
// absent from Window, OverlaySpec and the ComposedRequest / ChainRequest /
// SampleRequest roots (Compose / Chain inherit per inner Request). Checked
// on the struct tags, since an omitempty slot is invisible on a zero value.
func TestTimeZoneSlots_NotOnExcludedRoots(t *testing.T) {
	for _, v := range []any{
		types.Window{}, types.OverlaySpec{}, types.ComposedRequest{},
		types.ChainRequest{}, types.SampleRequest{},
	} {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if f.Name == "TimeZone" || key == "tz" || key == "time_zone" {
				t.Errorf("%s carries zone slot %s (json %q)", rt.Name(), f.Name, key)
			}
		}
	}
}
