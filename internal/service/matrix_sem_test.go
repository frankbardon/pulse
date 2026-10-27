package service

import (
	"context"
	"encoding/csv"
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_sem_test.go is MAT_SEM end to end through the service: the
// semopy reference fits of internal/sem/testdata (the default setup
// reproduces semopy's DEFAULT fit, a converging one its tight fit), a
// frequency weight equal to the expanded rows, the probability-weight
// refusal, the singular refusal, an undefined input and the warnings.

const semTestdata = "../sem/testdata/"

// semCohort writes a CSV's numeric columns (plus, when weights is set,
// a frequency weight column "w") as a .pulse on a hermetic filesystem.
// repeat > 1 writes every row that many times (the expansion of a
// frequency weight).
func semCohort(t *testing.T, csvPath string, weights func(r int) float64, repeat func(r int) int) (*fs.Config, []string) {
	t.Helper()
	schema, rows, cols := semRows(t, csvPath, weights, repeat)
	data := writeNullablePulse(t, schema, rows, func(int, int) bool { return false })
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "sem.pulse", data, 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg, cols
}

// semRows is semCohort's schema and records (and the numeric columns),
// unwritten.
func semRows(t *testing.T, csvPath string, weights func(r int) float64, repeat func(r int) int) (*encoding.Schema, [][]uint64, []string) {
	t.Helper()
	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var cols []string
	var idx []int
	for i, h := range recs[0] {
		if _, err := strconv.ParseFloat(recs[1][i], 64); err == nil {
			cols, idx = append(cols, h), append(idx, i)
		}
	}
	names := append([]string(nil), cols...)
	if weights != nil {
		names = append(names, "w")
	}
	fields := make([]encoding.Field, len(names))
	for i, n := range names {
		fields[i] = encoding.Field{Name: n, Type: encoding.FieldTypeF64, ByteOffset: 8 * i, CsvColumnIdx: i}
	}
	var rows [][]uint64
	for r, rec := range recs[1:] {
		row := make([]uint64, len(names))
		for k, i := range idx {
			v, err := strconv.ParseFloat(rec[i], 64)
			if err != nil {
				t.Fatalf("row %d %s: %v", r, cols[k], err)
			}
			row[k] = math.Float64bits(v)
		}
		if weights != nil {
			row[len(cols)] = math.Float64bits(weights(r))
		}
		times := 1
		if repeat != nil {
			times = repeat(r)
		}
		for range times {
			rows = append(rows, row)
		}
	}
	return &encoding.Schema{Fields: fields}, rows, cols
}

type semopyFixture struct {
	Fun    float64            `json:"fun"`
	Stats  map[string]float64 `json:"stats"`
	Params []struct {
		LVal string   `json:"lval"`
		Op   string   `json:"op"`
		RVal string   `json:"rval"`
		Est  float64  `json:"est"`
		Std  *float64 `json:"std"`
		SE   *float64 `json:"se"`
		Z    *float64 `json:"z"`
		P    *float64 `json:"p"`
	} `json:"params"`
}

var semStatKeys = map[string]string{
	"DoF": "dof", "DoF Baseline": "dof_baseline", "chi2": "chi2", "chi2 p-value": "chi2_p_value",
	"chi2 Baseline": "chi2_baseline", "CFI": "cfi", "GFI": "gfi", "AGFI": "agfi", "NFI": "nfi",
	"TLI": "tli", "RMSEA": "rmsea", "AIC": "aic", "BIC": "bic", "LogLik": "loglik",
}

func semRel(a, b float64) float64 { return math.Abs(a-b) / math.Max(1, math.Abs(b)) }

func checkSEMAgainstSemopy(t *testing.T, res types.MatrixResult, ref semopyFixture, tol float64) {
	t.Helper()
	if res.Scalars["converged"] != 1 || len(res.Warnings) != 0 {
		t.Fatalf("converged %v, warnings %v", res.Scalars["converged"], matWarningCodes(res))
	}
	est := res.Auxiliary["estimates"]
	if est == nil || est.Kind != types.MatrixKindRectangular {
		t.Fatalf("estimates = %+v", est)
	}
	got := map[string][]float64{}
	for i, k := range est.RowKeys {
		got[k] = est.Values[i]
	}
	if len(got) != len(ref.Params) {
		t.Errorf("%d estimate rows, semopy has %d", len(got), len(ref.Params))
	}
	for _, r := range ref.Params {
		k := r.LVal + " " + r.Op + " " + r.RVal
		row, ok := got[k]
		if !ok && r.Op == "~~" {
			row, ok = got[r.RVal+" ~~ "+r.LVal] // semopy orients exogenous-latent pairs by Python set order
		}
		if !ok {
			t.Errorf("missing row %q", k)
			continue
		}
		near := func(what string, g, w, tol float64) {
			if d := semRel(g, w); !(d <= tol) {
				t.Errorf("%s %s = %.12g, semopy %.12g (rel %.1e)", k, what, g, w, d)
			}
		}
		near("estimate", row[0], r.Est, tol)
		if r.Std != nil {
			near("std_estimate", row[1], *r.Std, tol)
		}
		if r.SE == nil {
			if !math.IsNaN(row[2]) {
				t.Errorf("%s: fixed in semopy, carries an SE", k)
			}
			continue
		}
		near("std_err", row[2], *r.SE, tol)
		near("z_value", row[3], *r.Z, 2*tol)
		if d := math.Abs(row[4] - *r.P); !(d <= tol) {
			t.Errorf("%s p = %g, semopy %g", k, row[4], *r.P)
		}
	}
	for sk, pk := range semStatKeys {
		if d := semRel(res.Scalars[pk], ref.Stats[sk]); !(d <= tol) {
			t.Errorf("%s = %.12g, semopy %.12g", pk, res.Scalars[pk], ref.Stats[sk])
		}
	}
	if d := semRel(res.Scalars["objective"], ref.Fun); !(d <= tol) {
		t.Errorf("objective %.14g, semopy %.14g", res.Scalars["objective"], ref.Fun)
	}
}

// TestMatrixSEM_MatchesSemopy runs the four reference models through
// the engine — a .pulse cohort, Process, the merged co-moment, the
// finalizer — and pins the default setup to semopy's default fit
// (early stop included) and the converging setup to its tight fit.
func TestMatrixSEM_MatchesSemopy(t *testing.T) {
	t.Parallel()

	models := []struct{ name, data string }{{"hier", "hier"}, {"poldem", "poldem"}, {"hz", "hz"}, {"hz_ext", "hz"}}
	setups := []struct {
		name, ref string
		params    map[string]any
		tol       float64
	}{
		{"default", "default", nil, 1e-9},
		{"fisher_rank2", "tight", map[string]any{"solver": "fisher", "information": "rank2"}, 1e-6},
	}
	for _, mdl := range models {
		desc, err := os.ReadFile(semTestdata + mdl.name + "_model.txt")
		if err != nil {
			t.Fatal(err)
		}
		cfg, cols := semCohort(t, semTestdata+mdl.data+"_data.csv", nil, nil)
		for _, su := range setups {
			t.Run(mdl.name+"/"+su.name, func(t *testing.T) {
				var ref semopyFixture
				raw, err := os.ReadFile(semTestdata + mdl.name + "_semopy_" + su.ref + ".json")
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &ref); err != nil {
					t.Fatal(err)
				}
				params := map[string]any{"model": string(desc)}
				for k, v := range su.params {
					params[k] = v
				}
				p, _ := json.Marshal(params)
				fields := semModelMembers(t, cols, string(desc))
				resp := processMatrices(t, cfg, &types.Request{
					Cohort:   &types.Cohort{Filename: "sem.pulse"},
					Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: fields, Params: p}},
				})
				res := resp.Matrices[0]
				checkSEMAgainstSemopy(t, res, ref, su.tol)
				if res.Primary.Kind != types.MatrixKindSquareSymmetric || len(res.Primary.RowKeys) != len(fields) {
					t.Errorf("primary %+v", res.Primary.RowKeys)
				}
				if c := resp.Components.Matrices[0]; float64(c.N) != res.Scalars["n"] {
					t.Errorf("components n %d, scalars.n %v", c.N, res.Scalars["n"])
				}
			})
		}
	}
}

// semModelMembers keeps the cohort columns the model names (the
// Holzinger–Swineford file carries more), in file order.
func semModelMembers(t *testing.T, cols []string, desc string) []string {
	t.Helper()
	used := map[string]bool{}
	tok := ""
	for _, r := range desc + "\n" {
		if r == '_' || r == '.' || ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') {
			tok += string(r)
			continue
		}
		used[tok], tok = true, ""
	}
	var out []string
	for _, c := range cols {
		if used[c] {
			out = append(out, c)
		}
	}
	return out
}

// TestMatrixSEM_FrequencyWeightIsExpansion: a frequency weight is
// exactly the fit on the expanded rows — N = Σw, S the weighted
// population covariance — so every figure matches the expansion's. The
// two covariances differ in their last bits (different sums), which
// the fit's flat directions amplify at the solver's stop: 1e-6, the
// converged-fit tolerance.
func TestMatrixSEM_FrequencyWeightIsExpansion(t *testing.T) {
	t.Parallel()

	desc, _ := os.ReadFile(semTestdata + "hz_model.txt")
	w := func(r int) float64 { return float64(1 + r%3) }
	weighted, cols := semCohort(t, semTestdata+"hz_data.csv", w, nil)
	expanded, _ := semCohort(t, semTestdata+"hz_data.csv", nil, func(r int) int { return 1 + r%3 })
	fields := semModelMembers(t, cols, string(desc))
	params, _ := json.Marshal(map[string]any{"model": string(desc), "solver": "fisher"})
	spec := types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: fields, Params: params}
	plain := processMatrices(t, expanded, &types.Request{Cohort: &types.Cohort{Filename: "sem.pulse"}, Matrices: []types.MatrixSpec{spec}}).Matrices[0]
	spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	wres := processMatrices(t, weighted, &types.Request{Cohort: &types.Cohort{Filename: "sem.pulse"}, Matrices: []types.MatrixSpec{spec}}).Matrices[0]
	if wres.Scalars["n"] != plain.Scalars["n"] || plain.Scalars["n"] != 600 {
		t.Fatalf("n weighted %v, expanded %v (want 600)", wres.Scalars["n"], plain.Scalars["n"])
	}
	for k := range semStatKeys {
		pk := semStatKeys[k]
		if d := semRel(wres.Scalars[pk], plain.Scalars[pk]); !(d <= 1e-6) {
			t.Errorf("%s weighted %.12g, expanded %.12g", pk, wres.Scalars[pk], plain.Scalars[pk])
		}
	}
	for i, row := range plain.Auxiliary["estimates"].Values {
		for c, v := range row {
			g := wres.Auxiliary["estimates"].Values[i][c]
			if math.IsNaN(v) != math.IsNaN(g) || (!math.IsNaN(v) && semRel(g, v) > 1e-6) {
				t.Errorf("estimates[%s][%d] weighted %.12g, expanded %.12g", plain.Auxiliary["estimates"].RowKeys[i], c, g, v)
			}
		}
	}
}

func semProcessErr(t *testing.T, cfg *fs.Config, spec types.MatrixSpec) (*types.Response, *errors.CodedError) {
	t.Helper()
	resp, err := processOrSlotErr(context.Background(), New(cfg), &types.Request{Cohort: &types.Cohort{Filename: "sem.pulse"}, Matrices: []types.MatrixSpec{spec}})
	var ce *errors.CodedError
	if err != nil && !stderrors.As(err, &ce) {
		t.Fatalf("uncoded error %v", err)
	}
	return resp, ce
}

// TestMatrixSEM_Refusals: a probability weight (no design-based form)
// and pairwise deletion are refused with their codes.
func TestMatrixSEM_Refusals(t *testing.T) {
	t.Parallel()

	cfg, _ := semCohort(t, semTestdata+"hz_data.csv", func(r int) float64 { return 1 + 0.5*float64(r%2) }, nil)
	model, _ := json.Marshal(map[string]any{"model": "f =~ x1 + x2 + x3"})
	base := types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: []string{"x1", "x2", "x3"}, Params: model}

	prob := base
	prob.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	if _, ce := semProcessErr(t, cfg, prob); ce == nil || ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED {
		t.Errorf("probability weight: %v, want PULSE_WEIGHT_UNSUPPORTED", ce)
	}
	pair := base
	pair.Params = json.RawMessage(`{"missing": "pairwise"}`)
	if _, ce := semProcessErr(t, cfg, pair); ce == nil || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "pairwise_unsupported" {
		t.Errorf("pairwise: %v, want SERVICE_VALIDATION pairwise_unsupported", ce)
	}

}

// semRowsCohort writes three f64 columns a, b, c.
func semRowsCohort(t *testing.T, rows [][3]float64) *fs.Config {
	t.Helper()
	fields := []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
		{Name: "c", Type: encoding.FieldTypeF64, ByteOffset: 16, CsvColumnIdx: 2},
	}
	recs := make([][]uint64, len(rows))
	for i, r := range rows {
		recs[i] = []uint64{math.Float64bits(r[0]), math.Float64bits(r[1]), math.Float64bits(r[2])}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "sem.pulse", writeNullablePulse(t, &encoding.Schema{Fields: fields}, recs, func(int, int) bool { return false }), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestMatrixSEM_SingularRefused: under params.singular "refuse" a
// member that is an exact linear combination of others makes S
// singular — refused with PULSE_MATRIX_SINGULAR naming the dependent
// fields.
func TestMatrixSEM_SingularRefused(t *testing.T) {
	t.Parallel()

	var rows [][3]float64
	for i := range 50 {
		a, b := float64(i%7)+0.3*float64(i%5), float64(i%11)
		rows = append(rows, [3]float64{a, b, a + b})
	}
	cfg := semRowsCohort(t, rows)
	_, ce := semProcessErr(t, cfg, types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: []string{"a", "b", "c"}, Params: json.RawMessage(`{"singular": "refuse"}`)})
	if ce == nil || ce.Code != errors.PULSE_MATRIX_SINGULAR {
		t.Fatalf("err = %v, want PULSE_MATRIX_SINGULAR", ce)
	}
	if ce.Details["matrix"] != "sem" || ce.Details["dependent_fields"] == nil {
		t.Errorf("details = %v", ce.Details)
	}
}

// TestMatrixSEM_UndefinedInputNulls: one row defines no covariance —
// every figure null, the estimate rows still listed, the co-moment
// warning saying why.
func TestMatrixSEM_UndefinedInputNulls(t *testing.T) {
	t.Parallel()

	cfg := semRowsCohort(t, [][3]float64{{1, 2, 3}})
	resp := processMatrices(t, cfg, &types.Request{
		Cohort: &types.Cohort{Filename: "sem.pulse"},
		Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: []string{"a", "b", "c"},
			Params: json.RawMessage(`{"model": "ind =~ a + b + c"}`)}},
	})
	res := resp.Matrices[0]
	if findWarning(res, errors.PULSE_MATRIX_INSUFFICIENT_N) == nil {
		t.Fatalf("warnings %v, want PULSE_MATRIX_INSUFFICIENT_N", matWarningCodes(res))
	}
	if est := res.Auxiliary["estimates"]; est == nil || len(est.RowKeys) != 7 {
		t.Fatalf("estimates = %+v", est)
	}
	for _, row := range res.Auxiliary["estimates"].Values {
		for _, v := range row {
			if !math.IsNaN(v) {
				t.Fatalf("estimate %v on an undefined input", row)
			}
		}
	}
	for k, v := range res.Scalars {
		if !math.IsNaN(v) {
			t.Errorf("scalars.%s = %v, want null", k, v)
		}
	}
}

// TestMatrixSEM_Warnings: a model with more parameters than moments is
// fitted (as semopy fits it) with PULSE_MATRIX_NOT_IDENTIFIED; an
// iteration cap keeps the last iterate with PULSE_MATRIX_NOT_CONVERGED.
func TestMatrixSEM_Warnings(t *testing.T) {
	t.Parallel()

	cfg, _ := semCohort(t, semTestdata+"hz_data.csv", nil, nil)
	run := func(params string) types.MatrixResult {
		t.Helper()
		return processMatrices(t, cfg, &types.Request{
			Cohort:   &types.Cohort{Filename: "sem.pulse"},
			Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: []string{"x1", "x2", "x3"}, Params: json.RawMessage(params)}},
		}).Matrices[0]
	}
	res := run(`{"model": "f =~ x1 + x2 + x3\nx1 ~~ x2\nx2 ~~ x3"}`)
	if w := findWarning(res, errors.PULSE_MATRIX_NOT_IDENTIFIED); w == nil || res.Scalars["dof"] != -2 {
		t.Errorf("dof %v, warnings %v: want -2 with PULSE_MATRIX_NOT_IDENTIFIED", res.Scalars["dof"], matWarningCodes(res))
	}
	res = run(`{"model": "f =~ x1 + x2 + x3", "max_iter": 1}`)
	w := findWarning(res, errors.PULSE_MATRIX_NOT_CONVERGED)
	if w == nil || res.Scalars["converged"] != 0 || w.Details["solver"] != "sem_slsqp" {
		t.Errorf("converged %v, warning %+v: want PULSE_MATRIX_NOT_CONVERGED from sem_slsqp", res.Scalars["converged"], w)
	}
	if ok := findWarning(run(`{"model": "f =~ x1 + x2 + x3"}`), errors.PULSE_MATRIX_NOT_CONVERGED); ok != nil {
		t.Errorf("a converged fit warns %+v", ok)
	}
}
