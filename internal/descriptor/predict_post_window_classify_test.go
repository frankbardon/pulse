package descriptor

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/types"
)

// predict_post_window_classify_test.go: ClassifyPostWindow (the
// cohort-less classifier behind Pulse.ClassifyPostWindow) is exactly
// the PredictResult.PostWindow Predict reports over a cohort with the
// same schema — through every step before the walk (hidden slot, item
// for_each, loaded models, the joined schema, smart defaults) — and,
// with no schema, the documented schema-free degradation.

// classifyModel is an inline OLS fit over a window term: an
// ATTR_REG_PREDICT bound to it runs after the windows only once the
// request's `models` entry is loaded.
const classifyModel = `{"model_format_version": 1, "type": "REG_OLS", "target": "m",
	"predictors": ["lag_n"], "terms": ["lag_n"],
	"coefficients": {"(intercept)": 0.5, "lag_n": 2}, "n_obs": 40}`

// classifyCorpus is postWindowCorpus plus the shapes whose plan turns
// on a step before the walk.
func classifyCorpus() map[string]*types.Request {
	out := postWindowCorpus()
	out["loaded model"] = &types.Request{
		Models:  []types.ModelRef{{Name: "lm", Inline: json.RawMessage(classifyModel)}},
		Windows: []*types.Window{lagWindow("lag_n")},
		Attributes: []*types.Attribute{
			postFormula("pre", "n + 1"),
			{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"lm"}`)},
		},
	}
	// The windows fan out per field: the formula reads lag_m, a window
	// label only once the item for_each has expanded.
	out["item for_each"] = &types.Request{
		Windows: []*types.Window{{Type: types.WIN_LAG, Field: "{{f}}", Label: "lag_{{f}}", OrderBy: []types.OrderKey{{Field: "m"}},
			ForEach: &types.ForEach{Axes: []types.SweepAxis{{Name: "f", Values: []any{"n", "m"}}}}}},
		Attributes: []*types.Attribute{postFormula("pre", "n + 1"), postFormula("sat", "lag_m * 2"), postFormula("after", "sat + 1")},
	}
	out["for_each refusal"] = &types.Request{Windows: []*types.Window{lagWindow("lag_n")}, Attributes: []*types.Attribute{
		{Type: types.ATTR_FORMULA, Label: "pre", Expression: "n * 2", ForEach: &types.ForEach{}},
		postFormula("sat", "lag_n * 2"),
	}}
	// The right cohort's n joins as r_n: a window labelled r_n is
	// shadowed by the joined field, so the formula reading it stays
	// before the windows — judged over the joined schema only.
	out["joined schema shadow"] = &types.Request{
		Joins:      []*types.JoinSpec{{Right: "right.pulse", On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: "r_"}},
		Windows:    []*types.Window{lagWindow("r_n"), lagWindow("lag_n")},
		Attributes: []*types.Attribute{postFormula("f", "r_n * 2"), postFormula("g", "lag_n * 2")},
	}
	out["defaulted aggregation"] = &types.Request{Windows: []*types.Window{lagWindow("lag_n")},
		Attributes:   []*types.Attribute{postFormula("sat", "lag_n * 2")},
		Aggregations: []*types.Aggregation{{Field: "n", Label: "s"}, {Type: types.AGG_SUM, Field: "sat", Label: "t"}}}
	return out
}

// classifyOpts are the options both sides share: the right cohort for
// the join, no model loader beyond inline entries.
func classifyOpts(t *testing.T) *PredictOptions {
	t.Helper()
	right := buildTestPulseFile(t, fieldRefSchema())
	return &PredictOptions{SchemaLoader: composeSchemaLoader(t, map[string][]byte{"right.pulse": right})}
}

// predictPostWindow is Predict's post_window for req over cohort.
func predictPostWindow(cohort []byte, req *types.Request, opts *PredictOptions) *descriptor.PostWindowPlan {
	env := Predict(bytes.NewReader(cohort), req, opts)
	return env.Data.(*descriptor.PredictResult).PostWindow
}

// TestClassifyPostWindow_MatchesPredict: over the post-window corpus,
// the pre-walk shapes and every example request with a probe window,
// the cohort-less plan is Predict's, refused requests included.
func TestClassifyPostWindow_MatchesPredict(t *testing.T) {
	t.Parallel()

	schema := fieldRefSchema()
	cohort := buildTestPulseFile(t, schema)
	opts := classifyOpts(t)
	check := func(name string, req *types.Request) {
		t.Helper()
		want := predictPostWindow(cohort, req, opts)
		got := ClassifyPostWindow(req, schema, opts)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: classified %+v, predict %+v", name, got, want)
		}
	}
	for name, req := range classifyCorpus() {
		check(name, req)
	}
	probe := &types.Window{Type: types.WIN_ROW_NUMBER, Label: "post_window_probe", OrderBy: []types.OrderKey{{Field: "m"}}}
	var n int
	for _, ex := range examples.All() {
		var body any
		if err := json.Unmarshal(ex.Body, &body); err != nil {
			t.Fatalf("%s: %v", ex.Name, err)
		}
		for _, raw := range exampleRequestObjects(t, body) {
			var req types.Request
			if err := json.Unmarshal(raw, &req); err != nil {
				continue
			}
			check(ex.Name, &req)
			withProbe := req
			withProbe.Windows = append(append([]*types.Window(nil), req.Windows...), probe)
			check(ex.Name+" (probe)", &withProbe)
			n++
		}
	}
	if n == 0 {
		t.Fatal("the example corpus yielded no request")
	}
}

// TestClassifyPostWindow_PreWalkSteps pins what each step before the
// walk contributes, so the parity above cannot pass on a plan that
// ignores one: the loaded model moves its score, the for_each expands
// (indices address the expanded request; a refused one the request as
// written), the joined field shadows its window, a defaulted
// aggregation over a post-window label still reads it windows-first.
func TestClassifyPostWindow_PreWalkSteps(t *testing.T) {
	t.Parallel()

	schema := fieldRefSchema()
	opts := classifyOpts(t)
	corpus := classifyCorpus()
	lag := []string{"lag_n"}
	cases := map[string]*descriptor.PostWindowPlan{
		"loaded model": {WindowLabels: lag, Attributes: []descriptor.PostWindowAttribute{
			{Index: 1, Label: "p", DependsOn: "lag_n", WindowReads: lag},
		}},
		"item for_each": {WindowLabels: []string{"lag_n", "lag_m"}, Attributes: []descriptor.PostWindowAttribute{
			{Index: 1, Label: "sat", DependsOn: "lag_m", WindowReads: []string{"lag_m"}},
			{Index: 2, Label: "after", DependsOn: "sat", WindowReads: []string{}},
		}},
		"for_each refusal": {WindowLabels: lag, Attributes: []descriptor.PostWindowAttribute{
			{Index: 1, Label: "sat", DependsOn: "lag_n", WindowReads: lag},
		}},
		"joined schema shadow": {WindowLabels: lag, Attributes: []descriptor.PostWindowAttribute{
			{Index: 1, Label: "g", DependsOn: "lag_n", WindowReads: lag},
		}},
		"defaulted aggregation": {WindowsFirst: true, WindowLabels: lag, Attributes: []descriptor.PostWindowAttribute{
			{Index: 0, Label: "sat", DependsOn: "lag_n", WindowReads: lag},
		}},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyPostWindow(corpus[name], schema, opts)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("classified %+v, want %+v", got, want)
			}
		})
	}
}

// TestClassifyPostWindow_HiddenSlot: a slot the instance hides stops
// Predict before the walk, so the plan is the schema-free one over the
// request as written — the shadowed window label n then counts, as it
// does in Predict's block — not the walk's plan over the schema.
func TestClassifyPostWindow_HiddenSlot(t *testing.T) {
	t.Parallel()

	var enabled []string
	for _, f := range FeatureNames() {
		if f != featJoins {
			enabled = append(enabled, f)
		}
	}
	opts := &PredictOptions{Instance: scopedOnly(enabled...)}
	schema := fieldRefSchema()
	req := postWindowCorpus()["shadowed by a field"]
	req.Joins = []*types.JoinSpec{{Right: "r.pulse", On: []types.OnPair{{LeftField: "n", RightField: "n"}}}}
	want := predictPostWindow(buildTestPulseFile(t, schema), req, opts)
	if want == nil || len(want.Attributes) != 1 || want.Attributes[0].DependsOn != "n" {
		t.Fatalf("predict over a hidden slot reports %+v, want the schema-free plan moving f", want)
	}
	if got := ClassifyPostWindow(req, schema, opts); !reflect.DeepEqual(got, want) {
		t.Fatalf("classified %+v, predict %+v", got, want)
	}
}

// TestClassifyPostWindow_NilSchema: with no schema the plan is the
// schema-free one Predict falls back to over an unreadable cohort —
// equal wherever no window label shadows a field, and misjudging
// exactly the shadowed label (the documented degradation). No window,
// or no request: nil.
func TestClassifyPostWindow_NilSchema(t *testing.T) {
	t.Parallel()

	opts := classifyOpts(t)
	unreadable := []byte("not a pulse file")
	req := earlyStopRequest()
	if got, want := ClassifyPostWindow(req, nil, opts), predictPostWindow(unreadable, req, opts); !reflect.DeepEqual(got, want) || want == nil {
		t.Fatalf("schema-free plan %+v, predict over an unreadable cohort %+v", got, want)
	}

	shadow := postWindowCorpus()["shadowed by a field"]
	withSchema := ClassifyPostWindow(shadow, fieldRefSchema(), opts)
	if len(withSchema.Attributes) != 0 || !reflect.DeepEqual(withSchema.WindowLabels, []string{"lag_n"}) {
		t.Fatalf("over the schema the field n wins: %+v", withSchema)
	}
	free := ClassifyPostWindow(shadow, nil, opts)
	want := &descriptor.PostWindowPlan{WindowLabels: []string{"n", "lag_n"}, Attributes: []descriptor.PostWindowAttribute{
		{Index: 0, Label: "f", DependsOn: "n", WindowReads: []string{"n"}},
	}}
	if !reflect.DeepEqual(free, want) {
		t.Fatalf("schema-free shadow plan %+v, want %+v", free, want)
	}

	if got := ClassifyPostWindow(postWindowCorpus()["no window"], fieldRefSchema(), opts); got != nil {
		t.Fatalf("no window: %+v", got)
	}
	if got := ClassifyPostWindow(nil, (*encoding.Schema)(nil), nil); got != nil {
		t.Fatalf("nil request: %+v", got)
	}
}
