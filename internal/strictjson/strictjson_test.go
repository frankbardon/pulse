package strictjson

import (
	stderrors "errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func codedOf(t *testing.T, err error) *perr.CodedError {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want a *CodedError, got %T: %v", err, err)
	}
	return ce
}

// Every depth of a request refuses an unknown key with
// PULSE_REQUEST_UNKNOWN_FIELD, located by details.path (the first
// offending key) and, inside a nested request, by its location key.
func TestDecode_UnknownKeyAtEveryDepth(t *testing.T) {
	cases := []struct {
		name     string
		decode   func([]byte) error
		body     string
		path     string // "" = root (no details.path)
		unknown  []string
		suggest  map[string]any
		location map[string]int
	}{
		{name: "root", decode: decodeAs[types.Request],
			body:    `{"cohort":{"filename":"c.pulse"},"filters":[{"type":"FILTER_INCLUDE"}]}`,
			unknown: []string{"filters"}, suggest: map[string]any{"filters": "filterers"}},
		{name: "sort entry", decode: decodeAs[types.Request],
			body: `{"sort":[{"field":"a"},{"field":"b","direction":"desc"}]}`,
			path: "sort[1].direction", unknown: []string{"direction"}},
		{name: "sort entry suggestion", decode: decodeAs[types.Request],
			body: `{"sort":[{"feild":"a"}]}`,
			path: "sort[0].feild", unknown: []string{"feild"}, suggest: map[string]any{"feild": "field"}},
		{name: "grouper label", decode: decodeAs[types.Request],
			body: `{"groups":[{"type":"GROUP_RANGE","field":"x","label":"x bands"}]}`,
			path: "groups[0].label", unknown: []string{"label"}},
		{name: "crosstab cell", decode: decodeAs[types.Request],
			body: `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"a"}],"cell":{"type":"AGG_COUNT","feild":"x"}}}`,
			path: "crosstab.cell.feild", unknown: []string{"feild"}, suggest: map[string]any{"feild": "field"}},
		{name: "weight object", decode: decodeAs[types.Request],
			body: `{"aggregations":[{"type":"AGG_SUM","field":"x","weight":{"field":"w","knd":"frequency"}}]}`,
			path: "aggregations[0].weight.knd", unknown: []string{"knd"}, suggest: map[string]any{"knd": "kind"}},
		{name: "compose request", decode: decodeAs[types.ComposedRequest],
			body:    `{"requests":[{"label":"a"},{"label":"b","aggregatoins":[]}]}`,
			unknown: []string{"aggregatoins"}, suggest: map[string]any{"aggregatoins": "aggregations"},
			location: map[string]int{LocationRequest: 1}},
		{name: "compose nested", decode: decodeAs[types.ComposedRequest],
			body: `{"requests":[{"label":"a","sort":[{"field":"x","direction":1}]}]}`,
			path: "sort[0].direction", unknown: []string{"direction"},
			location: map[string]int{LocationRequest: 0}},
		{name: "compose fit", decode: decodeAs[types.ComposedRequest],
			body:    `{"requests":[],"fits":[{"name":"a","request":{}},{"name":"b","request":{"cohrt":{}}}]}`,
			unknown: []string{"cohrt"}, suggest: map[string]any{"cohrt": "cohort"},
			location: map[string]int{LocationFit: 1}},
		{name: "compose root", decode: decodeAs[types.ComposedRequest],
			body:    `{"requests":[{"bogus":1}],"overlay":[]}`,
			unknown: []string{"overlay"}, suggest: map[string]any{"overlay": "overlays"}},
		{name: "chain stage", decode: decodeAs[types.ChainRequest],
			body: `{"stages":[{"name":"s0","request":{}},{"name":"s1","request":{"groups":[{"type":"GROUP_CATEGORY","field":"a","lable":"x"}]}}]}`,
			path: "groups[0].lable", unknown: []string{"lable"},
			location: map[string]int{LocationStage: 1}},
		{name: "facet", decode: decodeAs[types.FacetRequest],
			body:    `{"cohort":{"filename":"c"},"field":["a"]}`,
			unknown: []string{"field"}, suggest: map[string]any{"field": "fields"}},
		{name: "lookup", decode: decodeAs[types.LookupRequest],
			body:    `{"cohort":{"filename":"c"},"key":[{"field":"a","value":"1"}]}`,
			unknown: []string{"key"}, suggest: map[string]any{"key": "keys"}},
		{name: "sweep axis", decode: decodeAs[types.ComposedRequest],
			body: `{"requests":[],"sweep":{"axes":[{"name":"a","value":[1]}],"request":{}}}`,
			path: "sweep.axes[0].value", unknown: []string{"value"}, suggest: map[string]any{"value": "values"}},
		{name: "several keys in one object", decode: decodeAs[types.Request],
			body: `{"sort":[{"field":"a","zzz":1,"direction":"x"}]}`,
			path: "sort[0].direction", unknown: []string{"direction", "zzz"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ce := codedOf(t, tc.decode([]byte(tc.body)))
			if ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
				t.Fatalf("code = %s, want PULSE_REQUEST_UNKNOWN_FIELD (%s)", ce.Code, ce.Message)
			}
			if got, ok := ce.Details["path"]; tc.path == "" && ok {
				t.Errorf("root refusal carries details.path %v", got)
			} else if tc.path != "" && got != tc.path {
				t.Errorf("details.path = %v, want %s", got, tc.path)
			}
			if got := ce.Details["unknown_keys"]; !reflect.DeepEqual(got, tc.unknown) {
				t.Errorf("unknown_keys = %v, want %v", got, tc.unknown)
			}
			for k, want := range tc.suggest {
				if got := ce.Details["suggestions"].(map[string]any)[k]; got != want {
					t.Errorf("suggestions[%s] = %v, want %v", k, got, want)
				}
			}
			for _, key := range []string{LocationRequest, LocationFit, LocationStage} {
				want, located := tc.location[key]
				got, has := ce.Details[key]
				if located != has || (located && got != want) {
					t.Errorf("details.%s = %v (present %v), want %v (present %v)", key, got, has, want, located)
				}
			}
		})
	}
}

func decodeAs[T any](raw []byte) error {
	_, err := Decode[T](raw, Options{})
	return err
}

// A known key decodes exactly as encoding/json does, case-insensitive
// matching included; a wrong-typed value stays the plain json error.
func TestDecode_KnownKeysDecode(t *testing.T) {
	req, err := Decode[types.Request]([]byte(`{"cohort":{"filename":"c"},"Sort":[{"field":"a","desc":true}],"aggregations":[{"type":"AGG_SUM","field":"x","params":{"anything":1},"weight":"w"}]}`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Sort) != 1 || !req.Sort[0].Desc || req.Aggregations[0].Weight.IsZero() {
		t.Fatalf("decoded %+v", req)
	}
	_, err = Decode[types.Request]([]byte(`{"sort":"nope"}`), Options{})
	var ce *perr.CodedError
	if err == nil || stderrors.As(err, &ce) {
		t.Fatalf("a wrong-typed value is the json error, got %v", err)
	}
}

// `_meta` is tolerated at the document root only under AllowMeta.
func TestDecode_MetaTolerance(t *testing.T) {
	body := []byte(`{"_meta":{"name":"x"},"cohort":{"filename":"c"}}`)
	if _, err := Decode[types.Request](body, Options{AllowMeta: true}); err != nil {
		t.Fatalf("root _meta under AllowMeta: %v", err)
	}
	if err := decodeAs[types.Request](body); codedOf(t, err).Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("root _meta without AllowMeta: %v", err)
	}
	nested := []byte(`{"sort":[{"field":"a","_meta":{}}]}`)
	if _, err := Decode[types.Request](nested, Options{AllowMeta: true}); err == nil {
		t.Fatal("a nested _meta is refused even under AllowMeta")
	}
}

// A hidden key is refused as unknown and is never a valid key or a
// suggestion.
func TestDecode_HiddenKeysRefused(t *testing.T) {
	hide := func(t reflect.Type) []string {
		switch t {
		case reflect.TypeFor[types.Request]():
			return []string{"crosstab"}
		case reflect.TypeFor[types.Aggregation]():
			return []string{"weight"}
		}
		return nil
	}
	ce := codedOf(t, func() error {
		_, err := Decode[types.Request]([]byte(`{"crosstab":{}}`), Options{Hidden: hide})
		return err
	}())
	if valid := ce.Details["valid_keys"].([]string); slices.Contains(valid, "crosstab") {
		t.Errorf("valid_keys names the hidden slot: %v", valid)
	}
	ce = codedOf(t, func() error {
		_, err := Decode[types.Request]([]byte(`{"aggregations":[{"type":"AGG_SUM","weight":null}]}`), Options{Hidden: hide})
		return err
	}())
	if ce.Details["path"] != "aggregations[0].weight" {
		t.Errorf("path = %v", ce.Details["path"])
	}
	if valid := ce.Details["valid_keys"].([]string); slices.Contains(valid, "weight") {
		t.Errorf("valid_keys names the hidden weight: %v", valid)
	}
}

// The message says the key is refused, not ignored.
func TestUnknownFieldError_MessageSaysRefused(t *testing.T) {
	ce := UnknownFieldError("", []string{"filters"}, []string{"filterers"})
	for _, banned := range []string{"ignored during decode", "silently dropped."} {
		if strings.Contains(ce.Message, banned) {
			t.Errorf("message still says %q: %s", banned, ce.Message)
		}
	}
	if !strings.Contains(ce.Message, "refused") || !strings.Contains(ce.Message, `did you mean "filterers"`) {
		t.Errorf("message = %s", ce.Message)
	}
}
