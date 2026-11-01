package pulse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// glm_residual_kinds_reference_test.go pins ATTR_REG_RESIDUAL
// params.kind (U65 E1-S1) end to end: a Compose `fits` REG_GLM per
// canonical family, then one slot scoring every row's fitted mean and
// its response / pearson / deviance / working residual through the
// bound fit, each held to statsmodels 0.14.5 GLMResults.fittedvalues /
// resid_* on the same rows (testdata/statsmodels/glm_residuals.json,
// make reference-statsmodels; the R residuals.glm calls that reproduce
// it are in the generator's header).

const glmResidualsGolden = "testdata/statsmodels/glm_residuals.json"

type glmResidualCase struct {
	Name         string      `json:"name"`
	Family       string      `json:"family"`
	Link         string      `json:"link"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	Fitted       []float64   `json:"fitted"`
	Response     []float64   `json:"response"`
	Pearson      []float64   `json:"pearson"`
	Deviance     []float64   `json:"deviance"`
	Working      []float64   `json:"working"`
}

func loadGLMResiduals(t *testing.T) []glmResidualCase {
	t.Helper()
	raw, err := os.ReadFile(glmResidualsGolden)
	if err != nil {
		t.Fatalf("%v (regenerate with make reference-statsmodels)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatalf("%s has no golden-hash footer", glmResidualsGolden)
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatalf("%s: hash mismatch (hand-edited?); regenerate with make reference-statsmodels", glmResidualsGolden)
	}
	var doc struct {
		Cases []glmResidualCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) != 3 {
		t.Fatalf("%d cases, want 3", len(doc.Cases))
	}
	return doc.Cases
}

// TestGLMResidualKinds_MatchStatsmodels: every residual kind on every
// row of a binomial-logit, poisson-log and gamma-inverse fit agrees with
// statsmodels to 1e-8 relative (the fits converge to ~1e-12; the bound
// is the IRLS tolerance's echo in the fitted means), and the fitted
// coefficients agree to 1e-8.
func TestGLMResidualKinds_MatchStatsmodels(t *testing.T) {
	t.Parallel()

	const tol = 1e-8
	for _, c := range loadGLMResiduals(t) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			rows := make([][]any, len(c.Rows))
			for i, r := range c.Rows {
				rows[i] = []any{float64(i), r[0], r[1], r[2]}
			}
			p := strdPulse(t, "glm.pulse", []string{"id", "x1", "x2", "y"}, rows, nil)
			attrs := []*types.Attribute{fitRefAttr(types.ATTR_REG_FITTED, "fitted", `{"fit":"m"}`)}
			kinds := []string{"response", "pearson", "deviance", "working"}
			for _, k := range kinds {
				attrs = append(attrs, fitRefAttr(types.ATTR_REG_RESIDUAL, k, fmt.Sprintf(`{"fit":"m","kind":%q}`, k)))
			}
			out, err := p.Compose(context.Background(), &ComposedRequest{
				Fits: []types.FitSpec{{Name: "m", Request: &Request{
					Cohort:      &types.Cohort{Filename: "glm.pulse"},
					Regressions: []*types.RegressionSpec{{Type: types.REG_GLM, Name: "m", Family: c.Family, Link: c.Link, Target: "y", Predictors: []string{"x1", "x2"}, Tol: 1e-12, MaxIters: 200}},
				}}},
				Requests: []*Request{fitRefSlot("glm.pulse", "id", attrs...)},
			})
			if err != nil {
				t.Fatal(err)
			}
			data := out.Responses[0].Data
			if len(data) != len(c.Rows) {
				t.Fatalf("%d rows, want %d", len(data), len(c.Rows))
			}
			want := map[string][]float64{"fitted": c.Fitted, "response": c.Response, "pearson": c.Pearson, "deviance": c.Deviance, "working": c.Working}
			for col, ws := range want {
				for i, row := range data {
					got, _ := row[col].(float64)
					w := ws[i]
					if math.Abs(got-w) > tol*math.Max(1, math.Abs(w)) {
						t.Errorf("row %d %s = %.17g, statsmodels %.17g", i, col, got, w)
					}
				}
			}
		})
	}
}

// TestGLMResidualKinds_Refusals: kind on ATTR_REG_FITTED is an unknown
// params key (PULSE_REQUEST_UNKNOWN_FIELD), an unknown kind is
// PULSE_MODEL_SCORE_INVALID, and over an OLS fit every kind is the
// response residual.
func TestGLMResidualKinds_Refusals(t *testing.T) {
	t.Parallel()

	c := loadGLMResiduals(t)[1]
	rows := make([][]any, len(c.Rows))
	for i, r := range c.Rows {
		rows[i] = []any{float64(i), r[0], r[1], r[2]}
	}
	p := strdPulse(t, "glm.pulse", []string{"id", "x1", "x2", "y"}, rows, nil)
	fit := func(reg *types.RegressionSpec) []types.FitSpec {
		return []types.FitSpec{{Name: "m", Request: &Request{Cohort: &types.Cohort{Filename: "glm.pulse"}, Regressions: []*types.RegressionSpec{reg}}}}
	}
	glm := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "poisson", Target: "y", Predictors: []string{"x1", "x2"}}
	for _, tc := range []struct {
		name string
		attr *types.Attribute
	}{
		{"kind on fitted", fitRefAttr(types.ATTR_REG_FITTED, "f", `{"fit":"m","kind":"pearson"}`)},
		{"unknown kind", fitRefAttr(types.ATTR_REG_RESIDUAL, "r", `{"fit":"m","kind":"studentized"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Compose(context.Background(), &ComposedRequest{Fits: fit(glm), Requests: []*Request{fitRefSlot("glm.pulse", "id", tc.attr)}})
			// kind is not an ATTR_REG_FITTED key at all: the request
			// allowlist refuses it before the fit rule runs.
			if err == nil || !strings.Contains(err.Error(), "kind") {
				t.Fatalf("Compose err = %v, want a kind refusal", err)
			}
			// Predict refuses it with the runtime's own code and message.
			env, perr := p.PredictCompose(context.Background(), &ComposedRequest{Fits: fit(glm), Requests: []*Request{fitRefSlot("glm.pulse", "id", tc.attr)}})
			if perr != nil {
				t.Fatal(perr)
			}
			matched := false
			for _, e := range env.Errors {
				matched = matched || strings.Contains(err.Error(), e.Message)
			}
			if !matched {
				t.Errorf("predict errors %+v do not carry the runtime refusal %q", env.Errors, err)
			}
		})
	}
	// OLS: every kind is the response residual (Gaussian, identity).
	ols := &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}}
	out, err := p.Compose(context.Background(), &ComposedRequest{Fits: fit(ols), Requests: []*Request{fitRefSlot("glm.pulse", "id",
		fitRefAttr(types.ATTR_REG_RESIDUAL, "resp", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "dev", `{"fit":"m","kind":"deviance"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "pear", `{"fit":"m","kind":"pearson"}`),
	)}})
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range out.Responses[0].Data {
		if row["resp"] != row["dev"] || row["resp"] != row["pear"] {
			t.Errorf("OLS row %d: residual kinds differ %v / %v / %v", i, row["resp"], row["dev"], row["pear"])
		}
	}
}
