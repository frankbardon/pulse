package processing

import (
	stderrors "errors"
	"math"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The panel's DISTINCT-KEY within-prefix n leg (E4-S3).
//
// row_margin_distinct_within reads each slot's row margin out of the
// margin COMPONENTS as the cell aggregator's distinct-key
// cardinality. Its `_value_` sibling reads the margin CELL's VALUE off
// the payload. A different CARRIER, which is why it is not a variant
// spelling of the same leg.
//
// Three properties carry the story: the number it produces is the
// RESPONDENT count where the record-counted leg is inflated; a slot
// whose cell aggregator cannot carry a distinct-key figure is refused
// UP FRONT rather than silently read; and slots that disagree about
// what they are counting refuse the whole spec rather than pairing two
// legs in different units.

// panelAggFixture is one cell aggregator's observable component
// footprint: the OPERATOR key set its cells emit (the identity the
// admission gate matches on, exactly) and the key its row-margin
// figure rides on.
//
// The values are restatements of the same ComponentSchema halves
// cellAggregatorIdentitySignatures restates, chosen so the fixtures
// exercise the gate against real shapes rather than invented ones.
type panelAggFixture struct {
	cell      map[string]any
	marginKey string
}

var panelAggFixtures = map[types.AggregationType]panelAggFixture{
	// Admitted. Its distinct_count counts KEYS that contributed to the sum.
	types.AGG_DISTINCT_SUM: {
		cell:      map[string]any{"n": 1.0, "sum": 1.0, "distinct_count": 1.0},
		marginKey: "distinct_count",
	},
	// Admitted. Its cardinality counts distinct non-null VALUES.
	types.AGG_DISTINCT_COUNT: {
		cell:      map[string]any{"n": 1.0, "cardinality": 1.0},
		marginKey: "cardinality",
	},
	// NOT admitted: {sum} matches no distinct-bearing signature, so it
	// classifies as unidentified.
	types.AGG_SUM: {
		cell:      map[string]any{"n": 1.0, "sum": 1.0},
		marginKey: "distinct_count",
	},
	// NOT admitted, and the reason the gate matches on IDENTITY rather
	// than key presence: AGG_FREQUENCY emits a key literally spelled
	// distinct_count, but it counts distinct VALUES of the measure
	// field — answer codes, not respondents.
	types.AGG_FREQUENCY: {
		cell:      map[string]any{"n": 1.0, "distinct_count": 1.0, "mode_value": 1.0, "mode_count": 1.0},
		marginKey: "distinct_count",
	},
}

// withPanelMarginComponents attaches a CrosstabComponents block whose
// cells all carry `agg`'s operator key set and whose row margins carry
// `agg`'s distinct-key figure. A NaN entry in `marginDistinct` emits a
// NIL margin map — the margin that was never emitted, which must skip
// the coordinate rather than read as a zero-sized sample.
func withPanelMarginComponents(resp *types.Response, agg types.AggregationType, marginDistinct [3]float64) *types.Response {
	fx, ok := panelAggFixtures[agg]
	if !ok {
		panic("no panel fixture for aggregator " + string(agg))
	}
	rows := len(resp.Crosstab.Matrix.RowKeys)
	cells := make([][]map[string]any, rows)
	for i := 0; i < rows; i++ {
		row := make([]map[string]any, 3)
		for j := 0; j < 3; j++ {
			cell := make(map[string]any, len(fx.cell))
			for k, v := range fx.cell {
				cell[k] = v
			}
			row[j] = cell
		}
		cells[i] = row
	}
	margins := make([]map[string]any, rows)
	for i := 0; i < rows && i < len(marginDistinct); i++ {
		if math.IsNaN(marginDistinct[i]) {
			margins[i] = nil
			continue
		}
		margins[i] = map[string]any{fx.marginKey: marginDistinct[i]}
	}
	resp.Components = &types.ResponseComponents{
		Crosstab: &types.CrosstabComponents{
			CellComponents:      cells,
			RowMarginComponents: margins,
		},
	}
	return resp
}

// distinctSpec builds the two-slot panel spec every test below drives.
func distinctSpec(params map[string]any) types.ComposeOverlaySpec {
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	spec.Params = params
	return spec
}

// assertPanelShapeRefusal unwraps the coded error, checks the code and
// returns it so each test can assert on its own Details.
func assertPanelShapeRefusal(t *testing.T, err error) *pulseerrors.CodedError {
	t.Helper()
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	var ce *pulseerrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if ce.Code != pulseerrors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {
		t.Fatalf("Code = %q, want %q", ce.Code, pulseerrors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
	}
	return ce
}

// --- The filed bug, on the panel ------------------------------------

// The END-TO-END story, mirroring E1's crosstab fixture. Every
// respondent contributes TWO records, so the record-counted row margin
// is exactly twice the respondent count. The record-counted leg tests
// against the inflated denominator; the distinct leg tests against the
// true respondent base.
//
// Both legs read the SAME slot, at the SAME coordinate, over the SAME
// slab scope (omitted depth, no summing). They differ only in carrier,
// which is what makes the two numbers directly comparable — and they
// MUST differ, or the mode bought nothing.
func TestApplyPropZPanel_DistinctWithinGivesRespondentsNotRecords(t *testing.T) {
	// 100 respondents × 2 records = 200 rows on the reference;
	// 80 × 2 = 160 on the target.
	build := func() (*types.Response, *types.Response) {
		ref := withPanelMarginComponents(
			makeMatrixWithRowMargins(
				[3][3]float64{{50, 50, 50}, {50, 50, 50}, {50, 50, 50}},
				[3]float64{200, 200, 200},
			), types.AGG_DISTINCT_SUM, [3]float64{100, 100, 100})
		target := withPanelMarginComponents(
			makeMatrixWithRowMargins(
				[3][3]float64{{60, 60, 60}, {60, 60, 60}, {60, 60, 60}},
				[3]float64{160, 160, 160},
			), types.AGG_DISTINCT_SUM, [3]float64{80, 80, 80})
		return ref, target
	}

	wantRecords, ok := twoProportionZ(50, 200, 60, 160)
	if !ok {
		t.Fatal("record-counted expectation is degenerate")
	}
	wantRespondents, ok := twoProportionZ(50, 100, 60, 80)
	if !ok {
		t.Fatal("respondent-counted expectation is degenerate")
	}
	if math.Abs(wantRecords-wantRespondents) < 1e-9 {
		t.Fatalf("fixture cannot discriminate: the inflated (%v) and true (%v) p-values coincide",
			wantRecords, wantRespondents)
	}

	ref, target := build()
	inflated, warns, err := applyPropZPanel(
		ptrSpec(distinctSpec(map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin})),
		ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("record-counted leg: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("record-counted leg warned: %v", warns)
	}

	ref, target = build()
	trueN, warns, err := applyPropZPanel(
		ptrSpec(distinctSpec(map[string]any{"n_source": types.PanelNSourceRowMarginDistinctWithin})),
		ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("distinct leg: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("distinct leg warned: %v", warns)
	}

	gotInflated := pairs(t, inflated, 0, 0)[0]
	gotTrue := pairs(t, trueN, 0, 0)[0]
	if math.IsNaN(gotInflated) || math.IsNaN(gotTrue) {
		t.Fatalf("NaN p-value (records %v, respondents %v)", gotInflated, gotTrue)
	}
	if math.Abs(gotInflated-wantRecords) > 1e-12 {
		t.Errorf("record-counted p = %v, want %v (n = 200 / 160)", gotInflated, wantRecords)
	}
	if math.Abs(gotTrue-wantRespondents) > 1e-12 {
		t.Errorf("distinct p = %v, want %v (n = 100 / 80 respondents)", gotTrue, wantRespondents)
	}
	if gotInflated == gotTrue {
		t.Fatal("the distinct leg reproduced the record-counted number; it is reading the payload margin, not the margin components")
	}
}

// --- Slab scope -------------------------------------------------------

// Omitted depth reads the EXACT per-slot distinct margin and sums
// nothing; an explicit depth sums the prefix slab. The two must be
// different numbers, and the singleton slab must agree with the
// unsummed margin — an implementation that summed the whole axis would
// pass the first assertion and fail the second.
func TestApplyPropZPanel_DistinctWithinDepthSumsOnlyThePrefixSlab(t *testing.T) {
	build := func() (*types.Response, *types.Response) {
		ref := withPanelMarginComponents(
			withRowKeys(makeMatrixWithRowMargins(
				[3][3]float64{{50, 50, 50}, {25, 25, 25}, {40, 40, 40}},
				[3]float64{999, 999, 999}, // payload margins are decoys here
			), twoDimRowKeys), types.AGG_DISTINCT_SUM, [3]float64{100, 60, 80})
		target := withPanelMarginComponents(
			withRowKeys(makeMatrixWithRowMargins(
				[3][3]float64{{60, 55, 45}, {20, 20, 20}, {30, 65, 50}},
				[3]float64{999, 999, 999},
			), twoDimRowKeys), types.AGG_DISTINCT_SUM, [3]float64{90, 30, 50})
		return ref, target
	}

	// Row 0 is ("a","x"): exact distinct margins 100 / 90; the dim-0
	// slab {("a","x"), ("a","y")} is 160 / 120.
	wantExact, ok := twoProportionZ(50, 100, 60, 90)
	if !ok {
		t.Fatal("exact expectation degenerate")
	}
	wantSlab, ok := twoProportionZ(50, 160, 60, 120)
	if !ok {
		t.Fatal("slab expectation degenerate")
	}
	if wantExact == wantSlab {
		t.Fatal("fixture cannot distinguish the exact distinct margin from the depth-0 slab")
	}

	ref, target := build()
	omitted, _, err := applyPropZPanel(
		ptrSpec(distinctSpec(map[string]any{"n_source": types.PanelNSourceRowMarginDistinctWithin})),
		ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("omitted depth: %v", err)
	}
	ref, target = build()
	zero, _, err := applyPropZPanel(
		ptrSpec(distinctSpec(map[string]any{
			"n_source":       types.PanelNSourceRowMarginDistinctWithin,
			"n_within_depth": 0,
		})), ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("depth 0: %v", err)
	}

	approxEqual(t, "omitted-depth p", pairs(t, omitted, 0, 0)[0], wantExact, 1e-12)
	approxEqual(t, "depth-0 p", pairs(t, zero, 0, 0)[0], wantSlab, 1e-12)
	if pairs(t, omitted, 0, 0)[0] == pairs(t, zero, 0, 0)[0] {
		t.Fatal("omitted depth and depth 0 collapsed to the same number")
	}

	// Row 2 is ("b","x") — the only row with dim 0 = "b", so its slab
	// is itself and the denominator stays the unsummed margin.
	// Summing the whole axis would give 240 / 170.
	wantRow2, ok := twoProportionZ(40, 80, 30, 50)
	if !ok {
		t.Fatal("row 2 expectation degenerate")
	}
	wholeAxis, _ := twoProportionZ(40, 240, 30, 170)
	if wantRow2 == wholeAxis {
		t.Fatal("fixture cannot distinguish a singleton slab from the whole axis")
	}
	got := pairs(t, zero, 2, 0)[0]
	if math.IsNaN(got) || math.Abs(got-wantRow2) > 1e-12 {
		t.Fatalf("row 2 depth-0 p = %v, want %v (singleton slab); the whole-axis misread gives %v",
			got, wantRow2, wholeAxis)
	}
}

// --- Admission --------------------------------------------------------

// A cell aggregator that carries no distinct-KEY figure is refused UP
// FRONT, naming the observed aggregator and the admitted set — never
// silently read as a zero, and never per coordinate.
func TestApplyPropZPanel_DistinctWithinRefusesNonDistinctAggregator(t *testing.T) {
	for _, agg := range []types.AggregationType{types.AGG_SUM, types.AGG_FREQUENCY} {
		t.Run(string(agg), func(t *testing.T) {
			ref := withPanelMarginComponents(
				makeMatrixWithRowMargins(
					[3][3]float64{{50, 50, 50}, {50, 50, 50}, {50, 50, 50}},
					[3]float64{200, 200, 200},
				), agg, [3]float64{100, 100, 100})
			target := withPanelMarginComponents(
				makeMatrixWithRowMargins(
					[3][3]float64{{60, 60, 60}, {60, 60, 60}, {60, 60, 60}},
					[3]float64{160, 160, 160},
				), agg, [3]float64{80, 80, 80})

			layer, _, err := applyPropZPanel(
				ptrSpec(distinctSpec(map[string]any{"n_source": types.PanelNSourceRowMarginDistinctWithin})),
				ref, []*types.Response{target}, 0, []int{1})
			ce := assertPanelShapeRefusal(t, err)
			if layer.Payload.Matrix != nil {
				t.Error("a refused spec still emitted a matrix")
			}
			wantObserved := string(agg)
			if agg == types.AGG_SUM {
				// {sum} matches no distinct-bearing signature.
				wantObserved = "unidentified"
			}
			if ce.Details["observed_cell_aggregator"] != wantObserved {
				t.Errorf("Details[observed_cell_aggregator] = %v, want %q",
					ce.Details["observed_cell_aggregator"], wantObserved)
			}
			if ce.Details["panel_index"] != 0 {
				t.Errorf("Details[panel_index] = %v, want 0", ce.Details["panel_index"])
			}
			// The margin the gate refused to read carries a key
			// literally spelled distinct_count under AGG_FREQUENCY.
			// That is the whole reason admission is by identity: a
			// key-presence probe would have read 100 / 80 here and
			// called them respondent counts.
			if agg == types.AGG_FREQUENCY && !strings.Contains(ce.Message, "AGG_FREQUENCY") {
				t.Errorf("message does not name the observed aggregator: %s", ce.Message)
			}
		})
	}
}

// The story's headline risk. Slots whose cell aggregators DIFFER —
// one AGG_DISTINCT_SUM, one AGG_SUM — refuse the WHOLE spec, not the
// offending slot. Dropping a slot would change M, and M sets the
// length and pair ordering of every cell's flattened upper-triangular
// vector: the caller would get a shorter vector with no way to tell
// which slot left.
func TestApplyPropZPanel_DistinctWithinRefusesWholesaleOnDivergentSlots(t *testing.T) {
	ref := withPanelMarginComponents(
		makeMatrixWithRowMargins(
			[3][3]float64{{50, 50, 50}, {50, 50, 50}, {50, 50, 50}},
			[3]float64{200, 200, 200},
		), types.AGG_DISTINCT_SUM, [3]float64{100, 100, 100})
	// The TARGET is the offender, so a per-slot drop would have left a
	// one-slot panel that emits nothing rather than an error.
	target := withPanelMarginComponents(
		makeMatrixWithRowMargins(
			[3][3]float64{{60, 60, 60}, {60, 60, 60}, {60, 60, 60}},
			[3]float64{160, 160, 160},
		), types.AGG_SUM, [3]float64{80, 80, 80})

	layer, warns, err := applyPropZPanel(
		ptrSpec(distinctSpec(map[string]any{"n_source": types.PanelNSourceRowMarginDistinctWithin})),
		ref, []*types.Response{target}, 3, []int{7})
	ce := assertPanelShapeRefusal(t, err)
	if layer.Payload.Matrix != nil || warns != nil {
		t.Error("the spec was refused wholesale, so no layer and no warnings may be emitted")
	}
	if ce.Details["panel_index"] != 1 {
		t.Errorf("Details[panel_index] = %v, want 1 (the target)", ce.Details["panel_index"])
	}
	if ce.Details["slot_index"] != 7 {
		t.Errorf("Details[slot_index] = %v, want the authored Compose slot 7", ce.Details["slot_index"])
	}
	if ce.Details["slot_label"] != "t0" {
		t.Errorf("Details[slot_label] = %v, want t0", ce.Details["slot_label"])
	}
	if ce.Details["observed_cell_aggregator"] != "unidentified" {
		t.Errorf("Details[observed_cell_aggregator] = %v, want unidentified",
			ce.Details["observed_cell_aggregator"])
	}
}

// Both slots admitted, but counting DIFFERENT things:
// AGG_DISTINCT_SUM's distinct_count counts keys that summed, while
// AGG_DISTINCT_COUNT's cardinality counts distinct non-null values.
// Neither figure is wrong — but pairing them puts the two legs of one
// test in different units, which is the silent wrong number this
// effort exists to remove. Refused wholesale, same code.
func TestApplyPropZPanel_DistinctWithinRefusesMixedAdmittedAggregators(t *testing.T) {
	ref := withPanelMarginComponents(
		makeMatrixWithRowMargins(
			[3][3]float64{{50, 50, 50}, {50, 50, 50}, {50, 50, 50}},
			[3]float64{200, 200, 200},
		), types.AGG_DISTINCT_SUM, [3]float64{100, 100, 100})
	target := withPanelMarginComponents(
		makeMatrixWithRowMargins(
			[3][3]float64{{60, 60, 60}, {60, 60, 60}, {60, 60, 60}},
			[3]float64{160, 160, 160},
		), types.AGG_DISTINCT_COUNT, [3]float64{80, 80, 80})

	// Both legs are individually readable — 100 and 80 — so nothing
	// but the homogeneity rule stops this producing a plausible,
	// wrong, finite p-value.
	if _, ok := twoProportionZ(50, 100, 60, 80); !ok {
		t.Fatal("fixture would not have produced a finite p-value; it cannot demonstrate the hazard")
	}

	_, _, err := applyPropZPanel(
		ptrSpec(distinctSpec(map[string]any{"n_source": types.PanelNSourceRowMarginDistinctWithin})),
		ref, []*types.Response{target}, 0, []int{1})
	ce := assertPanelShapeRefusal(t, err)
	if ce.Details["reference_cell_aggregator"] != string(types.AGG_DISTINCT_SUM) {
		t.Errorf("Details[reference_cell_aggregator] = %v, want %s",
			ce.Details["reference_cell_aggregator"], types.AGG_DISTINCT_SUM)
	}
	if ce.Details["observed_cell_aggregator"] != string(types.AGG_DISTINCT_COUNT) {
		t.Errorf("Details[observed_cell_aggregator] = %v, want %s",
			ce.Details["observed_cell_aggregator"], types.AGG_DISTINCT_COUNT)
	}
	if !strings.Contains(ce.Message, "different units") {
		t.Errorf("message does not explain the unit mismatch: %s", ce.Message)
	}

	// A HOMOGENEOUS panel on the other admitted aggregator still runs,
	// so the refusal is about divergence and not about
	// AGG_DISTINCT_COUNT being unwelcome.
	ref = withPanelMarginComponents(
		makeMatrixWithRowMargins(
			[3][3]float64{{50, 50, 50}, {50, 50, 50}, {50, 50, 50}},
			[3]float64{200, 200, 200},
		), types.AGG_DISTINCT_COUNT, [3]float64{100, 100, 100})
	if _, _, err := applyPropZPanel(
		ptrSpec(distinctSpec(map[string]any{"n_source": types.PanelNSourceRowMarginDistinctWithin})),
		ref, []*types.Response{target}, 0, []int{1}); err != nil {
		t.Fatalf("a homogeneous AGG_DISTINCT_COUNT panel was refused: %v", err)
	}
}

// --- Components and unreadable margins --------------------------------

// A slot whose components are DISABLED cannot serve a distinct n. It
// must refuse with the configuration code, not read a zero — the
// figure exists, the run was told not to emit it.
func TestApplyPropZPanel_DistinctWithinRefusesComponentsDisabledSlot(t *testing.T) {
	ref := withPanelMarginComponents(
		makeMatrixWithRowMargins(
			[3][3]float64{{50, 50, 50}, {50, 50, 50}, {50, 50, 50}},
			[3]float64{200, 200, 200},
		), types.AGG_DISTINCT_SUM, [3]float64{100, 100, 100})
	// No components at all: the DisableComponents opt-out.
	target := makeMatrixWithRowMargins(
		[3][3]float64{{60, 60, 60}, {60, 60, 60}, {60, 60, 60}},
		[3]float64{160, 160, 160},
	)
	if target.Components != nil {
		t.Fatal("fixture gained components; it must stay components-free")
	}

	_, _, err := applyPropZPanel(
		ptrSpec(distinctSpec(map[string]any{"n_source": types.PanelNSourceRowMarginDistinctWithin})),
		ref, []*types.Response{target}, 0, []int{1})
	if err == nil {
		t.Fatal("a components-disabled slot was accepted; the distinct leg would read a fabricated zero")
	}
	var ce *pulseerrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if ce.Code != pulseerrors.PULSE_OVERLAY_COMPONENTS_REQUIRED {
		t.Fatalf("Code = %q, want %q (a configuration condition, not a shape one)",
			ce.Code, pulseerrors.PULSE_OVERLAY_COMPONENTS_REQUIRED)
	}
	if ce.Details["state"] != ComposeComponentsDisabled.String() {
		t.Errorf("Details[state] = %v, want %q", ce.Details["state"], ComposeComponentsDisabled.String())
	}
}

// A row whose margin components were never emitted is NOT a
// zero-sized margin. The coordinate skips with an n_missing warning,
// and the skip does not leak to the rows whose margins are present.
func TestApplyPropZPanel_DistinctWithinUnemittedMarginSkips(t *testing.T) {
	ref := withPanelMarginComponents(
		makeMatrixWithRowMargins(
			[3][3]float64{{50, 50, 50}, {50, 50, 50}, {50, 50, 50}},
			[3]float64{200, 200, 200},
		), types.AGG_DISTINCT_SUM, [3]float64{math.NaN(), 100, 100})
	target := withPanelMarginComponents(
		makeMatrixWithRowMargins(
			[3][3]float64{{60, 60, 60}, {60, 60, 60}, {60, 60, 60}},
			[3]float64{160, 160, 160},
		), types.AGG_DISTINCT_SUM, [3]float64{80, 80, 80})

	layer, warns, err := applyPropZPanel(
		ptrSpec(distinctSpec(map[string]any{"n_source": types.PanelNSourceRowMarginDistinctWithin})),
		ref, []*types.Response{target}, 3, []int{7})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	mx := layer.Payload.Matrix
	if mx == nil {
		t.Fatal("layer matrix nil")
	}
	if mx.Cells[0][0].Present {
		t.Fatalf("coordinate (0,0) emitted %v; an unemitted distinct margin must skip, not read as 0",
			mx.Cells[0][0].Value)
	}
	if !mx.Cells[1][0].Present {
		t.Fatal("the skip leaked beyond the unreadable row")
	}
	var found bool
	for _, w := range warns {
		if w.Details["n_missing"] != true {
			continue
		}
		found = true
		if w.Details["n_source"] != types.PanelNSourceRowMarginDistinctWithin {
			t.Errorf("Details[n_source] = %v, want %q", w.Details["n_source"],
				types.PanelNSourceRowMarginDistinctWithin)
		}
		if w.Details["slot_index"] != 3 {
			t.Errorf("Details[slot_index] = %v, want 3 (the reference's authored slot)", w.Details["slot_index"])
		}
	}
	if !found {
		t.Fatalf("no n_missing warning; got %+v", warns)
	}
}

// ptrSpec is the &spec dance every call site needs; a local helper
// keeps the tests above readable.
func ptrSpec(spec types.ComposeOverlaySpec) *types.ComposeOverlaySpec {
	return &spec
}
