//go:build heavy

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestTallyMerge_ParallelArmsMatchSerial: on the per-shard reducer and
// the parallel buffered decode the tally request takes the parallel
// arm, ungrouped and grouped, and answers exactly the serial run —
// rows, Components.Attributes and warnings (each slot's warning once,
// with cohort-wide counts). A filter admitting no row reports the zero
// tallies the serial run does.
func TestTallyMerge_ParallelArmsMatchSerial(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, a := range tallyArms(t) {
		t.Run(a.name, func(t *testing.T) {
			rec := &execRecorder{}
			p, path, ref, refPath := a.open(t, rec)
			for _, tc := range []struct {
				name    string
				groups  bool
				nothing bool
			}{{"ungrouped", false, false}, {"grouped", true, false}, {"no row", false, true}} {
				t.Run(tc.name, func(t *testing.T) {
					mk := func(path string) *types.Request {
						r := tallyRequest(path, tc.groups)
						if tc.nothing {
							r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "qty > 100"}}
						}
						return r
					}
					got, err := p.Process(ctx, mk(path))
					if err != nil {
						t.Fatalf("parallel Process: %v", err)
					}
					if arm := lastArmOf(t, rec); arm != a.parallel {
						t.Fatalf("tally request took arm %q, want %q", arm, a.parallel)
					}
					want, err := ref.Process(ctx, mk(refPath))
					if err != nil {
						t.Fatalf("serial Process: %v", err)
					}
					if !reflect.DeepEqual(got.Data, want.Data) {
						t.Errorf("Data differ\n  parallel: %v\n  serial:   %v", got.Data, want.Data)
					}
					if got.Components == nil || want.Components == nil {
						t.Fatalf("Components missing: parallel %v, serial %v", got.Components, want.Components)
					}
					if !reflect.DeepEqual(got.Components.Attributes, want.Components.Attributes) {
						t.Errorf("Components.Attributes differ\n  parallel: %+v\n  serial:   %+v", got.Components.Attributes, want.Components.Attributes)
					}
					if !reflect.DeepEqual(got.Warnings, want.Warnings) {
						t.Errorf("Warnings differ\n  parallel: %s\n  serial:   %s", warnString(got.Warnings), warnString(want.Warnings))
					}
					if len(want.Components.Attributes) != 6 {
						t.Fatalf("serial emitted %d attribute entries, want 6: %+v", len(want.Components.Attributes), want.Components.Attributes)
					}
					if tc.nothing {
						for _, e := range want.Components.Attributes {
							if e.N != 0 || e.NNull != 0 {
								t.Errorf("zero-row entry %+v", e)
							}
						}
						return
					}
					// Non-vacuous: every tally counted something, and
					// each tally warning appears exactly once.
					ops := map[string]map[string]any{}
					for _, e := range want.Components.Attributes {
						ops[e.Label] = e.Operator
					}
					for label, key := range map[string]string{"scale": "n_out_of_range", "keyed": "n_reference_missing", "jac": "n_domain_invalid", "ext": "n_high",
						"dkeyed": "n_reference_missing", "ddom": "n_domain_invalid"} {
						if n, _ := ops[label][key].(int); n == 0 {
							t.Errorf("vacuous fixture: %s.%s = %v", label, key, ops[label][key])
						}
					}
					// Once per slot: code × attribute label.
					seen := map[string]int{}
					for _, w := range got.Warnings {
						seen[w.Code+"/"+fmt.Sprint(w.Details["attribute"])]++
					}
					for _, k := range []string{
						string(perr.PULSE_ATTR_SCALE_OUT_OF_RANGE) + "/scale",
						string(perr.PULSE_VECTOR_REFERENCE_MISSING) + "/keyed",
						string(perr.PULSE_VECTOR_METRIC_DOMAIN) + "/jac",
						string(perr.PULSE_VECTOR_REFERENCE_MISSING) + "/dkeyed",
						string(perr.PULSE_VECTOR_METRIC_DOMAIN) + "/ddom",
					} {
						if seen[k] != 1 {
							t.Errorf("warning %s raised %d times, want once: %s", k, seen[k], warnString(got.Warnings))
						}
					}
				})
			}

			// The centroid form stays two-pass and serial; so does an
			// extension that emits components without declaring
			// Mergeable.
			centroid := tallyRequest(path, false)
			centroid.Attributes = []*types.Attribute{{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "v", Label: "c",
				Params: json.RawMessage(`{"metric":"cosine","reference":{"centroid":{}}}`)}}
			centroid.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "c", Label: "sum_c"}}
			solo := tallyRequest(path, false)
			solo.Attributes = []*types.Attribute{{Type: attrTallySolo, Field: "score", Label: "ext"}}
			solo.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "ext", Label: "sum_ext"}}
			fitted := tallyRequest(path, false)
			fitted.Attributes = []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Vector: "v", Label: "f",
				Params: json.RawMessage(`{"metric":"std_euclidean","reference":{"s":1,"q":1}}`)}}
			fitted.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "sum_f"}}
			for name, req := range map[string]*types.Request{"centroid": centroid, "undeclared extension": solo, "fitted vec distance": fitted} {
				if _, err := p.Process(ctx, req); err != nil {
					t.Fatalf("%s Process: %v", name, err)
				}
				if arm := lastArmOf(t, rec); arm == a.parallel {
					t.Errorf("%s took the parallel arm %q", name, arm)
				}
			}
		})
	}
}
