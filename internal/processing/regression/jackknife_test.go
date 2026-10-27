package regression

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// jackknifeCase is one jackknife.json case (scripts/reference/
// gen_jackknife.R): manual deletions in R, rows [x1, x2, y, w, c]; a
// JSON null cell is a null field (the row drops from the fit).
type jackknifeCase struct {
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	Family       string       `json:"family"`
	Penalty      string       `json:"penalty"`
	Alpha        float64      `json:"alpha"`
	LeaveOneOut  bool         `json:"leave_one_out"`
	Weighted     bool         `json:"weighted"`
	Groups       int          `json:"groups"`
	Rows         [][]*float64 `json:"rows"`
	Coefficients []float64    `json:"coefficients"`
	StdErrors    []float64    `json:"std_errors"`
	Vcov         [][]float64  `json:"vcov"`
}

func loadJackknifeReference(t *testing.T) []jackknifeCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "jackknife.json"))
	if err != nil {
		t.Fatalf("read jackknife.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		Packages map[string]string `json:"packages"`
		Cases    []jackknifeCase   `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode jackknife.json: %v", err)
	}
	if doc.Packages["survey"] == "" || len(doc.Cases) == 0 {
		t.Fatal("jackknife.json has no survey version or no cases")
	}
	return doc.Cases
}

func (c jackknifeCase) records() []Record {
	recs := make([]Record, len(c.Rows))
	names := []string{"x1", "x2", "y", "w", "c"}
	for i, r := range c.Rows {
		rec := mockRecord{values: map[string]float64{}, nulls: map[string]bool{}}
		for j, name := range names {
			if r[j] == nil {
				rec.nulls[name] = true
				continue
			}
			rec.values[name] = *r[j]
		}
		recs[i] = rec
	}
	return recs
}

func (c jackknifeCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{
		Name: c.Name, Type: types.RegressionType(c.Type), Target: "y", Predictors: []string{"x1", "x2"},
		Resample: "jackknife", Vcov: true, Tol: 1e-13, MaxIters: 200,
	}
	if !c.LeaveOneOut {
		s.JackknifeBy = "c"
	}
	if c.Type == string(types.REG_GLM) {
		s.Family = c.Family
	}
	if c.Penalty != "" {
		s.Penalty, s.Alpha = c.Penalty, c.Alpha
	}
	if c.Weighted {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	return s
}

// fitModifier routes spec through the modifier engine (the registry
// path every Resample spec takes).
func fitModifier(t *testing.T, spec *types.RegressionSpec, records []Record) (*types.RegressionResult, error) {
	t.Helper()
	eng, err := newModifierEngine(spec, nil)
	if err != nil {
		t.Fatalf("newModifierEngine: %v", err)
	}
	return eng.(BufferedEngine).FitBuffered(records)
}

// TestJackknife_MatchesROracle: the delete-a-group (cluster and
// assignment), frequency-weighted (JK1), l2-penalised and weighted
// leave-one-out jackknife of REG_OLS and REG_GLM reproduce R's manual
// deletions — the coefficients, the jackknife standard errors and the
// replicate covariance — and echo g.
func TestJackknife_MatchesROracle(t *testing.T) {
	t.Parallel()

	for _, c := range loadJackknifeReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			res, err := fitModifier(t, c.spec(), c.records())
			if err != nil {
				t.Fatalf("fit: %v", err)
			}
			keys := []string{InterceptKey, "x1", "x2"}
			for j, k := range keys {
				if !relNear(res.Coefficients[k], c.Coefficients[j], 1e-8) {
					t.Errorf("coef %s = %.12g, R %.12g", k, res.Coefficients[k], c.Coefficients[j])
				}
				if !relNear(res.StdErrors[k], c.StdErrors[j], 1e-7) {
					t.Errorf("se %s = %.12g, R %.12g", k, res.StdErrors[k], c.StdErrors[j])
				}
				for i := range keys {
					if !relNear(res.Vcov.Values[j][i], c.Vcov[j][i], 1e-7) {
						t.Errorf("vcov[%d][%d] = %.12g, R %.12g", j, i, res.Vcov.Values[j][i], c.Vcov[j][i])
					}
				}
			}
			wantG := c.Groups
			if c.LeaveOneOut {
				wantG = 0 // leave-one-out keeps its wire shape: no echo
			}
			if res.JackknifeGroups != wantG {
				t.Errorf("jackknife_groups echo = %d, want %d", res.JackknifeGroups, wantG)
			}
			if res.Resample != "jackknife" {
				t.Errorf("resample echo = %q", res.Resample)
			}
		})
	}
}

// glmJackknifeRecords is a binomial sample (FMA-safe generator).
func glmJackknifeRecords(n int, seed uint64) []Record {
	rng := rand.New(rand.NewPCG(seed, seed^0xC0FFEE))
	out := make([]Record, n)
	for i := range out {
		x1 := rng.NormFloat64()
		x2 := rng.Float64()*2 - 1
		eta := -0.4 + float64(0.9*x1) - float64(0.6*x2)
		y := 0.0
		if rng.Float64() < 1/(1+math.Exp(-eta)) {
			y = 1
		}
		out[i] = mockRecord{values: map[string]float64{"x1": x1, "x2": x2, "y": y, "w": float64(1 + i%3), "id": float64(i)}}
	}
	return out
}

// TestJackknife_GroupsAreTheSeededAssignment: jackknife_groups g with a
// seed is the delete-a-cluster jackknife over jackknifeAssignment(m, g,
// seed) bit for bit (so the cluster oracle above covers the grouped
// arithmetic); the same seed reproduces it and another seed moves it.
func TestJackknife_GroupsAreTheSeededAssignment(t *testing.T) {
	t.Parallel()

	recs := glmJackknifeRecords(400, 11)
	grouped := func(seed int64) *types.RegressionSpec {
		return &types.RegressionSpec{Name: "g", Type: types.REG_GLM, Family: "binomial", Target: "y",
			Predictors: []string{"x1", "x2"}, Resample: "jackknife", JackknifeGroups: 7, RNGSeed: seed, Vcov: true}
	}
	a, err := fitModifier(t, grouped(5), recs)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := fitModifier(t, grouped(5), recs)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed, different jackknife")
	}
	c, _ := fitModifier(t, grouped(6), recs)
	if reflect.DeepEqual(a.StdErrors, c.StdErrors) {
		t.Fatal("a different seed drew the same groups")
	}
	assign := jackknifeAssignment(len(recs), 7, 5)
	sizes := make([]int, 7)
	tagged := make([]Record, len(recs))
	for i, r := range recs {
		sizes[assign[i]]++
		v := map[string]float64{}
		for k, x := range r.(mockRecord).values {
			v[k] = x
		}
		v["grp"] = float64(assign[i])
		tagged[i] = mockRecord{values: v}
	}
	for _, s := range sizes {
		if s < 400/7 || s > 400/7+1 {
			t.Fatalf("group sizes %v are not balanced", sizes)
		}
	}
	by := grouped(0)
	by.JackknifeGroups, by.JackknifeBy = 0, "grp"
	d, err := fitModifier(t, by, tagged)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.StdErrors, d.StdErrors) || !reflect.DeepEqual(a.Vcov, d.Vcov) || a.JackknifeGroups != 7 || d.JackknifeGroups != 7 {
		t.Fatalf("grouped %v (g %d) != cluster over its assignment %v (g %d)", a.StdErrors, a.JackknifeGroups, d.StdErrors, d.JackknifeGroups)
	}
}

// TestJackknife_ZeroSeedIsDeterministic: rng_seed 0 derives the seed
// from the request and the data, never the clock.
func TestJackknife_ZeroSeedIsDeterministic(t *testing.T) {
	t.Parallel()

	recs := glmJackknifeRecords(120, 3)
	spec := &types.RegressionSpec{Name: "g", Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"},
		Resample: "jackknife", JackknifeGroups: 10}
	a, err := fitModifier(t, spec, recs)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := fitModifier(t, spec, recs)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("rng_seed 0 is not deterministic")
	}
}

// TestJackknife_UnitWeightsAreUnweighted: a frequency weight of 1 on
// every row is the unweighted jackknife bit for bit, leave-one-out and
// grouped, REG_OLS and REG_GLM (the weighted refit folds w = 1 exactly).
func TestJackknife_UnitWeightsAreUnweighted(t *testing.T) {
	t.Parallel()

	recs := glmJackknifeRecords(60, 4)
	for i, r := range recs {
		r.(mockRecord).values["one"] = 1
		recs[i] = r
	}
	for _, typ := range []types.RegressionType{types.REG_OLS, types.REG_GLM} {
		for _, groups := range []int{0, 6} {
			spec := &types.RegressionSpec{Name: "u", Type: typ, Target: "y", Predictors: []string{"x1", "x2"},
				Resample: "jackknife", JackknifeGroups: groups, RNGSeed: 9, Vcov: true}
			if typ == types.REG_GLM {
				spec.Family = "binomial"
			}
			plain, err := fitModifier(t, spec, recs)
			if err != nil {
				t.Fatal(err)
			}
			w := *spec
			w.Weight = types.SlotWeightOf(types.WeightSpec{Field: "one", Kind: types.WeightKindFrequency})
			weighted, err := fitModifier(t, &w, recs)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(plain.StdErrors, weighted.StdErrors) || !reflect.DeepEqual(plain.Vcov, weighted.Vcov) {
				t.Errorf("%s groups %d: unit weights moved the jackknife:\n %v\n %v", typ, groups, plain.StdErrors, weighted.StdErrors)
			}
		}
	}
}

// TestJackknife_ByDropsNullClusterRows: a row whose jackknife_by value
// is null drops from the fit listwise — the full-data fit too — so the
// result equals the fit on the rows that carry one.
func TestJackknife_ByDropsNullClusterRows(t *testing.T) {
	t.Parallel()

	recs := glmJackknifeRecords(90, 8)
	withNull := make([]Record, 0, len(recs))
	kept := make([]Record, 0, len(recs))
	for i, r := range recs {
		v := r.(mockRecord).values
		v["cl"] = float64(i % 5)
		if i%11 == 0 {
			withNull = append(withNull, mockRecord{values: v, nulls: map[string]bool{"cl": true}})
			continue
		}
		withNull = append(withNull, r)
		kept = append(kept, r)
	}
	spec := &types.RegressionSpec{Name: "c", Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"},
		Resample: "jackknife", JackknifeBy: "cl"}
	a, err := fitModifier(t, spec, withNull)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fitModifier(t, spec, kept)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("null cluster rows entered the fit:\n %+v\n %+v", a, b)
	}
	if a.NObs != len(kept) {
		t.Fatalf("NObs %d, want %d", a.NObs, len(kept))
	}
}

// TestJackknife_DataRefusals: g above the fitted row count, a single
// cluster, and a replicate whose refit fails (deleting the only group
// in which x1 varies leaves it constant) fail the slot with the
// refit's own code; the replicate refusal names the group.
func TestJackknife_DataRefusals(t *testing.T) {
	t.Parallel()

	recs := glmJackknifeRecords(30, 5)
	base := func() *types.RegressionSpec {
		return &types.RegressionSpec{Name: "r", Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}, Resample: "jackknife"}
	}
	codeOf := func(err error) (errors.Code, map[string]any) {
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) {
			t.Fatalf("uncoded error %v", err)
		}
		return ce.Code, ce.Details
	}

	tooMany := base()
	tooMany.JackknifeGroups = 31
	_, err := fitModifier(t, tooMany, recs)
	if code, d := codeOf(err); code != errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA || d["n"] != 30 {
		t.Fatalf("g > n: %v %v", code, d)
	}

	one := base()
	one.JackknifeBy = "w"
	single := make([]Record, len(recs))
	for i, r := range recs {
		v := map[string]float64{}
		for k, x := range r.(mockRecord).values {
			v[k] = x
		}
		v["w"] = 2
		single[i] = mockRecord{values: v}
	}
	_, err = fitModifier(t, one, single)
	if code, d := codeOf(err); code != errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA || d["groups"] != 1 {
		t.Fatalf("one cluster: %v %v", code, d)
	}

	// x1 varies only inside cluster 0.
	degenerate := make([]Record, len(recs))
	for i, r := range recs {
		v := map[string]float64{}
		for k, x := range r.(mockRecord).values {
			v[k] = x
		}
		v["cl"] = float64(i % 3)
		if i%3 != 0 {
			v["x1"] = 0
		}
		degenerate[i] = mockRecord{values: v}
	}
	by := base()
	by.JackknifeBy = "cl"
	_, err = fitModifier(t, by, degenerate)
	if err == nil {
		t.Fatal("a replicate with a constant predictor refit")
	}
	code, d := codeOf(err)
	if code == errors.PROCESSING_CONFIG || d["jackknife_group"] != 0 || d["jackknife_by"] != "cl" || d["jackknife_value"] != "0" {
		t.Fatalf("replicate refusal: %v %v", code, d)
	}
}

// TestIRLS_WarmStartSameFixedPoint: a warm start at the full-data β
// converges to the cold start's estimate (to the convergence
// tolerance) in fewer iterations; an invalid start falls back.
func TestIRLS_WarmStartSameFixedPoint(t *testing.T) {
	t.Parallel()

	recs := glmJackknifeRecords(500, 21)
	x, y := buildDesign(recs, []string{"x1", "x2"}, "y")
	pr := func(start []float64) *irlsProblem {
		return &irlsProblem{X: x, Y: y, K: 3, Columns: []string{InterceptKey, "x1", "x2"},
			Family: binomialLogit, Tol: 1e-12, Start: start}
	}
	cold, err := irlsFit(pr(nil))
	if err != nil {
		t.Fatal(err)
	}
	// Start from the fit on all but the first 20 rows (a replicate's
	// distance from the full fit).
	x2, y2 := buildDesign(recs[20:], []string{"x1", "x2"}, "y")
	other, err := irlsFit(&irlsProblem{X: x2, Y: y2, K: 3, Family: binomialLogit, Tol: 1e-12})
	if err != nil {
		t.Fatal(err)
	}
	warm, err := irlsFit(pr(other.Beta))
	if err != nil {
		t.Fatal(err)
	}
	for j := range cold.Beta {
		if !relNear(warm.Beta[j], cold.Beta[j], 1e-11) {
			t.Errorf("beta[%d] warm %.17g cold %.17g", j, warm.Beta[j], cold.Beta[j])
		}
	}
	if warm.Iters >= cold.Iters {
		t.Errorf("warm start took %d iterations, cold %d", warm.Iters, cold.Iters)
	}
	bad, err := irlsFit(pr([]float64{math.NaN(), 0, 0}))
	if err != nil || !reflect.DeepEqual(bad.Beta, cold.Beta) {
		t.Errorf("an invalid start did not fall back to the cold start: %v", err)
	}
}

// BenchmarkJackknife times the REG_GLM binomial jackknife arms. Run
// with -bench Jackknife -benchtime 1x; the leave-one-out arm is sized
// down (it is quadratic) — its per-row cost extrapolates by n².
func BenchmarkJackknife(b *testing.B) {
	run := func(b *testing.B, n int, spec *types.RegressionSpec, warm bool) {
		recs := glmJackknifeRecords(n, 1)
		eng, _ := newModifierEngine(spec, nil)
		me := eng.(*modifierEngine)
		base, err := me.finalizeOnPredictors(recs, spec.Predictors)
		if err != nil {
			b.Fatal(err)
		}
		fitter, pv := me.makeResampleFitter(spec.Predictors)
		var wf func([]Record, []float64) (*refitResult, error)
		if warm {
			wf = me.makeWarmFitter(spec.Predictors)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			start := time.Now()
			if _, err := runResample(spec, recs, spec.Predictors, base, fitter, wf, pv); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(time.Since(start).Seconds(), "s/fit")
		}
	}
	glm := func(groups int) *types.RegressionSpec {
		return &types.RegressionSpec{Name: "b", Type: types.REG_GLM, Family: "binomial", Target: "y",
			Predictors: []string{"x1", "x2"}, Resample: "jackknife", JackknifeGroups: groups, RNGSeed: 1}
	}
	b.Run("grouped50_n42000_warm", func(b *testing.B) { run(b, 42000, glm(50), true) })
	b.Run("grouped50_n42000_cold", func(b *testing.B) { run(b, 42000, glm(50), false) })
	b.Run("loo_n2000", func(b *testing.B) { run(b, 2000, glm(0), false) })
}
