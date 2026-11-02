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

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// ols_influence_reference_test.go pins the OLS refit's influence
// figures (U65 E1-S2) end to end: ATTR_REG_RESIDUAL params.kind
// standardized / studentized and ATTR_REG_INFLUENCE params.measure
// cooks / dffits on every row, unweighted and under a frequency weight,
// each held to statsmodels 0.14.5 OLSInfluence (the frequency case on
// the rep() expansion, one copy's figure) on the same rows
// (testdata/statsmodels/ols_influence.json, make reference-statsmodels;
// the reproducing R calls are in the generator's header).

const olsInfluenceGolden = "testdata/statsmodels/ols_influence.json"

type olsInfluenceCase struct {
	Name         string      `json:"name"`
	Weighted     bool        `json:"weighted"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	Hat          []float64   `json:"hat"`
	Standardized []float64   `json:"standardized"`
	Studentized  []float64   `json:"studentized"`
	Cooks        []float64   `json:"cooks"`
	DFFITS       []float64   `json:"dffits"`
}

func loadOLSInfluence(t *testing.T) []olsInfluenceCase {
	t.Helper()
	raw, err := os.ReadFile(olsInfluenceGolden)
	if err != nil {
		t.Fatalf("%v (regenerate with make reference-statsmodels)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatalf("%s has no golden-hash footer", olsInfluenceGolden)
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatalf("%s: hash mismatch (hand-edited?); regenerate with make reference-statsmodels", olsInfluenceGolden)
	}
	var doc struct {
		Cases []olsInfluenceCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) != 2 {
		t.Fatalf("%d cases, want 2", len(doc.Cases))
	}
	return doc.Cases
}

// influencePulse writes a case's rows as influence.pulse {id, x1, x2,
// y, w}.
func influencePulse(t *testing.T, c olsInfluenceCase) *Pulse {
	t.Helper()
	rows := make([][]any, len(c.Rows))
	for i, r := range c.Rows {
		rows[i] = []any{float64(i), r[0], r[1], r[2], r[3]}
	}
	return strdPulse(t, "influence.pulse", []string{"id", "x1", "x2", "y", "w"}, rows, nil)
}

// influenceAttr is a refit slot on y ~ x1 + x2.
func influenceAttr(typ types.AttributeType, label, params string) *types.Attribute {
	a := &types.Attribute{Type: typ, Label: label, Target: "y", Predictors: []string{"x1", "x2"}}
	if params != "" {
		a.Params = json.RawMessage(params)
	}
	return a
}

// influenceRequest is one row per id (GROUP_RANGE interval 1) carrying
// each attribute's value (AGG_MIN of one row), under a request weight
// the attributes inherit (the AGG_MIN readers opt out).
func influenceRequest(weight *types.WeightSpec, attrs ...*types.Attribute) *Request {
	req := fitRefSlot("influence.pulse", "id", attrs...)
	req.Weight = weight
	if weight != nil {
		for _, a := range req.Aggregations {
			a.Weight = types.NullSlotWeight()
		}
	}
	return req
}

// TestOLSInfluence_MatchesStatsmodels: on every row of both cases the
// standardized and studentized residuals, Cook's distance and DFFITS
// agree with statsmodels OLSInfluence to 1e-9 (relative, absolute below
// 1), and ATTR_REG_LEVERAGE is w times the oracle's one-copy hat.
func TestOLSInfluence_MatchesStatsmodels(t *testing.T) {
	t.Parallel()

	const tol = 1e-9
	for _, c := range loadOLSInfluence(t) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			p := influencePulse(t, c)
			var weight *types.WeightSpec
			if c.Weighted {
				weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
			}
			req := influenceRequest(weight,
				influenceAttr(types.ATTR_REG_RESIDUAL, "standardized", `{"kind":"standardized"}`),
				influenceAttr(types.ATTR_REG_RESIDUAL, "studentized", `{"kind":"studentized"}`),
				influenceAttr(types.ATTR_REG_INFLUENCE, "cooks", ""),
				influenceAttr(types.ATTR_REG_INFLUENCE, "dffits", `{"measure":"dffits"}`),
				influenceAttr(types.ATTR_REG_LEVERAGE, "leverage", ""),
			)
			resp, err := p.Process(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Data) != len(c.Rows) {
				t.Fatalf("%d rows, want %d", len(resp.Data), len(c.Rows))
			}
			hat := make([]float64, len(c.Hat))
			for i, h := range c.Hat {
				hat[i] = h * c.Rows[i][3]
			}
			want := map[string][]float64{"standardized": c.Standardized, "studentized": c.Studentized, "cooks": c.Cooks, "dffits": c.DFFITS, "leverage": hat}
			for col, ws := range want {
				worst := 0.0
				for i, row := range resp.Data {
					got, ok := row[col].(float64)
					w := ws[i]
					if !ok {
						t.Errorf("row %d %s = %v, want a number", i, col, row[col])
						continue
					}
					e := math.Abs(got-w) / math.Max(1, math.Abs(w))
					worst = math.Max(worst, e)
					if e > tol {
						t.Errorf("row %d %s = %.17g, statsmodels %.17g", i, col, got, w)
					}
				}
				t.Logf("%s max scaled error %.3g", col, worst)
			}
		})
	}
}

// TestOLSInfluence_RefusalsMatchPredict: each refusal of the
// design-reading refit slots — a penalty (leverage, influence, the
// studentized kinds), an unknown measure or refit kind, a probability
// weight on a per-observation figure, a studentized kind over a bound
// fit — is the same code and message from Process (or Compose) and
// Predict (or PredictCompose). Leverage keeps either weight kind.
func TestOLSInfluence_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	c := loadOLSInfluence(t)[0]
	p := influencePulse(t, c)
	ctx := context.Background()
	penal := func(a *types.Attribute) *types.Attribute { a.Penalty, a.Alpha = "l2", 0.5; return a }
	prob := &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
	for _, tc := range []struct {
		name   string
		weight *types.WeightSpec
		attr   *types.Attribute
		code   errors.Code
	}{
		{"influence penalty", nil, penal(influenceAttr(types.ATTR_REG_INFLUENCE, "a", "")), errors.PROCESSING_CONFIG},
		{"leverage penalty", nil, penal(influenceAttr(types.ATTR_REG_LEVERAGE, "a", "")), errors.PROCESSING_CONFIG},
		{"standardized penalty", nil, penal(influenceAttr(types.ATTR_REG_RESIDUAL, "a", `{"kind":"standardized"}`)), errors.PROCESSING_CONFIG},
		{"studentized penalty", nil, penal(influenceAttr(types.ATTR_REG_RESIDUAL, "a", `{"kind":"studentized"}`)), errors.PROCESSING_CONFIG},
		{"unknown measure", nil, influenceAttr(types.ATTR_REG_INFLUENCE, "a", `{"measure":"covratio"}`), errors.PROCESSING_CONFIG},
		{"unknown refit kind", nil, influenceAttr(types.ATTR_REG_RESIDUAL, "a", `{"kind":"partial"}`), errors.PULSE_MODEL_SCORE_INVALID},
		{"influence probability", prob, influenceAttr(types.ATTR_REG_INFLUENCE, "a", ""), errors.PULSE_WEIGHT_UNSUPPORTED},
		{"studentized probability", prob, influenceAttr(types.ATTR_REG_RESIDUAL, "a", `{"kind":"studentized"}`), errors.PULSE_WEIGHT_UNSUPPORTED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Process(ctx, influenceRequest(tc.weight, tc.attr))
			ce := codedOf(t, err)
			if ce.Code != tc.code {
				t.Fatalf("Process err = %v, want %s", err, tc.code)
			}
			pr, perr := p.Predict(ctx, influenceRequest(tc.weight, tc.attr))
			if perr != nil {
				t.Fatal(perr)
			}
			matched := false
			for _, e := range pr.Errors {
				matched = matched || (e.Code == string(ce.Code) && e.Message == ce.Message)
			}
			if pr.Valid || !matched {
				t.Errorf("predict (valid %v) errors %+v do not carry the runtime refusal %s %q", pr.Valid, pr.Errors, ce.Code, ce.Message)
			}
		})
	}
	t.Run("probability weight keeps leverage and the response residual", func(t *testing.T) {
		req := influenceRequest(prob, influenceAttr(types.ATTR_REG_LEVERAGE, "h", ""), influenceAttr(types.ATTR_REG_RESIDUAL, "e", `{"kind":"pearson"}`))
		if _, err := p.Process(ctx, req); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("studentized over a bound fit without vcov", func(t *testing.T) {
		// The bound-fit form reads the fit's vcov (U65 E1-S2,
		// glm_influence_reference_test.go); a fit without one refuses.
		creq := func() *ComposedRequest {
			return &ComposedRequest{
				Fits: []types.FitSpec{{Name: "m", Request: &Request{Cohort: &types.Cohort{Filename: "influence.pulse"},
					Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}}}}}},
				Requests: []*Request{fitRefSlot("influence.pulse", "id", fitRefAttr(types.ATTR_REG_RESIDUAL, "r", `{"fit":"m","kind":"studentized"}`))},
			}
		}
		_, err := p.Compose(ctx, creq())
		ce := codedOf(t, err)
		if ce.Code != errors.PULSE_MODEL_SCORE_INVALID || ce.Details["reason"] != "params_invalid" || ce.Details["requires"] != "vcov" {
			t.Fatalf("Compose err = %v, want PULSE_MODEL_SCORE_INVALID params_invalid requires vcov", err)
		}
		env, perr := p.PredictCompose(ctx, creq())
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

// TestOLSInfluence_RefitKindsAreResponse: on the OLS refit the pearson,
// deviance and working kinds are the response residual bit for bit
// (Gaussian, identity) — the refit used to ignore params.kind.
func TestOLSInfluence_RefitKindsAreResponse(t *testing.T) {
	t.Parallel()

	p := influencePulse(t, loadOLSInfluence(t)[0])
	resp, err := p.Process(context.Background(), influenceRequest(nil,
		influenceAttr(types.ATTR_REG_RESIDUAL, "resp", ""),
		influenceAttr(types.ATTR_REG_RESIDUAL, "pear", `{"kind":"pearson"}`),
		influenceAttr(types.ATTR_REG_RESIDUAL, "dev", `{"kind":"deviance"}`),
		influenceAttr(types.ATTR_REG_RESIDUAL, "work", `{"kind":"working"}`),
	))
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range resp.Data {
		if row["resp"] != row["pear"] || row["resp"] != row["dev"] || row["resp"] != row["work"] {
			t.Errorf("row %d: kinds differ %v / %v / %v / %v", i, row["resp"], row["pear"], row["dev"], row["work"])
		}
	}
}
