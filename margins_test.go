package pulse

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
)

// margins_test.go covers RegressionSpec.Margins (U65 E3-S1) end to end:
// every refusal is the same coded error at predict and at run
// (internal/regmargins), the block answers bit for bit the same under
// every DecodeWorkers / ShardWorkers count and on OLS's streaming and
// buffered arms, a multiplicity block corrects the effects in a family
// of their own (and predict counts them), and an absent block is
// byte-identical. The figures themselves are pinned to R by
// internal/processing/regression/margins_oracle_test.go.

const (
	marginsCohort  = "margins.pulse"
	marginsRows    = 120
	marginsPerShrd = 40
)

var marginsColumns = []string{"id", "x1", "x2", "y", "b", "c"}

// marginsRow is row i: two predictors, a continuous, a 0/1 and a count
// target. Products are barriered so every GOARCH builds the same rows.
func marginsRow(i int) []string {
	fi := float64(i)
	x1 := float64(fi*0.05) - 3
	x2 := math.Sin(float64(0.41*fi) + 0.3)
	y := 2 + float64(0.7*x1) - float64(0.9*x2) + float64(0.4*math.Sin(float64(1.7*fi)))
	b := 0.0
	if float64(0.8*x1)-float64(0.6*x2)+float64(1.3*math.Sin(float64(2.1*fi))) > 0 {
		b = 1
	}
	c := math.Round(math.Exp(0.3+float64(0.2*x1)+float64(0.25*x2)) + float64(0.7*math.Sin(float64(0.83*fi))))
	if c < 0 {
		c = 0
	}
	return []string{strconv.Itoa(i), jsonNumber(x1), jsonNumber(x2), jsonNumber(y), jsonNumber(b), jsonNumber(c)}
}

// marginsModes is the cohort as one file (serial decode and 4 decode
// workers) and as a 3-shard archive (1 and 3 shard workers).
func marginsModes(t *testing.T) (single, sharded []scorerParityMode) {
	t.Helper()
	var rows [][]string
	for i := 0; i < marginsRows; i++ {
		rows = append(rows, marginsRow(i))
	}
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, marginsCohort, marginsColumns, rows)
	sfs := afero.NewMemMapFs()
	var shards []string
	for s := 0; s*marginsPerShrd < marginsRows; s++ {
		path := "s" + strconv.Itoa(s) + ".pulse"
		createTestPulseFile(t, sfs, path, marginsColumns, rows[s*marginsPerShrd:(s+1)*marginsPerShrd])
		shards = append(shards, path)
	}
	mk := func(fs afero.Fs, o Options) *Pulse {
		o.FS = fs
		p, err := New(o)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	single = []scorerParityMode{
		{"serial", mk(fsys, Options{DecodeWorkers: 1}), marginsCohort},
		{"decode_workers_4", mk(fsys, Options{DecodeWorkers: 4}), marginsCohort},
	}
	sp := mk(sfs, Options{ShardWorkers: 1})
	if _, err := sp.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	sharded = []scorerParityMode{
		{"shard_workers_1", sp, "archive.pulse"},
		{"shard_workers_3", mk(sfs, Options{ShardWorkers: 3}), "archive.pulse"},
	}
	return single, sharded
}

func marginsReq(cohort string, specs ...*types.RegressionSpec) *Request {
	return &Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: specs}
}

// marginsSpecs are the fits the invariance covers: OLS ame / mem, logit
// ame / mem, a poisson link-scale and an HC3 logit block.
func marginsSpecs() []*types.RegressionSpec {
	xs := []string{"x1", "x2"}
	return []*types.RegressionSpec{
		{Type: types.REG_OLS, Name: "ols", Target: "y", Predictors: xs, Margins: &types.RegressionMargins{}},
		{Type: types.REG_OLS, Name: "ols_mem", Target: "y", Predictors: xs, Margins: &types.RegressionMargins{Type: types.MarginsMEM}},
		{Type: types.REG_GLM, Name: "logit", Family: "binomial", Target: "b", Predictors: xs, Margins: &types.RegressionMargins{}},
		{Type: types.REG_GLM, Name: "logit_mem", Family: "binomial", Target: "b", Predictors: xs, Margins: &types.RegressionMargins{Type: types.MarginsMEM, Terms: []string{"x2"}}},
		{Type: types.REG_GLM, Name: "pois_link", Family: "poisson", Target: "c", Predictors: xs, Margins: &types.RegressionMargins{Scale: types.MarginsScaleLink}},
		{Type: types.REG_GLM, Name: "logit_hc3", Family: "binomial", Target: "b", Predictors: xs, Margins: &types.RegressionMargins{},
			Covariance: &types.RegressionCovariance{Type: types.CovarianceHC3}},
	}
}

func marginsJSON(t *testing.T, resp *Response) string {
	t.Helper()
	var blocks []any
	for _, r := range resp.Regressions {
		if r.Error != nil {
			t.Fatalf("%s failed: %+v", r.Name, r.Error)
		}
		if r.Margins == nil {
			t.Fatalf("%s: no margins", r.Name)
		}
		blocks = append(blocks, r.Margins)
	}
	b, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestMargins_WorkerInvariant: the margins blocks are byte-identical
// across decode workers (one file) and shard workers (the archive), and
// OLS's streamed block equals its buffered one (an AGG_MEDIAN forces the
// buffered arm).
func TestMargins_WorkerInvariant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, sharded := marginsModes(t)
	for _, group := range [][]scorerParityMode{single, sharded} {
		var want string
		for _, m := range group {
			resp, err := m.p.Process(ctx, marginsReq(m.cohort, marginsSpecs()...))
			if err != nil {
				t.Fatalf("%s: %v", m.name, err)
			}
			got := marginsJSON(t, resp)
			if want == "" {
				want = got
			} else if got != want {
				t.Errorf("%s margins differ from %s:\n%s\nvs\n%s", m.name, group[0].name, got, want)
			}
		}
	}
	m := single[0]
	ols := marginsSpecs()[:2]
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
		t.Errorf("OLS streamed margins %s, buffered %s", a, b)
	}
}

// TestMargins_AbsentIsByteIdentical: a fit without a margins block
// answers byte for byte what the same fit with one answers, the block
// itself aside — computing the effects moves no other figure.
func TestMargins_AbsentIsByteIdentical(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	m := single[0]
	with := marginsSpecs()
	without := marginsSpecs()
	for _, s := range without {
		s.Margins = nil
		s.Vcov = true
	}
	for _, s := range with {
		s.Vcov = true
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
		r.Margins = nil
	}
	ja, _ := json.Marshal(a.Regressions)
	jb, _ := json.Marshal(b.Regressions)
	if string(ja) != string(jb) {
		t.Errorf("margins moved the fit:\n%s\nvs\n%s", ja, jb)
	}
}

// TestMargins_PredictMatchesRuntime: every margins refusal is the same
// coded error — code, message and details — at predict and at run,
// the dependent-predictor rule (the field-reference walk's step 13)
// included; a derived predictor that reads no other predictor passes
// both.
func TestMargins_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	xs := []string{"x1", "x2"}
	glm := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "binomial", Target: "b", Predictors: xs, Margins: &types.RegressionMargins{}}
		mut(s)
		return s
	}
	for _, tc := range []struct {
		name     string
		req      *Request
		requires string
	}{
		{"type", marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Margins.Type = "aie" })), "type ame or mem"},
		{"scale", marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Margins.Scale = "odds" })), "scale response or link"},
		{"unknown term", marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Margins.Terms = []string{"x3"} })), "a predictor"},
		{"repeated term", marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Margins.Terms = []string{"x1", "x1"} })), "a predictor"},
		{"bayes", marginsReq(marginsCohort, &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: xs, Margins: &types.RegressionMargins{}}), "type REG_OLS or REG_GLM"},
		{"penalty", marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 })), "unpenalized"},
		{"selection", marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" })), "no_selection"},
		{"resample", marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Resample = "jackknife" })), "no_resample"},
		{"bounds", marginsReq(marginsCohort, &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: xs, Margins: &types.RegressionMargins{},
			Bounds: &types.RegressionBounds{Nonnegative: []string{"x1"}}}), "unbounded"},
		{"predictor derived from a predictor", func() *Request {
			r := marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Predictors = []string{"x1", "x1_log"} }))
			r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "x1", Label: "x1_log"}}
			return r
		}(), "independent_predictors"},
		{"interaction of predictors", func() *Request {
			r := marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Predictors = []string{"x1", "x2", "x12"} }))
			r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "x12", Expression: "x1 * x2"}}
			return r
		}(), "independent_predictors"},
		{"window over a predictor", func() *Request {
			r := marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Predictors = []string{"x1", "x1_lag"} }))
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "x1", Label: "x1_lag", OrderBy: []types.OrderKey{{Field: "id"}}}}
			return r
		}(), "independent_predictors"},
		{"chained dependence", func() *Request {
			r := marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Predictors = []string{"x2", "lc2"} }))
			r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "x2", Label: "lx2"}}
			r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "lc2", Expression: "lx2 + c"}}
			return r
		}(), "independent_predictors"},
		{"extension model", marginsReq(marginsCohort, &types.RegressionSpec{Type: types.REG_MMM_HILL, Target: "y", Predictors: xs,
			Params: json.RawMessage(`{"channels":["x1"]}`), Margins: &types.RegressionMargins{}}), "type REG_OLS or REG_GLM"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Process(ctx, tc.req)
			ce := codedOf(t, err)
			if ce.Code != errors.PROCESSING_CONFIG || ce.Details["requires"] != tc.requires {
				t.Fatalf("Process: %v (details %v), want PROCESSING_CONFIG requires %q", err, ce.Details, tc.requires)
			}
			pr, perr := p.Predict(ctx, tc.req)
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

// TestMargins_DerivedPredictorAllowed: a derived predictor that reads
// no other predictor of the regression (FEAT_LOG of a non-predictor, a
// one-hot dummy, a formula over non-predictors) gets its effect — the
// slope in that column, as given — at predict and run alike; on the
// identity link of REG_OLS that is the coefficient, bit for bit.
func TestMargins_DerivedPredictorAllowed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	req := marginsReq(marginsCohort,
		&types.RegressionSpec{Type: types.REG_OLS, Name: "ols", Target: "y", Predictors: []string{"x1", "c_log", "cb"}, Margins: &types.RegressionMargins{}},
		&types.RegressionSpec{Type: types.REG_GLM, Name: "logit", Family: "binomial", Target: "b", Predictors: []string{"x1", "c_log"}, Margins: &types.RegressionMargins{}})
	req.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "c", Label: "c_log"}}
	req.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "cb", Expression: "c > 2 ? 1.0 : 0.0"}}
	pr, err := p.Predict(ctx, req)
	if err != nil || !pr.Valid {
		t.Fatalf("predict: %v %+v", err, pr.Errors)
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	ols, logit := resp.Regressions[0], resp.Regressions[1]
	for _, k := range []string{"x1", "c_log", "cb"} {
		if ols.Margins.Estimates[k] != ols.Coefficients[k] || ols.Margins.StdErrors[k] != ols.StdErrors[k] {
			t.Errorf("OLS margin %s = %v (se %v), coefficient %v (se %v)", k, ols.Margins.Estimates[k], ols.Margins.StdErrors[k], ols.Coefficients[k], ols.StdErrors[k])
		}
	}
	if e := logit.Margins.Estimates["c_log"]; math.IsNaN(e) || e == 0 {
		t.Errorf("logit margin over the derived column: %v", e)
	}
}

// TestMargins_DerivedPredictorsMatchR: margins over a FEAT_LOG output
// (log1p of a raw column) and a FEAT_ONE_HOT dummy equal R's analytic
// AME on the same pre-computed columns (margins.json logit_ame_derived)
// at 1e-9 relative: the effect of a unit change in each column, as
// given.
func TestMargins_DerivedPredictorsMatchR(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("internal/processing/testdata/reference/margins.json")
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	body = body[:strings.LastIndex(body, "\n// golden-hash: ")]
	type derivedCase struct {
		Name      string      `json:"name"`
		Rows      [][]float64 `json:"rows"`
		Raw       [][]float64 `json:"raw"`
		Estimates []float64   `json:"estimates"`
		StdErrors []float64   `json:"std_errors"`
		PValues   []float64   `json:"p_values"`
		CILower   []float64   `json:"ci_lower"`
		CIUpper   []float64   `json:"ci_upper"`
	}
	var doc struct {
		Cases []derivedCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(doc.Cases, func(c derivedCase) bool {
		return c.Name == "logit_ame_derived"
	})
	if idx < 0 {
		t.Fatal("margins.json has no logit_ame_derived case")
	}
	c := doc.Cases[idx]
	var rows [][]string
	for i, r := range c.Raw {
		rows = append(rows, []string{strconv.FormatFloat(r[0], 'g', 17, 64), []string{"a", "b"}[int(r[1])], strconv.FormatFloat(c.Rows[i][2], 'g', 17, 64)})
	}
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, "derived.pulse", []string{"z", "g", "y"}, rows)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	req := marginsReq("derived.pulse", &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "binomial", Target: "y",
		Predictors: []string{"z_log", "g_b"}, Margins: &types.RegressionMargins{}, Tol: 1e-13, MaxIters: 200})
	req.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "z", Label: "z_log"}, {Type: types.FEAT_ONE_HOT, Field: "g"}}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	mg := resp.Regressions[0].Margins
	if mg == nil {
		t.Fatalf("no margins: %+v", resp.Regressions[0].Error)
	}
	for i, k := range []string{"z_log", "g_b"} {
		for _, f := range []struct {
			what      string
			got, want float64
		}{{"estimate", mg.Estimates[k], c.Estimates[i]}, {"std_error", mg.StdErrors[k], c.StdErrors[i]}, {"p_value", mg.PValues[k], c.PValues[i]},
			{"ci_lower", mg.CILower[k], c.CILower[i]}, {"ci_upper", mg.CIUpper[k], c.CIUpper[i]}} {
			if e := relErr(f.got, f.want); e > 1e-9 {
				t.Errorf("%s[%s] = %.17g, R %.17g (rel %.3g)", f.what, k, f.got, f.want, e)
			} else {
				t.Logf("%s[%s]: rel error %.3g", f.what, k, e)
			}
		}
	}
}

// TestMargins_MultiplicityOwnFamily: under the regression's own
// multiplicity block the effects form a family of their own (one per
// regression, echo family "margins", m the terms), so the coefficients'
// p_adjusted, significant_adjusted and echo are byte-identical with
// margins and without; each effect's adjusted p is the method over the
// effects alone; predict counts every one as corrected and lists the
// margins family apart; a request block naming the echo-only family is
// refused alike at predict and run.
func TestMargins_MultiplicityOwnFamily(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	spec := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "binomial", Target: "b", Predictors: []string{"x1", "x2"},
		Margins:      &types.RegressionMargins{},
		Multiplicity: &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}}
	req := marginsReq(marginsCohort, spec)
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	r := resp.Regressions[0]
	adj, err := multiplicity.Adjust(multiplicity.Method(types.MultiplicityMethodHolm), []float64{r.Margins.PValues["x1"], r.Margins.PValues["x2"]})
	if err != nil {
		t.Fatal(err)
	}
	if got := []float64{r.Margins.PAdjusted["x1"], r.Margins.PAdjusted["x2"]}; !slices.Equal(got, adj) {
		t.Errorf("margins adjusted %v, Holm over the effects alone %v", got, adj)
	}
	if e := r.Margins.Multiplicity; e == nil || e.Family != types.MultiplicityFamilyMargins || e.M != 2 || e.Method != types.MultiplicityMethodHolm {
		t.Errorf("margins echo %+v, want holm / margins / m = 2", e)
	}
	for _, k := range []string{"x1", "x2"} {
		if r.Margins.SignificantAdjusted[k] != (r.Margins.PAdjusted[k] < r.Margins.Multiplicity.Alpha) {
			t.Errorf("significant_adjusted[%s] = %v", k, r.Margins.SignificantAdjusted[k])
		}
	}

	// The coefficients' correction is the one without margins, byte for
	// byte.
	bare := *spec
	bare.Margins = nil
	without, err := p.Process(ctx, marginsReq(marginsCohort, &bare))
	if err != nil {
		t.Fatal(err)
	}
	coefFold := func(r *types.RegressionResult) string {
		b, err := json.Marshal(map[string]any{"p_adjusted": r.PAdjusted, "significant_adjusted": r.SignificantAdjusted, "multiplicity": r.Multiplicity})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if g, w := coefFold(r), coefFold(without.Regressions[0]); g != w {
		t.Errorf("coefficients' correction moved with margins:\n with    %s\n without %s", g, w)
	}
	if r.Multiplicity == nil || r.Multiplicity.M != 2 || r.Multiplicity.Family != types.MultiplicityFamilyRequest {
		t.Errorf("coefficient echo %+v, want request / m = 2", r.Multiplicity)
	}

	pr, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if pr.PValues == nil || pr.PValues.Total != 4 || pr.PValues.Uncorrected != 0 {
		t.Errorf("predict p-values %+v, want 4, all corrected", pr.PValues)
	}
	// Without the block the effects stay uncorrected and are counted so.
	spec.Multiplicity = nil
	plain, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Regressions[0].Margins.PAdjusted != nil || plain.Regressions[0].Margins.Multiplicity != nil {
		t.Error("an uncorrected regression's margins carry p_adjusted or an echo")
	}

	// "margins" is an echo: a block naming it is refused, alike.
	spec.Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyMargins}
	_, err = p.Process(ctx, req)
	ce := codedOf(t, err)
	if ce.Code != errors.PULSE_MULTIPLICITY_INVALID {
		t.Fatalf("family margins: %v, want PULSE_MULTIPLICITY_INVALID", err)
	}
	pr, err = p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, e := range pr.Errors {
		if e.Code == string(ce.Code) && e.Message == ce.Message {
			matched = true
		}
	}
	if pr.Valid || !matched {
		t.Errorf("predict errors %+v do not carry the runtime refusal %q", pr.Errors, ce.Message)
	}
}

// TestMargins_MultiplicityComposeFamilies: inside Compose the margins
// family stays per regression — never pooled into `compose` — and
// predict's per-slot families list it apart from the coefficients'.
func TestMargins_MultiplicityComposeFamilies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	slot := func(name string) *Request {
		spec := &types.RegressionSpec{Type: types.REG_GLM, Name: name, Family: "binomial", Target: "b", Predictors: []string{"x1", "x2"},
			Margins:      &types.RegressionMargins{},
			Multiplicity: &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyCompose}}
		return marginsReq(marginsCohort, spec)
	}
	creq := &ComposedRequest{Requests: []*Request{slot("a"), slot("b")}}
	out, err := p.Compose(ctx, creq)
	if err != nil {
		t.Fatal(err)
	}
	for i, resp := range out.Responses {
		r := resp.Regressions[0]
		if r.Multiplicity == nil || r.Multiplicity.M != 4 || r.Multiplicity.Family != types.MultiplicityFamilyCompose {
			t.Errorf("slot %d coefficient echo %+v, want compose / m = 4", i, r.Multiplicity)
		}
		if e := r.Margins.Multiplicity; e == nil || e.M != 2 || e.Family != types.MultiplicityFamilyMargins {
			t.Errorf("slot %d margins echo %+v, want margins / m = 2", i, e)
		}
	}
	env, err := p.PredictCompose(ctx, creq)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("PredictCompose: %v %+v", err, env.Errors)
	}
	for i, s := range env.Data.(*ComposePredictResult).Slots {
		var fams []string
		for _, f := range s.Families {
			fams = append(fams, f.ID+"="+strconv.Itoa(f.Count))
		}
		want := []string{"compose=2", "requests[" + strconv.Itoa(i) + "].regressions[0]#margins=2"}
		if !slices.Equal(fams, want) {
			t.Errorf("slot %d predict families %v, want %v", i, fams, want)
		}
	}
}

// TestMargins_LinkScaleIsTheCoefficient: on the link scale (and on OLS,
// an identity link) every effect, standard error, p-value and bound is
// the coefficient table's own, bit for bit.
func TestMargins_LinkScaleIsTheCoefficient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	m := single[0]
	resp, err := m.p.Process(ctx, marginsReq(m.cohort, marginsSpecs()[0], marginsSpecs()[1], marginsSpecs()[4]))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resp.Regressions {
		mg := r.Margins
		for _, k := range mg.Terms {
			if mg.Estimates[k] != r.Coefficients[k] || mg.StdErrors[k] != r.StdErrors[k] || mg.PValues[k] != r.PValues[k] ||
				mg.CILower[k] != r.CILower[k] || mg.CIUpper[k] != r.CIUpper[k] {
				t.Errorf("%s[%s]: margins %v / %v / %v, coefficient %v / %v / %v", r.Name, k,
					mg.Estimates[k], mg.StdErrors[k], mg.PValues[k], r.Coefficients[k], r.StdErrors[k], r.PValues[k])
			}
		}
		if r.Type == types.REG_OLS && (mg.DF != r.DF || mg.DF == 0) {
			t.Errorf("%s: margins df %v, coefficient df %v", r.Name, mg.DF, r.DF)
		}
	}
}
