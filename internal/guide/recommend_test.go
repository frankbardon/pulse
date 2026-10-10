package guide

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// all returns every recommendation for intent on inst (no cap).
func all(t *testing.T, inst *descx.InstanceSnapshot, intent string) *descriptor.RecommendResult {
	t.Helper()
	res, err := Recommend(inst, descriptor.RecommendRequest{Intent: intent, Limit: 1000})
	if err != nil {
		t.Fatalf("Recommend(%s): %v", intent, err)
	}
	return res
}

// noDraftIntents are the intents that must answer with routes, never
// drafts: the non-analytic three and the one no operator serves yet.
var noDraftIntents = map[string]bool{
	descx.IntentPrepare: true, descx.IntentSimulate: true, descx.IntentLookup: true,
	descx.IntentFlows: true,
}

// TestRecommend_CronbachAlphaReachesReliability: a "cronbach's alpha"
// question resolves through the KnownAs synonym tier to MAT_RELIABILITY,
// whose intent is measure_construct, and recommending that intent
// drafts MAT_RELIABILITY (no longer a routes-only intent).
func TestRecommend_CronbachAlphaReachesReliability(t *testing.T) {
	op, ok := descx.BuiltinKnownAs()[descx.FoldAlias("Cronbach's Alpha")]
	if !ok || op != "MAT_RELIABILITY" {
		t.Fatalf("alias \"cronbach's alpha\" resolves to %q (%v), want MAT_RELIABILITY", op, ok)
	}
	p, _ := descx.PurposeOf(op)
	if !slices.Contains(p.Intents, descx.IntentMeasureConstruct) {
		t.Fatalf("MAT_RELIABILITY intents %v lack measure_construct", p.Intents)
	}
	res := all(t, nil, descx.IntentMeasureConstruct)
	found := false
	for _, r := range res.Recommendations {
		found = found || r.Operator == "MAT_RELIABILITY"
	}
	if !found || len(res.RoutesTo) != 0 {
		t.Errorf("measure_construct: recommendations %+v, routes %+v; want a MAT_RELIABILITY draft and no routes", res.Recommendations, res.RoutesTo)
	}
	if rs := Routes(descx.IntentMeasureConstruct); len(rs) != 1 || rs[0].Use != "MAT_RELIABILITY" {
		t.Errorf("measure_construct fallback route = %+v, want MAT_RELIABILITY", rs)
	}
}

func TestRecommend_UnboundEveryIntent(t *testing.T) {
	for _, in := range descx.Intents() {
		t.Run(in.ID, func(t *testing.T) {
			res := all(t, nil, in.ID)
			if res.Bound {
				t.Error("unbound result reports bound")
			}
			if res.Intent != in.ID || !reflect.DeepEqual(res.Shapes, in.Shapes) {
				t.Errorf("intent/shapes not echoed: %q %+v", res.Intent, res.Shapes)
			}
			if res.Recommendations == nil {
				t.Fatal("recommendations is nil; want an empty array at least")
			}
			if noDraftIntents[in.ID] {
				if len(res.Recommendations) != 0 {
					t.Errorf("got %d recommendations, want none", len(res.Recommendations))
				}
				if len(res.RoutesTo) == 0 {
					t.Error("no routes_to for an intent with no drafts")
				}
				return
			}
			if len(res.Recommendations) == 0 {
				t.Fatal("analytic intent with serving operators produced no recommendation")
			}
			if len(res.RoutesTo) != 0 {
				t.Errorf("routes_to set beside recommendations: %+v", res.RoutesTo)
			}
			for _, r := range res.Recommendations {
				if r.Bound || len(r.Request) == 0 || r.Why == "" || r.Category == "" {
					t.Errorf("%s: incomplete unbound draft %+v", r.Operator, r)
				}
				if !slices.Contains(r.Placeholders, "cohort") || !strings.Contains(string(r.Request), "<cohort>") {
					t.Errorf("%s: cohort placeholder missing", r.Operator)
				}
				for _, ph := range r.Placeholders {
					if !strings.Contains(string(r.Request), "\"<"+ph+">\"") {
						t.Errorf("%s: placeholder %q not in request %s", r.Operator, ph, r.Request)
					}
				}
			}
		})
	}
}

// TestRecommend_EveryServingOperatorDrafted: every operator with a
// draftable category that serves an analytic intent gets a skeleton —
// a gap in the mapping tables cannot drop one silently.
func TestRecommend_EveryServingOperatorDrafted(t *testing.T) {
	cat := catalog(descx.BuildManifestForInstance(nil))
	g := descx.BaseOntology()
	for _, in := range descx.Intents() {
		if !in.Analytic {
			continue
		}
		got := map[string]bool{}
		for _, r := range all(t, nil, in.ID).Recommendations {
			got[r.Operator] = true
		}
		for _, name := range g.OperatorsServing(in.ID) {
			if _, draftable := cat[name]; draftable && !got[name] {
				t.Errorf("%s: operator %s serves the intent but has no draft", in.ID, name)
			}
		}
	}
}

// TestRecommend_SkeletonKeysMatchWire walks every skeleton of every
// intent-serving operator against the wire types: each object key must
// be a JSON tag of the Go struct at that position (types.Request at the
// root), and each `params` key a param the operator declares.
func TestRecommend_SkeletonKeysMatchWire(t *testing.T) {
	m := descx.BuildManifestForInstance(nil)
	declared := declaredParams(m)
	n := 0
	for _, in := range descx.Intents() {
		for _, r := range all(t, nil, in.ID).Recommendations {
			n++
			var v any
			if err := json.Unmarshal(r.Request, &v); err != nil {
				t.Fatalf("%s: %v", r.Operator, err)
			}
			for _, p := range wireKeyProblems(v, reflect.TypeOf(types.Request{}), "", declared[r.Operator]) {
				t.Errorf("%s/%s: %s", in.ID, r.Operator, p)
			}
		}
	}
	if n == 0 {
		t.Fatal("no skeleton walked")
	}
}

// TestWireKeyWalker_Falsifiers: the walker the wire test leans on
// rejects an unknown slot key and an undeclared params key.
func TestWireKeyWalker_Falsifiers(t *testing.T) {
	root := reflect.TypeOf(types.Request{})
	var bad any
	_ = json.Unmarshal([]byte(`{"tests":[{"type":"TEST_T","splitBy":"<x>"}]}`), &bad)
	if len(wireKeyProblems(bad, root, "", nil)) == 0 {
		t.Error("a Go-spelled key (splitBy) passed the wire walk")
	}
	_ = json.Unmarshal([]byte(`{"aggregations":[{"type":"AGG_PERCENTILE","field":"<f>","params":{"pct":"<p>"}}]}`), &bad)
	if len(wireKeyProblems(bad, root, "", map[string]bool{"percentile": true})) == 0 {
		t.Error("an undeclared params key passed the wire walk")
	}
}

func declaredParams(m *descriptor.Manifest) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	add := func(name string, ps []descriptor.Param) {
		set := map[string]bool{}
		for _, p := range ps {
			set[p.Name] = true
		}
		out[name] = set
	}
	c := m.Components
	for _, ops := range [][]descriptor.Operator{c.Aggregators, c.Attributes, c.Filterers, c.Groupers, c.Windows, c.Features} {
		for _, o := range ops {
			add(o.Name, o.Params)
		}
	}
	for _, x := range append(append([]descriptor.TestMeta{}, m.Tests...), m.PostTests...) {
		if x.Name == x.Family {
			add(x.Name, x.Params)
		}
	}
	for _, x := range m.Regressions {
		add(x.Name, x.Params)
	}
	for _, x := range m.Matrices {
		add(x.Name, x.Params)
	}
	// Filterer value slots carried in params (FILTER_DATE_RANGES) are
	// read by the operator though the catalog declares no params.
	for name, s := range filtererShape {
		if s.inParam {
			if out[name] == nil {
				out[name] = map[string]bool{}
			}
			out[name][s.key] = true
		}
	}
	return out
}

var rawMessage = reflect.TypeOf(json.RawMessage{})

func wireKeyProblems(v any, t reflect.Type, path string, params map[string]bool) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice && t != rawMessage {
		t = t.Elem()
	}
	var out []string
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			out = append(out, wireKeyProblems(e, t, path+"[]", params)...)
		}
	case map[string]any:
		if t == rawMessage { // a `params` object
			for k := range x {
				if !params[k] {
					out = append(out, path+"."+k+": not a declared param")
				}
			}
			return out
		}
		if t.Kind() != reflect.Struct {
			return append(out, path+": object where the wire holds "+t.String())
		}
		tags := jsonFields(t)
		for k, sub := range x {
			ft, ok := tags[k]
			if !ok {
				out = append(out, path+"."+k+": no such key on "+t.Name())
				continue
			}
			out = append(out, wireKeyProblems(sub, ft, path+"."+k, params)...)
		}
	}
	return out
}

func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}

// TestRequireKeysMatchWire: the Go-name → JSON-key table agrees with
// types.Test's tags, and covers every Requires value the manifest
// declares on a test or post-test.
func TestRequireKeysMatchWire(t *testing.T) {
	tt := reflect.TypeOf(types.Test{})
	for goName, key := range requireKey {
		f, ok := tt.FieldByName(goName)
		if !ok {
			t.Errorf("types.Test has no field %s", goName)
			continue
		}
		if tag, _, _ := strings.Cut(f.Tag.Get("json"), ","); tag != key {
			t.Errorf("requireKey[%s] = %q, wire tag is %q", goName, key, tag)
		}
	}
	m := descx.BuildManifestForInstance(nil)
	for _, x := range append(append([]descriptor.TestMeta{}, m.Tests...), m.PostTests...) {
		for _, r := range x.Requires {
			if _, ok := requireKey[r]; !ok {
				t.Errorf("%s requires %s, which requireKey does not map", x.Name, r)
			}
		}
	}
}

// TestSlotKeysMatchWire: every category's slot key is a types.Request
// JSON tag.
func TestSlotKeysMatchWire(t *testing.T) {
	tags := jsonFields(reflect.TypeOf(types.Request{}))
	for cat, k := range slotKey {
		if _, ok := tags[k]; !ok {
			t.Errorf("slotKey[%s] = %q is not a Request key", cat, k)
		}
	}
	for k := range listKeys {
		found := false
		for _, st := range []any{types.Filterer{}, types.RegressionSpec{}, types.MatrixSpec{}} {
			if ft, ok := jsonFields(reflect.TypeOf(st))[k]; ok {
				found = true
				if ft.Kind() != reflect.Slice {
					t.Errorf("listKeys[%s] is not a list on %T", k, st)
				}
			}
		}
		if !found {
			t.Errorf("listKeys[%s] is no slot entry key", k)
		}
	}
}

func TestRecommend_UnknownIntent(t *testing.T) {
	for _, id := range []string{"", "compare-groups", "nonsense"} {
		_, err := Recommend(nil, descriptor.RecommendRequest{Intent: id})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RECOMMEND_INTENT_UNKNOWN {
			t.Fatalf("intent %q: err = %v, want PULSE_RECOMMEND_INTENT_UNKNOWN", id, err)
		}
		if !reflect.DeepEqual(ce.Details["valid"], descx.IntentIDs()) || ce.Details["intent"] != id {
			t.Errorf("details = %+v", ce.Details)
		}
	}
	md, ok := errors.MetadataFor(errors.PULSE_RECOMMEND_INTENT_UNKNOWN)
	if !ok || len(md.Fixups) == 0 {
		t.Fatal("no fixup metadata")
	}
	// The fixup lists the valid intents: every one of them.
	for _, id := range descx.IntentIDs() {
		if !strings.Contains(md.Fixups[0].Hint, id+",") && !strings.Contains(md.Fixups[0].Hint, id+";") {
			t.Errorf("fixup hint does not list intent %s", id)
		}
	}
}

func TestRecommend_InvalidLevelAndLimit(t *testing.T) {
	for _, req := range []descriptor.RecommendRequest{
		{Intent: descx.IntentDescribe, Level: "expert"},
		{Intent: descx.IntentDescribe, Limit: -1},
	} {
		_, err := Recommend(nil, req)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
			t.Errorf("%+v: err = %v, want SERVICE_VALIDATION", req, err)
		}
	}
}

// TestRecommend_ProseLintAndProvenance: every why, alternative and
// route sentence passes the guidance lint; alternatives and follow-ups
// are exactly the operator's declared ones (never invented).
func TestRecommend_ProseLintAndProvenance(t *testing.T) {
	lint := func(where, s string) {
		for _, h := range descx.LintGuidanceText(s) {
			t.Errorf("%s: lint %s on %q", where, h.Rule, h.Span)
		}
	}
	for _, in := range descx.Intents() {
		res := all(t, nil, in.ID)
		for _, r := range res.RoutesTo {
			lint(in.ID+" route", r.When)
		}
		for _, r := range res.Recommendations {
			lint(r.Operator+" why", r.Why)
			p, _ := descx.PurposeOf(r.Operator)
			if !reflect.DeepEqual(r.Alternatives, nilIfEmpty(p.NotFor)) {
				t.Errorf("%s: alternatives %+v, want not_for %+v", r.Operator, r.Alternatives, p.NotFor)
			}
			if !reflect.DeepEqual(r.FollowUps, nilIfEmpty(p.FollowUps)) {
				t.Errorf("%s: follow_ups %+v, want %+v", r.Operator, r.FollowUps, p.FollowUps)
			}
			for _, a := range append(r.Alternatives, r.FollowUps...) {
				lint(r.Operator+" alternative", a.When)
			}
		}
	}
	for id, rs := range routes {
		for _, r := range rs {
			lint(id+" route", r.When)
		}
	}
}

func nilIfEmpty(a []descriptor.Alternative) []descriptor.Alternative {
	if len(a) == 0 {
		return nil
	}
	return a
}

func TestRecommend_LimitAndRanking(t *testing.T) {
	res, err := Recommend(nil, descriptor.RecommendRequest{Intent: descx.IntentCompareGroups})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Recommendations) != DefaultLimit || !res.Truncated || res.CandidatesConsidered <= DefaultLimit {
		t.Errorf("default cap: n=%d truncated=%v considered=%d", len(res.Recommendations), res.Truncated, res.CandidatesConsidered)
	}
	full := all(t, nil, descx.IntentCompareGroups)
	if full.Truncated || full.CandidatesConsidered != len(full.Recommendations) {
		t.Errorf("uncapped: truncated=%v considered=%d n=%d", full.Truncated, full.CandidatesConsidered, len(full.Recommendations))
	}
	if !reflect.DeepEqual(full.Recommendations[:DefaultLimit], res.Recommendations) {
		t.Error("the cap is not a prefix of the full ranking")
	}
	for i := 1; i < len(full.Recommendations); i++ {
		a, b := full.Recommendations[i-1], full.Recommendations[i]
		la, lb := levelRank(a.Level, ""), levelRank(b.Level, "")
		if la > lb {
			t.Fatalf("%s (%s) ranks after a harder operator", b.Operator, b.Level)
		}
		if la == lb && categoryOrder[a.Category] > categoryOrder[b.Category] {
			t.Fatalf("%s (%s) ranks before %s (%s) at one level", a.Operator, a.Category, b.Operator, b.Category)
		}
	}
	for cat := range slotKey {
		if _, ok := categoryOrder[cat]; !ok {
			t.Errorf("category %s has no rank", cat)
		}
	}
	adv, err := Recommend(nil, descriptor.RecommendRequest{Intent: descx.IntentCompareGroups, Level: descriptor.LevelAdvanced})
	if err != nil {
		t.Fatal(err)
	}
	if adv.Recommendations[0].Level != descriptor.LevelAdvanced {
		t.Errorf("level=advanced ranks %s (%s) first", adv.Recommendations[0].Operator, adv.Recommendations[0].Level)
	}
}

// profiled builds a feature-profiled snapshot hiding hide plus every
// feature left without a satisfied dependency.
func profiled(hide ...string) *descx.InstanceSnapshot {
	gone := map[string]bool{}
	for _, h := range hide {
		gone[h] = true
	}
	for changed := true; changed; {
		changed = false
		for _, f := range descx.Features() {
			if gone[f.Name] {
				continue
			}
			for _, group := range f.DependsOn {
				if !slices.ContainsFunc(group, func(n string) bool { return !gone[n] }) {
					gone[f.Name], changed = true, true
					break
				}
			}
		}
	}
	var set descx.FeatureSet
	for _, n := range descx.FeatureNames() {
		if gone[n] {
			set.Hidden = append(set.Hidden, n)
		} else {
			set.Enabled = append(set.Enabled, n)
		}
	}
	return descx.NewInstanceSnapshot(nil, set)
}

func TestRecommend_ProfileHidesOperators(t *testing.T) {
	hidden := []string{"TEST_TUKEY_HSD", "TEST_PAIRED_T", "TEST_WELCH", "AGG_MEDIAN", "REG_GLM", "capability:lookup", "capability:import"}
	inst := profiled(hidden...)
	base := map[string]descriptor.Recommendation{}
	for _, in := range descx.Intents() {
		for _, r := range all(t, nil, in.ID).Recommendations {
			base[r.Operator] = r
		}
	}
	for _, in := range descx.Intents() {
		res := all(t, inst, in.ID)
		for _, r := range res.Recommendations {
			names := []string{r.Operator}
			for _, a := range append(r.Alternatives, r.FollowUps...) {
				names = append(names, a.Use)
			}
			for _, n := range names {
				if inst.Hidden(n) {
					t.Errorf("%s: %s names hidden %s", in.ID, r.Operator, n)
				}
			}
		}
		for _, r := range res.RoutesTo {
			if inst.Hidden(r.Use) {
				t.Errorf("%s: route to hidden %s", in.ID, r.Use)
			}
		}
	}
	// The prune bit: the hidden targets were there before.
	if !slices.ContainsFunc(base["TEST_ANOVA_F"].FollowUps, func(a descriptor.Alternative) bool { return a.Use == "TEST_TUKEY_HSD" }) {
		t.Fatal("fixture drift: TEST_ANOVA_F no longer follows up with TEST_TUKEY_HSD")
	}
	res := all(t, inst, descx.IntentCompareGroups)
	for _, r := range res.Recommendations {
		if r.Operator == "TEST_ANOVA_F" && len(r.FollowUps) == 0 {
			t.Error("TEST_ANOVA_F lost every follow-up; only the hidden one should go")
		}
	}
	lk := all(t, inst, descx.IntentLookup)
	if len(lk.RoutesTo) != 0 {
		t.Errorf("lookup routes on a lookup-hidden instance: %+v", lk.RoutesTo)
	}
	if got := all(t, nil, descx.IntentLookup); len(got.RoutesTo) != 1 || got.RoutesTo[0].Use != "capability:lookup" {
		t.Errorf("lookup routes on the default instance: %+v", got.RoutesTo)
	}
}

// TestRecommend_TestTTwoSampleForGroups: TEST_T requires only field
// (one-sample); a group comparison drafts the two-sample form.
func TestRecommend_TestTTwoSampleForGroups(t *testing.T) {
	find := func(intent string) string {
		for _, r := range all(t, nil, intent).Recommendations {
			if r.Operator == "TEST_T" {
				return string(r.Request)
			}
		}
		t.Fatalf("%s: no TEST_T draft", intent)
		return ""
	}
	if got := find(descx.IntentCompareGroups); !strings.Contains(got, `"split_by":"<split_by>"`) {
		t.Errorf("compare_groups TEST_T draft lacks split_by: %s", got)
	}
	if got := find(descx.IntentBenchmark); strings.Contains(got, "split_by") {
		t.Errorf("benchmark TEST_T draft carries split_by: %s", got)
	}
}
