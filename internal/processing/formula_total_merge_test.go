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
			p, err := NewTotalsPrepass(attrs, s, nil, nil, nil)
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

	// A multi-layer plan is not the first cut.
	_, err = NewTotalsPrepass([]*types.Attribute{
		{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "a"},
		{Type: types.ATTR_FORMULA, Expression: "total(x - a)", Label: "b"},
	}, s, nil, nil, nil)
	internal(t, err, "exactly one layer, the plan has 2")
	// A non-row-local, non-total attribute.
	_, err = NewTotalsPrepass([]*types.Attribute{
		{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"},
		{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "a"},
	}, s, nil, nil, nil)
	internal(t, err, "not eligible for the parallel total() pre-pass")
	_, err = NewTotalsPrepass([]*types.Attribute{{Type: "ATTR_NOPE"}}, s, nil, nil, nil)
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PROCESSING_CONFIG {
		t.Fatalf("unknown type: %v", err)
	}
	a, _ := NewTotalsPrepass([]*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "a"}}, s, nil, nil, nil)
	internal(t, a.Merge(&TotalsPrepass{}), "disagree on the attribute count")
}

// TestTotalsPrepassRequest: the pre-pass request keeps the filters and
// only the attributes the pre-pass evaluates — the layer-0 stages a
// total() argument reads and the total() formulas — and drops the
// aggregations, groups and matrices; a multi-layer request comes back
// unchanged (full projection).
func TestTotalsPrepassRequest(t *testing.T) {
	attrs := append(totalsAttrs(), &types.Attribute{Type: types.ATTR_FORMULA, Expression: "y + 1", Label: "unused"})
	req := &types.Request{
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "y > 1"}},
		Attributes:   attrs,
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "after"}},
		Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"x", "y"}}},
	}
	got := TotalsPrepassRequest(req, nil)
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
	multi := &types.Request{Attributes: []*types.Attribute{
		{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "a"},
		{Type: types.ATTR_FORMULA, Expression: "total(x - a)", Label: "b"},
	}}
	if TotalsPrepassRequest(multi, nil) != multi {
		t.Fatal("a multi-layer request must come back unchanged")
	}
	if !HasFormulaTotals(attrs) || HasFormulaTotals(attrs[:1]) {
		t.Fatal("HasFormulaTotals")
	}
}
