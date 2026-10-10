package service

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// matrix_collinearity_test.go is MAT_COLLINEARITY end to end: the R
// oracle (car::vif on lm, perturb::colldiag uncentered with the
// intercept and centered — unweighted and both weight kinds), the
// singular refusal, the PSD guard and the params refusals.

// collinearityOracle is one mv_collinearity.json case.
type collinearityOracle struct {
	Fixture    string        `json:"fixture"`
	Response   string        `json:"response"`
	Predictors []string      `json:"predictors"`
	Weight     string        `json:"weight"`
	NRows      int           `json:"n_rows"`
	VIF        []float64     `json:"vif"`
	Tolerance  []float64     `json:"tolerance"`
	Uncentered belsleyOracle `json:"uncentered"`
	Centered   belsleyOracle `json:"centered"`
}

// belsleyOracle is one perturb::colldiag result: condition indices
// ascending, variance_decomposition[k][j] (rows the dimensions, columns
// the variables in Columns order).
type belsleyOracle struct {
	Columns    []string    `json:"columns"`
	Condition  []float64   `json:"condition_indices"`
	Proportion [][]float64 `json:"variance_decomposition"`
}

// collinearityOracleTol pins every figure against R, absolute +
// relative. VIF comes from the FMA-free reference InverseSPD (R: QR in
// lm); Belsley's figures from the gonum-backed eigensolver on the
// scaled cross-product (R: the SVD of the scaled X), so the smallest
// eigenvalues carry the squared conditioning — 1e-9 holds with room on
// these fixtures (largest uncentered condition index ≈ 130).
const collinearityOracleTol = 1e-9

func collinearitySpec(name string, fields []string, params map[string]any, weight string) types.MatrixSpec {
	raw, _ := json.Marshal(params)
	s := types.MatrixSpec{Name: name, Type: types.MAT_COLLINEARITY, Fields: fields, Params: raw}
	switch weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability})
	}
	return s
}

func loadCollinearityOracle(t *testing.T) ([]collinearityOracle, string) {
	t.Helper()
	var golden struct {
		Cases []collinearityOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_collinearity.json", &golden)
	if len(golden.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	return golden.Cases, dir
}

func collNear(t *testing.T, where string, got, want float64) {
	t.Helper()
	if !(math.Abs(got-want) <= collinearityOracleTol*(1+math.Abs(want))) {
		t.Errorf("%s = %.17g, R = %.17g", where, got, want)
	}
}

// TestMatrixCollinearity_MatchesROracle pins every mv_collinearity.json
// case — attitude, mtcars and the weighted fixture; unweighted,
// frequency (the rep() expansion) and probability weights — on both
// Belsley variants: VIF and tolerance (car::vif over lm on the
// predictors; the response never enters), the condition indices and
// the variance-decomposition proportions (perturb::colldiag, transposed
// to a row per variable), and condition_number the largest index.
func TestMatrixCollinearity_MatchesROracle(t *testing.T) {
	cases, dir := loadCollinearityOracle(t)
	cfg := fixtureFS(t, dir)
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/%s", c.Fixture, c.Weight), func(t *testing.T) {
			resp := processMatrices(t, cfg, &types.Request{
				Cohort: &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
				Matrices: []types.MatrixSpec{
					collinearitySpec("uncentered", c.Predictors, nil, c.Weight),
					collinearitySpec("centered", c.Predictors, map[string]any{"center": true}, c.Weight),
				},
			})
			for v, want := range map[int]belsleyOracle{0: c.Uncentered, 1: c.Centered} {
				res := resp.Matrices[v]
				if len(res.Warnings) != 0 {
					t.Errorf("%s warnings %v", res.Name, matWarningCodes(res))
				}
				if res.Primary.Kind != types.MatrixKindSquareSymmetric || !reflect.DeepEqual(res.Primary.RowKeys, c.Predictors) {
					t.Fatalf("%s primary %+v", res.Name, res.Primary)
				}
				for key, wantVec := range map[string][]float64{"vif": c.VIF, "tolerance": c.Tolerance, "condition_indices": want.Condition} {
					got := relFloats(t, res.Vectors[key])
					if len(got) != len(wantVec) {
						t.Fatalf("%s %s length %d, want %d", res.Name, key, len(got), len(wantVec))
					}
					for i := range wantVec {
						collNear(t, fmt.Sprintf("%s %s[%d]", res.Name, key, i), got[i], wantVec[i])
					}
				}
				q := len(want.Columns)
				collNear(t, res.Name+" condition_number", res.Scalars["condition_number"], want.Condition[q-1])
				maxVIF := 0.0
				for _, v := range c.VIF {
					maxVIF = math.Max(maxVIF, v)
				}
				collNear(t, res.Name+" max_vif", res.Scalars["max_vif"], maxVIF)
				vd := res.Auxiliary["variance_decomposition"]
				rows := append([]string(nil), c.Predictors...)
				if v == 0 {
					rows = append([]string{"(intercept)"}, rows...)
				}
				cols := make([]string, q)
				for k := range cols {
					cols[k] = fmt.Sprintf("D%d", k+1)
				}
				if vd == nil || vd.Kind != types.MatrixKindRectangular || vd.Encoding != types.MatrixEncodingFull ||
					!reflect.DeepEqual(vd.RowKeys, rows) || !reflect.DeepEqual(vd.ColumnKeys, cols) || len(vd.Values) != q {
					t.Fatalf("%s variance_decomposition shape %+v", res.Name, vd)
				}
				for j := 0; j < q; j++ {
					for k := 0; k < q; k++ {
						collNear(t, fmt.Sprintf("%s pi[%s][D%d]", res.Name, rows[j], k+1), vd.Values[j][k], want.Proportion[k][j])
					}
				}
			}
		})
	}
}

// TestMatrixCollinearity_FrequencyIsExpansion: a frequency weight is the
// rep() expansion — the weighted run equals the unweighted run over
// rows repeated w times (VIF, both Belsley variants).
func TestMatrixCollinearity_FrequencyIsExpansion(t *testing.T) {
	var weighted, expanded [][5]float64
	for i := 0; i < 30; i++ {
		a, b := float64(i%7)+0.25*float64(i%4), float64((i*5)%11)-0.5*float64(i%3)
		row := [5]float64{a, b, 0.7*a - 0.3*b + float64(i%5), float64(1 + i%3), 1}
		weighted = append(weighted, row)
		for k := 0; k < int(row[3]); k++ {
			expanded = append(expanded, [5]float64{row[0], row[1], row[2], 1, 1})
		}
	}
	run := func(rows [][5]float64, weight string) *types.Response {
		cfg := writeMatrixCohort(t, "c.pulse", rows)
		specs := []types.MatrixSpec{
			{Name: "u", Type: types.MAT_COLLINEARITY, Fields: []string{"x1", "x2", "x3"}},
			{Name: "c", Type: types.MAT_COLLINEARITY, Fields: []string{"x1", "x2", "x3"}, Params: json.RawMessage(`{"center": true}`)},
		}
		if weight != "" {
			for i := range specs {
				specs[i].Weight = types.SlotWeightOf(types.WeightSpec{Field: weight, Kind: types.WeightKindFrequency})
			}
		}
		return processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"}, Matrices: specs})
	}
	w, e := run(weighted, "f"), run(expanded, "")
	for m := range w.Matrices {
		for _, key := range []string{"vif", "condition_indices"} {
			got, want := relFloats(t, w.Matrices[m].Vectors[key]), relFloats(t, e.Matrices[m].Vectors[key])
			for i := range want {
				if math.Abs(got[i]-want[i]) > 1e-10*(1+math.Abs(want[i])) {
					t.Errorf("%s %s[%d] weighted %v, expanded %v", w.Matrices[m].Name, key, i, got[i], want[i])
				}
			}
		}
	}
}

// TestMatrixCollinearity_SingularRefused: a member that is the sum of
// two others has no R⁻¹, so the matrix is refused with
// PULSE_MATRIX_SINGULAR naming the dependency.
func TestMatrixCollinearity_SingularRefused(t *testing.T) {
	rows := make([][5]float64, 40)
	for i := range rows {
		a, b := float64(i%7)+0.3*float64(i%3), float64((i*5)%11)
		rows[i] = [5]float64{a, b, a + b, 1, 1}
	}
	cfg := writeMatrixCohort(t, "sing.pulse", rows)
	_, err := New(cfg).Process(t.Context(), &types.Request{
		Cohort:   &types.Cohort{Filename: "sing.pulse"},
		Matrices: []types.MatrixSpec{{Name: "coll", Type: types.MAT_COLLINEARITY, Fields: []string{"x1", "x2", "x3"}}},
	})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_SINGULAR {
		t.Fatalf("error %v, want PULSE_MATRIX_SINGULAR", err)
	}
	if !reflect.DeepEqual(ce.Details["dependent_fields"], []string{"x1", "x2", "x3"}) || ce.Details["matrix"] != "coll" {
		t.Errorf("details %+v, want matrix coll and dependent_fields x1, x2, x3", ce.Details)
	}
	for _, key := range []string{"rank", "condition_number"} {
		if _, ok := ce.Details[key]; !ok {
			t.Errorf("details lack %s: %+v", key, ce.Details)
		}
	}
}

// TestMatrixCollinearity_UndefinedNulls: a zero-spread member leaves R
// undefined, so every figure is null (the co-moment warning says why)
// and nothing is refused.
func TestMatrixCollinearity_UndefinedNulls(t *testing.T) {
	rows := make([][5]float64, 20)
	for i := range rows {
		rows[i] = [5]float64{float64(i % 5), float64((i * 3) % 7), 4, 1, 1}
	}
	cfg := writeMatrixCohort(t, "flat.pulse", rows)
	resp := processMatrices(t, cfg, &types.Request{
		Cohort:   &types.Cohort{Filename: "flat.pulse"},
		Matrices: []types.MatrixSpec{{Name: "coll", Type: types.MAT_COLLINEARITY, Fields: []string{"x1", "x2", "x3"}}},
	})
	res := resp.Matrices[0]
	if findWarning(res, errors.PULSE_MATRIX_ZERO_VARIANCE) == nil {
		t.Errorf("warnings %v, want PULSE_MATRIX_ZERO_VARIANCE", matWarningCodes(res))
	}
	for key, n := range map[string]int{"vif": 3, "tolerance": 3, "condition_indices": 4} {
		got := relFloats(t, res.Vectors[key])
		if len(got) != n {
			t.Fatalf("%s length %d, want %d", key, len(got), n)
		}
		for i, v := range got {
			if !math.IsNaN(v) {
				t.Errorf("%s[%d] = %v, want null", key, i, v)
			}
		}
	}
	if !math.IsNaN(res.Scalars["condition_number"]) || !math.IsNaN(res.Scalars["max_vif"]) || len(res.Auxiliary["variance_decomposition"].Values) != 4 {
		t.Errorf("condition_number %v, variance_decomposition %+v", res.Scalars["condition_number"], res.Auxiliary["variance_decomposition"])
	}
}

// TestMatrixCollinearity_NotPSDGuard: a non-PSD pairwise correlation is
// refused with the fatal PULSE_MATRIX_NOT_PSD; params.repair "nearest"
// reads VIF and Belsley's figures off the repaired R (the primary), with
// the repair warning.
func TestMatrixCollinearity_NotPSDGuard(t *testing.T) {
	_, dir := loadCollinearityOracle(t)
	cfg := fixtureFS(t, dir)
	req := func(params string) *types.Request {
		return &types.Request{
			Cohort:   &types.Cohort{Filename: filepath.Join(dir, "nonpsd_pairwise.pulse")},
			Matrices: []types.MatrixSpec{{Name: "coll", Type: types.MAT_COLLINEARITY, Fields: []string{"x", "y", "z"}, Params: json.RawMessage(params)}},
		}
	}
	_, err := New(cfg).Process(t.Context(), req(`{"missing": "pairwise"}`))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATRIX_NOT_PSD {
		t.Fatalf("unrepaired: error %v, want PULSE_MATRIX_NOT_PSD", err)
	}
	for _, params := range []string{`{"missing": "pairwise", "repair": "nearest"}`, `{"missing": "pairwise", "repair": "nearest", "center": true}`} {
		res := processMatrices(t, cfg, req(params)).Matrices[0]
		if findWarning(res, errors.PULSE_MATRIX_NOT_PSD) == nil {
			t.Fatalf("%s: warnings %v, want PULSE_MATRIX_NOT_PSD", params, matWarningCodes(res))
		}
		for _, key := range []string{"vif", "condition_indices"} {
			for i, v := range relFloats(t, res.Vectors[key]) {
				if !(v >= 1) {
					t.Errorf("%s: %s[%d] = %v, want defined ≥ 1", params, key, i, v)
				}
			}
		}
		if res.Auxiliary["n"] == nil {
			t.Errorf("%s: pairwise auxiliary.n missing", params)
		}
	}
}

// TestMatrixCollinearity_Refusals: the params rules — every refusal
// SERVICE_VALIDATION bad_params (there is no response key: the members
// are the predictors).
func TestMatrixCollinearity_Refusals(t *testing.T) {
	cases, dir := loadCollinearityOracle(t)
	cfg := fixtureFS(t, dir)
	for _, params := range []string{
		`{"center": "yes"}`,
		`{"center": 1}`,
		`{"repair": "clip"}`,
		`{"response": "rating"}`,
		`{"on": "covariance"}`,
		`{"components": 2}`,
	} {
		_, err := New(cfg).Process(t.Context(), &types.Request{
			Cohort:   &types.Cohort{Filename: filepath.Join(dir, "attitude.pulse")},
			Matrices: []types.MatrixSpec{{Name: "coll", Type: types.MAT_COLLINEARITY, Fields: cases[0].Predictors, Params: json.RawMessage(params)}},
		})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" {
			t.Errorf("%s: error %v, want SERVICE_VALIDATION bad_params", params, err)
		}
	}
}
