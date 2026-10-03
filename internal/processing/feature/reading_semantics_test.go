package feature

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// The tests in this file pin the behaviours the FEAT_* guidance
// (internal/descriptor/purposes_features.go, interpretations_features.go)
// and the op-feat-* / feature-engineering skills state as fact: the log
// shift, null handling of out-of-domain input, the power columns, the
// frequency denominator, and that target encoding reads no split column.
// A change here must change that prose with it.

// FEAT_LOG is ln(1 + x): 0 -> 0, values in (-1, 0) go negative, and
// x <= -1 reads null, not an error.
func TestReading_LogIsLog1pAndNullsAtMinusOne(t *testing.T) {
	recs := makeRecords([]float64{0, -0.5, -1, -3, 9}, nil, "x")
	if err := Apply(recs, []*types.Feature{{Type: types.FEAT_LOG, Field: "x", Label: "l"}}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got := func(i int) (float64, bool) { return recs[i].NumericValue("l") }
	if v, ok := got(0); !ok || v != 0 {
		t.Errorf("log(1+0) = %v, %v; want 0 (log1p, not ln)", v, ok)
	}
	if v, ok := got(1); !ok || math.Abs(v-math.Log(0.5)) > 1e-12 {
		t.Errorf("log(1-0.5) = %v, %v; want ln 0.5 < 0", v, ok)
	}
	for _, i := range []int{2, 3} {
		if _, ok := got(i); ok {
			t.Errorf("row %d (x <= -1) is not null", i)
		}
	}
	if v, _ := got(4); math.Abs(v-math.Log(10)) > 1e-12 {
		t.Errorf("log(1+9) = %v, want ln 10", v)
	}
}

// FEAT_SQRT nulls a negative input and maps 0 to 0; between 0 and 1 the
// root is larger than the value.
func TestReading_SqrtNegativeNullFractionGrows(t *testing.T) {
	recs := makeRecords([]float64{-4, 0, 0.25}, nil, "x")
	if err := Apply(recs, []*types.Feature{{Type: types.FEAT_SQRT, Field: "x", Label: "s"}}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := recs[0].NumericValue("s"); ok {
		t.Error("sqrt(-4) is not null")
	}
	if v, ok := recs[1].NumericValue("s"); !ok || v != 0 {
		t.Errorf("sqrt(0) = %v, %v; want 0", v, ok)
	}
	if v, _ := recs[2].NumericValue("s"); v != 0.5 {
		t.Errorf("sqrt(0.25) = %v, want 0.5", v)
	}
}

// FEAT_POLY emits only <prefix>_2 .. <prefix>_<degree> (no power-1
// column), with the default prefix <field>_poly; even powers lose the sign.
func TestReading_PolyColumnsAndSign(t *testing.T) {
	recs := makeRecords([]float64{-3}, nil, "x")
	feat := &types.Feature{Type: types.FEAT_POLY, Field: "x", Params: json.RawMessage(`{"degree":3}`)}
	if err := Apply(recs, []*types.Feature{feat}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	r := recs[0].(*fakeRecord)
	if _, ok := r.writes["x_poly_1"]; ok {
		t.Error("FEAT_POLY wrote a power-1 column")
	}
	if len(r.writes) != 2 || r.writes["x_poly_2"] != 9 || r.writes["x_poly_3"] != -27 {
		t.Errorf("writes = %v, want x_poly_2=9, x_poly_3=-27 only", r.writes)
	}
}

// FEAT_FREQUENCY_ENCODE divides by the records WITH a category: a null
// row is out of the denominator and reads null.
func TestReading_FrequencyDenominatorExcludesNulls(t *testing.T) {
	recs := mkCategorical("c", []string{"a", "a", "b", ""}, []bool{false, false, false, true})
	if err := Apply(recs, []*types.Feature{{Type: types.FEAT_FREQUENCY_ENCODE, Field: "c", Label: "f"}}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if v, _ := recs[0].NumericValue("f"); math.Abs(v-2.0/3) > 1e-12 {
		t.Errorf("share of a = %v, want 2/3 (null row not in the total)", v)
	}
	if _, ok := recs[3].NumericValue("f"); ok {
		t.Error("null category row is not null")
	}
}

// FEAT_TARGET_ENCODE reads no split column: with FEAT_TRAIN_TEST_SPLIT
// before it, every row — train or test — gets the average over ALL rows of
// its category, its own outcome included. A category whose outcomes are
// all null reads the overall average. This is the leakage the guidance
// warns about; ordering the split first only silences predict's warning.
func TestReading_TargetEncodeIgnoresSplitAndIncludesOwnRow(t *testing.T) {
	var recs []Record
	targets := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for i, tv := range targets {
		r := newFakeRecord()
		if i < 5 {
			r.str["c"] = "a"
		} else {
			r.str["c"] = "b"
		}
		r.num["y"] = tv
		recs = append(recs, r)
	}
	lone := newFakeRecord()
	lone.str["c"] = "z" // category with no outcome
	lone.nulls["y"] = true
	recs = append(recs, lone)

	feats := []*types.Feature{
		{Type: types.FEAT_TRAIN_TEST_SPLIT, Params: json.RawMessage(`{"ratios":[0.5,0.5],"seed":7}`)},
		{Type: types.FEAT_TARGET_ENCODE, Field: "c", Label: "te", Params: json.RawMessage(`{"target":"y"}`)},
	}
	if err := Apply(recs, feats, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sawTrain, sawHeldOut := false, false
	for i := 0; i < 10; i++ {
		want := 3.0 // mean(1..5): every "a" row, whichever split
		if i >= 5 {
			want = 8.0
		}
		if v, _ := recs[i].NumericValue("te"); math.Abs(v-want) > 1e-12 {
			t.Errorf("row %d te = %v, want %v (all rows of the category, split ignored)", i, v, want)
		}
		if s, _ := recs[i].NumericValue("split"); s == SplitTrain {
			sawTrain = true
		} else {
			sawHeldOut = true
		}
	}
	if !sawTrain || !sawHeldOut {
		t.Fatal("split did not produce both train and held-out rows; the test proves nothing")
	}
	if v, _ := recs[10].NumericValue("te"); math.Abs(v-5.5) > 1e-12 {
		t.Errorf("all-null-outcome category te = %v, want overall average 5.5", v)
	}

	// A category seen once encodes exactly its own outcome when unsmoothed.
	one := []Record{newFakeRecord(), newFakeRecord()}
	one[0].(*fakeRecord).str["c"], one[0].(*fakeRecord).num["y"] = "solo", 42
	one[1].(*fakeRecord).str["c"], one[1].(*fakeRecord).num["y"] = "other", 0
	if err := Apply(one, feats[1:], nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if v, _ := one[0].NumericValue("te"); v != 42 {
		t.Errorf("single-record category te = %v, want its own outcome 42", v)
	}
}

// FEAT_TRAIN_TEST_SPLIT: shares are round(n * ratio), and the same seed
// on the same rows reproduces the labels.
func TestReading_SplitSharesAndDeterminism(t *testing.T) {
	run := func() []float64 {
		recs := makeRecords(make([]float64, 10), nil, "x")
		feat := &types.Feature{Type: types.FEAT_TRAIN_TEST_SPLIT, Params: json.RawMessage(`{"ratios":[0.7,0.2,0.1],"seed":3}`)}
		if err := Apply(recs, []*types.Feature{feat}, nil); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		out := make([]float64, len(recs))
		for i, r := range recs {
			out[i], _ = r.NumericValue("split")
		}
		return out
	}
	a, b := run(), run()
	counts := map[float64]int{}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("row %d: split %v then %v with the same seed", i, a[i], b[i])
		}
		counts[a[i]]++
	}
	if counts[SplitTrain] != 7 || counts[SplitVal] != 2 || counts[SplitTest] != 1 {
		t.Errorf("counts = %v, want 7 train / 2 val / 1 test", counts)
	}
}
