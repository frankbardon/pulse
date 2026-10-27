package guide

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

func dict(t testing.TB, values ...string) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range values {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// boundFixture is the cohort bound mode is tested over: two measures, a
// 4-level and a 2-level categorical, a flag, a date and a multi-select.
// Every field is described so predict raises nothing of its own.
func boundFixture(t testing.TB) *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeF64, Description: "Test score of the student"},
		{Name: "hours", Type: encoding.FieldTypeF64, Description: "Hours the student studied"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Region the student lives in", Dictionary: dict(t, "n", "s", "e", "w")},
		{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Description: "Arm the student was assigned to", Dictionary: dict(t, "control", "treatment")},
		{Name: "passed", Type: encoding.FieldTypePackedBool, Description: "Whether the student passed the course"},
		{Name: "taken_on", Type: encoding.FieldTypeDate, Description: "Day the student sat the test"},
		{Name: "topics", Type: encoding.FieldTypeSetU8, Description: "Topics the student chose to study", Dictionary: dict(t, "a", "b", "c")},
	}}
}

func cohortBytes(t testing.TB, s *encoding.Schema) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, s); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// bound wires a Bound over schema s for instance inst; calls counts the
// predicts it ran.
func bound(t testing.TB, inst *descx.InstanceSnapshot, s *encoding.Schema, calls *int) Bound {
	data := cohortBytes(t, s)
	return Bound{
		Cohort: &types.Cohort{Filename: "fixture.pulse"},
		Schema: s,
		Predict: func(r *types.Request) *descriptor.Envelope {
			if calls != nil {
				*calls++
			}
			return descx.Predict(bytes.NewReader(data), r, &descx.PredictOptions{Instance: inst})
		},
	}
}

func boundAll(t *testing.T, inst *descx.InstanceSnapshot, intent string, fields ...string) *descriptor.RecommendResult {
	t.Helper()
	res, err := RecommendBound(inst, descriptor.RecommendRequest{Intent: intent, Fields: fields, Limit: 1000}, bound(t, inst, boundFixture(t), nil))
	if err != nil {
		t.Fatalf("RecommendBound(%s): %v", intent, err)
	}
	return res
}

// TestRecommendBound_EveryDraftPassesPredict: over every intent, each
// fully bound draft is valid under an independent predict of its own
// wire request, carries no placeholder, and reports exactly that
// predict's advisories; a draft with needs names a placeholder per need
// and is Bound false.
func TestRecommendBound_EveryDraftPassesPredict(t *testing.T) {
	t.Parallel()

	data := cohortBytes(t, boundFixture(t))
	boundCount := 0
	for _, in := range descx.Intents() {
		res := boundAll(t, nil, in.ID)
		if !res.Bound {
			t.Errorf("%s: cohort-bound result reports bound=false", in.ID)
		}
		for _, r := range res.Recommendations {
			if len(r.Needs) > 0 {
				if r.Bound {
					t.Errorf("%s %s: needs %v but bound", in.ID, r.Operator, r.Needs)
				}
				var keys []string
				for _, n := range r.Needs {
					keys = append(keys, n.Param)
					if !strings.Contains(string(r.Request), "<"+n.Param+">") {
						t.Errorf("%s %s: need %s has no placeholder in %s", in.ID, r.Operator, n.Param, r.Request)
					}
				}
				slices.Sort(keys)
				if !slices.Equal(keys, r.Placeholders) {
					t.Errorf("%s %s: placeholders %v, needs %v", in.ID, r.Operator, r.Placeholders, keys)
				}
				continue
			}
			if !r.Bound || len(r.Placeholders) != 0 || strings.Contains(string(r.Request), "\"<") {
				t.Errorf("%s %s: fully bound draft not clean: bound=%v %s", in.ID, r.Operator, r.Bound, r.Request)
			}
			var req types.Request
			if err := json.Unmarshal(r.Request, &req); err != nil {
				t.Fatalf("%s %s: draft does not decode: %v", in.ID, r.Operator, err)
			}
			env := descx.Predict(bytes.NewReader(data), &req, &descx.PredictOptions{})
			pr := env.Data.(*descriptor.PredictResult)
			if !pr.Valid || len(env.Errors) != 0 {
				t.Errorf("%s %s: draft fails predict: %s %+v", in.ID, r.Operator, r.Request, env.Errors[0])
			}
			if len(pr.Advisories) != len(r.Advisories) {
				t.Errorf("%s %s: advisories %+v, predict says %+v", in.ID, r.Operator, r.Advisories, pr.Advisories)
			}
			boundCount++
		}
	}
	if boundCount < 100 {
		t.Fatalf("vacuous: only %d bound drafts over the fixture", boundCount)
	}
}

// unmappedOnFixture are the serving operators the fixture cohort yields
// no draft for, and why. Exact: an operator that starts drafting (or
// stops) fails the test until this list follows.
var unmappedOnFixture = map[[2]string]string{
	{"compare_groups", "GROUP_DATE_RANGES"}:     "compare_groups takes no date field",
	{"compare_groups", "GROUP_SET_PER_ELEMENT"}: "compare_groups takes no set field",
	// A share / lift window reads a numeric column — on a mix table the
	// count an aggregation emits per category — and composition binds
	// only categorical fields; benchmark drafts both.
	{"composition", "WIN_LIFT"}:  "composition takes no numeric field",
	{"composition", "WIN_SHARE"}: "composition takes no numeric field",
}

// TestRecommendBound_ServingOperatorsMapped covers the slot mapping for
// every operator that serves an intent: each draftable server yields a
// draft over the fixture unless unmappedOnFixture says why not.
func TestRecommendBound_ServingOperatorsMapped(t *testing.T) {
	t.Parallel()

	cat := catalog(descx.BuildManifestForInstance(nil))
	g := (*descx.InstanceSnapshot)(nil).Ontology()
	got := map[[2]string]bool{}
	for _, in := range descx.Intents() {
		if !in.Analytic {
			continue
		}
		drafted := map[string]bool{}
		for _, r := range boundAll(t, nil, in.ID).Recommendations {
			drafted[r.Operator] = true
		}
		for _, name := range g.OperatorsServing(in.ID) {
			if _, ok := cat[name]; !ok {
				continue
			}
			if !drafted[name] {
				got[[2]string{in.ID, name}] = true
			}
		}
	}
	for k := range got {
		if _, ok := unmappedOnFixture[k]; !ok {
			t.Errorf("%s %s: no draft over the fixture", k[0], k[1])
		}
	}
	for k := range unmappedOnFixture {
		if !got[k] {
			t.Errorf("%s %s: listed as unmapped but now drafts; drop it from unmappedOnFixture", k[0], k[1])
		}
	}
}

// TestRecommendBound_NeedsOperators: the user-only-param operators are
// emitted with their fields bound, Bound false, needs naming the param,
// and rank after every fully bound draft.
func TestRecommendBound_NeedsOperators(t *testing.T) {
	t.Parallel()

	want := []struct {
		intent, op string
		needs      []string
	}{
		{descx.IntentCompareGroups, "TEST_PROP_Z", []string{"success"}},
		{descx.IntentBenchmark, "TEST_T", []string{"mu"}},
		{descx.IntentDrivers, "REG_GLM", []string{"family"}},
		{descx.IntentDescribe, "AGG_PERCENTILE", []string{"percentile"}},
		{descx.IntentCompareGroups, "TEST_TUKEY_HSD", []string{"ms_within", "df_within"}},
		// exactly_one_of groups: the first member is the need.
		{descx.IntentChangeOverTime, "WIN_EWMA", []string{"frame", "alpha"}},
		{descx.IntentSegment, "FEAT_BUCKETIZE", []string{"boundaries"}},
		// No weight anywhere: the weighted mean asks for one.
		{descx.IntentDescribe, "AGG_WEIGHTED_MEAN", []string{"weight_field"}},
	}
	for _, w := range want {
		res := boundAll(t, nil, w.intent)
		found := false
		for _, r := range res.Recommendations {
			if r.Operator != w.op {
				continue
			}
			found = true
			var got []string
			for _, n := range r.Needs {
				got = append(got, n.Param)
				if n.Why == "" {
					t.Errorf("%s: need %s has no why", w.op, n.Param)
				}
			}
			if r.Bound || !slices.Equal(got, w.needs) {
				t.Errorf("%s/%s: bound=%v needs=%v, want needs %v", w.intent, w.op, r.Bound, got, w.needs)
			}
			if strings.Contains(string(r.Request), "<field>") || strings.Contains(string(r.Request), "<cohort>") {
				t.Errorf("%s: fields not bound: %s", w.op, r.Request)
			}
		}
		if !found {
			t.Errorf("%s: %s absent", w.intent, w.op)
		}
	}
}

// TestRecommendBound_NeedsRankBelowBound: without hints, no draft with
// needs ranks ahead of a fully bound one, on any intent.
func TestRecommendBound_NeedsRankBelowBound(t *testing.T) {
	t.Parallel()

	sawBoth := false
	for _, in := range descx.Intents() {
		recs := boundAll(t, nil, in.ID).Recommendations
		seenNeeds := false
		for _, r := range recs {
			if len(r.Needs) > 0 {
				seenNeeds = true
				continue
			}
			if seenNeeds {
				t.Errorf("%s: fully bound %s ranks after a draft with needs", in.ID, r.Operator)
				break
			}
		}
		if seenNeeds && len(recs) > 0 && len(recs[0].Needs) == 0 {
			sawBoth = true
		}
	}
	if !sawBoth {
		t.Fatal("vacuous: no intent mixes bound drafts and drafts with needs")
	}
}

// TestRank_KeyOrder pins each rank key against the next: hints, then
// fully bound before needs, then level, then advisories, then category,
// then name.
func TestRank_KeyOrder(t *testing.T) {
	t.Parallel()

	rec := func(op, cat string, lvl descriptor.Level, adv int, needs bool) descriptor.Recommendation {
		r := descriptor.Recommendation{Operator: op, Category: cat, Level: lvl}
		for range adv {
			r.Advisories = append(r.Advisories, descriptor.Advisory{Code: "X"})
		}
		if needs {
			r.Needs = []descriptor.RecommendNeed{{Param: "p", Why: "w"}}
		}
		return r
	}
	cases := []struct {
		name string
		a, b scored
	}{
		{"hints beat needs", scored{rec("Z", catPostTest, descriptor.LevelAdvanced, 3, true), 1}, scored{rec("A", catTest, descriptor.LevelBasic, 0, false), 0}},
		{"bound beats level", scored{rec("Z", catPostTest, descriptor.LevelAdvanced, 3, false), 0}, scored{rec("A", catTest, descriptor.LevelBasic, 0, true), 0}},
		{"level beats advisories", scored{rec("Z", catPostTest, descriptor.LevelBasic, 3, false), 0}, scored{rec("A", catTest, descriptor.LevelAdvanced, 0, false), 0}},
		{"advisories beat category", scored{rec("Z", catPostTest, descriptor.LevelBasic, 0, false), 0}, scored{rec("A", catTest, descriptor.LevelBasic, 1, false), 0}},
		{"category beats name", scored{rec("Z", catTest, descriptor.LevelBasic, 0, false), 0}, scored{rec("A", catPostTest, descriptor.LevelBasic, 0, false), 0}},
		{"name last", scored{rec("A", catTest, descriptor.LevelBasic, 0, false), 0}, scored{rec("B", catTest, descriptor.LevelBasic, 0, false), 0}},
	}
	for _, c := range cases {
		if !before(c.a, c.b, "") || before(c.b, c.a, "") {
			t.Errorf("%s: order not strict", c.name)
		}
		recs := []scored{c.b, c.a}
		rank(recs, "")
		if recs[0].rec.Operator != c.a.rec.Operator {
			t.Errorf("%s: rank put %s first", c.name, recs[0].rec.Operator)
		}
	}
}

// TestRecommendBound_AdvisoriesRankAndAttach: a TEST_T over the 4-level
// region raises the two-group advisory and ranks after the TEST_T over
// the 2-level arm, which raises none.
func TestRecommendBound_AdvisoriesRankAndAttach(t *testing.T) {
	t.Parallel()

	code := string(errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS)
	var order []string
	for _, r := range boundAll(t, nil, descx.IntentCompareGroups).Recommendations {
		if r.Operator != "TEST_T" {
			continue
		}
		var req types.Request
		if err := json.Unmarshal(r.Request, &req); err != nil {
			t.Fatal(err)
		}
		split := req.Tests[0].SplitBy
		order = append(order, split)
		fired := slices.ContainsFunc(r.Advisories, func(a descriptor.Advisory) bool { return a.Code == code })
		if fired != (split == "region") {
			t.Errorf("TEST_T split_by %s: advisory fired=%v %+v", split, fired, r.Advisories)
		}
	}
	if len(order) < 2 || order[0] != "arm" {
		t.Errorf("TEST_T drafts in order %v, want the advisory-free arm first", order)
	}
}

// TestRecommendBound_HintsPinRoles: hinted fields fill the roles they
// fit and every draft uses one; the draft using the most hints ranks
// first.
func TestRecommendBound_HintsPinRoles(t *testing.T) {
	t.Parallel()

	res := boundAll(t, nil, descx.IntentCompareGroups, "hours", "region")
	if len(res.Recommendations) == 0 {
		t.Fatal("no drafts for hinted fields")
	}
	both := func(r descriptor.Recommendation) bool {
		return strings.Contains(string(r.Request), `"hours"`) && strings.Contains(string(r.Request), `"region"`)
	}
	if !both(res.Recommendations[0]) {
		t.Errorf("top draft does not use both hints: %s", res.Recommendations[0].Request)
	}
	tt := false
	for _, r := range res.Recommendations {
		if !strings.Contains(string(r.Request), `"hours"`) && !strings.Contains(string(r.Request), `"region"`) {
			t.Errorf("%s: draft uses no hint: %s", r.Operator, r.Request)
		}
		if r.Operator == "TEST_T" {
			tt = true
			if !strings.Contains(string(r.Request), `"field":"hours"`) || !strings.Contains(string(r.Request), `"split_by":"region"`) {
				t.Errorf("TEST_T roles not pinned: %s", r.Request)
			}
		}
	}
	if !tt {
		t.Error("no TEST_T draft for an outcome and a group hint")
	}
	// Unhinted, hours is not the first numeric field: the pin is real.
	for _, r := range boundAll(t, nil, descx.IntentCompareGroups).Recommendations {
		if r.Operator == "TEST_T" && strings.Contains(string(r.Request), `"field":"hours"`) {
			t.Fatalf("vacuous: unhinted TEST_T already starts at hours")
		}
		if r.Operator == "TEST_T" {
			break
		}
	}
}

// TestRecommendBound_BadHintIsCoded: a hint the cohort lacks, or whose
// kind no role of the intent takes, is SERVICE_VALIDATION naming it.
func TestRecommendBound_BadHintIsCoded(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ intent, hint, kind string }{
		{descx.IntentCompareGroups, "nope", ""},
		{descx.IntentCompareGroups, "taken_on", "date"},
		{descx.IntentDescribe, "taken_on", "date"},
	} {
		_, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: c.intent, Fields: []string{"score", c.hint}}, bound(t, nil, boundFixture(t), nil))
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
			t.Fatalf("%s %s: err = %v, want SERVICE_VALIDATION", c.intent, c.hint, err)
		}
		if ce.Details["hint"] != c.hint || !strings.Contains(ce.Message, c.hint) {
			t.Errorf("%s: error does not name the hint: %v %+v", c.hint, ce.Message, ce.Details)
		}
		if c.kind != "" && ce.Details["kind"] != c.kind {
			t.Errorf("%s: kind %v, want %s", c.hint, ce.Details["kind"], c.kind)
		}
	}
	// The same date hint is fine where a role takes dates.
	if _, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: descx.IntentChangeOverTime, Fields: []string{"taken_on"}}, bound(t, nil, boundFixture(t), nil)); err != nil {
		t.Errorf("change_over_time date hint: %v", err)
	}
}

// wideSchema has 32 numeric fields and 4 categorical ones.
func wideSchema(t testing.TB) *encoding.Schema {
	s := &encoding.Schema{}
	for i := range 32 {
		s.Fields = append(s.Fields, encoding.Field{Name: "m" + strconv.Itoa(i), Type: encoding.FieldTypeF64, Description: "Measured amount number " + strconv.Itoa(i)})
	}
	for i := range 4 {
		s.Fields = append(s.Fields, encoding.Field{Name: "c" + strconv.Itoa(i), Type: encoding.FieldTypeCategoricalU8, Description: "Category label number " + strconv.Itoa(i), Dictionary: dict(t, "x", "y")})
	}
	return s
}

// TestRecommendBound_WideSchemaCapped: on a 36-field cohort, no
// operator gets more than K bindings, predict runs at most K times per
// draftable operator, the default limit cuts the list and
// candidates_considered counts the survivors before the cut.
func TestRecommendBound_WideSchemaCapped(t *testing.T) {
	t.Parallel()

	s := wideSchema(t)
	for _, intent := range []string{descx.IntentDescribe, descx.IntentCompareGroups, descx.IntentRelationship} {
		calls := 0
		capped, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: intent}, bound(t, nil, s, &calls))
		if err != nil {
			t.Fatal(err)
		}
		full, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: intent, Limit: 1000}, bound(t, nil, s, nil))
		if err != nil {
			t.Fatal(err)
		}
		if len(capped.Recommendations) != DefaultLimit || !capped.Truncated {
			t.Errorf("%s: n=%d truncated=%v", intent, len(capped.Recommendations), capped.Truncated)
		}
		if capped.CandidatesConsidered != len(full.Recommendations) || full.Truncated {
			t.Errorf("%s: candidates_considered %d, uncapped list has %d", intent, capped.CandidatesConsidered, len(full.Recommendations))
		}
		ops := map[string]int{}
		for _, r := range full.Recommendations {
			ops[r.Operator]++
		}
		draftable := 0
		cat := catalog(descx.BuildManifestForInstance(nil))
		for _, name := range (*descx.InstanceSnapshot)(nil).Ontology().OperatorsServing(intent) {
			if _, ok := cat[name]; ok {
				draftable++
			}
		}
		for op, n := range ops {
			if n > BindingsPerOperator {
				t.Errorf("%s: %s has %d bindings", intent, op, n)
			}
		}
		if calls > BindingsPerOperator*draftable {
			t.Errorf("%s: %d predict calls for %d operators", intent, calls, draftable)
		}
		if calls == 0 {
			t.Fatalf("%s: vacuous: no predict ran", intent)
		}
	}
}

func TestRecommendBound_ProfileHidesOperators(t *testing.T) {
	t.Parallel()

	hidden := []string{"TEST_TUKEY_HSD", "TEST_WELCH", "AGG_MEDIAN", "REG_GLM", "TEST_ANOVA_WELCH"}
	inst := profiled(hidden...)
	for _, in := range descx.Intents() {
		res, err := RecommendBound(inst, descriptor.RecommendRequest{Intent: in.ID, Limit: 1000}, bound(t, inst, boundFixture(t), nil))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Recommendations {
			names := []string{r.Operator}
			for _, a := range append(r.Alternatives, r.FollowUps...) {
				names = append(names, a.Use)
			}
			for _, a := range r.Advisories {
				if s, ok := a.Details["suggested"].(string); ok {
					names = append(names, s)
				}
				for _, h := range hidden {
					if strings.Contains(a.Message, h) {
						t.Errorf("%s %s: advisory names hidden %s", in.ID, r.Operator, h)
					}
				}
			}
			for _, n := range names {
				if inst.Hidden(n) {
					t.Errorf("%s: %s names hidden %s", in.ID, r.Operator, n)
				}
			}
		}
	}
}

// TestRecommend_NeedsWhyLint: the bound why template and every need's
// why pass the guidance lint.
func TestRecommend_NeedsWhyLint(t *testing.T) {
	t.Parallel()

	lint := func(where, s string) {
		for _, h := range descx.LintGuidanceText(s) {
			t.Errorf("%s: lint %s on %q", where, h.Rule, h.Span)
		}
	}
	for k, w := range needWhy {
		lint("need "+k, w)
	}
	lint("need default", needWhyDefault)
	for _, in := range descx.Intents() {
		for _, r := range boundAll(t, nil, in.ID).Recommendations {
			lint(r.Operator+" why", r.Why)
			for _, n := range r.Needs {
				lint(r.Operator+" need "+n.Param, n.Why)
			}
			if r.Bound && !strings.Contains(r.Why, " It uses ") {
				t.Errorf("%s: why names no field: %q", r.Operator, r.Why)
			}
			if (len(r.Needs) > 0) != strings.Contains(r.Why, unboundWhy) {
				t.Errorf("%s: placeholder sentence does not follow needs: %q", r.Operator, r.Why)
			}
		}
	}
}

// TestRecommendBound_VectorAggregatorDraftsFields: a vector aggregator
// (AGG_VEC_MEAN / AGG_VEC_SUM) is drafted fully bound with its members
// as an inline fields list of the schema's numeric fields — never a
// scalar field — and the draft decodes to a vector-input aggregation.
func TestRecommendBound_VectorAggregatorDraftsFields(t *testing.T) {
	t.Parallel()

	for _, op := range []string{"AGG_VEC_MEAN", "AGG_VEC_SUM"} {
		found := false
		for _, r := range boundAll(t, nil, descx.IntentDescribe).Recommendations {
			if r.Operator != op {
				continue
			}
			found = true
			var req types.Request
			if err := json.Unmarshal(r.Request, &req); err != nil {
				t.Fatalf("%s: draft does not decode: %v", op, err)
			}
			if !r.Bound || len(req.Aggregations) != 1 {
				t.Fatalf("%s: bound=%v draft %s", op, r.Bound, r.Request)
			}
			a := req.Aggregations[0]
			if a.Field != "" || !slices.Equal(a.Fields, []string{"score", "hours"}) {
				t.Errorf("%s: field=%q fields=%v, want fields [score hours]: %s", op, a.Field, a.Fields, r.Request)
			}
		}
		if !found {
			t.Errorf("%s: no describe draft", op)
		}
	}
}

// draftsOf is every recommendation for op under intent over bound b,
// each draft's request decoded.
func draftsOf(t *testing.T, b Bound, intent, op string) []descriptor.Recommendation {
	t.Helper()
	res, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: intent, Limit: 1000}, b)
	if err != nil {
		t.Fatal(err)
	}
	var out []descriptor.Recommendation
	for _, r := range res.Recommendations {
		if r.Operator == op {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s: %s drafts nothing", intent, op)
	}
	return out
}

func decodeDraft(t *testing.T, r descriptor.Recommendation) *types.Request {
	t.Helper()
	var req types.Request
	if err := json.Unmarshal(r.Request, &req); err != nil {
		t.Fatalf("%s: %v", r.Request, err)
	}
	return &req
}

// TestRecommendBound_RatioAndRegressionAttributes (U35 #249): AGG_RATIO
// ignores its field but the wire requires one, so the draft names its
// numerator there; ATTR_REG_FITTED / RESIDUAL / LEVERAGE draft their
// declared top-level target and predictors and no field.
func TestRecommendBound_RatioAndRegressionAttributes(t *testing.T) {
	t.Parallel()

	b := bound(t, nil, boundFixture(t), nil)
	for _, r := range draftsOf(t, b, descx.IntentDescribe, "AGG_RATIO") {
		a := decodeDraft(t, r).Aggregations[0]
		var params struct {
			Num string `json:"numerator_field"`
			Den string `json:"denominator_field"`
		}
		if err := json.Unmarshal(a.Params, &params); err != nil {
			t.Fatal(err)
		}
		if !r.Bound || a.Field == "" || a.Field != params.Num || params.Den == "" {
			t.Errorf("AGG_RATIO draft %s: bound=%v, want field = numerator_field", r.Request, r.Bound)
		}
	}
	for _, c := range []struct{ intent, op string }{
		{descx.IntentRelationship, "ATTR_REG_FITTED"},
		{"data_quality", "ATTR_REG_RESIDUAL"},
		{"data_quality", "ATTR_REG_LEVERAGE"},
	} {
		for _, r := range draftsOf(t, b, c.intent, c.op) {
			a := decodeDraft(t, r).Attributes[0]
			if !r.Bound || a.Field != "" || a.Target == "" || len(a.Predictors) == 0 || len(a.Params) != 0 ||
				slices.Contains(a.Predictors, a.Target) {
				t.Errorf("%s draft %s: bound=%v, want top-level target + predictors, no field", c.op, r.Request, r.Bound)
			}
		}
	}
}

// TestRecommendBound_WeightedMeanWeight (U35 #249): AGG_WEIGHTED_MEAN
// binds params.weight_field to the cohort's suggested weight
// (Bound.Weight) — a fully bound draft predict accepts, never weighting
// a field by itself; with no suggestion but an instance default weight
// the draft needs nothing; with neither it asks for weight_field
// (TestRecommendBound_NeedsOperators).
func TestRecommendBound_WeightedMeanWeight(t *testing.T) {
	t.Parallel()

	b := bound(t, nil, boundFixture(t), nil)
	b.Weight = "hours"
	for _, r := range draftsOf(t, b, descx.IntentDescribe, "AGG_WEIGHTED_MEAN") {
		a := decodeDraft(t, r).Aggregations[0]
		if !r.Bound || len(r.Needs) != 0 || !strings.Contains(string(a.Params), `"weight_field":"hours"`) || a.Field == "hours" {
			t.Errorf("suggested weight: draft %s bound=%v needs=%+v, want weight_field hours on another field", r.Request, r.Bound, r.Needs)
		}
	}

	s := boundFixture(t)
	data := cohortBytes(t, s)
	b = bound(t, nil, s, nil)
	b.Predict = func(r *types.Request) *descriptor.Envelope {
		return descx.Predict(bytes.NewReader(data), r, &descx.PredictOptions{DefaultWeight: &types.WeightSpec{Field: "hours"}})
	}
	for _, r := range draftsOf(t, b, descx.IntentDescribe, "AGG_WEIGHTED_MEAN") {
		if !r.Bound || len(r.Needs) != 0 || strings.Contains(string(r.Request), "weight_field") {
			t.Errorf("default weight: draft %s bound=%v needs=%+v, want no weight ask", r.Request, r.Bound, r.Needs)
		}
	}
}

// TestBind_PackedBoolIsATwoLevelCategory: the binder's level slots use
// the acceptance table's level set (fieldaccept's categoricalValue
// class), so a packed_bool fills split_by, a contingency axis,
// TEST_PROP_Z's outcome and a categorical field_filter (stratify) as a
// two-level category — and the two-level split_by slot — while
// TEST_ANOVA_RM's subject stays categorical-only.
func TestBind_PackedBoolIsATwoLevelCategory(t *testing.T) {
	t.Parallel()

	passed := &encoding.Field{Name: "passed", Type: encoding.FieldTypePackedBool}
	region := &encoding.Field{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict(t, "n", "s", "e", "w")}
	arm := &encoding.Field{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict(t, "control", "treatment")}
	score := &encoding.Field{Name: "score", Type: encoding.FieldTypeF64}
	for _, c := range []struct {
		op, key string
		f       *encoding.Field
		want    bool
	}{
		{"TEST_T", "split_by", passed, true},
		{"TEST_HOTELLING_T2", "split_by", passed, true},
		{"TEST_HOTELLING_T2", "split_by", arm, true},
		{"TEST_HOTELLING_T2", "split_by", region, false},
		{"TEST_CHISQ", "rows", passed, true},
		{"TEST_FISHER_EXACT", "cols", passed, true},
		{"TEST_PROP_Z", "field", passed, true},
		{"TEST_PROP_Z", "field", score, false},
		{"TEST_ANOVA_RM", "subject_field", passed, false},
		{"TEST_ANOVA_RM", "subject_field", region, true},
		{"TEST_ANOVA_RM", "split_by", passed, true},
	} {
		if got := testSlot(opInfo{name: c.op, category: catTest}, c.key).accept(c.f); got != c.want {
			t.Errorf("%s %s accepts %s (%s) = %v, want %v", c.op, c.key, c.f.Name, c.f.Type, got, c.want)
		}
	}
	if !fieldFilterAccept("categorical")(passed) {
		t.Error(`field_filter "categorical" (stratify) refuses a packed_bool`)
	}
	if fieldFilterAccept("categorical")(score) {
		t.Error(`field_filter "categorical" accepts a numeric field`)
	}

	// End to end: a packed_bool group hint pins TEST_T's split_by, and
	// the draft passes predict (TestRecommendBound_EveryDraftPassesPredict
	// holds every draft to that).
	found := false
	for _, r := range boundAll(t, nil, descx.IntentCompareGroups, "score", "passed").Recommendations {
		if r.Operator == "TEST_T" {
			found = true
			if !strings.Contains(string(r.Request), `"split_by":"passed"`) {
				t.Errorf("TEST_T split_by not bound to the packed_bool hint: %s", r.Request)
			}
			if !strings.Contains(r.Why, "passed (packed_bool, 2 groups)") {
				t.Errorf("TEST_T why does not count the packed_bool's two levels: %s", r.Why)
			}
		}
	}
	if !found {
		t.Error("no TEST_T draft for a numeric outcome and a packed_bool group hint")
	}
}

// TestBind_ManyGroupTestsPreferThreeLevels: the k-group tests (ANOVA F,
// Welch's ANOVA, Kruskal-Wallis, Brown-Forsythe, Tukey HSD) are for
// three or more groups (their Purpose), so their split_by draws a
// field with at least three levels when the cohort has one — the
// 4-level region, never the 2-level arm or the packed_bool passed. A
// 2-level field still binds when hinted or when no 3+-level field
// exists (a preference, not a refusal), except TEST_TUKEY_HSD, whose
// runtime refuses fewer than three groups (PULSE_TEST_SPLIT_GROUPS_LT_2).
func TestBind_ManyGroupTestsPreferThreeLevels(t *testing.T) {
	t.Parallel()

	passed := &encoding.Field{Name: "passed", Type: encoding.FieldTypePackedBool}
	region := &encoding.Field{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict(t, "n", "s", "e", "w")}
	arm := &encoding.Field{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict(t, "control", "treatment")}
	for _, op := range []string{"TEST_ANOVA_F", "TEST_ANOVA_WELCH", "TEST_KRUSKAL_WALLIS", "TEST_BROWN_FORSYTHE", "TEST_TUKEY_HSD"} {
		s := testSlot(opInfo{name: op, category: catTest}, "split_by")
		if s.narrow == nil || !s.narrow(region) || s.narrow(arm) || s.narrow(passed) {
			t.Errorf("%s split_by: no 3+-level preference", op)
		}
		hard := op == "TEST_TUKEY_HSD"
		if s.accept(arm) == hard || s.accept(passed) == hard || !s.accept(region) {
			t.Errorf("%s split_by accepts arm=%v passed=%v region=%v, want 2-level %v", op, s.accept(arm), s.accept(passed), s.accept(region), !hard)
		}
	}
	for _, op := range []string{"TEST_T", "TEST_WELCH", "TEST_MANN_WHITNEY_U", "TEST_HOTELLING_T2", "TEST_MANOVA"} {
		if testSlot(opInfo{name: op, category: catTest}, "split_by").narrow != nil {
			t.Errorf("%s split_by narrows to 3+ levels", op)
		}
	}

	// End to end over the fixture (region 4 levels, arm 2, passed a
	// packed_bool): every many-group draft splits by region.
	b := bound(t, nil, boundFixture(t), nil)
	seen := 0
	for _, op := range []string{"TEST_ANOVA_F", "TEST_ANOVA_WELCH"} {
		for _, r := range draftsOf(t, b, descx.IntentCompareGroups, op) {
			seen++
			if got := decodeDraft(t, r).Tests[0].SplitBy; got != "region" {
				t.Errorf("%s drafts split_by %q, want the 4-level region: %s", op, got, r.Request)
			}
		}
	}
	if seen < 2 {
		t.Fatalf("vacuous: %d many-group drafts", seen)
	}

	// A hinted 2-level field still pins the split.
	hinted := false
	for _, r := range boundAll(t, nil, descx.IntentCompareGroups, "score", "passed").Recommendations {
		if r.Operator == "TEST_ANOVA_F" && strings.Contains(string(r.Request), `"split_by":"passed"`) {
			hinted = true
		}
	}
	if !hinted {
		t.Error("TEST_ANOVA_F does not bind a hinted packed_bool split")
	}

	// Without a 3+-level field the 2-level ones bind.
	two := &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeF64, Description: "Test score of the student"},
		{Name: "passed", Type: encoding.FieldTypePackedBool, Description: "Whether the student passed the course"},
	}}
	r := draftsOf(t, bound(t, nil, two, nil), descx.IntentCompareGroups, "TEST_ANOVA_F")
	if got := decodeDraft(t, r[0]).Tests[0].SplitBy; got != "passed" {
		t.Errorf("TEST_ANOVA_F over a 2-level-only cohort splits by %q, want passed", got)
	}
}

// TestRecommendBound_PreviousStateSubjectPrefersAnID: over a long panel
// (a row id, a subject id, a snapshot date and a plan), the lead flows
// draft's ATTR_PREVIOUS_STATE names the subject id as subject_field —
// an id-named field ahead of a categorical state or a bare row id the
// "any" field filter would also take — and the plan as the state.
func TestRecommendBound_PreviousStateSubjectPrefersAnID(t *testing.T) {
	t.Parallel()

	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "plan", Type: encoding.FieldTypeCategoricalU8, Description: "Plan the customer held at the snapshot", Dictionary: dict(t, "basic", "plus", "pro")},
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Snapshot row identifier"},
		{Name: "seen_at", Type: encoding.FieldTypeDate, Description: "Day of the snapshot"},
		{Name: "customer_id", Type: encoding.FieldTypeU32, Description: "Customer the snapshot belongs to"},
	}}
	res, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: descx.IntentFlows, Limit: 1000}, bound(t, nil, s, nil))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range res.Recommendations {
		if r.Operator != string(types.ATTR_PREVIOUS_STATE) {
			continue
		}
		if found {
			break // later drafts are alternatives; the lead one is judged
		}
		found = true
		var req types.Request
		if err := json.Unmarshal(r.Request, &req); err != nil {
			t.Fatal(err)
		}
		if len(req.Attributes) != 1 {
			t.Fatalf("draft attributes %s", r.Request)
		}
		a := req.Attributes[0]
		if a.Field != "plan" || a.SubjectField != "customer_id" || a.OrderField != "seen_at" {
			t.Errorf("draft field %q subject %q order %q, want plan / customer_id / seen_at: %s", a.Field, a.SubjectField, a.OrderField, r.Request)
		}
		if !r.Bound {
			t.Errorf("draft not bound: needs %v", r.Needs)
		}
	}
	if !found {
		t.Fatal("no ATTR_PREVIOUS_STATE draft for the flows intent")
	}
}
