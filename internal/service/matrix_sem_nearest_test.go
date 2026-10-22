package service

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// matrix_sem_nearest_test.go is MAT_SEM's params.singular, default
// "nearest" (semopy's default fit): a sample covariance whose Cholesky
// fails is fitted to statsmodels' cov_nearest substitute, as semopy
// fits it, with PULSE_MATRIX_NEAREST_PD; one that factors, however
// near singular, is fitted raw; a zero-spread member (no substitute —
// semopy raises) fails the slot. "refuse" refuses every rank-deficient
// covariance. References: internal/sem/testdata singular_* (n < p,
// semopy's own substitution from the rows, fit_semopy_nearest.py),
// near_* (a near-exact dependency that factors, fit_semopy_exact.py)
// and constant_data.csv (a constant item).

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

// TestMatrixSEM_SingularNearestMatchesSemopy: by default (and under an
// explicit "nearest") an n < p battery fits semopy 2.3.11 on the
// adjusted matrix (the default setup its default fit, the converging
// setup its tight fit), warns PULSE_MATRIX_NEAREST_PD with n, p and the
// correlation form's smallest eigenvalue, and reports
// residual_covariance against the adjusted matrix.
func TestMatrixSEM_SingularNearestMatchesSemopy(t *testing.T) {
	desc, err := os.ReadFile(semTestdata + "singular_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	cfg, cols := semCohort(t, semTestdata+"singular_data.csv", nil, nil)
	for _, su := range []struct {
		name, ref string
		params    map[string]any
	}{
		{"default", "default", nil},
		{"fisher_rank2", "tight", map[string]any{"solver": "fisher", "information": "rank2", "singular": "nearest"}},
	} {
		t.Run(su.name, func(t *testing.T) {
			ref := loadNearestFixture(t, "singular_semopy_"+su.ref)
			params := map[string]any{"model": string(desc)}
			for k, v := range su.params {
				params[k] = v
			}
			p, _ := json.Marshal(params)
			res := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: "sem.pulse"},
				Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: cols, Params: p}},
			}).Matrices[0]
			if res.Error != nil {
				t.Fatalf("error %+v", res.Error)
			}
			w := findWarning(res, errors.PULSE_MATRIX_NEAREST_PD)
			if w == nil || len(res.Warnings) != 1 {
				t.Fatalf("warnings %v, want PULSE_MATRIX_NEAREST_PD only", matWarningCodes(res))
			}
			if w.Details["matrix"] != "sem" || w.Details["n"] != ref.N || w.Details["p"] != ref.P {
				t.Errorf("details %v, want n %v p %v", w.Details, ref.N, ref.P)
			}
			if me, _ := w.Details["min_eigenvalue"].(float64); math.Abs(me-ref.MinEig) > 1e-12 {
				t.Errorf("min_eigenvalue %v, semopy's input %v", w.Details["min_eigenvalue"], ref.MinEig)
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

// TestMatrixSEM_NearSingularFactorsFitsRaw: a near-exact dependency
// that the one singular rule calls singular (explicit "refuse" is
// PULSE_MATRIX_SINGULAR) but whose Cholesky factors is fitted raw by
// default, as semopy fits it — no substitution, no warning, every
// figure against the data's own S. near_data.csv's rows make that S
// exact (and its Cholesky exact) on every platform; the default
// early stop is pinned at 1e-4 (internal/sem
// TestFit_NearSingularRawMatchesSemopy says why), the converging setup
// to semopy's tight fit at 1e-6.
func TestMatrixSEM_NearSingularFactorsFitsRaw(t *testing.T) {
	desc, err := os.ReadFile(semTestdata + "near_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	cfg, cols := semCohort(t, semTestdata+"near_data.csv", nil, nil)
	refuse, _ := json.Marshal(map[string]any{"model": string(desc), "singular": "refuse"})
	if _, ce := semProcessErr(t, cfg, types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: cols, Params: refuse}); ce == nil || ce.Code != errors.PULSE_MATRIX_SINGULAR {
		t.Fatalf("refuse: %v, want PULSE_MATRIX_SINGULAR (the rule must call this S singular)", ce)
	}
	for _, su := range []struct {
		name, ref string
		params    map[string]any
		tol       float64
	}{
		{"default", "default", nil, 1e-4},
		{"fisher_rank2", "tight", map[string]any{"solver": "fisher", "information": "rank2"}, 1e-6},
	} {
		t.Run(su.name, func(t *testing.T) {
			raw, err := os.ReadFile(semTestdata + "near_semopy_" + su.ref + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var ref struct {
				semopyFixture
				Sample   [][]float64 `json:"sample_cov"`
				Observed []string    `json:"observed"`
			}
			if err := json.Unmarshal(raw, &ref); err != nil {
				t.Fatal(err)
			}
			params := map[string]any{"model": string(desc)}
			for k, v := range su.params {
				params[k] = v
			}
			p, _ := json.Marshal(params)
			res := processMatrices(t, cfg, &types.Request{
				Cohort:   &types.Cohort{Filename: "sem.pulse"},
				Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: cols, Params: p}},
			}).Matrices[0]
			if res.Error != nil {
				t.Fatalf("error %+v", res.Error)
			}
			for i, a := range ref.Observed {
				for j, b := range ref.Observed {
					got := squareAt(t, res.Auxiliary["residual_covariance"], a, b) + squareAt(t, res.Primary, a, b)
					if d := semRel(got, ref.Sample[i][j]); d > 1e-9 {
						t.Errorf("S[%s][%s] = %.15g, the data's %.15g", a, b, got, ref.Sample[i][j])
					}
				}
			}
			checkSEMAgainstSemopy(t, res, ref.semopyFixture, su.tol)
		})
	}
}

// TestMatrixSEM_SingularRefuse: params.singular "refuse" keeps the
// pre-default behaviour — the n < p battery is PULSE_MATRIX_SINGULAR,
// a standardized constant item nulls every figure — and an unknown
// value is bad_params. By default a constant item, raw or
// standardized, has no nearest substitute (semopy raises LinAlgError):
// the slot fails with PULSE_MATRIX_SINGULAR and PULSE_SLOT_FAILED.
func TestMatrixSEM_SingularRefuse(t *testing.T) {
	cfg, cols := semCohort(t, semTestdata+"singular_data.csv", nil, nil)
	_, ce := semProcessErr(t, cfg, types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: cols, Params: json.RawMessage(`{"singular": "refuse"}`)})
	if ce == nil || ce.Code != errors.PULSE_MATRIX_SINGULAR {
		t.Errorf("refuse: %v, want PULSE_MATRIX_SINGULAR", ce)
	}
	_, ce = semProcessErr(t, cfg, types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: cols, Params: json.RawMessage(`{"singular": "ridge"}`)})
	if ce == nil || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" {
		t.Errorf("singular ridge: %v, want SERVICE_VALIDATION bad_params", ce)
	}

	cfg, cols = semCohort(t, semTestdata+"constant_data.csv", nil, nil)
	desc, err := os.ReadFile(semTestdata + "constant_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	run := func(singular string, std types.VectorStandardize) *types.Response {
		params := map[string]any{"model": string(desc)}
		if singular != "" {
			params["singular"] = singular
		}
		p, _ := json.Marshal(params)
		return processMatrices(t, cfg, &types.Request{
			Cohort:   &types.Cohort{Filename: "sem.pulse"},
			Vectors:  []types.VectorSpec{{Name: "items", Fields: cols, Standardize: std}},
			Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Vector: "items", Params: p}},
		})
	}
	res := run("refuse", types.VectorStandardizeZScore).Matrices[0]
	if res.Error != nil || findWarning(res, errors.PULSE_MATRIX_NEAREST_PD) != nil || findWarning(res, errors.PULSE_MATRIX_ZERO_VARIANCE) == nil {
		t.Errorf("refuse, standardized: error %v, warnings %v, want PULSE_MATRIX_ZERO_VARIANCE only", res.Error, matWarningCodes(res))
	}
	for k, v := range res.Scalars {
		if !math.IsNaN(v) {
			t.Errorf("refuse, standardized: scalars.%s = %v, want null", k, v)
		}
	}
	for _, std := range []types.VectorStandardize{"", types.VectorStandardizeZScore} {
		for _, singular := range []string{"", "nearest"} {
			resp := run(singular, std)
			res := resp.Matrices[0]
			if res.Error == nil || res.Error.Code != string(errors.PULSE_MATRIX_SINGULAR) {
				t.Errorf("singular %q, standardize %q: error %+v, want PULSE_MATRIX_SINGULAR", singular, std, res.Error)
			}
			failed := false
			for _, w := range resp.Warnings {
				failed = failed || w.Code == string(errors.PULSE_SLOT_FAILED)
			}
			if !failed {
				t.Errorf("singular %q, standardize %q: no PULSE_SLOT_FAILED warning", singular, std)
			}
		}
	}
}
