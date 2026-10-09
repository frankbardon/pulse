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
	{"describe", "AGG_RATIO"}:                   "predict refuses an AGG_RATIO with no field",
	{"composition", "AGG_RATIO"}:                "predict refuses an AGG_RATIO with no field",
	{"describe", "AGG_WEIGHTED_MEAN"}:           "needs a weight the fixture does not name",
	{"relationship", "ATTR_REG_FITTED"}:         "regression attributes take target and predictors the manifest does not declare",
	{"benchmark", "ATTR_REG_FITTED"}:            "regression attributes take target and predictors the manifest does not declare",
	{"benchmark", "ATTR_REG_RESIDUAL"}:          "regression attributes take target and predictors the manifest does not declare",
	{"data_quality", "ATTR_REG_RESIDUAL"}:       "regression attributes take target and predictors the manifest does not declare",
	{"data_quality", "ATTR_REG_LEVERAGE"}:       "regression attributes take target and predictors the manifest does not declare",
	{"segment", "FEAT_BUCKETIZE"}:               "needs boundaries or quantiles, neither declared required",
	{"compare_groups", "GROUP_DATE_RANGES"}:     "compare_groups takes no date field",
	{"compare_groups", "GROUP_SET_PER_ELEMENT"}: "compare_groups takes no set field",
}

// TestRecommendBound_ServingOperatorsMapped covers the slot mapping for
// every operator that serves an intent: each draftable server yields a
// draft over the fixture unless unmappedOnFixture says why not.
func TestRecommendBound_ServingOperatorsMapped(t *testing.T) {
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
	want := []struct {
		intent, op string
		needs      []string
	}{
		{descx.IntentCompareGroups, "TEST_PROP_Z", []string{"success"}},
		{descx.IntentBenchmark, "TEST_T", []string{"mu"}},
		{descx.IntentDrivers, "REG_GLM", []string{"family"}},
		{descx.IntentDescribe, "AGG_PERCENTILE", []string{"percentile"}},
		{descx.IntentCompareGroups, "TEST_TUKEY_HSD", []string{"ms_within", "df_within"}},
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
			if r.Bound && !strings.Contains(r.Why, " It uses ") {
				t.Errorf("%s: why names no field: %q", r.Operator, r.Why)
			}
			if (len(r.Needs) > 0) != strings.Contains(r.Why, unboundWhy) {
				t.Errorf("%s: placeholder sentence does not follow needs: %q", r.Operator, r.Why)
			}
		}
	}
}
