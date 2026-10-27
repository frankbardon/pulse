package exprslots_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/exprslots"
	"github.com/frankbardon/pulse/types"
)

func TestMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		keys []string
		want string // "" = no slot
	}{
		{[]string{"filterers", "expression"}, exprslots.SlotExpression},
		{[]string{"attributes", "expression"}, exprslots.SlotExpression},
		{[]string{"requests", "attributes", "params", "fit_on", "expression"}, exprslots.SlotExpression},
		{[]string{"stages", "request", "filterers", "expression"}, exprslots.SlotExpression},
		{[]string{"regressions", "holdout"}, exprslots.SlotHoldout},
		{[]string{"overlays", "params", "formula"}, exprslots.SlotOverlayFormula},
		{[]string{"requests", "overlays", "params", "formula"}, exprslots.SlotOverlayFormula},
		{[]string{"features", "params", "fit_on"}, exprslots.SlotFeatureFitOn},
		{[]string{"stages", "request", "features", "params", "fit_on"}, exprslots.SlotFeatureFitOn},
		// An attribute's params.fit_on is a keyword or a filterer list.
		{[]string{"attributes", "params", "fit_on"}, ""},
		{[]string{"params", "features", "params", "fit_on"}, ""},
		// A params entry sharing a slot's key is data.
		{[]string{"params", "expression"}, ""},
		{[]string{"attributes", "params", "holdout"}, ""},
		{[]string{"params", "formula"}, ""},
		{[]string{"label"}, ""},
		{[]string{"filterers", "field"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		s, ok := exprslots.Match(c.keys)
		if got := map[bool]string{true: s.Name}[ok]; got != c.want {
			t.Errorf("Match(%q) = %q, want %q", c.keys, got, c.want)
		}
	}
}

func TestAll_IsACopy(t *testing.T) {
	t.Parallel()

	a := exprslots.All()
	a[0].Tail[0] = "mutated"
	if exprslots.All()[0].Tail[0] == "mutated" {
		t.Fatal("All() aliases the package list")
	}
}

// position is one place an owner struct sits in a request root: its
// type and the JSON key path to it (array indices dropped).
type position struct {
	typ  reflect.Type
	keys []string
}

// walkRoot records every struct position reachable from t.
func walkRoot(t reflect.Type, keys []string, stack map[reflect.Type]bool, out *[]position) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t.PkgPath() != reflect.TypeOf(types.Request{}).PkgPath() {
		return
	}
	*out = append(*out, position{t, slices.Clone(keys)})
	if stack[t] {
		return
	}
	stack[t] = true
	defer delete(stack, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		key := jsonKey(f)
		if key == "-" {
			continue
		}
		if f.Anonymous && key == "" {
			walkRoot(f.Type, keys, stack, out)
			continue
		}
		walkRoot(f.Type, append(slices.Clone(keys), key), stack, out)
	}
}

func jsonKey(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		if f.Anonymous {
			return ""
		}
		return f.Name
	}
	name := strings.Split(tag, ",")[0]
	if name == "" && !f.Anonymous {
		return f.Name
	}
	return name
}

// fieldByKey returns t's field whose JSON key is key.
func fieldByKey(t reflect.Type, key string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.IsExported() && jsonKey(f) == key {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// TestTemplateExprSplice_SlotsCoverRequestRoots: in every request root a
// template can target, each position of an expression slot's owner
// struct yields a key path the slot matches (so a `{{token}}` there is
// refused wherever the owner nests), each owner carries the slot's key,
// and no other struct reachable from a root carries an `expression` or
// `holdout` string the list does not name.
func TestTemplateExprSplice_SlotsCoverRequestRoots(t *testing.T) {
	t.Parallel()

	ownerOf := map[string][]exprslots.Slot{}
	for _, s := range exprslots.All() {
		for _, o := range s.Owners {
			ownerOf[o] = append(ownerOf[o], s)
		}
	}
	rawMessage := reflect.TypeOf(json.RawMessage(nil))
	anyMap := reflect.TypeOf(map[string]any(nil))
	roots := []any{types.Request{}, types.ComposedRequest{}, types.ChainRequest{}, types.FacetRequest{}, types.SampleRequest{}}
	seenOwner := map[string]bool{}
	for _, root := range roots {
		var positions []position
		walkRoot(reflect.TypeOf(root), nil, map[reflect.Type]bool{}, &positions)
		for _, p := range positions {
			for _, key := range []string{"expression", "holdout"} {
				f, ok := fieldByKey(p.typ, key)
				if !ok || f.Type.Kind() != reflect.String {
					continue
				}
				if len(ownerOf[p.typ.Name()]) == 0 {
					t.Errorf("%T: %s.%s (at %q) is a %q string no expression slot owns; add it to internal/exprslots or rename it",
						root, p.typ.Name(), f.Name, p.keys, key)
				}
			}
			for _, s := range ownerOf[p.typ.Name()] {
				seenOwner[p.typ.Name()] = true
				// The tail's part on the owner: from its first key the
				// owner declares.
				i := slices.IndexFunc(s.Tail, func(k string) bool { _, ok := fieldByKey(p.typ, k); return ok })
				if i < 0 {
					t.Errorf("%s carries none of slot %s's tail %q", p.typ.Name(), s.Name, s.Tail)
					continue
				}
				f, _ := fieldByKey(p.typ, s.Tail[i])
				switch {
				case i == len(s.Tail)-1 && f.Type.Kind() != reflect.String:
					t.Errorf("%s.%s is %s, slot %s expects a string", p.typ.Name(), f.Name, f.Type, s.Name)
				case i < len(s.Tail)-1 && f.Type != rawMessage && f.Type != anyMap:
					// A params object carrying the expression key.
					t.Errorf("%s.%s is %s, slot %s expects a JSON object", p.typ.Name(), f.Name, f.Type, s.Name)
				}
				keys := append(slices.Clone(p.keys), s.Tail[i:]...)
				if got, ok := exprslots.Match(keys); !ok || got.Name != s.Name {
					t.Errorf("%T: %s at %q: Match(%q) = (%q, %v), want slot %s",
						root, p.typ.Name(), p.keys, keys, got.Name, ok, s.Name)
				}
			}
		}
	}
	for o := range ownerOf {
		if !seenOwner[o] {
			t.Errorf("owner %s is reachable from no request root; the slot list names a type that is gone", o)
		}
	}
}
