package regression

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// glmIRLSCase is one glm_irls.json case (scripts/reference/gen_glm_irls.R).
type glmIRLSCase struct {
	Name      string `json:"name"`
	Family    string `json:"family"`
	Link      string `json:"link"`
	Target    string `json:"target"`
	Predictor string `json:"predictor"`
	NRows     int    `json:"n_rows"`
	Cells     []struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		N int     `json:"n"`
	} `json:"cells"`
	RWarnings           []string  `json:"r_warnings"`
	Separated           bool      `json:"separated"`
	Coefficients        []float64 `json:"coefficients"`
	ClosedFormCoefs     []float64 `json:"closed_form_coefficients"`
	StdErrors           []float64 `json:"std_errors"`
	ClosedFormStdErrors []float64 `json:"closed_form_std_errors"`
	Deviance            float64   `json:"deviance"`
	NullDeviance        float64   `json:"null_deviance"`
	// Gamma only: summary.glm's estimated dispersion, its residual df
	// and t-based p-values (std_errors read the same dispersion).
	Dispersion float64   `json:"dispersion"`
	DFResidual float64   `json:"df_residual"`
	PValues    []float64 `json:"p_values"`
}

// loadGLMIRLSReference reads the R oracle golden, footer stripped.
func loadGLMIRLSReference(t *testing.T) []glmIRLSCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "glm_irls.json"))
	if err != nil {
		t.Fatalf("read glm_irls.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		RVersion string        `json:"r_version"`
		Cases    []glmIRLSCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode glm_irls.json: %v", err)
	}
	if doc.RVersion == "" || len(doc.Cases) == 0 {
		t.Fatalf("glm_irls.json has no R version or no cases")
	}
	return doc.Cases
}

func (c glmIRLSCase) records() []Record {
	var recs []Record
	for _, cell := range c.Cells {
		for i := 0; i < cell.N; i++ {
			recs = append(recs, mockRecord{values: map[string]float64{c.Predictor: cell.X, c.Target: cell.Y}})
		}
	}
	return recs
}

// oracleClose is the reference oracles' comparison: relative to the
// larger magnitude, with an absolute floor of 1e-14 only. The floor is
// for a reference that is zero up to rounding — glm_penalty.json
// separated_l2's intercept is 1.7e-16 by the design's symmetry, so it has
// no relative digits — and sits at a few ulps of the O(1) coefficients
// around it. (Until reference-oracles E2-S1 the floor was 1: every figure
// below 1 in magnitude, most standard errors among them, was compared
// ABSOLUTELY.)
func oracleClose(got, want, tol float64) bool {
	return math.Abs(got-want) <= math.Max(tol*math.Max(math.Abs(got), math.Abs(want)), 1e-14)
}

// TestRegGLM_IRLSMatchesR: every non-separated case — the credit-risk
// showcase's default × collections_flag table (2% vs 92% default; the
// fit that used to diverge and blame collinearity) among them —
// converges to R glm's coefficients, standard errors (dispersion 1 for
// binomial / poisson, gamma's estimated dispersion with its t-based
// p-values and df) and deviances AND to the closed-form logits / log
// means / inverse means,
// every figure within relative 1e-10 (the golden's SEs are evaluated at
// the MLE by a warm-started refit, gen_glm_irls.R);
// every case R itself flags "fitted probabilities numerically 0 or 1"
// refuses with PROCESSING_REGRESSION_SEPARATION naming the predictor.
// epoch_seconds_poisson (x ~ 1.7e9, raw XᵀWX condition ~1e23) has no
// closed form; it pins the equilibrated solve against glm alone.
func TestRegGLM_IRLSMatchesR(t *testing.T) {
	t.Parallel()

	cases := loadGLMIRLSReference(t)
	var sawSeparated, sawConverged bool
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			recs := c.records()
			if len(recs) != c.NRows {
				t.Fatalf("expanded %d rows, golden says %d", len(recs), c.NRows)
			}
			spec := &types.RegressionSpec{
				Type: types.REG_GLM, Target: c.Target, Predictors: []string{c.Predictor},
				Family: c.Family, Link: c.Link,
			}
			res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
			if c.Separated {
				sawSeparated = true
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_REGRESSION_SEPARATION {
					t.Fatalf("err = %v, want PROCESSING_REGRESSION_SEPARATION (R warned %q)", err, c.RWarnings)
				}
				if got := ce.Details["predictors"]; !reflect.DeepEqual(got, []string{c.Predictor}) {
					t.Errorf("details.predictors = %v, want [%s]", got, c.Predictor)
				}
				return
			}
			sawConverged = true
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			terms := []string{InterceptKey, c.Predictor}
			for i, term := range terms {
				got := res.Coefficients[term]
				if !oracleClose(got, c.Coefficients[i], 1e-10) {
					t.Errorf("β[%s] = %.17g, R glm %.17g", term, got, c.Coefficients[i])
				}
				if c.ClosedFormCoefs != nil && !oracleClose(got, c.ClosedFormCoefs[i], 1e-10) {
					t.Errorf("β[%s] = %.17g, closed form %.17g", term, got, c.ClosedFormCoefs[i])
				}
				if se := res.StdErrors[term]; !oracleClose(se, c.StdErrors[i], 1e-10) {
					t.Errorf("SE[%s] = %.17g, R summary %.17g", term, se, c.StdErrors[i])
				}
				if c.PValues != nil && !oracleClose(res.PValues[term], c.PValues[i], 1e-10) {
					t.Errorf("p[%s] = %.17g, R summary %.17g", term, res.PValues[term], c.PValues[i])
				}
				if c.ClosedFormStdErrors != nil && !oracleClose(res.StdErrors[term], c.ClosedFormStdErrors[i], 1e-10) {
					t.Errorf("SE[%s] = %.17g, closed form %.17g", term, res.StdErrors[term], c.ClosedFormStdErrors[i])
				}
			}
			if c.Family == "gamma" {
				if c.PValues == nil || res.DF != c.DFResidual {
					t.Errorf("gamma df = %v, R df.residual %v (p-values in golden: %v)", res.DF, c.DFResidual, c.PValues != nil)
				}
			} else if res.DF != 0 {
				t.Errorf("df = %v, want none (Wald z at dispersion 1)", res.DF)
			}
			if !oracleClose(res.Deviance, c.Deviance, 1e-10) {
				t.Errorf("deviance = %.17g, R %.17g", res.Deviance, c.Deviance)
			}
			if !oracleClose(res.NullDeviance, c.NullDeviance, 1e-10) {
				t.Errorf("null deviance = %.17g, R %.17g", res.NullDeviance, c.NullDeviance)
			}
		})
	}
	if !sawSeparated || !sawConverged {
		t.Fatalf("golden must carry both separated and converging cases (separated %v, converged %v)", sawSeparated, sawConverged)
	}
}

// TestRegGLM_RankDeficient_UpFront: collinearity is refused by the
// up-front QR check BEFORE any iteration — details name the aliased
// column (the later one of a collinear set, R lm's convention) and carry
// no iteration — for an exact linear combination and for a constant
// predictor aliased with the intercept, on the plain and the refit path.
func TestRegGLM_RankDeficient_UpFront(t *testing.T) {
	t.Parallel()

	const n = 50
	cases := []struct {
		name       string
		predictors []string
		row        func(i int) map[string]float64
		want       []string
	}{
		{"linear combination", []string{"x1", "x2", "x3"}, func(i int) map[string]float64 {
			x1 := float64(i%7) - 3
			x3 := math.Sin(float64(i))
			return map[string]float64{"x1": x1, "x2": 2*x1 - x3, "x3": x3, "y": float64(i % 2)}
		}, []string{"x3"}},
		{"constant predictor", []string{"x1", "c"}, func(i int) map[string]float64 {
			return map[string]float64{"x1": float64(i % 5), "c": 4, "y": float64(i % 3 % 2)}
		}, []string{"c"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			recs := make([]Record, n)
			for i := range recs {
				recs[i] = mockRecord{values: c.row(i)}
			}
			spec := &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: c.predictors, Family: "binomial"}
			_, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
			_, rerr := glmFitter(recs, "y", c.predictors, binomialLogit, 0, 0, nil)
			for path, e := range map[string]error{"engine": err, "refit": rerr} {
				var ce *errors.CodedError
				if !stderrors.As(e, &ce) || ce.Code != errors.PROCESSING_REGRESSION_RANK_DEFICIENT {
					t.Fatalf("%s: err = %v, want PROCESSING_REGRESSION_RANK_DEFICIENT", path, e)
				}
				if got := ce.Details["columns"]; !reflect.DeepEqual(got, c.want) {
					t.Errorf("%s: details.columns = %v, want %v", path, got, c.want)
				}
				if _, ok := ce.Details["iter"]; ok {
					t.Errorf("%s: details carry an iteration %v; the check runs before the loop", path, ce.Details["iter"])
				}
			}
		})
	}
}

// TestAliasedColumns_WeightedDesign: the rank check reads the PRIOR-
// WEIGHTED design — a column that is only non-zero on zero-weight rows
// is aliased, though the unweighted design has full rank.
func TestAliasedColumns_WeightedDesign(t *testing.T) {
	t.Parallel()

	// k = 3: intercept, x, z; z is non-zero only on rows weighted 0.
	x := []float64{1, 0, 0, 1, 1, 0, 1, 2, 0, 1, 3, 1, 1, 4, 2}
	prior := []float64{1, 1, 1, 0, 0}
	ones := []float64{1, 1, 1, 1, 1}
	if got := aliasedColumns(x, ones, 5, 3); len(got) != 0 {
		t.Fatalf("unweighted design aliased %v, want full rank", got)
	}
	if got := aliasedColumns(x, prior, 5, 3); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("weighted design aliased %v, want [2] (z is zero on every weighted row)", got)
	}
}

// TestRegGLM_Separation_NotBlamedOnCollinearity: a separated fit never
// surfaces RANK_DEFICIENT however long the loop runs (the old loop
// raised it once the working weights collapsed), and a run too short to
// reach the boundary stays NO_CONVERGE — separation needs the evidence.
func TestRegGLM_Separation_NotBlamedOnCollinearity(t *testing.T) {
	t.Parallel()

	recs := make([]Record, 40)
	for i := range recs {
		x := float64(i) - 19.5
		y := 0.0
		if x >= 0 {
			y = 1
		}
		recs[i] = mockRecord{values: map[string]float64{"x": x, "y": y}}
	}
	for _, iters := range []int{15, 50, 200} {
		spec := &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "binomial", MaxIters: iters}
		_, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
		if !errors.HasCode(err, errors.PROCESSING_REGRESSION_SEPARATION) {
			t.Errorf("max_iters %d: err = %v, want PROCESSING_REGRESSION_SEPARATION", iters, err)
		}
	}
	spec := &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "binomial", MaxIters: 2}
	_, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
	if !errors.HasCode(err, errors.PROCESSING_REGRESSION_NO_CONVERGE) {
		t.Errorf("max_iters 2: err = %v, want PROCESSING_REGRESSION_NO_CONVERGE (no boundary evidence yet)", err)
	}
	// The refit path classifies identically.
	_, rerr := glmFitter(recs, "y", []string{"x"}, binomialLogit, 50, 0, nil)
	if !errors.HasCode(rerr, errors.PROCESSING_REGRESSION_SEPARATION) {
		t.Errorf("refit: err = %v, want PROCESSING_REGRESSION_SEPARATION", rerr)
	}
}

// TestRegGLM_PoissonNoConvergeIsNotSeparation: separation is a binomial
// verdict — a poisson fit that diverges (a level with only zero counts:
// its log mean runs to −∞) stays PROCESSING_REGRESSION_NO_CONVERGE.
func TestRegGLM_PoissonNoConvergeIsNotSeparation(t *testing.T) {
	t.Parallel()

	var recs []Record
	for i := 0; i < 30; i++ {
		y := 0.0
		x := 0.0
		if i >= 10 {
			x, y = 1, float64(2+i%4)
		}
		recs = append(recs, mockRecord{values: map[string]float64{"x": x, "y": y}})
	}
	// At 12 iterations the slope is still climbing by exactly 1 per
	// iteration (the intercept falling by 1) — the separation pattern,
	// on a family where it is not a separation.
	for _, iters := range []int{12, 25} {
		spec := &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "poisson", MaxIters: iters}
		_, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
		if !errors.HasCode(err, errors.PROCESSING_REGRESSION_NO_CONVERGE) {
			t.Errorf("max_iters %d: err = %v, want PROCESSING_REGRESSION_NO_CONVERGE", iters, err)
		}
	}
}

// TestFamilies_MuStart pins R's per-row starting means.
func TestFamilies_MuStart(t *testing.T) {
	t.Parallel()

	cases := []struct {
		fam  glmFamily
		y, w float64
		want float64
	}{
		{binomialLogit, 1, 1, 0.75},
		{binomialLogit, 0, 1, 0.25},
		{binomialLogit, 1, 3, 3.5 / 4},
		{binomialLogit, 0, 0.5, 0.5 / 1.5},
		{poissonLog, 0, 1, 0.1},
		{poissonLog, 7, 2, 7.1},
		{gammaInverse, 2.5, 1, 2.5},
	}
	for _, c := range cases {
		if got := c.fam.MuStart(c.y, c.w); got != c.want {
			t.Errorf("%s MuStart(%v, %v) = %v, want %v", c.fam.Name, c.y, c.w, got, c.want)
		}
	}
}

// overshootPenalty is a test penalty hook with no penalty: its solve
// takes the Cholesky step and then overshoots it by factor f — a step
// the deviance rejects, so only step-halving can bring the fit home.
type overshootPenalty struct{ f float64 }

func (o overshootPenalty) solve(k int, xtwx, xtwz, beta []float64) ([]float64, bool) {
	chol, ok := factorSPD(k, xtwx)
	if !ok {
		return nil, false
	}
	next, err := solveSPD(chol, xtwz)
	if err != nil {
		return nil, false
	}
	if beta != nil {
		for a := range next {
			next[a] = beta[a] + o.f*(next[a]-beta[a])
		}
	}
	return next, true
}

func (overshootPenalty) value([]float64) float64 { return 0 }

// nanPenalty makes every candidate after the start non-finite.
type nanPenalty struct{}

func (nanPenalty) solve(k int, xtwx, xtwz, beta []float64) ([]float64, bool) {
	return overshootPenalty{f: 1}.solve(k, xtwx, xtwz, beta)
}

func (nanPenalty) value([]float64) float64 { return math.NaN() }

// small2x2 is a 2 × 2 binomial design: 10 / 50 events at x = 0, 25 / 30
// at x = 1 (MLE β₀ = log(10/40), β₁ = log(25/5) − β₀).
func small2x2() (x, y []float64) {
	add := func(xv, yv float64, n int) {
		for i := 0; i < n; i++ {
			x = append(x, 1, xv)
			y = append(y, yv)
		}
	}
	add(0, 0, 40)
	add(0, 1, 10)
	add(1, 0, 5)
	add(1, 1, 25)
	return x, y
}

// TestIRLS_StepHalving_RestoresDescent: a step that overshoots 4× would
// raise the deviance; the loop halves it back toward the previous β
// until the deviance decreases, so every accepted objective is no higher
// than the last (within the round-off allowance) and the fit still
// reaches the MLE.
func TestIRLS_StepHalving_RestoresDescent(t *testing.T) {
	t.Parallel()

	x, y := small2x2()
	pr := &irlsProblem{X: x, Y: y, K: 2, Family: binomialLogit, Penalty: overshootPenalty{f: 4}, Tol: 1e-4}
	prev, halvings := math.Inf(1), 0
	pr.onStep = func(iter int, obj float64, h int) {
		halvings += h
		if iter > 0 && obj-prev > stepHalvingTol*(math.Abs(obj)+0.1) {
			t.Errorf("iteration %d raised the objective %.17g → %.17g", iter, prev, obj)
		}
		prev = obj
	}
	sol, err := irlsFit(pr)
	if err != nil {
		t.Fatalf("irlsFit: %v", err)
	}
	if halvings == 0 {
		t.Fatal("no step was halved; the overshoot fixture no longer exercises step-halving")
	}
	want := []float64{math.Log(10.0 / 40), math.Log(25.0/5) - math.Log(10.0/40)}
	for a, w := range want {
		if math.Abs(sol.Beta[a]-w) > 1e-4 {
			t.Errorf("β[%d] = %.10g, want the MLE %.10g", a, sol.Beta[a], w)
		}
	}
}

// TestIRLS_StepHalving_ExhaustionIsNoConverge: when no halving can
// produce an acceptable step, the fit stops after maxStepHalvings with
// PROCESSING_REGRESSION_NO_CONVERGE (reason step_halving_exhausted).
func TestIRLS_StepHalving_ExhaustionIsNoConverge(t *testing.T) {
	t.Parallel()

	x, y := small2x2()
	pr := &irlsProblem{X: x, Y: y, K: 2, Family: binomialLogit, Penalty: nanPenalty{}}
	_, err := irlsFit(pr)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_REGRESSION_NO_CONVERGE {
		t.Fatalf("err = %v, want PROCESSING_REGRESSION_NO_CONVERGE", err)
	}
	if ce.Details["reason"] != "step_halving_exhausted" {
		t.Errorf("details.reason = %v, want step_halving_exhausted", ce.Details["reason"])
	}
}

// TestGrowingPredictors pins the "keeps growing" half of the separation
// rule on synthetic β histories (column 0, the intercept, is never
// named): strictly rising |β| with a fixed sign over the last three
// accepted iterations, its last increment at least half the one before.
func TestGrowingPredictors(t *testing.T) {
	t.Parallel()

	h := func(rows ...[]float64) [][]float64 { return rows }
	cases := []struct {
		name    string
		history [][]float64
		want    []int
	}{
		{"constant climb", h([]float64{9, 1}, []float64{10, 2}, []float64{11, 3}, []float64{12, 4}), []int{1}},
		{"negative climb", h([]float64{0, -1}, []float64{0, -2}, []float64{0, -3.1}, []float64{0, -4.2}), []int{1}},
		{"intercept only grows", h([]float64{1, 2}, []float64{2, 2}, []float64{3, 2}, []float64{4, 2}), nil},
		{"converging (shrinking increments)", h([]float64{0, 1}, []float64{0, 2}, []float64{0, 2.5}, []float64{0, 2.6}), nil},
		{"sign flip", h([]float64{0, -0.5}, []float64{0, 1}, []float64{0, 2}, []float64{0, 3}), nil},
		{"too short", h([]float64{0, 1}, []float64{0, 2}, []float64{0, 3}), nil},
		{"two columns, one climbing", h([]float64{0, 1, 5}, []float64{0, 2, 5.5}, []float64{0, 3, 5.2}, []float64{0, 4, 5.1}), []int{1}},
	}
	for _, c := range cases {
		k := len(c.history[0])
		if got := growingPredictors(c.history, k); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: growingPredictors = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestRegGLM_SeparationNeedsBoundaryEvidence: a quasi-separated fit
// stopped after five iterations has a steadily climbing slope but no
// fitted probability near 0 or 1 yet — it stays NO_CONVERGE; the same
// fit run on refuses SEPARATION.
func TestRegGLM_SeparationNeedsBoundaryEvidence(t *testing.T) {
	t.Parallel()

	var recs []Record
	for _, cell := range []struct{ x, y, n float64 }{{0, 0, 50}, {0, 1, 10}, {1, 1, 25}} {
		for i := 0; i < int(cell.n); i++ {
			recs = append(recs, mockRecord{values: map[string]float64{"x": cell.x, "y": cell.y}})
		}
	}
	for iters, want := range map[int]errors.Code{5: errors.PROCESSING_REGRESSION_NO_CONVERGE, 50: errors.PROCESSING_REGRESSION_SEPARATION} {
		spec := &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "binomial", MaxIters: iters}
		_, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
		if !errors.HasCode(err, want) {
			t.Errorf("max_iters %d: err = %v, want %s", iters, err, want)
		}
	}
}

// TestIRLS_ValidEta: a gamma / inverse candidate with η ≤ 0 (μ = 1/η not
// positive) is not a usable step and is halved like a deviance rise;
// the canonical logit / log links take every finite η.
func TestIRLS_ValidEta(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		fam  glmFamily
		eta  float64
		want bool
	}{
		{gammaInverse, 0.5, true},
		{gammaInverse, 0, false},
		{gammaInverse, -0.2, false},
		{binomialLogit, -40, true},
		{poissonLog, -40, true},
		{binomialLogit, math.Inf(1), false},
	} {
		pr := &irlsProblem{Family: c.fam}
		if got := pr.valid(1, []float64{c.eta}); got != c.want {
			t.Errorf("%s valid(η = %v) = %v, want %v", c.fam.Name, c.eta, got, c.want)
		}
	}
	if (&irlsProblem{Family: binomialLogit}).valid(math.NaN(), []float64{0}) {
		t.Error("a NaN objective is valid; want invalid")
	}
}
