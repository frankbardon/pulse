//go:build heavy

package integration_test

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// TestU64ParamsInvariance_Heavy is the exact-u64 family on the shared
// worker-invariance harness: `id OP rhs` for every comparison and a
// param (int64 and uint64), a literal and another u64 field keeps
// exactly the rows the integer comparison keeps — FILTER_EXPRESSION
// (including the rows whose y is null, which run the untyped twin) and
// ATTR_FORMULA alike, checked on the serial (streaming) reference — and
// DecodeWorkers 2/3/7/8 (the parallel-decode arm) answer the same whole
// response bit for bit.
func TestU64ParamsInvariance_Heavy(t *testing.T) {
	t.Parallel()

	cohorts := map[invariance.Layout]invCohort{invariance.BigFile: {afero.NewBasePathFs(afero.NewOsFs(), u64ParamsDir(t)), "u64.pulse"}}
	params := types.Params{
		"k":   json.Number("9007199254740993"),     // 2^53 + 1, an int64 param
		"top": json.Number("18446744073709540000"), // a uint64 param past 2^63
	}
	rhss := []struct {
		src string
		at  func(i int) *big.Int
	}{
		{"params.k", func(int) *big.Int { return new(big.Int).SetUint64(9007199254740993) }},
		{"params.top", func(int) *big.Int { return new(big.Int).SetUint64(18446744073709540000) }},
		{"9007199254740993", func(int) *big.Int { return new(big.Int).SetUint64(9007199254740993) }},
		{"id2", func(i int) *big.Int { return new(big.Int).SetUint64(u64RowID2(i)) }},
	}
	g := invariance.DecodeGroup("decode", invariance.BigFile)
	g.Ref.Arm = observe.ArmStreaming
	for _, rhs := range rhss {
		for _, op := range []string{"==", "!=", "<", "<=", ">", ">="} {
			cmp := "id " + op + " " + rhs.src
			want := 0
			for i := 0; i < u64Rows; i++ {
				if u64Exact(op, new(big.Int).SetUint64(u64RowID(i)), rhs.at(i)) {
					want++
				}
			}
			req := func(path string) *types.Request {
				return &types.Request{
					Cohort:     &types.Cohort{Filename: path},
					Params:     params,
					Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "(" + cmp + ") && (y == nil || y >= 0)"}},
					Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "hit", Expression: cmp + " ? 1 : 0"}},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "id", Label: "kept"},
						{Type: types.AGG_SUM, Field: "hit", Label: "hits"},
					},
				}
			}
			invariance.Run(t, invariance.Family[*types.Response]{
				Name:   cmp,
				Groups: []invariance.Group{g},
				Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
					c := cohorts[k.Layout]
					p, arm := invEngine(t, pulse.Options{}, c, k)
					resp, err := p.Process(context.Background(), req(c.path))
					if err != nil {
						t.Fatalf("%s %s: %v", cmp, k, err)
					}
					return invariance.Result[*types.Response]{Answer: invariance.LayoutFree(resp), Arm: arm()}
				},
				Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
					if kept, hits := paramsFloat(t, resp, "kept"), paramsFloat(t, resp, "hits"); kept != float64(want) || hits != float64(want) {
						t.Fatalf("%s: kept %v hits %v, want %d", cmp, kept, hits, want)
					}
				},
			})
		}
	}
}
