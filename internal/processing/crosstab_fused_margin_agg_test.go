package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// auxMarginSchema is the fixture the auxiliary-margin admission tests
// drive. Two categorical axes plus three numeric fields: `metric` is the
// cell aggregator's field (so a null there is a record that reached a
// cell slot but contributed no value), `respondent` is the distinct key,
// and `weight` is the auxiliary value field.
func auxMarginSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	brand := encoding.NewDictionary()
	for _, b := range []string{"alpha", "beta"} {
		if _, err := brand.Add(b); err != nil {
			t.Fatalf("brand dict.Add: %v", err)
		}
	}
	gender := encoding.NewDictionary()
	for _, g := range []string{"f", "m"} {
		if _, err := gender.Add(g); err != nil {
			t.Fatalf("gender dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: brand},
			{Name: "gender", Type: encoding.FieldTypeCategoricalU8, Dictionary: gender},
			{Name: "respondent", Type: encoding.FieldTypeF64},
			{Name: "weight", Type: encoding.FieldTypeF64},
			{Name: "metric", Type: encoding.FieldTypeF64},
		},
	}
}

// auxMarginRecords is deliberately built so each of the three ways a
// record can fail cell admission is present exactly once, alongside two
// records that are admitted. Field omission is how a null is expressed
// (Record.NumericValue reports ok=false for an absent key).
//
//	respondent 1  alpha / f  metric 10   ADMITTED
//	respondent 2  beta  / f  metric 20   row key excluded by Include
//	respondent 3  alpha / f  metric —    cell field null
//	respondent 4  alpha / —  metric 40   column key null
//	respondent 5  alpha / m  metric 50   ADMITTED
//
// Weights are distinct per respondent so AGG_DISTINCT_SUM over `weight`
// keyed by `respondent` reads back as a plain sum of the admitted rows,
// and every respondent id is distinct so AGG_DISTINCT_COUNT reads back
// as an admitted-record count.
func auxMarginRecords(schema *encoding.Schema) []*Record {
	rec := func(fields map[string]float64) *Record {
		return NewRecord(schema, fields)
	}
	return []*Record{
		rec(map[string]float64{"brand": 0, "gender": 0, "respondent": 1, "weight": 1, "metric": 10}),
		rec(map[string]float64{"brand": 1, "gender": 0, "respondent": 2, "weight": 2, "metric": 20}),
		rec(map[string]float64{"brand": 0, "gender": 0, "respondent": 3, "weight": 4}),
		rec(map[string]float64{"brand": 0, "respondent": 4, "weight": 8, "metric": 40}),
		rec(map[string]float64{"brand": 0, "gender": 1, "respondent": 5, "weight": 16, "metric": 50}),
	}
}

// auxMarginSpec builds the crosstab every test in this file varies: the
// alpha-only Include on the row axis is what makes respondent 2's row
// key unresolvable, and all three margins are on so every auxiliary slot
// has somewhere to land.
func auxMarginSpec() *types.CrosstabSpec {
	return &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand", Include: []string{"alpha"}}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "gender"}},
		Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "metric"},
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		MarginAggregations: []*types.Aggregation{
			{Type: types.AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"},
			{Type: types.AGG_DISTINCT_SUM, Field: "weight", Label: "weighted_base",
				Params: json.RawMessage(`{"distinct_by":"respondent"}`)},
		},
	}
}

// driveAuxMargin runs the fused state over the fixture and hands the
// state back unfinalized, so a test can read the auxiliary accumulators
// as they were accumulated. Finalize is deliberately not called — these
// assertions are about the WALK, and finalizeAuxMargins' projection of
// the same accumulators onto the response is covered separately by
// crosstab_margin_agg_wire_test.go.
func driveAuxMargin(t *testing.T, spec *types.CrosstabSpec) *FusedCrosstabState {
	t.Helper()
	schema := auxMarginSchema(t)
	state, err := NewFusedCrosstabState(spec, schema, &ExtensionRegistry{})
	if err != nil {
		t.Fatalf("NewFusedCrosstabState: %v", err)
	}
	for _, r := range auxMarginRecords(schema) {
		state.AddTotalRow()
		if err := state.Update(r); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	return state
}

// auxFinal finalizes one auxiliary accumulator slot and returns its
// scalar value plus its universal-floor counters.
func auxFinal(t *testing.T, slot []auxMarginAccumulator, idx int) (float64, int, int) {
	t.Helper()
	if idx >= len(slot) {
		t.Fatalf("auxiliary index %d out of range (%d accumulators)", idx, len(slot))
	}
	acc := slot[idx]
	if acc.agg == nil {
		return 0, acc.n, acc.nNull
	}
	v, err := acc.agg.Finalize()
	if err != nil {
		t.Fatalf("auxiliary Finalize: %v", err)
	}
	return v, acc.n, acc.nNull
}

// TestFusedCrosstab_AuxMarginObservesCellAdmission is the whole point of
// this story. An auxiliary margin aggregation must see EXACTLY the
// records that reached a cell — so the two records excluded by the
// grouper Include and by the null cell field are absent from every
// auxiliary slot, while the cell aggregator's OWN margins still count
// them (which is measured, long-standing behaviour, not a bug being
// fixed here).
//
// Getting this wrong is silent: every number still renders, the base is
// merely cohort-wide instead of metric-scoped.
func TestFusedCrosstab_AuxMarginObservesCellAdmission(t *testing.T) {
	state := driveAuxMargin(t, auxMarginSpec())

	// Grand slot: respondents 1 and 5 are the only admitted records.
	base, n, nNull := auxFinal(t, state.grandMarginAux, 0)
	if base != 2 {
		t.Errorf("grand auxiliary distinct_count = %v, want 2 (respondents 1 and 5); "+
			"3 means the null cell field was admitted, 4 means the Include-excluded row was", base)
	}
	if n != 2 || nNull != 0 {
		t.Errorf("grand auxiliary floor = {n:%d, n_null:%d}, want {2, 0}", n, nNull)
	}

	weighted, _, _ := auxFinal(t, state.grandMarginAux, 1)
	if weighted != 17 {
		t.Errorf("grand auxiliary distinct_sum = %v, want 17 (weights 1 + 16); "+
			"21 admits the null-metric row, 19 admits the Include-excluded row, 25 admits the null column key",
			weighted)
	}

	// The cell aggregator's own grand margin is deliberately NOT
	// admission-scoped: it counts every filter-passing record. If this
	// assertion ever fails the two behaviours have been conflated.
	if state.grandMarginCount != 5 {
		t.Errorf("cell grand margin count = %d, want 5 — the auxiliary rule must not "+
			"narrow the cell aggregator's own margins", state.grandMarginCount)
	}
}

// TestFusedCrosstab_AuxMarginPerAxisSlots pins the per-row and
// per-column auxiliary figures. The contrast with the cell's own column
// margin is the measured evidence from the research note: the `f`
// column margin counts the Include-excluded record, the auxiliary
// beside it must not.
func TestFusedCrosstab_AuxMarginPerAxisSlots(t *testing.T) {
	state := driveAuxMargin(t, auxMarginSpec())

	// One row key survives the Include: alpha.
	if len(state.rowKeys) != 1 || state.rowKeys[0] != "alpha" {
		t.Fatalf("row keys = %v, want [alpha]", state.rowKeys)
	}
	rowBase, _, _ := auxFinal(t, state.rowMarginAux[0], 0)
	if rowBase != 2 {
		t.Errorf("alpha row auxiliary distinct_count = %v, want 2", rowBase)
	}

	// Column `f` holds respondents 1 (admitted), 2 (Include-excluded)
	// and 3 (null cell field); only respondent 1 is admitted.
	colIdx, ok := state.colIndex["f"]
	if !ok {
		t.Fatalf("column key f not interned; columns = %v", state.colKeys)
	}
	colBase, _, _ := auxFinal(t, state.colMarginAux[colIdx], 0)
	if colBase != 1 {
		t.Errorf("f column auxiliary distinct_count = %v, want 1 (respondent 1 only)", colBase)
	}
	if got := state.colMarginCount[colIdx]; got != 3 {
		t.Errorf("cell f column margin count = %d, want 3 — the cell aggregator's own "+
			"column margin still counts every record routed to the column", got)
	}
}

// TestFusedCrosstab_AuxMarginNoAllocationWhenAbsent covers the third
// acceptance criterion from both directions: no auxiliary structure is
// allocated when the slot is not declared, and a declared auxiliary
// allocates only for the margin slots the spec actually asks for.
func TestFusedCrosstab_AuxMarginNoAllocationWhenAbsent(t *testing.T) {
	t.Run("slot absent", func(t *testing.T) {
		spec := auxMarginSpec()
		spec.MarginAggregations = nil
		state := driveAuxMargin(t, spec)
		if state.auxAggs != nil || state.auxFactories != nil || state.auxPresent != nil {
			t.Errorf("auxiliary wiring allocated with no margin_aggregations declared")
		}
		if state.rowMarginAux != nil || state.colMarginAux != nil || state.grandMarginAux != nil {
			t.Errorf("auxiliary accumulators allocated with no margin_aggregations declared")
		}
	})

	t.Run("column margin only", func(t *testing.T) {
		spec := auxMarginSpec()
		spec.Margins = types.CrosstabMargins{Columns: true}
		state := driveAuxMargin(t, spec)
		if state.colMarginAux == nil {
			t.Fatalf("column auxiliary accumulators not allocated for a column-margin spec")
		}
		if state.rowMarginAux != nil {
			t.Errorf("row auxiliary accumulators allocated for a spec with no row margin")
		}
		if state.grandMarginAux != nil {
			t.Errorf("grand auxiliary accumulator allocated for a spec with no grand margin")
		}
	})
}

// TestCanFuseCrosstab_MarginAggregationsStayFusable is the performance
// premise of the whole effort: declaring an auxiliary margin
// aggregation must not push a Visa-shaped crosstab onto the buffered
// path, where the same numbers cost roughly six times the memory and
// nothing says so.
func TestCanFuseCrosstab_MarginAggregationsStayFusable(t *testing.T) {
	schema := auxMarginSchema(t)
	req := &types.Request{Crosstab: auxMarginSpec()}
	if ok, reason := CanFuseCrosstab(req, schema, nil); !ok {
		t.Errorf("auxiliary-bearing crosstab declined fusion: %q", reason)
	}
}

// TestCanFuseCrosstab_MarginAggregationDeclinesUnaccumulable is the
// other half of the gate decision: an auxiliary the fused walk cannot
// drive record-by-record must decline fusion rather than be silently
// dropped or blow up mid-scan.
func TestCanFuseCrosstab_MarginAggregationDeclinesUnaccumulable(t *testing.T) {
	schema := auxMarginSchema(t)

	t.Run("non-online auxiliary", func(t *testing.T) {
		spec := auxMarginSpec()
		spec.MarginAggregations = []*types.Aggregation{
			{Type: types.AGG_MEDIAN, Field: "weight", Label: "med"},
		}
		ok, reason := CanFuseCrosstab(&types.Request{Crosstab: spec}, schema, nil)
		if ok {
			t.Errorf("AGG_MEDIAN auxiliary admitted to the fused path; it has no UpdateRow")
		}
		if reason == "" {
			t.Errorf("declined with an empty reason")
		}
	})

	t.Run("decimal auxiliary field", func(t *testing.T) {
		decimal := auxMarginSchema(t)
		decimal.Fields = append(decimal.Fields, encoding.Field{
			Name: "amount", Type: encoding.FieldTypeDecimal128, Scale: 2,
		})
		spec := auxMarginSpec()
		spec.MarginAggregations = []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "amount", Label: "amt"},
		}
		if ok, _ := CanFuseCrosstab(&types.Request{Crosstab: spec}, decimal, nil); ok {
			t.Errorf("decimal128 auxiliary field admitted to the fused path")
		}
	})
}
