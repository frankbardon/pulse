package regmmm

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// bayes_digest_test.go pins the geometric default: a REG_MMM_BAYES fit
// with params.adstock absent (and the same fit with it spelled
// "geometric", and params.saturation absent or spelled "hill") is the
// SHA-256 of its output's JSON (undefined figures null), measured before
// the delayed and Weibull kernels landed (U70 E1-S2) and unchanged by
// the logistic and Michaelis–Menten curves (E1-S3), so a kernel or curve
// change that moves one bit of the geometric Hill model fails here.

// digestRows is the small cohort as probe rows.
func digestRows() probeRows {
	rows := BayesRows(11)
	out := make(probeRows, len(rows))
	for i, r := range rows {
		out[i] = probeRow{"week": r.Week, "tv": r.TV, "price": r.Price, "sales": r.Sales}
	}
	return out
}

// fitDigest is the SHA-256 of params' fit over digestRows.
func fitDigest(t *testing.T, params string, seed int64) string {
	t.Helper()
	spec := BayesSpec(params)
	spec.RNGSeed = seed
	reg, err := BayesFactory(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := reg.(extend.ContextRegression).FitContext(context.Background(), digestRows())
	if err != nil {
		t.Fatal(err)
	}
	body, err := types.MarshalFinite(out)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

// TestMMMBayes_GeometricDefaultDigest: the geometric Hill fit's output
// bytes are pinned — params.adstock and params.saturation absent under
// the derived seed, and absent or spelled "geometric" / "hill" under an
// explicit seed (spelling them out moves only the derived seed, never
// the model).
func TestMMMBayes_GeometricDefaultDigest(t *testing.T) {
	t.Parallel()

	const base = `{"channels":["tv"],"time":"week","chains":1,"warmup":40,"draws":30`
	for _, tc := range []struct {
		params string
		seed   int64
		want   string
	}{
		{base + `}`, 0, "c5253d29b46fd7911b0cb3de70c061ab0f69138ea7449736e77c2f74b21fcab3"},
		{base + `}`, 99, "e1b68270343c09c00e6ceafdfd4c76aa2af90875bb6a6c648a4a7e05607662b0"},
		{base + `,"adstock":"geometric"}`, 99, "e1b68270343c09c00e6ceafdfd4c76aa2af90875bb6a6c648a4a7e05607662b0"},
		{base + `,"saturation":"hill"}`, 99, "e1b68270343c09c00e6ceafdfd4c76aa2af90875bb6a6c648a4a7e05607662b0"},
		{base + `,"adstock":"geometric","saturation":"hill"}`, 99, "e1b68270343c09c00e6ceafdfd4c76aa2af90875bb6a6c648a4a7e05607662b0"},
	} {
		if got := fitDigest(t, tc.params, tc.seed); got != tc.want {
			t.Errorf("%s seed %d: digest %s, want %s", tc.params, tc.seed, got, tc.want)
		}
	}
}
