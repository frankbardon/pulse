package processing

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// setPerElementGroup returns a GROUP_SET_PER_ELEMENT spec bound to the
// "tags" set field carried by crosstabFusedGateSchema.
func setPerElementGroup() *types.Group {
	return &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}
}

// TestCanFuseCrosstab_MultiKeyGrouperAdmitted pins the E2 widening:
// GROUP_SET_PER_ELEMENT implements MultiKeyStreamingGrouper rather than
// StreamableGrouper (one record fans into one bucket per selected set
// bit, so KeyFor(record) has no single answer). The admission probe
// must accept it on either axis, at the head position AND at a trailing
// position of a multi-grouper chain — the buffered PartitionByAxis
// recursion fans at any depth, so the gate cannot special-case depth 0.
func TestCanFuseCrosstab_MultiKeyGrouperAdmitted(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*types.CrosstabSpec)
	}{
		{"row axis position 0", func(c *types.CrosstabSpec) {
			c.Rows = []*types.Group{setPerElementGroup()}
		}},
		{"row axis trailing position", func(c *types.CrosstabSpec) {
			c.Rows = []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "brand"},
				setPerElementGroup(),
			}
		}},
		{"column axis position 0", func(c *types.CrosstabSpec) {
			c.Columns = []*types.Group{setPerElementGroup()}
		}},
		{"column axis trailing position", func(c *types.CrosstabSpec) {
			c.Columns = []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				setPerElementGroup(),
			}
		}},
		{"both axes fan out", func(c *types.CrosstabSpec) {
			c.Rows = []*types.Group{setPerElementGroup()}
			c.Columns = []*types.Group{setPerElementGroup()}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := happyPathCrosstabRequest()
			tc.apply(req.Crosstab)
			ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
			if !ok {
				t.Fatalf("expected fused-eligible for GROUP_SET_PER_ELEMENT, got reason %q", reason)
			}
			if reason != "" {
				t.Fatalf("expected empty reason on success, got %q", reason)
			}
		})
	}
}

// TestCanFuseCrosstab_MultiKeyWideningKeepsQuantileRejected guards the
// blast radius of the widening: admitting MultiKeyStreamingGrouper must
// not admit groupers with no per-record key derivation at all.
// GROUP_QUANTILE needs a finalize-time sorted view and implements
// neither keying interface; the reason string shape is unchanged.
func TestCanFuseCrosstab_MultiKeyWideningKeepsQuantileRejected(t *testing.T) {
	cases := []struct {
		name    string
		apply   func(*types.CrosstabSpec)
		wantAxi string
	}{
		{"row axis head", func(c *types.CrosstabSpec) {
			c.Rows = []*types.Group{{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4}}
		}, "row"},
		{"row axis behind a fan-out", func(c *types.CrosstabSpec) {
			c.Rows = []*types.Group{
				setPerElementGroup(),
				{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4},
			}
		}, "row"},
		{"column axis behind a fan-out", func(c *types.CrosstabSpec) {
			c.Columns = []*types.Group{
				setPerElementGroup(),
				{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4},
			}
		}, "column"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := happyPathCrosstabRequest()
			tc.apply(req.Crosstab)
			ok, reason := CanFuseCrosstab(req, crosstabFusedGateSchema(), nil)
			if ok {
				t.Fatalf("expected ineligible for GROUP_QUANTILE")
			}
			want := "non-streamable grouper on " + tc.wantAxi + " axis"
			if !strings.Contains(reason, want) || !strings.Contains(reason, "GROUP_QUANTILE") {
				t.Fatalf("reason %q: want substrings %q and GROUP_QUANTILE", reason, want)
			}
		})
	}
}

// TestBuildStreamableAxis_DiscriminatesKeyingShape pins the construction
// contract: each axis position resolves to exactly one keying shape, and
// the multi-key entry carries the spec field name because
// MultiKeyStreamingGrouper.KeysForRow takes the field as an argument
// (StreamableGrouper implementations bind their field at factory time).
func TestBuildStreamableAxis_DiscriminatesKeyingShape(t *testing.T) {
	schema := crosstabFusedGateSchema()
	axis := []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "brand"},
		setPerElementGroup(),
	}
	built, err := buildStreamableAxis(axis, schema, nil, "rows")
	if err != nil {
		t.Fatalf("buildStreamableAxis: %v", err)
	}
	if len(built) != 2 {
		t.Fatalf("built %d axis entries, want 2", len(built))
	}

	single := built[0]
	if single.fansOut() {
		t.Error("GROUP_CATEGORY must not classify as fan-out")
	}
	if single.single == nil {
		t.Error("GROUP_CATEGORY entry must carry the StreamableGrouper shape")
	}
	if single.multi != nil {
		t.Error("GROUP_CATEGORY entry must not carry a MultiKeyStreamingGrouper shape")
	}
	if single.grouper == nil {
		t.Error("axis entry must retain the constructed Grouper instance")
	}
	if single.field != "brand" {
		t.Errorf("field = %q, want brand", single.field)
	}

	multi := built[1]
	if !multi.fansOut() {
		t.Error("GROUP_SET_PER_ELEMENT must classify as fan-out")
	}
	if multi.multi == nil {
		t.Fatal("GROUP_SET_PER_ELEMENT entry must carry the MultiKeyStreamingGrouper shape")
	}
	if multi.single != nil {
		t.Error("fan-out entry must leave the single-key shape nil so no reader takes the narrow path")
	}
	if multi.field != "tags" {
		t.Errorf("field = %q, want tags", multi.field)
	}
}

// TestBuildStreamableAxis_RejectsUnkeyableGrouper keeps the construction
// path aligned with the admission probe: a grouper implementing neither
// keying interface is a typed PROCESSING_INTERNAL error, not a silent
// nil entry.
func TestBuildStreamableAxis_RejectsUnkeyableGrouper(t *testing.T) {
	schema := crosstabFusedGateSchema()
	axis := []*types.Group{{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4}}
	if _, err := buildStreamableAxis(axis, schema, nil, "rows"); err == nil {
		t.Fatal("expected error for GROUP_QUANTILE on a fused axis")
	}
}

// TestNewFusedCrosstabState_AdmitsMultiKeyAxis pins that construction —
// not just the gate — accepts a fan-out axis, and that
// axisComponents still resolves MetaGrouper across both grouper shapes.
// Key derivation and record routing land in E2-S2 / E2-S3; this test
// deliberately asserts construction and components wiring only.
func TestNewFusedCrosstabState_AdmitsMultiKeyAxis(t *testing.T) {
	schema := crosstabFusedGateSchema()
	spec := &types.CrosstabSpec{
		Rows:    []*types.Group{setPerElementGroup()},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "score"},
		Shape:   types.CrosstabShapeMatrix,
	}
	st, err := NewFusedCrosstabState(spec, schema, nil)
	if err != nil {
		t.Fatalf("NewFusedCrosstabState with fan-out row axis: %v", err)
	}
	if len(st.rowGroupers) != 1 || !st.rowGroupers[0].fansOut() {
		t.Fatalf("row axis did not resolve to a fan-out entry: %+v", st.rowGroupers)
	}
	if len(st.colGroupers) != 1 || st.colGroupers[0].fansOut() {
		t.Fatalf("column axis must stay single-key: %+v", st.colGroupers)
	}

	rowComps, err := st.axisComponents(st.rowGroupers)
	if err != nil {
		t.Fatalf("axisComponents(rows): %v", err)
	}
	if len(rowComps) != 1 || rowComps[0] == nil {
		t.Fatalf("MetaGrouper must resolve on the multi-key shape, got %#v", rowComps)
	}
	if _, ok := rowComps[0]["total_label_observations"]; !ok {
		t.Errorf("GROUP_SET_PER_ELEMENT components missing total_label_observations: %#v", rowComps[0])
	}
	colComps, err := st.axisComponents(st.colGroupers)
	if err != nil {
		t.Fatalf("axisComponents(columns): %v", err)
	}
	if len(colComps) != 1 || colComps[0] == nil {
		t.Fatalf("MetaGrouper must resolve on the single-key shape, got %#v", colComps)
	}
}

// TestFusedAxisGrouper_MultiPreferredOverSingle documents the precedence
// rule for an instance satisfying BOTH keying interfaces: the fan-out
// shape wins, mirroring Processor.processStreamingGrouped which prefers
// KeysForRow over KeyForRow. No built-in grouper is in that position;
// an extension could be, and silently taking the narrow path there
// would drop buckets.
func TestFusedAxisGrouper_MultiPreferredOverSingle(t *testing.T) {
	entry := classifyFusedAxisGrouper(&dualShapeGrouper{}, "tags")
	if !entry.fansOut() {
		t.Fatal("dual-shape grouper must classify as fan-out")
	}
	if entry.single != nil {
		t.Error("dual-shape grouper must not expose the single-key shape")
	}
}

// dualShapeGrouper implements Grouper, StreamableGrouper AND
// MultiKeyStreamingGrouper — the ambiguous case the precedence rule
// resolves. Test-only; no built-in grouper has this shape.
type dualShapeGrouper struct{}

func (dualShapeGrouper) Group([]*Record, string) (map[string][]*Record, error) {
	return map[string][]*Record{}, nil
}
func (dualShapeGrouper) KeyFor(*Record) (string, error) { return "", nil }
func (dualShapeGrouper) KeysForRow(*Record, string) ([]string, bool, error) {
	return nil, false, nil
}

var (
	_ Grouper                  = dualShapeGrouper{}
	_ StreamableGrouper        = dualShapeGrouper{}
	_ MultiKeyStreamingGrouper = dualShapeGrouper{}
)
