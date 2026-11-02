package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// compose_compare_oracle_test.go pins the Compose nested-model
// comparison (ComposedRequest.Compare, internal/regcompare) end to end —
// the two fits run by Compose, their figures, the statistic and its
// tail — to R 4.6.1: nested.json (scripts/reference/gen_nested.R, make
// reference) — anova(m0, m1[, test = "F" | "Chisq" | "LRT"]) on lm and
// the binomial logit / probit, poisson log, gamma inverse / log and
// negative binomial glm (theta estimated: MASS's anova.negbin; theta
// fixed with the dispersion fixed or estimated), and
// lmtest::waldtest(m0, m1, vcov = vcov | vcovHC(type = "HC3")), with a
// restricted fit through the origin and frequency-weighted cases fitted
// on the rep() expansion.

type nestedCase struct {
	Name                 string      `json:"name"`
	Test                 string      `json:"test"`
	N                    int         `json:"n"`
	Family               string      `json:"family"`
	Link                 string      `json:"link"`
	Theta                float64     `json:"theta"`
	Dispersion           string      `json:"dispersion"`
	Covariance           string      `json:"covariance"`
	Weight               string      `json:"weight"`
	Restricted           []string    `json:"restricted"`
	Full                 []string    `json:"full"`
	RestrictedIntercept  bool        `json:"restricted_intercept"`
	Terms                []string    `json:"terms"`
	Distribution         string      `json:"distribution"`
	Rows                 [][]float64 `json:"rows"`
	Statistic            float64     `json:"statistic"`
	DF                   float64     `json:"df"`
	DFResidual           *float64    `json:"df_residual"`
	PValue               float64     `json:"p_value"`
	RestrictedDFResidual float64     `json:"restricted_df_residual"`
	FullDFResidual       float64     `json:"full_df_residual"`
	RestrictedDeviance   float64     `json:"restricted_deviance"`
	FullDeviance         float64     `json:"full_deviance"`
	DispersionUsed       *float64    `json:"dispersion_used"`
	RestrictedTwoLogLik  *float64    `json:"restricted_two_log_lik"`
	FullTwoLogLik        *float64    `json:"full_two_log_lik"`
}

func loadNested(t *testing.T) []nestedCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "processing", "testdata", "reference", "nested.json"))
	if err != nil {
		t.Fatalf("read nested.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("nested.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("nested.json: hash mismatch (hand-edited?); regenerate with `make reference`")
	}
	var doc struct {
		RVersion string            `json:"r_version"`
		Packages map[string]string `json:"packages"`
		Cases    []nestedCase      `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode nested.json: %v", err)
	}
	if !strings.HasPrefix(doc.RVersion, "R version 4.6.1") || doc.Packages["lmtest"] != "0.9.40" || len(doc.Cases) != 21 {
		t.Fatalf("nested.json: R %q, lmtest %q, %d cases", doc.RVersion, doc.Packages["lmtest"], len(doc.Cases))
	}
	return doc.Cases
}

const nestedCohort = "nested.pulse"

func nestedSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x1", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "x2", Type: encoding.FieldTypeF64, ByteOffset: 8},
		{Name: "x3", Type: encoding.FieldTypeF64, ByteOffset: 16},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 24},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 32},
	}}
}

func (c nestedCase) service(t *testing.T) *Service {
	t.Helper()
	rows := make([][]uint64, len(c.Rows))
	for i, r := range c.Rows {
		rows[i] = make([]uint64, len(r))
		for j, v := range r {
			rows[i][j] = math.Float64bits(v)
		}
	}
	return New(setupTestFS(t, nestedCohort, nestedSchema(), rows))
}

// fit is the case's fit request over predictors.
func (c nestedCase) fit(predictors []string, intercept bool) *types.Request {
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictors}
	if !intercept {
		f := false
		spec.Intercept = &f
	}
	if c.Family != "gaussian" {
		spec.Type, spec.Family, spec.Link, spec.Tol, spec.MaxIters = types.REG_GLM, c.Family, c.Link, 1e-13, 200
		spec.Theta, spec.Dispersion = c.Theta, c.Dispersion
	}
	if c.Covariance != "" {
		spec.Covariance = &types.RegressionCovariance{Type: types.CovarianceType(strings.ToLower(c.Covariance))}
	}
	if c.Weight == "frequency" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	return &types.Request{Cohort: &types.Cohort{Filename: nestedCohort}, Regressions: []*types.RegressionSpec{spec}}
}

func (c nestedCase) batch() *types.ComposedRequest {
	return &types.ComposedRequest{
		Fits: []types.FitSpec{
			{Name: "m0", Request: c.fit(c.Restricted, c.RestrictedIntercept)},
			{Name: "m1", Request: c.fit(c.Full, true)},
		},
		Requests: []*types.Request{{Label: "rows", Cohort: &types.Cohort{Filename: nestedCohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}}}},
		Compare: []types.FitComparisonSpec{{Name: c.Name, Restricted: "m0", Full: "m1", Test: types.ComparisonTest(c.Test)}},
	}
}

// nestedRel is |got − want| / |want| (|got − want| when want is 0).
func nestedRel(got, want float64) float64 {
	d := math.Abs(got - want)
	if want != 0 {
		d /= math.Abs(want)
	}
	return d
}

// TestReferenceOracle_NestedMatchesR: every case's statistic, df,
// residual df, p-value, per-model residual df and deviance, the
// dispersion it divides by and (negative binomial, theta estimated) the
// two 2 loglik agree with R at 1e-10 relative — the frequency-weighted
// cases against the rep()-expanded fit — and the distribution and tested
// terms are R's.
func TestReferenceOracle_NestedMatchesR(t *testing.T) {
	t.Parallel()
	worst := map[string]float64{}
	for _, c := range loadNested(t) {
		resp, err := c.service(t).Compose(context.Background(), c.batch())
		if err != nil {
			t.Fatalf("%s: Compose: %v", c.Name, err)
		}
		if len(resp.Comparisons) != 1 {
			t.Fatalf("%s: %d comparisons", c.Name, len(resp.Comparisons))
		}
		got := resp.Comparisons[0]
		if got.Error != nil {
			t.Fatalf("%s: comparison failed: %+v", c.Name, got.Error)
		}
		if string(got.Distribution) != c.Distribution || !slices.Equal(got.Terms, c.Terms) || string(got.Test) != c.Test {
			t.Errorf("%s: test %s distribution %s terms %v, want %s %s %v", c.Name, got.Test, got.Distribution, got.Terms, c.Test, c.Distribution, c.Terms)
		}
		if c.Weight == "" && got.NObs != c.N {
			t.Errorf("%s: n_obs %d, want %d", c.Name, got.NObs, c.N)
		}
		check := func(what string, g, w float64) {
			e := nestedRel(g, w)
			if e > worst[what] {
				worst[what] = e
			}
			if e > 1e-10 {
				t.Errorf("%s: %s = %.17g, want %.17g (rel %.2g)", c.Name, what, g, w, e)
			}
		}
		check("statistic", got.Statistic, c.Statistic)
		check("df", got.DF, c.DF)
		check("p_value", got.PValue, c.PValue)
		check("restricted_df_residual", got.RestrictedDFResidual, c.RestrictedDFResidual)
		check("full_df_residual", got.FullDFResidual, c.FullDFResidual)
		check("restricted_deviance", got.RestrictedDeviance, c.RestrictedDeviance)
		check("full_deviance", got.FullDeviance, c.FullDeviance)
		if c.DFResidual != nil {
			check("df_residual", got.DFResidual, *c.DFResidual)
		} else if got.DFResidual != 0 {
			t.Errorf("%s: df_residual %g on a chi-square test", c.Name, got.DFResidual)
		}
		if c.DispersionUsed != nil {
			check("dispersion", got.Dispersion, *c.DispersionUsed)
		} else if got.Dispersion != 0 {
			t.Errorf("%s: dispersion %g, want absent", c.Name, got.Dispersion)
		}
		if c.FullTwoLogLik != nil {
			check("two_log_lik", got.RestrictedTwoLogLik, *c.RestrictedTwoLogLik)
			check("two_log_lik", got.FullTwoLogLik, *c.FullTwoLogLik)
		} else if got.FullTwoLogLik != 0 || got.RestrictedTwoLogLik != 0 {
			t.Errorf("%s: two_log_lik pair present", c.Name)
		}
	}
	t.Logf("max relative error: %v", worst)
}
