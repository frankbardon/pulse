package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// regression_solver_test.go: REG_OLS solver ("qr" | "normal") and
// intercept: false end to end — predict and Process refuse the same
// requests with the same code, message and details (internal/regsolve),
// the two solvers agree on a well-conditioned fit, and a fit through the
// origin pins the intercept to 0 with null inference.

func solverOLS(mut func(*types.RegressionSpec)) *types.RegressionSpec {
	s := &types.RegressionSpec{Name: "ols", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt", "hp", "qsec"}}
	if mut != nil {
		mut(s)
	}
	return s
}

func throughOrigin(s *types.RegressionSpec) {
	f := false
	s.Intercept = &f
}

func TestRegSolve_PredictRuntimeParity(t *testing.T) {
	t.Parallel()

	cfg, cohort, data := boundsFixture(t)
	cases := []struct {
		name string
		spec *types.RegressionSpec
	}{
		{"unknown solver", solverOLS(func(s *types.RegressionSpec) { s.Solver = "svd" })},
		{"solver with ridge", solverOLS(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Solver = "l2", 0.1, "qr" })},
		{"solver on glm", solverOLS(func(s *types.RegressionSpec) {
			s.Type, s.Family, s.Target, s.Predictors, s.Solver = types.REG_GLM, "binomial", "am", []string{"wt", "hp"}, "qr"
		})},
		{"through origin with lasso", solverOLS(func(s *types.RegressionSpec) { throughOrigin(s); s.Penalty, s.Alpha = "l1", 0.1 })},
		{"through origin with robust", solverOLS(func(s *types.RegressionSpec) {
			throughOrigin(s)
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
		})},
		{"through origin with normal", solverOLS(func(s *types.RegressionSpec) { throughOrigin(s); s.Solver = "normal" })},
		{"through origin on bayes", solverOLS(func(s *types.RegressionSpec) { throughOrigin(s); s.Type = types.REG_BAYES_LINEAR })},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{c.spec}}
			_, err := New(cfg).Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG {
				t.Fatalf("Process err = %v, want PROCESSING_CONFIG", err)
			}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			found := false
			for _, e := range pred.Errors {
				if e.Code == string(ce.Code) && e.Message == ce.Message && reflect.DeepEqual(e.Details, ce.Details) {
					found = true
				}
			}
			if !found {
				t.Errorf("predict errors %+v lack %s %q %v", pred.Errors, ce.Code, ce.Message, ce.Details)
			}
		})
	}
}

func fitOne(t *testing.T, spec *types.RegressionSpec) *types.RegressionResult {
	t.Helper()
	cfg, cohort, _ := boundsFixture(t)
	resp, err := New(cfg).Process(context.Background(), &types.Request{
		Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{spec},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		t.Fatalf("regressions %+v", resp.Regressions)
	}
	return resp.Regressions[0]
}

// TestRegSolve_SolversAgree: on mtcars (well conditioned) the QR and
// normal-equation solvers agree to 1e-10 on every figure, and an
// explicit "qr" is the default bit for bit.
func TestRegSolve_SolversAgree(t *testing.T) {
	t.Parallel()

	def := fitOne(t, solverOLS(nil))
	qr := fitOne(t, solverOLS(func(s *types.RegressionSpec) { s.Solver = "qr" }))
	normal := fitOne(t, solverOLS(func(s *types.RegressionSpec) { s.Solver = "normal" }))
	if !reflect.DeepEqual(def.Coefficients, qr.Coefficients) || !reflect.DeepEqual(def.StdErrors, qr.StdErrors) || def.R2 != qr.R2 {
		t.Errorf("solver qr differs from the default:\n%+v\n%+v", def, qr)
	}
	near := func(what string, a, b float64) {
		if math.Abs(a-b) > 1e-10*math.Max(math.Abs(a), math.Abs(b)) {
			t.Errorf("%s: qr %.17g, normal %.17g", what, a, b)
		}
	}
	for k, v := range def.Coefficients {
		near("coef "+k, v, normal.Coefficients[k])
		near("se "+k, def.StdErrors[k], normal.StdErrors[k])
	}
	near("r2", def.R2, normal.R2)
	near("residual_std_err", def.ResidualStdErr, normal.ResidualStdErr)
}

// TestRegSolve_ThroughOriginShape: intercept false keeps the
// "(intercept)" key at exactly 0 with null std_error, p-value and
// interval, reads df N − p, and a Vcov's intercept row and column are
// null while the slope block is finite.
func TestRegSolve_ThroughOriginShape(t *testing.T) {
	t.Parallel()

	res := fitOne(t, solverOLS(func(s *types.RegressionSpec) { throughOrigin(s); s.Vcov = true }))
	const key = "(intercept)"
	if c, ok := res.Coefficients[key]; !ok || c != 0 || math.Signbit(c) {
		t.Errorf("intercept coefficient = %v (present %v), want +0", c, ok)
	}
	for name, m := range map[string]map[string]float64{"std_errors": res.StdErrors, "p_values": res.PValues, "ci_lower": res.CILower, "ci_upper": res.CIUpper} {
		if v, ok := m[key]; !ok || !math.IsNaN(v) {
			t.Errorf("%s[%s] = %v (present %v), want NaN (null)", name, key, v, ok)
		}
	}
	if res.DF != float64(res.NObs-3) {
		t.Errorf("df = %v, want n − p = %d", res.DF, res.NObs-3)
	}
	for _, p := range []string{"wt", "hp", "qsec"} {
		if math.IsNaN(res.StdErrors[p]) || res.StdErrors[p] <= 0 {
			t.Errorf("std_errors[%s] = %v, want finite > 0", p, res.StdErrors[p])
		}
	}
	if res.Vcov == nil || len(res.Vcov.Values) != 4 {
		t.Fatalf("vcov = %+v, want 4×4", res.Vcov)
	}
	v, keys := res.Vcov.Values, res.Vcov.RowKeys
	for j := range v {
		if !math.IsNaN(v[0][j]) || !math.IsNaN(v[j][0]) {
			t.Errorf("vcov intercept row/col [%d] = %v / %v, want NaN", j, v[0][j], v[j][0])
		}
	}
	for i := 1; i < 4; i++ {
		se := res.StdErrors[keys[i]]
		if math.IsNaN(v[i][i]) || math.Abs(math.Sqrt(v[i][i])-se) > 1e-12*se {
			t.Errorf("vcov[%s] = %v does not square to std_error %v", keys[i], v[i][i], se)
		}
	}
}
