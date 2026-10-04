package processing

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// Weighted core aggregators (weighting-descriptive E1-S3). The oracle
// is the AGG_WEIGHTED_MEAN fixture (aggregator_weighted_variance_test.go,
// exact rational constants):
//
//	x = [2, 4, 4, 5, 7, 9], w = [1/2, 3/2, 1, 2, 4/5, 6/5]
//	Σw = 7, Σwx = 187/5, mean = 187/35, m2 = 5001/175
var wcoreSpecs = []types.AggregationType{
	types.AGG_COUNT, types.AGG_SUM, types.AGG_AVERAGE, types.AGG_WEIGHTED_MEAN,
	types.AGG_VARIANCE, types.AGG_STDDEV, types.AGG_WELFORD,
}

func wcoreWant(op types.AggregationType) float64 {
	switch op {
	case types.AGG_COUNT:
		return wvOracleSumWeights
	case types.AGG_SUM:
		return wvOracleSumWeighted
	case types.AGG_VARIANCE:
		return wvOracleM2 / wvOracleSumWeights
	case types.AGG_STDDEV:
		return math.Sqrt(wvOracleM2 / wvOracleSumWeights)
	}
	return wvOracleMean
}

func wcoreAgg(op types.AggregationType, w types.SlotWeight) *types.Aggregation {
	return &types.Aggregation{Type: op, Field: "value", Label: "x", Weight: w}
}

func wcoreNew(t *testing.T, a *types.Aggregation) Aggregator {
	t.Helper()
	f, ok := aggregatorRegistry[a.Type]
	if !ok {
		t.Fatalf("no factory for %s", a.Type)
	}
	agg, err := f(a, cohortSchema())
	if err != nil {
		t.Fatalf("%s: %v", a.Type, err)
	}
	return agg
}

func wcoreStream(t *testing.T, agg Aggregator, records []*Record) float64 {
	t.Helper()
	oa := agg.(OnlineAggregator)
	for _, r := range records {
		if err := oa.UpdateRow(r, "value"); err != nil {
			t.Fatal(err)
		}
	}
	v, err := oa.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestWeightedCore_Oracle: every weighted core aggregator returns the
// exact weighted figure on the buffered, streaming and merged paths —
// COUNT Σw, SUM Σwx, AVERAGE / WEIGHTED_MEAN Σwx/Σw, VARIANCE / STDDEV
// the population m2/Σw, WELFORD the running mean plus the sample
// m2/(Σw−1) triple.
func TestWeightedCore_Oracle(t *testing.T) {
	records := wvRecords(wvOracleX, wvOracleW)
	for _, op := range wcoreSpecs {
		t.Run(string(op), func(t *testing.T) {
			spec := wcoreAgg(op, types.SlotWeightField("weight"))
			want := wcoreWant(op)

			buffered, err := wcoreNew(t, spec).Aggregate(records, "value")
			if err != nil {
				t.Fatal(err)
			}
			streamed := wcoreStream(t, wcoreNew(t, spec), records)

			left, right := wcoreNew(t, spec), wcoreNew(t, spec)
			_ = wcoreStream(t, left, nil) // Finalize-reset on an empty instance is harmless
			for _, r := range records[:2] {
				_ = left.(OnlineAggregator).UpdateRow(r, "value")
			}
			for _, r := range records[2:] {
				_ = right.(OnlineAggregator).UpdateRow(r, "value")
			}
			if err := left.(MergeableAggregator).MergeOnline(right.(OnlineAggregator)); err != nil {
				t.Fatal(err)
			}
			merged, _ := left.(OnlineAggregator).Finalize()

			for name, got := range map[string]float64{"buffered": buffered, "streamed": streamed, "merged": merged} {
				if !wvClose(got, want) {
					t.Errorf("%s %s = %.17g, want %.17g", op, name, got, want)
				}
			}
			if op == types.AGG_WELFORD {
				rich, _ := left.(RichAggregator).Rich()
				tr, ok := rich.(WelfordTriple)
				if !ok || !wvClose(tr.Variance, wvOracleVariance) || tr.N != uint64(len(records)) {
					t.Errorf("welford triple = %+v, want variance %v n %d", rich, wvOracleVariance, len(records))
				}
			}
		})
	}
}

// unitRecords mirrors records with an all-ones weight column.
func unitRecords(xs []float64) []*Record {
	ws := make([]float64, len(xs))
	for i := range ws {
		ws[i] = 1
	}
	return wvRecords(xs, ws)
}

// weightedOnlyKeys are the operator keys only the weighted AGG_AVERAGE
// emits (it gains AGG_WEIGHTED_MEAN's moments); unity parity compares
// the rest.
var weightedOnlyKeys = map[string]bool{
	"sum_weighted": true, "weighted_mean": true, "m2_weighted": true,
	"sum_weights_sq": true, "weighted_variance": true,
}

func stripWeightedOnly(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := map[string]any{}
	for k, v := range m {
		if !weightedOnlyKeys[k] {
			out[k] = v
		}
	}
	return out
}

// TestWeightedCore_UnityParity: an all-ones weight reproduces the
// unweighted scalar and components BIT FOR BIT on the buffered,
// streaming and merged paths (the operation-order rule). Values are
// chosen so a reassociated formula would drift in the last ULP.
func TestWeightedCore_UnityParity(t *testing.T) {
	// 400 deterministic values over several magnitudes: enough rows
	// that a reassociated recurrence ((w/Σw)·δ instead of (w·δ)/Σw, or
	// a regrouped merge term) drifts in the last ULP somewhere.
	xs := make([]float64, 400)
	seed := uint64(0x9e3779b97f4a7c15)
	for i := range xs {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		xs[i] = float64(seed%1_000_003)/997.0 + float64(i%7)*1e-3
	}
	records := unitRecords(xs)
	for _, op := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM, types.AGG_AVERAGE, types.AGG_VARIANCE, types.AGG_STDDEV, types.AGG_WELFORD} {
		t.Run(string(op), func(t *testing.T) {
			plain := wcoreAgg(op, types.SlotWeight{})
			weighted := wcoreAgg(op, types.SlotWeightField("weight"))

			type run func(Aggregator) float64
			runs := map[string]run{
				"buffered": func(a Aggregator) float64 { v, _ := a.Aggregate(records, "value"); return v },
				"streamed": func(a Aggregator) float64 { return wcoreStream(t, a, records) },
			}
			for name, fn := range runs {
				pa, wa := wcoreNew(t, plain), wcoreNew(t, weighted)
				pv, wv := fn(pa), fn(wa)
				if math.Float64bits(pv) != math.Float64bits(wv) {
					t.Errorf("%s scalar: unweighted %.17g, unit-weighted %.17g", name, pv, wv)
				}
				pc, _ := pa.(MetaAggregator).Components()
				wc, _ := wa.(MetaAggregator).Components()
				if !reflect.DeepEqual(pc, stripWeightedOnly(wc)) {
					t.Errorf("%s components: unweighted %v, unit-weighted %v", name, pc, wc)
				}
			}

			// Merge: the weighted Chan-Welford merge is mergeWelford's
			// operation sequence at unit weights.
			merge := func(a *types.Aggregation) (float64, map[string]any) {
				// Uneven partitions folded in order, as the reducers do.
				l := wcoreNew(t, a)
				cuts := []int{0, 3, 41, 42, 157, 230, 311, 377, len(records)}
				for i := 0; i+1 < len(cuts); i++ {
					part := wcoreNew(t, a)
					for _, rec := range records[cuts[i]:cuts[i+1]] {
						_ = part.(OnlineAggregator).UpdateRow(rec, "value")
					}
					if err := l.(MergeableAggregator).MergeOnline(part.(OnlineAggregator)); err != nil {
						t.Fatal(err)
					}
				}
				v, _ := l.(OnlineAggregator).Finalize()
				c, _ := l.(MetaAggregator).Components()
				return v, c
			}
			pv, pc := merge(plain)
			wv, wc := merge(weighted)
			if math.Float64bits(pv) != math.Float64bits(wv) || !reflect.DeepEqual(pc, stripWeightedOnly(wc)) {
				t.Errorf("merged: unweighted %.17g %v, unit-weighted %.17g %v", pv, pc, wv, wc)
			}
		})
	}
}

// TestWeightedCore_InvalidWeightsExcluded: null, negative, NaN, ±Inf
// and (under frequency) fractional weights are excluded — never
// coerced — and a zero weight contributes nothing.
func TestWeightedCore_InvalidWeightsExcluded(t *testing.T) {
	good := []*Record{cohortRec(10, 1, 0, 0, nil), cohortRec(20, 3, 0, 0, nil)}
	bad := []*Record{
		cohortRec(1000, 0, 0, 0, map[string]bool{"weight": true}),
		cohortRec(1000, -2, 0, 0, nil),
		cohortRec(1000, math.NaN(), 0, 0, nil),
		cohortRec(1000, math.Inf(1), 0, 0, nil),
		cohortRec(1000, math.Inf(-1), 0, 0, nil),
		cohortRec(1000, 0, 0, 0, nil), // zero: valid, contributes nothing
	}
	records := append(append([]*Record{}, good...), bad...)
	for _, op := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM, types.AGG_AVERAGE, types.AGG_VARIANCE} {
		spec := wcoreAgg(op, types.SlotWeightField("weight"))
		got, _ := wcoreNew(t, spec).Aggregate(records, "value")
		want, _ := wcoreNew(t, spec).Aggregate(good, "value")
		if got != want {
			t.Errorf("%s with invalid rows = %v, want %v (invalid weights must be excluded)", op, got, want)
		}
	}

	frac := append(append([]*Record{}, good...), cohortRec(1000, 2.5, 0, 0, nil))
	freq := wcoreAgg(types.AGG_SUM, types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: types.WeightKindFrequency}))
	if got, _ := wcoreNew(t, freq).Aggregate(frac, "value"); got != 70 {
		t.Errorf("frequency SUM with a fractional weight = %v, want 70", got)
	}
	prob := wcoreAgg(types.AGG_SUM, types.SlotWeightField("weight"))
	if got, _ := wcoreNew(t, prob).Aggregate(frac, "value"); got != 2570 {
		t.Errorf("probability SUM with a fractional weight = %v, want 2570", got)
	}
}

// TestWeightedCore_WeightedMeanAlias: AGG_WEIGHTED_MEAN is weighted
// AGG_AVERAGE — same scalar — that keeps its own components shape; its
// weight is the stamped slot weight or, unstamped, params.weight_field;
// none at all (or an explicit null) is PROCESSING_CONFIG.
func TestWeightedCore_WeightedMeanAlias(t *testing.T) {
	records := wvRecords(wvOracleX, wvOracleW)
	avg, _ := wcoreNew(t, wcoreAgg(types.AGG_AVERAGE, types.SlotWeightField("weight"))).Aggregate(records, "value")
	wm := wcoreNew(t, wcoreAgg(types.AGG_WEIGHTED_MEAN, types.SlotWeightField("weight")))
	got, _ := wm.Aggregate(records, "value")
	if math.Float64bits(avg) != math.Float64bits(got) {
		t.Fatalf("WEIGHTED_MEAN %v != weighted AVERAGE %v", got, avg)
	}
	c, _ := wm.(MetaAggregator).Components()
	for _, k := range []string{"sum_weighted", "sum_weights", "weighted_mean", "m2_weighted", "sum_weights_sq", "weighted_variance", "n_eff"} {
		if _, ok := c[k]; !ok {
			t.Errorf("WEIGHTED_MEAN components missing %q: %v", k, c)
		}
	}
	if _, ok := c["sum"]; ok {
		t.Errorf("WEIGHTED_MEAN components must keep their shape (no sum): %v", c)
	}

	sugar := &types.Aggregation{Type: types.AGG_WEIGHTED_MEAN, Field: "value", Params: json.RawMessage(`{"weight_field":"weight"}`)}
	if v, _ := wcoreNew(t, sugar).Aggregate(records, "value"); math.Float64bits(v) != math.Float64bits(avg) {
		t.Fatalf("unstamped weight_field = %v, want %v", v, avg)
	}
	for _, a := range []*types.Aggregation{
		{Type: types.AGG_WEIGHTED_MEAN, Field: "value"},
		{Type: types.AGG_WEIGHTED_MEAN, Field: "value", Params: json.RawMessage(`{"weight_field":"weight"}`), Weight: types.NullSlotWeight()},
	} {
		if _, err := newWeightedMeanAggregator(a, nil); !errors.HasCode(err, errors.PROCESSING_CONFIG) {
			t.Errorf("%+v: err = %v, want PROCESSING_CONFIG", a, err)
		}
	}
}

// TestStampWeights: an unweighted request is returned untouched (the
// same pointer); otherwise each aggregation slot — top-level, crosstab
// cell, crosstab margin — carries its resolved weight on a copy:
// slot → weight_field → request → default, an applied weight only on
// a weight-aware operator, `null` elsewhere. Stamping is idempotent
// and never mutates the caller's request.
func TestStampWeights(t *testing.T) {
	plain := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}
	if got := StampWeights(plain, nil); got != plain {
		t.Fatal("unweighted request must be returned as-is")
	}

	def := &types.WeightSpec{Field: "d"}
	req := &types.Request{
		Weight: &types.WeightSpec{Field: "r", Kind: types.WeightKindFrequency},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x"},
			{Type: types.AGG_SUM, Field: "x", Weight: types.SlotWeightField("s")},
			{Type: types.AGG_SUM, Field: "x", Weight: types.NullSlotWeight()},
			{Type: types.AGG_MIN, Field: "x"},
			{Type: types.AGG_WEIGHTED_MEAN, Field: "x", Params: json.RawMessage(`{"weight_field":"p"}`)},
		},
		Crosstab: &types.CrosstabSpec{
			Cell:               &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
			MarginAggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Weight: types.NullSlotWeight()}},
		},
	}
	got := StampWeights(req, def)
	spec := func(a *types.Aggregation) any {
		if a.Weight.IsNull() {
			return "null"
		}
		return *a.Weight.Spec()
	}
	want := []any{
		types.WeightSpec{Field: "r", Kind: types.WeightKindFrequency},
		types.WeightSpec{Field: "s", Kind: types.WeightKindProbability},
		"null", "null",
		types.WeightSpec{Field: "p", Kind: types.WeightKindProbability},
	}
	for i, a := range got.Aggregations {
		if !reflect.DeepEqual(spec(a), want[i]) {
			t.Errorf("aggregations[%d]: %v, want %v", i, spec(a), want[i])
		}
	}
	if s := spec(got.Crosstab.Cell); !reflect.DeepEqual(s, types.WeightSpec{Field: "r", Kind: types.WeightKindFrequency}) {
		t.Errorf("crosstab.cell: %v", s)
	}
	if s := spec(got.Crosstab.MarginAggregations[0]); s != "null" {
		t.Errorf("margin: %v", s)
	}
	// Default only, no request weight.
	req2 := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x"}}}
	if s := spec(StampWeights(req2, def).Aggregations[0]); !reflect.DeepEqual(s, types.WeightSpec{Field: "d", Kind: types.WeightKindProbability}) {
		t.Errorf("default: %v", s)
	}
	// The caller's request is untouched; stamping is idempotent.
	if !req.Aggregations[0].Weight.IsZero() || !req.Crosstab.Cell.Weight.IsZero() || req2.Aggregations[0].Weight.Spec() != nil {
		t.Fatal("StampWeights mutated the caller's request")
	}
	again := StampWeights(got, def)
	for i := range got.Aggregations {
		if !reflect.DeepEqual(spec(again.Aggregations[i]), spec(got.Aggregations[i])) {
			t.Errorf("not idempotent at %d", i)
		}
	}
}

// weightProcSchema is a cohort with a value, a probability weight with
// invalid rows, and a grouping key.
func weightProcSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "value", Type: encoding.FieldTypeF64},
		{Name: "weight", Type: encoding.FieldTypeF64},
		{Name: "g", Type: encoding.FieldTypeU8},
	}}
}

// weightProcRecords: 8 rows; row 5's weight is null, row 6's negative,
// row 7's NaN; row 4's value is null (n_null, its weight not judged by
// the slot floor, though the row-level warning tally counts nothing for
// it: its weight, 9, is valid).
func weightProcRecords() []*Record {
	s := weightProcSchema()
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	ws := []float64{1, 2, 0.5, 1.5, 9, 0, -1, math.NaN()}
	out := make([]*Record, len(vals))
	for i := range vals {
		nulls := map[string]bool{}
		if i == 4 {
			nulls["value"] = true
		}
		if i == 5 {
			nulls["weight"] = true
		}
		out[i] = NewRecordWithNulls(s, map[string]float64{"value": vals[i], "weight": ws[i], "g": float64(i % 2)}, nulls)
	}
	return out
}

func weightProcRequest(w *types.WeightSpec) *types.Request {
	return &types.Request{
		Weight: w,
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value", Label: "sum"},
			{Type: types.AGG_COUNT, Field: "value", Label: "n", Weight: types.NullSlotWeight()},
		},
	}
}

// TestWeightedProcess_FloorAndWarning: on every Process path —
// streaming, buffered, two-pass, grouped streaming — a weighted slot
// carries {sum_weights, n_eff, n_weight_invalid} beside the unchanged
// {n, n_null}, an opted-out slot carries none, and the response
// carries one PULSE_WEIGHT_INVALID_ROWS warning counting every
// filter-passing row with an invalid weight, by reason.
func TestWeightedProcess_FloorAndWarning(t *testing.T) {
	type path struct {
		name string
		run  func(p *Processor, req *types.Request) (*types.Response, error)
	}
	paths := []path{
		{"streaming", func(p *Processor, req *types.Request) (*types.Response, error) {
			return p.Process(context.Background(), req, NewSliceIterator(weightProcRecords()))
		}},
		{"buffered", func(p *Processor, req *types.Request) (*types.Response, error) {
			return p.processRecords(context.Background(), p.stampWeights(req), weightProcRecords())
		}},
		{"two_pass", func(p *Processor, req *types.Request) (*types.Response, error) {
			req.Attributes = []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "g", Label: "gz", Weight: types.NullSlotWeight()}}
			return p.Process(context.Background(), req, NewSliceIterator(weightProcRecords()))
		}},
	}
	for _, pt := range paths {
		t.Run(pt.name, func(t *testing.T) {
			resp, err := pt.run(NewProcessor(weightProcSchema()), weightProcRequest(&types.WeightSpec{Field: "weight"}))
			if err != nil {
				t.Fatal(err)
			}
			// Valid present rows: values 1,2,3,4 (w 1,2,.5,1.5); value 5
			// is null; 6, 7, 8 carry a null, negative and NaN weight.
			if got := resp.Data[0]["sum"]; got != 1*1+2*2+3*0.5+4*1.5 {
				t.Errorf("weighted sum = %v", got)
			}
			if got := resp.Data[0]["n"]; got != 7.0 {
				t.Errorf("opted-out count = %v, want 7 raw rows", got)
			}
			aggs := resp.Components.Aggregations
			if aggs[0].N != 7 || aggs[0].NNull != 1 {
				t.Errorf("floor n/n_null = %d/%d, want 7/1", aggs[0].N, aggs[0].NNull)
			}
			if aggs[0].SumWeights == nil || *aggs[0].SumWeights != 5 ||
				aggs[0].NWeightInvalid == nil || *aggs[0].NWeightInvalid != 3 ||
				aggs[0].NEff == nil || !wvClose(*aggs[0].NEff, 25/7.5) {
				t.Errorf("weighted floor = %v %v %v, want 5 / 25/7.5 / 3", deref(aggs[0].SumWeights), deref(aggs[0].NEff), aggs[0].NWeightInvalid)
			}
			if aggs[1].SumWeights != nil || aggs[1].NEff != nil || aggs[1].NWeightInvalid != nil {
				t.Errorf("opted-out slot carries weighted floor keys: %+v", aggs[1])
			}
			assertInvalidRowsWarning(t, resp, map[string]int64{"null": 1, "negative": 1, "nan_inf": 1, "non_integer_frequency": 0})
		})
	}

	t.Run("grouped_streaming", func(t *testing.T) {
		req := weightProcRequest(&types.WeightSpec{Field: "weight"})
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
		p := NewProcessor(weightProcSchema())
		resp, err := p.Process(context.Background(), req, NewSliceIterator(weightProcRecords()))
		if err != nil {
			t.Fatal(err)
		}
		if p.LastPath() != PathStreaming {
			t.Fatalf("path = %v, want streaming", p.LastPath())
		}
		assertInvalidRowsWarning(t, resp, map[string]int64{"null": 1, "negative": 1, "nan_inf": 1, "non_integer_frequency": 0})
	})

	t.Run("frequency_has_no_n_eff", func(t *testing.T) {
		resp, err := NewProcessor(weightProcSchema()).Process(context.Background(),
			weightProcRequest(&types.WeightSpec{Field: "weight", Kind: types.WeightKindFrequency}), NewSliceIterator(weightProcRecords()))
		if err != nil {
			t.Fatal(err)
		}
		a := resp.Components.Aggregations[0]
		if a.NEff != nil || a.SumWeights == nil {
			t.Errorf("frequency floor: n_eff %v sum_weights %v", a.NEff, a.SumWeights)
		}
		// 0.5 and 1.5 are fractional frequency weights: excluded too.
		if *a.NWeightInvalid != 5 {
			t.Errorf("n_weight_invalid = %d, want 5", *a.NWeightInvalid)
		}
		assertInvalidRowsWarning(t, resp, map[string]int64{"null": 1, "negative": 1, "nan_inf": 1, "non_integer_frequency": 2})
	})

	t.Run("strict", func(t *testing.T) {
		p := NewProcessor(weightProcSchema())
		p.SetWeighting(nil, true)
		_, err := p.Process(context.Background(), weightProcRequest(&types.WeightSpec{Field: "weight"}), NewSliceIterator(weightProcRecords()))
		if !errors.HasCode(err, errors.PULSE_WEIGHT_INVALID_ROWS) {
			t.Fatalf("strict: err = %v, want PULSE_WEIGHT_INVALID_ROWS", err)
		}
	})

	t.Run("default_weight", func(t *testing.T) {
		p := NewProcessor(weightProcSchema())
		p.SetWeighting(&types.WeightSpec{Field: "weight"}, false)
		resp, err := p.Process(context.Background(), weightProcRequest(nil), NewSliceIterator(weightProcRecords()))
		if err != nil {
			t.Fatal(err)
		}
		if resp.Components.Aggregations[0].SumWeights == nil {
			t.Fatal("Options.DefaultWeight did not reach the slot")
		}
	})
}

// TestWeightedProcess_UnweightedUnchanged: with no weight anywhere the
// response carries no weighted key and no warning — byte-identical to
// the pre-weighting wire form.
func TestWeightedProcess_UnweightedUnchanged(t *testing.T) {
	resp, err := NewProcessor(weightProcSchema()).Process(context.Background(), weightProcRequest(nil), NewSliceIterator(weightProcRecords()))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(resp)
	for _, k := range []string{"sum_weights", "n_eff", "n_weight_invalid", "PULSE_WEIGHT_INVALID_ROWS"} {
		if strings.Contains(string(b), k) {
			t.Errorf("unweighted response mentions %s: %s", k, b)
		}
	}
}

func assertInvalidRowsWarning(t *testing.T, resp *types.Response, byReason map[string]int64) {
	t.Helper()
	var hits []*types.ResponseWarning
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_WEIGHT_INVALID_ROWS) {
			hits = append(hits, w)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("PULSE_WEIGHT_INVALID_ROWS warnings = %d, want 1 (%+v)", len(hits), resp.Warnings)
	}
	d := hits[0].Details
	var total int64
	got := d["by_reason"].(map[string]any)
	for k, v := range byReason {
		if got[k] != v {
			t.Errorf("by_reason[%s] = %v, want %d", k, got[k], v)
		}
		total += v
	}
	if d["field"] != "weight" || d["count"] != total {
		t.Errorf("details = %v, want field weight count %d", d, total)
	}
}

// TestWeightedCore_StaysMergeable: a weight does not change any core
// aggregator's streamability or mergeability (CanStreamRequest /
// CanMergeRequest answer exactly as for the unweighted request).
func TestWeightedCore_StaysMergeable(t *testing.T) {
	for _, op := range wcoreSpecs {
		plain := &types.Request{Aggregations: []*types.Aggregation{{Type: op, Field: "value", Params: json.RawMessage(`{"weight_field":"weight"}`)}}}
		req := &types.Request{
			Weight:       &types.WeightSpec{Field: "weight"},
			Aggregations: []*types.Aggregation{{Type: op, Field: "value", Params: json.RawMessage(`{"weight_field":"weight"}`)}},
		}
		if op != types.AGG_WEIGHTED_MEAN {
			req.Aggregations[0].Params = nil
		}
		stamped := StampWeights(req, nil)
		if !CanStreamRequest(stamped, cohortSchema()) ||
			CanMergeRequest(stamped, cohortSchema()) != CanMergeRequest(plain, cohortSchema()) ||
			(op != types.AGG_WELFORD && !CanMergeRequest(stamped, cohortSchema())) {
			t.Errorf("%s weighted: mergeable=%v streamable=%v, unweighted mergeable=%v", op,
				CanMergeRequest(stamped, cohortSchema()), CanStreamRequest(stamped, cohortSchema()), CanMergeRequest(plain, cohortSchema()))
		}
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

// TestWeightedCore_ComponentsDeclared: a WEIGHTED run of each
// weight-aware aggregator emits exactly the operator keys its manifest
// schema declares (optional weighted keys included) and the typed
// weighted floor the schema declares as optional floor keys.
func TestWeightedCore_ComponentsDeclared(t *testing.T) {
	floor := map[string]bool{"n": true, "n_null": true, "sum_weights": true, "n_eff": true, "n_weight_invalid": true}
	m := descx.BuildManifest()
	for _, op := range wcoreSpecs {
		t.Run(string(op), func(t *testing.T) {
			schema := m.ComponentsSchemas.Aggregators[string(op)]
			var want []string
			declaredFloor := map[string]bool{}
			for _, k := range schema.Keys {
				if floor[k.Name] && (k.Optional || k.Name == "n" || k.Name == "n_null") {
					declaredFloor[k.Name] = true
					continue
				}
				want = append(want, k.Name)
			}
			for _, k := range []string{"sum_weights", "n_eff", "n_weight_invalid"} {
				if op != types.AGG_WEIGHTED_MEAN && !declaredFloor[k] {
					t.Errorf("schema does not declare optional floor key %s", k)
				}
			}
			req := &types.Request{Weight: &types.WeightSpec{Field: "weight"}, Aggregations: []*types.Aggregation{{Type: op, Field: "value", Label: "x"}}}
			resp, err := NewProcessor(cohortSchema()).processRecords(context.Background(), StampWeights(req, nil), wvRecords(wvOracleX, wvOracleW))
			if err != nil {
				t.Fatal(err)
			}
			entry := resp.Components.Aggregations[0]
			sort.Strings(want)
			if got := mapKeysSorted(entry.Operator); !reflect.DeepEqual(got, append([]string{}, want...)) && !(len(got) == 0 && len(want) == 0) {
				t.Errorf("weighted operator keys %v, declared %v", got, want)
			}
			if entry.SumWeights == nil || entry.NEff == nil || entry.NWeightInvalid == nil {
				t.Errorf("weighted floor missing: %+v", entry)
			}
		})
	}
}
