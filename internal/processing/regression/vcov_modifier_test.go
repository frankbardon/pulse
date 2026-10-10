package regression

import (
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"gonum.org/v1/gonum/mat"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// vcov_modifier_test.go pins RegressionSpec.Vcov under the Resample and
// Selection modifiers: the matrix is the covariance the modifier's
// standard errors are the square roots of the diagonal of — the
// replicate covariance for a resample, the final refit's covariance for
// a selection — and lasso / elastic net stay refused under either.

// fitBase is the unmodified OLS fit on preds with Vcov on: the base
// result a resample driver wraps.
func fitBase(t *testing.T, records []Record, preds []string) *types.RegressionResult {
	t.Helper()
	return runOLSWithModifier(t, &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: preds, Vcov: true}, records)
}

// recordingFitter wraps olsFitter, failing every failEvery-th call (0 =
// never) and recording each SUCCESSFUL replicate (intercept, slopes…).
func recordingFitter(preds []string, spec *types.RegressionSpec, failEvery int, reps *[][]float64) func([]Record) (*refitResult, error) {
	calls := 0
	return func(recs []Record) (*refitResult, error) {
		calls++
		if failEvery > 0 && calls%failEvery == 0 {
			return nil, errors.NewCodedError(errors.PROCESSING_REGRESSION_RANK_DEFICIENT, "injected replicate failure")
		}
		fit, err := olsFitter(recs, "y", preds, spec)
		if err != nil {
			return nil, err
		}
		*reps = append(*reps, append([]float64{fit.Intercept}, fit.Slopes...))
		return fit, nil
	}
}

// handCov is the covariance of the replicate rows, written out with no
// shared code: Σ (r − r̄)(r − r̄)ᵀ, then finish.
func handCov(reps [][]float64, finish func(float64) float64) [][]float64 {
	q := len(reps[0])
	mean := make([]float64, q)
	for _, r := range reps {
		for j, v := range r {
			mean[j] += v / float64(len(reps))
		}
	}
	out := make([][]float64, q)
	for i := range out {
		out[i] = make([]float64, q)
		for j := range out[i] {
			s := 0.0
			for _, r := range reps {
				s += (r[i] - mean[i]) * (r[j] - mean[j])
			}
			out[i][j] = finish(s)
		}
	}
	return out
}

func relNear(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol*math.Max(1, math.Abs(want))
}

// assertVcovDescribesSE: keys (intercept) + preds, exact symmetry,
// √diag == StdErrors exactly and diag == SE² to 1e-12, PSD.
func assertVcovDescribesSE(t *testing.T, res *types.RegressionResult, preds []string) {
	t.Helper()
	if res.Vcov == nil || res.Correlation == nil {
		t.Fatalf("vcov / correlation absent: %+v", res)
	}
	keys := append([]string{InterceptKey}, preds...)
	if !reflect.DeepEqual(res.Vcov.RowKeys, keys) || !reflect.DeepEqual(res.Vcov.ColumnKeys, keys) {
		t.Fatalf("vcov keys %v / %v, want %v", res.Vcov.RowKeys, res.Vcov.ColumnKeys, keys)
	}
	v := res.Vcov.Values
	q := len(keys)
	sym := mat.NewSymDense(q, nil)
	for i := 0; i < q; i++ {
		se := res.StdErrors[keys[i]]
		if math.Sqrt(v[i][i]) != se {
			t.Errorf("√vcov[%s] = %.17g, std_error %.17g (must be exact)", keys[i], math.Sqrt(v[i][i]), se)
		}
		if !relNear(v[i][i], se*se, 1e-12) {
			t.Errorf("vcov[%s][%s] = %.17g, SE² = %.17g", keys[i], keys[i], v[i][i], se*se)
		}
		if res.Correlation.Values[i][i] != 1 {
			t.Errorf("correlation diagonal [%s] = %v", keys[i], res.Correlation.Values[i][i])
		}
		for j := 0; j < q; j++ {
			if v[i][j] != v[j][i] {
				t.Errorf("vcov[%d][%d] not exactly symmetric", i, j)
			}
			sym.SetSym(i, j, v[i][j])
		}
	}
	var eig mat.EigenSym
	if !eig.Factorize(sym, false) {
		t.Fatal("eigen factorization failed")
	}
	vals := eig.Values(nil)
	top := vals[len(vals)-1]
	for _, l := range vals {
		if l < -1e-12*top {
			t.Errorf("vcov not PSD: eigenvalue %g (largest %g)", l, top)
		}
	}
}

// TestVcov_Bootstrap_ReplicateCovariance: the bootstrap vcov is the
// sample covariance (B − 1 divisor) of exactly the replicates the SE
// sums over — a failed replicate is skipped by both — so its diagonal
// is SE², its off-diagonal is the replicate covariance computed by
// hand, and it is symmetric and PSD.
func TestVcov_Bootstrap_ReplicateCovariance(t *testing.T) {
	records := makeLinearRecords(t, 60, []float64{1.5, -0.7, 0.3}, 2, 1, 11)
	preds := predictorNames(3)
	base := fitBase(t, records, preds)
	spec := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: preds, Resample: "bootstrap", RNGSeed: 42, Vcov: true}
	var reps [][]float64
	res, err := runBootstrap(spec, records, preds, base, recordingFitter(preds, spec, 7, &reps), waldZTwoSidedP)
	if err != nil {
		t.Fatalf("runBootstrap: %v", err)
	}
	if len(reps) != defaultBootstrapIters-defaultBootstrapIters/7 {
		t.Fatalf("recorded %d successful replicates", len(reps))
	}
	assertVcovDescribesSE(t, res, preds)
	div := float64(len(reps) - 1)
	want := handCov(reps, func(s float64) float64 { return s / div })
	for i := range want {
		for j := range want[i] {
			if !relNear(res.Vcov.Values[i][j], want[i][j], 1e-12) {
				t.Errorf("vcov[%d][%d] = %.17g, hand replicate covariance %.17g", i, j, res.Vcov.Values[i][j], want[i][j])
			}
		}
	}
	if reflect.DeepEqual(res.Vcov.Values, base.Vcov.Values) {
		t.Error("bootstrap vcov is the analytical one; it must be the replicate covariance")
	}
}

// TestVcov_Jackknife_ReplicateCovariance: (n − 1)/n · Σ (β₋ᵢ − β̄)(β₋ᵢ −
// β̄)ᵀ over the leave-one-out replicates, computed by hand.
func TestVcov_Jackknife_ReplicateCovariance(t *testing.T) {
	records := makeLinearRecords(t, 40, []float64{1.5, -0.7}, 2, 1, 12)
	preds := predictorNames(2)
	base := fitBase(t, records, preds)
	spec := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: preds, Resample: "jackknife", Vcov: true}
	var reps [][]float64
	res, err := runJackknife(spec, records, preds, base, recordingFitter(preds, spec, 0, &reps), waldZTwoSidedP)
	if err != nil {
		t.Fatalf("runJackknife: %v", err)
	}
	assertVcovDescribesSE(t, res, preds)
	n := float64(len(reps))
	want := handCov(reps, func(s float64) float64 { return (n - 1) / n * s })
	for i := range want {
		for j := range want[i] {
			if !relNear(res.Vcov.Values[i][j], want[i][j], 1e-12) {
				t.Errorf("vcov[%d][%d] = %.17g, hand jackknife covariance %.17g", i, j, res.Vcov.Values[i][j], want[i][j])
			}
		}
	}
}

// TestVcov_Bootstrap_Deterministic: a fixed seed reproduces the vcov
// bit for bit, and switching vcov on moves no other figure.
func TestVcov_Bootstrap_Deterministic(t *testing.T) {
	records := makeLinearRecords(t, 50, []float64{1, 2}, 0.5, 1, 13)
	preds := predictorNames(2)
	spec := func(vcov bool) *types.RegressionSpec {
		return &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: preds, Resample: "bootstrap", RNGSeed: 99, Vcov: vcov}
	}
	a := runOLSWithModifier(t, spec(true), records)
	b := runOLSWithModifier(t, spec(true), records)
	assertVcovDescribesSE(t, a, preds)
	if !reflect.DeepEqual(a.Vcov, b.Vcov) || !reflect.DeepEqual(a.Correlation, b.Correlation) {
		t.Error("same seed, different vcov")
	}
	off := runOLSWithModifier(t, spec(false), records)
	if off.Vcov != nil || off.Correlation != nil {
		t.Fatal("vcov emitted without opt-in")
	}
	stripped := *a
	stripped.Vcov, stripped.Correlation = nil, nil
	if !reflect.DeepEqual(off, &stripped) {
		t.Errorf("vcov moved the fit:\n off %+v\n on  %+v", off, &stripped)
	}
}

// TestVcov_SelectionAndResample: a Selection fit reports the final
// refit's covariance keyed (intercept) + the selected predictors; with
// Resample on top the vcov follows the resample SEs over that subset.
func TestVcov_SelectionAndResample(t *testing.T) {
	records := makeSelectionFixture(t, 200, 5)
	sel := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2", "x3", "x4", "x5"}, Selection: "forward", Criterion: "bic", Vcov: true}
	res := runOLSWithModifier(t, sel, records)
	if len(res.SelectedFeatures) == 0 || len(res.SelectedFeatures) == len(sel.Predictors) {
		t.Fatalf("fixture selected %v; want a strict, non-empty subset", res.SelectedFeatures)
	}
	assertVcovDescribesSE(t, res, res.SelectedFeatures)
	final := fitBase(t, records, res.SelectedFeatures)
	if !reflect.DeepEqual(res.Vcov, final.Vcov) {
		t.Errorf("selection vcov is not the final refit's:\n got  %v\n want %v", res.Vcov.Values, final.Vcov.Values)
	}

	both := *sel
	both.Resample = "jackknife"
	rb := runOLSWithModifier(t, &both, records)
	assertVcovDescribesSE(t, rb, rb.SelectedFeatures)
	if reflect.DeepEqual(rb.Vcov.Values, res.Vcov.Values) {
		t.Error("selection + jackknife vcov is the analytical one; it must follow the jackknife SEs")
	}
}

// TestVcov_InterceptOnlySelection: a selection that keeps no predictor
// still answers vcov — the 1×1 variance of β₀ = ȳ its SE is the root of.
func TestVcov_InterceptOnlySelection(t *testing.T) {
	records := makeLinearRecords(t, 30, []float64{0}, 3, 1, 14)
	spec := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}, Selection: "forward", Criterion: "bic", Vcov: true}
	e := &modifierEngine{spec: spec, family: "OLS"}
	res, err := e.fitOLSInterceptOnly(records)
	if err != nil {
		t.Fatal(err)
	}
	assertVcovDescribesSE(t, res, nil)
	if res.StdErrors[InterceptKey] <= 0 {
		t.Fatalf("intercept SE %v", res.StdErrors[InterceptKey])
	}
}

// TestVcov_ModifierRefusals: Resample and Selection no longer refuse
// vcov; lasso / elastic net still do, under either modifier too.
func TestVcov_ModifierRefusals(t *testing.T) {
	base := types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1"}, Vcov: true}
	cases := []struct {
		name   string
		mut    func(*types.RegressionSpec)
		refuse bool
	}{
		{"resample", func(s *types.RegressionSpec) { s.Resample = "bootstrap" }, false},
		{"selection", func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" }, false},
		{"glm-selection", func(s *types.RegressionSpec) {
			s.Type, s.Family, s.Selection, s.Criterion = types.REG_GLM, "poisson", "backward", "aic"
		}, false},
		{"lasso+resample", func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Resample = "l1", 0.1, "jackknife" }, true},
		{"elasticnet+selection", func(s *types.RegressionSpec) {
			s.Penalty, s.Alpha, s.L1Ratio, s.Selection, s.Criterion = "elasticnet", 0.1, 0.5, "forward", "aic"
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base
			c.mut(&s)
			err := validateVcov(&s)
			var ce *errors.CodedError
			switch {
			case c.refuse && (!stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED || ce.Details["reason"] != "penalty"):
				t.Errorf("err = %v, want VCOV_UNSUPPORTED reason penalty", err)
			case !c.refuse && err != nil:
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}
