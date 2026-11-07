package processing

import (
	"testing"

	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// TestCellShapeOf_MatchesRuntime holds predict-compose's cell-shape
// table (overlayhost.CellShapeOf, read off a cell aggregation's type) to
// the token the runtime's per-cell probe renders (canonicalCellShape)
// from the value every built-in aggregator writes into a crosstab cell
// (dispatchAggregatorCellResult — AGG_WELFORD's triple is lifted to its
// mean there): a type the table decides writes that token, and only the
// types whose value the type alone does not decide (AGG_CONSTANT, the
// array-valued AGG_VEC_*) are undecided.
func TestCellShapeOf_MatchesRuntime(t *testing.T) {
	t.Parallel()

	fixtures := allAggParityFixtures(t)
	for _, op := range types.AllAggregationTypes() {
		fix := fixtures[op]
		t.Run(string(op), func(t *testing.T) {
			slot := &types.Aggregation{Type: op, Field: fix.field, Fields: fix.fields, Label: "primary", Params: fix.params}
			factory, ok := (*ExtensionRegistry)(nil).LookupAggregator(op)
			if !ok {
				t.Fatalf("no factory for %s", op)
			}
			agg, err := factory(slot, fix.schema)
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			scalar, err := agg.Aggregate(fix.records, fix.field)
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			v, err := dispatchAggregatorCellResult(agg, scalar)
			if err != nil {
				t.Fatalf("cell result: %v", err)
			}
			got := canonicalCellShape(v)
			want := overlayhost.CellShapeOf(op, true, false)
			if want == "" {
				if op != types.AGG_CONSTANT && !op.ArrayValued() {
					t.Errorf("%s is undecided by the table but writes %q", op, got)
				}
				return
			}
			if got != want {
				t.Errorf("%s writes cell shape %q, the table says %q", op, got, want)
			}
		})
	}
	if overlayhost.CellShapeOf(types.AGG_SUM, false, false) != "" {
		t.Error("an extension aggregation type decided a cell shape")
	}
}
