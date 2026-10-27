package regression

import (
	"math"
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
	if h.N != 3 || h.NNull != 2 || math.Abs(h.RMSE-math.Sqrt(14.0/3)) > 1e-12 || math.Abs(h.MAE-2) > 1e-12 ||
		math.Abs(h.R2-(1-14/sst)) > 1e-12 {
		t.Fatalf("figures = %+v", *h)
	}

	flat, err := HoldoutFigures(spec, columnScorer{}, []Record{holdoutRow(4, 3), holdoutRow(4, 5)}, 0)
	if err != nil || !math.IsNaN(flat.R2) || flat.RMSE != 1 || flat.MAE != 1 {
		t.Fatalf("constant targets: %+v, %v; want r2 NaN, rmse 1, mae 1", flat, err)
	}

	_, err = HoldoutFigures(spec, columnScorer{}, []Record{holdoutRow(5, math.NaN())}, 0)
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA || ce.Details["reason"] != model.HoldoutEmptyReason {
		t.Fatalf("empty: err = %v", err)
	}
}
