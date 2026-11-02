package pulse

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// collinearity_test.go covers RegressionSpec.Collinearity (U65 E3-S2)
// end to end: every refusal is the same coded error at predict and at
// run (internal/collinearity), the block answers bit for bit the same
// under every DecodeWorkers / ShardWorkers count and on OLS's streaming
// and buffered arms, it moves no other figure, it is a function of the
// design alone (a penalty, a bound, a robust covariance or a resample
// leaves it unchanged), and a FEAT_ONE_HOT block grouped as one term
// equals car::vif's generalized VIF. The figures themselves are pinned
// to car by internal/processing/regression/vif_oracle_test.go. The
// cohort is margins_test.go's.

func collinearitySpecs() []*types.RegressionSpec {
	xs := []string{"x1", "x2", "c"}
	col := func() *types.RegressionCollinearity { return &types.RegressionCollinearity{} }
	return []*types.RegressionSpec{
		{Type: types.REG_OLS, Name: "ols", Target: "y", Predictors: xs, Collinearity: col()},
		{Type: types.REG_OLS, Name: "ols_grouped", Target: "y", Predictors: xs,
			Collinearity: &types.RegressionCollinearity{Groups: []types.RegressionTermGroup{{Name: "xs", Predictors: []string{"x1", "x2"}}}}},
		{Type: types.REG_GLM, Name: "logit", Family: "binomial", Target: "b", Predictors: xs, Collinearity: col()},
		{Type: types.REG_GLM, Name: "logit_predictors", Family: "binomial", Target: "b", Predictors: xs,
			Collinearity: &types.RegressionCollinearity{Basis: types.CollinearityBasisPredictors}},
		{Type: types.REG_GLM, Name: "pois_grouped", Family: "poisson", Target: "c", Predictors: []string{"x1", "x2", "y"},
			Collinearity: &types.RegressionCollinearity{Groups: []types.RegressionTermGroup{{Name: "xs", Predictors: []string{"x2", "x1"}}}}},
	}
}

func collinearityJSON(t *testing.T, resp *Response) string {
	t.Helper()
	var blocks []any
	for _, r := range resp.Regressions {
		if r.Error != nil {
			t.Fatalf("%s failed: %+v", r.Name, r.Error)
		}
		if r.Collinearity == nil {
			t.Fatalf("%s: no collinearity", r.Name)
		}
		blocks = append(blocks, r.Collinearity)
	}
	b, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestCollinearity_WorkerInvariant: the blocks are byte-identical
// across decode workers (one file) and shard workers (the archive);
// OLS's streamed block equals its buffered one (an AGG_MEDIAN forces the
// buffered arm), and predict calls the OLS request streamable as the
// run streams it.
func TestCollinearity_WorkerInvariant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, sharded := marginsModes(t)
	for _, group := range [][]scorerParityMode{single, sharded} {
		var want string
		for _, m := range group {
			resp, err := m.p.Process(ctx, marginsReq(m.cohort, collinearitySpecs()...))
			if err != nil {
				t.Fatalf("%s: %v", m.name, err)
			}
			got := collinearityJSON(t, resp)
			if want == "" {
				want = got
			} else if got != want {
				t.Errorf("%s collinearity differs from %s:\n%s\nvs\n%s", m.name, group[0].name, got, want)
			}
		}
	}
	m := single[0]
	ols := collinearitySpecs()[:2]
	pr, err := m.p.Predict(ctx, marginsReq(m.cohort, ols...))
	if err != nil || !pr.Valid || !pr.Streamable {
		t.Fatalf("predict: valid %v streamable %v (%v) %+v", pr.Valid, pr.Streamable, err, pr.StreamableReasons)
	}
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
	if a, b := collinearityJSON(t, streamed), collinearityJSON(t, buffered); a != b {
		t.Errorf("OLS streamed collinearity %s, buffered %s", a, b)
	}
}

// TestCollinearity_AbsentIsByteIdentical: a fit without the block
// answers byte for byte what the same fit with one answers, the block
// itself aside.
func TestCollinearity_AbsentIsByteIdentical(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	m := single[0]
	with := collinearitySpecs()
	without := collinearitySpecs()
	for i := range with {
		with[i].Vcov, without[i].Vcov = true, true
		without[i].Collinearity = nil
	}
	a, err := m.p.Process(ctx, marginsReq(m.cohort, with...))
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.p.Process(ctx, marginsReq(m.cohort, without...))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range a.Regressions {
		r.Collinearity = nil
	}
	ja, _ := json.Marshal(a.Regressions)
	jb, _ := json.Marshal(b.Regressions)
	if string(ja) != string(jb) {
		t.Errorf("collinearity moved the fit:\n%s\nvs\n%s", ja, jb)
	}
}

// TestCollinearity_DesignOnly: the block reads the design, not the
// estimator — an OLS fit's is the same bit for bit under ridge, lasso,
// a nonnegative bound, an HC3 covariance and a jackknife, and equals its
// predictors basis; a GLM's predictors basis is the same with or without
// a penalty, and agrees with OLS's to rounding (another fold of the same
// rows); its model basis does not move under HC3, and differs from its
// predictors basis (the working weights enter).
func TestCollinearity_DesignOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	m := single[0]
	xs := []string{"x1", "x2", "c"}
	ols := func(name string, mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_OLS, Name: name, Target: "y", Predictors: xs, Collinearity: &types.RegressionCollinearity{}}
		mut(s)
		return s
	}
	glm := func(name string, mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_GLM, Name: name, Family: "binomial", Target: "b", Predictors: xs, Collinearity: &types.RegressionCollinearity{}}
		mut(s)
		return s
	}
	predictors := func(s *types.RegressionSpec) { s.Collinearity.Basis = types.CollinearityBasisPredictors }
	specs := []*types.RegressionSpec{
		ols("plain", func(*types.RegressionSpec) {}),
		ols("ridge", func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.5 }),
		ols("lasso", func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l1", 0.01 }),
		ols("bounded", func(s *types.RegressionSpec) { s.Bounds = &types.RegressionBounds{Nonnegative: []string{"x2"}} }),
		ols("hc3", func(s *types.RegressionSpec) { s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3} }),
		ols("jackknife", func(s *types.RegressionSpec) { s.Resample = "jackknife" }),
		ols("predictors", predictors),
		glm("glm_predictors", predictors),
		glm("glm_ridge_predictors", func(s *types.RegressionSpec) { predictors(s); s.Penalty, s.Alpha = "l2", 0.5 }),
		glm("glm_model", func(*types.RegressionSpec) {}),
		glm("glm_model_hc3", func(s *types.RegressionSpec) { s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3} }),
	}
	resp, err := m.p.Process(ctx, marginsReq(m.cohort, specs...))
	if err != nil {
		t.Fatal(err)
	}
	figures := map[string]string{}
	blocks := map[string]*types.RegressionCollinearityResult{}
	for _, r := range resp.Regressions {
		if r.Error != nil || r.Collinearity == nil {
			t.Fatalf("%s: %+v", r.Name, r.Error)
		}
		b, _ := json.Marshal(map[string]any{"vif": r.Collinearity.VIF, "tolerance": r.Collinearity.Tolerance, "df": r.Collinearity.DF})
		figures[r.Name] = string(b)
		blocks[r.Name] = r.Collinearity
	}
	for _, same := range [][]string{
		{"plain", "ridge", "lasso", "bounded", "hc3", "jackknife", "predictors"},
		{"glm_predictors", "glm_ridge_predictors"},
		{"glm_model", "glm_model_hc3"},
	} {
		for _, n := range same[1:] {
			if figures[n] != figures[same[0]] {
				t.Errorf("%s figures %s, %s %s", n, figures[n], same[0], figures[same[0]])
			}
		}
	}
	// The same rows and weights, folded two ways (OLS's streaming
	// Welford moments, GLM's two-pass cross-product): equal to rounding.
	for _, k := range xs {
		if e := relErr(blocks["glm_predictors"].VIF[k], blocks["plain"].VIF[k]); e > 1e-12 {
			t.Errorf("vif[%s]: GLM predictors basis %v, OLS %v (rel %.3g)", k, blocks["glm_predictors"].VIF[k], blocks["plain"].VIF[k], e)
		}
	}
	if figures["glm_model"] == figures["glm_predictors"] {
		t.Error("the GLM model basis equals its predictors basis: the working weights did not enter")
	}
}

// TestCollinearity_PredictMatchesRuntime: every collinearity refusal is
// the same coded error — code, message and details — at predict and at
// run.
func TestCollinearity_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	xs := []string{"x1", "x2", "c"}
	glm := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "binomial", Target: "b", Predictors: xs, Collinearity: &types.RegressionCollinearity{}}
		mut(s)
		return s
	}
	groups := func(gs ...types.RegressionTermGroup) func(*types.RegressionSpec) {
		return func(s *types.RegressionSpec) { s.Collinearity.Groups = gs }
	}
	for _, tc := range []struct {
		name     string
		spec     *types.RegressionSpec
		requires string
	}{
		{"basis", glm(func(s *types.RegressionSpec) { s.Collinearity.Basis = "design" }), "basis model or predictors"},
		{"unnamed group", glm(groups(types.RegressionTermGroup{Predictors: []string{"x1", "x2"}})), "a group name"},
		{"unknown predictor", glm(groups(types.RegressionTermGroup{Name: "g", Predictors: []string{"x1", "z"}})), "a predictor"},
		{"name clash", glm(groups(types.RegressionTermGroup{Name: "c", Predictors: []string{"x1", "x2"}})), "a unique term name"},
		{"bayes", &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: xs, Collinearity: &types.RegressionCollinearity{}}, "type REG_OLS or REG_GLM"},
		{"no intercept", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: xs, Intercept: new(bool), Collinearity: &types.RegressionCollinearity{}}, "intercept"},
		{"selection", glm(func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" }), "no_selection"},
		{"glm penalty", glm(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 }), "unpenalized"},
		{"one term", glm(groups(types.RegressionTermGroup{Name: "all", Predictors: xs})), "two_or_more_terms"},
		{"extension model", &types.RegressionSpec{Type: types.REG_MMM_HILL, Target: "y", Predictors: xs,
			Params: json.RawMessage(`{"channels":["x1"]}`), Collinearity: &types.RegressionCollinearity{}}, "type REG_OLS or REG_GLM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := marginsReq(marginsCohort, tc.spec)
			_, err := p.Process(ctx, req)
			ce := codedOf(t, err)
			if ce.Code != errors.PROCESSING_CONFIG || ce.Details["requires"] != tc.requires {
				t.Fatalf("Process: %v (details %v), want PROCESSING_CONFIG requires %q", err, ce.Details, tc.requires)
			}
			pr, perr := p.Predict(ctx, req)
			if perr != nil {
				t.Fatal(perr)
			}
			matched := false
			for _, e := range pr.Errors {
				if e.Code == string(ce.Code) && e.Message == ce.Message && jsonEqual(t, e.Details, ce.Details) {
					matched = true
				}
			}
			if pr.Valid || !matched {
				t.Errorf("predict errors %+v do not carry the runtime refusal %q %v", pr.Errors, ce.Message, ce.Details)
			}
		})
	}
}

// TestCollinearity_OneHotMatchesCar: a FEAT_ONE_HOT block's dummies
// grouped as one term give car::vif's generalized VIF, df and
// GVIF^(1/(2·df)) end to end (vif.json logit_group: glm(binomial) on
// y ~ x1 + g + x2), at 1e-10 relative.
func TestCollinearity_OneHotMatchesCar(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("internal/processing/testdata/reference/vif.json")
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	body = body[:strings.LastIndex(body, "\n// golden-hash: ")]
	type vifCase struct {
		Name       string      `json:"name"`
		Rows       [][]float64 `json:"rows"`
		Terms      []string    `json:"terms"`
		GVIF       []float64   `json:"gvif"`
		DF         []float64   `json:"df"`
		GVIFScaled []float64   `json:"gvif_scaled"`
	}
	var doc struct {
		Cases []vifCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(doc.Cases, func(c vifCase) bool { return c.Name == "logit_group" })
	if idx < 0 {
		t.Fatal("vif.json has no logit_group case")
	}
	c := doc.Cases[idx]
	g17 := func(v float64) string { return strconv.FormatFloat(v, 'g', 17, 64) }
	var rows [][]string
	for _, r := range c.Rows {
		level := "a"
		switch {
		case r[3] == 1:
			level = "b"
		case r[4] == 1:
			level = "c"
		}
		rows = append(rows, []string{g17(r[0]), g17(r[1]), level, g17(r[5])})
	}
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, "onehot.pulse", []string{"x1", "x2", "g", "y"}, rows)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	req := marginsReq("onehot.pulse", &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "binomial", Target: "y",
		Predictors: []string{"x1", "g_b", "g_c", "x2"}, Tol: 1e-13, MaxIters: 200,
		Collinearity: &types.RegressionCollinearity{Groups: []types.RegressionTermGroup{{Name: "g", Predictors: []string{"g_b", "g_c"}}}}})
	req.Features = []*types.Feature{{Type: types.FEAT_ONE_HOT, Field: "g"}}
	if pr, err := p.Predict(context.Background(), req); err != nil || !pr.Valid {
		t.Fatalf("predict: %v %+v", err, pr.Errors)
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	col := resp.Regressions[0].Collinearity
	if col == nil {
		t.Fatalf("no collinearity: %+v", resp.Regressions[0].Error)
	}
	if !slices.Equal(col.Terms, c.Terms) {
		t.Fatalf("terms %v, car %v", col.Terms, c.Terms)
	}
	for i, k := range c.Terms {
		if float64(col.DF[k]) != c.DF[i] {
			t.Errorf("df[%s] = %d, car %v", k, col.DF[k], c.DF[i])
		}
		for _, f := range []struct {
			what      string
			got, want float64
		}{{"vif", col.VIF[k], c.GVIF[i]}, {"tolerance", col.Tolerance[k], 1 / c.GVIF[i]}, {"gvif_scaled", col.GVIFScaled[k], c.GVIFScaled[i]}} {
			if e := relErr(f.got, f.want); e > 1e-10 {
				t.Errorf("%s[%s] = %.17g, car %.17g (rel %.3g)", f.what, k, f.got, f.want, e)
			}
		}
	}
}
