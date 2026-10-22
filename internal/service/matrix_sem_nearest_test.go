package service

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// matrix_sem_nearest_test.go is MAT_SEM's params.singular "nearest":
// a sample covariance that is not positive definite is fitted to
// statsmodels' cov_nearest substitute, as semopy fits it, with
// PULSE_MATRIX_NEAREST_PD. References: internal/sem/testdata
// singular_* (n < p, semopy's own substitution from the rows) and
// constant_* (a constant item, standardized: zeros, entered at unit
// variance), fit_semopy_nearest.py.

// semNearestFixture is a fit_semopy_nearest.py reference.
type semNearestFixture struct {
	semopyFixture
	N           float64     `json:"n"`
	P           int         `json:"p"`
	MinEig      float64     `json:"min_eigenvalue"`
	AdjustedCov [][]float64 `json:"adjusted_cov"`
	Observed    []string    `json:"observed"`
}

func loadNearestFixture(t *testing.T, name string) semNearestFixture {
	t.Helper()
	raw, err := os.ReadFile(semTestdata + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var ref semNearestFixture
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

// squareAt reads a square MatrixValues by row / column key.
func squareAt(t *testing.T, v *types.MatrixValues, r, c string) float64 {
	t.Helper()
	ri, ci := -1, -1
	for i, k := range v.RowKeys {
		if k == r {
			ri = i
		}
		if k == c {
			ci = i
		}
	}
	if ri < 0 || ci < 0 {
		t.Fatalf("no cell %s / %s in %v", r, c, v.RowKeys)
	}
	if ci < len(v.Values[ri]) {
		return v.Values[ri][ci]
	}
	return v.Values[ci][ri]
}

// TestMatrixSEM_SingularNearestMatchesSemopy: under singular "nearest"
// an n < p battery and a standardized battery with a constant item fit
// semopy 2.3.11 on the adjusted matrix (the default setup its default
// fit, the converging setup its tight fit), warn PULSE_MATRIX_NEAREST_PD
// with n, p and the correlation form's smallest eigenvalue, and report
// residual_covariance against the adjusted matrix.
func TestMatrixSEM_SingularNearestMatchesSemopy(t *testing.T) {
	for _, tc := range []struct {
		name string
		std  types.VectorStandardize
		also []string // the other warnings the battery raises
	}{
		{"singular", "", nil},
		{"constant", types.VectorStandardizeZScore, []string{string(errors.PULSE_MATRIX_ZERO_VARIANCE)}},
	} {
		desc, err := os.ReadFile(semTestdata + tc.name + "_model.txt")
		if err != nil {
			t.Fatal(err)
		}
		cfg, cols := semCohort(t, semTestdata+tc.name+"_data.csv", nil, nil)
		for _, su := range []struct {
			name, ref string
			params    map[string]any
		}{
			{"default", "default", nil},
			{"fisher_rank2", "tight", map[string]any{"solver": "fisher", "information": "rank2"}},
		} {
			t.Run(tc.name+"/"+su.name, func(t *testing.T) {
				ref := loadNearestFixture(t, tc.name+"_semopy_"+su.ref)
				params := map[string]any{"model": string(desc), "singular": "nearest"}
				for k, v := range su.params {
					params[k] = v
				}
				p, _ := json.Marshal(params)
				res := processMatrices(t, cfg, &types.Request{
					Cohort:   &types.Cohort{Filename: "sem.pulse"},
					Vectors:  []types.VectorSpec{{Name: "items", Fields: cols, Standardize: tc.std}},
					Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Vector: "items", Params: p}},
				}).Matrices[0]
				if res.Error != nil {
					t.Fatalf("error %+v", res.Error)
				}
				w := findWarning(res, errors.PULSE_MATRIX_NEAREST_PD)
				if w == nil {
					t.Fatalf("warnings %v, want PULSE_MATRIX_NEAREST_PD", matWarningCodes(res))
				}
				if w.Details["matrix"] != "sem" || w.Details["n"] != ref.N || w.Details["p"] != ref.P {
					t.Errorf("details %v, want n %v p %v", w.Details, ref.N, ref.P)
				}
				if me, _ := w.Details["min_eigenvalue"].(float64); math.Abs(me-ref.MinEig) > 1e-12 {
					t.Errorf("min_eigenvalue %v, semopy's input %v", w.Details["min_eigenvalue"], ref.MinEig)
				}
				var rest []*types.ResponseWarning
				for _, x := range res.Warnings {
					if x != w {
						rest = append(rest, x)
					}
				}
				if len(rest) != len(tc.also) || (len(rest) == 1 && rest[0].Code != tc.also[0]) {
					t.Errorf("other warnings %v, want %v", matWarningCodes(types.MatrixResult{Warnings: rest}), tc.also)
				}
				for i, a := range ref.Observed {
					for j, b := range ref.Observed {
						got := squareAt(t, res.Auxiliary["residual_covariance"], a, b) + squareAt(t, res.Primary, a, b)
						if d := semRel(got, ref.AdjustedCov[i][j]); d > 1e-9 {
							t.Errorf("S[%s][%s] = %.15g, semopy's adjusted %.15g", a, b, got, ref.AdjustedCov[i][j])
						}
					}
				}
				res.Warnings = nil
				checkSEMAgainstSemopy(t, res, ref.semopyFixture, 1e-6)
			})
		}
	}
}

// TestMatrixSEM_SingularDefaultRefuses: without params.singular the
// same batteries behave as before the knob — the n < p one is
// PULSE_MATRIX_SINGULAR, the standardized constant item nulls every
// figure — and an unknown value is bad_params.
func TestMatrixSEM_SingularDefaultRefuses(t *testing.T) {
	cfg, cols := semCohort(t, semTestdata+"singular_data.csv", nil, nil)
	for _, params := range []string{`{}`, `{"singular": "refuse"}`} {
		_, ce := semProcessErr(t, cfg, types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: cols, Params: json.RawMessage(params)})
		if ce == nil || ce.Code != errors.PULSE_MATRIX_SINGULAR {
			t.Errorf("%s: %v, want PULSE_MATRIX_SINGULAR", params, ce)
		}
	}
	_, ce := semProcessErr(t, cfg, types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: cols, Params: json.RawMessage(`{"singular": "ridge"}`)})
	if ce == nil || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" {
		t.Errorf("singular ridge: %v, want SERVICE_VALIDATION bad_params", ce)
	}

	cfg, cols = semCohort(t, semTestdata+"constant_data.csv", nil, nil)
	desc, err := os.ReadFile(semTestdata + "constant_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(map[string]any{"model": string(desc)})
	res := processMatrices(t, cfg, &types.Request{
		Cohort:   &types.Cohort{Filename: "sem.pulse"},
		Vectors:  []types.VectorSpec{{Name: "items", Fields: cols, Standardize: types.VectorStandardizeZScore}},
		Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Vector: "items", Params: p}},
	}).Matrices[0]
	if findWarning(res, errors.PULSE_MATRIX_NEAREST_PD) != nil || findWarning(res, errors.PULSE_MATRIX_ZERO_VARIANCE) == nil {
		t.Errorf("warnings %v, want PULSE_MATRIX_ZERO_VARIANCE only", matWarningCodes(res))
	}
	for k, v := range res.Scalars {
		if !math.IsNaN(v) {
			t.Errorf("scalars.%s = %v, want null", k, v)
		}
	}
}
