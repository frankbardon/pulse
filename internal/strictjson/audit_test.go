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
