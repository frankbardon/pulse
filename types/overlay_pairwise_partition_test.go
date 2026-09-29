package types

import (
	"strings"
	"testing"
)

// Unit coverage for CheckPairwiseSlabPartition — the shared predicate
// behind both arms of the distinct-key slab partition gate. The two
// arms (descriptor.validateOverlayPairwise, processing's crosstab
// overlay hook) are tested end-to-end in their own packages; this pins
// the decision table so a wrong ACCEPT or a wrong REFUSE shows up here
// with the offending shape named.

func pwPartCT(rows, cols []*Group) *CrosstabSpec {
	return &CrosstabSpec{
		Rows:    rows,
		Columns: cols,
		Cell:    &Aggregation{Type: AGG_DISTINCT_SUM, Field: "value"},
	}
}

func TestCheckPairwiseSlabPartition_Table(t *testing.T) {
	fanOut := func(field string) *Group { return &Group{Type: GROUP_SET_PER_ELEMENT, Field: field} }
	flat := func(field string) *Group { return &Group{Type: GROUP_CATEGORY, Field: field} }

	tests := []struct {
		name      string
		rows      []*Group
		cols      []*Group
		scope     OverlayScope
		nSource   string
		depth     int
		wantBad   bool
		wantDim   int
		wantGroup GroupType
		wantField string
		wantAxis  string
	}{
		// --- the refusal, on each axis -------------------------------
		{
			name:      "row scope: fan-out INNER level is summed across",
			rows:      []*Group{flat("segment"), fanOut("brand")},
			cols:      []*Group{flat("wave")},
			scope:     OverlayScopeRow,
			nSource:   PairwiseNSourceNWithinDistinct,
			depth:     0,
			wantBad:   true,
			wantDim:   1,
			wantGroup: GROUP_SET_PER_ELEMENT,
			wantField: "brand",
			wantAxis:  "row",
		},
		{
			name:      "column scope: same shape on the column axis",
			rows:      []*Group{flat("wave")},
			cols:      []*Group{flat("segment"), fanOut("brand")},
			scope:     OverlayScopeColumn,
			nSource:   PairwiseNSourceNWithinDistinct,
			depth:     0,
			wantBad:   true,
			wantDim:   1,
			wantGroup: GROUP_SET_PER_ELEMENT,
			wantField: "brand",
			wantAxis:  "column",
		},
		{
			name:    "row scope: fan-out two levels past the prefix",
			rows:    []*Group{flat("a"), flat("b"), fanOut("brand")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceNWithinDistinct,
			depth:   0,
			wantBad: true,
			wantDim: 2,
		},

		// --- the deliberate ACCEPTS ----------------------------------
		{
			// The filed shape. Refusing it would refuse the request
			// that motivated the whole mode.
			name:    "fan-out at depth == NWithinDepth is INSIDE the fixed prefix",
			rows:    []*Group{fanOut("brand"), flat("segment")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceNWithinDistinct,
			depth:   0,
			wantBad: false,
		},
		{
			name:    "fan-out at depth < NWithinDepth is inside the prefix too",
			rows:    []*Group{fanOut("brand"), flat("segment"), flat("wave2")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceNWithinDistinct,
			depth:   1,
			wantBad: false,
		},
		{
			name:    "raising NWithinDepth to cover the fan-out dim clears the refusal",
			rows:    []*Group{flat("segment"), fanOut("brand")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceNWithinDistinct,
			depth:   1,
			wantBad: false,
		},
		{
			name:    "row scope ignores a fan-out on the COLUMN axis",
			rows:    []*Group{flat("segment")},
			cols:    []*Group{flat("wave"), fanOut("brand")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceNWithinDistinct,
			depth:   0,
			wantBad: false,
		},
		{
			name:    "column scope ignores a fan-out on the ROW axis",
			rows:    []*Group{flat("segment"), fanOut("brand")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeColumn,
			nSource: PairwiseNSourceNWithinDistinct,
			depth:   0,
			wantBad: false,
		},
		{
			name:    "GROUP_SET_VALUE partitions and is never refused",
			rows:    []*Group{flat("segment"), {Type: GROUP_SET_VALUE, Field: "brand"}},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceNWithinDistinct,
			depth:   0,
			wantBad: false,
		},

		// --- every non-distinct mode is untouched --------------------
		{
			// Record counts ARE additive: a record that fans into two
			// buckets is genuinely two contributions. Gating n_within
			// would refuse requests that are correct today.
			name:    "n_within is NOT gated",
			rows:    []*Group{flat("segment"), fanOut("brand")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceNWithin,
			depth:   0,
			wantBad: false,
		},
		{
			// Margin distinct modes arrive in E1-S4 and are gated
			// separately; the record-count margin modes never are.
			name:    "row_margin_n is NOT gated",
			rows:    []*Group{flat("segment"), fanOut("brand")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceRowMarginN,
			depth:   0,
			wantBad: false,
		},
		{
			name:    "column_margin_n is NOT gated",
			rows:    []*Group{flat("segment"), fanOut("brand")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceColumnMarginN,
			depth:   0,
			wantBad: false,
		},
		{
			name:    "default (empty) n_source is NOT gated",
			rows:    []*Group{flat("segment"), fanOut("brand")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: "",
			depth:   0,
			wantBad: false,
		},

		// --- structural guards ---------------------------------------
		{
			name:    "a non-pair scope is left to the scope gate",
			rows:    []*Group{flat("segment"), fanOut("brand")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeCell,
			nSource: PairwiseNSourceNWithinDistinct,
			depth:   0,
			wantBad: false,
		},
		{
			name:    "a nil Group entry is skipped, not dereferenced",
			rows:    []*Group{flat("segment"), nil},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceNWithinDistinct,
			depth:   0,
			wantBad: false,
		},
		{
			name:    "a negative NWithinDepth still scans from dim 0",
			rows:    []*Group{fanOut("brand"), flat("segment")},
			cols:    []*Group{flat("wave")},
			scope:   OverlayScopeRow,
			nSource: PairwiseNSourceNWithinDistinct,
			depth:   -5,
			wantBad: true,
			wantDim: 0,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			params := PairwiseOverlayParams{NSource: tc.nSource, NWithinDepth: tc.depth}
			got, bad := CheckPairwiseSlabPartition(pwPartCT(tc.rows, tc.cols), tc.scope, params)
			if bad != tc.wantBad {
				t.Fatalf("CheckPairwiseSlabPartition bad = %v, want %v (violation %+v)", bad, tc.wantBad, got)
			}
			if !tc.wantBad {
				if got != (PairwiseSlabPartitionViolation{}) {
					t.Errorf("accepted shape still returned a violation: %+v", got)
				}
				return
			}
			if got.DimIndex != tc.wantDim {
				t.Errorf("DimIndex = %d, want %d", got.DimIndex, tc.wantDim)
			}
			if tc.wantGroup != "" && got.GroupType != tc.wantGroup {
				t.Errorf("GroupType = %q, want %q", got.GroupType, tc.wantGroup)
			}
			if tc.wantField != "" && got.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", got.Field, tc.wantField)
			}
			if tc.wantAxis != "" && got.Axis != tc.wantAxis {
				t.Errorf("Axis = %q, want %q", got.Axis, tc.wantAxis)
			}
		})
	}
}

// TestCheckPairwiseSlabPartition_NilCrosstab pins the nil-host guard:
// the pairwise MATRIX-host gate already refuses a crosstab-less request
// and this predicate must not panic ahead of it.
func TestCheckPairwiseSlabPartition_NilCrosstab(t *testing.T) {
	params := PairwiseOverlayParams{NSource: PairwiseNSourceNWithinDistinct}
	if _, bad := CheckPairwiseSlabPartition(nil, OverlayScopeRow, params); bad {
		t.Fatal("nil CrosstabSpec must not report a violation")
	}
}

// TestPairwiseNSourceSumsDistinctCells_NotUsesWithinDepth is the
// regression that keeps the two slab predicates apart. Both slab modes
// read NWithinDepth, but only the DISTINCT one sums a non-additive
// figure — keying the partition gate off UsesWithinDepth would refuse
// correct n_within requests.
func TestPairwiseNSourceSumsDistinctCells_NotUsesWithinDepth(t *testing.T) {
	if !PairwiseNSourceUsesWithinDepth(PairwiseNSourceNWithin) {
		t.Fatal("n_within must still read n_within_depth")
	}
	if PairwiseNSourceSumsDistinctCells(PairwiseNSourceNWithin) {
		t.Error("n_within sums RECORD counts, which are additive — it must not be gated")
	}
	if !PairwiseNSourceSumsDistinctCells(PairwiseNSourceNWithinDistinct) {
		t.Error("n_within_distinct sums distinct cardinalities — it must be gated")
	}
	for _, s := range []string{
		"",
		PairwiseNSourceCellNUnweighted,
		PairwiseNSourceCellValueWeight,
		PairwiseNSourceRowMarginN,
		PairwiseNSourceColumnMarginN,
		PairwiseNSourceCellWeightSum,
	} {
		if PairwiseNSourceSumsDistinctCells(s) {
			t.Errorf("n_source %q does not sum distinct cardinalities but is gated", s)
		}
	}
}

// TestPairwiseSlabPartitionViolation_Diagnostics pins the shared
// message + details both arms emit. The acceptance criterion is that
// Details carry the offending dim index, the grouper type and the axis.
func TestPairwiseSlabPartitionViolation_Diagnostics(t *testing.T) {
	params := PairwiseOverlayParams{NSource: PairwiseNSourceNWithinDistinct, NWithinDepth: 0}
	v, bad := CheckPairwiseSlabPartition(
		pwPartCT(
			[]*Group{{Type: GROUP_CATEGORY, Field: "segment"}, {Type: GROUP_SET_PER_ELEMENT, Field: "brand"}},
			[]*Group{{Type: GROUP_CATEGORY, Field: "wave"}},
		),
		OverlayScopeRow, params)
	if !bad {
		t.Fatal("expected a violation")
	}

	d := v.Details(OverlayKindPairwisePropZ, params, 3)
	for key, want := range map[string]any{
		"index":          3,
		"kind":           string(OverlayKindPairwisePropZ),
		"n_source":       PairwiseNSourceNWithinDistinct,
		"n_within_depth": 0,
		"dim_index":      1,
		"group_type":     string(GROUP_SET_PER_ELEMENT),
		"field":          "brand",
		"axis":           "row",
	} {
		if got := d[key]; got != want {
			t.Errorf("Details[%q] = %v, want %v", key, got, want)
		}
	}

	msg := v.Message(OverlayKindPairwisePropZ, params)
	for _, want := range []string{
		string(OverlayKindPairwisePropZ),
		PairwiseNSourceNWithinDistinct,
		string(GROUP_SET_PER_ELEMENT),
		"brand",
		"row-axis dim 1",
		"n_within_depth",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Message %q does not name %q", msg, want)
		}
	}
}
