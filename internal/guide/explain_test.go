package guide

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// explainChecks wires Checks over the bound fixture for inst, the way
// the facade does: predict for a Request, the root validators for the
// others.
func explainChecks(t testing.TB, inst *descx.InstanceSnapshot) Checks {
	data := cohortBytes(t, boundFixture(t))
	opts := func() *descx.PredictOptions { return &descx.PredictOptions{Instance: inst} }
	return Checks{
		Request: func(r *types.Request) (*descriptor.Envelope, error) {
			return descx.Predict(bytes.NewReader(data), r, opts()), nil
		},
		Compose: func(c *types.ComposedRequest) (*descriptor.Envelope, error) {
			return descx.ValidateComposeWithOptions(c, opts()), nil
		},
		Chain: func(c *types.ChainRequest) (*descriptor.Envelope, error) {
			return descx.ValidateChainWithOptions(bytes.NewReader(data), c, opts()), nil
		},
		Facet: func(f *types.FacetRequest) (*descriptor.Envelope, error) {
			return descx.ValidateFacetWithOptions(bytes.NewReader(data), f, opts()), nil
		},
	}
}

var fixtureCohort = &types.Cohort{Filename: "fixture.pulse"}

// everySlotRequest names one operator in every operator slot Explain
// describes, plus a join, a weight and a correction block.
func everySlotRequest() *types.Request {
	return &types.Request{
		Joins:        []*types.JoinSpec{{Right: "other.pulse", On: []types.OnPair{{LeftField: "arm", RightField: "arm"}}}},
		Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"n", "s"}}},
		Features:     []*types.Feature{{Type: types.FEAT_LOG, Field: "hours"}},
		Attributes:   []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "score"}},
		Windows:      []*types.Window{{Type: types.WIN_LAG, Field: "score", OrderBy: []types.OrderKey{{Field: "taken_on"}}}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score"}},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "arm"}},
			Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "score"},
		},
		Tests:        []*types.Test{{Type: types.TEST_ANOVA_F, Field: "score", SplitBy: "region", Alpha: 0.01}},
		PostTests:    []*types.Test{{Type: types.TEST_TUKEY_HSD, Field: "score", SplitBy: "region"}},
		Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"hours"}}},
		Matrices:     []types.MatrixSpec{{Type: types.MAT_CORRELATION, Fields: []string{"score", "hours"}}},
		Overlays:     []types.OverlaySpec{{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix}},
		Weight:       &types.WeightSpec{Field: "hours", Kind: types.WeightKindFrequency},
		Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodHolm},
	}
}

func explain(t *testing.T, inst *descx.InstanceSnapshot, req descriptor.ExplainRequest, c Checks) *descriptor.ExplainResult {
	t.Helper()
	res, err := Explain(inst, req, c)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	return res
}

func TestExplain_ValidatesRootAndDetail(t *testing.T) {
	r := &types.Request{}
	for name, req := range map[string]descriptor.ExplainRequest{
		"no root":    {},
		"two roots":  {Request: r, Sample: &types.SampleRequest{}},
		"bad detail": {Request: r, Detail: "chatty"},
	} {
		_, err := Explain(nil, req, Checks{})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
			t.Errorf("%s: err = %v, want SERVICE_VALIDATION", name, err)
		}
	}
	res := explain(t, nil, descriptor.ExplainRequest{Request: r}, Checks{})
	if res.Detail != descriptor.ExplainTerse || res.Mode != descriptor.ExplainModeRequest || res.Root != descriptor.ExplainRootRequest {
		t.Errorf("defaults: detail=%q mode=%q root=%q", res.Detail, res.Mode, res.Root)
	}
	if res.Findings == nil {
		t.Error("findings is nil; want an empty array")
	}
	for _, d := range []descriptor.ExplainDetail{descriptor.ExplainTerse, descriptor.ExplainFull} {
		if res := explain(t, nil, descriptor.ExplainRequest{Request: r, Detail: d}, Checks{}); res.Detail != d {
			t.Errorf("detail %q echoed as %q", d, res.Detail)
		}
	}
}

// TestExplain_EverySlotDescribed: each operator slot is one step whose
// text carries the operator's plain purpose; an unchecked request says
// it was not checked and has no verdict.
func TestExplain_EverySlotDescribed(t *testing.T) {
	r := everySlotRequest()
	res := explain(t, nil, descriptor.ExplainRequest{Request: r}, Checks{})
	wantSlots := []string{"joins[0]", "filterers[0]", "features[0]", "attributes[0]", "windows[0]", "groups[0]",
		"aggregations[0]", "crosstab", "tests[0]", "post_tests[0]", "regressions[0]", "matrices[0]", "overlays[0]",
		"weight", "multiplicity"}
	var got []string
	for _, st := range res.Steps {
		got = append(got, st.Slot)
		if st.Operator == "" {
			continue
		}
		p, ok := descx.PurposeOf(st.Operator)
		if !ok {
			t.Fatalf("%s: no purpose", st.Operator)
		}
		if !strings.Contains(st.Text, p.Plain) || !strings.Contains(st.Text, st.Operator) {
			t.Errorf("%s: text %q lacks the operator or its plain purpose", st.Slot, st.Text)
		}
	}
	if !slices.Equal(got, wantSlots) {
		t.Errorf("slots = %v, want %v", got, wantSlots)
	}
	if res.Valid != nil || len(res.Caveats) == 0 || !strings.Contains(res.Caveats[0], "No cohort") {
		t.Errorf("unchecked request: valid=%v caveats=%v", res.Valid, res.Caveats)
	}
	for _, want := range []string{"1 filter", "1 test", "1 post-test", "1 regression", "1 matrix result", "1 overlay", "1 crosstab"} {
		if !strings.Contains(res.Summary, want) {
			t.Errorf("summary %q lacks %q", res.Summary, want)
		}
	}
	if len(res.Sentences)+len(res.GlossaryRefs)+len(res.FollowUps) != 0 {
		t.Error("terse result carries full-detail parts")
	}
}

// TestExplain_DefaultsAndAdvisories: predict's inferred operators fill
// untyped slots (marked Defaulted, the inference sentence in the
// text) and its advisories and verdict ride the result.
func TestExplain_DefaultsAndAdvisories(t *testing.T) {
	r := &types.Request{
		Cohort:       fixtureCohort,
		Groups:       []*types.Group{{Field: "arm"}},
		Aggregations: []*types.Aggregation{{Field: "score"}},
		Tests:        []*types.Test{{Type: types.TEST_T, Field: "score", SplitBy: "region"}},
	}
	res := explain(t, nil, descriptor.ExplainRequest{Request: r}, explainChecks(t, nil))
	if res.Valid == nil || !*res.Valid {
		t.Fatalf("valid = %v, caveats %v", res.Valid, res.Caveats)
	}
	byOp := map[string]descriptor.ExplainStep{}
	for _, st := range res.Steps {
		byOp[st.Slot] = st
	}
	agg := byOp["aggregations[0]"]
	if agg.Operator != string(types.AGG_SUM) || !agg.Defaulted || !strings.Contains(agg.Text, "AGG_SUM is inferred for `score`") {
		t.Errorf("aggregation step = %+v", agg)
	}
	if g := byOp["groups[0]"]; g.Operator != string(types.GROUP_CATEGORY) || !g.Defaulted {
		t.Errorf("group step = %+v", g)
	}
	if byOp["tests[0]"].Defaulted {
		t.Error("an explicit operator is marked defaulted")
	}
	if len(res.DefaultsApplied) != 2 {
		t.Errorf("defaults_applied = %+v", res.DefaultsApplied)
	}
	if !slices.ContainsFunc(res.Advisories, func(a descriptor.Advisory) bool {
		return a.Code == string(errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS)
	}) {
		t.Errorf("advisories = %+v", res.Advisories)
	}
	if !strings.HasPrefix(res.Summary, "This request reads `fixture.pulse` and runs 1 grouping, 1 aggregation and 1 test") {
		t.Errorf("summary = %q", res.Summary)
	}
}

// TestExplain_RefusedRequest: a request predict refuses is still
// explained, Valid false and the refusal a caveat naming its code.
func TestExplain_RefusedRequest(t *testing.T) {
	r := &types.Request{Cohort: fixtureCohort, Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "nope"}}}
	res := explain(t, nil, descriptor.ExplainRequest{Request: r}, explainChecks(t, nil))
	if res.Valid == nil || *res.Valid {
		t.Fatalf("valid = %v", res.Valid)
	}
	if !slices.ContainsFunc(res.Caveats, func(c string) bool { return strings.HasPrefix(c, "Predict refuses this request with ") }) {
		t.Errorf("caveats = %v", res.Caveats)
	}
	if len(res.Steps) != 1 {
		t.Errorf("steps = %+v", res.Steps)
	}
	if len(res.Refusals) == 0 || res.Refusals[0].Code == "" {
		t.Errorf("refusals = %+v", res.Refusals)
	}
}

// TestExplain_EveryRoot: each of the five roots is explained, its
// slot paths prefixed by the slot's place in the root.
func TestExplain_EveryRoot(t *testing.T) {
	c := explainChecks(t, nil)
	slotReq := func() *types.Request {
		return &types.Request{Cohort: fixtureCohort, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Field: "score"}}}
	}
	comp := explain(t, nil, descriptor.ExplainRequest{Composed: &types.ComposedRequest{
		Requests: []*types.Request{slotReq(), slotReq()},
		Overlays: []types.ComposeOverlaySpec{{Kind: types.OverlayKindShareOfTotal, Reference: "request_1", Targets: []string{"request_2"}}},
	}}, c)
	if comp.Root != descriptor.ExplainRootComposed || !strings.HasPrefix(comp.Summary, "This request batch runs 2 requests side by side") {
		t.Errorf("compose summary = %q", comp.Summary)
	}
	if !slices.ContainsFunc(comp.Steps, func(s descriptor.ExplainStep) bool {
		return s.Slot == "requests[1].aggregations[0]" && s.Operator == string(types.AGG_SUM) && s.Defaulted
	}) {
		t.Errorf("compose slot 1 aggregation not defaulted: %+v", comp.Steps)
	}
	if len(comp.DefaultsApplied) != 2 || !slices.Equal(comp.DefaultsApplied[1].Path[:2], []string{"Requests", "1"}) {
		t.Errorf("compose defaults = %+v", comp.DefaultsApplied)
	}
	if !slices.ContainsFunc(comp.Steps, func(s descriptor.ExplainStep) bool { return s.Slot == "overlays[0]" }) {
		t.Error("compose overlay not described")
	}

	chain := explain(t, nil, descriptor.ExplainRequest{Chain: &types.ChainRequest{
		Cohort: fixtureCohort,
		Stages: []*types.ChainStage{{Request: slotReq()}, {Request: &types.Request{Aggregations: []*types.Aggregation{{Field: "score"}}}}},
	}}, c)
	if !strings.HasPrefix(chain.Summary, "This chain reads `fixture.pulse` and runs 2 stages") {
		t.Errorf("chain summary = %q", chain.Summary)
	}
	if !slices.ContainsFunc(chain.Steps, func(s descriptor.ExplainStep) bool {
		return s.Slot == "stages[0].request.aggregations[0]" && s.Defaulted
	}) {
		t.Errorf("chain stage 0 not defaulted: %+v", chain.Steps)
	}
	if !slices.ContainsFunc(chain.Caveats, func(c string) bool { return strings.Contains(c, "previous stage's output") }) {
		t.Errorf("chain caveats = %v", chain.Caveats)
	}

	facet := explain(t, nil, descriptor.ExplainRequest{Facet: &types.FacetRequest{
		Cohort: fixtureCohort, Fields: []string{"region", "score"},
		Filterers:          []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "arm", Values: []string{"control"}}},
		NumericPercentiles: []float64{0.5},
	}}, c)
	if facet.Valid == nil || !*facet.Valid || !strings.HasPrefix(facet.Summary, "This facet request summarises 2 fields of `fixture.pulse` and runs 1 filter") {
		t.Errorf("facet: valid=%v summary=%q caveats=%v", facet.Valid, facet.Summary, facet.Caveats)
	}

	sample := explain(t, nil, descriptor.ExplainRequest{Sample: &types.SampleRequest{Cohort: fixtureCohort, N: 5}}, c)
	if sample.Valid != nil || sample.Summary != "This sample request returns up to 5 rows of `fixture.pulse`." {
		t.Errorf("sample: valid=%v summary=%q", sample.Valid, sample.Summary)
	}
}

// TestExplain_FullDetail: full detail adds the narrative, the linked
// glossary terms, the declared follow-ups and the tests' assumptions;
// terse carries none of them.
func TestExplain_FullDetail(t *testing.T) {
	r := everySlotRequest()
	full := explain(t, nil, descriptor.ExplainRequest{Request: r, Detail: descriptor.ExplainFull}, Checks{})
	if len(full.Sentences) != len(full.Steps)+1 || full.Sentences[0] != full.Summary {
		t.Errorf("sentences = %v", full.Sentences)
	}
	anova, _ := descx.PurposeOf(string(types.TEST_ANOVA_F))
	for _, id := range anova.Glossary {
		if !slices.Contains(full.GlossaryRefs, id) {
			t.Errorf("glossary_refs %v lacks %s", full.GlossaryRefs, id)
		}
	}
	if !slices.ContainsFunc(full.FollowUps, func(a descriptor.Alternative) bool { return a.Use == string(types.TEST_TUKEY_HSD) }) {
		t.Errorf("follow_ups = %+v", full.FollowUps)
	}
	if !slices.Contains(full.Caveats, "TEST_ANOVA_F: "+anova.Assumptions[0]) {
		t.Errorf("caveats lack the ANOVA assumption: %v", full.Caveats)
	}
	terse := explain(t, nil, descriptor.ExplainRequest{Request: r}, Checks{})
	if slices.Contains(terse.Caveats, "TEST_ANOVA_F: "+anova.Assumptions[0]) {
		t.Error("terse result states assumptions")
	}
}

// TestExplain_ProfileHidesOperators: on a profiled instance a step
// naming a hidden operator says so without naming it, and no hidden
// name appears anywhere in the result — follow-ups and glossary
// included.
func TestExplain_ProfileHidesOperators(t *testing.T) {
	hidden := []string{"TEST_TUKEY_HSD", "AGG_MEDIAN"}
	inst := profiled(hidden...)
	r := everySlotRequest()
	r.PostTests = nil
	r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "score"})
	res := explain(t, inst, descriptor.ExplainRequest{Request: r, Detail: descriptor.ExplainFull}, Checks{})
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hidden {
		if strings.Contains(string(raw), h) {
			t.Errorf("result names hidden %s: %s", h, raw)
		}
	}
	st := res.Steps[slices.IndexFunc(res.Steps, func(s descriptor.ExplainStep) bool { return s.Slot == "aggregations[1]" })]
	if st.Operator != "" || !strings.Contains(st.Text, "does not offer") {
		t.Errorf("hidden step = %+v", st)
	}
	// The prune bit: the default instance names both.
	def := explain(t, nil, descriptor.ExplainRequest{Request: r, Detail: descriptor.ExplainFull}, Checks{})
	raw, _ = json.Marshal(def)
	for _, h := range hidden {
		if !strings.Contains(string(raw), h) {
			t.Errorf("fixture drift: default instance does not name %s", h)
		}
	}
}

// TestExplain_ProseLint: every sentence Explain writes, over every
// root and both details, passes the guidance prose lint.
func TestExplain_ProseLint(t *testing.T) {
	c := explainChecks(t, nil)
	reqs := []descriptor.ExplainRequest{
		{Request: everySlotRequest()},
		{Request: &types.Request{Cohort: fixtureCohort, Aggregations: []*types.Aggregation{{Field: "score"}},
			Tests: []*types.Test{{Type: types.TEST_T, Field: "score", SplitBy: "region"}}}},
		{Composed: &types.ComposedRequest{Requests: []*types.Request{everySlotRequest()}, Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodBH}}},
		{Chain: &types.ChainRequest{Cohort: fixtureCohort, Stages: []*types.ChainStage{{Request: everySlotRequest()}, {Request: &types.Request{Groups: []*types.Group{{Field: "x"}}}}}}},
		{Facet: &types.FacetRequest{Fields: []string{"region"}, AdditiveFields: []string{"region"}, DiscreteTopK: 3, IncludeHistogram: true}},
		{Sample: &types.SampleRequest{N: 1, Labels: []*types.LabelBinding{{Field: "region", Table: "t"}}}},
	}
	n := 0
	for _, req := range reqs {
		for _, d := range []descriptor.ExplainDetail{descriptor.ExplainTerse, descriptor.ExplainFull} {
			req.Detail = d
			res := explain(t, nil, req, c)
			for _, s := range explainTexts(res) {
				n++
				for _, h := range descx.LintGuidanceText(s) {
					t.Errorf("%s/%s: lint %s on %q in %q", res.Root, d, h.Rule, h.Span, s)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("vacuous: no text linted")
	}
}

// explainTexts is every prose string Explain wrote into res.
func explainTexts(res *descriptor.ExplainResult) []string {
	out := []string{res.Summary}
	for _, s := range res.Steps {
		out = append(out, s.Text)
	}
	for _, a := range res.FollowUps {
		out = append(out, a.When)
	}
	out = append(out, res.Sentences...)
	return append(out, res.Caveats...)
}

// TestExplain_ProseLint_Falsifier: the lint the gate runs bites on a
// template that overstates a result.
func TestExplain_ProseLint_Falsifier(t *testing.T) {
	if len(descx.LintGuidanceText("The test shows no difference between the groups.")) == 0 {
		t.Fatal("lint passes an ASA-NODIFF sentence")
	}
}
