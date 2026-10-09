package descriptor

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func jsonKeyOf(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}

// TestScopedManifestWire_ShadowsElidedKeys: the scoped wire form's
// shadow fields are exactly ScopedManifestElidedKeys, each a real
// top-level Manifest key, so a key is dropped iff it is declared elided.
func TestScopedManifestWire_ShadowsElidedKeys(t *testing.T) {
	keys := map[string]bool{}
	mt := reflect.TypeFor[Manifest]()
	for i := range mt.NumField() {
		keys[jsonKeyOf(mt.Field(i))] = true
	}
	var shadows []string
	wt := reflect.TypeFor[scopedManifestWire]()
	for i := range wt.NumField() {
		f := wt.Field(i)
		if f.Anonymous {
			continue
		}
		k := jsonKeyOf(f)
		if !keys[k] {
			t.Errorf("shadow %s is not a Manifest key", k)
		}
		if !strings.HasSuffix(f.Tag.Get("json"), ",omitempty") || f.Type.Kind() != reflect.Pointer {
			t.Errorf("shadow %s must be a nil-able pointer with omitempty", k)
		}
		shadows = append(shadows, k)
	}
	slices.Sort(shadows)
	if want := ScopedManifestElidedKeys(); !slices.Equal(shadows, want) {
		t.Errorf("shadows = %v, want ScopedManifestElidedKeys %v", shadows, want)
	}
	if !slices.IsSorted(ScopedManifestElidedKeys()) {
		t.Error("ScopedManifestElidedKeys not sorted")
	}
}

// TestManifestMarshal_ScopeSwitchesWireForm: without Scope the manifest
// encodes as the default struct encoding (every key, empty or not);
// with Scope every elided key is absent and every other key stays.
func TestManifestMarshal_ScopeSwitchesWireForm(t *testing.T) {
	m := Manifest{FormatVersion: "1.0", Intents: []string{"describe"}}
	plain, err := json.Marshal(plainManifest(m))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatalf("unscoped wire form differs from the default encoding:\n%s\n%s", got, plain)
	}

	m.Scope = &ManifestScope{Intent: Intent{ID: "describe"}}
	m.Elided = ScopedManifestElidedKeys()
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	for _, k := range ScopedManifestElidedKeys() {
		if _, ok := wire[k]; ok {
			t.Errorf("elided %s present", k)
		}
	}
	for _, k := range []string{"format_version", "components", "tests", "skills", "intents", "overlays", "return_presets", "scope", "elided"} {
		if _, ok := wire[k]; !ok {
			t.Errorf("kept %s absent", k)
		}
	}
	var back Manifest
	if err := json.Unmarshal(body, &back); err != nil || back.Scope == nil || back.Scope.Intent.ID != "describe" {
		t.Errorf("round trip: %v %+v", err, back.Scope)
	}
}
