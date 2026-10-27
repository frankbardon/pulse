package strictjson

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// requestRoots are the JSON request roots decoded through this package.
var requestRoots = []reflect.Type{
	reflect.TypeFor[types.Request](),
	reflect.TypeFor[types.ComposedRequest](),
	reflect.TypeFor[types.ChainRequest](),
	reflect.TypeFor[types.FacetRequest](),
	reflect.TypeFor[types.LookupRequest](),
	reflect.TypeFor[types.SampleRequest](),
}

// TestStrictJSON_UnmarshalersAudited: a custom UnmarshalJSON hides its
// object's keys from the walk, so every json.Unmarshaler reachable from
// a request root must be audited into objectShapes (checked against
// another shape), walkedUnmarshalers (checked as its own fields) or
// opaqueUnmarshalers (never accepts an object). A new one fails here
// until it is classified — never silently lenient.
func TestStrictJSON_UnmarshalersAudited(t *testing.T) {
	t.Parallel()

	seen := map[reflect.Type]bool{}
	var unaudited []string
	var visit func(reflect.Type)
	visit = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if seen[t] || t == rawMessageType {
			return
		}
		seen[t] = true
		if implements(t, jsonUnmarshalerType) {
			_, shaped := objectShapes[t]
			if !shaped && !walkedUnmarshalers[t] && !opaqueUnmarshalers[t] {
				unaudited = append(unaudited, t.PkgPath()+"."+t.Name())
			}
		}
		switch t.Kind() {
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				visit(t.Field(i).Type)
			}
		case reflect.Slice, reflect.Array, reflect.Map:
			visit(t.Elem())
		}
	}
	for _, r := range requestRoots {
		visit(r)
	}
	for _, u := range unaudited {
		t.Errorf("%s implements json.Unmarshaler but is not audited in objectShapes / walkedUnmarshalers / opaqueUnmarshalers", u)
	}
	for shape := range objectShapes {
		if !seen[shape] {
			t.Errorf("objectShapes lists %s, which no request root reaches", shape)
		}
	}
}

// The strict decode yields exactly what encoding/json decodes for a
// request carrying every custom unmarshaller's accepted forms.
func TestStrictJSON_WalkAgreesWithDecoder(t *testing.T) {
	t.Parallel()

	body := []byte(`{"cohort":{"filename":"c"},"aggregations":[{"type":"AGG_SUM","field":"x","weight":{"field":"w","kind":"frequency"}}],"regressions":[{"type":"REG_OLS","covariance":{"type":"hac","lags":"auto"}}]}`)
	var viaJSON types.Request
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&viaJSON); err != nil {
		t.Fatalf("fixture is not a valid strict request: %v", err)
	}
	got, err := Decode[types.Request](body, Options{})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(viaJSON)
	b, _ := json.Marshal(got)
	if !bytes.Equal(a, b) {
		t.Fatalf("strict decode differs from encoding/json:\n%s\n%s", a, b)
	}
}

// TestStrictJSON_ImportBoundary: the decoder is a leaf (stdlib + types
// + errors) so the no-execute descriptor, the template package and the
// sweep rules can all import it.
func TestStrictJSON_ImportBoundary(t *testing.T) {
	t.Parallel()

	const prefix = "github.com/frankbardon/pulse"
	allowed := map[string]bool{
		prefix + "/internal/strictjson": true,
		prefix + "/types":               true,
		prefix + "/errors":              true,
		prefix + "/internal/returnplan": true, // rides in through types
	}
	out, err := exec.Command("go", "list", "-deps", prefix+"/internal/strictjson").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, prefix) && !allowed[dep] {
			t.Errorf("internal/strictjson imports %s; it may import only stdlib, types and errors", dep)
		}
	}
}

// TestStrictJSON_ParamsFreeFormKeys: Request.Params (and the Compose /
// chain top-level params) is a name→value map, so ANY parameter name
// and any nested value passes the walk and decodes exactly as
// encoding/json does, numbers kept as json.Number — while an unknown
// key beside params, or inside a slot after it, is still refused.
func TestStrictJSON_ParamsFreeFormKeys(t *testing.T) {
	t.Parallel()

	accepted := []struct {
		name   string
		decode func([]byte) error
		body   string
	}{
		{name: "request", decode: decodeAs[types.Request],
			body: `{"params":{"from_wave":"a","min_age":30,"ids":[18446744073709551615,2],"nested":{"cohrt":1},"lists":[[1]]}}`},
		{name: "compose", decode: decodeAs[types.ComposedRequest],
			body: `{"params":{"feild":1},"requests":[{"label":"a","params":{"lable":"x"}}]}`},
		{name: "chain", decode: decodeAs[types.ChainRequest],
			body: `{"params":{"direction":true},"stages":[{"name":"s0","request":{"params":{"sort":[1]}}}]}`},
	}
	for _, tc := range accepted {
		t.Run("accepts/"+tc.name, func(t *testing.T) {
			if err := tc.decode([]byte(tc.body)); err != nil {
				t.Fatalf("params keys are caller names, never unknown fields: %v", err)
			}
		})
	}

	body := []byte(`{"params":{"big":18446744073709551615,"rate":1.10}}`)
	got, err := Decode[types.Request](body, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var viaJSON types.Request
	if err := json.Unmarshal(body, &viaJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Params, viaJSON.Params) {
		t.Fatalf("strict params %#v differ from encoding/json %#v", got.Params, viaJSON.Params)
	}
	if n, ok := got.Params["big"].(json.Number); !ok || n.String() != "18446744073709551615" {
		t.Fatalf("params.big = %#v, want json.Number 18446744073709551615", got.Params["big"])
	}

	refused := []struct {
		name, body, path string
		unknown          []string
	}{
		{name: "sibling of params", body: `{"params":{"a":1},"filters":[]}`, unknown: []string{"filters"}},
		{name: "slot after params", body: `{"params":{"a":1},"sort":[{"feild":"a"}]}`,
			path: "sort[0].feild", unknown: []string{"feild"}},
	}
	for _, tc := range refused {
		t.Run("refuses/"+tc.name, func(t *testing.T) {
			ce := codedOf(t, decodeAs[types.Request]([]byte(tc.body)))
			if ce.Code != "PULSE_REQUEST_UNKNOWN_FIELD" {
				t.Fatalf("code = %s, want PULSE_REQUEST_UNKNOWN_FIELD", ce.Code)
			}
			if got := ce.Details["unknown_keys"]; !reflect.DeepEqual(got, tc.unknown) {
				t.Errorf("unknown_keys = %v, want %v", got, tc.unknown)
			}
			if got, ok := ce.Details["path"]; (tc.path == "") == ok || (ok && got != tc.path) {
				t.Errorf("details.path = %v, want %q", got, tc.path)
			}
		})
	}
}
