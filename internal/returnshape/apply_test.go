package returnshape_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/internal/returnshape"
	"github.com/frankbardon/pulse/types"
)

var rawMessageType = reflect.TypeFor[json.RawMessage]()

// fill populates v with a non-zero value in every slot reachable
// through structs, pointers, slices (one element) and string-keyed maps
// (one key); a type already on the stack stays zero.
func fill(v reflect.Value, stack map[reflect.Type]bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if stack[v.Type().Elem()] {
			return
		}
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem(), stack)
	case reflect.Struct:
		if stack[v.Type()] {
			return
		}
		stack[v.Type()] = true
		defer delete(stack, v.Type())
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i), stack)
			}
		}
	case reflect.Slice:
		if v.Type() == rawMessageType {
			v.SetBytes([]byte("1"))
			return
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			v.SetBytes([]byte("x"))
			return
		}
		if stack[v.Type().Elem()] {
			return
		}
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), stack)
		v.Set(s)
	case reflect.Array:
		for i := range v.Len() {
			fill(v.Index(i), stack)
		}
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return
		}
		m := reflect.MakeMap(v.Type())
		e := reflect.New(v.Type().Elem()).Elem()
		fill(e, stack)
		m.SetMapIndex(reflect.ValueOf("k").Convert(v.Type().Key()), e)
		v.Set(m)
	case reflect.Interface:
		if v.Type().NumMethod() == 0 {
			v.Set(reflect.ValueOf("v"))
		}
	case reflect.String:
		v.SetString("s")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	}
}

// responsePath is one statically reachable Response slot.
type responsePath struct {
	path      string
	omitEmpty bool
}

// responsePaths lists every JSON slot of Response reachable through
// fixed object keys, pointers and arrays — never through a map or an
// open value — except the `returned` marker.
func responsePaths() []responsePath {
	var out []responsePath
	stack := map[reflect.Type]bool{}
	var walk func(t reflect.Type, prefix string)
	walk = func(t reflect.Type, prefix string) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			if t.Kind() == reflect.Pointer {
				t = t.Elem()
				continue
			}
			if t.Elem().Kind() == reflect.Uint8 {
				return
			}
			prefix += "[*]"
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || stack[t] {
			return
		}
		stack[t] = true
		defer delete(stack, t)
		for i := range t.NumField() {
			sf := t.Field(i)
			tag := sf.Tag.Get("json")
			if tag == "-" || !sf.IsExported() {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if sf.Anonymous && name == "" {
				walk(sf.Type, prefix)
				continue
			}
			if name == "" {
				name = sf.Name
			}
			if prefix == "" && name == "returned" {
				continue
			}
			p := name
			if prefix != "" {
				p = prefix + "." + name
			}
			out = append(out, responsePath{path: p, omitEmpty: strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero")})
			walk(sf.Type, p)
		}
	}
	walk(reflect.TypeFor[types.Response](), "")
	return out
}

// present reports whether some concrete node at path exists in the
// decoded JSON doc.
func present(doc any, segs []returnplan.Segment) bool {
	if len(segs) == 0 {
		return true
	}
	s := segs[0]
	switch d := doc.(type) {
	case map[string]any:
		if s.Index {
			return false
		}
		v, ok := d[s.Name]
		return ok && present(v, segs[1:])
	case []any:
		if !s.Index {
			return false
		}
		for _, e := range d {
			if present(e, segs[1:]) {
				return true
			}
		}
	}
	return false
}

// goZero reports whether every Go value at path in v is zero (a nil
// pointer / slice on the way counts as absent, hence zero).
func goZero(v reflect.Value, segs []returnplan.Segment) bool {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	if len(segs) == 0 {
		return v.IsZero()
	}
	s := segs[0]
	if s.Index {
		for i := range v.Len() {
			if !goZero(v.Index(i), segs[1:]) {
				return false
			}
		}
		return true
	}
	for i := range v.NumField() {
		sf := v.Type().Field(i)
		name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if sf.IsExported() && name == s.Name {
			return goZero(v.Field(i), segs[1:])
		}
	}
	return true
}

func filledResponse() *types.Response {
	r := &types.Response{}
	fill(reflect.ValueOf(r).Elem(), map[reflect.Type]bool{})
	r.Returned = nil
	return r
}

func planFor(t *testing.T, ret *types.Return) *returnplan.Plan {
	t.Helper()
	plan, err := descx.ResolveReturn(&types.Request{Return: ret}, nil)
	if err != nil {
		t.Fatalf("ResolveReturn(%+v): %v", ret, err)
	}
	return plan
}

// TestReturn_ExcludeEveryFieldAbsent is the reflection-driven gate:
// every statically reachable Response slot — each non-omitempty leaf in
// particular, which the Go value can only zero — is present in a fully
// populated response, and once excluded is ABSENT on the wire (never
// null) and zero (nil for a nillable slot) in Go.
func TestReturn_ExcludeEveryFieldAbsent(t *testing.T) {
	paths := responsePaths()
	nonOmit := 0
	for _, rp := range paths {
		if !rp.omitEmpty {
			nonOmit++
		}
	}
	if nonOmit < 20 {
		t.Fatalf("only %d non-omitempty slots found; the walk is broken", nonOmit)
	}
	baseline, err := json.Marshal(filledResponse())
	if err != nil {
		t.Fatal(err)
	}
	var baseDoc any
	if err := json.Unmarshal(baseline, &baseDoc); err != nil {
		t.Fatal(err)
	}
	for _, rp := range paths {
		parsed, err := returnplan.Parse(rp.path)
		if err != nil {
			t.Fatalf("%s: %v", rp.path, err)
		}
		if !present(baseDoc, parsed.Segments) {
			t.Errorf("%s: absent from the fully populated baseline", rp.path)
			continue
		}
		resp := filledResponse()
		returnshape.Apply(resp, planFor(t, &types.Return{Exclude: []string{rp.path}}))
		if !goZero(reflect.ValueOf(resp), parsed.Segments) {
			t.Errorf("%s: excluded but non-zero in Go", rp.path)
		}
		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("%s: marshal: %v", rp.path, err)
		}
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		if present(doc, parsed.Segments) {
			t.Errorf("%s: excluded but present on the wire (omitempty=%v)", rp.path, rp.omitEmpty)
		}
		if !present(doc, []returnplan.Segment{returnplan.Key("returned")}) {
			t.Errorf("%s: shaped response lost its returned marker", rp.path)
		}
	}
}

// TestReturn_MarkerIffNonIdentity: identity plans (nil, preset full, an
// empty block) leave the response untouched and unmarked; a non-identity
// plan stamps preset, digest and precision.
func TestReturn_MarkerIffNonIdentity(t *testing.T) {
	for name, ret := range map[string]*types.Return{
		"nil block":   nil,
		"preset full": {Preset: types.ReturnPresetFull},
		"empty block": {},
	} {
		resp := filledResponse()
		want, _ := json.Marshal(resp)
		returnshape.Apply(resp, planFor(t, ret))
		got, _ := json.Marshal(resp)
		if resp.Returned != nil || string(got) != string(want) {
			t.Errorf("%s: identity plan changed the response (returned=%+v)", name, resp.Returned)
		}
	}
	for name, c := range map[string]struct {
		ret       *types.Return
		preset    string
		precision int
	}{
		"exclude":      {&types.Return{Exclude: []string{"metadata"}}, "custom", 0},
		"include only": {&types.Return{Include: []string{"data"}}, "custom", 0},
		"precision":    {&types.Return{Precision: 4}, "full", 4},
	} {
		resp := filledResponse()
		plan := planFor(t, c.ret)
		returnshape.Apply(resp, plan)
		m := resp.Returned
		if m == nil || m.Preset != c.preset || m.Digest != plan.Digest || m.Precision != c.precision {
			t.Errorf("%s: returned = %+v; want {%s %s %d}", name, m, c.preset, plan.Digest, c.precision)
		}
	}
}

// TestPrecision_WalkMirrorsEncodeFinite: with Return.precision set the
// planned encoder walks wholly selected subtrees itself instead of
// handing them to encodeFinite. Over a response with every reachable
// slot populated (every float 1.5, which 'g' at 17 digits writes as
// "1.5"), precision 17 must reproduce the precision-free bytes exactly
// — field order, omitempty, maps, nested marshallers — under the full
// selection, an exclude and a preset.
func TestPrecision_WalkMirrorsEncodeFinite(t *testing.T) {
	for name, ret := range map[string]types.Return{
		"full":     {},
		"exclude":  {Exclude: []string{"metadata", "tests[*].p_value"}},
		"standard": {Preset: types.ReturnPresetStandard},
	} {
		t.Run(name, func(t *testing.T) {
			base := filledResponse()
			returnshape.Apply(base, planFor(t, &ret))
			withP := ret
			withP.Precision = 17
			r := filledResponse()
			returnshape.Apply(r, planFor(t, &withP))
			if r.Returned == nil || r.Returned.Precision != 17 {
				t.Fatalf("marker = %+v, want precision 17", r.Returned)
			}
			// The markers differ by precision only; compare the rest.
			base.Returned, r.Returned = nil, nil
			want, err := json.Marshal(base)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), "1.5") {
				t.Fatalf("no float reached the wire: %s", got)
			}
			if string(got) != string(want) {
				t.Errorf("precision 17 differs from the precision-free walk:\n got %s\nwant %s", got, want)
			}
		})
	}
}
