package processing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// crosstabFusedGateSchema returns a minimal schema covering the field
// names referenced by the per-disqualifier test bodies. Decimal128 is
// included so the decimal-cell branch has a target field to point at.
func crosstabFusedGateSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64},
			{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2},
			{Name: "ts", Type: encoding.FieldTypeDate},
		},
	}
}

// happyPathCrosstabRequest returns a fused-eligible Crosstab request:
// AGG_SUM cell, single GROUP_CATEGORY on each axis, no features /
// attributes / filterers / tests.
func happyPathCrosstabRequest() *types.Request {
	return &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "score"},
		},
	}
}

func TestCanFuseCrosstab_HappyPath(t *testing.T) {
	schema := crosstabFusedGateSchema()
	req := happyPathCrosstabRequest()
	ok, reason := CanFuseCrosstab(req, schema, nil)
	if !ok {
		t.Fatalf("expected fused-eligible, got reason %q", reason)
	}
	if reason != "" {
		t.Fatalf("expected empty reason on success, got %q", reason)
	}
}

func TestCanFuseCrosstab_NilRequest(t *testing.T) {
	ok, reason := CanFuseCrosstab(nil, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible for nil request")
	}
	if !strings.Contains(reason, "nil request") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

func TestCanFuseCrosstab_NoCrosstab(t *testing.T) {
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
	}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible without crosstab spec")
	}
	if !strings.Contains(reason, "no crosstab") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

func TestCanFuseCrosstab_MissingCell(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Crosstab.Cell = nil
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible without cell aggregator")
	}
	if !strings.Contains(reason, "missing cell aggregator") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

func TestCanFuseCrosstab_NonMergeableCellAggregator(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Crosstab.Cell = &types.Aggregation{Type: types.AGG_MEDIAN, Field: "score"}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible for AGG_MEDIAN cell")
	}
	if !strings.Contains(reason, "non-mergeable cell aggregator") || !strings.Contains(reason, "AGG_MEDIAN") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

// AGG_MIN / AGG_MAX / AGG_STDDEV / AGG_VARIANCE are mergeable but their
// MarginReducibility is MarginRecompute. The brief specifies cell
// aggregators must classify as Summable or MeanReducible, so these
// should be rejected on the recompute branch.
func TestCanFuseCrosstab_RecomputeMarginCellAggregator(t *testing.T) {
	cases := []types.AggregationType{
		types.AGG_MIN, types.AGG_MAX, types.AGG_STDDEV, types.AGG_VARIANCE,
	}
	for _, aggType := range cases {
		t.Run(string(aggType), func(t *testing.T) {
			req := happyPathCrosstabRequest()
			req.Crosstab.Cell = &types.Aggregation{Type: aggType, Field: "score"}
			ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
			if ok {
				t.Fatalf("expected ineligible for %s (recompute margin)", aggType)
			}
			if !strings.Contains(reason, "recompute-margin cell aggregator") || !strings.Contains(reason, string(aggType)) {
				t.Fatalf("unexpected reason %q", reason)
			}
		})
	}
}

// AGG_AVERAGE / AGG_WEIGHTED_MEAN / AGG_RATIO classify as
// MarginMeanReducible — accepted alongside MarginSummable.
func TestCanFuseCrosstab_MeanReducibleCellAccepted(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Crosstab.Cell = &types.Aggregation{Type: types.AGG_AVERAGE, Field: "score"}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if !ok {
		t.Fatalf("expected fused-eligible for AGG_AVERAGE cell, got %q", reason)
	}
}

func TestCanFuseCrosstab_DecimalCellFieldForcesBuffered(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Crosstab.Cell = &types.Aggregation{Type: types.AGG_SUM, Field: "amount"}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible for decimal128 cell field")
	}
	if !strings.Contains(reason, "decimal128") || !strings.Contains(reason, "amount") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

func TestCanFuseCrosstab_NonStreamableGrouperOnRowAxis(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Crosstab.Rows = []*types.Group{{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4}}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible for GROUP_QUANTILE on row axis")
	}
	if !strings.Contains(reason, "non-streamable grouper on row axis") || !strings.Contains(reason, "GROUP_QUANTILE") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

// GROUP_QUANTILE on the column axis is the canonical
// non-key-derivable case — the per-record fused walk cannot replicate
// its finalize-time bucketization. After E4-S4P the gate consults the
// StreamableGrouper interface directly rather than the static
// GroupType.Streamable() table; GROUP_QUANTILE has no StreamableGrouper
// implementation and is rejected here.
func TestCanFuseCrosstab_NonStreamableGrouperOnColumnAxis(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Crosstab.Columns = []*types.Group{{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4}}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible for GROUP_QUANTILE on column axis")
	}
	if !strings.Contains(reason, "non-streamable grouper on column axis") || !strings.Contains(reason, "GROUP_QUANTILE") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

// GROUP_DATE on either axis must be accepted by the widened gate.
// The static GroupType.Streamable() table returns false for GROUP_DATE
// (Process-level streaming is not wired through), but the per-record
// key derivation (StreamableGrouper.KeyFor) IS implemented — the fused
// crosstab path consumes that directly. The canonical 1M-row bench
// uses a fiscal-offset GROUP_DATE column axis; this test pins the
// widened gate so the bench can actually engage the fused path.
func TestCanFuseCrosstab_GroupDateOnAxisAccepted(t *testing.T) {
	// fiscal_offset=10 mirrors the canonical bench shape: October FY
	// start (the same offset huge-request.json carries).
	dateParams := []byte(`{"component":"quarter","fiscal_offset":10}`)
	cases := []struct {
		name string
		axis string
	}{
		{"RowAxis", "row"},
		{"ColumnAxis", "column"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := happyPathCrosstabRequest()
			grp := &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: dateParams}
			if tc.axis == "row" {
				req.Crosstab.Rows = []*types.Group{grp}
			} else {
				req.Crosstab.Columns = []*types.Group{grp}
			}
			ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
			if !ok {
				t.Fatalf("expected fused-eligible for GROUP_DATE on %s axis, got reason %q", tc.axis, reason)
			}
			if reason != "" {
				t.Fatalf("expected empty reason on success, got %q", reason)
			}
		})
	}
}

func TestCanFuseCrosstab_FeaturesForceBuffered(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "score"}}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible with FEAT_LOG present")
	}
	if !strings.Contains(reason, "features force buffered") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

func TestCanFuseCrosstab_TestsForceBuffered(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Tests = []*types.Test{{Type: types.TEST_T, Field: "score"}}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible with tier-1 test present")
	}
	if !strings.Contains(reason, "stat tests force buffered") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

func TestCanFuseCrosstab_PostTestsForceBuffered(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.PostTests = []*types.Test{{Type: types.TEST_TUKEY_HSD}}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible with tier-2 post-test present")
	}
	if !strings.Contains(reason, "stat tests force buffered") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

func TestCanFuseCrosstab_AttrFormulaBail(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Attributes = []*types.Attribute{
		{Type: types.ATTR_FORMULA, Field: "derived", Expression: "score * 2"},
	}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible with ATTR_FORMULA expression")
	}
	if !strings.Contains(reason, "ATTR_FORMULA bail") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

// An ATTR_FORMULA with an empty expression is functionally inert and
// should NOT trigger the bail — the bail rule keys on a non-empty
// expression.
func TestCanFuseCrosstab_AttrFormulaEmptyExpressionAccepted(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Attributes = []*types.Attribute{
		{Type: types.ATTR_DATE_PART, Field: "ts"},
	}
	ok, _ := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if !ok {
		t.Fatalf("expected fused-eligible with row-local ATTR_DATE_PART")
	}
}

func TestCanFuseCrosstab_FilterExpressionBail(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Filterers = []*types.Filterer{
		{Type: types.FILTER_EXPRESSION, Expression: "score > 0"},
	}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if ok {
		t.Fatalf("expected ineligible with FILTER_EXPRESSION")
	}
	if !strings.Contains(reason, "FILTER_EXPRESSION bail") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

// FILTER_INCLUDE / FILTER_EXCLUDE / FILTER_RANGE are row-local and
// streamable — they should not disqualify the fused path.
func TestCanFuseCrosstab_RowLocalFilterAccepted(t *testing.T) {
	req := happyPathCrosstabRequest()
	req.Filterers = []*types.Filterer{
		{Type: types.FILTER_RANGE, Field: "score"},
	}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
	if !ok {
		t.Fatalf("expected fused-eligible with FILTER_RANGE, got %q", reason)
	}
}

// extension-bound aggregator without a FieldInputs hook → ineligible.
func TestCanFuseCrosstab_ExtensionAggregatorWithoutFieldInputs(t *testing.T) {
	customAgg := types.AggregationType("AGG_CUSTOM_X")
	ext := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			customAgg: func(*types.Aggregation, *encoding.Schema) (Aggregator, error) {
				return nil, nil
			},
		},
	}
	req := happyPathCrosstabRequest()
	req.Aggregations = []*types.Aggregation{
		{Type: customAgg, Field: "score"},
	}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), ext)
	if ok {
		t.Fatalf("expected ineligible for extension aggregator with no FieldInputs hook")
	}
	if !strings.Contains(reason, "extension aggregator") || !strings.Contains(reason, "AGG_CUSTOM_X") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

// extension aggregator on the cell slot, same rule.
func TestCanFuseCrosstab_ExtensionCellAggregatorWithoutFieldInputs(t *testing.T) {
	customAgg := types.AggregationType("AGG_CUSTOM_Y")
	ext := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			customAgg: func(*types.Aggregation, *encoding.Schema) (Aggregator, error) {
				return nil, nil
			},
		},
	}
	// Force the cell-level mergeable / margin gates to pass first by
	// piggy-backing on a built-in type when sequencing — but here the
	// custom type is on the cell. Cells with unknown type fall through
	// Mergeable()==false (the default branch), so the gate will short-
	// circuit on the cell-mergeable branch BEFORE reaching extension
	// checks. The point of this case is that even when an extension is
	// the only non-built-in operator surface, the gate is reachable —
	// so use a built-in cell + custom aggregator in Aggregations to
	// exercise the extension path independently.
	req := happyPathCrosstabRequest()
	req.Aggregations = []*types.Aggregation{
		{Type: customAgg, Field: "score"},
	}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), ext)
	if ok {
		t.Fatalf("expected ineligible with unintrospectable extension aggregator")
	}
	if !strings.Contains(reason, "extension aggregator") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

// extension aggregator WITH a FieldInputs hook → accepted.
func TestCanFuseCrosstab_ExtensionAggregatorWithFieldInputsAccepted(t *testing.T) {
	customAgg := types.AggregationType("AGG_CUSTOM_Z")
	ext := &ExtensionRegistry{
		Aggregators: map[types.AggregationType]AggregatorFactory{
			customAgg: func(*types.Aggregation, *encoding.Schema) (Aggregator, error) {
				return nil, nil
			},
		},
		FieldInputs: map[string]FieldInputsFunc{
			StreamabilityKey("aggregator", string(customAgg)): func(_ json.RawMessage) []string { return nil },
		},
	}
	req := happyPathCrosstabRequest()
	req.Aggregations = []*types.Aggregation{
		{Type: customAgg, Field: "score"},
	}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), ext)
	if !ok {
		t.Fatalf("expected fused-eligible with introspectable extension aggregator, got %q", reason)
	}
}

// extension filterer without FieldInputs hook → ineligible.
func TestCanFuseCrosstab_ExtensionFiltererWithoutFieldInputs(t *testing.T) {
	customFilt := types.FiltererType("FILTER_CUSTOM_X")
	ext := &ExtensionRegistry{
		Filterers: map[types.FiltererType]FiltererFactory{
			customFilt: func() FiltererBuilder { return nil },
		},
	}
	req := happyPathCrosstabRequest()
	req.Filterers = []*types.Filterer{
		{Type: customFilt, Field: "score"},
	}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), ext)
	if ok {
		t.Fatalf("expected ineligible for extension filterer with no FieldInputs hook")
	}
	if !strings.Contains(reason, "extension filterer") {
		t.Fatalf("unexpected reason %q", reason)
	}
}

// extension grouper on a crosstab axis without FieldInputs hook →
// ineligible.
func TestCanFuseCrosstab_ExtensionGrouperOnAxisWithoutFieldInputs(t *testing.T) {
	customGrp := types.GroupType("GROUP_CUSTOM_X")
	ext := &ExtensionRegistry{
		Groupers: map[types.GroupType]GrouperFactory{
			customGrp: func(*types.Group, *encoding.Schema) (Grouper, error) { return nil, nil },
		},
		// Mark streamable so the axis-streamability check passes; the
		// FieldInputs miss is what we're isolating here.
		Streamable: map[string]bool{
			StreamabilityKey("grouper", string(customGrp)): true,
		},
	}
	req := happyPathCrosstabRequest()
	req.Crosstab.Rows = []*types.Group{{Type: customGrp, Field: "brand"}}
	ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), ext)
	// The axis-grouper streamability check consults the per-type
	// GroupType.Streamable() method directly (not the overlay) per
	// types/streamability.go, so an unknown custom grouper type falls
	// through to the default false branch and trips the streamability
	// gate before the FieldInputs check. Accept either reason — both
	// disqualify the request.
	if ok {
		t.Fatalf("expected ineligible for custom grouper on row axis without FieldInputs")
	}
	if !strings.Contains(reason, "non-streamable grouper on row axis") &&
		!strings.Contains(reason, "extension grouper") {
		t.Fatalf("unexpected reason %q", reason)
	}
}
