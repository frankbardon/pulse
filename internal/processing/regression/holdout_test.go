package regression

import (
	"math"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// columnScorer scores a row as its "yhat" column (null when absent).
type columnScorer struct{}

func (columnScorer) Columns() []string { return []string{"yhat"} }
func (columnScorer) Score(row model.Row) (float64, bool) {
	return row.NumericValue("yhat")
}

func holdoutRow(y, yhat float64) Record {
	return mockRecord{values: map[string]float64{"y": y, "yhat": yhat}}
}

// TestHoldoutFigures_Fold: rmse / mae / r2 fold over the scored rows; a
// NaN / ±Inf score or a null target is not scored; constant held-out
// targets leave r2 undefined (NaN); no scored row is holdout_empty.
func TestHoldoutFigures_Fold(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Holdout: "h"}
	rows := []Record{
		holdoutRow(18, 17), holdoutRow(17, 19), holdoutRow(24, 21),
		holdoutRow(5, math.NaN()), holdoutRow(5, math.Inf(1)),
		mockRecord{values: map[string]float64{"yhat": 1}, nulls: map[string]bool{"y": true}},
	}
	h, err := HoldoutFigures(spec, columnScorer{}, rows, 2)
	if err != nil {
		t.Fatal(err)
	}
	mean := 59.0 / 3
	sst := (18-mean)*(18-mean) + (17-mean)*(17-mean) + (24-mean)*(24-mean)
	if h.N != 3 || h.NNull != 2 || math.Abs(*h.RMSE-math.Sqrt(14.0/3)) > 1e-12 || math.Abs(*h.MAE-2) > 1e-12 ||
		math.Abs(*h.R2-(1-14/sst)) > 1e-12 {
		t.Fatalf("figures = %+v", *h)
	}

	flat, err := HoldoutFigures(spec, columnScorer{}, []Record{holdoutRow(4, 3), holdoutRow(4, 5)}, 0)
	if err != nil || !math.IsNaN(*flat.R2) || *flat.RMSE != 1 || *flat.MAE != 1 {
		t.Fatalf("constant targets: %+v, %v; want r2 NaN, rmse 1, mae 1", flat, err)
	}

	_, err = HoldoutFigures(spec, columnScorer{}, []Record{holdoutRow(5, math.NaN())}, 0)
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA || ce.Details["reason"] != model.HoldoutEmptyReason {
		t.Fatalf("empty: err = %v", err)
	}
}

// vectorScorer is a ProbabilityScorer over outcomes reading row
// columns p0..p(K−1) as the vector (null when one is absent).
type vectorScorer struct{ outcomes []string }

func (s vectorScorer) Columns() []string  { return nil }
func (s vectorScorer) Outcomes() []string { return s.outcomes }
func (s vectorScorer) Probabilities(row model.Row, out []float64) bool {
	for k := range out {
		v, ok := row.NumericValue("p" + strconv.Itoa(k))
		if !ok {
			return false
		}
		out[k] = v
	}
	return true
}

// outcomeRow is a held-out row: target label (code its position in
// codes, -1 for null), probability vector p and weight w.
func outcomeRow(label string, code float64, p []float64, w float64) Record {
	v := map[string]float64{"w": w}
	for k, pk := range p {
		v["p"+strconv.Itoa(k)] = pk
	}
	rec := labeledRecord{mockRecord: mockRecord{values: v, nulls: map[string]bool{}}, labels: map[string]string{}}
	if code < 0 {
		rec.nulls["y"] = true
	} else {
		v["y"] = code
		rec.labels["y"] = label
	}
	return rec
}

// TestHoldoutOutcomeFigures_Fold: the proper scores fold every scored
// held-out row's whole vector, weighted — log loss (natural log), the
// multi-class Brier, accuracy (the first outcome on a tie) and, ordered,
// the (K−1)-normalised ranked probability score — hand-computed; a null
// target, an outcome the fit does not carry, a null vector and a zero
// weight are not scored; an unordered fit carries no rps; RMSE / MAE /
// R2 stay absent.
func TestHoldoutOutcomeFigures_Fold(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_ORDINAL, Target: "y", Holdout: "h",
		Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})}
	ps := vectorScorer{outcomes: []string{"lo", "mid", "hi"}}
	rows := []Record{
		outcomeRow("lo", 0, []float64{0.7, 0.2, 0.1}, 1),
		outcomeRow("hi", 2, []float64{0.2, 0.5, 0.3}, 2),
		outcomeRow("mid", 1, []float64{0.4, 0.4, 0.2}, 1), // a tie: lo, not mid
		outcomeRow("top", 3, []float64{0.1, 0.1, 0.8}, 1), // not an outcome of the fit
		outcomeRow("", -1, []float64{0.1, 0.1, 0.8}, 1),   // null target
		outcomeRow("lo", 0, []float64{0.6, 0.3, 0.1}, 0),  // zero weight
		outcomeRow("lo", 0, []float64{0.6, 0.3}, 1),       // a null probability
	}
	h, err := HoldoutOutcomeFigures(spec, ps, true, rows, 3)
	if err != nil {
		t.Fatal(err)
	}
	sw := 4.0
	wantLog := (-math.Log(0.7) - 2*math.Log(0.3) - math.Log(0.4)) / sw
	wantBrier := (0.14 + 2*0.78 + 0.56) / sw
	wantRPS := (0.05 + 2*0.265 + 0.1) / sw
	near := func(what string, got *float64, want float64) {
		t.Helper()
		if got == nil || math.Abs(*got-want) > 1e-12 {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}
	if h.N != 3 || h.NNull != 3 || h.RMSE != nil || h.MAE != nil || h.R2 != nil {
		t.Fatalf("figures = %+v: want n 3, n_null 3, no rmse / mae / r2", *h)
	}
	near("log_loss", h.LogLoss, wantLog)
	near("brier", h.Brier, wantBrier)
	near("accuracy", h.Accuracy, 0.25)
	near("rps", h.RPS, wantRPS)

	un, err := HoldoutOutcomeFigures(spec, ps, false, rows, 0)
	if err != nil || un.RPS != nil || *un.Brier != *h.Brier {
		t.Fatalf("unordered: %+v, %v; want no rps, the same brier", un, err)
	}

	// A numeric target reads its value formatted as REG_ORDINAL's level.
	num := []Record{mockRecord{values: map[string]float64{"y": 2, "w": 1, "p0": 0.25, "p1": 0.75}}}
	nh, err := HoldoutOutcomeFigures(spec, vectorScorer{outcomes: []string{"1", "2"}}, true, num, 0)
	if err != nil || nh.N != 1 || *nh.Accuracy != 1 || math.Abs(*nh.LogLoss+math.Log(0.75)) > 1e-15 {
		t.Fatalf("numeric target: %+v, %v", nh, err)
	}

	// An observed outcome scored probability 0 has an infinite log loss.
	zero := []Record{outcomeRow("hi", 2, []float64{0.5, 0.5, 0}, 1)}
	zh, err := HoldoutOutcomeFigures(spec, ps, true, zero, 0)
	if err != nil || !math.IsInf(*zh.LogLoss, 1) || *zh.Accuracy != 0 {
		t.Fatalf("p_y = 0: %+v, %v; want log_loss +Inf, accuracy 0", zh, err)
	}

	_, err = HoldoutOutcomeFigures(spec, ps, true, rows[3:], 0)
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA || ce.Details["reason"] != model.HoldoutEmptyReason {
		t.Fatalf("empty: err = %v", err)
	}
}

// corruptRecord is a held-out row whose categorical target code its
// dictionary cannot decode.
type corruptRecord struct{ mockRecord }

func (corruptRecord) StringValueChecked(string) (string, bool, error) {
	return "", false, errors.NewCodedError(errors.ENCODING_INVALID, "corrupt code")
}

// TestHoldoutOutcomeFigures_CorruptLabel: a target code the dictionary
// cannot decode is the record's ENCODING_INVALID, returned (the slot
// fails alone), never a skipped row.
func TestHoldoutOutcomeFigures_CorruptLabel(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "y", Holdout: "h"}
	rec := corruptRecord{mockRecord{values: map[string]float64{"y": 9, "p0": 0.5, "p1": 0.5}}}
	_, err := HoldoutOutcomeFigures(spec, vectorScorer{outcomes: []string{"a", "b"}}, false, []Record{rec}, 0)
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.ENCODING_INVALID {
		t.Fatalf("err = %v, want ENCODING_INVALID", err)
	}
}
