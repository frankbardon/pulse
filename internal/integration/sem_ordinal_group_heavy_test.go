//go:build heavy

package integration_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestSEM_OrdinalBootstrapDeterministic: a seeded bootstrap of a WLSMV
// fit — single group, and the waves under threshold + loading
// invariance (rows redrawn within each wave) — moves with another seed;
// every threshold row and free scale factor carries a bootstrap SE;
// predict reports the buffered path and the declared keys cover the
// result. The same seed's bits run to run and under every worker knob
// are the MAT_SEM invariance family's (TestSEMInvariance_Heavy,
// ordinal_bootstrap_*).
func TestSEM_OrdinalBootstrapDeterministic(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name   string
		params map[string]any
	}{
		{"single", map[string]any{"group": nil}},
		{"grouped", map[string]any{"group_equal": "thresholds,loadings"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := func(seed int) *types.Request {
				params := map[string]any{"resample": "bootstrap", "bootstrap_iters": 12, "rng_seed": seed}
				for k, v := range c.params {
					params[k] = v
				}
				return ordinalGroupRequest(t, "brand_ord", brandOrdered, params)
			}
			run := func(p *pulse.Pulse, seed int) (string, types.MatrixResult) {
				res := semGroupRun(t, p, req(seed))
				if res.Error != nil {
					t.Fatalf("error %+v", res.Error)
				}
				raw, _ := json.Marshal(res)
				return string(raw), res
			}
			p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
			want, res := run(p, 7)
			if res.Scalars["bootstrap_succeeded"] != 12 {
				t.Fatalf("bootstrap_succeeded %v", res.Scalars["bootstrap_succeeded"])
			}
			est := res.Auxiliary["estimates"]
			for i, k := range est.RowKeys {
				free := !math.IsNaN(est.Values[i][4])
				if (strings.Contains(k, " | ") || (strings.Contains(k, " ~*~ ") && strings.HasPrefix(k, "[2]"))) && !free {
					t.Errorf("%s carries no bootstrap p", k)
				}
			}
			if other, _ := run(p, 8); other == want {
				t.Fatal("another seed gave the same result")
			}
			env := predictEnvelope(t, p, fsys, "brand.pulse", req(7))
			if len(env.Errors) != 0 {
				t.Fatalf("predict errors %v", env.Errors)
			}
			if pr := env.Data.(*descriptor.PredictResult); pr.Streamable || pr.Matrices[0].Streamable || pr.Matrices[0].Mergeable {
				t.Error("an ordinal bootstrap predicts streamable / mergeable")
			}
			checkSEMKeysDeclared(t, semDeclaredKeys(t, p), res)
		})
	}
}
