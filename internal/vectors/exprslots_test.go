package vectors

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/exprslots"
	"github.com/frankbardon/pulse/types"
)

// indexRe strips the array indices off an ExprSlot path.
var indexRe = regexp.MustCompile(`\[[0-9]+\]`)

// exprSlotsOutOfScope lists every compile-bearing exprslots slot (one
// with Sites) ExprSlots deliberately does not walk, with the reason.
// A slot with Sites ExprSlots neither walks nor lists here fails
// TestTemplateExprSplice_EngineExprSlotsMatch — so a new compiled
// expression slot is either read by the reference / kind / length /
// static checks and the sweep-fit scan, or named here on purpose.
var exprSlotsOutOfScope = map[string]string{
	// A WIN_FORMULA expression reads output-row columns only: it binds
	// no request vector, refuses ref() at compile (internal/winformula)
	// and declares no coef(), so no reference / kind / length check or
	// sweep-fit scan has anything to read in it.
	exprslots.SlotWindowFormula: "window formula: output-row columns only, no vector, ref() or coef()",
}

// TestTemplateExprSplice_EngineExprSlotsMatch, both directions: every
// request expression the engine's reference / kind / length checks read
// (ExprSlots) — a filterer, a formula, a fitted attribute's
// params.fit_on filterers, a comparison centroid's filter, a feature's
// params.fit_on row expression, a regression holdout — sits at a key path the shared
// expression-slot list (internal/exprslots) matches, so the template
// renderer refuses a `{{token}}` splice into each of them; and every
// exprslots slot the engine compiles (Sites non-empty) is walked by
// ExprSlots or listed in exprSlotsOutOfScope.
func TestTemplateExprSplice_EngineExprSlotsMatch(t *testing.T) {
	t.Parallel()

	fitOn := json.RawMessage(`{"fit_on":[{"type":"FILTER_EXPRESSION","expression":"b1 > 0"}]}`)
	req := &types.Request{
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "a1 > 0"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "f", Expression: "a1 + 1"},
			{Type: types.ATTR_MAHALANOBIS, Fields: []string{"b1", "b2"}, Params: fitOn},
			{Type: types.ATTR_VEC_DISTANCE, Fields: []string{"b1", "b2"}, Params: json.RawMessage(`{"metric":"euclidean","fit_on":[{"type":"FILTER_EXPRESSION","expression":"b2 > 0"}],"reference":{"centroid":{"filter":[{"type":"FILTER_EXPRESSION","expression":"a1 > 0"}]}}}`)},
		},
		Features: []*types.Feature{
			{Type: types.FEAT_TARGET_ENCODE, Field: "g", Params: json.RawMessage(`{"target":"a1","fit_on":"b1 > 0"}`)},
		},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Holdout: "a1 > 0"}},
	}
	slots := ExprSlots(req)
	if len(slots) < 7 {
		t.Fatalf("ExprSlots = %+v, want the filterer, the formula, both fit_on lists, the centroid filter, the feature fit_on and the holdout", slots)
	}
	walked := map[string]bool{}
	for _, s := range slots {
		keys := strings.Split(indexRe.ReplaceAllString(s.Path, ""), ".")
		got, ok := exprslots.Match(keys)
		if !ok || len(got.Sites) == 0 {
			t.Errorf("engine expression %s (%q): exprslots.Match = (%q, %v), want a compiled exprslots slot", s.Path, s.Src, got.Name, ok)
			continue
		}
		walked[got.Name] = true
	}
	for _, s := range exprslots.All() {
		if len(s.Sites) == 0 {
			continue
		}
		reason, out := exprSlotsOutOfScope[s.Name]
		switch {
		case walked[s.Name] && out:
			t.Errorf("exprslots slot %s is walked by ExprSlots yet listed out of scope (%s)", s.Name, reason)
		case !walked[s.Name] && !out:
			t.Errorf("exprslots slot %s (sites %v) is neither walked by ExprSlots nor listed in exprSlotsOutOfScope", s.Name, s.Sites)
		}
	}
	for name := range exprSlotsOutOfScope {
		found := false
		for _, s := range exprslots.All() {
			found = found || (s.Name == name && len(s.Sites) > 0)
		}
		if !found {
			t.Errorf("exprSlotsOutOfScope lists %s, no compiled exprslots slot", name)
		}
	}
}

// TestExprSlots_FeatureFitOn: a FEAT_TARGET_ENCODE params.fit_on is an
// expression slot at features[i].params.fit_on, after the attributes;
// a blank or non-string fit_on, another feature's fit_on key and a nil
// feature are not; mentionsRef sees a ref() in it.
func TestExprSlots_FeatureFitOn(t *testing.T) {
	t.Parallel()

	req := &types.Request{
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "a1 + 1"}},
		Features: []*types.Feature{
			nil,
			{Type: types.FEAT_TRAIN_TEST_SPLIT, Label: "split", Params: json.RawMessage(`{"fit_on":"a1 > 0"}`)},
			{Type: types.FEAT_TARGET_ENCODE, Field: "g", Params: json.RawMessage(`{"target":"a1","fit_on":"split == 0"}`)},
			{Type: types.FEAT_TARGET_ENCODE, Field: "g", Params: json.RawMessage(`{"target":"a1","fit_on":true}`)},
			{Type: types.FEAT_TARGET_ENCODE, Field: "g", Params: json.RawMessage(`{"target":"a1","fit_on":""}`)},
			{Type: types.FEAT_TARGET_ENCODE, Field: "g", Params: json.RawMessage(`{"target":"a1"}`)},
		},
	}
	got := ExprSlots(req)
	want := []ExprSlot{
		{Path: "attributes[0].expression", Src: "a1 + 1"},
		{Path: "features[2].params.fit_on", Src: "split == 0"},
	}
	if len(got) != len(want) {
		t.Fatalf("ExprSlots = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ExprSlots[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	refReq := &types.Request{Features: []*types.Feature{
		{Type: types.FEAT_TARGET_ENCODE, Field: "g", Params: json.RawMessage(`{"target":"a1","fit_on":"dot(v, ref(\"w\", \"k\")) > 0"}`)},
	}}
	if !mentionsRef(refReq) {
		t.Error("mentionsRef missed a ref() in a feature params.fit_on")
	}
	if mentionsRef(req) {
		t.Error("mentionsRef reported a ref() in a request holding none")
	}
}

// TestExprSlots_RegressionHoldout: each regression's holdout predicate
// is an expression slot at regressions[i].holdout, after the features;
// a blank holdout and a nil regression are not; mentionsRef sees a
// ref() in it, and a vector it reads counts as referenced.
func TestExprSlots_RegressionHoldout(t *testing.T) {
	t.Parallel()

	req := &types.Request{
		Vectors:    []types.VectorSpec{{Name: "v", Fields: []string{"a1", "a2"}}, {Name: "u", Fields: []string{"a1", "a2"}}},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "a1 + 1"}},
		Regressions: []*types.RegressionSpec{
			nil,
			{Type: types.REG_OLS, Holdout: ""},
			{Type: types.REG_OLS, Holdout: "vmean(v) > 0"},
		},
	}
	got := ExprSlots(req)
	want := []ExprSlot{
		{Path: "attributes[0].expression", Src: "a1 + 1"},
		{Path: "regressions[2].holdout", Src: "vmean(v) > 0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExprSlots = %+v, want %+v", got, want)
	}
	if un := Unreferenced(req); !reflect.DeepEqual(un, []string{"u"}) {
		t.Errorf("Unreferenced = %v, want [u] (the holdout reads v)", un)
	}
	if !mentionsRef(&types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Holdout: `dot(v, ref("w", "k")) > 0`}}}) {
		t.Error("mentionsRef missed a ref() in a holdout")
	}
}
