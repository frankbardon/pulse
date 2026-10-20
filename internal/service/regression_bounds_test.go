package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
)

// regression_bounds_test.go: REG_OLS bounds.nonnegative end to end —
// predict and Process refuse the same requests with the same code,
// message and details (internal/regbounds, internal/regcov), and a fit
// whose bound binds carries PULSE_ADVISORY_CONSTRAINT_ACTIVE on its
// result: never a warning, never escalated by Strict, dropped by
// suppression, absent under resample and on a fit whose bound does not
// bind. Result-only: predict never raises it (binding is data-dependent).

func boundsFixture(t *testing.T) (*fs.Config, string, []byte) {
	t.Helper()
	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	cfg := fixtureFS(t, dir)
	cohort := filepath.Join(dir, "mtcars.pulse")
	data, err := os.ReadFile(cohort)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, cohort, data
}

func boundedOLS(mut func(*types.RegressionSpec)) *types.RegressionSpec {
	s := &types.RegressionSpec{
		Name: "ols", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt", "hp", "qsec"},
		Bounds: &types.RegressionBounds{Nonnegative: []string{"wt"}},
	}
	if mut != nil {
		mut(s)
	}
	return s
}

func TestRegBounds_PredictRuntimeParity(t *testing.T) {
	cfg, cohort, data := boundsFixture(t)
	nn := func(names ...string) *types.RegressionBounds { return &types.RegressionBounds{Nonnegative: names} }
	cases := []struct {
		name string
		spec *types.RegressionSpec
		code errors.Code
	}{
		{"unknown predictor", boundedOLS(func(s *types.RegressionSpec) { s.Bounds = nn("cyl") }), errors.PROCESSING_CONFIG},
		{"target named", boundedOLS(func(s *types.RegressionSpec) { s.Bounds = nn("mpg") }), errors.PROCESSING_CONFIG},
		{"listed twice", boundedOLS(func(s *types.RegressionSpec) { s.Bounds = nn("wt", "wt") }), errors.PROCESSING_CONFIG},
		{"ridge", boundedOLS(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 }), errors.PROCESSING_CONFIG},
		{"lasso", boundedOLS(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l1", 0.1 }), errors.PROCESSING_CONFIG},
		{"selection", boundedOLS(func(s *types.RegressionSpec) { s.Selection, s.Criterion = "backward", "aic" }), errors.PROCESSING_CONFIG},
		{"glm", boundedOLS(func(s *types.RegressionSpec) {
			s.Type, s.Family, s.Target, s.Predictors = types.REG_GLM, "binomial", "am", []string{"wt", "hp"}
		}), errors.PROCESSING_CONFIG},
		{"bayes", boundedOLS(func(s *types.RegressionSpec) { s.Type = types.REG_BAYES_LINEAR }), errors.PROCESSING_CONFIG},
		{"robust covariance", boundedOLS(func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
		}), errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{c.spec}}
			_, err := New(cfg).Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("Process err = %v, want %s", err, c.code)
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

func TestRegBounds_ConstraintActiveAdvisory(t *testing.T) {
	cfg, cohort, data := boundsFixture(t)
	code := string(errors.PULSE_ADVISORY_CONSTRAINT_ACTIVE)
	run := func(t *testing.T, svc *Service, specs ...*types.RegressionSpec) *types.Response {
		t.Helper()
		req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: specs}
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		for _, w := range resp.Warnings {
			if w.Code == code {
				t.Errorf("advisory surfaced as a warning: %+v", w)
			}
		}
		// Result-only: predict validates the request and raises nothing.
		env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{Strict: true})
		for _, e := range env.Errors {
			if e.Code == code {
				t.Errorf("Strict predict escalated the advisory: %+v", *e)
			}
		}
		if lax := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{}); len(lax.Errors) != 0 {
			t.Errorf("predict errors %+v", *lax.Errors[0])
		}
		for _, a := range env.Data.(*descriptor.PredictResult).Advisories {
			if a.Code == code {
				t.Errorf("predict raised the result-only advisory %+v", a)
			}
		}
		return resp
	}

	for _, strict := range []bool{false, true} {
		svc := New(cfg)
		svc.SetStrict(strict)
		resp := run(t, svc,
			boundedOLS(nil), // wt's unconstrained slope is negative: binds
			boundedOLS(func(s *types.RegressionSpec) { s.Name, s.Bounds.Nonnegative = "free", []string{"qsec"} }),
			boundedOLS(func(s *types.RegressionSpec) { s.Name, s.Resample, s.BootstrapIters = "boot", "bootstrap", 100 }),
		)
		bound, free, boot := resp.Regressions[0], resp.Regressions[1], resp.Regressions[2]
		if len(bound.Advisories) != 1 || bound.Advisories[0].Code != code {
			t.Fatalf("strict %v: binding fit advisories %+v", strict, bound.Advisories)
		}
		d := bound.Advisories[0].Details
		if d["slot"] != "regressions[0]" || !reflect.DeepEqual(d["active"], []string{"wt"}) ||
			!reflect.DeepEqual(d["suggested"], map[string]any{"resample": "bootstrap"}) {
			t.Errorf("advisory details %v", d)
		}
		if bound.Coefficients["wt"] != 0 {
			t.Errorf("β(wt) = %v, want held at 0", bound.Coefficients["wt"])
		}
		for key, se := range bound.StdErrors {
			if !math.IsNaN(se) || !math.IsNaN(bound.PValues[key]) {
				t.Errorf("binding %s: se %v p %v, want null", key, se, bound.PValues[key])
			}
		}
		if len(free.Advisories) != 0 || len(boot.Advisories) != 0 {
			t.Errorf("non-binding %+v / bootstrap %+v carry advisories", free.Advisories, boot.Advisories)
		}
		for key, se := range boot.StdErrors {
			if math.IsNaN(se) {
				t.Errorf("bootstrap SE(%s) null; the replicates replace the nulls", key)
			}
		}
	}

	// A bound that does not bind is the unbounded fit on the wire.
	resp := run(t, New(cfg),
		boundedOLS(func(s *types.RegressionSpec) { s.Bounds.Nonnegative, s.Vcov = []string{"qsec"}, true }),
		boundedOLS(func(s *types.RegressionSpec) { s.Bounds, s.Vcov = nil, true }),
	)
	a, _ := types.MarshalFinite(resp.Regressions[0])
	b, _ := types.MarshalFinite(resp.Regressions[1])
	if !bytes.Equal(a, b) {
		t.Errorf("non-binding bounded fit differs from plain OLS:\n%s\n%s", a, b)
	}

	// Suppressed on the instance: the note goes, the null figures stay.
	svc := New(cfg)
	svc.SetInstanceSnapshot((*descx.InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{code}))
	res := run(t, svc, boundedOLS(nil)).Regressions[0]
	if len(res.Advisories) != 0 || !math.IsNaN(res.StdErrors["wt"]) {
		t.Errorf("suppressed: advisories %+v, SE(wt) %v", res.Advisories, res.StdErrors["wt"])
	}
}

// TestRegBounds_WireRequestRuns: bounds arrives through a strict JSON
// request; a misspelled key inside it is refused by the strict decoder.
func TestRegBounds_WireRequestRuns(t *testing.T) {
	cfg, cohort, _ := boundsFixture(t)
	raw := `{"cohort":{"filename":` + jsonString(cohort) + `},"regressions":[
		{"name":"nn","type":"REG_OLS","target":"mpg","predictors":["wt","hp","qsec"],"bounds":{"nonnegative":["wt","qsec"]}}]}`
	var req types.Request
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	resp, err := New(cfg).Process(context.Background(), &req)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Regressions[0].Advisories; len(got) != 1 || !slices.Contains(got[0].Details["active"].([]string), "wt") {
		t.Errorf("advisories %+v", got)
	}
	bad := json.NewDecoder(bytes.NewReader([]byte(`{"type":"REG_OLS","bounds":{"nonnegativ":["wt"]}}`)))
	bad.DisallowUnknownFields()
	var spec types.RegressionSpec
	if err := bad.Decode(&spec); err == nil {
		t.Error("a misspelled bounds key decoded")
	}
}
