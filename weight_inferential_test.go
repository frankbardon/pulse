package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// inferentialRefusalRequests builds, per refused surface, a request
// over acceptanceCohort whose refused slot carries weight w (the zero
// SlotWeight inherits). Regular operators reuse acceptanceTemplates
// (field x); post-tests, crosstab-axis GROUP_QUANTILE and the
// inferential overlays are built here.
func inferentialRefusalRequests(t *testing.T, cohort string, w types.SlotWeight) map[string]*types.Request {
	t.Helper()
	field := map[string]string{"TEST_CHISQ": "subj", "TEST_FISHER_EXACT": "g", "TEST_PROP_Z": "g", "TEST_PAIRED_T": "y"}
	var ops []string
	for _, tt := range types.AllTestTypes() {
		if weighting.IsAware(string(tt)) {
			continue // computes weighted: TestWeight_DefaultInstanceWeightsTTest
		}
		ops = append(ops, string(tt))
	}
	for _, rt := range types.AllRegressionTypes() {
		if weighting.IsAware(string(rt)) {
			continue // fits weighted: TestWeight_RegressionsMatchPredict
		}
		ops = append(ops, string(rt))
	}
	// ATTR_ZSCORE / TSCORE and GROUP_QUANTILE compute weighted since U12
	// E5-S2 (TestWeight_ScoresAndQuantileMatchPredict).
	ops = append(ops, "ATTR_PERCENTILE")
	out := map[string]*types.Request{}
	decode := func(body string) *types.Request {
		var r types.Request
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("template %s: %v", body, err)
		}
		r.Cohort = &types.Cohort{Filename: cohort}
		return &r
	}
	for _, op := range ops {
		tmpl := acceptanceTemplates[op]
		if tmpl == "" {
			continue // post-test only: built below
		}
		f := field[op]
		if f == "" {
			f = "x"
		}
		r := decode(strings.NewReplacer("$F", f, "$S", "a").Replace(tmpl))
		switch {
		case len(r.Tests) > 0:
			r.Tests[0].Weight = w
		case len(r.Regressions) > 0:
			r.Regressions[0].Weight = w
		case len(r.Attributes) > 0:
			r.Attributes[0].Weight = w
			// The consuming aggregation must not itself refuse or
			// carry the weight being judged.
			r.Aggregations[0].Weight = types.NullSlotWeight()
		case len(r.Groups) > 0:
			r.Groups[0].Weight = w
			r.Aggregations[0].Weight = types.NullSlotWeight()
		}
		out[op] = r
	}
	for _, op := range []string{"TEST_TREND", "TEST_TUKEY_HSD"} {
		r := decode(`{"groups":[{"type":"GROUP_CATEGORY","field":"subj"}],"aggregations":[{"type":"AGG_AVERAGE","field":"x","label":"m","weight":null}],` +
			`"post_tests":[{"type":"` + op + `","field":"m","split_by":"subj","order_by":[{"field":"m"}],"params":{"ms_within":1.5,"df_within":20}}]}`)
		r.PostTests[0].Weight = w
		out["post/"+op] = r
	}
	// A post-test of a family whose tier-1 form is liftable: refused
	// permanently for being a post-test.
	welch := decode(`{"groups":[{"type":"GROUP_CATEGORY","field":"subj"}],"aggregations":[` +
		`{"type":"AGG_AVERAGE","field":"x","label":"m","weight":null},{"type":"AGG_COUNT","field":"x","label":"n","weight":null},` +
		`{"type":"AGG_VARIANCE","field":"x","label":"v","weight":null}],` +
		`"post_tests":[{"type":"TEST_ANOVA_WELCH","field":"m","split_by":"subj","params":{"n_col":"n","variance_col":"v"}}]}`)
	welch.PostTests[0].Weight = w
	out["post/TEST_ANOVA_WELCH"] = welch
	for _, mod := range []string{`"resample":"bootstrap","bootstrap_iters":20,"rng_seed":1`, `"selection":"forward","criterion":"aic"`} {
		r := decode(`{"regressions":[{"type":"REG_OLS","name":"r","target":"x","predictors":["y"],` + mod + `}]}`)
		r.Regressions[0].Weight = w
		out["modifier/"+strings.SplitN(mod, `"`, 3)[1]] = r
	}
	// The contingency and proportion overlays are lifted (E3-S2,
	// TestWeight_ContingencyOverlaysMatchPredict); the probit t stays
	// refused permanently.
	probit := decode(`{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"subj"}],` +
		`"cell":{"type":"AGG_COUNT","field":"x","weight":null},"margins":{"rows":true,"columns":true,"grand":true}},` +
		`"overlays":[{"kind":"OVERLAY_PAIRWISE_PROBIT_T","scope":"row"}]}`)
	probit.Overlays[0].Weight = w
	out["overlay/OVERLAY_PAIRWISE_PROBIT_T"] = probit
	return out
}

// permanentRefusalKeys are the inferentialRefusalRequests keys refused
// PERMANENTLY: the refusal carries details.reason and states it.
var permanentRefusalKeys = map[string]bool{
	"TEST_SHAPIRO_WILK": true, "TEST_ANOVA_RM": true, "ATTR_PERCENTILE": true,
	"post/TEST_TREND": true, "post/TEST_TUKEY_HSD": true, "post/TEST_ANOVA_WELCH": true,
	"modifier/resample": true, "modifier/selection": true,
	"overlay/OVERLAY_PAIRWISE_PROBIT_T": true,
}

// TestWeight_InferentialRefusalsMatchPredict: every refused inferential
// surface — each refused built-in TEST_* (tests and post-tests), REG_*
// (plain and with a resample / selection modifier), ATTR_PERCENTILE and
// the refused Inferential overlays — is refused PULSE_WEIGHT_UNSUPPORTED at runtime
// and predict with the same code, message and details, under the
// instance default, a request weight and a slot weight alike; a
// permanent refusal states its reason (details.reason), a pending one
// says it has no weighted form yet, and none names the roadmap. The
// slot's `weight: null` opts out on both sides (an instance with
// Options.DefaultWeight still runs TEST_T).
func TestWeight_InferentialRefusalsMatchPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	def := &types.WeightSpec{Field: "y"}
	plain := weightPulse(t, fs, nil)
	withDefault := weightPulse(t, fs, def)

	type source struct {
		p    *pulse.Pulse
		w    types.SlotWeight
		reqW *types.WeightSpec
	}
	sources := map[string]source{
		"default": {withDefault, types.SlotWeight{}, nil},
		"request": {plain, types.SlotWeight{}, def},
		"slot":    {plain, types.SlotWeightField("y"), nil},
	}
	for sname, src := range sources {
		reqs := inferentialRefusalRequests(t, cohort, src.w)
		keys := sortedKeys(reqs)
		for _, key := range keys {
			t.Run(sname+"/"+key, func(t *testing.T) {
				mk := func() *types.Request {
					r := inferentialRefusalRequests(t, cohort, src.w)[key]
					r.Weight = src.reqW
					return r
				}
				_, rerr := src.p.Process(ctx, mk())
				ce := requireCode(t, rerr, errors.PULSE_WEIGHT_UNSUPPORTED)
				if ce.Details["field"] != "y" || ce.Details["slot"] == nil || ce.Details["operator"] == nil {
					t.Fatalf("details = %v", ce.Details)
				}
				reason, _ := ce.Details["reason"].(string)
				switch {
				case permanentRefusalKeys[key] && (reason == "" || !strings.Contains(ce.Message, "cannot be weighted: "+reason)):
					t.Fatalf("permanent refusal without its reason: %s %v", ce.Message, ce.Details)
				case !permanentRefusalKeys[key] && (reason != "" || !strings.Contains(ce.Message, "has no weighted form yet")):
					t.Fatalf("pending refusal: %s %v", ce.Message, ce.Details)
				case strings.Contains(ce.Message, "U12") || strings.Contains(ce.Message, "not implemented"):
					t.Fatalf("refusal names the roadmap: %s", ce.Message)
				}
				sameEntry(t, predictEnvelope(t, src.p, fs, cohort, mk()), rerr)
			})
		}
	}

	for _, key := range sortedKeys(inferentialRefusalRequests(t, cohort, types.NullSlotWeight())) {
		t.Run("null/"+key, func(t *testing.T) {
			mk := func() *types.Request {
				r := inferentialRefusalRequests(t, cohort, types.NullSlotWeight())[key]
				r.Weight = def
				return r
			}
			_, rerr := withDefault.Process(ctx, mk())
			var ce *errors.CodedError
			if rerr != nil && !(stderrors.As(rerr, &ce) && acceptanceDataOutcomes[ce.Code]) {
				t.Fatalf("opted-out slot refused: %v", rerr)
			}
			if env := predictEnvelope(t, withDefault, fs, cohort, mk()); len(env.Errors) != 0 {
				t.Fatalf("predict refused the opted-out slot: %+v", env.Errors)
			}
		})
	}
}

// TestWeight_DefaultInstanceWeightsTTest: since E1-S2 an instance with
// Options.DefaultWeight WEIGHTS a bare TEST_T (predict says applied,
// details carry sum_weights and n_eff, raw n unchanged) and `weight:
// null` runs it unweighted, byte-identical to a plain instance.
func TestWeight_DefaultInstanceWeightsTTest(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	p := weightPulse(t, fs, &types.WeightSpec{Field: "y"})
	plain := weightPulse(t, fs, nil)
	ctx := context.Background()
	tt := func(w types.SlotWeight) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Tests:  []*types.Test{{Type: types.TEST_T, Field: "x", Params: json.RawMessage(`{"mu":10}`), Weight: w}},
		}
	}
	weighted, err := p.Process(ctx, tt(types.SlotWeight{}))
	if err != nil {
		t.Fatalf("weighted TEST_T: %v", err)
	}
	wd := weighted.Tests[0].Details
	if wd["sum_weights"] == nil || wd["n_eff"] == nil || wd["n"] != int64(48) {
		t.Fatalf("weighted details = %v", wd)
	}
	if env := predictEnvelope(t, p, fs, cohort, tt(types.SlotWeight{})); len(env.Errors) != 0 {
		t.Fatalf("predict refused the weighted TEST_T: %+v", env.Errors)
	}
	optedOut, err := p.Process(ctx, tt(types.NullSlotWeight()))
	if err != nil {
		t.Fatalf("opted-out TEST_T: %v", err)
	}
	unweighted, err := plain.Process(ctx, tt(types.SlotWeight{}))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(optedOut.Tests)
	b, _ := json.Marshal(unweighted.Tests)
	if string(a) != string(b) {
		t.Fatalf("opted-out TEST_T differs from unweighted:\n%s\n%s", a, b)
	}
	if weighted.Tests[0].Statistic == unweighted.Tests[0].Statistic {
		t.Fatal("the default weight did not change TEST_T")
	}
}

// TestWeight_WindowsAndUnaffectedMatchPredict: a WIN_* is
// PROCESSING_CONFIG under a request weight (identically in predict) and
// runs under the instance default; filterers, features, row-local
// attributes and the other groupers run under a request weight.
func TestWeight_WindowsAndUnaffectedMatchPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	def := &types.WeightSpec{Field: "y"}
	win := func(reqW *types.WeightSpec) *types.Request {
		var r types.Request
		if err := json.Unmarshal([]byte(strings.ReplaceAll(acceptanceTemplates["WIN_LAG"], "$F", "x")), &r); err != nil {
			t.Fatal(err)
		}
		r.Cohort, r.Weight = &types.Cohort{Filename: cohort}, reqW
		return &r
	}
	plain := weightPulse(t, fs, nil)
	_, rerr := plain.Process(ctx, win(def))
	ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
	if ce.Details["slot"] != "windows[0]" || ce.Details["operator"] != "WIN_LAG" {
		t.Fatalf("details = %v", ce.Details)
	}
	sameEntry(t, predictEnvelope(t, plain, fs, cohort, win(def)), rerr)
	withDefault := weightPulse(t, fs, def)
	if _, err := withDefault.Process(ctx, win(nil)); err != nil {
		t.Fatalf("window under the default: %v", err)
	}

	unaffected := []string{"FILTER_INCLUDE", "FILTER_RANGE", "FILTER_EXPRESSION", "FEAT_LOG", "FEAT_BUCKETIZE", "ATTR_FORMULA",
		"GROUP_CATEGORY", "GROUP_RANGE", "GROUP_ROUNDED"}
	field := map[string]string{"FILTER_INCLUDE": "g", "GROUP_CATEGORY": "g", "FILTER_EXPRESSION": "x"}
	for _, op := range unaffected {
		t.Run(op, func(t *testing.T) {
			f := field[op]
			if f == "" {
				f = "x"
			}
			v := map[string]string{"g": "a", "x": "1"}[f]
			body := strings.NewReplacer("$F", f, "$V", v, "$W", "20").Replace(acceptanceTemplates[op])
			var r types.Request
			if err := json.Unmarshal([]byte(body), &r); err != nil {
				t.Fatal(err)
			}
			r.Cohort, r.Weight = &types.Cohort{Filename: cohort}, def
			if _, err := plain.Process(ctx, &r); err != nil {
				t.Fatalf("%s under a request weight: %v", op, err)
			}
			if env := predictEnvelope(t, plain, fs, cohort, &r); len(env.Errors) != 0 {
				t.Fatalf("%s: predict errors %+v", op, env.Errors)
			}
		})
	}
}

func sortedKeys(m map[string]*types.Request) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestWeight_ComposeInferentialOverlayMatchesValidator: a Compose-host
// Inferential overlay lifted under both weight kinds (OVERLAY_Z_VS_REF,
// weighting-inferential E3-S1) over slots an instance default weight
// reaches runs at runtime and validates clean in
// ValidateComposeWithOptions — the two arms agree; `weight: null` on
// the host slots' aggregations runs it too. The refusal arm of the same
// shared rule (ComposeOverlayWeightRefusal) is pinned per still-refused
// kind by internal/descriptor TestComposeOverlayWeightRefusal.
func TestWeight_ComposeInferentialOverlayMatchesValidator(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	def := &types.WeightSpec{Field: "y"}
	p := weightPulse(t, fs, def)
	mk := func(w types.SlotWeight) *types.ComposedRequest {
		slot := func(label, v string) *types.Request {
			return &types.Request{
				Label:        label,
				Cohort:       &types.Cohort{Filename: cohort},
				Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "g", Values: []string{v}}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_WELFORD, Field: "x", Label: "t", Weight: w}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}},
			}
		}
		return &types.ComposedRequest{
			Requests: []*types.Request{slot("control", "a"), slot("variant", "b")},
			Overlays: []types.ComposeOverlaySpec{{Kind: types.OverlayKindZVsRef, Scope: types.OverlayScopeGroup, Reference: "control", Targets: []string{"variant"}}},
		}
	}
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs), DefaultWeight: def}
	for name, w := range map[string]types.SlotWeight{"weighted": {}, "opted out": types.NullSlotWeight()} {
		if _, err := p.Compose(ctx, mk(w)); err != nil {
			t.Fatalf("%s: runtime refused the lifted overlay: %v", name, err)
		}
		if env := descx.ValidateComposeWithOptions(mk(w), opts); len(env.Errors) != 0 {
			t.Fatalf("%s: validator refused the lifted overlay: %+v", name, env.Errors)
		}
	}
}

// TestWeight_FrequencyOnlyMatchesPredict: every frequency-only test
// (the rank tests) is refused PULSE_WEIGHT_UNSUPPORTED naming the kind
// under a probability weight — instance default, request and slot — at
// runtime and predict identically, and runs weighted under a frequency
// weight (predict reports it applied, the result carries sum_weights).
func TestWeight_FrequencyOnlyMatchesPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	prob := &types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	freq := &types.WeightSpec{Field: "y", Kind: types.WeightKindFrequency}
	// y is fractional (every row invalid as a frequency weight); the
	// weighted run reads the integer column t_u8.
	freqRun := &types.WeightSpec{Field: "t_u8", Kind: types.WeightKindFrequency}
	var ops []string
	for _, tt := range types.AllTestTypes() {
		if weighting.ClassOf(string(tt)) == weighting.ClassFrequencyOnly {
			ops = append(ops, string(tt))
		}
	}
	if len(ops) == 0 {
		t.Fatal("no frequency-only test: the gate is vacuous")
	}
	for _, op := range ops {
		mk := func(w types.SlotWeight, reqW *types.WeightSpec) *types.Request {
			var r types.Request
			field := "y"
			if op == "TEST_FISHER_EXACT" {
				field = "g" // rows g × cols g: a 2×2 table on the two-level split
			}
			if err := json.Unmarshal([]byte(strings.NewReplacer("$F", field, "$S", "a").Replace(acceptanceTemplates[op])), &r); err != nil {
				t.Fatalf("%s template: %v", op, err)
			}
			r.Cohort, r.Weight = &types.Cohort{Filename: cohort}, reqW
			r.Tests[0].Weight = w
			return &r
		}
		for name, tc := range map[string]struct {
			p    *pulse.Pulse
			w    types.SlotWeight
			reqW *types.WeightSpec
		}{
			"default": {weightPulse(t, fs, prob), types.SlotWeight{}, nil},
			"request": {weightPulse(t, fs, nil), types.SlotWeight{}, prob},
			"slot":    {weightPulse(t, fs, freq), types.SlotWeightOf(*prob), nil},
		} {
			t.Run(op+"/"+name, func(t *testing.T) {
				_, rerr := tc.p.Process(ctx, mk(tc.w, tc.reqW))
				ce := requireCode(t, rerr, errors.PULSE_WEIGHT_UNSUPPORTED)
				if ce.Details["kind"] != "probability" || !strings.Contains(ce.Message, `kind "probability"`) {
					t.Fatalf("refusal does not name the kind: %s %v", ce.Message, ce.Details)
				}
				sameEntry(t, predictEnvelope(t, tc.p, fs, cohort, mk(tc.w, tc.reqW)), rerr)
			})
		}
		t.Run(op+"/frequency", func(t *testing.T) {
			p := weightPulse(t, fs, nil)
			if env := predictEnvelope(t, p, fs, cohort, mk(types.SlotWeight{}, freq)); len(env.Errors) != 0 {
				t.Fatalf("predict refused a frequency weight: %+v", *env.Errors[0])
			}
			resp, err := p.Process(ctx, mk(types.SlotWeight{}, freqRun))
			if err != nil {
				t.Fatalf("runtime refused a frequency weight: %v", err)
			}
			if len(resp.Tests) == 0 || resp.Tests[0].Details["sum_weights"] == nil {
				t.Fatalf("frequency weight did not apply: %+v", resp.Tests)
			}
		})
	}
}

// TestWeight_RegressionsMatchPredict: the weight-aware regressions
// (U12 E4-S1) — REG_OLS under both kinds, REG_BAYES_LINEAR under
// frequency — are applied at predict and fitted weighted at runtime
// from the instance default, the request and the slot (the result
// carries sum_weights, plus n_eff under probability); REG_BAYES_LINEAR
// under a probability weight is refused PULSE_WEIGHT_UNSUPPORTED naming
// the kind, at runtime and predict identically; `weight: null` runs
// either unweighted.
func TestWeight_RegressionsMatchPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	prob := &types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	// y is fractional (every row invalid as a frequency weight); a
	// frequency run reads the integer column t_u8.
	freq := &types.WeightSpec{Field: "t_u8", Kind: types.WeightKindFrequency}
	mk := func(op string, w types.SlotWeight, reqW *types.WeightSpec) *types.Request {
		var r types.Request
		// REG_GLM's template targets y, so its predictor is x.
		pred := map[bool]string{false: "y", true: "x"}[op == "REG_GLM"]
		if err := json.Unmarshal([]byte(strings.ReplaceAll(acceptanceTemplates[op], "$F", pred)), &r); err != nil {
			t.Fatalf("%s template: %v", op, err)
		}
		r.Cohort, r.Weight = &types.Cohort{Filename: cohort}, reqW
		r.Regressions[0].Weight = w
		return &r
	}
	for _, op := range []string{"REG_OLS", "REG_BAYES_LINEAR", "REG_GLM"} {
		for _, kind := range weighting.KindsOf(op) {
			spec := freq
			if kind == types.WeightKindProbability {
				spec = prob
			}
			for name, tc := range map[string]struct {
				p    *pulse.Pulse
				w    types.SlotWeight
				reqW *types.WeightSpec
			}{
				"default": {weightPulse(t, fs, spec), types.SlotWeight{}, nil},
				"request": {weightPulse(t, fs, nil), types.SlotWeight{}, spec},
				"slot":    {weightPulse(t, fs, nil), types.SlotWeightOf(*spec), nil},
			} {
				t.Run(op+"/"+string(kind)+"/"+name, func(t *testing.T) {
					env := predictEnvelope(t, tc.p, fs, cohort, mk(op, tc.w, tc.reqW))
					if len(env.Errors) != 0 {
						t.Fatalf("predict refused: %+v", *env.Errors[0])
					}
					resp, err := tc.p.Process(ctx, mk(op, tc.w, tc.reqW))
					if err != nil {
						t.Fatalf("runtime refused: %v", err)
					}
					r := resp.Regressions[0]
					if r.SumWeights == 0 || (kind == types.WeightKindProbability) != (r.NEff != 0) {
						t.Fatalf("weight did not apply: sum_weights %v n_eff %v", r.SumWeights, r.NEff)
					}
					optedOut, err := tc.p.Process(ctx, mk(op, types.NullSlotWeight(), tc.reqW))
					if err != nil || optedOut.Regressions[0].SumWeights != 0 {
						t.Fatalf("weight: null did not opt out: %v %+v", err, optedOut)
					}
				})
			}
		}
	}
	for name, tc := range map[string]struct {
		p    *pulse.Pulse
		w    types.SlotWeight
		reqW *types.WeightSpec
	}{
		"default": {weightPulse(t, fs, prob), types.SlotWeight{}, nil},
		"request": {weightPulse(t, fs, nil), types.SlotWeight{}, prob},
		"slot":    {weightPulse(t, fs, freq), types.SlotWeightOf(*prob), nil},
	} {
		t.Run("REG_BAYES_LINEAR/probability/"+name, func(t *testing.T) {
			_, rerr := tc.p.Process(ctx, mk("REG_BAYES_LINEAR", tc.w, tc.reqW))
			ce := requireCode(t, rerr, errors.PULSE_WEIGHT_UNSUPPORTED)
			if ce.Details["kind"] != "probability" || !strings.Contains(ce.Message, `kind "probability"`) {
				t.Fatalf("refusal does not name the kind: %s %v", ce.Message, ce.Details)
			}
			sameEntry(t, predictEnvelope(t, tc.p, fs, cohort, mk("REG_BAYES_LINEAR", tc.w, tc.reqW)), rerr)
		})
	}
}

// TestWeight_RegressionAttributesMatchPredict: the regression
// attributes (U12 E4-S2) refit weighted under each kind REG_OLS
// advertises, from every source: predict reports the slot applied and
// the runtime's Σ fitted moves off the unweighted Σ fitted (= Σ target,
// an OLS identity) while `weight: null` restores it.
func TestWeight_RegressionAttributesMatchPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	prob := &types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	freq := &types.WeightSpec{Field: "t_u8", Kind: types.WeightKindFrequency}
	mk := func(w types.SlotWeight, reqW *types.WeightSpec) *types.Request {
		var r types.Request
		if err := json.Unmarshal([]byte(strings.ReplaceAll(acceptanceTemplates["ATTR_REG_FITTED"], "$F", "y")), &r); err != nil {
			t.Fatal(err)
		}
		r.Cohort, r.Weight = &types.Cohort{Filename: cohort}, reqW
		r.Attributes[0].Weight = w
		r.Aggregations[0].Weight, r.Aggregations[0].Label = types.NullSlotWeight(), "s"
		return &r
	}
	sumOf := func(resp *types.Response) float64 {
		v, ok := resp.Data[0]["s"].(float64)
		if !ok {
			t.Fatalf("no Σ fitted in %v", resp.Data[0])
		}
		return v
	}
	if kinds := weighting.KindsOf("ATTR_REG_FITTED"); len(kinds) != 2 {
		t.Fatalf("ATTR_REG_FITTED kinds %v, want REG_OLS's two", kinds)
	}
	for _, kind := range weighting.KindsOf("ATTR_REG_FITTED") {
		spec := freq
		if kind == types.WeightKindProbability {
			spec = prob
		}
		for name, tc := range map[string]struct {
			p    *pulse.Pulse
			w    types.SlotWeight
			reqW *types.WeightSpec
		}{
			"default": {weightPulse(t, fs, spec), types.SlotWeight{}, nil},
			"request": {weightPulse(t, fs, nil), types.SlotWeight{}, spec},
			"slot":    {weightPulse(t, fs, nil), types.SlotWeightOf(*spec), nil},
		} {
			t.Run(string(kind)+"/"+name, func(t *testing.T) {
				pr, err := tc.p.Predict(ctx, mk(tc.w, tc.reqW))
				if err != nil {
					t.Fatal(err)
				}
				status := map[string]string{}
				for _, w := range pr.Weights {
					status[w.Slot] = w.Status
				}
				if status["attributes[0]"] != descriptor.WeightStatusApplied {
					t.Fatalf("predict statuses %v, want attributes[0] applied", status)
				}
				weighted, err := tc.p.Process(ctx, mk(tc.w, tc.reqW))
				if err != nil {
					t.Fatalf("runtime refused: %v", err)
				}
				optedOut, err := tc.p.Process(ctx, mk(types.NullSlotWeight(), tc.reqW))
				if err != nil {
					t.Fatal(err)
				}
				if sumOf(weighted) == sumOf(optedOut) {
					t.Fatalf("weight did not reach the refit: Σ fitted %v both ways", sumOf(weighted))
				}
			})
		}
	}
}

// TestWeight_ScoresAndQuantileMatchPredict: ATTR_ZSCORE / ATTR_TSCORE
// and GROUP_QUANTILE (groups and a crosstab axis) compute weighted
// under both kinds since U12 E5-S2, from every source: predict reports
// the slot applied, the runtime answer moves off the opted-out one, and
// the grouper floor total_n stays the raw row count.
func TestWeight_ScoresAndQuantileMatchPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	prob := &types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	freq := &types.WeightSpec{Field: "t_u8", Kind: types.WeightKindFrequency}
	type slotCase struct {
		slot string
		mk   func(w types.SlotWeight) *types.Request
	}
	decode := func(body string) *types.Request {
		var r types.Request
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		r.Cohort = &types.Cohort{Filename: cohort}
		return &r
	}
	cases := map[string]slotCase{}
	for _, op := range []string{"ATTR_ZSCORE", "ATTR_TSCORE"} {
		cases[op] = slotCase{"attributes[0]", func(w types.SlotWeight) *types.Request {
			r := decode(strings.ReplaceAll(acceptanceTemplates[op], "$F", "x"))
			r.Attributes[0].Weight = w
			r.Aggregations[0].Weight, r.Aggregations[0].Label = types.NullSlotWeight(), "s"
			return r
		}}
	}
	cases["GROUP_QUANTILE"] = slotCase{"groups[0]", func(w types.SlotWeight) *types.Request {
		r := decode(strings.ReplaceAll(acceptanceTemplates["GROUP_QUANTILE"], "$F", "x"))
		r.Groups[0].Weight = w
		r.Aggregations[0].Weight = types.NullSlotWeight()
		return r
	}}
	cases["crosstab.rows/GROUP_QUANTILE"] = slotCase{"crosstab.rows[0]", func(w types.SlotWeight) *types.Request {
		r := decode(`{"crosstab":{"rows":[{"type":"GROUP_QUANTILE","field":"x","interval":4}],"columns":[{"type":"GROUP_CATEGORY","field":"g"}],` +
			`"cell":{"type":"AGG_COUNT","field":"x","weight":null}}}`)
		r.Crosstab.Rows[0].Weight = w
		return r
	}}
	for _, op := range []string{"ATTR_ZSCORE", "ATTR_TSCORE", "GROUP_QUANTILE"} {
		if kinds := weighting.KindsOf(op); len(kinds) != 2 {
			t.Fatalf("%s kinds %v, want both", op, kinds)
		}
	}
	answer := func(resp *types.Response) string {
		b, err := json.Marshal(map[string]any{"data": resp.Data, "crosstab": resp.Crosstab})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for key, c := range cases {
		for _, spec := range []*types.WeightSpec{freq, prob} {
			for name, tc := range map[string]struct {
				p    *pulse.Pulse
				w    types.SlotWeight
				reqW *types.WeightSpec
			}{
				"default": {weightPulse(t, fs, spec), types.SlotWeight{}, nil},
				"request": {weightPulse(t, fs, nil), types.SlotWeight{}, spec},
				"slot":    {weightPulse(t, fs, nil), types.SlotWeightOf(*spec), nil},
			} {
				t.Run(key+"/"+string(spec.Kind)+"/"+name, func(t *testing.T) {
					mk := func(w types.SlotWeight) *types.Request {
						r := c.mk(w)
						r.Weight = tc.reqW
						return r
					}
					pr, err := tc.p.Predict(ctx, mk(tc.w))
					if err != nil {
						t.Fatal(err)
					}
					status := map[string]string{}
					for _, w := range pr.Weights {
						status[w.Slot] = w.Status + "/" + w.Kind
					}
					if status[c.slot] != descriptor.WeightStatusApplied+"/"+string(spec.Kind) {
						t.Fatalf("predict statuses %v, want %s applied", status, c.slot)
					}
					weighted, err := tc.p.Process(ctx, mk(tc.w))
					if err != nil {
						t.Fatalf("runtime refused: %v", err)
					}
					optedOut, err := tc.p.Process(ctx, mk(types.NullSlotWeight()))
					if err != nil {
						t.Fatal(err)
					}
					if answer(weighted) == answer(optedOut) {
						t.Fatalf("weight did not reach the slot: %s both ways", answer(weighted))
					}
					if c.slot == "groups[0]" {
						gw, go_ := weighted.Components.Groupers[0], optedOut.Components.Groupers[0]
						if gw.TotalN != go_.TotalN || gw.NNull != go_.NNull {
							t.Fatalf("grouper floor {total_n %d, n_null %d}, unweighted {%d, %d}: must stay raw", gw.TotalN, gw.NNull, go_.TotalN, go_.NNull)
						}
					}
				})
			}
		}
	}
}

// TestWeight_NormalizedNotWeightableMatchesPredict: ATTR_NORMALIZED (a
// min-max rescale, no weighted meaning) runs under the instance default
// and is PROCESSING_CONFIG under an explicit request or slot weight, at
// runtime and predict identically.
func TestWeight_NormalizedNotWeightableMatchesPredict(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	def := &types.WeightSpec{Field: "y"}
	mk := func(w types.SlotWeight, reqW *types.WeightSpec) *types.Request {
		var r types.Request
		if err := json.Unmarshal([]byte(strings.ReplaceAll(acceptanceTemplates["ATTR_NORMALIZED"], "$F", "x")), &r); err != nil {
			t.Fatal(err)
		}
		r.Cohort, r.Weight = &types.Cohort{Filename: cohort}, reqW
		r.Attributes[0].Weight = w
		r.Aggregations[0].Weight, r.Aggregations[0].Label = types.NullSlotWeight(), "s"
		return &r
	}
	withDefault := weightPulse(t, fs, def)
	if _, err := withDefault.Process(ctx, mk(types.SlotWeight{}, nil)); err != nil {
		t.Fatalf("default weight not skipped: %v", err)
	}
	plain := weightPulse(t, fs, nil)
	for name, req := range map[string]func() *types.Request{
		"request": func() *types.Request { return mk(types.SlotWeight{}, def) },
		"slot":    func() *types.Request { return mk(types.SlotWeightField("y"), nil) },
	} {
		_, rerr := plain.Process(ctx, req())
		ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
		if ce.Details["operator"] != "ATTR_NORMALIZED" || ce.Details["slot"] != "attributes[0]" {
			t.Fatalf("%s: details = %v", name, ce.Details)
		}
		sameEntry(t, predictEnvelope(t, plain, fs, cohort, req()), rerr)
	}
}

// TestWeight_PairwiseNSourceMatchesPredict: the weighted-host n_source
// rule (weighting-inferential E3-S1) refuses at runtime and in predict
// with the same code and message. The host is weighted by its cell's
// OWN slot weight — the slot-only host that, before E3-S1, ran the
// overlay on raw row counts — under each kind; the raw-row-count
// sources are PROCESSING_CONFIG under both, the weight sum under
// probability only, and an omitted source runs and predicts clean.
func TestWeight_PairwiseNSourceMatchesPredict(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	mk := func(w types.WeightSpec, nSource string) *types.Request {
		var params json.RawMessage
		if nSource != "" {
			params = json.RawMessage(`{"n_source":"` + nSource + `"}`)
		}
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 20}},
				Cell:    &types.Aggregation{Type: types.AGG_WELFORD, Field: "x", Label: "m", Weight: types.SlotWeightOf(w)},
			},
			Overlays: []types.OverlaySpec{{Name: "pw", Kind: types.OverlayKindPairwiseWelchT, Scope: types.OverlayScopeRow, Params: params}},
		}
	}
	for name, w := range map[string]types.WeightSpec{
		"frequency":   {Field: "t_u8", Kind: types.WeightKindFrequency},
		"probability": {Field: "y", Kind: types.WeightKindProbability},
	} {
		refused := []string{types.PairwiseNSourceCellNUnweighted, types.PairwiseNSourceRowMarginN, types.PairwiseNSourceColumnMarginN}
		if w.Kind == types.WeightKindProbability {
			refused = append(refused, types.PairwiseNSourceCellWeightSum)
		}
		for _, s := range refused {
			_, rerr := p.Process(ctx, mk(w, s))
			ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
			env := predictEnvelope(t, p, fs, cohort, mk(w, s))
			if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
				t.Fatalf("%s n_source %s: predict %+v, runtime %s %q", name, s, env.Errors, ce.Code, ce.Message)
			}
		}
		resp, err := p.Process(ctx, mk(w, ""))
		if err != nil {
			t.Fatalf("%s omitted n_source: %v", name, err)
		}
		if len(resp.Overlays) != 1 || resp.Overlays[0].Summary.Parameters["sum_weights"] <= 0 {
			t.Fatalf("%s: layer summary %+v, want the host's sum_weights", name, resp.Overlays[0].Summary)
		}
		if env := predictEnvelope(t, p, fs, cohort, mk(w, "")); len(env.Errors) != 0 {
			t.Fatalf("%s omitted n_source: predict errors %+v", name, env.Errors)
		}
	}
}

// TestWeight_ContingencyOverlaysMatchPredict: the contingency and
// proportion overlays (weighting-inferential E3-S2) run on a weighted
// crosstab under both kinds — request or slot-only cell weight — and
// predict clean, each layer reporting the host's sum_weights (and n_eff
// under probability); OVERLAY_FISHER_EXACT_CELL is frequency-only: a
// probability cell weight it cannot see on its own slot is refused at
// runtime and in predict with one code and message; an unweighted-count
// n_source on the weighted host is PROCESSING_CONFIG on both arms.
func TestWeight_ContingencyOverlaysMatchPredict(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	freq := types.WeightSpec{Field: "t_u8", Kind: types.WeightKindFrequency}
	prob := types.WeightSpec{Field: "y", Kind: types.WeightKindProbability}
	mk := func(reqW *types.WeightSpec, cellW types.SlotWeight, overlays ...types.OverlaySpec) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Weight: reqW,
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 20}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n", Weight: cellW},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			Overlays: overlays,
		}
	}
	lifted := []types.OverlaySpec{
		{Name: "m", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
		{Name: "pz", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn},
	}
	for name, w := range map[string]types.WeightSpec{"frequency": freq, "probability": prob} {
		for src, req := range map[string]*types.Request{
			"request":   mk(&w, types.SlotWeight{}, lifted...),
			"slot only": mk(nil, types.SlotWeightOf(w), lifted...),
		} {
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("%s %s: %v", name, src, err)
			}
			if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
				t.Fatalf("%s %s: predict errors %+v", name, src, env.Errors)
			}
			for _, l := range resp.Overlays {
				_, hasNEff := l.Summary.Parameters["n_eff"]
				if l.Summary.Parameters["sum_weights"] <= 0 || hasNEff != (w.Kind == types.WeightKindProbability) {
					t.Fatalf("%s %s %s: Parameters %v", name, src, l.Kind, l.Summary.Parameters)
				}
			}
		}
	}

	fisher := types.OverlaySpec{Name: "f", Kind: types.OverlayKindFisherExactCell, Scope: types.OverlayScopeCell}
	if _, err := p.Process(ctx, mk(&freq, types.SlotWeight{}, fisher)); err != nil {
		t.Fatalf("Fisher under frequency: %v", err)
	}
	for name, req := range map[string]*types.Request{
		"request probability":   mk(&prob, types.SlotWeight{}, fisher),
		"slot-only probability": mk(nil, types.SlotWeightOf(prob), fisher),
	} {
		_, rerr := p.Process(ctx, req)
		ce := requireCode(t, rerr, errors.PULSE_WEIGHT_UNSUPPORTED)
		if ce.Details["kind"] != "probability" {
			t.Fatalf("%s: details %v", name, ce.Details)
		}
		sameEntry(t, predictEnvelope(t, p, fs, cohort, req), rerr)
	}

	nWithin := types.OverlaySpec{Name: "pz", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn,
		Params: json.RawMessage(`{"n_source":"n_within"}`)}
	req := mk(nil, types.SlotWeightOf(freq), nWithin)
	_, rerr := p.Process(ctx, req)
	ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
	// Code and message (predict's details add the spec index, as for
	// every per-Request overlay refusal the runtime raises).
	if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 1 ||
		env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
		t.Fatalf("n_within: predict %+v, runtime %s %q", env.Errors, ce.Code, ce.Message)
	}
}
