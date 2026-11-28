package descriptor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// wireTagSet returns the wire keys (JSON tag names) of struct type t's
// fields.
func wireTagSet(t reflect.Type) map[string]bool {
	out := make(map[string]bool, t.NumField())
	for i := range t.NumField() {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			out[tag] = true
		}
	}
	return out
}

// TestManifestTestRequiresAreWireKeys: every manifest tests[].requires
// and post_tests[].requires value is a types.Test JSON tag, so an agent
// copies it into a request verbatim (#783).
func TestManifestTestRequiresAreWireKeys(t *testing.T) {
	t.Parallel()

	tags := wireTagSet(reflect.TypeOf(types.Test{}))
	m := BuildManifestForInstance(nil)
	entries := append(append([]descriptor.TestMeta{}, m.Tests...), m.PostTests...)
	seen := 0
	for _, x := range entries {
		for _, r := range x.Requires {
			seen++
			if !tags[r] {
				t.Errorf("%s requires %q, which is not a types.Test JSON tag", x.Name, r)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no tests[].requires values in the manifest; the gate checks nothing")
	}
}

// TestManifestRefKindsAreWireKeys: every overlays[].ref_kinds value on
// the manifest (top-level and the ProcessChain capability) is a
// types.OverlayRef JSON tag — the arm key a request writes under
// `ref` (#794).
func TestManifestRefKindsAreWireKeys(t *testing.T) {
	t.Parallel()

	tags := wireTagSet(reflect.TypeOf(types.OverlayRef{}))
	m := BuildManifestForInstance(nil)
	caps := append([]descriptor.OverlayCapability{}, m.Overlays...)
	if m.ProcessChain != nil {
		caps = append(caps, m.ProcessChain.Overlays...)
	}
	seen := 0
	for _, c := range caps {
		for _, r := range c.RefKinds {
			seen++
			if !tags[r] {
				t.Errorf("%s ref_kinds %q is not a types.OverlayRef JSON tag", c.Kind, r)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no overlays[].ref_kinds values in the manifest; the gate checks nothing")
	}
}
