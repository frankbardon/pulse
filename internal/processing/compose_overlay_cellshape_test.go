package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
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

	// A weighted AGG_SET_FREQUENCY writes a Σw map (map[string]float64),
	// not the unweighted count map: the table keys on the slot's
	// resolved weight. The weighted aggregator is the one the factory
	// builds off the slot's stamped weight (slotWeight).
	t.Run("AGG_SET_FREQUENCY/weighted", func(t *testing.T) {
		schema := makeSetTestSchema(t)
		schema.Fields = append(schema.Fields, encoding.Field{Name: "w", Type: encoding.FieldTypeF64})
		var records []*Record
		for i, mask := range []uint64{0b0001, 0b0011, 0b0100, 0b1111} {
			records = append(records, NewRecordWithWide(schema, map[string]float64{"w": float64(i) + 0.5}, nil, map[string]any{"tags": mask}))
		}
		slot := &types.Aggregation{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "primary", Weight: types.SlotWeightField("w")}
		factory, ok := (*ExtensionRegistry)(nil).LookupAggregator(types.AGG_SET_FREQUENCY)
		if !ok {
			t.Fatal("no factory for AGG_SET_FREQUENCY")
		}
		agg, err := factory(slot, schema)
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		scalar, err := agg.Aggregate(records, "tags")
		if err != nil {
			t.Fatalf("Aggregate: %v", err)
		}
		v, err := dispatchAggregatorCellResult(agg, scalar)
		if err != nil {
			t.Fatalf("cell result: %v", err)
		}
		if got, want := canonicalCellShape(v), overlayhost.CellShapeOf(types.AGG_SET_FREQUENCY, true, true); got != want {
			t.Errorf("weighted AGG_SET_FREQUENCY writes cell shape %q, the table says %q", got, want)
		}
	})
}
