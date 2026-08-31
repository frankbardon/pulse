package processing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// This file is E2-S5's contract: the auxiliary margin figures the two
// accumulation paths hold reach Response.Components.Crosstab, and reach
// it IDENTICALLY on both.
//
// Everything before this story read the figures off each path's own
// internal carrier, because there was no response field to read. That is
// no longer the strongest available assertion: a caller sees a Response,
// and a figure that is correct in a carrier but mis-projected on the way
// out is wrong in exactly the way nobody notices. So every assertion
// here goes through RunCrosstab / RunCrosstabFused end to end.
//
// WHY THE PARITY ARM IS NOT ENOUGH ON ITS OWN. Dispatch picks fused or
// buffered on request SHAPE and nothing in Response says which ran, so
// the arms must agree — but a parity test alone passes just as happily
// when both arms are wrong in the same direction. The numeric
// expectations below are therefore stated independently of the parity
// comparison, and they are MEASURED against the shared fixture rather
// than derived from whatever the code currently emits.

// auxWire drives one request down one arm and hands back the crosstab
// components block. A response with no components at all is a legal
// answer (disableComponents), so the caller decides whether nil is a
// failure.
func auxWire(t *testing.T, schema *encoding.Schema, req *types.Request,
	recs []*Record, fused, disableComponents bool) *types.Response {
	t.Helper()
	p := NewProcessor(schema)
	p.SetDisableComponents(disableComponents)
	var (
		resp *types.Response
		err  error
	)
	if fused {
		resp, err = p.RunCrosstabFused(t.Context(), req, NewSliceIterator(recs))
	} else {
		resp, err = p.RunCrosstab(t.Context(), req, recs)
	}
	if err != nil {
		t.Fatalf("crosstab (fused=%v): %v", fused, err)
	}
	return resp
}

// auxFigure reads one labelled auxiliary figure out of a slot, failing
// loudly rather than returning a zero when the label is absent — an
// absent label and a present-but-empty figure are the two states this
// story exists to keep distinguishable, so a helper that conflated them
// would defeat its own tests.
func auxFigure(t *testing.T, slot map[string]types.MarginAggregationFigure, label string) types.MarginAggregationFigure {
	t.Helper()
	fig, ok := slot[label]
	if !ok {
		t.Fatalf("auxiliary %q absent from slot; present labels: %v", label, sortedLabels(slot))
	}
	return fig
}

func sortedLabels(slot map[string]types.MarginAggregationFigure) []string {
	out := make([]string, 0, len(slot))
	for k := range slot {
		out = append(out, k)
	}
	return out
}

// auxScalar returns a present figure's scalar value.
func auxScalar(t *testing.T, fig types.MarginAggregationFigure, what string) float64 {
	t.Helper()
	if !fig.Present {
		t.Fatalf("%s: figure reports Present false, so it has no value", what)
	}
	return coerceFloat64(fig.Value)
}

// TestCrosstabAuxMargin_FiguresReachTheWire pins the figures at the
// surface a caller reads, on both arms, over the shared admission
// fixture. The numbers are the ones E2-S2 / E2-S3 pinned against each
// path's internal carrier: what is new here is that they survive the
// projection onto Response.Components.Crosstab and land under the axis
// key and the label a caller can address them by.
func TestCrosstabAuxMargin_FiguresReachTheWire(t *testing.T) {
	// Measured against auxMarginRecords: respondents 1 (weight 1) and 5
	// (weight 16) are the only admitted records; respondent 1 is
	// column f, respondent 5 is column m, and both are row alpha.
	type want struct {
		base     float64 // AGG_DISTINCT_COUNT over respondent
		weighted float64 // AGG_DISTINCT_SUM over weight, keyed by respondent
	}
	grand := want{base: 2, weighted: 17}
	rowAlpha := want{base: 2, weighted: 17}
	colF := want{base: 1, weighted: 1}
	colM := want{base: 1, weighted: 16}

	for _, fused := range []bool{false, true} {
		name := "buffered"
		if fused {
			name = "fused"
		}
		t.Run(name, func(t *testing.T) {
			schema := auxMarginSchema(t)
			resp := auxWire(t, schema, &types.Request{Crosstab: auxMarginSpec()},
				auxMarginRecords(schema), fused, false)
			ct := resp.Components.Crosstab
			if ct == nil {
				t.Fatal("no crosstab components emitted")
			}

			// Fixture guard: the axis order below is what indexes the
			// vectors. Row axis carries alpha only (the Include); the
			// column axis carries f then m.
			if got := resp.Crosstab.Matrix.RowKeys; len(got) != 1 {
				t.Fatalf("fixture drift: %d row keys, want 1; got %v", len(got), got)
			}
			if got := resp.Crosstab.Matrix.ColumnKeys; len(got) != 2 {
				t.Fatalf("fixture drift: %d column keys, want 2; got %v", len(got), got)
			}

			check := func(slot map[string]types.MarginAggregationFigure, w want, where string) {
				t.Helper()
				if slot == nil {
					t.Fatalf("%s: no auxiliary figures emitted", where)
				}
				if got := auxScalar(t, auxFigure(t, slot, "base"), where+" base"); got != w.base {
					t.Errorf("%s base = %v, want %v", where, got, w.base)
				}
				if got := auxScalar(t, auxFigure(t, slot, "weighted_base"), where+" weighted_base"); got != w.weighted {
					t.Errorf("%s weighted_base = %v, want %v", where, got, w.weighted)
				}
			}

			check(ct.GrandTotalAggregations, grand, "grand")
			if len(ct.RowMarginAggregations) != 1 {
				t.Fatalf("row_margin_aggregations has %d entries, want 1 (one per row key)",
					len(ct.RowMarginAggregations))
			}
			check(ct.RowMarginAggregations[0], rowAlpha, "row[alpha]")
			if len(ct.ColumnMarginAggregations) != 2 {
				t.Fatalf("column_margin_aggregations has %d entries, want 2 (one per column key)",
					len(ct.ColumnMarginAggregations))
			}
			check(ct.ColumnMarginAggregations[0], colF, "column[f]")
			check(ct.ColumnMarginAggregations[1], colM, "column[m]")
		})
	}
}

// TestCrosstabAuxMargin_WireFiguresAgreeAcrossPaths compares the emitted
// auxiliary block BYTE FOR BYTE between the two arms.
//
// This is the assertion that matters most for a caller: dispatch picks
// an arm on request shape — a non-mergeable cell aggregator, a quantile
// axis, a feature — and nothing in Response reports which one ran, so a
// figure that differed between them would appear, vanish or change for
// reasons a caller cannot see and cannot ask about. Comparing the
// marshalled JSON rather than the Go values is deliberate: it is what
// the caller receives, including the omitempty-driven presence
// distinctions reflect.DeepEqual is too strict about.
func TestCrosstabAuxMargin_WireFiguresAgreeAcrossPaths(t *testing.T) {
	schema := auxMarginSchema(t)
	spec := auxMarginSpec()

	buffered := auxWire(t, schema, &types.Request{Crosstab: spec}, auxMarginRecords(schema), false, false)
	fused := auxWire(t, schema, &types.Request{Crosstab: spec}, auxMarginRecords(schema), true, false)

	slice := func(resp *types.Response) string {
		t.Helper()
		ct := resp.Components.Crosstab
		out, err := json.Marshal(map[string]any{
			"row":    ct.RowMarginAggregations,
			"column": ct.ColumnMarginAggregations,
			"grand":  ct.GrandTotalAggregations,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(out)
	}

	if b, f := slice(buffered), slice(fused); b != f {
		t.Errorf("auxiliary margin block differs across dispatch arms\nbuffered: %s\n   fused: %s", b, f)
	}
}

// TestCrosstabAuxMargin_DistinctCountReachablePerSlot is the criterion
// that makes this one scan rather than two.
//
// AGG_DISTINCT_SUM's scalar output is the weighted figure; its distinct
// COUNT rides its ComponentSchema. A caller rendering an unweighted
// respondent base beside a weighted one needs both, and needs them for
// the SAME slot — so the count must be reachable per margin slot rather
// than only as the operator's scalar. If only the scalar surfaced, that
// second row would cost a second aggregation and, in the shape this slot
// exists for, a second scan.
func TestCrosstabAuxMargin_DistinctCountReachablePerSlot(t *testing.T) {
	// Distinct respondents admitted at each slot, alongside the weighted
	// sum the same operator emits as its value.
	type want struct{ sum, distinct float64 }
	cases := map[string]want{
		"grand":      {sum: 17, distinct: 2},
		"row[alpha]": {sum: 17, distinct: 2},
		"column[f]":  {sum: 1, distinct: 1},
		"column[m]":  {sum: 16, distinct: 1},
	}

	for _, fused := range []bool{false, true} {
		name := "buffered"
		if fused {
			name = "fused"
		}
		t.Run(name, func(t *testing.T) {
			schema := auxMarginSchema(t)
			resp := auxWire(t, schema, &types.Request{Crosstab: auxMarginSpec()},
				auxMarginRecords(schema), fused, false)
			ct := resp.Components.Crosstab

			got := map[string]types.MarginAggregationFigure{
				"grand":      auxFigure(t, ct.GrandTotalAggregations, "weighted_base"),
				"row[alpha]": auxFigure(t, ct.RowMarginAggregations[0], "weighted_base"),
				"column[f]":  auxFigure(t, ct.ColumnMarginAggregations[0], "weighted_base"),
				"column[m]":  auxFigure(t, ct.ColumnMarginAggregations[1], "weighted_base"),
			}

			for slot, w := range cases {
				fig := got[slot]
				if v := coerceFloat64(fig.Value); v != w.sum {
					t.Errorf("%s weighted_base value = %v, want %v", slot, v, w.sum)
				}
				if fig.Components == nil {
					t.Fatalf("%s weighted_base carries no components, so the distinct count "+
						"is unreachable and the second rendered row would need its own scan", slot)
				}
				dc, ok := fig.Components["distinct_count"]
				if !ok {
					t.Fatalf("%s weighted_base components have no distinct_count key; got %v",
						slot, fig.Components)
				}
				if v := coerceFloat64(dc); v != w.distinct {
					t.Errorf("%s weighted_base distinct_count = %v, want %v", slot, v, w.distinct)
				}
				// The universal floor rides the same map, over the
				// ADMITTED records — nothing re-scans at emission time,
				// so losing it here loses it entirely.
				if _, ok := fig.Components["n"]; !ok {
					t.Errorf("%s weighted_base components have no n; got %v", slot, fig.Components)
				}
				if _, ok := fig.Components["n_null"]; !ok {
					t.Errorf("%s weighted_base components have no n_null; got %v", slot, fig.Components)
				}
			}
		})
	}
}

// emptySlotSchema / emptySlotRecords build a fixture with a margin slot
// that EXISTS but admits nothing: column `m` is reached by exactly one
// record, whose cell field is null, so the column is a real column with
// real cell-margin counts and an auxiliary base of nothing at all.
//
// The shared admission fixture cannot express this — every one of its
// axis keys admits at least one record — and the state is not exotic: a
// nullable metric plus a thin column produces it on live data.
func emptySlotSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	gender := encoding.NewDictionary()
	for _, g := range []string{"f", "m"} {
		if _, err := gender.Add(g); err != nil {
			t.Fatalf("gender dict.Add: %v", err)
		}
	}
	brand := encoding.NewDictionary()
	if _, err := brand.Add("alpha"); err != nil {
		t.Fatalf("brand dict.Add: %v", err)
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: brand},
			{Name: "gender", Type: encoding.FieldTypeCategoricalU8, Dictionary: gender},
			{Name: "respondent", Type: encoding.FieldTypeF64},
			{Name: "metric", Type: encoding.FieldTypeF64},
		},
	}
}

func emptySlotRecords(schema *encoding.Schema) []*Record {
	return []*Record{
		// column f: admitted.
		NewRecord(schema, map[string]float64{"brand": 0, "gender": 0, "respondent": 1, "metric": 10}),
		// column m: routed, but the cell field is null so nothing is
		// admitted. The column still exists on the axis.
		NewRecord(schema, map[string]float64{"brand": 0, "gender": 1, "respondent": 2}),
	}
}

func emptySlotSpec() *types.CrosstabSpec {
	return &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "gender"}},
		Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "metric"},
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		MarginAggregations: []*types.Aggregation{
			{Type: types.AGG_DISTINCT_COUNT, Field: "respondent", Label: "base"},
		},
	}
}

// TestCrosstabAuxMargin_EmptySlotIsPresentFalseNotZero is the presence
// contract, and it is the one thing about this wire shape a consumer
// cannot recover if it is got wrong.
//
// A slot that admitted no record has no defined aggregate. Emitting 0
// there would be indistinguishable, on the wire, from a genuine zero —
// so a renderer would put a fabricated base beside real cells and
// nothing would say so. The figure therefore carries Present false and
// NO value key at all, while its components still carry the floor, whose
// n = 0 is a true statement about the slot.
//
// The contrast is asserted in the same breath: the CELL aggregator's own
// column margin for that same column still counts the routed record, so
// "the auxiliary saw nothing here" is a measured difference rather than
// an empty response.
func TestCrosstabAuxMargin_EmptySlotIsPresentFalseNotZero(t *testing.T) {
	for _, fused := range []bool{false, true} {
		name := "buffered"
		if fused {
			name = "fused"
		}
		t.Run(name, func(t *testing.T) {
			schema := emptySlotSchema(t)
			resp := auxWire(t, schema, &types.Request{Crosstab: emptySlotSpec()},
				emptySlotRecords(schema), fused, false)
			ct := resp.Components.Crosstab

			if got := resp.Crosstab.Matrix.ColumnKeys; len(got) != 2 {
				t.Fatalf("fixture drift: %d column keys, want 2 (f, m); got %v", len(got), got)
			}
			if len(ct.ColumnMarginAggregations) != 2 {
				t.Fatalf("column_margin_aggregations has %d entries, want 2", len(ct.ColumnMarginAggregations))
			}

			// Column f admitted one record.
			if got := auxScalar(t, auxFigure(t, ct.ColumnMarginAggregations[0], "base"), "column[f] base"); got != 1 {
				t.Errorf("column[f] base = %v, want 1", got)
			}

			// Column m admitted none.
			empty := auxFigure(t, ct.ColumnMarginAggregations[1], "base")
			if empty.Present {
				t.Errorf("column[m] base reports Present true with value %v, but no record was "+
					"admitted to that slot", empty.Value)
			}
			if empty.Value != nil {
				t.Errorf("column[m] base carries value %v; an aggregator over an empty set has no "+
					"defined output and a fabricated figure is indistinguishable from a real one",
					empty.Value)
			}
			if empty.Components == nil {
				t.Fatalf("column[m] base carries no components; n = 0 is a true statement about the slot")
			}
			if n := coerceFloat64(empty.Components["n"]); n != 0 {
				t.Errorf("column[m] base floor n = %v, want 0", n)
			}

			// The contrast: the cell's own column margin still counts
			// the routed record, in its n_null.
			if ct.ColumnMarginCounts[1] != 1 {
				t.Errorf("column[m] cell margin count = %d, want 1 — this effort must not narrow "+
					"the cell aggregator's own margins", ct.ColumnMarginCounts[1])
			}

			// The wire form itself: `present` survives as false rather
			// than vanishing into the same absence a consumer must not
			// read as a figure, and no `value` key is emitted.
			out, err := json.Marshal(ct.ColumnMarginAggregations[1])
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(out), `"present":false`) {
				t.Errorf("empty slot marshals without an explicit present:false — %s", out)
			}
			if strings.Contains(string(out), `"value"`) {
				t.Errorf("empty slot marshals a value key — %s", out)
			}
		})
	}
}

// TestCrosstabAuxMargin_UndeclaredRequestEmitsNothing is the additive
// half of the contract at the processing layer: a crosstab that declares
// no auxiliary must carry no auxiliary key at all — not an empty array,
// not a null, nothing. Anything else changes the bytes every existing
// caller already receives.
func TestCrosstabAuxMargin_UndeclaredRequestEmitsNothing(t *testing.T) {
	for _, fused := range []bool{false, true} {
		name := "buffered"
		if fused {
			name = "fused"
		}
		t.Run(name, func(t *testing.T) {
			schema := auxMarginSchema(t)
			spec := auxMarginSpec()
			spec.MarginAggregations = nil
			resp := auxWire(t, schema, &types.Request{Crosstab: spec}, auxMarginRecords(schema), fused, false)
			out, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, key := range []string{"row_margin_aggregations", "column_margin_aggregations", "grand_total_aggregations"} {
				if strings.Contains(string(out), key) {
					t.Errorf("undeclared request emitted %s: %s", key, out)
				}
			}
		})
	}
}

// TestCrosstabAuxMargin_SuppressedWhenComponentsDisabled holds the
// auxiliary block to the same opt-out every other components block
// obeys. It is not a separate knob: DisableComponents means the block is
// never built, so a figure that leaked out under it would be the one
// piece of components work a caller cannot turn off.
func TestCrosstabAuxMargin_SuppressedWhenComponentsDisabled(t *testing.T) {
	for _, fused := range []bool{false, true} {
		name := "buffered"
		if fused {
			name = "fused"
		}
		t.Run(name, func(t *testing.T) {
			schema := auxMarginSchema(t)
			resp := auxWire(t, schema, &types.Request{Crosstab: auxMarginSpec()},
				auxMarginRecords(schema), fused, true)
			if resp.Components != nil {
				t.Fatalf("components emitted with DisableComponents set: %+v", resp.Components)
			}
			// The matrix itself is untouched by the knob, so the request
			// still answered — this is a suppression, not a failure.
			if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
				t.Fatal("no matrix emitted; the knob suppressed more than components")
			}
		})
	}
}

// TestCrosstabAuxMargin_DisplayFlagGatesEmission pins the auxiliary to
// the SAME emission rule the cell aggregator's own margin counts and
// components follow: a margin computed only because a normalization mode
// needed it is not one the caller asked to see, and stays off the wire.
//
// The alternative — emitting the auxiliary for a margin whose own counts
// and components are withheld — would put a figure on the wire beside no
// margin at all, in a slot MatrixPayload does not render either.
func TestCrosstabAuxMargin_DisplayFlagGatesEmission(t *testing.T) {
	for _, fused := range []bool{false, true} {
		name := "buffered"
		if fused {
			name = "fused"
		}
		t.Run(name, func(t *testing.T) {
			schema := auxMarginSchema(t)
			spec := auxMarginSpec()
			// Row margins are needed as the normalize denominator but
			// never displayed; column and grand stay off entirely.
			spec.Margins = types.CrosstabMargins{}
			spec.Normalize = types.CrosstabNormalizeRow
			if !spec.NeedsRowMargin() {
				t.Fatal("fixture drift: normalize=row no longer needs a row margin, so this " +
					"test would pass vacuously")
			}

			resp := auxWire(t, schema, &types.Request{Crosstab: spec}, auxMarginRecords(schema), fused, false)
			ct := resp.Components.Crosstab
			if ct.RowMarginComponents != nil {
				t.Fatalf("fixture drift: the cell's own row margin components are being emitted, "+
					"so there is no withheld-margin case to test: %v", ct.RowMarginComponents)
			}
			if ct.RowMarginAggregations != nil {
				t.Errorf("row_margin_aggregations emitted for a margin the caller did not ask to "+
					"see: %v", ct.RowMarginAggregations)
			}
			if ct.ColumnMarginAggregations != nil || ct.GrandTotalAggregations != nil {
				t.Errorf("auxiliary emitted for an unrequested margin: cols=%v grand=%v",
					ct.ColumnMarginAggregations, ct.GrandTotalAggregations)
			}
		})
	}
}
