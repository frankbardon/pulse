package service

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// lavaanFixture is a testdata/*_lavaan.json reference (internal/sem's
// fit_lavaan.R): lavaan's ML fit with meanstructure, expected
// information, fixed.x.
type lavaanFixture struct {
	Lavaan    string   `json:"lavaan"`
	N         float64  `json:"n"`
	Observed  []string `json:"observed"`
	Converged bool     `json:"converged"`
	Params    []struct {
		LHS     string   `json:"lhs"`
		Op      string   `json:"op"`
		RHS     string   `json:"rhs"`
		Free    bool     `json:"free"`
		Est     *float64 `json:"est"`
		SE      *float64 `json:"se"`
		Z       *float64 `json:"z"`
		P       *float64 `json:"p"`
		CILower *float64 `json:"ci_lower"`
		CIUpper *float64 `json:"ci_upper"`
		StdAll  *float64 `json:"std_all"`
	} `json:"params"`
	Fit        map[string]*float64 `json:"fit"`
	ImpliedMu  []float64           `json:"implied_mean"`
	SampleMu   []float64           `json:"sample_mean"`
	ImpliedCov [][]float64         `json:"implied_cov"`
}

// lavaanScalarKeys maps MAT_SEM's scalars to lavaan's fitMeasures
// under lavaan conventions (gfi / agfi are lavaan's LISREL pair).
var lavaanScalarKeys = map[string]string{
	"chi2": "chisq", "dof": "df", "chi2_p_value": "pvalue", "chi2_baseline": "baseline.chisq",
	"dof_baseline": "baseline.df", "cfi": "cfi", "tli": "tli", "nfi": "nfi", "rmsea": "rmsea",
	"aic": "aic", "bic": "bic", "loglik": "logl", "gfi": "gfi_lisrel", "agfi": "agfi_lisrel", "n_params": "npar",
}

func lavRel(a, b float64) float64 { return math.Abs(a-b) / math.Max(math.Abs(b), 1e-8) }

// TestMatrixSEM_MeanStructureMatchesLavaan runs the brand-survey
// fixture through the engine — a .pulse cohort, Process, the merged
// co-moment's means and covariance, the finalizer — with a mean
// structure (params.meanstructure, explicit ~ 1 lines, both) and holds
// every figure to lavaan's meanstructure = TRUE fit: ~1 rows, the Wald
// interval columns, implied_means / observed_means, the fit indices.
// The resolved plan (the rule predict sizes from) names exactly the
// auxiliaries and the estimates shape the run emits.
func TestMatrixSEM_MeanStructureMatchesLavaan(t *testing.T) {
	t.Parallel()

	cfg, cols := semCohort(t, semTestdata+"brand_data.csv", nil, nil)
	for _, c := range []struct {
		name, ref, model string
		params           map[string]any
	}{
		{"meanstructure", "brand_ms", "brand_ms", map[string]any{"meanstructure": true}},
		{"intercept_lines", "brand_mx", "brand_mx", nil},
		{"intercept_lines_and_param", "brand_mx", "brand_mx", map[string]any{"meanstructure": true}},
		{"rank2", "brand_ms", "brand_ms", map[string]any{"meanstructure": true, "information": "rank2"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			desc, err := os.ReadFile(semTestdata + c.model + "_model.txt")
			if err != nil {
				t.Fatal(err)
			}
			var ref lavaanFixture
			raw, err := os.ReadFile(semTestdata + c.ref + "_lavaan.json")
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &ref); err != nil {
				t.Fatal(err)
			}
			params := map[string]any{"model": string(desc)}
			for k, v := range c.params {
				params[k] = v
			}
			p, _ := json.Marshal(params)
			fields := semModelMembers(t, cols, string(desc))
			req := &types.Request{
				Cohort:   &types.Cohort{Filename: "sem.pulse"},
				Matrices: []types.MatrixSpec{{Name: "sem", Type: types.MAT_SEM, Fields: fields, Params: p}},
			}
			resp := processMatrices(t, cfg, req)
			res := resp.Matrices[0]
			if res.Scalars["converged"] != 1 || len(res.Warnings) != 0 {
				t.Fatalf("converged %v, warnings %v", res.Scalars["converged"], matWarningCodes(res))
			}
			est := res.Auxiliary["estimates"]
			wantCols := []string{"estimate", "std_estimate", "std_err", "z_value", "p_value", "ci_lower", "ci_upper"}
			if !reflect.DeepEqual(est.ColumnKeys, wantCols) {
				t.Fatalf("estimate columns %v, want %v", est.ColumnKeys, wantCols)
			}
			rows := map[string][]float64{}
			for i, k := range est.RowKeys {
				rows[k] = est.Values[i]
			}
			intercepts := 0
			for _, r := range ref.Params {
				key := r.LHS + " " + r.Op + " " + r.RHS
				switch r.Op {
				case "=~":
					key = r.RHS + " ~ " + r.LHS
				case "~1":
					key = r.LHS + " ~1"
				}
				row, ok := rows[key]
				if !ok && r.Op == "~~" {
					row, ok = rows[r.RHS+" ~~ "+r.LHS]
				}
				if !ok {
					if !r.Free && (r.LHS == "wave") {
						continue // fixed.x sample moments are hidden
					}
					t.Errorf("lavaan row %q missing", key)
					continue
				}
				if r.Op == "~1" {
					intercepts++
				}
				near := func(what string, g float64, w *float64, tol float64) {
					if w == nil || !(lavRel(g, *w) <= tol) {
						t.Errorf("%s %s = %.10g, lavaan %v", key, what, g, w)
					}
				}
				near("estimate", row[0], r.Est, 1e-4)
				near("std_estimate", row[1], r.StdAll, 1e-4)
				if !r.Free {
					for _, v := range row[2:] {
						if !math.IsNaN(v) {
							t.Errorf("%s: a fixed row carries SE / z / p / interval figures %v", key, row)
							break
						}
					}
					continue
				}
				near("std_err", row[2], r.SE, 1e-4)
				near("z_value", row[3], r.Z, 1e-4)
				if r.P == nil || !(math.Abs(row[4]-*r.P) <= 1e-4) {
					t.Errorf("%s p = %g, lavaan %v", key, row[4], r.P)
				}
				near("ci_lower", row[5], r.CILower, 1e-4)
				near("ci_upper", row[6], r.CIUpper, 1e-4)
			}
			if intercepts == 0 {
				t.Fatal("no ~1 row matched: the fixture is vacuous")
			}
			for k, lk := range lavaanScalarKeys {
				if w := ref.Fit[lk]; w == nil || !(lavRel(res.Scalars[k], *w) <= 1e-3) {
					t.Errorf("scalar %s = %.10g, lavaan %s %v", k, res.Scalars[k], lk, w)
				}
			}
			if res.Scalars["n"] != ref.N {
				t.Errorf("n = %v, lavaan %v", res.Scalars["n"], ref.N)
			}
			pos := map[string]int{}
			for i, v := range ref.Observed {
				pos[v] = i
			}
			for _, name := range []string{"implied_means", "observed_means"} {
				mv := res.Auxiliary[name]
				if mv == nil || mv.Kind != types.MatrixKindRectangular || !reflect.DeepEqual(mv.RowKeys, fields) || !reflect.DeepEqual(mv.ColumnKeys, []string{"mean"}) {
					t.Fatalf("auxiliary.%s = %+v", name, mv)
				}
				want := ref.ImpliedMu
				if name == "observed_means" {
					want = ref.SampleMu
				}
				for r, f := range fields {
					if d := lavRel(mv.Values[r][0], want[pos[f]]); !(d <= 1e-4) {
						t.Errorf("%s %s = %.10g, lavaan %.10g", name, f, mv.Values[r][0], want[pos[f]])
					}
				}
			}
			for r, a := range fields {
				for cc, b := range fields {
					if d := lavRel(res.Primary.Values[r][cc], ref.ImpliedCov[pos[a]][pos[b]]); !(d <= 1e-4) {
						t.Errorf("implied cov %s,%s = %g, lavaan %g", a, b, res.Primary.Values[r][cc], ref.ImpliedCov[pos[a]][pos[b]])
					}
				}
			}
			// Predict ≡ runtime: the resolved plan names the run's
			// auxiliaries and its estimates shape.
			schema := mustReadSchema(t, cfg.Fs(), "sem.pulse")
			plans, cerr := vectors.ResolveMatrices(req, schema, nil)
			if cerr != nil {
				t.Fatal(cerr)
			}
			keys, _ := plans[0].AuxiliaryKeys()
			var got []string
			for k := range res.Auxiliary {
				got = append(got, k)
			}
			sort.Strings(got)
			slices.Sort(keys)
			if !reflect.DeepEqual(keys, got) {
				t.Errorf("plan auxiliary keys %v, runtime %v", keys, got)
			}
			if n := len(sem.SpecifiedRows(plans[0].SEM.Model)); n != len(est.RowKeys) || len(vectors.SEMEstimateColumnsOf(plans[0].SEM)) != len(est.ColumnKeys) {
				t.Errorf("plan sizes estimates %d x %d, runtime %d x %d", n, len(vectors.SEMEstimateColumnsOf(plans[0].SEM)), len(est.RowKeys), len(est.ColumnKeys))
			}
		})
	}
}
