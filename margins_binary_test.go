package pulse

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// margins_binary_test.go covers RegressionMargins.Binary (U69 #641)
// end to end on the margins cohort, whose column b is 0/1: binary slope
// (explicit) answers byte for byte what an absent field answers; under
// binary contrast the block lists b in contrasts, answers the same
// under every DecodeWorkers / ShardWorkers count and on OLS's streamed
// and buffered arms, and an identity link's contrast is the
// coefficient. The figures are pinned to R by margins_oracle_test.go
// (internal/processing/regression) and the ordinal / multinomial
// oracles.

// marginsBinarySpecs are fits with the 0/1 predictor b beside x1, under
// binary (empty = absent).
func marginsBinarySpecs(binary string) []*types.RegressionSpec {
	xs := []string{"x1", "b"}
	mg := func(m types.RegressionMargins) *types.RegressionMargins { m.Binary = binary; return &m }
	return []*types.RegressionSpec{
		{Type: types.REG_OLS, Name: "ols", Target: "y", Predictors: xs, Margins: mg(types.RegressionMargins{})},
		{Type: types.REG_OLS, Name: "ols_mem", Target: "y", Predictors: xs, Margins: mg(types.RegressionMargins{Type: types.MarginsMEM})},
		{Type: types.REG_GLM, Name: "pois", Family: "poisson", Target: "c", Predictors: xs, Margins: mg(types.RegressionMargins{})},
		{Type: types.REG_GLM, Name: "pois_mem", Family: "poisson", Target: "c", Predictors: xs, Margins: mg(types.RegressionMargins{Type: types.MarginsMEM})},
		{Type: types.REG_GLM, Name: "pois_link", Family: "poisson", Target: "c", Predictors: xs, Margins: mg(types.RegressionMargins{Scale: types.MarginsScaleLink})},
		{Type: types.REG_GLM, Name: "pois_hc3", Family: "poisson", Target: "c", Predictors: xs, Margins: mg(types.RegressionMargins{}),
			Covariance: &types.RegressionCovariance{Type: types.CovarianceHC3}},
	}
}

// TestMarginsBinary_SlopeIsByteIdentical: binary slope, spelled out,
// answers byte for byte what the absent field answers — no binary echo,
// no contrasts, the same slopes.
func TestMarginsBinary_SlopeIsByteIdentical(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	m := single[0]
	absent, err := m.p.Process(ctx, marginsReq(m.cohort, marginsBinarySpecs("")...))
	if err != nil {
		t.Fatal(err)
	}
	slope, err := m.p.Process(ctx, marginsReq(m.cohort, marginsBinarySpecs(types.MarginsBinarySlope)...))
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(absent.Regressions)
	js, _ := json.Marshal(slope.Regressions)
	if string(ja) != string(js) {
		t.Errorf("binary slope moved the output:\n%s\nvs\n%s", js, ja)
	}
	for _, r := range slope.Regressions {
		if r.Margins == nil || r.Margins.Binary != "" || r.Margins.Contrasts != nil {
			t.Errorf("%s: slope block %+v carries a binary echo or contrasts", r.Name, r.Margins)
		}
	}
}

// TestMarginsBinary_ContrastWorkerInvariant: under binary contrast every
// block lists b (and not x1) in contrasts and echoes binary; the blocks
// are byte-identical across decode workers, across shard workers, and
// between OLS's streamed and buffered arms; on an identity link and on
// the link scale the contrast is the coefficient bit for bit, and on the
// response scale of a log link it differs from the slope.
func TestMarginsBinary_ContrastWorkerInvariant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, sharded := marginsModes(t)
	var first *Response
	for _, group := range [][]scorerParityMode{single, sharded} {
		var want string
		for _, m := range group {
			resp, err := m.p.Process(ctx, marginsReq(m.cohort, marginsBinarySpecs(types.MarginsBinaryContrast)...))
			if err != nil {
				t.Fatalf("%s: %v", m.name, err)
			}
			got := marginsJSON(t, resp)
			if want == "" {
				want = got
			} else if got != want {
				t.Errorf("%s contrast margins differ from %s:\n%s\nvs\n%s", m.name, group[0].name, got, want)
			}
			if first == nil {
				first = resp
			}
		}
	}
	for _, r := range first.Regressions {
		if r.Margins.Binary != types.MarginsBinaryContrast || !slices.Equal(r.Margins.Contrasts, []string{"b"}) {
			t.Errorf("%s: binary %q contrasts %v, want contrast [b]", r.Name, r.Margins.Binary, r.Margins.Contrasts)
		}
		identity := r.Type == types.REG_OLS || r.Margins.Scale == types.MarginsScaleLink
		if identity && (r.Margins.Estimates["b"] != r.Coefficients["b"] || r.Margins.StdErrors["b"] != r.StdErrors["b"]) {
			t.Errorf("%s: contrast %v (se %v), coefficient %v (se %v)", r.Name, r.Margins.Estimates["b"], r.Margins.StdErrors["b"], r.Coefficients["b"], r.StdErrors["b"])
		}
	}
	slope, err := single[0].p.Process(ctx, marginsReq(single[0].cohort, marginsBinarySpecs("")...))
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i < 4; i++ { // the poisson response-scale blocks
		c, s := first.Regressions[i].Margins, slope.Regressions[i].Margins
		if c.Estimates["b"] == s.Estimates["b"] || c.StdErrors["b"] == s.StdErrors["b"] {
			t.Errorf("%s: contrast %v equals the slope %v", first.Regressions[i].Name, c.Estimates["b"], s.Estimates["b"])
		}
		if c.Estimates["x1"] != s.Estimates["x1"] || c.StdErrors["x1"] != s.StdErrors["x1"] {
			t.Errorf("%s: x1's slope moved under binary contrast", first.Regressions[i].Name)
		}
	}
	m := single[0]
	ols := marginsBinarySpecs(types.MarginsBinaryContrast)[:2]
	streamed, err := m.p.Process(ctx, marginsReq(m.cohort, ols...))
	if err != nil {
		t.Fatal(err)
	}
	req := marginsReq(m.cohort, ols...)
	req.Aggregations = []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "y", Label: "med"}}
	buffered, err := m.p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := marginsJSON(t, streamed), marginsJSON(t, buffered); a != b {
		t.Errorf("OLS streamed contrast margins %s, buffered %s", a, b)
	}
}
