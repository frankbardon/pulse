package guide

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// batteryFixture is a cohort with three numeric items an item-battery
// producer binds, one of them named after a materialised column so the
// label must step aside.
func batteryFixture(t testing.TB) *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "q1", Type: encoding.FieldTypeF64, Description: "First rating item of the battery"},
		{Name: "q2", Type: encoding.FieldTypeF64, Description: "Second rating item of the battery"},
		{Name: "pc_score", Type: encoding.FieldTypeF64, Description: "Third rating item of the battery"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Region the respondent lives in", Dictionary: dict(t, "n", "s")},
	}}
}

// recsOf returns every recommendation of intent keyed by operator (the
// first draft of each).
func recsOf(res *descriptor.RecommendResult) map[string]descriptor.Recommendation {
	out := map[string]descriptor.Recommendation{}
	for _, r := range res.Recommendations {
		if _, ok := out[r.Operator]; !ok {
			out[r.Operator] = r
		}
	}
	return out
}

// stepLabel returns the label of req's one derived entry when it is
// op's — an attribute or a feature — else "".
func stepLabel(req types.Request, op string) string {
	switch {
	case len(req.Attributes) == 1 && len(req.Features) == 0 && req.Attributes[0].Type == types.AttributeType(op):
		return req.Attributes[0].Label
	case len(req.Features) == 1 && len(req.Attributes) == 0 && req.Features[0].Type == types.FeatureType(op):
		return req.Features[0].Label
	}
	return ""
}

// TestRecommend_MaterializeStepsTrigger: each materializeSteps producer
// drafted (unbound) carries a pulse_materialize step whose request is
// the draft itself with its attribute or feature labelled, then a pulse_process
// REG_OLS request on the "<out>" cohort reading that label; every step
// placeholder appears in its request and every why is lint-clean.
func TestRecommend_MaterializeStepsTrigger(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, in := range descx.Intents() {
		for _, r := range all(t, nil, in.ID).Recommendations {
			trig, ok := materializeSteps[r.Operator]
			if !ok {
				continue
			}
			seen[r.Operator] = true
			if len(r.Steps) != 2 {
				t.Fatalf("%s: %d steps, want 2", r.Operator, len(r.Steps))
			}
			mat, model := r.Steps[0], r.Steps[1]
			if mat.Tool != "pulse_materialize" || model.Tool != "pulse_process" {
				t.Errorf("%s: tools %s, %s", r.Operator, mat.Tool, model.Tool)
			}
			var body struct {
				Request types.Request `json:"request"`
				Out     string        `json:"out"`
			}
			if err := json.Unmarshal(mat.Request, &body); err != nil {
				t.Fatalf("%s: %v in %s", r.Operator, err, mat.Request)
			}
			if body.Out != "<out>" || stepLabel(body.Request, r.Operator) != trig.column {
				t.Errorf("%s: materialize step %s", r.Operator, mat.Request)
			}
			var req types.Request
			if err := json.Unmarshal(model.Request, &req); err != nil {
				t.Fatalf("%s: %v in %s", r.Operator, err, model.Request)
			}
			if req.Cohort == nil || req.Cohort.Filename != "<out>" || len(req.Regressions) != 1 || string(req.Regressions[0].Type) != trig.model {
				t.Fatalf("%s: model step %s", r.Operator, model.Request)
			}
			reg := req.Regressions[0]
			reads := reg.Target == trig.column
			if trig.role == "predictors" {
				reads = slices.Equal(reg.Predictors, []string{trig.column})
			}
			if !reads {
				t.Errorf("%s: model step does not read %s as %s: %s", r.Operator, trig.column, trig.role, model.Request)
			}
			for _, s := range r.Steps {
				if !slices.Contains(s.Placeholders, "out") {
					t.Errorf("%s: step %s lacks the out placeholder", r.Operator, s.Tool)
				}
				for _, ph := range s.Placeholders {
					if !strings.Contains(string(s.Request), "\"<"+ph+">\"") {
						t.Errorf("%s: placeholder %q not in step %s", r.Operator, ph, s.Request)
					}
				}
				for _, h := range descx.LintGuidanceText(s.Why) {
					t.Errorf("%s step why: lint %s on %q", r.Operator, h.Rule, h.Span)
				}
			}
		}
	}
	for op := range materializeSteps {
		if !seen[op] {
			t.Errorf("trigger %s is drafted by no intent", op)
		}
	}
}

// TestRecommend_MaterializeStepsAbsentElsewhere: a draft whose operator
// is not a producer carries no steps, and with the trigger table
// emptied every recommendation — triggers included, steps aside — is
// byte-identical: the field is purely additive.
func TestRecommend_MaterializeStepsAbsentElsewhere(t *testing.T) {
	// serial: swaps the package-level guide.materializeSteps test hook

	wire := func(inst *descx.InstanceSnapshot) map[string][]byte {
		out := map[string][]byte{}
		for _, in := range descx.Intents() {
			for name, res := range map[string]*descriptor.RecommendResult{
				"unbound": all(t, inst, in.ID),
				"bound":   boundAll(t, inst, in.ID),
			} {
				for i, r := range res.Recommendations {
					if _, trig := materializeSteps[r.Operator]; !trig && r.Steps != nil {
						t.Errorf("%s %s: non-producer %s has steps", in.ID, name, r.Operator)
					}
					r.Steps = nil
					raw, err := json.Marshal(r)
					if err != nil {
						t.Fatal(err)
					}
					out[in.ID+"/"+name+"/"+r.Operator+"/"+string(rune('a'+i))] = raw
				}
			}
		}
		return out
	}
	with := wire(nil)
	saved := materializeSteps
	materializeSteps = nil
	defer func() { materializeSteps = saved }()
	without := wire(nil)
	if len(with) != len(without) {
		t.Fatalf("%d recommendations with steps, %d without", len(with), len(without))
	}
	for k, v := range without {
		if !bytes.Equal(v, with[k]) {
			t.Errorf("%s moved:\n with    %s\n without %s", k, with[k], v)
		}
		if bytes.Contains(v, []byte(`"steps"`)) {
			t.Errorf("%s: steps key on the wire without a trigger", k)
		}
	}
}

// TestRecommend_MaterializeStepsHidden: a hidden step tool or model
// operator drops the whole plan; the draft stays.
func TestRecommend_MaterializeStepsHidden(t *testing.T) {
	t.Parallel()

	for _, hide := range []string{"capability:materialize", "capability:process", "REG_OLS"} {
		inst := profiled(hide)
		recs := recsOf(all(t, inst, descx.IntentMeasureConstruct))
		r, ok := recs["ATTR_PC_SCORE"]
		if !ok {
			t.Fatalf("hide %s: ATTR_PC_SCORE no longer drafted", hide)
		}
		if r.Steps != nil {
			t.Errorf("hide %s: steps %+v survive", hide, r.Steps)
		}
		f, ok := recsOf(all(t, inst, descx.IntentDistributionShape))["FEAT_LOG"]
		if !ok {
			t.Fatalf("hide %s: FEAT_LOG no longer drafted", hide)
		}
		if f.Steps != nil {
			t.Errorf("hide %s: FEAT_LOG steps %+v survive", hide, f.Steps)
		}
	}
	if r := recsOf(all(t, profiled("TEST_WELCH"), descx.IntentMeasureConstruct))["ATTR_PC_SCORE"]; len(r.Steps) != 2 {
		t.Errorf("an unrelated hidden operator dropped the steps: %+v", r.Steps)
	}
}

// TestRecommendBound_MaterializeStepValidates: bound, the materialize
// draft is the bound producer draft labelled with a column name the
// cohort does not already hold, and it passes materialize's slot gate
// and column naming over the schema; a producer whose labelled draft
// materialize refuses gets no steps.
func TestRecommendBound_MaterializeStepValidates(t *testing.T) {
	t.Parallel()

	s := batteryFixture(t)
	b := bound(t, nil, s, nil)
	r := draftsOf(t, b, descx.IntentMeasureConstruct, "ATTR_PC_SCORE")[0]
	if len(r.Steps) != 2 {
		t.Fatalf("bound ATTR_PC_SCORE steps = %+v", r.Steps)
	}
	var body struct {
		Request types.Request `json:"request"`
	}
	if err := json.Unmarshal(r.Steps[0].Request, &body); err != nil {
		t.Fatal(err)
	}
	if got := body.Request.Attributes[0].Label; got != "pc_score_2" {
		t.Errorf("label %q, want pc_score_2 (pc_score is a source field)", got)
	}
	if body.Request.Cohort == nil || body.Request.Cohort.Filename != b.Cohort.Filename {
		t.Errorf("materialize request cohort %+v, want the bound cohort", body.Request.Cohort)
	}
	if !strings.Contains(string(r.Steps[1].Request), `"pc_score_2"`) {
		t.Errorf("model step does not read pc_score_2: %s", r.Steps[1].Request)
	}
	inner, ok := labelled(r.Request, "pc_score_2")
	if !ok || !materializes(nil, inner, &b, "pc_score_2") {
		t.Error("the bound materialize draft fails materialize validation")
	}
	// The gate bites: a draft carrying a slot materialize refuses, or
	// whose column is not the label, is not materialised.
	refused, _ := json.Marshal(map[string]any{
		"cohort":       b.Cohort,
		"attributes":   []any{map[string]any{"type": "ATTR_PC_SCORE", "fields": []string{"q1", "q2"}, "label": "x"}},
		"aggregations": []any{map[string]any{"type": "AGG_COUNT", "field": "q1"}},
	})
	if materializes(nil, refused, &b, "x") {
		t.Error("a request with an aggregations slot passes the materialize gate")
	}
	if materializes(nil, inner, &b, "other") {
		t.Error("a column the draft does not write passes")
	}
	// A feature producer: its label steps aside too, and the labelled
	// feature draft passes the same validation.
	fs := &encoding.Schema{Fields: []encoding.Field{
		{Name: "income", Type: encoding.FieldTypeF64, Description: "Yearly income of the household"},
		{Name: "log_value", Type: encoding.FieldTypeF64, Description: "Spending of the household last year"},
	}}
	fb := bound(t, nil, fs, nil)
	fr := draftsOf(t, fb, descx.IntentDistributionShape, "FEAT_LOG")[0]
	if len(fr.Steps) != 2 {
		t.Fatalf("bound FEAT_LOG steps = %+v", fr.Steps)
	}
	if err := json.Unmarshal(fr.Steps[0].Request, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Request.Features) != 1 || body.Request.Features[0].Label != "log_value_2" {
		t.Errorf("feature materialize request %s, want label log_value_2 (log_value is a source field)", fr.Steps[0].Request)
	}
	if !strings.Contains(string(fr.Steps[1].Request), `"predictors":["log_value_2"]`) {
		t.Errorf("model step does not read log_value_2: %s", fr.Steps[1].Request)
	}
	finner, ok := labelled(fr.Request, "log_value_2")
	if !ok || !materializes(nil, finner, &fb, "log_value_2") {
		t.Error("the bound feature materialize draft fails materialize validation")
	}
	if materializes(nil, fr.Request, &fb, "log_value_2") {
		t.Error("an unlabelled feature draft passes: its column is LOG_<field>, not the label")
	}
}

// TestMaterializeSteps_FeatureTriggersDecided: a feature is a trigger
// exactly when its capability declaration emits ONE float column (a
// measurement a model can read as a predictor — not a bin number, a
// split code, calendar parts or a column set whose size the caller
// still picks) and some intent drafts it. Each such row reads the
// column as a predictor of REG_OLS. A new FEAT_* operator lands on one
// side of the rule without a table edit going unnoticed.
func TestMaterializeSteps_FeatureTriggersDecided(t *testing.T) {
	t.Parallel()

	drafted := map[string]bool{}
	for _, in := range descx.Intents() {
		for _, r := range all(t, nil, in.ID).Recommendations {
			drafted[r.Operator] = true
		}
	}
	caps := map[string]descriptor.Operator{}
	for _, op := range descx.BuildManifest().Components.Features {
		caps[op.Name] = op
	}
	for _, ft := range types.AllFeatureTypes() {
		name := string(ft)
		op, ok := caps[name]
		if !ok {
			t.Fatalf("%s: no capability declaration", name)
		}
		oneFloat := (op.EmitsType == "f64" || op.EmitsType == "f32") && strings.HasPrefix(op.EmitsTypeNote, "single column")
		trig, isTrig := materializeSteps[name]
		if want := oneFloat && drafted[name]; isTrig != want {
			t.Errorf("%s: trigger %v, want %v (one float column %v, drafted %v)", name, isTrig, want, oneFloat, drafted[name])
		}
		if isTrig && (trig.model != "REG_OLS" || trig.role != "predictors") {
			t.Errorf("%s: trigger %+v, want a REG_OLS predictor", name, trig)
		}
	}
}
