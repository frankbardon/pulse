//go:build heavy

package pulse_test

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
// invariance (rows redrawn within each wave) — gives the same bits run
// to run, under every decode-worker count and over a shard archive
// under any shard-worker count; another seed moves the SEs; every
// threshold row and free scale factor carries a bootstrap SE; predict
// reports the buffered path and the declared keys cover the result.
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
			p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{DecodeWorkers: 1, ShardWorkers: 1})
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
			if again, _ := run(p, 7); again != want {
				t.Fatal("a second run differs")
			}
			if other, _ := run(p, 8); other == want {
				t.Fatal("another seed gave the same result")
			}
			for _, dw := range []int{3, 8} {
				p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{DecodeWorkers: dw})
				if got, _ := run(p, 7); got != want {
					t.Errorf("DecodeWorkers %d differs", dw)
				}
			}
			for _, sw := range []int{1, 3} {
				p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8, shards: 230}.build(t, pulse.Options{ShardWorkers: sw})
				if got, _ := run(p, 7); got != want {
					t.Errorf("shard archive, ShardWorkers %d differs", sw)
				}
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
