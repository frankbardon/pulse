package pulse

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/regadapt"
	"github.com/frankbardon/pulse/types"
)

// extensions_regression_nonfinite_test.go pins transformed-predictors
// E1-S3 on the extension arm: the engine's admission of an extension
// regression's rows and its scoring of an extension RowScorer read a
// NaN / ±Inf input as missing, exactly as every built-in does
// (regression.InputValue).

// TestExtensions_RegressionNonFiniteInputDropsListwise: an extension
// fit regressing on a column with a NaN cell admits the same rows and
// reports the same n_obs as REG_OLS over the same slot — the NaN row
// dropped like a null — and its coefficients are finite.
func TestExtensions_RegressionNonFiniteInputDropsListwise(t *testing.T) {
	t.Parallel()

	rows := windowParityRows()
	rows[10][2] = math.NaN()   // east, week 11: tv is NaN
	rows[20][2] = math.Inf(1)  // east, week 21: tv is +Inf
	rows[30][5] = math.Inf(-1) // east, week 31: sales is -Inf
	p := windowParityPulseRows(t, Options{Extensions: Extensions{Regressions: []RegressionRegistration{centredRegistration(nil)}}}, rows)
	req := &Request{Cohort: &types.Cohort{Filename: "weekly.pulse"}, Regressions: []*types.RegressionSpec{
		{Type: regScoredName, Name: "ext", Target: "sales", Predictors: []string{"tv"}},
		{Type: types.REG_OLS, Name: "ols", Target: "sales", Predictors: []string{"tv"}},
	}}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	ext, ols := resp.Regressions[0], resp.Regressions[1]
	if ext.Error != nil || ols.Error != nil {
		t.Fatalf("slot failed: ext %+v ols %+v", ext.Error, ols.Error)
	}
	if want := 2*windowParityWeeks - 3; ols.NObs != want || ext.NObs != want {
		t.Fatalf("n_obs ext %d, REG_OLS %d, want %d (three non-finite rows dropped)", ext.NObs, ols.NObs, want)
	}
	for k, v := range ext.Coefficients {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("ext coefficient %s = %v: a non-finite row reached the model", k, v)
		}
	}
}

// nonFiniteScoreRow is a one-row model.Row / extend.ScoreRow.
type nonFiniteScoreRow map[string]float64

func (r nonFiniteScoreRow) NumericValue(name string) (float64, bool) {
	v, ok := r[name]
	return v, ok
}

// countingScorer counts the rows the extension was handed.
type countingScorer struct{ calls *int }

func (countingScorer) Columns() []string { return []string{"x"} }
func (s countingScorer) Score(row extend.ScoreRow) (float64, bool) {
	*s.calls++
	v, ok := row.NumericValue("x")
	return v, ok
}

// TestExtensions_RegressionScorerNonFiniteIsNull: a row whose declared
// column is NaN or ±Inf scores null without reaching the extension's
// scorer; a finite row still reaches it.
func TestExtensions_RegressionScorerNonFiniteIsNull(t *testing.T) {
	t.Parallel()

	calls := 0
	s := regadapt.NewRegressionRowScorer(countingScorer{calls: &calls}, []string{"x"})
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got, ok := s.Score(nonFiniteScoreRow{"x": v}); ok {
			t.Errorf("x=%v: score %v, want null", v, got)
		}
	}
	if calls != 0 {
		t.Errorf("extension scorer saw %d non-finite rows, want 0", calls)
	}
	if got, ok := s.Score(nonFiniteScoreRow{"x": 2}); !ok || got != 2 || calls != 1 {
		t.Errorf("finite row: score (%v, %v) after %d calls, want (2, true) after 1", got, ok, calls)
	}
}
