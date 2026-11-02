package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// AGG_STDDEV / AGG_VARIANCE params.ddof (reference-validation E3): 0
// (default) is the population figure m2 / n, 1 the sample figure
// m2 / (n − 1) — weighted, AGG_WELFORD's m2_w / (Σw − 1). Oracle:
//
//	x = [2, 4, 4, 4, 5, 5, 7, 9]: n = 8, mean = 5, m2 = 32,
//	population variance 4, sample variance 32/7.
var ddofX = []float64{2, 4, 4, 4, 5, 5, 7, 9}

const (
	ddofM2        = 32.0
	ddofPopVar    = 4.0
	ddofSampleVar = 32.0 / 7.0
)

func ddofAgg(op types.AggregationType, params string) *types.Aggregation {
	a := &types.Aggregation{Type: op, Field: "value", Label: "x"}
	if params != "" {
		a.Params = json.RawMessage(params)
	}
	return a
}

// ddofWant is the scalar op returns for a variance v.
func ddofWant(op types.AggregationType, v float64) float64 {
	if op == types.AGG_STDDEV {
		return math.Sqrt(v)
	}
	return v
}

// ddofPaths runs spec on the buffered, streaming and merged (two
// partials, Chan-Welford) paths, returning each scalar and its
// components.
func ddofPaths(t *testing.T, spec *types.Aggregation, records []*Record) (map[string]float64, map[string]map[string]any) {
	t.Helper()
	vals := map[string]float64{}
	comps := map[string]map[string]any{}
	components := func(a Aggregator) map[string]any {
		m, err := a.(MetaAggregator).Components()
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	buf := wcoreNew(t, spec)
	v, err := buf.Aggregate(records, "value")
	if err != nil {
		t.Fatal(err)
	}
	vals["buffered"], comps["buffered"] = v, components(buf)

	str := wcoreNew(t, spec)
	vals["streamed"], comps["streamed"] = wcoreStream(t, str, records), components(str)

	left, right := wcoreNew(t, spec), wcoreNew(t, spec)
	half := len(records) / 2
	for _, r := range records[:half] {
		_ = left.(OnlineAggregator).UpdateRow(r, "value")
	}
	for _, r := range records[half:] {
		_ = right.(OnlineAggregator).UpdateRow(r, "value")
	}
	if err := left.(MergeableAggregator).MergeOnline(right.(OnlineAggregator)); err != nil {
		t.Fatal(err)
	}
	m, err := left.(OnlineAggregator).Finalize()
	if err != nil {
		t.Fatal(err)
	}
	vals["merged"], comps["merged"] = m, components(left)
	return vals, comps
}

// TestDDOF_Unweighted: ddof 1 is the sample figure on every path, and
// the components' variance / stddev equal the scalar while m2 stays the
// raw sum of squared deviations.
func TestDDOF_Unweighted(t *testing.T) {
	t.Parallel()

	records := unitlessRecords(ddofX)
	for _, op := range []types.AggregationType{types.AGG_STDDEV, types.AGG_VARIANCE} {
		for _, tc := range []struct {
			params string
			want   float64
		}{
			{`{"ddof":1}`, ddofSampleVar},
			{`{"ddof":1.0}`, ddofSampleVar},
			{`{"ddof":0}`, ddofPopVar},
			{"", ddofPopVar},
		} {
			t.Run(string(op)+tc.params, func(t *testing.T) {
				vals, comps := ddofPaths(t, ddofAgg(op, tc.params), records)
				for path, got := range vals {
					if !wvClose(got, ddofWant(op, tc.want)) {
						t.Errorf("%s = %.17g, want %.17g", path, got, ddofWant(op, tc.want))
					}
					c := comps[path]
					if !wvClose(c["variance"].(float64), tc.want) {
						t.Errorf("%s components variance = %v, want %v", path, c["variance"], tc.want)
					}
					if !wvClose(c["m2"].(float64), ddofM2) {
						t.Errorf("%s components m2 = %v, want raw %v", path, c["m2"], ddofM2)
					}
					if op == types.AGG_STDDEV && c["stddev"].(float64) != got {
						t.Errorf("%s components stddev = %v, scalar %v", path, c["stddev"], got)
					}
					if op == types.AGG_VARIANCE && c["variance"].(float64) != got {
						t.Errorf("%s components variance = %v, scalar %v", path, c["variance"], got)
					}
				}
			})
		}
	}
}

// unitlessRecords builds unweighted value-only records.
func unitlessRecords(xs []float64) []*Record {
	out := make([]*Record, len(xs))
	for i, x := range xs {
		out[i] = NewRecord(cohortSchema(), map[string]float64{"value": x})
	}
	return out
}

// TestDDOF_AbsentIsZeroBitExact: ddof absent and ddof 0 return the same
// bits on every path, scalar and components alike.
func TestDDOF_AbsentIsZeroBitExact(t *testing.T) {
	t.Parallel()

	xs := []float64{0.1, 0.7, 1e-3, 3.3, 2.2, 9.75, 0.3}
	for _, records := range [][]*Record{unitlessRecords(xs), wvRecords(wvOracleX, wvOracleW)} {
		for _, op := range []types.AggregationType{types.AGG_STDDEV, types.AGG_VARIANCE} {
			for _, w := range []types.SlotWeight{{}, types.SlotWeightField("weight")} {
				absent := ddofAgg(op, "")
				absent.Weight = w
				zero := ddofAgg(op, `{"ddof":0}`)
				zero.Weight = w
				av, ac := ddofPaths(t, absent, records)
				zv, zc := ddofPaths(t, zero, records)
				for path := range av {
					if !ddofSameBits(av[path], zv[path]) || !ddofSameComponents(ac[path], zc[path]) {
						t.Errorf("%s %s weighted=%v: absent %v %v, ddof 0 %v %v", op, path, !w.IsZero(), av[path], ac[path], zv[path], zc[path])
					}
				}
			}
		}
	}
}

// ddofSameBits is bit equality with every NaN equal (a NaN's sign bit is
// platform-dependent, never compared).
func ddofSameBits(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

// ddofSameComponents is ddofSameBits over two components maps.
func ddofSameComponents(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		af, aok := av.(float64)
		bf, bok := bv.(float64)
		if aok != bok || (aok && !ddofSameBits(af, bf)) || (!aok && !reflect.DeepEqual(av, bv)) {
			return false
		}
	}
	return true
}

// TestDDOF_SingleRowIsNull: n = 1 under ddof 1 is undefined (NaN, JSON
// null) on every path, never 0; ddof 0 keeps its 0.
func TestDDOF_SingleRowIsNull(t *testing.T) {
	t.Parallel()

	records := unitlessRecords([]float64{42})
	for _, op := range []types.AggregationType{types.AGG_STDDEV, types.AGG_VARIANCE} {
		vals, comps := ddofSingle(t, ddofAgg(op, `{"ddof":1}`), records)
		for path, got := range vals {
			if !math.IsNaN(got) {
				t.Errorf("%s %s ddof 1 n=1 = %v, want NaN", op, path, got)
			}
			if v := comps[path]["variance"].(float64); !math.IsNaN(v) {
				t.Errorf("%s %s components variance = %v, want NaN", op, path, v)
			}
		}
		vals, _ = ddofSingle(t, ddofAgg(op, ""), records)
		for path, got := range vals {
			if got != 0 {
				t.Errorf("%s %s ddof 0 n=1 = %v, want 0", op, path, got)
			}
		}
	}
}

// ddofSingle is ddofPaths without the merge split (one record).
func ddofSingle(t *testing.T, spec *types.Aggregation, records []*Record) (map[string]float64, map[string]map[string]any) {
	t.Helper()
	vals := map[string]float64{}
	comps := map[string]map[string]any{}
	buf := wcoreNew(t, spec)
	v, err := buf.Aggregate(records, "value")
	if err != nil {
		t.Fatal(err)
	}
	vals["buffered"] = v
	comps["buffered"], _ = buf.(MetaAggregator).Components()
	str := wcoreNew(t, spec)
	vals["streamed"] = wcoreStream(t, str, records)
	comps["streamed"], _ = str.(MetaAggregator).Components()
	return vals, comps
}

// TestDDOF_Weighted: under a frequency or a probability weight, ddof 1
// is AGG_WELFORD's sample variance m2_w / (Σw − 1) on every path, and
// null when Σw ≤ 1.
func TestDDOF_Weighted(t *testing.T) {
	t.Parallel()

	// Frequency weights must be whole counts. x = [2, 4, 4, 5, 7, 9],
	// w = [1, 2, 1, 3, 1, 2]: Σw = 10, mean = 27/5, m2 = 232/5,
	// sample variance 232/45 (exact rationals).
	freqW := []float64{1, 2, 1, 3, 1, 2}
	const freqM2, freqVar = 232.0 / 5.0, 232.0 / 45.0
	for _, tc := range []struct {
		kind         types.WeightKind
		records      []*Record
		m2, variance float64
		light        []*Record // Σw ≤ 1
	}{
		{types.WeightKindFrequency, wvRecords(wvOracleX, freqW), freqM2, freqVar, wvRecords([]float64{3}, []float64{1})},
		{types.WeightKindProbability, wvRecords(wvOracleX, wvOracleW), wvOracleM2, wvOracleVariance, wvRecords([]float64{3, 5}, []float64{0.25, 0.5})},
	} {
		records := tc.records
		for _, op := range []types.AggregationType{types.AGG_STDDEV, types.AGG_VARIANCE} {
			t.Run(string(tc.kind)+"/"+string(op), func(t *testing.T) {
				spec := ddofAgg(op, `{"ddof":1}`)
				spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: tc.kind})
				vals, comps := ddofPaths(t, spec, records)
				for path, got := range vals {
					if !wvClose(got, ddofWant(op, tc.variance)) {
						t.Errorf("%s = %.17g, want %.17g", path, got, ddofWant(op, tc.variance))
					}
					if !wvClose(comps[path]["variance"].(float64), tc.variance) || !wvClose(comps[path]["m2"].(float64), tc.m2) {
						t.Errorf("%s components %v", path, comps[path])
					}
				}

				// The same figure AGG_WELFORD reports on these rows.
				wel := ddofAgg(types.AGG_WELFORD, "")
				wel.Weight = spec.Weight
				wa := wcoreNew(t, wel)
				if _, err := wa.Aggregate(records, "value"); err != nil {
					t.Fatal(err)
				}
				wc, _ := wa.(MetaAggregator).Components()
				if !wvClose(comps["streamed"]["variance"].(float64), wc["variance"].(float64)) {
					t.Errorf("variance %v, AGG_WELFORD %v", comps["streamed"]["variance"], wc["variance"])
				}

				// Σw ≤ 1: undefined, never 0.
				lv, _ := ddofSingle(t, spec, tc.light)
				for path, got := range lv {
					if !math.IsNaN(got) {
						t.Errorf("Σw ≤ 1 %s = %v, want NaN", path, got)
					}
				}
			})
		}
	}
}

// TestDDOF_InvalidRefused: a ddof other than 0 / 1 (or not a number) is
// PROCESSING_CONFIG from the factory, weighted or not.
func TestDDOF_InvalidRefused(t *testing.T) {
	t.Parallel()

	for _, op := range []types.AggregationType{types.AGG_STDDEV, types.AGG_VARIANCE} {
		for _, params := range []string{`{"ddof":2}`, `{"ddof":-1}`, `{"ddof":0.5}`, `{"ddof":"1"}`, `{"ddof":true}`} {
			for _, w := range []types.SlotWeight{{}, types.SlotWeightField("weight")} {
				spec := ddofAgg(op, params)
				spec.Weight = w
				_, err := aggregatorRegistry[op](spec, cohortSchema())
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG {
					t.Errorf("%s %s weighted=%v: err = %v, want PROCESSING_CONFIG", op, params, !w.IsZero(), err)
				}
			}
		}
	}
}

// TestDDOF_Decimal: a decimal128 field honours ddof in the decimal
// two-pass (and refuses an invalid one there too).
func TestDDOF_Decimal(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "v", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2},
	}}
	// {1, 2, 3, 4, 5}: m2 = 10, sample variance 10/4 = 2.5.
	records := []*Record{
		makeDecimalRecord(t, schema, "v", "1.00", 2),
		makeDecimalRecord(t, schema, "v", "2.00", 2),
		makeDecimalRecord(t, schema, "v", "3.00", 2),
		makeDecimalRecord(t, schema, "v", "4.00", 2),
		makeDecimalRecord(t, schema, "v", "5.00", 2),
	}
	slot := func(op types.AggregationType, params string) *types.Aggregation {
		a := &types.Aggregation{Type: op, Field: "v"}
		if params != "" {
			a.Params = json.RawMessage(params)
		}
		return a
	}
	out, err := AggregateDecimalSlot(slot(types.AGG_VARIANCE, `{"ddof":1}`), records, 2)
	if err != nil {
		t.Fatal(err)
	}
	if out.FellBack || out.Value.String(out.Scale) != "2.50000000" {
		t.Errorf("sample variance = %s (fell back %v), want 2.50000000", out.Value.String(out.Scale), out.FellBack)
	}
	out, err = AggregateDecimalSlot(slot(types.AGG_STDDEV, `{"ddof":1}`), records, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Value.String(out.Scale); got != "1.5811" { // √2.5 = 1.58113…
		t.Errorf("sample stddev = %s, want 1.5811", got)
	}
	out, err = AggregateDecimalSlot(slot(types.AGG_VARIANCE, ""), records, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Value.String(out.Scale); got != "2.00000000" {
		t.Errorf("population variance = %s, want 2.00000000", got)
	}
	// One value under ddof 1: undefined.
	out, err = AggregateDecimalSlot(slot(types.AGG_STDDEV, `{"ddof":1}`), records[:1], 2)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Undefined {
		t.Errorf("n=1 ddof 1 = %+v, want undefined", out)
	}
	// The f64 fallback honours ddof too.
	if fb := decimalVarianceFloat64(nil, 2, types.AGG_VARIANCE, 1); !math.IsNaN(fb.Float) {
		t.Errorf("fallback empty = %v, want NaN", fb.Float)
	}
	vals := []encoding.Decimal128{records[0].decimalOf(t, "v"), records[1].decimalOf(t, "v")}
	if fb := decimalVarianceFloat64(vals, 2, types.AGG_VARIANCE, 1); fb.Float != 0.5 {
		t.Errorf("fallback {1, 2} ddof 1 = %v, want 0.5", fb.Float)
	}
	_, err = AggregateDecimalSlot(slot(types.AGG_VARIANCE, `{"ddof":3}`), records, 2)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG {
		t.Errorf("ddof 3 on decimal: err = %v, want PROCESSING_CONFIG", err)
	}
}

// decimalOf reads a decimal value off a test record.
func (r *Record) decimalOf(t *testing.T, field string) encoding.Decimal128 {
	t.Helper()
	v, ok := r.WideValue(field)
	if !ok {
		t.Fatalf("no wide value for %s", field)
	}
	return v.(encoding.Decimal128)
}

// TestDDOF_ProcessorArms: the Processor's buffered and streaming arms
// and a crosstab (cells and margins, buffered and fused) all honour
// ddof — the margin recomputes over raw rows with the same param.
func TestDDOF_ProcessorArms(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "value", Type: encoding.FieldTypeF64},
		{Name: "g", Type: encoding.FieldTypeU8},
	}}
	gs := []float64{1, 1, 1, 1, 2, 2, 2, 2}
	recs := func() []*Record {
		out := make([]*Record, len(ddofX))
		for i, x := range ddofX {
			out[i] = NewRecord(schema, map[string]float64{"value": x, "g": gs[i]})
		}
		return out
	}
	agg := &types.Aggregation{Type: types.AGG_VARIANCE, Field: "value", Label: "v", Params: json.RawMessage(`{"ddof":1}`)}
	p := NewProcessor(schema)
	resp, err := p.Process(context.Background(), &types.Request{Aggregations: []*types.Aggregation{agg}}, NewSliceIterator(recs()))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["v"].(float64); !wvClose(got, ddofSampleVar) {
		t.Errorf("process = %v, want %v", got, ddofSampleVar)
	}
	if !p.canStream(&types.Request{Aggregations: []*types.Aggregation{agg}}) {
		t.Fatal("ddof 1 slot must stay streamable")
	}

	// Crosstab: rows g, cell AGG_VARIANCE ddof 1. Group 1 {2,4,4,4}:
	// m2 = 3, sample 1; group 2 {5,5,7,9}: m2 = 11, sample 11/3; grand
	// total 32/7.
	ct := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Cell:    agg,
		Margins: types.CrosstabMargins{Rows: true, Grand: true},
		Shape:   types.CrosstabShapeMatrix,
	}}
	for _, fused := range []bool{false, true} {
		var resp *types.Response
		if fused {
			resp, err = NewProcessor(schema).RunCrosstabFused(context.Background(), ct, NewSliceIterator(recs()))
		} else {
			resp, err = NewProcessor(schema).RunCrosstab(context.Background(), ct, recs())
		}
		if err != nil {
			t.Fatalf("fused=%v: %v", fused, err)
		}
		m := resp.Crosstab.Matrix
		want := []float64{1, 11.0 / 3.0}
		for r, w := range want {
			if got := m.Cells[r][0].Scalar(); !wvClose(got, w) {
				t.Errorf("fused=%v cell %d = %v, want %v", fused, r, got, w)
			}
			if got := m.RowMargins[r].Scalar(); !wvClose(got, w) {
				t.Errorf("fused=%v row margin %d = %v, want %v", fused, r, got, w)
			}
		}
		if got := m.GrandTotal.Scalar(); !wvClose(got, ddofSampleVar) {
			t.Errorf("fused=%v grand total = %v, want %v", fused, got, ddofSampleVar)
		}
	}
}
