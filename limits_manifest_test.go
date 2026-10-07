package pulse

import (
	"context"
	"testing"
	"time"
)

// manifestLimitValues indexes a manifest's limits block by name.
func manifestLimitValues(t *testing.T, p *Pulse) (map[string]int64, string, string) {
	t.Helper()
	m := p.Manifest(context.Background())
	out := map[string]int64{}
	for _, e := range m.Limits {
		out[e.Name] = e.Value
	}
	return out, m.LimitsDigest, m.FeatureSetDigest
}

// TestManifest_LimitsBlock: with no options the manifest reports the
// defaults; Options.Limits and a profile `limits` section show up as
// the effective values; limits_digest moves with them and
// feature_set_digest does not.
func TestManifest_LimitsBlock(t *testing.T) {
	plain, err := New(Options{FS: memFsWith(t, nil)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vals, plainDigest, plainFS := manifestLimitValues(t, plain)
	if vals["max_groups"] != DefaultMaxGroups || vals["request_timeout"] != int64(DefaultRequestTimeout) || len(vals) != 8 {
		t.Fatalf("default manifest limits = %v", vals)
	}

	tuned, err := New(Options{FS: memFsWith(t, nil), Limits: Limits{MaxGroups: 500, RequestTimeout: 2 * time.Second}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vals, tunedDigest, tunedFS := manifestLimitValues(t, tuned)
	if vals["max_groups"] != 500 || vals["request_timeout"] != int64(2*time.Second) {
		t.Errorf("Options.Limits not in the manifest: %v", vals)
	}
	if tunedDigest == plainDigest {
		t.Error("limits_digest did not move with Options.Limits")
	}
	if tunedFS != plainFS {
		t.Error("feature_set_digest moved with Options.Limits")
	}

	profiled, err := New(Options{FS: memFsWith(t, nil), FeatureProfile: &FeatureProfile{
		Features: []string{},
		Limits:   &FeatureProfileLimits{MaxMatrixDim: 64},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vals, profDigest, _ := manifestLimitValues(t, profiled)
	if vals["max_matrix_dim"] != 64 {
		t.Errorf("profile limits not in the manifest: %v", vals)
	}
	if profDigest == plainDigest {
		t.Error("limits_digest did not move with the profile's limits")
	}

	// Same effective limits, different spelling: same digest.
	explicit, err := New(Options{FS: memFsWith(t, nil), Limits: Limits{MaxGroups: DefaultMaxGroups}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, d, _ := manifestLimitValues(t, explicit); d != plainDigest {
		t.Error("explicitly-default limits moved limits_digest")
	}
}
