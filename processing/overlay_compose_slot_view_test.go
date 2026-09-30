package processing

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// withCrosstabComponents attaches a CrosstabComponents block to a
// matrix-shaped fixture response built by makeMatrixFromValues /
// makeMatrixWithRowMargins. Returns the same pointer for chaining.
func withCrosstabComponents(resp *types.Response, comps *types.CrosstabComponents) *types.Response {
	resp.Components = &types.ResponseComponents{Crosstab: comps}
	return resp
}

// componentsFixture is the canonical 3×3 components block the slot-view
// tests read. Cell (0,0) carries the AGG_DISTINCT_SUM signature so the
// admission accessors have something to classify; the string-valued key
// on (0,1) exercises the non-numeric arm.
func componentsFixture() *types.CrosstabComponents {
	return &types.CrosstabComponents{
		CellCounts: [][]int{
			{10, 11, 12},
			{20, 21, 22},
			{30, 31, 32},
		},
		CellComponents: [][]map[string]any{
			{
				{"n": 10, "sum": 40.0, "distinct_count": 7},
				{"n": 11, "label": "not-a-number"},
				nil,
			},
			{nil, nil, nil},
			{nil, nil, nil},
		},
		RowMarginCounts:    []int{60, 61, 62},
		ColumnMarginCounts: []int{70, 71, 72},
		RowMarginComponents: []map[string]any{
			{"n": 60, "sum": 100.0, "distinct_count": 5},
			// No distinct key, and a string-valued key so the margin
			// readers' own numeric-coercion arm is exercised — the cell
			// readers forward to CrosstabHostView, the margin readers do
			// not, so a non-numeric case on a CELL does not cover them.
			{"n": 61, "sum": 101.0, "label": "not-a-number"},
			nil,
		},
		ColumnMarginComponents: []map[string]any{
			{"n": 70, "cardinality": 9},
			nil,
			nil,
		},
		GrandTotalCount:      180,
		GrandTotalComponents: map[string]any{"n": 180, "sum": 301.0, "label": "not-a-number"},
	}
}

// composeSlotFixtures builds the three-slot host the tests share:
// slot 0 carries components, slot 1 has components DISABLED (no
// Components block at all), slot 2 carries a Components block with no
// Crosstab inside (a non-crosstab slot).
func composeSlotFixtures() *ComposeHostView {
	withComps := withCrosstabComponents(
		makeMatrixWithRowMargins(
			[3][3]float64{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}},
			[3]float64{100, 100, 100},
		),
		componentsFixture(),
	)
	disabled := makeMatrixWithRowMargins(
		[3][3]float64{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}},
		[3]float64{100, 100, 100},
	)
	nonCrosstab := &types.Response{
		Data:       []map[string]any{{"g": "a", "v": 1}},
		Components: &types.ResponseComponents{},
	}
	return NewComposeHostView([]*types.Response{withComps, disabled, nonCrosstab, nil})
}

// TestComposeHostView_SlotStates is the acceptance criterion the whole
// story turns on: a slot whose components were DISABLED must not be
// confusable with a slot that merely lacks the requested key. The four
// states are read off the same host in one pass so a future change
// that collapses any pair fails here.
func TestComposeHostView_SlotStates(t *testing.T) {
	host := composeSlotFixtures()
	if got := host.SlotCount(); got != 4 {
		t.Fatalf("SlotCount() = %d, want 4", got)
	}
	for _, tc := range []struct {
		name  string
		index int
		want  ComposeComponentsState
	}{
		{"components present", 0, ComposeComponentsPresent},
		{"components disabled", 1, ComposeComponentsDisabled},
		{"non-crosstab slot", 2, ComposeComponentsNonCrosstab},
		{"nil slot response", 3, ComposeComponentsSlotAbsent},
		{"index past the end", 99, ComposeComponentsSlotAbsent},
		{"negative index", -1, ComposeComponentsSlotAbsent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slot := host.Slot(tc.index)
			if slot == nil {
				t.Fatal("Slot() returned nil; it must always return a usable view")
			}
			if got := slot.State(); got != tc.want {
				t.Fatalf("State() = %v, want %v", got, tc.want)
			}
			if got, want := slot.HasComponents(), tc.want == ComposeComponentsPresent; got != want {
				t.Fatalf("HasComponents() = %v, want %v", got, want)
			}
			if got, want := slot.State().Available(), tc.want == ComposeComponentsPresent; got != want {
				t.Fatalf("State().Available() = %v, want %v", got, want)
			}
			if slot.State().String() == "" {
				t.Fatal("State().String() is empty; diagnostics cannot name the state")
			}
		})
	}
}

// TestComposeHostView_DisabledIsNotKeyAbsent states the distinction the
// story forbids collapsing, as an assertion rather than as prose: both
// reads return ok=false, and the STATE is what separates a
// configuration error from a data condition.
func TestComposeHostView_DisabledIsNotKeyAbsent(t *testing.T) {
	host := composeSlotFixtures()

	present := host.Slot(0)
	if _, ok := present.CellComponentFloat(0, 0, "no_such_key"); ok {
		t.Fatal("expected ok=false for an absent key on a slot that HAS components")
	}
	if present.State() != ComposeComponentsPresent {
		t.Fatalf("a missing KEY must leave the slot state Present, got %v", present.State())
	}

	disabled := host.Slot(1)
	if _, ok := disabled.CellComponentFloat(0, 0, "n"); ok {
		t.Fatal("expected ok=false when components are disabled")
	}
	if disabled.State() != ComposeComponentsDisabled {
		t.Fatalf("a disabled slot must report Disabled, got %v", disabled.State())
	}

	if present.State() == disabled.State() {
		t.Fatal("key-absent and components-disabled collapsed into one state")
	}
}

// TestComposeSlotView_ComponentsPresent walks the accessor set against
// the fixture block. Every figure is asserted against the literal the
// fixture declares, so a forwarder wired to the wrong slot is caught.
func TestComposeSlotView_ComponentsPresent(t *testing.T) {
	slot := composeSlotFixtures().Slot(0)

	if got := slot.Components(); got == nil {
		t.Fatal("Components() = nil on a slot that carries a block")
	}
	if got := slot.Response(); got == nil {
		t.Fatal("Response() = nil on a present slot")
	}
	if got := slot.Matrix(); got == nil || got.Payload() == nil {
		t.Fatal("Matrix() must expose the slot's CrosstabHostView")
	}
	if got := slot.RowCount(); got != 3 {
		t.Fatalf("RowCount() = %d, want 3", got)
	}
	if got := slot.ColumnCount(); got != 3 {
		t.Fatalf("ColumnCount() = %d, want 3", got)
	}
	if got := slot.RowAxisDepth(); got != 1 {
		t.Fatalf("RowAxisDepth() = %d, want 1", got)
	}
	if got := slot.ColumnAxisDepth(); got != 1 {
		t.Fatalf("ColumnAxisDepth() = %d, want 1", got)
	}
	if got := slot.RowKey(1); len(got) != 1 || got[0] != "r1" {
		t.Fatalf("RowKey(1) = %v, want [r1]", got)
	}
	if got := slot.ColumnKey(2); len(got) != 1 || got[0] != "c2" {
		t.Fatalf("ColumnKey(2) = %v, want [c2]", got)
	}

	// CellComponents[1][2] is nil, so "n" is unreadable there.
	if got, ok := slot.CellN(1, 2); ok {
		t.Fatalf("CellN(1,2) = (%d, true), want ok=false for a nil cell slot", got)
	}
	if got, ok := slot.CellN(0, 0); !ok || got != 10 {
		t.Fatalf("CellN(0,0) = (%d, %v), want (10, true)", got, ok)
	}
	if got, ok := slot.CellComponentFloat(0, 0, "sum"); !ok || got != 40 {
		t.Fatalf("CellComponentFloat(0,0,sum) = (%v, %v), want (40, true)", got, ok)
	}
	if got, ok := slot.CellCount(2, 1); !ok || got != 31 {
		t.Fatalf("CellCount(2,1) = (%d, %v), want (31, true)", got, ok)
	}

	if got, ok := slot.RowMarginN(1); !ok || got != 61 {
		t.Fatalf("RowMarginN(1) = (%d, %v), want (61, true)", got, ok)
	}
	if got, ok := slot.ColumnMarginN(2); !ok || got != 72 {
		t.Fatalf("ColumnMarginN(2) = (%d, %v), want (72, true)", got, ok)
	}
	if got, ok := slot.RowMarginComponentFloat(0, "sum"); !ok || got != 100 {
		t.Fatalf("RowMarginComponentFloat(0,sum) = (%v, %v), want (100, true)", got, ok)
	}
	if got, ok := slot.ColumnMarginComponentFloat(0, "cardinality"); !ok || got != 9 {
		t.Fatalf("ColumnMarginComponentFloat(0,cardinality) = (%v, %v), want (9, true)", got, ok)
	}
	if got, ok := slot.GrandTotalComponentFloat("sum"); !ok || got != 301 {
		t.Fatalf("GrandTotalComponentFloat(sum) = (%v, %v), want (301, true)", got, ok)
	}
	if got, ok := slot.GrandTotalN(); !ok || got != 180 {
		t.Fatalf("GrandTotalN() = (%d, %v), want (180, true)", got, ok)
	}

	// Distinct-key reads reuse the crosstab arm's probe order, so the
	// row margin resolves through "distinct_count" and the column
	// margin through "cardinality" without the view naming either.
	if got, ok := slot.RowMarginDistinctN(0); !ok || got != 5 {
		t.Fatalf("RowMarginDistinctN(0) = (%d, %v), want (5, true)", got, ok)
	}
	if got, ok := slot.ColumnMarginDistinctN(0); !ok || got != 9 {
		t.Fatalf("ColumnMarginDistinctN(0) = (%d, %v), want (9, true)", got, ok)
	}

	agg, ok := slot.CellAggregatorIdentity()
	if !ok || agg != types.AGG_DISTINCT_SUM {
		t.Fatalf("CellAggregatorIdentity() = (%v, %v), want (AGG_DISTINCT_SUM, true)", agg, ok)
	}
	agg, key, admitted := slot.AdmitsDistinctKeyN()
	if !admitted || agg != types.AGG_DISTINCT_SUM || key != "distinct_count" {
		t.Fatalf("AdmitsDistinctKeyN() = (%v, %q, %v), want (AGG_DISTINCT_SUM, distinct_count, true)",
			agg, key, admitted)
	}
}

// TestComposeSlotView_KeyAbsentAndNonNumeric covers the two DATA
// conditions on a slot that does carry components: a key that is not
// there, and a key whose value is not a number. Both are ok=false and
// neither moves the slot state.
func TestComposeSlotView_KeyAbsentAndNonNumeric(t *testing.T) {
	slot := composeSlotFixtures().Slot(0)
	for _, tc := range []struct {
		name string
		read func() (float64, bool)
	}{
		{"cell key absent", func() (float64, bool) { return slot.CellComponentFloat(0, 0, "nope") }},
		{"cell value non-numeric", func() (float64, bool) { return slot.CellComponentFloat(0, 1, "label") }},
		{"cell slot nil", func() (float64, bool) { return slot.CellComponentFloat(0, 2, "n") }},
		{"row margin key absent", func() (float64, bool) { return slot.RowMarginComponentFloat(1, "distinct_count") }},
		{"row margin value non-numeric", func() (float64, bool) { return slot.RowMarginComponentFloat(1, "label") }},
		{"grand total value non-numeric", func() (float64, bool) { return slot.GrandTotalComponentFloat("label") }},
		{"row margin slot nil", func() (float64, bool) { return slot.RowMarginComponentFloat(2, "n") }},
		{"column margin slot nil", func() (float64, bool) { return slot.ColumnMarginComponentFloat(1, "n") }},
		{"grand total key absent", func() (float64, bool) { return slot.GrandTotalComponentFloat("cardinality") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if v, ok := tc.read(); ok {
				t.Fatalf("expected ok=false, got (%v, true)", v)
			}
			if slot.State() != ComposeComponentsPresent {
				t.Fatalf("a data condition must not move the slot state, got %v", slot.State())
			}
		})
	}
	if _, ok := slot.RowMarginDistinctN(1); ok {
		t.Fatal("RowMarginDistinctN on a margin with no distinct key must be ok=false")
	}
}

// TestComposeSlotView_OutOfRangeIndexes checks the bounds-checking
// posture across every indexed accessor. Nothing here may panic.
func TestComposeSlotView_OutOfRangeIndexes(t *testing.T) {
	slot := composeSlotFixtures().Slot(0)
	for _, tc := range []struct {
		name string
		read func() bool
	}{
		{"cell row high", func() bool { _, ok := slot.CellComponentFloat(9, 0, "n"); return ok }},
		{"cell row negative", func() bool { _, ok := slot.CellComponentFloat(-1, 0, "n"); return ok }},
		{"cell col high", func() bool { _, ok := slot.CellComponentFloat(0, 9, "n"); return ok }},
		{"cell count row high", func() bool { _, ok := slot.CellCount(9, 9); return ok }},
		{"cell count row negative", func() bool { _, ok := slot.CellCount(-1, 0); return ok }},
		// Row IN range, column out: the row bound would otherwise mask
		// a missing column bound entirely.
		{"cell count col high", func() bool { _, ok := slot.CellCount(0, 9); return ok }},
		{"cell count col negative", func() bool { _, ok := slot.CellCount(0, -1); return ok }},
		{"row margin high", func() bool { _, ok := slot.RowMarginN(9); return ok }},
		{"row margin negative", func() bool { _, ok := slot.RowMarginN(-1); return ok }},
		{"column margin high", func() bool { _, ok := slot.ColumnMarginN(9); return ok }},
		{"row margin comp high", func() bool { _, ok := slot.RowMarginComponentFloat(9, "n"); return ok }},
		{"column margin comp negative", func() bool { _, ok := slot.ColumnMarginComponentFloat(-1, "n"); return ok }},
		{"row margin distinct high", func() bool { _, ok := slot.RowMarginDistinctN(9); return ok }},
		{"column margin distinct high", func() bool { _, ok := slot.ColumnMarginDistinctN(9); return ok }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.read() {
				t.Fatal("expected ok=false for an out-of-range index")
			}
		})
	}
	if got := slot.RowKey(9); got != nil {
		t.Fatalf("RowKey(9) = %v, want nil", got)
	}
	if got := slot.ColumnKey(-1); got != nil {
		t.Fatalf("ColumnKey(-1) = %v, want nil", got)
	}
}

// TestComposeSlotView_DisabledAndAbsentSlotsAreInert walks the whole
// accessor set on a components-disabled slot, a non-crosstab slot, an
// absent slot and a nil view. Every read must answer ok=false without
// panicking — the nil-safety criterion.
func TestComposeSlotView_DisabledAndAbsentSlotsAreInert(t *testing.T) {
	host := composeSlotFixtures()
	var nilView *ComposeSlotView
	for _, tc := range []struct {
		name string
		slot *ComposeSlotView
	}{
		{"components disabled", host.Slot(1)},
		{"non-crosstab slot", host.Slot(2)},
		{"nil slot response", host.Slot(3)},
		{"index past the end", host.Slot(99)},
		{"nil view", nilView},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.slot
			if s.Components() != nil {
				t.Fatal("Components() must be nil without a crosstab components block")
			}
			if s.HasComponents() {
				t.Fatal("HasComponents() must be false")
			}
			if _, ok := s.CellComponentFloat(0, 0, "n"); ok {
				t.Fatal("CellComponentFloat must be ok=false")
			}
			if _, ok := s.CellN(0, 0); ok {
				t.Fatal("CellN must be ok=false")
			}
			if _, ok := s.CellCount(0, 0); ok {
				t.Fatal("CellCount must be ok=false")
			}
			if _, ok := s.RowMarginN(0); ok {
				t.Fatal("RowMarginN must be ok=false")
			}
			if _, ok := s.ColumnMarginN(0); ok {
				t.Fatal("ColumnMarginN must be ok=false")
			}
			if _, ok := s.RowMarginComponentFloat(0, "n"); ok {
				t.Fatal("RowMarginComponentFloat must be ok=false")
			}
			if _, ok := s.ColumnMarginComponentFloat(0, "n"); ok {
				t.Fatal("ColumnMarginComponentFloat must be ok=false")
			}
			if _, ok := s.GrandTotalComponentFloat("n"); ok {
				t.Fatal("GrandTotalComponentFloat must be ok=false")
			}
			if _, ok := s.GrandTotalN(); ok {
				t.Fatal("GrandTotalN must be ok=false")
			}
			if _, ok := s.RowMarginDistinctN(0); ok {
				t.Fatal("RowMarginDistinctN must be ok=false")
			}
			if _, ok := s.ColumnMarginDistinctN(0); ok {
				t.Fatal("ColumnMarginDistinctN must be ok=false")
			}
			if _, ok := s.CellAggregatorIdentity(); ok {
				t.Fatal("CellAggregatorIdentity must be ok=false")
			}
			if _, _, ok := s.AdmitsDistinctKeyN(); ok {
				t.Fatal("AdmitsDistinctKeyN must be ok=false")
			}
			// Payload-side reads stay safe too; they simply report the
			// shape the slot actually has.
			_ = s.RowCount()
			_ = s.ColumnCount()
			_ = s.RowAxisDepth()
			_ = s.ColumnAxisDepth()
			_ = s.RowKey(0)
			_ = s.ColumnKey(0)
			_ = s.Matrix()
			_ = s.Response()
		})
	}
}

// TestNewComposeHostView_NilAndEmpty pins the constructor's degenerate
// inputs: a nil receiver and an empty slot list are both usable.
func TestNewComposeHostView_NilAndEmpty(t *testing.T) {
	var nilHost *ComposeHostView
	if got := nilHost.SlotCount(); got != 0 {
		t.Fatalf("nil host SlotCount() = %d, want 0", got)
	}
	if got := nilHost.Slot(0); got == nil || got.State() != ComposeComponentsSlotAbsent {
		t.Fatal("nil host must still hand back an absent-state slot view")
	}
	empty := NewComposeHostView(nil)
	if got := empty.SlotCount(); got != 0 {
		t.Fatalf("empty host SlotCount() = %d, want 0", got)
	}
	if got := empty.Slot(0).State(); got != ComposeComponentsSlotAbsent {
		t.Fatalf("empty host Slot(0).State() = %v, want SlotAbsent", got)
	}
}

// TestComposeHostView_MatchesCrosstabHostView is the parity assertion:
// the per-slot view must agree with the crosstab arm's own accessors
// on the same payload + components pair. If the two ever diverge, one
// of the two arms is reading a different number from the same block.
func TestComposeHostView_MatchesCrosstabHostView(t *testing.T) {
	resp := withCrosstabComponents(
		makeMatrixWithRowMargins(
			[3][3]float64{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}},
			[3]float64{100, 100, 100},
		),
		componentsFixture(),
	)
	slot := NewComposeHostView([]*types.Response{resp}).Slot(0)
	direct := NewCrosstabHostViewWithComponents(resp.Crosstab.Matrix, resp.Components.Crosstab)

	gotN, gotOK := slot.CellN(0, 0)
	wantN, wantOK := direct.CellN(0, 0)
	if gotN != wantN || gotOK != wantOK {
		t.Fatalf("CellN parity: slot (%d, %v) vs crosstab (%d, %v)", gotN, gotOK, wantN, wantOK)
	}
	gotD, gotOK := slot.RowMarginDistinctN(0)
	wantD, wantOK := direct.RowMarginDistinctN(0)
	if gotD != wantD || gotOK != wantOK {
		t.Fatalf("RowMarginDistinctN parity: slot (%d, %v) vs crosstab (%d, %v)", gotD, gotOK, wantD, wantOK)
	}
	gotAgg, gotOK := slot.CellAggregatorIdentity()
	wantAgg, wantOK := direct.CellAggregatorIdentity()
	if gotAgg != wantAgg || gotOK != wantOK {
		t.Fatalf("CellAggregatorIdentity parity: slot (%v, %v) vs crosstab (%v, %v)",
			gotAgg, gotOK, wantAgg, wantOK)
	}
}
