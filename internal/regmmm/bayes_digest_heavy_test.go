//go:build heavy

package regmmm

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// TestMMMBayes_LiftAbsentDigest: params.lift_tests empty (or null) is the
// lift-free fit — the geometric Hill digest of
// TestMMMBayes_GeometricDefaultDigest under the same explicit seed —
// so the lift likelihood adds nothing, not even a rounding, when no test
// is given. (Absent lift_tests is that test's own rows.)
func TestMMMBayes_LiftAbsentDigest(t *testing.T) {
	t.Parallel()

	const base = `{"channels":["tv"],"time":"week","chains":1,"warmup":40,"draws":30`
	const want = "e1b68270343c09c00e6ceafdfd4c76aa2af90875bb6a6c648a4a7e05607662b0"
	for _, params := range []string{base + `,"lift_tests":[]}`, base + `,"lift_tests":null}`} {
		if got := fitDigest(t, params, 99); got != want {
			t.Errorf("%s: digest %s, want %s", params, got, want)
		}
	}
}

// geoFitDigest is the SHA-256 of params' geo-panel fit over the
// simulated panel's first 40 weeks per geo (geoShortRows).
func geoFitDigest(t *testing.T, params string, seed int64) string {
	t.Helper()
	spec := geoSpec(params)
	spec.RNGSeed = seed
	reg, err := BayesFactory(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := reg.(extend.ContextRegression).FitContext(context.Background(), geoShortRows())
	if err != nil {
		t.Fatal(err)
	}
	body, err := types.MarshalFinite(out)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

// TestMMMBayes_GeoPerCurvesAbsentDigest: a geo panel with
// params.per_geo_curves absent, false, or with an empty lift_tests is
// the shared-curve panel fit, bit for bit under one explicit seed — at
// the geo target_accept default (0.95, U70 E2-S4) and, with
// target_accept 0.8, the fit measured before per-geo curves and panel
// lift tests landed (U70 E2-S2), so an explicit 0.8 restores the old
// default's sampling exactly.
func TestMMMBayes_GeoPerCurvesAbsentDigest(t *testing.T) {
	t.Parallel()

	const base = geoParams + `,"chains":1,"warmup":20,"draws":10`
	for _, c := range []struct{ params, want string }{
		{base, "7a82326b26438942325ab970a912ba1e2be6487a91b55c6a121656eb5a43acc1"},
		{base + `,"target_accept":0.8`, "c49dfdc172bf47df36f360c17a39fd1d95c1afe06c1fda2856c38538e1695209"},
	} {
		for _, params := range []string{c.params + `}`, c.params + `,"per_geo_curves":false}`, c.params + `,"lift_tests":[]}`} {
			if got := geoFitDigest(t, params, 99); got != c.want {
				t.Errorf("%s: digest %s, want %s", params, got, c.want)
			}
		}
	}
}

// TestMMMBayes_LiftDigest: a one-market fit with a lift test is the fit
// measured before the lift term's partials were routed through a sink
// for the geo panel (U70 E2-S2), bit for bit.
func TestMMMBayes_LiftDigest(t *testing.T) {
	t.Parallel()

	const params = `{"channels":["tv"],"time":"week","chains":1,"warmup":40,"draws":30,"lift_tests":[{"channel":"tv","start":10,"end":20,"spend_delta":-300,"lift":-150,"se":20}]}`
	if got, want := fitDigest(t, params, 99), "ad3550f6aee9904b7d95a34c17b8e27045fbdc5ecca02785aee39c0b2dbc01ee"; got != want {
		t.Errorf("digest %s, want %s", got, want)
	}
}
