package pulse_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestCrosstabJoin_PredictAndRuntimeAgree: a crosstab carrying one
// JoinSpec is accepted by predict (which resolves axis fields against
// the joined schema) and the runtime alike, on the fused-enabled and
// fused-disabled engines — and the runtime really crosstabs the joined
// stream. A self-join on the unique key n is the identity, so
// rows=cat × columns=r_cat must equal the join-free rows=cat ×
// columns=cat crosstab cell for cell; before the fix the fused engine
// refused it with PROCESSING_INTERNAL and the buffered engine ignored
// the join and bucketed every row under a null r_cat column.
func TestCrosstabJoin_PredictAndRuntimeAgree(t *testing.T) {
	fs, cohort := zoneCohort(t)
	ctx := context.Background()
	crosstab := func(col string, joins []*types.JoinSpec) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Joins:  joins,
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: col}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n", Label: "c"},
				Margins: types.CrosstabMargins{Grand: true},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	}
	selfJoin := []*types.JoinSpec{{Right: cohort, As: "r_", On: []types.OnPair{{LeftField: "n", RightField: "n"}}}}
	cells := func(t *testing.T, resp *types.Response) map[string]float64 {
		t.Helper()
		m := resp.Crosstab.Matrix
		out := map[string]float64{}
		for i, rk := range m.RowKeys {
			for j, ck := range m.ColumnKeys {
				if m.Cells[i][j].Present {
					out[fmt.Sprint(rk[0], "|", ck[0])] = m.Cells[i][j].Scalar()
				}
			}
		}
		return out
	}

	for _, disable := range []bool{false, true} {
		t.Run(fmt.Sprintf("disable_fusion_%v", disable), func(t *testing.T) {
			p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disable})
			if err != nil {
				t.Fatal(err)
			}
			env := predictEnvelope(t, p, fs, cohort, crosstab("r_cat", selfJoin))
			if len(env.Errors) != 0 || !env.Data.(*descriptor.PredictResult).Valid {
				t.Fatalf("predict refused a crosstab over the joined schema: %+v", env.Errors)
			}
			joined, err := p.Process(ctx, crosstab("r_cat", selfJoin))
			if err != nil {
				t.Fatalf("runtime refused what predict accepts: %v", err)
			}
			plain, err := p.Process(ctx, crosstab("cat", nil))
			if err != nil {
				t.Fatalf("plain crosstab: %v", err)
			}
			got, want := cells(t, joined), cells(t, plain)
			if len(want) != 3 || len(got) != len(want) {
				t.Fatalf("joined cells = %v, want %v", got, want)
			}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("cell %s = %v, want %v", k, got[k], v)
				}
			}
			if g := joined.Crosstab.Matrix.GrandTotal.Scalar(); g != 40 {
				t.Errorf("grand total = %v, want 40", g)
			}
		})
	}
}
