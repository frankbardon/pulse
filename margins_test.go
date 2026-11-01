package pulse

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
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
// buffered arms, a multiplicity block corrects the effects in the
// regression's family (and predict counts them), and an absent block is
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
// coded error — code, message and details — at predict and at run.
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
		{"derived predictor", func() *Request {
			r := marginsReq(marginsCohort, glm(func(s *types.RegressionSpec) { s.Predictors = []string{"x1", "x2_log"} }))
			r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "c", Label: "x2_log"}}
			return r
		}(), "schema_predictors"},
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

// TestMargins_MultiplicityJoinsRegressionFamily: under the regression's
// own multiplicity block the effects join its family after the member
// coefficients — m counts both, each adjusted p is the method's over the
// pooled family — and predict counts the same p-values.
func TestMargins_MultiplicityJoinsRegressionFamily(t *testing.T) {
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
	ps := []float64{r.PValues["x1"], r.PValues["x2"], r.Margins.PValues["x1"], r.Margins.PValues["x2"]}
	adj, err := multiplicity.Adjust(multiplicity.Method(types.MultiplicityMethodHolm), ps)
	if err != nil {
		t.Fatal(err)
	}
	got := []float64{r.PAdjusted["x1"], r.PAdjusted["x2"], r.Margins.PAdjusted["x1"], r.Margins.PAdjusted["x2"]}
	if !slices.Equal(got, adj) {
		t.Errorf("adjusted %v, Holm over the pooled family %v", got, adj)
	}
	if r.Multiplicity == nil || r.Multiplicity.M != 4 {
		t.Errorf("echo %+v, want m = 4", r.Multiplicity)
	}
	for _, k := range []string{"x1", "x2"} {
		if r.Margins.SignificantAdjusted[k] != (r.Margins.PAdjusted[k] < r.Multiplicity.Alpha) {
			t.Errorf("significant_adjusted[%s] = %v", k, r.Margins.SignificantAdjusted[k])
		}
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
	if plain.Regressions[0].Margins.PAdjusted != nil {
		t.Error("an uncorrected regression's margins carry p_adjusted")
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
