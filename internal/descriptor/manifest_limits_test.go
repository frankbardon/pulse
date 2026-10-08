package descriptor

import (
	"testing"
	"time"

	"github.com/frankbardon/pulse/internal/limits"
)

// TestManifestLimits_NilSnapshotReportsDefaults: the block is always
// present and a nil (unstamped) instance lists the built-in defaults.
func TestManifestLimits_NilSnapshotReportsDefaults(t *testing.T) {
	m := BuildManifest()
	names := limits.Names()
	if len(m.Limits) != len(names) {
		t.Fatalf("limits block has %d entries, want %d", len(m.Limits), len(names))
	}
	defs := limits.Defaults()
	for i, n := range names {
		e := m.Limits[i]
		if e.Name != string(n) || e.Value != limits.Value(defs, n) || e.Default != limits.Value(defs, n) || e.Unit != limits.Unit(n) {
			t.Errorf("limits[%d] = %+v, want %s at its default", i, e, n)
		}
	}
	if m.LimitsDigest != limits.Digest(defs) {
		t.Errorf("limits_digest = %q, want the defaults' digest", m.LimitsDigest)
	}
}

// TestManifestLimits_TunedValues: a stamped snapshot reports its
// effective values; limits_digest moves with them and
// feature_set_digest does not.
func TestManifestLimits_TunedValues(t *testing.T) {
	base := BuildManifest()
	tuned := limits.Defaults()
	tuned.MaxGroups = 1_000
	tuned.RequestTimeout = 30 * time.Second
	m := BuildManifestForInstance((*InstanceSnapshot)(nil).WithLimits(tuned))

	got := map[string]int64{}
	for _, e := range m.Limits {
		got[e.Name] = e.Value
	}
	if got["max_groups"] != 1_000 || got["request_timeout"] != int64(30*time.Second) {
		t.Errorf("tuned limits not reported: %v", got)
	}
	if got["max_matrix_dim"] != limits.DefaultMaxMatrixDim {
		t.Errorf("an untuned limit moved: %v", got)
	}
	if m.LimitsDigest == base.LimitsDigest {
		t.Error("limits_digest did not move with the effective limits")
	}
	if m.FeatureSetDigest != base.FeatureSetDigest {
		t.Errorf("feature_set_digest moved with the limits: %q vs %q", m.FeatureSetDigest, base.FeatureSetDigest)
	}

	same := BuildManifestForInstance((*InstanceSnapshot)(nil).WithLimits(limits.Defaults()))
	if same.LimitsDigest != base.LimitsDigest {
		t.Error("explicit default limits do not share the defaults' limits_digest")
	}
}
