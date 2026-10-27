package descriptor

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// TestSlimManifest_RemovesDescriptions verifies that SlimManifest blanks
// every prose Description field while preserving structural metadata.
func TestSlimManifest_RemovesDescriptions(t *testing.T) {
	t.Parallel()

	full := BuildManifest()
	slim := SlimManifest(full)

	if slim == nil {
		t.Fatal("SlimManifest returned nil")
	}

	for _, op := range slim.Components.Aggregators {
		if op.Description != "" {
			t.Errorf("aggregator %s retained Description in slim mode", op.Name)
		}
		if op.StreamableHint != "" {
			t.Errorf("aggregator %s retained StreamableHint in slim mode", op.Name)
		}
		for _, p := range op.Params {
			if p.Description != "" {
				t.Errorf("aggregator %s param %s retained Description in slim mode", op.Name, p.Name)
			}
		}
	}
	for _, tm := range slim.Tests {
		if tm.Description != "" {
			t.Errorf("test %s retained Description in slim mode", tm.Name)
		}
	}
	for _, tm := range slim.PostTests {
		if tm.Description != "" {
			t.Errorf("post-test %s retained Description in slim mode", tm.Name)
		}
	}
	for _, d := range slim.SynthDistributions {
		if d.Description != "" {
			t.Errorf("distribution %s retained Description in slim mode", d.Name)
		}
	}
	// ErrorCodes is name-only in both full and slim manifests; nothing
	// to strip.
	for _, mt := range slim.MCPTools {
		if mt.Description != "" {
			t.Errorf("mcp tool %s retained Description in slim mode", mt.Name)
		}
	}
}

// TestSlimManifest_PreservesStructure verifies that names, types, and
// counts are unchanged in slim mode.
func TestSlimManifest_PreservesStructure(t *testing.T) {
	t.Parallel()

	full := BuildManifest()
	slim := SlimManifest(full)

	if len(slim.Components.Aggregators) != len(full.Components.Aggregators) {
		t.Errorf("slim aggregator count %d != full %d", len(slim.Components.Aggregators), len(full.Components.Aggregators))
	}
	if len(slim.Tests) != len(full.Tests) {
		t.Errorf("slim Tests count %d != full %d", len(slim.Tests), len(full.Tests))
	}
	if len(slim.PostTests) != len(full.PostTests) {
		t.Errorf("slim PostTests count %d != full %d", len(slim.PostTests), len(full.PostTests))
	}
	if len(slim.ErrorCodes) != len(full.ErrorCodes) {
		t.Errorf("slim ErrorCodes count %d != full %d", len(slim.ErrorCodes), len(full.ErrorCodes))
	}
	// Sample one operator to confirm structural fields intact.
	if len(slim.Components.Aggregators) > 0 {
		op := slim.Components.Aggregators[0]
		if op.Name == "" || op.Category == "" || len(op.AcceptsTypes) == 0 {
			t.Errorf("slim aggregator lost structural fields: %+v", op)
		}
	}
}

// TestSlimManifest_DoesNotMutateInput verifies that the input manifest is
// not modified.
func TestSlimManifest_DoesNotMutateInput(t *testing.T) {
	t.Parallel()

	m := BuildManifest()
	before, _ := json.Marshal(m)
	_ = SlimManifest(m)
	after, _ := json.Marshal(m)
	if string(before) != string(after) {
		t.Fatalf("SlimManifest mutated input manifest")
	}
}

// TestSlimManifest_SmallerThanFull sanity-checks that the slim payload is
// noticeably smaller than the full one.
func TestSlimManifest_SmallerThanFull(t *testing.T) {
	t.Parallel()

	full, _ := json.Marshal(BuildManifest())
	slim, _ := json.Marshal(SlimManifest(BuildManifest()))
	if len(slim) >= len(full) {
		t.Errorf("slim payload (%d bytes) not smaller than full (%d bytes)", len(slim), len(full))
	}
	if float64(len(slim))/float64(len(full)) > 0.75 {
		// Sanity threshold: if slim is more than 75% of full, descriptions
		// either weren't significant or the function isn't stripping them.
		t.Errorf("slim payload only %.1f%% smaller than full; expected more", 100*(1-float64(len(slim))/float64(len(full))))
	}
}

// slimProseKeys are the wire keys SlimManifest's doc comment promises to
// blank wherever they occur in the manifest tree.
var slimProseKeys = []string{"description", "streamable_hint", "emits_type_note"}

// proseOnWire walks a JSON document and returns the path of every
// non-empty slimProseKeys value.
func proseOnWire(t *testing.T, m *descriptor.Manifest) []string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var found []string
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				if s, ok := c.(string); ok && s != "" && slices.Contains(slimProseKeys, k) {
					found = append(found, path+"."+k)
				}
				walk(path+"."+k, c)
			}
		case []any:
			for i, c := range x {
				walk(path+"["+strconv.Itoa(i)+"]", c)
			}
		}
	}
	walk("", doc)
	sort.Strings(found)
	return found
}

// TestSlimManifest_NoProseOnWire: no prose key survives anywhere in the
// slim manifest — regressions (operator, params, modifiers, hints),
// matrices (operator, params, component_schema keys), every operator's
// component_schema keys, components_schemas, operations, the
// capability blocks — unscoped and for every analytic intent's scope.
func TestSlimManifest_NoProseOnWire(t *testing.T) {
	t.Parallel()

	inst := hidingSnapshot()
	check := func(label string, m *descriptor.Manifest) {
		if got := proseOnWire(t, SlimManifest(m)); len(got) > 0 {
			t.Errorf("%s: %d prose values survive slim, e.g. %v", label, len(got), got[:min(len(got), 10)])
		}
	}
	check("unscoped", BuildManifestForInstance(inst))
	for _, in := range Intents() {
		m, err := BuildManifestForIntent(inst, in.ID)
		if err != nil {
			t.Fatalf("%s: %v", in.ID, err)
		}
		check(in.ID, m)
	}
}

// slimProseFields are the Go field names behind slimProseKeys.
var slimProseFields = []string{"Description", "StreamableHint", "EmitsTypeNote"}

// fillStrings populates every exported string reachable from v with
// "x", growing each slice and map to one element and allocating each
// pointer, so a section SlimManifest skips cannot hide behind an empty
// built-in catalog (extension blocks, optional capability pointers).
func fillStrings(v reflect.Value, depth int) {
	if depth > 12 {
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		fillStrings(v.Elem(), depth+1)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fillStrings(v.Field(i), depth+1)
			}
		}
	case reflect.Slice:
		if v.Len() == 0 {
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		}
		for i := 0; i < v.Len(); i++ {
			fillStrings(v.Index(i), depth+1)
		}
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return
		}
		m := reflect.MakeMap(v.Type())
		e := reflect.New(v.Type().Elem()).Elem()
		fillStrings(e, depth+1)
		m.SetMapIndex(reflect.ValueOf("k").Convert(v.Type().Key()), e)
		v.Set(m)
	}
}

// proseFields returns the path of every non-empty slimProseFields
// string reachable from v, and how many prose fields it visited.
func proseFields(v reflect.Value, path string, depth int) (found []string, seen int) {
	if depth > 12 {
		return nil, 0
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return proseFields(v.Elem(), path, depth+1)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			p := path + "." + f.Name
			if f.Type.Kind() == reflect.String && slices.Contains(slimProseFields, f.Name) {
				seen++
				if v.Field(i).String() != "" {
					found = append(found, p)
				}
				continue
			}
			fd, s := proseFields(v.Field(i), p, depth+1)
			found, seen = append(found, fd...), seen+s
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			fd, s := proseFields(v.Index(i), path+"[]", depth+1)
			found, seen = append(found, fd...), seen+s
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			fd, s := proseFields(iter.Value(), path+"{}", depth+1)
			found, seen = append(found, fd...), seen+s
		}
	}
	return found, seen
}

// TestSlimManifest_BlanksEveryDescriptionField: a manifest with every
// reachable string filled (every slice, map and capability pointer
// populated) keeps no Description / StreamableHint / EmitsTypeNote after
// SlimManifest, and the filled input is left untouched.
func TestSlimManifest_BlanksEveryDescriptionField(t *testing.T) {
	t.Parallel()

	var m descriptor.Manifest
	fillStrings(reflect.ValueOf(&m).Elem(), 0)
	before, seenIn := proseFields(reflect.ValueOf(m), "Manifest", 0)
	if seenIn < 20 || len(before) != seenIn {
		t.Fatalf("fill reached %d prose fields, %d filled; the walk is broken", seenIn, len(before))
	}
	found, seen := proseFields(reflect.ValueOf(*SlimManifest(&m)), "Manifest", 0)
	if seen != seenIn {
		t.Errorf("slim visited %d prose fields, input %d", seen, seenIn)
	}
	sort.Strings(found)
	for _, p := range found {
		t.Errorf("slim kept %s", p)
	}
	if after, _ := proseFields(reflect.ValueOf(m), "Manifest", 0); len(after) != len(before) {
		t.Errorf("SlimManifest mutated input: %d prose fields before, %d after", len(before), len(after))
	}
}
