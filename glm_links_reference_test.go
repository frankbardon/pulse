package pulse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// glm_links_reference_test.go pins the non-canonical REG_GLM links (U65
// E2-S1) end to end over a BOUND fit: a Compose `fits` REG_GLM entry
// (binomial probit / cloglog, poisson identity / sqrt, gamma log /
// identity) with vcov, then params.fit slots scoring every row's
// ATTR_REG_FITTED / ATTR_REG_PREDICT (the inverse link), ATTR_REG_RESIDUAL
// kind working ((y − μ)·g′(μ)), ATTR_REG_LEVERAGE (hatvalues) and
// ATTR_REG_INFLUENCE dffits (its sign sign g′(μ)), each held to R 4.6.1
// (internal/processing/testdata/reference/glm_links.json,
// scripts/reference/gen_glm_links.R — make reference). The fit-level
// figures: TestReferenceOracle_GLMLinksMatchR
// (internal/processing/regression).

const glmLinksGolden = "internal/processing/testdata/reference/glm_links.json"

type glmLinksRefCase struct {
	Name         string      `json:"name"`
	Family       string      `json:"family"`
	Link         string      `json:"link"`
	Weighted     bool        `json:"weighted"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	Fitted       []float64   `json:"fitted"`
	Working      []float64   `json:"working"`
	Hat          []float64   `json:"hat"`
	DFFITS       []float64   `json:"dffits"`
}

func loadGLMLinksRef(t *testing.T) []glmLinksRefCase {
	t.Helper()
	raw, err := os.ReadFile(glmLinksGolden)
	if err != nil {
		t.Fatalf("%v (regenerate with make reference)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatalf("%s has no golden-hash footer", glmLinksGolden)
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatalf("%s: hash mismatch (hand-edited?); regenerate with make reference", glmLinksGolden)
	}
	var doc struct {
		Cases []glmLinksRefCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	var out []glmLinksRefCase
	for _, c := range doc.Cases {
		if !c.Weighted {
			out = append(out, c)
		}
	}
	if len(out) != 6 {
		t.Fatalf("%d unweighted cases, want 6", len(out))
	}
	return out
}

// glmLinksRefPulse writes a case's rows as glm.pulse {id, x1, x2, y} on
// a memory fs it returns too (the saved model file is read back).
func glmLinksRefPulse(t *testing.T, c glmLinksRefCase) (*Pulse, afero.Fs) {
	t.Helper()
	p, fsys := matPulse(t, Options{})
	var schema encoding.Schema
	for _, name := range []string{"id", "x1", "x2", "y"} {
		schema.Fields = append(schema.Fields, encoding.Field{Name: name, Type: encoding.FieldTypeF64, Description: "glm link oracle column " + name})
	}
	b, err := p.NewCohortBuilder(context.Background(), "glm.pulse", schema, CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range c.Rows {
		if err := b.Append(CohortRow{float64(i), r[0], r[1], r[2]}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

func glmLinksRefFits(c glmLinksRefCase) []types.FitSpec {
	return glmInfluenceFits(&types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: c.Family, Link: c.Link,
		Target: "y", Predictors: []string{"x1", "x2"}, Vcov: true, Tol: 1e-13, MaxIters: 500})
}

func glmLinksRefSlot() *Request {
	return fitRefSlot("glm.pulse", "id",
		fitRefAttr(types.ATTR_REG_FITTED, "fitted", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_PREDICT, "predict", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "working", `{"fit":"m","kind":"working"}`),
		fitRefAttr(types.ATTR_REG_LEVERAGE, "hat", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_INFLUENCE, "dffits", `{"fit":"m","measure":"dffits"}`),
	)
}

// TestGLMLinks_BoundFitMatchesR: over a bound fit under each
// non-canonical link, every row's fitted mean (and its ATTR_REG_PREDICT
// score, bit for bit), working residual, leverage and DFFITS agree with
// R 4.6.1 at 1e-9 relative (absolute floor 1e-12); a model file saved
// from the fit carries its link and scores the same rows bit for bit
// when loaded.
func TestGLMLinks_BoundFitMatchesR(t *testing.T) {
	t.Parallel()

	const tol = 1e-9
	ctx := context.Background()
	for _, c := range loadGLMLinksRef(t) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			p, fsys := glmLinksRefPulse(t, c)
			fits := glmLinksRefFits(c)
			fits[0].Save = "m.pulse-model.json"
			out, err := p.Compose(ctx, &ComposedRequest{Fits: fits, Requests: []*Request{glmLinksRefSlot()}})
			if err != nil {
				t.Fatal(err)
			}
			fit := out.Fits["m"].RegressionResult
			if fit.Family != c.Family || fit.Link != c.Link {
				t.Fatalf("fit echoes %s/%s", fit.Family, fit.Link)
			}
			for j, k := range []string{"(intercept)", "x1", "x2"} {
				if e := relErr(fit.Coefficients[k], c.Coefficients[j]); e > tol {
					t.Errorf("coefficient %s = %.17g, R %.17g (rel %.3g)", k, fit.Coefficients[k], c.Coefficients[j], e)
				}
			}
			data := out.Responses[0].Data
			if len(data) != len(c.Rows) {
				t.Fatalf("%d rows, want %d", len(data), len(c.Rows))
			}
			for col, ws := range map[string][]float64{"fitted": c.Fitted, "working": c.Working, "hat": c.Hat, "dffits": c.DFFITS} {
				worst := 0.0
				for i, row := range data {
					got, ok := row[col].(float64)
					if !ok {
						t.Fatalf("row %d %s = %v, not a number", i, col, row[col])
					}
					e := relErr(got, ws[i])
					worst = math.Max(worst, e)
					if e > tol {
						t.Errorf("row %d %s = %.17g, R %.17g (rel %.3g)", i, col, got, ws[i], e)
					}
				}
				t.Logf("%s %s: max rel error %.3g", c.Name, col, worst)
			}
			for i, row := range data {
				if row["fitted"] != row["predict"] {
					t.Errorf("row %d: fitted %v ≠ ATTR_REG_PREDICT %v", i, row["fitted"], row["predict"])
				}
			}

			// The model file round-trips the link.
			raw, err := fsys.Open("m.pulse-model.json")
			if err != nil {
				t.Fatal(err)
			}
			var file struct {
				Family string `json:"family"`
				Link   string `json:"link"`
			}
			if err := json.NewDecoder(raw).Decode(&file); err != nil {
				t.Fatal(err)
			}
			_ = raw.Close()
			if file.Family != c.Family || file.Link != c.Link {
				t.Errorf("model file carries %s/%s", file.Family, file.Link)
			}
			loaded := glmLinksRefSlot()
			loaded.Models = []types.ModelRef{{Name: "m", Path: "m.pulse-model.json"}}
			lo, err := p.Process(ctx, loaded)
			if err != nil {
				t.Fatal(err)
			}
			for i, row := range lo.Data {
				for _, col := range []string{"fitted", "predict", "working", "hat", "dffits"} {
					if g, w := row[col], data[i][col]; g != w {
						t.Errorf("row %d %s: loaded %v, in-request %v", i, col, g, w)
					}
				}
			}
		})
	}
}

// TestGLMLinks_PredictMatchesRuntime: a REG_GLM request on any of a
// family's links passes predict and runs; a link of another family is
// PROCESSING_REGRESSION_INVALID_LINK with the same message and the same
// allowed set at predict and at run.
func TestGLMLinks_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := loadGLMLinksRef(t)[2] // poisson_identity: y ≥ 0 counts, valid for every family's check
	p, _ := glmLinksRefPulse(t, c)
	req := func(family, link string) *Request {
		return &Request{Cohort: &types.Cohort{Filename: "glm.pulse"}, Regressions: []*types.RegressionSpec{{
			Type: types.REG_GLM, Name: "m", Family: family, Link: link, Target: "y", Predictors: []string{"x1", "x2"},
		}}}
	}
	// The counts fit every poisson link; the other families' pairs are
	// judged by predict alone below.
	for _, tc := range []struct{ family, link string }{
		{"poisson", "identity"}, {"poisson", "sqrt"}, {"poisson", ""},
	} {
		res, err := p.Process(ctx, req(tc.family, tc.link))
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.family, tc.link, err)
		}
		if r := res.Regressions[0]; r.Error != nil {
			t.Fatalf("%s/%s: slot failed %v", tc.family, tc.link, r.Error)
		}
	}
	for _, tc := range []struct{ family, link string }{
		{"binomial", "probit"}, {"binomial", "cloglog"}, {"poisson", "identity"}, {"poisson", "sqrt"}, {"gamma", "log"}, {"gamma", "identity"},
	} {
		pr, err := p.Predict(ctx, req(tc.family, tc.link))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range pr.Errors {
			if e.Code == string(errors.PROCESSING_REGRESSION_INVALID_LINK) {
				t.Errorf("predict refuses %s/%s: %+v", tc.family, tc.link, e)
			}
		}
	}
	for _, tc := range []struct{ family, link string }{
		{"poisson", "logit"}, {"binomial", "sqrt"}, {"gamma", "probit"}, {"poisson", "inverse"},
	} {
		_, err := p.Process(ctx, req(tc.family, tc.link))
		ce := codedOf(t, err)
		if ce.Code != errors.PROCESSING_REGRESSION_INVALID_LINK {
			t.Fatalf("%s/%s: Process err %v, want INVALID_LINK", tc.family, tc.link, err)
		}
		pr, perr := p.Predict(ctx, req(tc.family, tc.link))
		if perr != nil {
			t.Fatal(perr)
		}
		matched := false
		for _, e := range pr.Errors {
			if e.Code == string(ce.Code) && e.Message == ce.Message {
				matched = true
				if got, want := e.Details["allowed"], ce.Details["allowed"]; !jsonEqual(t, got, want) {
					t.Errorf("%s/%s: predict allowed %v, runtime %v", tc.family, tc.link, got, want)
				}
			}
		}
		if pr.Valid || !matched {
			t.Errorf("%s/%s: predict errors %+v do not carry the runtime refusal %q", tc.family, tc.link, pr.Errors, ce.Message)
		}
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(x) == string(y)
}
