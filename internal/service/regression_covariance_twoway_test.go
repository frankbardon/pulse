package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// regression_covariance_twoway_test.go pins two-way clustering (U69
// E4-S2, #401) — covariance cluster + cluster2, Cameron–Gelbach–Miller
// V₁ + V₂ − V₁∩₂ — on REG_OLS, REG_GLM (binomial, poisson) and
// REG_BAYES_LINEAR against robust_vcov_panel.json's cluster2_cases
// (scripts/reference/gen_robust_vcov_panel.R: sandwich::vcovCL(cluster =
// ~geo + week / month)), including the fits whose difference meat leaves
// a variance negative (kept in vcov, its standard error null); plus the
// refusals' predict / runtime parity and the one-way echo's bytes.

// twoWaySpec is c's spec, its clusters keyed by first / second.
func twoWaySpec(c twoWayCase, first, second string) *types.RegressionSpec {
	s := &types.RegressionSpec{Name: "fit", Target: c.Target, Predictors: panelPredictors, Vcov: true,
		Covariance: &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: first, Cluster2: second}}
	switch c.Family {
	case "ols":
		s.Type = types.REG_OLS
	case "bayes":
		s.Type = types.REG_BAYES_LINEAR
	default:
		s.Type, s.Family = types.REG_GLM, c.Family
	}
	switch c.Weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability})
	}
	return s
}

// TestRegressionCovariancePanel_TwoWayMatchesROracle: every two-way case
// — coefficients, standard errors, p-values (OLS t on min(G₁, G₂) − 1
// df; GLM Wald z; Bayes none), 95% intervals and the full Vcov match
// vcovCL; a variance R leaves negative stays negative in Vcov and its
// standard error, p-value and interval are NaN (null on the wire);
// √diag(Vcov) is the standard error bit for bit wherever it is defined;
// the echo names both fields and their level counts; keying geo by its
// integer id is the same fit to the bit, and swapping the two fields
// the same fit within rounding.
func TestRegressionCovariancePanel_TwoWayMatchesROracle(t *testing.T) {
	t.Parallel()

	g := loadPanel(t)
	svc, _ := panelCohort(t, g.Rows)
	keys := append([]string{"(intercept)"}, panelPredictors...)
	negSeen := 0
	for _, c := range g.Cluster2Cases {
		t.Run(fmt.Sprintf("%s/%s/%s", c.Family, c.Weight, c.Cluster2), func(t *testing.T) {
			res := processPanel(t, svc, twoWaySpec(c, "geo", c.Cluster2), twoWaySpec(c, "geo_id", c.Cluster2), twoWaySpec(c, c.Cluster2, "geo"))
			r := res[0]
			check := func(what string, got map[string]float64, want []*float64) {
				for i, k := range keys {
					switch {
					case want[i] == nil:
						if !math.IsNaN(got[k]) {
							t.Errorf("%s[%s] = %v, R null (negative variance)", what, k, got[k])
						}
					case !vcovNear(got[k], *want[i], panelTol):
						t.Errorf("%s[%s] = %.17g, R = %.17g", what, k, got[k], *want[i])
					}
				}
			}
			for i, k := range keys {
				if !vcovNear(r.Coefficients[k], c.Coeffs[i], panelTol) {
					t.Errorf("coefficients[%s] = %.17g, R = %.17g", k, r.Coefficients[k], c.Coeffs[i])
				}
			}
			check("std_errors", r.StdErrors, c.StdErrors)
			check("ci_lower", r.CILower, c.CILower)
			check("ci_upper", r.CIUpper, c.CIUpper)
			if c.Family == "bayes" {
				if r.PValues != nil {
					t.Errorf("bayes reports p-values %v", r.PValues)
				}
			} else {
				check("p_values", r.PValues, c.PValues)
			}
			if c.Family == "ols" && r.DF != c.DF {
				t.Errorf("df = %v, R = %v (min(G₁, G₂) − 1)", r.DF, c.DF)
			}
			if r.Vcov == nil {
				t.Fatal("vcov absent")
			}
			neg := 0
			for i := range keys {
				for j := range keys {
					if !vcovNear(r.Vcov.Values[i][j], c.Vcov[i][j], panelTol) {
						t.Errorf("vcov[%s][%s] = %.17g, R = %.17g", keys[i], keys[j], r.Vcov.Values[i][j], c.Vcov[i][j])
					}
				}
				vii, se := r.Vcov.Values[i][i], r.StdErrors[keys[i]]
				if vii < 0 {
					neg++
					if !math.IsNaN(se) {
						t.Errorf("negative variance %v of %s, std_error %v (want NaN)", vii, keys[i], se)
					}
				} else if math.Sqrt(vii) != se {
					t.Errorf("√vcov[%s] = %.17g, std_error %.17g (must be exact)", keys[i], math.Sqrt(vii), se)
				}
			}
			if neg != c.NegVariances {
				t.Errorf("%d negative variances, R %d", neg, c.NegVariances)
			}
			negSeen += neg
			echo := r.Covariance
			if echo == nil || echo.Type != types.CovarianceCluster || echo.Cluster != "geo" || echo.Clusters != c.Clusters ||
				echo.Cluster2 != c.Cluster2 || echo.Clusters2 != c.Clusters2 || echo.OrderBy != "" || echo.Periods != 0 {
				t.Errorf("covariance echo %+v, want geo (%d) × %s (%d)", echo, c.Clusters, c.Cluster2, c.Clusters2)
			}
			for _, k := range keys {
				if a, b := res[1].StdErrors[k], r.StdErrors[k]; a != b && !(math.IsNaN(a) && math.IsNaN(b)) {
					t.Errorf("geo_id SE[%s] = %v, by geo %v", k, a, b)
				}
				if a, b := res[2].StdErrors[k], r.StdErrors[k]; !(math.IsNaN(a) && math.IsNaN(b)) && !vcovNear(a, b, panelTol) {
					t.Errorf("swapped fields SE[%s] = %v, %v", k, a, b)
				}
			}
		})
	}
	if negSeen == 0 {
		t.Error("no case exercised a negative two-way variance")
	}
}

// TestRegressionCovariancePanel_TwoWayWire: a negative two-way variance
// reaches the wire as a null standard error, p-value and interval (the
// key kept) beside the negative vcov diagonal; a one-way cluster echo
// carries no cluster2 / clusters2 key (byte-identical to before
// cluster2).
func TestRegressionCovariancePanel_TwoWayWire(t *testing.T) {
	t.Parallel()

	g := loadPanel(t)
	svc, _ := panelCohort(t, g.Rows)
	var neg *twoWayCase
	for i, c := range g.Cluster2Cases {
		if c.Family == "binomial" && c.NegVariances > 0 {
			neg = &g.Cluster2Cases[i]
			break
		}
	}
	if neg == nil {
		t.Fatal("golden has no negative-variance binomial case")
	}
	one := twoWaySpec(*neg, "geo", "")
	res := processPanel(t, svc, twoWaySpec(*neg, "geo", neg.Cluster2), one)
	raw, err := json.Marshal(res[0])
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		StdErrors map[string]*float64 `json:"std_errors"`
		PValues   map[string]*float64 `json:"p_values"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	nulls := 0
	for k, se := range wire.StdErrors {
		if se == nil {
			nulls++
			if p, ok := wire.PValues[k]; !ok || p != nil {
				t.Errorf("p_values[%s] = %v (present %v), want null beside the null SE", k, p, ok)
			}
		}
	}
	if nulls != neg.NegVariances {
		t.Errorf("%d null std_errors on the wire, want %d: %s", nulls, neg.NegVariances, raw)
	}
	echo, err := json.Marshal(res[1].Covariance)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(echo), "cluster2") || strings.Contains(string(echo), "clusters2") {
		t.Errorf("one-way cluster echo carries a second cluster: %s", echo)
	}
}

// TestRegressionCovariancePanel_TwoWayRefusals: the two-way shape and
// type refusals fire in predict and the runtime alike
// (internal/regcov.Check); a second cluster field of a non-cluster type
// is predict's (SERVICE_VALIDATION), the same as the first.
func TestRegressionCovariancePanel_TwoWayRefusals(t *testing.T) {
	t.Parallel()

	g := loadPanel(t)
	svc, data := panelCohort(t, g.Rows)
	cases := []struct {
		name    string
		code    errors.Code
		reason  string
		runtime bool
		cov     *types.RegressionCovariance
		regType types.RegressionType
	}{
		{"cluster2 without cluster", errors.PROCESSING_CONFIG, "", true, &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster2: "week"}, types.REG_OLS},
		{"cluster2 on hc1", errors.PROCESSING_CONFIG, "", true, &types.RegressionCovariance{Type: types.CovarianceHC1, Cluster2: "week"}, types.REG_GLM},
		{"cluster2 is cluster", errors.PROCESSING_CONFIG, "", true, &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "geo", Cluster2: "geo"}, types.REG_BAYES_LINEAR},
		{"two-way on multinomial", errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "type", true, &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "month", Cluster2: "week"}, types.REG_MULTINOMIAL},
		{"float cluster2", errors.SERVICE_VALIDATION, "", false, &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "geo", Cluster2: "x1"}, types.REG_OLS},
		{"unknown cluster2", errors.SERVICE_VALIDATION, "", false, &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "geo", Cluster2: "nope"}, types.REG_GLM},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := &types.RegressionSpec{Name: "fit", Type: c.regType, Target: "y", Predictors: panelPredictors, Covariance: c.cov}
			switch c.regType {
			case types.REG_GLM:
				spec.Family, spec.Target = "poisson", "y_count"
			case types.REG_MULTINOMIAL:
				spec.Target = "geo"
			}
			req := &types.Request{Cohort: &types.Cohort{Filename: "panel.pulse"}, Regressions: []*types.RegressionSpec{spec}}
			if c.runtime {
				_, err := svc.Process(context.Background(), req)
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != c.code {
					t.Fatalf("Process err = %v, want %s", err, c.code)
				}
				if c.reason != "" && ce.Details["reason"] != c.reason {
					t.Errorf("details.reason = %v, want %s", ce.Details["reason"], c.reason)
				}
			}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			found := false
			for _, e := range pred.Errors {
				if e.Code == string(c.code) && (c.reason == "" || e.Details["reason"] == c.reason) {
					found = true
				}
			}
			if !found {
				t.Errorf("predict misses %s (errors %+v)", c.code, pred.Errors)
			}
		})
	}
}

// TestRegressionCovariancePanel_TwoWayProjectedDecode: under projected
// buffered decode the second cluster field is decoded
// (processing.NeededFields lists covariance.cluster2), so a two-way fit
// is the full-decode fit to the bit.
func TestRegressionCovariancePanel_TwoWayProjectedDecode(t *testing.T) {
	t.Parallel()

	g := loadPanel(t)
	full, _ := panelCohort(t, g.Rows)
	projected, _ := panelCohort(t, g.Rows)
	projected.SetProjectBufferedFields(true)
	for _, c := range g.Cluster2Cases {
		if c.Weight != "none" || c.Family != "ols" {
			continue
		}
		want := processPanel(t, full, twoWaySpec(c, "geo", c.Cluster2))[0]
		got := processPanel(t, projected, twoWaySpec(c, "geo", c.Cluster2))[0]
		for k, se := range want.StdErrors {
			if got.StdErrors[k] != se {
				t.Errorf("%s: projected SE[%s] = %v, full %v", c.Cluster2, k, got.StdErrors[k], se)
			}
		}
		if got.Covariance == nil || got.Covariance.Clusters2 != c.Clusters2 {
			t.Errorf("%s: projected echo %+v", c.Cluster2, got.Covariance)
		}
	}
}
