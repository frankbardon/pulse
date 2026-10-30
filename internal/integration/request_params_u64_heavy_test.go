//go:build heavy

package integration_test

import (
	"context"
	"encoding/json"
	"math/big"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// TestParams_U64FieldExactAcrossArms: `id OP rhs` for every comparison
// and a param (int64 and uint64), a literal and another u64 field keeps
// exactly the rows the integer comparison keeps — FILTER_EXPRESSION
// (including the rows whose y is null, which run the untyped twin) and
// ATTR_FORMULA alike — and DecodeWorkers 1 (streaming) and 4 (parallel
// decode) return byte-identical responses.
func TestParams_U64FieldExactAcrossArms(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := u64ParamsDir(t)
	var mu sync.Mutex
	var arm observe.Arm
	open := func(workers int) *pulse.Pulse {
		p, err := pulse.New(pulse.Options{DataDir: dir, DecodeWorkers: workers, Hooks: &observe.Hooks{
			OnOperationEnd: func(_ context.Context, info observe.OperationInfo, res observe.OperationResult) {
				if info.Kind == observe.OpProcess {
					mu.Lock()
					arm = res.Arm
					mu.Unlock()
				}
			},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	serial, parallel := open(1), open(4)
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
	for _, rhs := range rhss {
		for _, op := range []string{"==", "!=", "<", "<=", ">", ">="} {
			cmp := "id " + op + " " + rhs.src
			want := 0
			for i := 0; i < u64Rows; i++ {
				if u64Exact(op, new(big.Int).SetUint64(u64RowID(i)), rhs.at(i)) {
					want++
				}
			}
			req := func() *types.Request {
				return &types.Request{
					Cohort:     &types.Cohort{Filename: "u64.pulse"},
					Params:     params,
					Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "(" + cmp + ") && (y == nil || y >= 0)"}},
					Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "hit", Expression: cmp + " ? 1 : 0"}},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "id", Label: "kept"},
						{Type: types.AGG_SUM, Field: "hit", Label: "hits"},
					},
				}
			}
			var outs []string
			for _, run := range []struct {
				p    *pulse.Pulse
				want observe.Arm
			}{{serial, observe.ArmStreaming}, {parallel, observe.ArmParallelDecode}} {
				resp, err := run.p.Process(ctx, req())
				if err != nil {
					t.Fatalf("%s: %v", cmp, err)
				}
				mu.Lock()
				got := arm
				mu.Unlock()
				if got != run.want {
					t.Fatalf("%s ran on %q, want %q", cmp, got, run.want)
				}
				if kept, hits := paramsFloat(t, resp, "kept"), paramsFloat(t, resp, "hits"); kept != float64(want) || hits != float64(want) {
					t.Errorf("%s on %s: kept %v hits %v, want %d", cmp, got, kept, hits, want)
				}
				b, err := json.Marshal(resp)
				if err != nil {
					t.Fatal(err)
				}
				outs = append(outs, string(b))
			}
			if outs[0] != outs[1] {
				t.Errorf("%s: DecodeWorkers 1 and 4 differ:\n%s\n%s", cmp, outs[0], outs[1])
			}
		}
	}
}
