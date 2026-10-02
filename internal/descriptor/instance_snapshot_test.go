package descriptor

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

// TestFeatureSetDigest_Format pins the digest algorithm: "fs1:" +
// sha256hex over the sorted, newline-joined enabled names followed by
// the effective behaviour switches in a fixed order.
func TestFeatureSetDigest_Format(t *testing.T) {
	b := FeatureBehaviour{DisableProjection: true}
	payload := strings.Join([]string{
		"AGG_SUM",
		"capability:process",
		"behaviour:disable_cohort_scan=false",
		"behaviour:disable_components=false",
		"behaviour:disable_defaults=false",
		"behaviour:disable_projection=true",
	}, "\n")
	sum := sha256.Sum256([]byte(payload))
	want := "fs1:" + hex.EncodeToString(sum[:])
	if got := FeatureSetDigest([]string{"capability:process", "AGG_SUM", "AGG_SUM"}, b); got != want {
		t.Errorf("FeatureSetDigest = %s, want %s", got, want)
	}
}

// TestFeatureSetDigest_Sensitivity: order never matters; every enabled
// name and every behaviour switch does.
func TestFeatureSetDigest_Sensitivity(t *testing.T) {
	base := FeatureSetDigest([]string{"a", "b"}, FeatureBehaviour{})
	if FeatureSetDigest([]string{"b", "a"}, FeatureBehaviour{}) != base {
		t.Error("digest depends on input order")
	}
	if FeatureSetDigest([]string{"a"}, FeatureBehaviour{}) == base {
		t.Error("dropping a name did not change the digest")
	}
	for i, b := range []FeatureBehaviour{
		{DisableDefaults: true}, {DisableComponents: true}, {DisableProjection: true}, {DisableCohortScan: true},
	} {
		if FeatureSetDigest([]string{"a", "b"}, b) == base {
			t.Errorf("behaviour switch %d did not change the digest", i)
		}
	}
}

// TestInstanceSnapshot_Scoped covers Enabled / Hidden / names / digest
// on a scoped snapshot and the extension pass-through.
func TestInstanceSnapshot_Scoped(t *testing.T) {
	ext := &ExtensionsSnapshot{}
	s := NewInstanceSnapshot(ext, FeatureSet{
		Enabled:   []string{"capability:process", "AGG_SUM"},
		Hidden:    []string{"AGG_COUNT", "AGG_SUM"}, // an enabled name is never hidden
		Behaviour: FeatureBehaviour{DisableDefaults: true},
	})
	if !s.Scoped() {
		t.Fatal("NewInstanceSnapshot is not scoped")
	}
	if s.Extensions() != ext {
		t.Error("Extensions did not return the wrapped projection")
	}
	if !s.Enabled("AGG_SUM") || !s.Enabled("capability:process") {
		t.Error("listed name not enabled")
	}
	if s.Enabled("AGG_COUNT") || s.Enabled("AGG_NEVER_REGISTERED") {
		t.Error("unlisted name enabled")
	}
	if !s.Hidden("AGG_COUNT") {
		t.Error("hidden name not reported hidden")
	}
	if s.Hidden("AGG_SUM") || s.Hidden("AGG_NEVER_REGISTERED") {
		t.Error("enabled or unknown name reported hidden")
	}
	if got := s.EnabledNames(); !reflect.DeepEqual(got, []string{"AGG_SUM", "capability:process"}) {
		t.Errorf("EnabledNames = %v", got)
	}
	if got := s.HiddenNames(); !reflect.DeepEqual(got, []string{"AGG_COUNT"}) {
		t.Errorf("HiddenNames = %v", got)
	}
	if got, want := s.Digest(), FeatureSetDigest([]string{"AGG_SUM", "capability:process"}, FeatureBehaviour{DisableDefaults: true}); got != want {
		t.Errorf("Digest = %s, want %s", got, want)
	}
	if s.Behaviour() != (FeatureBehaviour{DisableDefaults: true}) {
		t.Errorf("Behaviour = %+v", s.Behaviour())
	}
	names := s.EnabledNames()
	names[0] = "mutated"
	if s.EnabledNames()[0] != "AGG_SUM" {
		t.Error("EnabledNames aliases internal state")
	}
}

// TestInstanceSnapshot_Unscoped: a nil or unscoped snapshot enables
// everything, hides nothing and has no digest.
func TestInstanceSnapshot_Unscoped(t *testing.T) {
	var nilSnap *InstanceSnapshot
	if UnscopedInstanceSnapshot(nil) != nil {
		t.Error("UnscopedInstanceSnapshot(nil) should be nil")
	}
	ext := &ExtensionsSnapshot{}
	for name, s := range map[string]*InstanceSnapshot{"nil": nilSnap, "unscoped": UnscopedInstanceSnapshot(ext)} {
		if s.Scoped() || !s.Enabled("ANYTHING") || s.Hidden("ANYTHING") || s.Digest() != "" ||
			s.EnabledNames() != nil || s.HiddenNames() != nil || s.Behaviour() != (FeatureBehaviour{}) {
			t.Errorf("%s snapshot is not permissive", name)
		}
	}
	if nilSnap.Extensions() != nil || UnscopedInstanceSnapshot(ext).Extensions() != ext {
		t.Error("Extensions pass-through broken")
	}
}
