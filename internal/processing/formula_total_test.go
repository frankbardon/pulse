package processing

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func totalFormula(t *testing.T, s *encoding.Schema, src string) AttributeComputer {
	t.Helper()
	comp, err := newFormulaAttribute(&types.Attribute{Type: types.ATTR_FORMULA, Expression: src, Label: "v"}, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := BindAttribute(comp, nil, nil); err != nil {
		t.Fatalf("%q: bind: %v", src, err)
	}
	return comp
}

// TestFormulaTotal_Interface: only a formula that calls total() is a
// TwoPassAttribute; any other stays the row-local formulaAttribute.
func TestFormulaTotal_Interface(t *testing.T) {
	s := exprParitySchema()
	if _, ok := totalFormula(t, s, "x * 2").(*formulaAttribute); !ok {
		t.Fatal("plain formula is not the row-local formulaAttribute")
	}
	if _, ok := totalFormula(t, s, "x * 2").(TwoPassAttribute); ok {
		t.Fatal("plain formula must not be a TwoPassAttribute")
	}
	if _, ok := totalFormula(t, s, "x / total(x)").(TwoPassAttribute); !ok {
		t.Fatal("a total() formula must be a TwoPassAttribute")
	}
}

// TestFormulaTotal_ExactSequentialSum: total(expr) is the float64 sum
// of expr over the records, in record order, null rows skipped — bit
// for bit.
func TestFormulaTotal_ExactSequentialSum(t *testing.T) {
	s := exprParitySchema()
	recs := exprParityRecords(s, 203, true)
	var want float64
	for _, r := range recs {
		if v, ok := r.NumericValue("x"); ok {
			want += v / 3
		}
	}
	got, err := totalFormula(t, s, "total(x / 3) + 0 * y").Compute(recs, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		if math.Float64bits(v) != math.Float64bits(want) {
			t.Fatalf("row %d: %v (%x), want %v (%x)", i, v, math.Float64bits(v), want, math.Float64bits(want))
		}
	}
	// A row-level term beside the total: x / total(x) on full rows.
	var sx float64
	for _, r := range recs {
		if v, ok := r.NumericValue("x"); ok {
			sx += v
		}
	}
	shares, err := totalFormula(t, s, "(x ?? 0) / total(x)").Compute(recs, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range recs {
		x, _ := r.NumericValue("x")
		if shares[i] != x/sx {
			t.Fatalf("row %d: share %v, want %v", i, shares[i], x/sx)
		}
	}
}

// TestFormulaTotal_EmptyIsNaN: a total over zero non-null rows is NaN,
// so the formula is NaN on every row — never 0.
func TestFormulaTotal_EmptyIsNaN(t *testing.T) {
	s := exprParitySchema()
	var nullOnly []*Record
	for _, r := range exprParityRecords(s, 40, true) {
		if _, ok := r.NumericValue("x"); !ok {
			nullOnly = append(nullOnly, r)
		}
	}
	if len(nullOnly) == 0 {
		t.Fatal("degenerate fixture: no null rows")
	}
	got, err := totalFormula(t, s, "y + total(x)").Compute(nullOnly, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		if !math.IsNaN(v) {
			t.Fatalf("row %d: %v, want NaN", i, v)
		}
	}
}

// TestFormulaTotal_ZeroDivisor: a total of exactly 0 used as a divisor
// gives a non-finite row value, which the wire writes as null.
func TestFormulaTotal_ZeroDivisor(t *testing.T) {
	s := exprParitySchema()
	recs := exprParityRecords(s, 20, false)
	got, err := totalFormula(t, s, "(y + 1) / total(y * 0)").Compute(recs, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		if !math.IsInf(v, 1) {
			t.Fatalf("row %d: %v, want +Inf", i, v)
		}
	}
	b, err := types.MarshalFinite(map[string]any{"v": got[0]})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"v":null}` {
		t.Fatalf("wire: %s, want null", b)
	}
}

// TestFormulaTotal_RowBeforeFinalize: reading a total before its
// pre-pass is a coded internal fault, never a silent 0.
func TestFormulaTotal_RowBeforeFinalize(t *testing.T) {
	s := exprParitySchema()
	comp := totalFormula(t, s, "x / total(x)")
	_, err := comp.(RowLocalAttribute).Row(exprParityRecords(s, 1, false)[0], "")
	if !errors.HasCode(err, errors.PROCESSING_INTERNAL) {
		t.Fatalf("Row before Finalize: %v, want PROCESSING_INTERNAL", err)
	}
}

// TestFormulaTotal_BuildRefusals: nesting and arity are refused when
// the formula builds; a total() in a filter or overlay formula is
// refused by its own build.
func TestFormulaTotal_BuildRefusals(t *testing.T) {
	s := exprParitySchema()
	for src, code := range map[string]errors.Code{
		"total(x * total(y))": errors.PULSE_FORMULA_TOTAL_NESTED,
		"total(x, y)":         errors.PULSE_FORMULA_TOTAL_ARITY,
	} {
		comp, err := newFormulaAttribute(&types.Attribute{Type: types.ATTR_FORMULA, Expression: src}, s)
		if err != nil {
			t.Fatal(err)
		}
		if err := BindAttribute(comp, nil, nil); !errors.HasCode(err, code) {
			t.Errorf("%q: %v, want %s", src, err, code)
		}
	}
	if _, err := BuildFilters([]*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x > total(x) / 10"}}, s, nil); !errors.HasCode(err, errors.PULSE_FORMULA_TOTAL_SITE) {
		t.Errorf("filter: %v, want PULSE_FORMULA_TOTAL_SITE", err)
	}
	spec := &types.OverlaySpec{Kind: types.OverlayKindFormula}
	if _, err := compileFormulaProgram(spec, "value / total(value)", map[string]any{"value": 0.0, "total": 0.0}); !errors.HasCode(err, errors.PULSE_FORMULA_TOTAL_SITE) {
		t.Errorf("overlay: %v, want PULSE_FORMULA_TOTAL_SITE", err)
	}
}

func totalRunReq(attrs []*types.Attribute, groups []*types.Group, filters []*types.Filterer) *types.Request {
	return &types.Request{
		Attributes:   attrs,
		Groups:       groups,
		Filterers:    filters,
		Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "out", Label: "max"}, {Type: types.AGG_MIN, Field: "out", Label: "min"}},
	}
}

func runTotal(t *testing.T, s *encoding.Schema, recs []*Record, req *types.Request) (*types.Response, ProcessPath) {
	t.Helper()
	p := NewProcessor(s)
	resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp, p.LastPath()
}

// TestFormulaTotal_Routing: an ungrouped total() formula takes the
// two-pass streaming drive (its PrePass sees exactly the post-filter
// rows), a grouped one runs buffered and stays cohort-wide, and both
// equal the hand sum. An earlier attribute's label — row-local or
// two-pass — is a valid argument on both arms.
func TestFormulaTotal_Routing(t *testing.T) {
	s := exprParitySchema()
	recs := exprParityRecords(s, 120, true)
	var want float64
	for _, r := range recs {
		y, _ := r.NumericValue("y")
		if v, ok := r.NumericValue("x"); ok && y >= 2 {
			want += v
		}
	}
	filters := []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "y >= 2"}}
	out := &types.Attribute{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "out"}

	resp, path := runTotal(t, s, recs, totalRunReq([]*types.Attribute{out}, nil, filters))
	if path != PathStreaming {
		t.Fatalf("ungrouped total() formula took path %v, want streaming", path)
	}
	if got := resp.Data[0]["max"]; got != want || resp.Data[0]["min"] != want {
		t.Fatalf("streaming: max %v min %v, want %v", got, resp.Data[0]["min"], want)
	}

	grouped, gpath := runTotal(t, s, recs, totalRunReq([]*types.Attribute{out},
		[]*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, filters))
	if gpath != PathBuffered {
		t.Fatalf("grouped total() formula took path %v, want buffered", gpath)
	}
	if len(grouped.Data) < 2 {
		t.Fatalf("degenerate grouping: %d rows", len(grouped.Data))
	}
	for _, row := range grouped.Data {
		if row["max"] != want {
			t.Fatalf("group %v: total %v, want the cohort-wide %v", row["cat"], row["max"], want)
		}
	}

	// Earlier labels: a row-local formula and a two-pass z-score.
	chain := []*types.Attribute{
		{Type: types.ATTR_FORMULA, Expression: "(x ?? 0) * 2", Label: "u"},
		{Type: types.ATTR_ZSCORE, Field: "u", Label: "z"},
		{Type: types.ATTR_FORMULA, Expression: "total(u) + total(z * z)", Label: "out"},
	}
	sResp, sPath := runTotal(t, s, recs, totalRunReq(chain, nil, filters))
	if sPath != PathStreaming {
		t.Fatalf("chained total() took path %v, want streaming", sPath)
	}
	buffered := append(append([]*types.Attribute(nil), chain...),
		&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "y", Label: "pct"})
	bResp, bPath := runTotal(t, s, recs, totalRunReq(buffered, nil, filters))
	if bPath != PathBuffered {
		t.Fatalf("percentile request took path %v, want buffered", bPath)
	}
	var n int
	var su float64
	for _, r := range recs {
		y, _ := r.NumericValue("y")
		if y < 2 {
			continue
		}
		n++
		x, _ := r.NumericValue("x")
		su += x * 2
	}
	// Σz² over the population is n (population sd); Σu is exact.
	ref := su + float64(n)
	for name, resp := range map[string]*types.Response{"streaming": sResp, "buffered": bResp} {
		got := resp.Data[0]["max"].(float64)
		if math.Abs(got-ref) > 1e-9*ref {
			t.Errorf("%s: %v, want %v", name, got, ref)
		}
	}
	if sResp.Data[0]["max"] != bResp.Data[0]["max"] {
		t.Errorf("streaming %v != buffered %v", sResp.Data[0]["max"], bResp.Data[0]["max"])
	}
}

// TestFormulaTotal_NotMergeable: a total() formula is not row-local, so
// the parallel reducers and ProcessChain refuse to fan it out (each
// partition's total would be partition-local); a plain formula merges.
func TestFormulaTotal_NotMergeable(t *testing.T) {
	s := exprParitySchema()
	req := func(expr string) *types.Request {
		return &types.Request{
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: expr, Label: "out"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "out"}},
		}
	}
	if !CanMergeRequest(req("x * 2"), s) {
		t.Fatal("plain formula must stay mergeable")
	}
	if CanMergeRequest(req("x / total(x)"), s) {
		t.Fatal("a total() formula must not be mergeable")
	}
}

// TestFormulaTotal_CrosstabDeclines: the fused crosstab arm never runs
// a PrePass, so a total() formula must decline fusion. The shared rule's
// type-keyed two-pass fact does not see it (ATTR_FORMULA is row-local by
// type); the name-keyed ATTR_FORMULA bail does, which is why
// registryFuseFacts.AttributeTwoPass may stay type-keyed. The runtime
// AssertCanFuse echo refuses it per attribute.
func TestFormulaTotal_CrosstabDeclines(t *testing.T) {
	s := exprParitySchema()
	req := &types.Request{
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "x / total(x)", Label: "share"}},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "share"},
		},
	}
	if ok, reason := CanFuseCrosstab(req, s, nil); ok {
		t.Fatal("a total() formula must decline the fused crosstab arm")
	} else if !strings.Contains(reason, "ATTR_FORMULA") {
		t.Fatalf("decline reason %q, want the ATTR_FORMULA bail", reason)
	}
	state, err := NewFusedCrosstabState(req.Crosstab, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = state.AssertCanFuse(req)
	if !errors.HasCode(err, errors.PROCESSING_INTERNAL) {
		t.Fatalf("AssertCanFuse: %v, want the PROCESSING_INTERNAL two-pass refusal", err)
	}
	if !strings.Contains(err.Error(), "two-pass attribute") {
		t.Fatalf("AssertCanFuse refused for the wrong reason: %v", err)
	}
}

// weightedTotalFormula is totalFormula with a slot weight on the
// attribute (the stamped spec StampWeightsWith leaves on it).
func weightedTotalFormula(t *testing.T, s *encoding.Schema, src string, w *types.WeightSpec) AttributeComputer {
	t.Helper()
	attr := &types.Attribute{Type: types.ATTR_FORMULA, Expression: src, Label: "v"}
	if w != nil {
		attr.Weight = types.SlotWeightOf(*w)
	}
	comp, err := newFormulaAttribute(attr, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := BindAttribute(comp, nil, nil); err != nil {
		t.Fatalf("%q: bind: %v", src, err)
	}
	return comp
}

// TestFormulaTotal_FrequencyWeighted: under a frequency weight
// total(expr) is Σw·expr in record order (the expanded-row total) — a
// zero-weight row adds nothing and a null argument is still skipped —
// bit for bit on the buffered arm and through both Processor arms
// (ungrouped two-pass streaming, grouped buffered).
func TestFormulaTotal_FrequencyWeighted(t *testing.T) {
	s := exprParitySchema()
	recs := exprParityRecords(s, 203, true)
	freq := &types.WeightSpec{Field: "y", Kind: types.WeightKindFrequency}
	var want, unweighted float64
	for _, r := range recs {
		y, _ := r.NumericValue("y")
		if v, ok := r.NumericValue("x"); ok {
			unweighted += v
			if y != 0 {
				want += float64(y * v)
			}
		}
	}
	if want == unweighted {
		t.Fatal("degenerate fixture: weighted total equals unweighted")
	}
	got, err := weightedTotalFormula(t, s, "total(x) + 0 * y", freq).Compute(recs, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		if math.Float64bits(v) != math.Float64bits(want) {
			t.Fatalf("row %d: %v, want Σw·x %v", i, v, want)
		}
	}
	out := &types.Attribute{Type: types.ATTR_FORMULA, Expression: "total(x)", Label: "out", Weight: types.SlotWeightOf(*freq)}
	resp, path := runTotal(t, s, recs, totalRunReq([]*types.Attribute{out}, nil, nil))
	if path != PathStreaming {
		t.Fatalf("path %v, want streaming", path)
	}
	if resp.Data[0]["max"] != want {
		t.Fatalf("streaming: %v, want %v", resp.Data[0]["max"], want)
	}
	grouped, gpath := runTotal(t, s, recs, totalRunReq([]*types.Attribute{out},
		[]*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, nil))
	if gpath != PathBuffered {
		t.Fatalf("grouped path %v, want buffered", gpath)
	}
	for _, row := range grouped.Data {
		if row["max"] != want {
			t.Fatalf("group %v: %v, want %v", row["cat"], row["max"], want)
		}
	}
}

// TestFormulaTotal_UnitWeightByteIdentical: a weight of 1 on every row
// leaves total() bit-identical to the unweighted sum, and an unweighted
// slot is untouched.
func TestFormulaTotal_UnitWeightByteIdentical(t *testing.T) {
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "w", Type: encoding.FieldTypeU16},
	}}
	recs := make([]*Record, 97)
	for i := range recs {
		vals := map[string]float64{"x": float64(i%13)/7 + 0.1, "w": 1}
		nulls := map[string]bool{}
		if i%5 == 4 {
			delete(vals, "x")
			nulls["x"] = true
		}
		recs[i] = NewRecordWithWide(s, vals, nulls, nil)
	}
	plain, err := weightedTotalFormula(t, s, "(x ?? 0) / total(x)", nil).Compute(recs, "")
	if err != nil {
		t.Fatal(err)
	}
	unit, err := weightedTotalFormula(t, s, "(x ?? 0) / total(x)", &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}).Compute(recs, "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range plain {
		if math.Float64bits(plain[i]) != math.Float64bits(unit[i]) {
			t.Fatalf("row %d: unit weight %v, unweighted %v", i, unit[i], plain[i])
		}
	}
}
