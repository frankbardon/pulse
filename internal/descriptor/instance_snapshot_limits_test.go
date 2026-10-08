package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/internal/limits"
)

// TestInstanceSnapshot_Limits: an absent or nil snapshot reports the
// built-in defaults; WithLimits installs a copy and leaves the receiver
// untouched.
func TestInstanceSnapshot_Limits(t *testing.T) {
	var nilSnap *InstanceSnapshot
	if nilSnap.Limits() != limits.Defaults() {
		t.Fatal("nil snapshot does not report the defaults")
	}
	base := NewInstanceSnapshot(nil, FeatureSet{})
	if base.Limits() != limits.Defaults() {
		t.Fatal("snapshot without limits does not report the defaults")
	}
	want := limits.Defaults()
	want.MaxGroups = 5
	got := base.WithLimits(want)
	if got.Limits() != want {
		t.Fatalf("WithLimits: Limits() = %+v, want %+v", got.Limits(), want)
	}
	if base.Limits() != limits.Defaults() {
		t.Fatal("WithLimits mutated the receiver")
	}
	if nilSnap.WithLimits(want).Limits() != want {
		t.Fatal("WithLimits on a nil receiver lost the limits")
	}
}
