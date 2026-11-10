package orderedcat

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func testSchema() *encoding.Schema {
	d := encoding.NewDictionary()
	for _, l := range []string{"mid", "low", "high", "na"} {
		d.Add(l)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "sat", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
		{Name: "score", Type: encoding.FieldTypeF64},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Scale: 2},
		{Name: "emb", Type: encoding.FieldTypeVecF32},
	}}
}

func TestRequested(t *testing.T) {
	t.Parallel()

	cases := []struct {
		op   types.AggregationType
		raw  string
		want bool
	}{
		{types.AGG_MEDIAN, ``, false},
		{types.AGG_MEDIAN, `{}`, false},
		{types.AGG_MEDIAN, `{"ordered":false}`, false},
		{types.AGG_MEDIAN, `{"ordered":null,"levels":null}`, false},
		{types.AGG_MEDIAN, `{"ordered":true}`, true},
		{types.AGG_MIN, `{"levels":["a"]}`, true},
		{types.AGG_MAX, `{"levels":[]}`, true},
		{types.AGG_PERCENTILE, `{"percentile":5,"ordered":"yes"}`, true},
		{types.AGG_MEDIAN, `[1]`, false},
		{types.AGG_AVERAGE, `{"ordered":true}`, false},
		{types.AGG_MODE, `{"levels":["a"]}`, false},
	}
	for _, c := range cases {
		if got := Requested(c.op, json.RawMessage(c.raw)); got != c.want {
			t.Errorf("Requested(%s, %s) = %v, want %v", c.op, c.raw, got, c.want)
		}
	}
}

func TestResolve_Specs(t *testing.T) {
	t.Parallel()

	s, cerr := Resolve(types.AGG_MEDIAN, "sat", json.RawMessage(`{"ordered":true}`), testSchema())
	if cerr != nil {
		t.Fatal(cerr)
	}
	if !reflect.DeepEqual(s.Rank, []int{0, 1, 2, 3}) || !reflect.DeepEqual(s.Code, []int{0, 1, 2, 3}) {
		t.Fatalf("dictionary order: rank %v code %v", s.Rank, s.Code)
	}
	s, cerr = Resolve(types.AGG_MEDIAN, "sat", json.RawMessage(`{"levels":["low","mid","high"]}`), testSchema())
	if cerr != nil {
		t.Fatal(cerr)
	}
	if !reflect.DeepEqual(s.Rank, []int{1, 0, 2, -1}) || !reflect.DeepEqual(s.Code, []int{1, 0, 2}) {
		t.Fatalf("levels order: rank %v code %v", s.Rank, s.Code)
	}
	if r, ok := s.RankOf(3); ok || r != -1 {
		t.Fatalf("na listed: %d %v", r, ok)
	}
	// Not requested, not applicable, no schema, a vector field: no spec,
	// no refusal (the caller's other rules own the slot).
	for _, c := range []struct {
		op     types.AggregationType
		field  string
		raw    string
		schema *encoding.Schema
	}{
		{types.AGG_MEDIAN, "sat", `{"ordered":false}`, testSchema()},
		{types.AGG_SUM, "sat", `{"ordered":true}`, testSchema()},
		{types.AGG_MEDIAN, "sat", `{"ordered":true}`, nil},
		{types.AGG_MEDIAN, "emb", `{"ordered":true}`, testSchema()},
	} {
		if s, cerr := Resolve(c.op, c.field, json.RawMessage(c.raw), c.schema); s != nil || cerr != nil {
			t.Errorf("%s %s %s: %v %v, want nil nil", c.op, c.field, c.raw, s, cerr)
		}
	}
}

func TestResolve_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		op     types.AggregationType
		field  string
		raw    string
		code   errors.Code
		reason string
	}{
		{types.AGG_MEDIAN, "score", `{"ordered":true}`, errors.PROCESSING_CONFIG, ReasonNeedsCategorical},
		{types.AGG_MIN, "derived_col", `{"ordered":true}`, errors.PROCESSING_CONFIG, ReasonNeedsCategorical},
		{types.AGG_MIN, "amt", `{"ordered":true}`, errors.PROCESSING_CONFIG, ReasonNeedsCategorical},
		{types.AGG_MEDIAN, "amt", `{"ordered":true}`, errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL, ""},
		{types.AGG_MAX, "sat", `{"levels":["low","nope"]}`, errors.PROCESSING_CONFIG, ReasonUnknownLevel},
		{types.AGG_MAX, "sat", `{"levels":["low","high","low"]}`, errors.PROCESSING_CONFIG, ReasonDuplicateLevel},
		{types.AGG_PERCENTILE, "sat", `{"levels":[]}`, errors.PROCESSING_CONFIG, ReasonEmptyLevels},
		{types.AGG_MEDIAN, "sat", `{"ordered":false,"levels":["low"]}`, errors.PROCESSING_CONFIG, ReasonOrderedFalse},
		{types.AGG_MEDIAN, "sat", `{"levels":"low"}`, errors.PROCESSING_CONFIG, ReasonInvalidParams},
	}
	for _, c := range cases {
		_, cerr := Resolve(c.op, c.field, json.RawMessage(c.raw), testSchema())
		if cerr == nil || cerr.Code != c.code {
			t.Errorf("%s %s %s: %v, want %s", c.op, c.field, c.raw, cerr, c.code)
			continue
		}
		if c.reason != "" && cerr.Details["reason"] != c.reason {
			t.Errorf("%s %s %s: reason %v, want %s", c.op, c.field, c.raw, cerr.Details["reason"], c.reason)
		}
	}
}

// TestQuantileRank pins R quantile(type = 1) over per-rank counts:
//
//	quantile(c(1,1,2,3,3,3), c(0, .1, .3, .34, .5, .51, 1), type = 1)
//	# 1 1 1 2 2 3 3
//
// and the weighted reading (the lowest rank whose cumulative Σw reaches
// p·Σw), the zero-total ranks skipped and an empty total answering -1.
func TestQuantileRank(t *testing.T) {
	t.Parallel()

	counts := []float64{2, 1, 3}
	ps := []float64{0, 0.1, 0.3, 0.34, 0.5, 0.51, 1}
	want := []int{0, 0, 0, 1, 1, 2, 2}
	for i, p := range ps {
		if got := QuantileRank(counts, p, false); got != want[i] {
			t.Errorf("p=%v: rank %d, want %d", p, got, want[i])
		}
	}
	// p = 0 is the lowest OBSERVED rank.
	if got := QuantileRank([]float64{0, 0, 4, 1}, 0, false); got != 2 {
		t.Errorf("p=0 over leading empty ranks: %d, want 2", got)
	}
	if got := QuantileRank([]float64{0, 0}, 0.5, true); got != -1 {
		t.Errorf("empty: %d, want -1", got)
	}
	// Weighted: 0.1 + 0.2 sums to 0.30000000000000004 against a target
	// of 0.3: the snap keeps the mathematically exact share reaching it,
	// and a share genuinely short of it does not.
	if got := QuantileRank([]float64{0.1, 0.2, 0.7}, 0.3, true); got != 1 {
		t.Errorf("snapped share: %d, want 1", got)
	}
	if got := QuantileRank([]float64{0.1, 0.19, 0.71}, 0.3, true); got != 2 {
		t.Errorf("short share: %d, want 2", got)
	}
	// R's n·p fuzz, both sides of it:
	//   quantile(c(rep(1, 7), rep(2, 3)), .7, type = 1)   # 1 (0.7·10 = 7)
	//   quantile(c(rep(1, 7), rep(2, 18)), .28, type = 1) # 2 (0.28·25 = 7 + 8.9e-16)
	//   quantile(c(rep(1, 29), rep(2, 21)), .58, type = 1) # 1 (0.58·50 = 29 − 3.6e-15)
	if got := QuantileRank([]float64{7, 3}, 0.7, false); got != 0 {
		t.Errorf("0.7 of 10: %d, want 0", got)
	}
	if got := QuantileRank([]float64{7, 18}, 0.28, false); got != 1 {
		t.Errorf("0.28 of 25: %d, want 1 (R)", got)
	}
	if got := QuantileRank([]float64{29, 21}, 0.58, false); got != 0 {
		t.Errorf("0.58 of 50: %d, want 0 (R)", got)
	}
}
