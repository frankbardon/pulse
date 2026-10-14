package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Model-error aggregators (fit-scoring E2-S1): AGG_MAPE, AGG_RMSE,
// AGG_MAE over an actual (Field) and params.prediction_field.

func merrSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	if _, err := dict.Add("a"); err != nil {
		panic(err)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "pred", Type: encoding.FieldTypeF64},
		{Name: "w", Type: encoding.FieldTypeF64},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "day", Type: encoding.FieldTypeDate},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
	}}
}

// merrRow is one fixture row; a NaN y / pred is a null.
type merrRow struct{ y, pred, w float64 }

func merrRecords(rows []merrRow) []*Record {
	s := merrSchema()
	out := make([]*Record, len(rows))
	for i, r := range rows {
		vals := map[string]float64{"y": r.y, "pred": r.pred, "w": r.w}
		nulls := map[string]bool{}
		for k, v := range vals {
			if math.IsNaN(v) {
				nulls[k] = true
			}
		}
		out[i] = NewRecordWithWide(s, vals, nulls, nil)
	}
	return out
}

// merrOracleRows is the oracle fixture: two nulls (one per side) and one
// zero actual. Reference figures (R 4.x, base only):
//
//	y <- c(10,20,0,-8,40); p <- c(12,15,3,-6,50); w <- c(1,2,1,.5,1.5); e <- y-p
//	mean(abs(e)); sqrt(mean(e^2))                      # 4.4, 5.3291650377896911
//	weighted.mean(abs(e),w); sqrt(weighted.mean(e^2,w)) # 5.166666666666667, 5.9860949986893237
//	m <- y != 0; mean(abs(e[m])/abs(y[m]))              # 0.2375
//	weighted.mean(abs(e[m])/abs(y[m]), w[m])            # 0.24
//
// The two null rows are absent from the R vectors: a null on either side
// leaves the row out.
var merrOracleRows = []merrRow{
	{10, 12, 1},
	{20, 15, 2},
	{0, 3, 1},          // zero actual: excluded from MAPE only
	{5, math.NaN(), 3}, // null prediction
	{math.NaN(), 4, 1}, // null actual
	{-8, -6, 0.5},      // negative actual: |y| in the MAPE denominator
	{40, 50, 1.5},
}

func merrAgg(op types.AggregationType, weighted bool) *types.Aggregation {
	a := &types.Aggregation{Type: op, Field: "y", Label: "x", Params: json.RawMessage(`{"prediction_field":"pred"}`)}
	if weighted {
		a.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	}
	return a
}

func merrNew(t *testing.T, a *types.Aggregation) *modelErrorAggregator {
	t.Helper()
	agg, err := aggregatorRegistry[a.Type](a, merrSchema())
	if err != nil {
		t.Fatalf("%s factory: %v", a.Type, err)
	}
	return agg.(*modelErrorAggregator)
}

func approx(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Abs(a-b) <= 1e-12*math.Max(1, math.Abs(b))
}

// TestModelError_Oracle: unweighted and weighted figures equal the R
// reference, and the counters say which rows were left out.
func TestModelError_Oracle(t *testing.T) {
	cases := []struct {
		op       types.AggregationType
		weighted bool
		want     float64
		scored   int
		zero     int
	}{
		{types.AGG_MAE, false, 4.4, 5, 0},
		{types.AGG_RMSE, false, 5.3291650377896911, 5, 0},
		{types.AGG_MAPE, false, 0.2375, 4, 1},
		{types.AGG_MAE, true, 5.166666666666667, 5, 0},
		{types.AGG_RMSE, true, 5.9860949986893237, 5, 0},
		{types.AGG_MAPE, true, 0.24, 4, 1},
	}
	for _, tc := range cases {
		name := string(tc.op)
		if tc.weighted {
			name += "/weighted"
		}
		t.Run(name, func(t *testing.T) {
			a := merrNew(t, merrAgg(tc.op, tc.weighted))
			got, err := a.Aggregate(merrRecords(merrOracleRows), "y")
			if err != nil {
				t.Fatal(err)
			}
			if !approx(got, tc.want) {
				t.Errorf("%s = %.17g, want %.17g", tc.op, got, tc.want)
			}
			c, _ := a.Components()
			if c["n_scored"] != tc.scored {
				t.Errorf("n_scored = %v, want %d", c["n_scored"], tc.scored)
			}
			if c["n_null_prediction"] != 1 {
				t.Errorf("n_null_prediction = %v, want 1", c["n_null_prediction"])
			}
			if tc.op == types.AGG_MAPE {
				if c["n_zero_actual"] != tc.zero {
					t.Errorf("n_zero_actual = %v, want %d", c["n_zero_actual"], tc.zero)
				}
				if !approx(c["mape"].(float64), tc.want) {
					t.Errorf("components mape = %v, want %v", c["mape"], tc.want)
				}
			} else if _, ok := c["n_zero_actual"]; ok {
				t.Errorf("%s carries n_zero_actual", tc.op)
			}
			if tc.op == types.AGG_RMSE && !approx(c["mse"].(float64), tc.want*tc.want) {
				t.Errorf("components mse = %v, want %v", c["mse"], tc.want*tc.want)
			}
		})
	}
}

// TestModelError_UnityWeightBitIdentical: an all-ones weight answers the
// unweighted figure bit for bit.
func TestModelError_UnityWeightBitIdentical(t *testing.T) {
	rows := append([]merrRow(nil), merrOracleRows...)
	for i := range rows {
		rows[i].w = 1
	}
	for _, op := range []types.AggregationType{types.AGG_MAPE, types.AGG_RMSE, types.AGG_MAE} {
		plain, _ := merrNew(t, merrAgg(op, false)).Aggregate(merrRecords(rows), "y")
		weighted, _ := merrNew(t, merrAgg(op, true)).Aggregate(merrRecords(rows), "y")
		if math.Float64bits(plain) != math.Float64bits(weighted) {
			t.Errorf("%s: unity weight %.17g != unweighted %.17g", op, weighted, plain)
		}
	}
}

// TestModelError_InvalidWeightSkipsRow: a zero, negative or null weight
// leaves the row out entirely (its loss and its weight).
func TestModelError_InvalidWeightSkipsRow(t *testing.T) {
	clean := []merrRow{{10, 12, 1}, {20, 15, 2}}
	dirty := append(append([]merrRow(nil), clean...), merrRow{30, 0, 0}, merrRow{30, 0, -1}, merrRow{30, 0, math.NaN()})
	for _, op := range []types.AggregationType{types.AGG_MAPE, types.AGG_RMSE, types.AGG_MAE} {
		want, _ := merrNew(t, merrAgg(op, true)).Aggregate(merrRecords(clean), "y")
		got, _ := merrNew(t, merrAgg(op, true)).Aggregate(merrRecords(dirty), "y")
		if got != want {
			t.Errorf("%s: invalid weights moved the figure: %v != %v", op, got, want)
		}
	}
}

// TestModelError_EmptyIsNaN: no contributing row — and, for MAPE, only
// zero actuals — yields NaN, never 0.
func TestModelError_EmptyIsNaN(t *testing.T) {
	for _, op := range []types.AggregationType{types.AGG_MAPE, types.AGG_RMSE, types.AGG_MAE} {
		got, _ := merrNew(t, merrAgg(op, false)).Aggregate(merrRecords([]merrRow{{math.NaN(), 1, 1}, {2, math.NaN(), 1}}), "y")
		if !math.IsNaN(got) {
			t.Errorf("%s over no pair = %v, want NaN", op, got)
		}
	}
	got, _ := merrNew(t, merrAgg(types.AGG_MAPE, false)).Aggregate(merrRecords([]merrRow{{0, 1, 1}, {0, 2, 1}}), "y")
	if !math.IsNaN(got) {
		t.Errorf("AGG_MAPE over zero actuals only = %v, want NaN", got)
	}
}

// TestModelError_StreamingMatchesBuffered: UpdateRow + Finalize equals
// Aggregate bit for bit (same fold order), components included.
func TestModelError_StreamingMatchesBuffered(t *testing.T) {
	recs := merrRecords(merrOracleRows)
	for _, op := range []types.AggregationType{types.AGG_MAPE, types.AGG_RMSE, types.AGG_MAE} {
		for _, weighted := range []bool{false, true} {
			buf := merrNew(t, merrAgg(op, weighted))
			want, _ := buf.Aggregate(recs, "y")
			wantC, _ := buf.Components()
			str := merrNew(t, merrAgg(op, weighted))
			for _, r := range recs {
				if err := str.UpdateRow(r, "y"); err != nil {
					t.Fatal(err)
				}
			}
			got, _ := str.Finalize()
			gotC, _ := str.Components()
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("%s weighted=%v: streamed %v != buffered %v", op, weighted, got, want)
			}
			for k, v := range wantC {
				if gv := gotC[k]; gv != v {
					t.Errorf("%s weighted=%v: component %s streamed %v != buffered %v", op, weighted, k, gv, v)
				}
			}
		}
	}
}

// TestModelError_MergeEqualsSinglePass: the merged partial state of any
// split equals the single pass — counters exactly, figures to summation
// rounding — so sharded and parallel arms answer the serial figure.
func TestModelError_MergeEqualsSinglePass(t *testing.T) {
	recs := merrRecords(merrOracleRows)
	for _, op := range []types.AggregationType{types.AGG_MAPE, types.AGG_RMSE, types.AGG_MAE} {
		for _, weighted := range []bool{false, true} {
			single := merrNew(t, merrAgg(op, weighted))
			want, _ := single.Aggregate(recs, "y")
			wantC, _ := single.Components()
			for cut := 0; cut <= len(recs); cut++ {
				left, right := merrNew(t, merrAgg(op, weighted)), merrNew(t, merrAgg(op, weighted))
				for _, r := range recs[:cut] {
					_ = left.UpdateRow(r, "y")
				}
				for _, r := range recs[cut:] {
					_ = right.UpdateRow(r, "y")
				}
				if err := left.MergeOnline(right); err != nil {
					t.Fatal(err)
				}
				got, _ := left.Finalize()
				gotC, _ := left.Components()
				if !approx(got, want) {
					t.Errorf("%s weighted=%v cut=%d: merged %v != single %v", op, weighted, cut, got, want)
				}
				for _, k := range []string{"n_scored", "n_null_prediction", "n_zero_actual"} {
					if gotC[k] != wantC[k] {
						t.Errorf("%s cut=%d: %s merged %v != single %v", op, cut, k, gotC[k], wantC[k])
					}
				}
			}
		}
	}
}

// TestModelError_MergeRefusesOtherKind: an AGG_MAE partial never folds
// into an AGG_RMSE accumulator.
func TestModelError_MergeRefusesOtherKind(t *testing.T) {
	a := merrNew(t, merrAgg(types.AGG_RMSE, false))
	b := merrNew(t, merrAgg(types.AGG_MAE, false))
	if err := a.MergeOnline(b); err == nil {
		t.Fatal("merging AGG_MAE state into AGG_RMSE succeeded")
	}
}

// TestModelError_ConfigRefusals: a missing prediction_field and a
// non-numeric column on either side (the accepted-types table's
// PULSE_FIELD_TYPE_UNSUPPORTED) are refused; packed_bool
// (a 0/1 outcome) is admitted.
func TestModelError_ConfigRefusals(t *testing.T) {
	cases := []struct {
		name, field, params string
		ok                  bool
	}{
		{"missing prediction_field", "y", `{}`, false},
		{"no params", "y", ``, false},
		{"malformed params", "y", `{"prediction_field":3}`, false},
		{"categorical actual", "cat", `{"prediction_field":"pred"}`, false},
		{"date prediction", "y", `{"prediction_field":"day"}`, false},
		{"packed_bool actual", "flag", `{"prediction_field":"pred"}`, true},
		{"derived prediction column", "y", `{"prediction_field":"score_col"}`, true},
	}
	for _, tc := range cases {
		for _, op := range []types.AggregationType{types.AGG_MAPE, types.AGG_RMSE, types.AGG_MAE} {
			a := &types.Aggregation{Type: op, Field: tc.field}
			if tc.params != "" {
				a.Params = json.RawMessage(tc.params)
			}
			_, err := aggregatorRegistry[op](a, merrSchema())
			if tc.ok {
				if err != nil {
					t.Errorf("%s %s: unexpected refusal %v", op, tc.name, err)
				}
				continue
			}
			want := errors.PROCESSING_CONFIG
			if strings.HasPrefix(tc.name, "categorical") || strings.HasPrefix(tc.name, "date") {
				want = errors.PULSE_FIELD_TYPE_UNSUPPORTED // the accepted-types table's
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != want {
				t.Errorf("%s %s: err = %v, want %s", op, tc.name, err, want)
			}
		}
	}
}
