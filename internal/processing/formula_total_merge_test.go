package processing

import (
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// totalsAttrs is a one-layer request: a row-local formula, then a
// total() formula reading it (two distinct arguments), then a row-local
// formula reading the total formula.
func totalsAttrs() []*types.Attribute {
	return []*types.Attribute{
		{Type: types.ATTR_FORMULA, Expression: "x == nil ? nil : x / 3", Label: "third"},
		{Type: types.ATTR_FORMULA, Expression: "third == nil ? nil : third / total(third) + total(y * 0.7)", Label: "share"},
		{Type: types.ATTR_FORMULA, Expression: "share ?? 0", Label: "after"},
	}
}

// TestTotalsPrepass_PartitionMergeEqualsSerial (U51 E3-S1, #399): the
// two-phase drive's merged pre-pass publishes the serial pre-pass bits
// under any partition of the rows — the binned sums merge exactly — and
// a fresh instance bound to the published totals (PublishedTotals.Bind)
// scores every row as the serial two-pass Compute does.
func TestTotalsPrepass_PartitionMergeEqualsSerial(t *testing.T) {
	t.Parallel()

	s := exprParitySchema()
	attrs := totalsAttrs()
	serial, err := newFormulaAttribute(attrs[1], s)
	if err != nil {
		t.Fatal(err)
	}
	if err := BindAttribute(serial, nil, nil); err != nil {
		t.Fatal(err)
	}
	third := totalFormula(t, s, "x == nil ? nil : x / 3")
	recs := exprParityRecords(s, 311, true)
	for _, r := range recs {
		v, err := third.(RowLocalAttribute).Row(r, "")
		if err != nil {
			t.Fatal(err)
		}
		SetAttributeOutput(r, "third", v, true)
	}
	want, err := serial.Compute(recs, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, cuts := range [][]int{{311}, {1, 311}, {0, 100, 101, 311}, {7, 8, 9, 200, 311}} {
		var parts []*TotalsPrepass
		lo := 0
		for _, hi := range cuts {
			p, err := NewTotalsPrepass(attrs, s, nil, nil, nil, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range exprParityRecords(s, 311, true)[lo:hi] {
				if err := p.Fold(r); err != nil {
					t.Fatal(err)
				}
			}
			parts = append(parts, p)
			lo = hi
		}
		// Merge in reverse: order cannot reach the bits.
		merged := parts[len(parts)-1]
		for i := len(parts) - 2; i >= 0; i-- {
			if err := merged.Merge(parts[i]); err != nil {
				t.Fatal(err)
			}
		}
		if err := merged.Merge(nil); err != nil {
			t.Fatal(err)
		}
		pub, err := merged.Publish()
		if err != nil {
			t.Fatal(err)
		}
		bound, err := newFormulaAttribute(attrs[1], s)
		if err != nil {
			t.Fatal(err)
		}
		if err := BindAttribute(bound, nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := pub.Bind(1, bound); err != nil {
			t.Fatal(err)
		}
		// A row-local slot binds nothing.
		if err := pub.Bind(0, third); err != nil {
			t.Fatal(err)
		}
		for i, r := range recs {
			got, err := bound.(RowLocalAttribute).Row(r, "")
			if err != nil {
				t.Fatal(err)
			}
			if math.Float64bits(got) != math.Float64bits(want[i]) {
				t.Fatalf("cuts %v row %d: %v, serial %v", cuts, i, got, want[i])
			}
		}
	}
}

// TestTotalsPrepass_Refusals: the hooks refuse every inconsistent use
// as PROCESSING_INTERNAL — never a silent 0.
func TestTotalsPrepass_Refusals(t *testing.T) {
	t.Parallel()

	s := exprParitySchema()
	internal := func(t *testing.T, err error, want string) {
		t.Helper()
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PROCESSING_INTERNAL || !strings.Contains(ce.Message, want) {
			t.Fatalf("err = %v, want PROCESSING_INTERNAL %q", err, want)
		}
	}
	tot := func() *formulaTotalAttribute {
		return totalFormula(t, s, "total(x) + total(y)").(*formulaTotalAttribute)
	}

	// An unbound instance refuses its first row (the patched total()
	// read's internal fault, surfaced through the formula evaluation).
	_, err := tot().Row(exprParityRecords(s, 1, false)[0], "")
	var inner *errors.CodedError
	if err == nil || !stderrors.As(err, &inner) {
		t.Fatalf("unbound Row = %v, want a coded refusal", err)
	}

	// A nil PublishedTotals binds nothing.
	var none *PublishedTotals
	if err := none.Bind(0, tot()); err != nil {
		t.Fatal(err)
	}
	// A slot without published values.
	internal(t, (&PublishedTotals{values: map[int][]float64{}}).Bind(3, tot()), "no published total() values")
	// Arity mismatches.
	internal(t, tot().PublishTotals([]float64{1}), "published 1 values for 2 arguments")
	other := totalFormula(t, s, "total(x)").(*formulaTotalAttribute)
	part, err := other.PartialTotals()
	if err != nil {
		t.Fatal(err)
	}
	internal(t, tot().AbsorbTotals(part), "1 arguments merged into 2")
	swapped := totalFormula(t, s, "total(y) + total(x)").(*formulaTotalAttribute)
	part, _ = swapped.PartialTotals()
	internal(t, tot().AbsorbTotals(part), `argument "y" merged into "x"`)
	// Absorbing after Finalize.
	done := tot()
	if err := done.Finalize(); err != nil {
		t.Fatal(err)
	}
	part, _ = tot().PartialTotals()
	internal(t, done.AbsorbTotals(part), "after its pre-pass finalized")

	// A layer outside the plan, and a later layer without the earlier
	// layers' publication (U74 E3-S2, #494) — never a silent 0.
	twoLayers := []*types.Attribute{
		{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "a"},
		{Type: types.ATTR_FORMULA, Expression: "total(x - a)", Label: "b"},
	}
	_, err = NewTotalsPrepass(twoLayers, s, nil, nil, nil, 2, nil)
	internal(t, err, "layer 2 is outside the plan's 2 layers")
	_, err = NewTotalsPrepass(twoLayers, s, nil, nil, nil, -1, nil)
	internal(t, err, "layer -1 is outside the plan's 2 layers")
	_, err = NewTotalsPrepass(twoLayers, s, nil, nil, nil, 1, nil)
	internal(t, err, "layer 1: attribute slot 0 (layer 0) has no published total() values")
	l0, err := NewTotalsPrepass(twoLayers, s, nil, nil, nil, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	pub0, err := l0.Publish()
	if err != nil {
		t.Fatal(err)
	}
	l1, err := NewTotalsPrepass(twoLayers, s, nil, nil, nil, 1, pub0)
	if err != nil {
		t.Fatal(err)
	}
	internal(t, l1.Merge(l0), "disagree on the layer: 0 merged into 1")
	// A non-row-local, non-total attribute.
	_, err = NewTotalsPrepass([]*types.Attribute{
		{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"},
		{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "a"},
	}, s, nil, nil, nil, 0, nil)
	internal(t, err, "not eligible for the parallel total() pre-pass")
	_, err = NewTotalsPrepass([]*types.Attribute{{Type: "ATTR_NOPE"}}, s, nil, nil, nil, 0, nil)
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PROCESSING_CONFIG {
		t.Fatalf("unknown type: %v", err)
	}
	a, _ := NewTotalsPrepass([]*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "a"}}, s, nil, nil, nil, 0, nil)
	internal(t, a.Merge(&TotalsPrepass{}), "disagree on the attribute count")
}

// TestTotalsPrepassRequest: the pre-pass request keeps the filters and
// only the attributes the layer's pre-pass evaluates — the stages a
// layer-L total() argument reads and the layer-L total() formulas — and
// drops the aggregations, groups and matrices; a layer outside the plan
// comes back unchanged (full projection).
func TestTotalsPrepassRequest(t *testing.T) {
	t.Parallel()

	attrs := append(totalsAttrs(), &types.Attribute{Type: types.ATTR_FORMULA, Expression: "y + 1", Label: "unused"})
	req := &types.Request{
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "y > 1"}},
		Attributes:   attrs,
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "after"}},
		Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"x", "y"}}},
	}
	got := TotalsPrepassRequest(req, nil, 0)
	if len(got.Attributes) != 2 || got.Attributes[0] != attrs[0] || got.Attributes[1].Label != "share" {
		t.Fatalf("attributes = %v, want third + share", got.Attributes)
	}
	// The total() formula projects as its arguments only.
	if e := got.Attributes[1].Expression; e != "[third, y * 0.7]" {
		t.Fatalf("pre-pass share expression = %q, want its total() arguments", e)
	}
	if attrs[1].Expression == got.Attributes[1].Expression {
		t.Fatal("the caller's attribute was mutated")
	}
	if got.Aggregations != nil || got.Matrices != nil || got.Groups != nil || len(got.Filterers) != 1 {
		t.Fatalf("pre-pass request = %+v", got)
	}
	if len(req.Attributes) != 4 || req.Aggregations == nil {
		t.Fatal("the caller's request was mutated")
	}
	if TotalsPrepassRequest(req, nil, 1) != req || TotalsPrepassRequest(req, nil, -1) != req {
		t.Fatal("a layer outside the plan must come back unchanged")
	}
	// Multi-layer (U74 E3-S2, #494): layer 0 projects a's argument only;
	// layer 1 evaluates a whole (b's argument reads it) and projects b's
	// argument — never the field only b's row expression reads (cat).
	multi := &types.Request{Attributes: []*types.Attribute{
		{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "a"},
		{Type: types.ATTR_FORMULA, Expression: "cat + total(x - a)", Label: "b"},
	}}
	if got := TotalsPrepassRequest(multi, nil, 0); len(got.Attributes) != 1 || got.Attributes[0].Expression != "[x]" {
		t.Fatalf("layer 0 attributes = %+v, want a's argument only", got.Attributes)
	}
	l1 := TotalsPrepassRequest(multi, nil, 1)
	if len(l1.Attributes) != 2 || l1.Attributes[0] != multi.Attributes[0] || l1.Attributes[1].Expression != "[x - a]" {
		t.Fatalf("layer 1 attributes = %+v, want a whole and b's argument", l1.Attributes)
	}
	if TotalsPrepassLayers(multi.Attributes, nil) != 2 || TotalsPrepassLayers(attrs, nil) != 1 {
		t.Fatal("TotalsPrepassLayers")
	}
	if !HasFormulaTotals(attrs) || HasFormulaTotals(attrs[:1]) {
		t.Fatal("HasFormulaTotals")
	}
}

// totalsLayeredAttrs is a three-layer request: a total over a field
// (layer 0), a row-local over it, a total over the row-local (layer 1)
// beside an independent layer-0 total, and a total whose argument reads
// the layer-1 one (layer 2), then a row-local over everything.
func totalsLayeredAttrs() []*types.Attribute {
	return []*types.Attribute{
		{Type: types.ATTR_FORMULA, Expression: "x == nil ? nil : x / total(x)", Label: "s0"},
		{Type: types.ATTR_FORMULA, Expression: "s0 == nil ? nil : s0 * 3 - y / 7", Label: "d"},
		{Type: types.ATTR_FORMULA, Expression: "y / total(y * 0.3)", Label: "i0"},
		{Type: types.ATTR_FORMULA, Expression: "d == nil ? nil : d / total(d)", Label: "s1"},
		{Type: types.ATTR_FORMULA, Expression: "(s1 ?? 0) + total((s1 ?? 0) - i0 / 11)", Label: "s2"},
		{Type: types.ATTR_FORMULA, Expression: "s2 + i0", Label: "out"},
	}
}

// TestTotalsPrepass_MultiLayerPartitionMergeEqualsSerial (U74 E3-S2,
// #494): phase 1 run layer by layer — every partition of layer L built
// with the earlier layers' publication bound, folded, merged in reverse
// and published — publishes, under any partition of the rows, the
// totals the serial buffered arm computes; instances bound to the last
// publication score every slot as the serial applyAttributes does,
// bit for bit.
func TestTotalsPrepass_MultiLayerPartitionMergeEqualsSerial(t *testing.T) {
	t.Parallel()

	s := exprParitySchema()
	attrs := totalsLayeredAttrs()
	const n = 311
	if got := TotalsPrepassLayers(attrs, nil); got != 3 {
		t.Fatalf("layers = %d, want 3", got)
	}
	want := exprParityRecords(s, n, true)
	if _, _, err := NewProcessor(s).applyAttributes(attrs, want); err != nil {
		t.Fatal(err)
	}

	for _, cuts := range [][]int{{n}, {1, n}, {0, 100, 101, n}, {7, 8, 9, 200, n}} {
		var pub *PublishedTotals
		for layer := 0; layer < 3; layer++ {
			var parts []*TotalsPrepass
			lo := 0
			for _, hi := range cuts {
				p, err := NewTotalsPrepass(attrs, s, nil, nil, nil, layer, pub)
				if err != nil {
					t.Fatal(err)
				}
				if p.Layers() != 3 {
					t.Fatalf("Layers() = %d", p.Layers())
				}
				for _, r := range exprParityRecords(s, n, true)[lo:hi] {
					if err := p.Fold(r); err != nil {
						t.Fatal(err)
					}
				}
				parts = append(parts, p)
				lo = hi
			}
			merged := parts[len(parts)-1]
			for i := len(parts) - 2; i >= 0; i-- {
				if err := merged.Merge(parts[i]); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if pub, err = merged.Publish(); err != nil {
				t.Fatal(err)
			}
		}
		// Phase 2: fresh instances bound to the last publication.
		got := exprParityRecords(s, n, true)
		for i, a := range attrs {
			comp, err := newFormulaAttribute(a, s)
			if err != nil {
				t.Fatal(err)
			}
			if err := BindAttribute(comp, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := pub.Bind(i, comp); err != nil {
				t.Fatal(err)
			}
			for _, r := range got {
				v, err := comp.(RowLocalAttribute).Row(r, "")
				if err != nil {
					t.Fatalf("cuts %v slot %s: %v", cuts, a.Label, err)
				}
				SetAttributeOutput(r, a.Label, v, EmitsNull(comp))
			}
		}
		for j := range got {
			for _, a := range attrs {
				g, gok := got[j].NumericValue(a.Label)
				w, wok := want[j].NumericValue(a.Label)
				if gok != wok || got[j].IsNull(a.Label) != want[j].IsNull(a.Label) || math.Float64bits(g) != math.Float64bits(w) {
					t.Fatalf("cuts %v row %d %s: %v (%v), serial %v (%v)", cuts, j, a.Label, g, gok, w, wok)
				}
			}
		}
	}
}
