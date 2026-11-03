package pulse

import (
	"context"
	"sync"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// TestFitBoundInvariance_Archive is the bound-fit shard family on the
// shared worker-invariance harness: a fitted attribute bound to a
// prebuilt population (ATTR_MAHALANOBIS, params.fit) is row-local and
// merges, so a shard archive runs it on the per-shard reducer — its
// vector-input slot resolved there like every other arm, and a bound
// fit's warning raised ONCE, not once per shard — to the same whole
// response (LayoutFree, bit-exact: count / min / max are exact under
// any merge) as the single-file facade cohort the archive splits, under
// every ShardWorkers count, grouped or not, through fields or a vector.
func TestFitBoundInvariance_Archive(t *testing.T) {
	t.Parallel()

	fsys := fitBoundFS(t)
	cohorts := map[invariance.Layout]string{invariance.File: vecFacadeCohort, invariance.Archive: fbArchive}
	for _, pop := range []string{"attr", "tiny"} {
		for _, grouped := range []bool{false, true} {
			for _, viaVector := range []bool{false, true} {
				mk := func(cohort string) *Request {
					r := fbScoring(cohort, types.ATTR_MAHALANOBIS, nil, fbBound(pop), popRef(pop, "pops/"+pop+model.PopulationSuffix))
					r.Aggregations = r.Aggregations[:3] // count / min / max: exact under any merge
					if viaVector {
						r.Vectors = []types.VectorSpec{{Name: "battery", Fields: []string{"a", "b", "c"}}}
						r.Attributes[0].Fields, r.Attributes[0].Vector = nil, "battery"
					}
					if grouped {
						return fbGrouped(r)
					}
					return r
				}
				invariance.Run(t, invariance.Family[*types.Response]{
					Name:   pop + map[bool]string{true: "_grouped"}[grouped] + map[bool]string{true: "_vector"}[viaVector],
					Groups: []invariance.Group{invariance.ArchiveGroup()},
					Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
						var mu sync.Mutex
						var arm observe.Arm
						p, err := New(Options{FS: fsys, ShardWorkers: k.ShardWorkers, DecodeWorkers: k.DecodeWorkers,
							Hooks: &observe.Hooks{OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, r observe.OperationResult) {
								mu.Lock()
								arm = r.Arm
								mu.Unlock()
							}}})
						if err != nil {
							t.Fatal(err)
						}
						resp, err := p.Process(context.Background(), mk(cohorts[k.Layout]))
						if err != nil {
							t.Fatalf("%s: %v", k, err)
						}
						mu.Lock()
						defer mu.Unlock()
						return invariance.Result[*types.Response]{Answer: invariance.LayoutFree(resp), Arm: arm}
					},
					Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
						if len(resp.Data) == 0 {
							t.Fatal("no rows")
						}
						if pop == "tiny" && (len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(perr.PULSE_MATRIX_INSUFFICIENT_N)) {
							t.Fatalf("tiny fit: warnings %+v, want one PULSE_MATRIX_INSUFFICIENT_N", resp.Warnings)
						}
					},
				})
			}
		}
	}
}
