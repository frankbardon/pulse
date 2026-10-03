package pulse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// runtimeSweepCase is one request built to reach a runtime refusal
// whose prose names built-ins OTHER than the ones the request wrote
// (a remedy clause: "use TEST_ANOVA_F", "admitted: AGG_DISTINCT_SUM,
// …"). features is a profile that offers what the request needs and
// hides what the clause recommends; reach are names the unprofiled
// refusal carries, proving the clause is reached.
type runtimeSweepCase struct {
	name     string
	features []string
	reach    []string
	run      func(p *Pulse) ([]byte, any)
}

func sweepProcess(req *types.Request) func(p *Pulse) ([]byte, any) {
	return func(p *Pulse) ([]byte, any) {
		resp, err := p.Process(context.Background(), req)
		return parityOutcome(resp, err), req
	}
}

func sweepCohortRequest(cohort string) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: cohort}}
}

// runtimeSweepCases covers every runtime remedy clause
// (internal/processing/refusal_prose.go and the regression remedies).
// The chain gate's refusals name only the operator the refused request
// itself wrote, so they recommend nothing else and have no case here.
func runtimeSweepCases() []runtimeSweepCase {
	count := &types.Aggregation{Type: types.AGG_COUNT, Field: "age", Label: "n"}
	set := func(mut func(r *types.Request)) *types.Request {
		r := sweepCohortRequest(pwDistinctCohort)
		mut(r)
		return r
	}
	par := func(mut func(r *types.Request)) *types.Request {
		r := sweepCohortRequest(parityCohort)
		mut(r)
		return r
	}
	twoSample := func(tt types.TestType, params string) *types.Request {
		return par(func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{count}
			t := &types.Test{Type: tt, Field: "age", SplitBy: "region"}
			if tt == types.TEST_PROP_Z {
				t.Field = "region"
			}
			if params != "" {
				t.Params = json.RawMessage(params)
			}
			r.Tests = []*types.Test{t}
		})
	}
	reg := func(spec types.RegressionSpec) *types.Request {
		return par(func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{count}
			s := spec
			s.Target, s.Predictors = "age", []string{"age"}
			r.Regressions = []*types.RegressionSpec{&s}
		})
	}
	base := []string{"capability:process", "AGG_COUNT"}
	with := func(names ...string) []string { return append(append([]string(nil), base...), names...) }
	return []runtimeSweepCase{
		{"aggregator on a set field", []string{"capability:process", "AGG_MODE_COUNT"},
			[]string{"AGG_SET_FREQUENCY", "AGG_COUNT"},
			sweepProcess(set(func(r *types.Request) {
				r.Aggregations = []*types.Aggregation{{Type: types.AGG_MODE_COUNT, Field: "brand"}}
			}))},
		{"attribute on a set field", []string{"capability:process", "AGG_SUM", "ATTR_ZSCORE"},
			[]string{"ATTR_SET_POPCOUNT"},
			sweepProcess(set(func(r *types.Request) {
				r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}}
				r.Attributes = []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "brand", Label: "z"}}
			}))},
		{"filterer on a set field", []string{"capability:process", "AGG_SUM", "FILTER_RANGE"},
			[]string{"FILTER_SET_CONTAINS_ANY", "ATTR_SET_POPCOUNT"},
			sweepProcess(set(func(r *types.Request) {
				r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}}
				r.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "brand", Values: []string{"0", "3"}}}
			}))},
		{"grouper on a set field", []string{"capability:process", "AGG_SUM", "GROUP_RANGE"},
			[]string{"GROUP_SET_VALUE"},
			sweepProcess(set(func(r *types.Request) {
				r.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "value"}}
				r.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "brand", Interval: 1}}
			}))},
		{"TEST_T on four groups", with("TEST_T"), []string{"TEST_ANOVA_F"}, sweepProcess(twoSample(types.TEST_T, ""))},
		{"TEST_Z_TWO_SAMPLE on four groups", with("TEST_Z_TWO_SAMPLE"), []string{"TEST_ANOVA_F"}, sweepProcess(twoSample(types.TEST_Z_TWO_SAMPLE, ""))},
		{"TEST_PROP_Z on four groups", with("TEST_PROP_Z"), []string{"TEST_CHISQ"}, sweepProcess(twoSample(types.TEST_PROP_Z, `{"success":"north"}`))},
		{"TEST_KS on four groups", with("TEST_KS"), []string{"FILTER_INCLUDE", "FILTER_EXCLUDE"}, sweepProcess(twoSample(types.TEST_KS, ""))},
		{"TEST_MANN_WHITNEY_U on four groups", with("TEST_MANN_WHITNEY_U"), []string{"FILTER_INCLUDE", "FILTER_EXCLUDE"}, sweepProcess(twoSample(types.TEST_MANN_WHITNEY_U, ""))},
		{"TEST_TUKEY_HSD without params", with("GROUP_CATEGORY", "TEST_TUKEY_HSD"), []string{"TEST_ANOVA_F"},
			sweepProcess(par(func(r *types.Request) {
				r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
				r.Aggregations = []*types.Aggregation{count}
				r.PostTests = []*types.Test{{Type: types.TEST_TUKEY_HSD, Field: "n", SplitBy: "region"}}
			}))},
		{"REG_GLM alpha", with("REG_GLM"), []string{"REG_OLS"}, sweepProcess(reg(types.RegressionSpec{Type: types.REG_GLM, Family: "poisson", Alpha: 1}))},
		{"REG_BAYES_LINEAR penalty", with("REG_BAYES_LINEAR"), []string{"REG_OLS"}, sweepProcess(reg(types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Penalty: "l2"}))},
		{"REG_BAYES_LINEAR family", with("REG_BAYES_LINEAR"), []string{"REG_GLM"}, sweepProcess(reg(types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Family: "poisson"}))},
		{"REG_BAYES_LINEAR link", with("REG_BAYES_LINEAR"), []string{"REG_GLM"}, sweepProcess(reg(types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Link: "log"}))},
		{"REG_BAYES_LINEAR resample", with("REG_BAYES_LINEAR"), []string{"REG_OLS"}, sweepProcess(reg(types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Resample: "bootstrap"}))},
		{"REG_BAYES_LINEAR selection", with("REG_BAYES_LINEAR"), []string{"REG_OLS"}, sweepProcess(reg(types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Selection: "forward"}))},
		{"Welford pairwise distinct n_source",
			[]string{"capability:process", "capability:crosstab", "GROUP_CATEGORY", "AGG_WELFORD", "OVERLAY_PAIRWISE_WELCH_T"},
			[]string{"OVERLAY_PAIRWISE_PROP_Z"},
			sweepProcess(par(func(r *types.Request) {
				region := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
				r.Crosstab = &types.CrosstabSpec{Rows: region, Columns: region,
					Cell: &types.Aggregation{Type: types.AGG_WELFORD, Field: "age"}}
				r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindPairwiseWelchT, Scope: types.OverlayScopeRow,
					Params: json.RawMessage(`{"n_source":"` + types.PairwiseNSourceNWithinDistinct + `","n_within_depth":0}`)}}
			}))},
		{"pairwise distinct-key admission",
			[]string{"capability:process", "capability:crosstab", "GROUP_CATEGORY", "GROUP_SET_PER_ELEMENT", "AGG_MODE_COUNT", "OVERLAY_PAIRWISE_PROP_Z"},
			[]string{"AGG_DISTINCT_SUM", "AGG_DISTINCT_COUNT"},
			sweepProcess(func() *types.Request {
				r := pwDistinctRequest(types.PairwiseNSourceNWithinDistinct)
				r.Crosstab.Cell = &types.Aggregation{Type: types.AGG_MODE_COUNT, Field: "segment", Label: "freq_segment"}
				return r
			}())},
	}
}

func runtimeSweepFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := parityFS(t)
	writePairwiseDistinctCohort(t, fsys)
	return fsys
}

// hiddenTokensOutside is the instance's hidden-token set minus every
// token the request authored.
func hiddenTokensOutside(t *testing.T, p *Pulse, authored any) map[string]bool {
	t.Helper()
	b, err := json.Marshal(authored)
	if err != nil {
		t.Fatal(err)
	}
	wrote := map[string]bool{}
	for _, tok := range strings.FieldsFunc(string(b), func(c rune) bool { return !isWordByte(byte(c)) || c > 127 }) {
		wrote[tok] = true
	}
	out := map[string]bool{}
	for tok := range errorsHiddenTokens(p) {
		if !wrote[tok] {
			out[tok] = true
		}
	}
	return out
}

func outcomeCode(outcome []byte) string {
	var v struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(outcome, &v); err != nil {
		return ""
	}
	return v.Code
}

// TestProfileRuntimeRefusalSweep extends the hidden-name sweep to the
// runtime: every refusal built in internal/processing / internal/service
// whose prose recommends OTHER built-ins is driven (1) on a profile
// that offers what the request needs and hides what the clause
// recommends — the refusal is still raised, with the same code, and
// names no hidden feature beyond what the request wrote — and (2) on
// every fixture profile, where nothing the instance emits names a
// hidden feature either. An unprofiled control proves each clause is
// reached and still names the recommended operators by default.
func TestProfileRuntimeRefusalSweep(t *testing.T) {
	fsys := runtimeSweepFS(t)
	open, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range runtimeSweepCases() {
		t.Run(tc.name, func(t *testing.T) {
			control, _ := tc.run(open)
			if !parityIsError(control) {
				t.Fatalf("control succeeded: %s", control)
			}
			for _, tok := range tc.reach {
				if !strings.Contains(string(control), tok) {
					t.Fatalf("vacuous: the unprofiled refusal does not name %s\n%s", tok, control)
				}
			}
			p, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Features: tc.features}})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, authored := tc.run(p)
			if outcomeCode(got) != outcomeCode(control) {
				t.Errorf("profiled refusal changed code\nprofiled: %s\ncontrol:  %s", got, control)
			}
			hidden := hiddenTokensOutside(t, p, authored)
			for _, tok := range tc.reach {
				if !hidden[tok] {
					t.Fatalf("profile does not hide %s: the case proves nothing", tok)
				}
			}
			if tok := namesHiddenToken(string(got), hidden); tok != "" {
				t.Errorf("refusal names hidden %s\n%s", tok, got)
			}
		})
	}
	for _, fixture := range featureSetFixtures {
		raw, err := afero.ReadFile(afero.NewOsFs(), featureSetFixtureDir+fixture+".json")
		if err != nil {
			t.Fatal(err)
		}
		fp, err := ParseFeatureProfile(raw)
		if err != nil {
			t.Fatal(err)
		}
		p, err := New(Options{FS: fsys, FeatureProfile: fp})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range runtimeSweepCases() {
			got, authored := tc.run(p)
			if tok := namesHiddenToken(string(got), hiddenTokensOutside(t, p, authored)); tok != "" {
				t.Errorf("%s/%s: names hidden %s\n%s", fixture, tc.name, tok, got)
			}
		}
	}
}
