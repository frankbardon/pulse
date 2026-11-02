package regordinal_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/regordinal"
	"github.com/frankbardon/pulse/types"
)

// ordinal_e2e_test.go runs REG_ORDINAL end to end — a cohort, a
// regression slot, Process — against the R golden (ordinal.json, MASS
// polr / brant / sandwich; see oracle_test.go), and pins the operator's
// contract around the fit: the shared slots it refuses identically at
// predict and run, the data refusals that fail the slot alone, the
// level order, frequency weights against the expansion, and worker
// invariance.

const e2eRel = 1e-10

// z975 is the standard normal 0.975 quantile, the conf_level 0.95
// interval's critical value (R qnorm(0.975)).
const z975 = 1.959963984540054

func newInstance(t testing.TB, mut func(*pulse.Options)) *pulse.Pulse {
	t.Helper()
	opts := pulse.Options{FS: afero.NewMemMapFs(), DisablePanicRecovery: true}
	if mut != nil {
		mut(&opts)
	}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

// cohortSpec describes one test cohort: p predictors x1..xp (f64), the
// response y (u8, or categorical_u8 over dict), a weight w (f64) and a
// cluster id g (u8).
type cohortSpec struct {
	p    int
	dict []string // nil: a numeric y
}

func (c cohortSpec) schema() encoding.Schema {
	var fields []encoding.Field
	for j := 1; j <= c.p; j++ {
		fields = append(fields, encoding.Field{Name: "x" + strconv.Itoa(j), Type: encoding.FieldTypeF64})
	}
	if c.dict != nil {
		d := encoding.NewDictionary()
		for _, l := range c.dict {
			_, _ = d.Add(l)
		}
		fields = append(fields, encoding.Field{Name: "y", Type: encoding.FieldTypeCategoricalU8, Dictionary: d})
	} else {
		fields = append(fields, encoding.Field{Name: "y", Type: encoding.FieldTypeU8})
	}
	fields = append(fields,
		encoding.Field{Name: "w", Type: encoding.FieldTypeF64},
		encoding.Field{Name: "g", Type: encoding.FieldTypeU8})
	s := encoding.Schema{Fields: fields}
	off := 0
	for i := range s.Fields {
		s.Fields[i].CsvColumnIdx = i
		s.Fields[i].ByteOffset = off
		s.Fields[i].Description = "Ordinal regression test column " + s.Fields[i].Name + "."
		off += s.FieldWidth(i)
	}
	return s
}

// writeCohort writes rows ([x..., y, w, g], y the level index 1..K or,
// with labels, labels[y-1]) to name.
func writeCohort(t testing.TB, p *pulse.Pulse, name string, cs cohortSpec, rows [][]float64, labels []string) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), name, cs.schema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		row := pulse.CohortRow{}
		for j := 0; j < cs.p; j++ {
			row = append(row, r[j])
		}
		if labels != nil {
			row = append(row, labels[int(r[cs.p])-1])
		} else {
			row = append(row, uint64(r[cs.p]))
		}
		row = append(row, r[cs.p+1], uint64(r[cs.p+2]))
		if err := b.Append(row); err != nil {
			t.Fatalf("Append row %d: %v", i, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// caseSpec is the regression slot the golden case describes.
func caseSpec(c regordinal.OracleCase, levelsParam []string) *types.RegressionSpec {
	params := map[string]any{}
	if c.Link != regordinal.LinkLogit {
		params["link"] = c.Link
	}
	if c.Brant != nil {
		params["brant"] = map[string]any{}
	}
	if levelsParam != nil {
		params["levels"] = levelsParam
	}
	s := &types.RegressionSpec{Type: types.REG_ORDINAL, Name: "ord", Target: "y", Predictors: c.Predictors, Vcov: true}
	if len(params) > 0 {
		raw, _ := json.Marshal(params)
		s.Params = raw
	}
	if c.Covariance != "classical" {
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceType(c.Covariance)}
		if c.Covariance == "cluster" {
			s.Covariance.Cluster = "g"
		}
	}
	switch c.Weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	}
	return s
}

func fitOne(t testing.TB, p *pulse.Pulse, cohort string, s *types.RegressionSpec) *types.RegressionResult {
	t.Helper()
	resp, err := p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{s}})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		b, _ := json.Marshal(resp.Regressions)
		t.Fatalf("regressions = %s", b)
	}
	return resp.Regressions[0]
}

func rel(got, want float64) float64 {
	return math.Abs(got-want) / math.Max(math.Abs(want), 1e-12)
}

// checkAgainstGolden compares a result with its golden case.
func checkAgainstGolden(t *testing.T, c regordinal.OracleCase, res *types.RegressionResult) {
	t.Helper()
	check := func(what string, got, want float64) {
		t.Helper()
		if e := rel(got, want); e > e2eRel {
			t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want, e)
		}
	}
	p := len(c.Predictors)
	var thresholds []string
	for k := 0; k+1 < len(c.Levels); k++ {
		thresholds = append(thresholds, c.Levels[k]+"|"+c.Levels[k+1])
	}
	names := append(slices.Clone(c.Predictors), thresholds...)
	if len(res.Coefficients) != len(names) {
		t.Fatalf("coefficients %v, want the keys %v", res.Coefficients, names)
	}
	for j, name := range names {
		est, se := c.Coefficients[j], c.StdErrors[j]
		check("coefficient "+name, res.Coefficients[name], est)
		check("std_error "+name, res.StdErrors[name], se)
		check("statistic "+name, res.Statistics[name], est/se)
		check("ci_lower "+name, res.CILower[name], est-z975*se)
		check("ci_upper "+name, res.CIUpper[name], est+z975*se)
		if j < p {
			check("p_value "+name, res.PValues[name], math.Erfc(math.Abs(est/se)/math.Sqrt2))
		} else if !math.IsNaN(res.PValues[name]) {
			t.Errorf("threshold %s carries p-value %v, want null", name, res.PValues[name])
		}
		for k, other := range names {
			check("vcov "+name+","+other, res.Vcov.Values[j][k], c.Vcov[j][k])
		}
	}
	check("deviance", res.Deviance, c.Deviance)
	check("null_deviance", res.NullDeviance, c.NullDeviance)
	check("pseudo_r2", res.PseudoR2, 1-c.Deviance/c.NullDeviance)
	if c.TwoLogLik != nil {
		check("two_log_lik", res.TwoLogLik, *c.TwoLogLik)
		check("aic", res.AIC, *c.AIC)
	} else if res.TwoLogLik != 0 || res.AIC != 0 {
		t.Errorf("probability-weighted fit reports two_log_lik %v / aic %v, want absent", res.TwoLogLik, res.AIC)
	}
	if res.NObs != c.NObs {
		t.Errorf("n_obs = %d, want %d", res.NObs, c.NObs)
	}
	out := res.Outputs
	if !slices.Equal(anyStrings(out["levels"]), c.Levels) || !slices.Equal(anyStrings(out["thresholds"]), thresholds) {
		t.Errorf("outputs levels %v / thresholds %v, want %v / %v", out["levels"], out["thresholds"], c.Levels, thresholds)
	}
	fp, _ := out["fitted_probabilities"].(map[string]any)
	for k, lv := range c.Levels {
		v, _ := fp[lv].(float64)
		check("fitted_probability "+lv, v, c.FittedProbabilities[k])
	}
	if c.Brant == nil {
		if _, ok := out["brant"]; ok {
			t.Errorf("outputs.brant present without params.brant")
		}
		return
	}
	b, _ := out["brant"].(map[string]any)
	check("brant statistic", b["statistic"].(float64), c.Brant.Statistic)
	check("brant p", b["p_value"].(float64), c.Brant.PValue)
	if b["df"].(float64) != c.Brant.DF {
		t.Errorf("brant df %v, want %v", b["df"], c.Brant.DF)
	}
	terms, _ := b["terms"].(map[string]any)
	for a, name := range c.Predictors {
		tm, _ := terms[name].(map[string]any)
		check("brant "+name, tm["statistic"].(float64), c.Brant.Terms[a])
		check("brant p "+name, tm["p_value"].(float64), c.Brant.TermsP[a])
	}
}

func anyStrings(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, len(s))
		for i, x := range s {
			out[i], _ = x.(string)
		}
		return out
	}
	return nil
}

// TestRegOrdinal_MatchesPolrEndToEnd: every golden case through Process
// — the numeric and categorical response, each link, the robust
// covariances, frequency and probability weights, the Brant block, and
// params.levels dropping a response value it does not list (R's factor
// NA) — matches R at 1e-10.
func TestRegOrdinal_MatchesPolrEndToEnd(t *testing.T) {
	t.Parallel()
	for _, c := range regordinal.LoadOracle(t) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			p := newInstance(t, nil)
			cs := cohortSpec{p: len(c.Predictors)}
			var labels []string
			if c.Levels[0] != "1" {
				cs.dict, labels = c.Levels, c.Levels
			}
			rows := c.Rows
			if c.LevelsParam != nil {
				// Rows at a value params.levels does not list: dropped.
				for i := 0; i < 25; i++ {
					extra := slices.Clone(rows[i])
					extra[cs.p] = 0
					extra[0] = float64(i) / 7
					rows = append(rows, extra)
				}
			}
			writeCohort(t, p, "ord.pulse", cs, rows, labels)
			checkAgainstGolden(t, c, fitOne(t, p, "ord.pulse", caseSpec(c, c.LevelsParam)))
		})
	}
}

// goldenCase returns the named golden case.
func goldenCase(t *testing.T, name string) regordinal.OracleCase {
	t.Helper()
	for _, c := range regordinal.LoadOracle(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no golden case %s", name)
	return regordinal.OracleCase{}
}

// TestRegOrdinal_LevelOrder: a categorical response fits in its
// dictionary order, and params.levels reorders a scrambled dictionary
// to the same fit bit for bit; a numeric response orders its values
// ascending, whatever order the rows carry.
func TestRegOrdinal_LevelOrder(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "logit_four_labeled")
	p := newInstance(t, nil)
	writeCohort(t, p, "dict.pulse", cohortSpec{p: 3, dict: c.Levels}, c.Rows, c.Levels)
	writeCohort(t, p, "scrambled.pulse", cohortSpec{p: 3, dict: []string{"mid", "top", "low", "high"}}, c.Rows, c.Levels)
	base := fitOne(t, p, "dict.pulse", caseSpec(c, nil))
	checkAgainstGolden(t, c, base)
	ordered := fitOne(t, p, "scrambled.pulse", caseSpec(c, c.Levels))
	if a, b := mustJSON(t, base), mustJSON(t, ordered); a != b {
		t.Errorf("params.levels over a scrambled dictionary differs from the dictionary order:\n%s\n%s", a, b)
	}
	// Without params.levels the scrambled dictionary is the order: a
	// different (reversed-in-part) scale, so a different fit.
	scr := fitOne(t, p, "scrambled.pulse", caseSpec(c, nil))
	if got := anyStrings(scr.Outputs["levels"]); !slices.Equal(got, []string{"mid", "top", "low", "high"}) {
		t.Errorf("dictionary order levels = %v", got)
	}
}

// TestRegOrdinal_UnitFrequencyWeightIsUnweighted: a frequency weight of
// 1 on every row is the unweighted fit bit for bit (the floor keys
// aside).
func TestRegOrdinal_UnitFrequencyWeightIsUnweighted(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "logit_two")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	plain := fitOne(t, p, "ord.pulse", caseSpec(c, nil))
	c.Weight = "frequency"
	weighted := fitOne(t, p, "ord.pulse", caseSpec(c, nil))
	if weighted.SumWeights != float64(c.NObs) {
		t.Errorf("sum_weights = %v, want %d", weighted.SumWeights, c.NObs)
	}
	weighted.SumWeights = 0
	if a, b := mustJSON(t, plain), mustJSON(t, weighted); a != b {
		t.Errorf("unit frequency weights differ from the unweighted fit:\n%s\n%s", a, b)
	}
}

// TestRegOrdinal_WorkerInvariant: the fit is byte-identical across
// DecodeWorkers / ShardWorkers counts.
func TestRegOrdinal_WorkerInvariant(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "logit_four_labeled")
	var first string
	for _, w := range []int{1, 2, 7} {
		p := newInstance(t, func(o *pulse.Options) { o.DecodeWorkers, o.ShardWorkers = w, w })
		writeCohort(t, p, "ord.pulse", cohortSpec{p: 3, dict: c.Levels}, c.Rows, c.Levels)
		got := mustJSON(t, fitOne(t, p, "ord.pulse", caseSpec(c, nil)))
		if first == "" {
			first = got
		} else if got != first {
			t.Errorf("workers %d: result differs:\n%s\n%s", w, first, got)
		}
	}
}

// TestRegOrdinal_OutputsMatchApplicability: a plain fit emits exactly
// the RegressionResult keys the applicability row lists.
func TestRegOrdinal_OutputsMatchApplicability(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "logit_two")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	s := caseSpec(c, nil)
	s.Vcov = false
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(mustJSON(t, fitOne(t, p, "ord.pulse", s))), &keys); err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range keys {
		if k != "name" && k != "type" {
			got = append(got, k)
		}
	}
	sort.Strings(got)
	if want := descx.RegressionOutputKeys(string(types.REG_ORDINAL)); !slices.Equal(got, want) {
		t.Fatalf("a plain fit emits %v, the applicability row lists %v", got, want)
	}
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRegOrdinal_RefusalsPredictMatchRuntime: every RegressionSpec block
// REG_ORDINAL does not take, and every malformed params key, is refused
// by predict and by the runtime with the same code, message and
// details; the blocks it does take run.
func TestRegOrdinal_RefusalsPredictMatchRuntime(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "logit_two")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	ctx := context.Background()
	req := func(extra string) *types.Request {
		var s types.RegressionSpec
		body := `{"type":"REG_ORDINAL","name":"ord","target":"y","predictors":["x1","x2"]` + extra + `}`
		if err := json.Unmarshal([]byte(body), &s); err != nil {
			t.Fatal(err)
		}
		return &types.Request{Cohort: &types.Cohort{Filename: "ord.pulse"}, Regressions: []*types.RegressionSpec{&s}}
	}
	refused := map[string]struct {
		extra string
		code  perr.Code
		slot  string
	}{
		"penalty":        {`,"penalty":"l2","alpha":0.1`, perr.PROCESSING_CONFIG, "penalty"},
		"selection":      {`,"selection":"forward","criterion":"aic"`, perr.PROCESSING_CONFIG, "selection"},
		"resample":       {`,"resample":"jackknife"`, perr.PROCESSING_CONFIG, "resample"},
		"bootstrap":      {`,"resample":"bootstrap"`, perr.PROCESSING_CONFIG, "resample"},
		"margins":        {`,"margins":{}`, perr.PROCESSING_CONFIG, "margins"},
		"collinearity":   {`,"collinearity":{}`, perr.PROCESSING_CONFIG, "collinearity"},
		"covariance_hc3": {`,"covariance":{"type":"hc3"}`, perr.PROCESSING_CONFIG, "covariance"},
		"covariance_hac": {`,"covariance":{"type":"hac","order_by":"x1"}`, perr.PROCESSING_CONFIG, "covariance"},
		"save":           {`,"save":"m.pulse-model.json"`, perr.PROCESSING_CONFIG, "save"},
		"link_top_level": {`,"link":"probit"`, perr.PROCESSING_CONFIG, "link"},
		"family":         {`,"family":"binomial"`, perr.PROCESSING_CONFIG, "family"},
		"max_iters":      {`,"max_iters":10`, perr.PROCESSING_CONFIG, "max_iters"},
		"intercept":      {`,"intercept":false`, perr.PROCESSING_CONFIG, "intercept"},
		"bounds":         {`,"bounds":{"nonnegative":["x1"]}`, perr.PROCESSING_CONFIG, "bounds"},
		"prior":          {`,"prior":"nig"`, perr.PROCESSING_CONFIG, "prior"},
		"holdout":        {`,"holdout":"x1 > 0"`, perr.PULSE_MODEL_SCORE_INVALID, ""},
		"params_link":    {`,"params":{"link":"cauchit"}`, perr.PROCESSING_CONFIG, "params.link"},
		"params_levels":  {`,"params":{"levels":["1","2","2"]}`, perr.PROCESSING_CONFIG, "params.levels"},
		"params_two":     {`,"params":{"levels":["1","2"]}`, perr.PROCESSING_CONFIG, "params.levels"},
		"params_brant":   {`,"params":{"link":"probit","brant":{}}`, perr.PROCESSING_CONFIG, "params.brant"},
		"params_unknown": {`,"params":{"lnk":"logit"}`, perr.PULSE_REQUEST_UNKNOWN_FIELD, ""},
		"brant_key":      {`,"params":{"brant":{"by":"x1"}}`, perr.PROCESSING_CONFIG, "params"},
		"conf_level":     {`,"conf_level":2`, perr.PROCESSING_CONFIG, ""},
	}
	for name, rc := range refused {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := req(rc.extra)
			pr, err := p.Predict(ctx, r)
			if err != nil {
				t.Fatalf("Predict: %v", err)
			}
			_, runErr := p.Process(ctx, r)
			if len(pr.Errors) == 0 || runErr == nil {
				t.Fatalf("predict errors %v, runtime %v: want both refused", pr.Errors, runErr)
			}
			var ce *perr.CodedError
			if !asCoded(runErr, &ce) {
				t.Fatalf("runtime error %v is not coded", runErr)
			}
			got := pr.Errors[0]
			if got.Code != string(ce.Code) || got.Message != ce.Message || mustJSON(t, got.Details) != mustJSON(t, ce.Details) {
				t.Errorf("predict first error %s %q %v, runtime %s %q %v", got.Code, got.Message, got.Details, ce.Code, ce.Message, ce.Details)
			}
			if ce.Code != rc.code {
				t.Errorf("code %s, want %s (%s)", ce.Code, rc.code, ce.Message)
			}
			if rc.slot != "" && ce.Details["slot"] != rc.slot {
				t.Errorf("details.slot %v, want %q (%v)", ce.Details["slot"], rc.slot, ce.Details)
			}
		})
	}
	// The blocks REG_ORDINAL takes run, predict accepting them.
	for name, extra := range map[string]string{
		"vcov":         `,"vcov":true`,
		"hc0":          `,"covariance":{"type":"hc0"}`,
		"hc1":          `,"covariance":{"type":"hc1"}`,
		"cluster":      `,"covariance":{"type":"cluster","cluster":"g"}`,
		"conf_level":   `,"conf_level":0.9`,
		"multiplicity": `,"multiplicity":{"method":"holm"}`,
		"weight":       `,"weight":{"field":"w","kind":"probability"}`,
		"probit":       `,"params":{"link":"probit"}`,
		"cloglog":      `,"params":{"link":"cloglog"}`,
		"brant":        `,"params":{"brant":{}}`,
		"levels":       `,"params":{"levels":["1","2","3"]}`,
	} {
		t.Run("runs_"+name, func(t *testing.T) {
			t.Parallel()
			r := req(extra)
			pr, err := p.Predict(ctx, r)
			if err != nil || len(pr.Errors) != 0 {
				t.Fatalf("predict refused: %v %+v", err, pr.Errors)
			}
			resp, err := p.Process(ctx, r)
			if err != nil || resp.Regressions[0].Error != nil {
				t.Fatalf("runtime refused: %v %s", err, mustJSON(t, resp))
			}
		})
	}
}

func asCoded(err error, out **perr.CodedError) bool {
	ce, ok := err.(*perr.CodedError)
	if ok {
		*out = ce
		return true
	}
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok && u.Unwrap() != nil {
		return asCoded(u.Unwrap(), out)
	}
	return false
}

// TestRegOrdinal_DataRefusalsFailAlone: a response with fewer than
// three levels, a params.levels entry no fitted row carries and a
// predictor that separates the levels each fail the regression slot
// alone (PULSE_SLOT_FAILED, the slot's own code), predict accepting the
// request (the data decide). A single cluster is the built-ins' fatal
// refusal (regcov.TooFewClusters), here too.
func TestRegOrdinal_DataRefusalsFailAlone(t *testing.T) {
	t.Parallel()
	c := goldenCase(t, "logit_two")
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	// two.pulse: the response folded to two levels; sep.pulse: x1 is the
	// level itself (complete separation); one.pulse: one cluster.
	two, sep, one := make([][]float64, len(c.Rows)), make([][]float64, len(c.Rows)), make([][]float64, len(c.Rows))
	for i, r := range c.Rows {
		two[i], sep[i], one[i] = slices.Clone(r), slices.Clone(r), slices.Clone(r)
		two[i][2] = math.Min(r[2], 2)
		sep[i][0] = r[2] + float64(i%5)/100
		one[i][4] = 1
	}
	writeCohort(t, p, "two.pulse", cohortSpec{p: 2}, two, nil)
	writeCohort(t, p, "sep.pulse", cohortSpec{p: 2}, sep, nil)
	writeCohort(t, p, "one.pulse", cohortSpec{p: 2}, one, nil)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		cohort, extra string
		code          perr.Code
		reason        string
		fatal         bool
	}{
		"two_levels":  {"two.pulse", "", perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA, "fewer_than_three_levels", false},
		"empty_level": {"ord.pulse", `,"params":{"levels":["1","2","3","4"]}`, perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA, "empty_level", false},
		"separation":  {"sep.pulse", "", perr.PROCESSING_REGRESSION_NO_CONVERGE, "", false},
		"one_cluster": {"one.pulse", `,"covariance":{"type":"cluster","cluster":"g"}`, perr.PROCESSING_REGRESSION_VCOV_UNSUPPORTED, "clusters", true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var s types.RegressionSpec
			if err := json.Unmarshal([]byte(`{"type":"REG_ORDINAL","name":"ord","target":"y","predictors":["x1","x2"]`+tc.extra+`}`), &s); err != nil {
				t.Fatal(err)
			}
			r := &types.Request{Cohort: &types.Cohort{Filename: tc.cohort}, Regressions: []*types.RegressionSpec{&s}}
			if pr, err := p.Predict(ctx, r); err != nil || len(pr.Errors) != 0 {
				t.Fatalf("predict refused: %v %+v", err, pr.Errors)
			}
			resp, err := p.Process(ctx, r)
			if tc.fatal {
				var ce *perr.CodedError
				if !asCoded(err, &ce) || ce.Code != tc.code || ce.Details["reason"] != tc.reason {
					t.Fatalf("Process: %v, want the fatal %s (%s)", err, tc.code, tc.reason)
				}
				return
			}
			if err != nil {
				t.Fatalf("Process: %v (the slot should fail alone)", err)
			}
			res := resp.Regressions[0]
			if res.Error == nil || res.Error.Code != string(tc.code) {
				t.Fatalf("slot error %s, want %s", mustJSON(t, res.Error), tc.code)
			}
			if tc.reason != "" && fmt.Sprint(res.Error.Details["reason"]) != tc.reason {
				t.Errorf("details.reason %v, want %s", res.Error.Details["reason"], tc.reason)
			}
			if len(resp.Warnings) == 0 || resp.Warnings[0].Code != string(perr.PULSE_SLOT_FAILED) {
				t.Errorf("warnings %s, want PULSE_SLOT_FAILED", mustJSON(t, resp.Warnings))
			}
		})
	}
}
