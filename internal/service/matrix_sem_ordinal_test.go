package service

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// matrix_sem_ordinal_test.go is MAT_SEM under params.estimator "dwls"
// / "wlsmv" end to end: a .pulse cohort of the brand survey's 1-5
// ratings, Process, the buffered rows, the two-step statistics, the
// fit — every figure held to lavaan 0.7.3's (internal/sem/testdata
// fit_lavaan_wlsmv.R).

type ordinalLavaanFixture struct {
	lavaanFixture
	Options struct {
		Estimator string   `json:"estimator"`
		Ordered   []string `json:"ordered"`
	} `json:"options"`
}

// lavaanScaledKeys maps the WLSMV scalars to lavaan's fitMeasures.
var lavaanScaledKeys = map[string]string{
	"chi2_scaled": "chisq.scaled", "chi2_scaled_p_value": "pvalue.scaled", "scaling_factor": "chisq.scaling.factor",
	"chi2_baseline_scaled": "baseline.chisq.scaled", "cfi_scaled": "cfi.scaled", "tli_scaled": "tli.scaled",
	"rmsea_scaled": "rmsea.scaled",
}

func ordinalSpec(t *testing.T, cols []string, model string, params map[string]any) types.MatrixSpec {
	t.Helper()
	desc, err := os.ReadFile(semTestdata + model + "_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]any{"model": string(desc)}
	for k, v := range params {
		all[k] = v
	}
	p, _ := json.Marshal(all)
	return types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: semModelMembers(t, cols, string(desc)), Params: p}
}

// TestMatrixSEM_OrdinalMatchesLavaan: DWLS and WLSMV over the nine
// ordered ratings (a measurement model, a mean structure with a
// continuous outcome, := rows on the robust vcov, a structural
// regression) match lavaan row for row — estimates, std.all, the
// robust SE / z / p / interval, the threshold rows — and every fit
// measure, the scaled-shifted test included. The plan sizes the
// threshold rows predict estimates (four per 1-5 rating) exactly.
func TestMatrixSEM_OrdinalMatchesLavaan(t *testing.T) {
	t.Parallel()

	cfg, cols := semCohort(t, semTestdata+"brand_data.csv", nil, nil)
	for _, c := range []struct{ ref, model string }{
		{"brand_ord_wlsmv", "brand_ord"},
		{"brand_ord_dwls", "brand_ord"},
		{"brand_ordms_wlsmv", "brand_ms"},
		{"brand_orddef_wlsmv", "brand_def"},
		{"brand_ordres_wlsmv", "brand_res"},
	} {
		t.Run(c.ref, func(t *testing.T) {
			var ref ordinalLavaanFixture
			raw, err := os.ReadFile(semTestdata + c.ref + "_lavaan.json")
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &ref); err != nil {
				t.Fatal(err)
			}
			est := strings.ToLower(ref.Options.Estimator)
			spec := ordinalSpec(t, cols, c.model, map[string]any{"estimator": est, "ordered": ref.Options.Ordered})
			req := &types.Request{Cohort: &types.Cohort{Filename: "sem.pulse"}, Matrices: []types.MatrixSpec{spec}}
			if spec.Streamable() {
				t.Fatal("an ordinal MAT_SEM spec streams; it must buffer")
			}
			res := processMatrices(t, cfg, req).Matrices[0]
			if res.Scalars["converged"] != 1 || len(res.Warnings) != 0 {
				t.Fatalf("converged %v, warnings %v", res.Scalars["converged"], matWarningCodes(res))
			}
			tab := res.Auxiliary["estimates"]
			rows := map[string][]float64{}
			for i, k := range tab.RowKeys {
				rows[k] = tab.Values[i]
				if lhs, _, ok := strings.Cut(k, " := "); ok {
					rows[lhs+" :="] = tab.Values[i]
				}
			}
			matched := map[string]int{}
			for _, r := range ref.Params {
				var key string
				switch r.Op {
				case "~*~":
					continue // the delta scaling factors (fixed 1) are lavaan-internal
				case "=~":
					key = r.RHS + " ~ " + r.LHS
				case "~1":
					key = r.LHS + " ~1"
				case ":=":
					key = r.LHS + " :="
				default:
					key = r.LHS + " " + r.Op + " " + r.RHS
				}
				row, ok := rows[key]
				if !ok && r.Op == "~~" {
					row, ok = rows[r.RHS+" ~~ "+r.LHS]
				}
				if !ok {
					t.Errorf("lavaan row %q missing", key)
					continue
				}
				matched[r.Op]++
				near := func(what string, g float64, w *float64, tol float64) {
					if w == nil || !(math.Abs(g-*w) <= tol*math.Max(1, math.Abs(*w))) {
						t.Errorf("%s %s = %.10g, lavaan %v", key, what, g, w)
					}
				}
				near("estimate", row[0], r.Est, 1e-5)
				near("std_estimate", row[1], r.StdAll, 1e-5)
				if !r.Free && r.Op != ":=" {
					continue
				}
				near("std_err", row[2], r.SE, 1e-5)
				near("z_value", row[3], r.Z, 1e-3)
				near("p_value", row[4], r.P, 1e-5)
				near("ci_lower", row[5], r.CILower, 1e-5)
				near("ci_upper", row[6], r.CIUpper, 1e-5)
			}
			if matched["|"] != 4*len(ref.Options.Ordered) {
				t.Errorf("%d threshold rows matched, want %d", matched["|"], 4*len(ref.Options.Ordered))
			}
			if c.model == "brand_def" && matched[":="] == 0 {
				t.Error("no := row matched: the fixture is vacuous")
			}
			for k, lk := range lavaanScalarKeys {
				w := ref.Fit[lk]
				g := res.Scalars[k]
				if w == nil {
					if !math.IsNaN(g) {
						t.Errorf("scalar %s = %v, lavaan NA", k, g)
					}
					continue
				}
				if !(math.Abs(g-*w) <= 1e-4*math.Max(1, math.Abs(*w))) {
					t.Errorf("scalar %s = %.10g, lavaan %s %v", k, g, lk, *w)
				}
			}
			for k, lk := range lavaanScaledKeys {
				g, has := res.Scalars[k]
				if est != "wlsmv" {
					if has {
						t.Errorf("dwls carries scalar %s", k)
					}
					continue
				}
				if w := ref.Fit[lk]; w == nil || !(math.Abs(g-*w) <= 1e-4*math.Max(1, math.Abs(*w))) {
					t.Errorf("scalar %s = %.10g, lavaan %s %v", k, g, lk, w)
				}
			}
			if est == "wlsmv" {
				if g := res.Scalars["shift_parameter"]; math.IsNaN(g) {
					t.Error("shift_parameter is null")
				}
			}
			if res.Scalars["n"] != ref.N {
				t.Errorf("n = %v, lavaan %v", res.Scalars["n"], ref.N)
			}
			// Predict's threshold estimate (no schema bound on an f64
			// rating: SEMThresholdRowsDefault each) is the run's count.
			plans, cerr := vectors.ResolveMatrices(req, mustReadSchema(t, cfg.Fs(), "sem.pulse"), nil)
			if cerr != nil {
				t.Fatal(cerr)
			}
			want := len(sem.SpecifiedRows(plans[0].SEM.Model)) + len(sem.SpecifiedDefined(plans[0].SEM.Model)) + plans[0].SEM.ThresholdRowsEstimate()
			if want != len(tab.RowKeys) {
				t.Errorf("plan sizes %d estimate rows, runtime %d", want, len(tab.RowKeys))
			}
		})
	}
}

// TestMatrixSEM_OrdinalFrequencyWeightIsExpansion: a frequency weight
// is the expanded rows — the same thresholds, correlations, Gamma and
// so every figure.
func TestMatrixSEM_OrdinalFrequencyWeightIsExpansion(t *testing.T) {
	t.Parallel()

	w := func(r int) float64 { return float64(1 + r%3) }
	weighted, cols := semCohort(t, semTestdata+"brand_data.csv", w, nil)
	expanded, _ := semCohort(t, semTestdata+"brand_data.csv", nil, func(r int) int { return 1 + r%3 })
	ordered := []string{"q1", "q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3"}
	spec := ordinalSpec(t, cols, "brand_ord", map[string]any{"estimator": "wlsmv", "ordered": ordered})
	plain := processMatrices(t, expanded, &types.Request{Cohort: &types.Cohort{Filename: "sem.pulse"}, Matrices: []types.MatrixSpec{spec}}).Matrices[0]
	spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	wres := processMatrices(t, weighted, &types.Request{Cohort: &types.Cohort{Filename: "sem.pulse"}, Matrices: []types.MatrixSpec{spec}}).Matrices[0]
	if wres.Scalars["n"] != plain.Scalars["n"] || plain.Scalars["n"] != 2400 {
		t.Fatalf("n weighted %v, expanded %v (want 2400)", wres.Scalars["n"], plain.Scalars["n"])
	}
	for k, v := range plain.Scalars {
		g := wres.Scalars[k]
		if math.IsNaN(v) != math.IsNaN(g) || (!math.IsNaN(v) && semRel(g, v) > 1e-7) {
			t.Errorf("%s weighted %.12g, expanded %.12g", k, g, v)
		}
	}
	for i, row := range plain.Auxiliary["estimates"].Values {
		for c, v := range row {
			g := wres.Auxiliary["estimates"].Values[i][c]
			if math.IsNaN(v) != math.IsNaN(g) || (!math.IsNaN(v) && semRel(g, v) > 1e-7) {
				t.Errorf("estimates[%s][%d] weighted %.12g, expanded %.12g", plain.Auxiliary["estimates"].RowKeys[i], c, g, v)
			}
		}
	}
}

// TestMatrixSEM_OrdinalRefusals: the params an ordinal fit cannot honour
// are bad_params, parameterization "theta" parameterization_unsupported;
// a constant ordered member fails the slot with PULSE_MATRIX_SINGULAR
// (details.reason constant), and a probability weight stays
// PULSE_WEIGHT_UNSUPPORTED.
func TestMatrixSEM_OrdinalRefusals(t *testing.T) {
	t.Parallel()

	cfg, cols := semCohort(t, semTestdata+"brand_data.csv", func(r int) float64 { return 1 + 0.5*float64(r%2) }, nil)
	ordered := []string{"q1", "q2", "q3"}
	for _, c := range []struct {
		name   string
		params map[string]any
		param  string
	}{
		{"estimator unknown", map[string]any{"estimator": "uls"}, "estimator"},
		{"ordered without estimator", map[string]any{"ordered": ordered}, "ordered"},
		{"estimator without ordered", map[string]any{"estimator": "wlsmv"}, "ordered"},
		{"empty ordered", map[string]any{"estimator": "wlsmv", "ordered": []string{}}, "ordered"},
		{"duplicate ordered", map[string]any{"estimator": "wlsmv", "ordered": []string{"q1", "q1"}}, "ordered"},
		{"ordered latent", map[string]any{"estimator": "wlsmv", "ordered": []string{"quality"}}, "ordered"},
		{"solver slsqp", map[string]any{"estimator": "wlsmv", "ordered": ordered, "solver": "slsqp"}, "solver"},
		{"information", map[string]any{"estimator": "dwls", "ordered": ordered, "information": "rank2"}, "information"},
		{"singular", map[string]any{"estimator": "dwls", "ordered": ordered, "singular": "refuse"}, "singular"},
		{"meanstructure false", map[string]any{"estimator": "wlsmv", "ordered": ordered, "meanstructure": false}, "meanstructure"},
		{"parameterization without estimator", map[string]any{"parameterization": "delta"}, "parameterization"},
		{"parameterization unknown", map[string]any{"estimator": "wlsmv", "ordered": ordered, "parameterization": "logit"}, "parameterization"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, ce := semProcessErr(t, cfg, ordinalSpec(t, cols, "brand_ord", c.params))
			if ce == nil || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "bad_params" || ce.Details["param"] != c.param {
				t.Fatalf("err = %v, want bad_params on %s", ce, c.param)
			}
		})
	}
	_, ce := semProcessErr(t, cfg, ordinalSpec(t, cols, "brand_ord", map[string]any{"estimator": "wlsmv", "ordered": ordered, "parameterization": "theta"}))
	if ce == nil || ce.Code != errors.SERVICE_VALIDATION || ce.Details["reason"] != "parameterization_unsupported" || ce.Details["param"] != "parameterization" {
		t.Fatalf("theta: %v (%v), want parameterization_unsupported", ce, detailsOf(ce))
	}
	prob := ordinalSpec(t, cols, "brand_ord", map[string]any{"estimator": "wlsmv", "ordered": ordered})
	prob.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	if _, ce := semProcessErr(t, cfg, prob); ce == nil || ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED {
		t.Errorf("probability weight: %v, want PULSE_WEIGHT_UNSUPPORTED", ce)
	}

	var rows [][3]float64
	for i := range 60 {
		rows = append(rows, [3]float64{float64(1 + i%3), 2, float64(1 + (i/3)%4)})
	}
	flat := semRowsCohort(t, rows)
	_, ce = semProcessErr(t, flat, types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: []string{"a", "b", "c"},
		Params: json.RawMessage(`{"model":"f =~ a + b + c","estimator":"wlsmv","ordered":["a","b","c"]}`)})
	if ce == nil || ce.Code != errors.PULSE_MATRIX_SINGULAR || ce.Details["reason"] != sem.ReasonConstant || ce.Details["variable"] != "b" {
		t.Fatalf("constant member: %v (details %v), want PULSE_MATRIX_SINGULAR reason constant on b", ce, detailsOf(ce))
	}
}

func detailsOf(ce *errors.CodedError) map[string]any {
	if ce == nil {
		return nil
	}
	return ce.Details
}

// TestMatrixSEM_OrdinalTooManyCategories: an ordered member holding more
// than sem.MaxOrderedCategories distinct values over the slot's rows
// fails the slot ALONE with PULSE_MATRIX_TOO_MANY_CATEGORIES (details
// variable, categories, limit): the response carries the slot's
// identity and error, no figure; one at the ceiling fits, and the same
// member declared numeric fits too.
func TestMatrixSEM_OrdinalTooManyCategories(t *testing.T) {
	t.Parallel()

	build := func(k int) *fs.Config {
		var rows [][3]float64
		for i := range 40 * k {
			c := float64(1 + (i/2)%3)
			rows = append(rows, [3]float64{float64(i % k), c, float64(1 + (i/3)%4)})
		}
		return semRowsCohort(t, rows)
	}
	spec := func(ordered string) types.MatrixSpec {
		return types.MatrixSpec{Name: "sem", Type: types.MAT_SEM, Fields: []string{"a", "b", "c"},
			Params: json.RawMessage(`{"model":"f =~ a + b + c","estimator":"wlsmv","ordered":` + ordered + `}`)}
	}
	resp, ce := semProcessErr(t, build(sem.MaxOrderedCategories+1), spec(`["a","b","c"]`))
	if resp == nil || len(resp.Matrices) != 1 || resp.Matrices[0].Error == nil || resp.Matrices[0].Primary != nil || resp.Matrices[0].Name != "sem" {
		t.Fatalf("%d categories: response %+v, want the slot failed alone (identity + error, no figure)", sem.MaxOrderedCategories+1, resp)
	}
	if ce == nil || ce.Code != errors.PULSE_MATRIX_TOO_MANY_CATEGORIES || ce.Details["variable"] != "a" ||
		ce.Details["categories"] != sem.MaxOrderedCategories+1 || ce.Details["limit"] != sem.MaxOrderedCategories {
		t.Fatalf("%d categories: %v (%v), want PULSE_MATRIX_TOO_MANY_CATEGORIES on a", sem.MaxOrderedCategories+1, ce, detailsOf(ce))
	}
	if _, ce := semProcessErr(t, build(sem.MaxOrderedCategories+1), spec(`["b","c"]`)); ce != nil && ce.Code == errors.PULSE_MATRIX_TOO_MANY_CATEGORIES {
		t.Fatalf("a numeric member: %v", ce)
	}
	if _, ce := semProcessErr(t, build(sem.MaxOrderedCategories), spec(`["a","b","c"]`)); ce != nil && ce.Code == errors.PULSE_MATRIX_TOO_MANY_CATEGORIES {
		t.Fatalf("%d categories refused: %v", sem.MaxOrderedCategories, ce)
	}
}
