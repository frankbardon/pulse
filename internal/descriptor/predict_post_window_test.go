package descriptor

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// predict_post_window_test.go: PredictResult.PostWindow (and a Compose
// slot's ComposeSlotPredict.PostWindow) is the field-reference walk's
// own post-window plan (vectors.PostWindowAttributes), reported on a
// refused request too and absent without a window.

// postWindowCorpus is the walk's post-window shapes: a formula chain, a
// total() formula, a non-row-local slot over a post-window label, a
// bound-model score over a window term, the grouped shape, windows-first
// (an aggregation over the window and over a post-window label), a
// window label shadowed by a schema field and by a vector, windows with
// no attribute, and no window.
func postWindowCorpus() map[string]*types.Request {
	win := []*types.Window{lagWindow("lag_n")}
	return map[string]*types.Request{
		"formula chain": {Windows: win, Attributes: []*types.Attribute{
			postFormula("sat", "hill(lag_n, 3, 2)"), postFormula("pre", "n * 2"), postFormula("sat2", "sat * pre + lag_n"),
		}},
		"total()": {Windows: win, Attributes: []*types.Attribute{postFormula("share", "lag_n / total(lag_n)")}},
		"through a post-window label": {Windows: win, Attributes: []*types.Attribute{
			postFormula("sat", "hill(lag_n, 3, 2)"), {Type: types.ATTR_PERCENTILE, Field: "sat", Label: "pct"},
		}},
		"bound-model score": {Windows: win, Attributes: []*types.Attribute{
			{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)},
			postFormula("c", `coef("m", "lag_n") * lag_n`),
		}},
		"grouped shape": {Windows: win, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Aggregations: []*types.Aggregation{sumField("n", "n_sum")}, Attributes: []*types.Attribute{postFormula("f", "lag_n * 2")}},
		"windows-first": {Windows: win, Attributes: []*types.Attribute{postFormula("pre", "n + 1"), postFormula("sat", "lag_n / (lag_n + 3)")},
			Aggregations: []*types.Aggregation{sumField("sat", "s"), sumField("lag_n", "l")}},
		"shadowed by a field": {Windows: []*types.Window{lagWindow("n"), lagWindow("lag_n")}, Attributes: []*types.Attribute{postFormula("f", "n * 2")}},
		"shadowed by a vector": {Vectors: []types.VectorSpec{{Name: "lag_n", Fields: []string{"n", "m"}}}, Windows: win,
			Attributes: []*types.Attribute{postFormula("f", "vmean(lag_n)")}},
		"no attribute": {Windows: win},
		"no window":    {Attributes: []*types.Attribute{postFormula("f", "n * 2")}},
	}
}

func postWindowCatalog() model.FitCatalog {
	return model.FitCatalog{"m": {Name: "m", Type: types.REG_OLS, Target: "m", Terms: []string{"lag_n"}, Kind: types.FitKindRegression}}
}

// checkPostWindowParity fails unless the walk's plan for req is the
// classifier's, and the reported block is that plan: the moved slots'
// indices, labels and dependencies, WindowsFirst, every window-derived
// label, and per-slot window reads whose first-read union is the
// classifier's WindowReads.
func checkPostWindowParity(t *testing.T, name string, req *types.Request, cat model.Catalog) {
	t.Helper()
	schema := fieldRefSchema()
	_, got := fieldRefWalkPlan(req, schema, nil, nil, nil, cat)
	want := vectors.PostWindowAttributes(req, schema, nil, cat)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: walk plan %+v, classifier %+v", name, got, want)
	}
	block := postWindowDescriptor(req, schema, got)
	if len(req.Windows) == 0 {
		if block != nil {
			t.Fatalf("%s: block %+v without a window", name, block)
		}
		return
	}
	if block == nil {
		t.Fatalf("%s: no block over %d windows", name, len(req.Windows))
	}
	if block.WindowsFirst != want.WindowsFirst {
		t.Errorf("%s: windows_first %v, classifier %v", name, block.WindowsFirst, want.WindowsFirst)
	}
	if !reflect.DeepEqual(block.WindowLabels, append([]string{}, vectors.WindowDerivedLabels(req, schema)...)) {
		t.Errorf("%s: window_labels %v", name, block.WindowLabels)
	}
	var idx []int
	var labels, union []string
	for _, a := range block.Attributes {
		idx = append(idx, a.Index)
		labels = append(labels, a.Label)
		if a.DependsOn != want.DepOf(a.Index) || a.DependsOn == "" {
			t.Errorf("%s: attribute %d depends_on %q, classifier %q", name, a.Index, a.DependsOn, want.DepOf(a.Index))
		}
		for _, r := range a.WindowReads {
			if !slices.Contains(block.WindowLabels, r) {
				t.Errorf("%s: attribute %d reads %q, not a window label", name, a.Index, r)
			}
			if !slices.Contains(union, r) {
				union = append(union, r)
			}
		}
	}
	var wantIdx []int
	for i := range req.Attributes {
		if want.IsPost(i) {
			wantIdx = append(wantIdx, i)
		}
	}
	if !reflect.DeepEqual(idx, wantIdx) || !reflect.DeepEqual(labels, want.Labels) || !reflect.DeepEqual(union, want.WindowReads) {
		t.Errorf("%s: block %+v, classifier post %v labels %v reads %v", name, block.Attributes, wantIdx, want.Labels, want.WindowReads)
	}
}

// TestPredictPostWindow_ParityWithClassifier: over the walk's
// post-window corpus (with a bound model, and without one), and over
// every request of the shipped example corpus with a probe window
// added, the plan predict reports is the internal classifier's.
func TestPredictPostWindow_ParityWithClassifier(t *testing.T) {
	var moved int
	for name, req := range postWindowCorpus() {
		checkPostWindowParity(t, name, req, postWindowCatalog())
		checkPostWindowParity(t, name+" (no catalog)", req, nil)
		if vectors.PostWindowAttributes(req, fieldRefSchema(), nil, postWindowCatalog()).Any() {
			moved++
		}
	}
	if moved < 3 {
		t.Fatalf("only %d corpus requests move an attribute; the parity judged too little", moved)
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
			checkPostWindowParity(t, ex.Name, &req, nil)
			withProbe := req
			withProbe.Windows = append(append([]*types.Window(nil), req.Windows...), probe)
			checkPostWindowParity(t, ex.Name+" (probe)", &withProbe, nil)
			n++
		}
	}
	if n == 0 {
		t.Fatal("the example corpus yielded no request")
	}
}

// exampleRequestObjects is every JSON object in v carrying an
// "attributes" or "windows" key, re-marshalled.
func exampleRequestObjects(t *testing.T, v any) []json.RawMessage {
	var out []json.RawMessage
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			_, a := x["attributes"]
			_, w := x["windows"]
			if a || w {
				raw, err := json.Marshal(x)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, raw)
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(v)
	return out
}

// TestPredictPostWindow_ProcessReportsPlanOnRefusal: Process predict
// reports the plan beside a refusal — the post-window chain and a
// non-row-local slot over a window label (PULSE_ATTRIBUTE_WINDOW_ORDER,
// not listed); a windows-first request reports windows_first with no
// moved attribute; a request without a window carries no post_window
// key.
func TestPredictPostWindow_ProcessReportsPlanOnRefusal(t *testing.T) {
	cohort := buildTestPulseFile(t, fieldRefSchema())
	predict := func(req *types.Request) (*descriptor.PredictResult, *descriptor.Envelope) {
		env := Predict(bytes.NewReader(cohort), req, &PredictOptions{})
		return env.Data.(*descriptor.PredictResult), env
	}
	refused := &types.Request{Windows: []*types.Window{lagWindow("lag_n")}, Attributes: []*types.Attribute{
		postFormula("pre", "n * 2"),
		postFormula("sat", "hill(lag_n, 3, 2)"),
		{Type: types.ATTR_ZSCORE, Field: "lag_n", Label: "z"},
		postFormula("sat2", "sat + 1"),
	}}
	res, env := predict(refused)
	if res.Valid || len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_ATTRIBUTE_WINDOW_ORDER) {
		t.Fatalf("want a PULSE_ATTRIBUTE_WINDOW_ORDER refusal, got valid=%v %+v", res.Valid, env.Errors)
	}
	want := &descriptor.PostWindowPlan{WindowLabels: []string{"lag_n"}, Attributes: []descriptor.PostWindowAttribute{
		{Index: 1, Label: "sat", DependsOn: "lag_n", WindowReads: []string{"lag_n"}},
		{Index: 3, Label: "sat2", DependsOn: "sat", WindowReads: []string{}},
	}}
	if !reflect.DeepEqual(res.PostWindow, want) {
		t.Fatalf("post_window %+v, want %+v", res.PostWindow, want)
	}

	first, _ := predict(&types.Request{Windows: []*types.Window{lagWindow("lag_n")}, Aggregations: []*types.Aggregation{sumField("lag_n", "s")}})
	if first.PostWindow == nil || !first.PostWindow.WindowsFirst || len(first.PostWindow.Attributes) != 0 {
		t.Fatalf("windows-first post_window %+v", first.PostWindow)
	}

	plain, _ := predict(&types.Request{Attributes: []*types.Attribute{postFormula("f", "n * 2")}})
	raw, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	if plain.PostWindow != nil || bytes.Contains(raw, []byte(`"post_window"`)) {
		t.Fatalf("a request with no window carries post_window: %s", raw)
	}
}

// TestPredictPostWindow_ComposeSlotsReadFits: each Compose slot reports
// its own plan, classified over the batch's fits — an ATTR_REG_PREDICT
// whose fit reads a window label moves after the windows — and a slot
// without a window reports none.
func TestPredictPostWindow_ComposeSlotsReadFits(t *testing.T) {
	cohort := buildTestPulseFile(t, fieldRefSchema())
	opts := &PredictOptions{SchemaLoader: composeSchemaLoader(t, map[string][]byte{"c.pulse": cohort})}
	at := &types.Cohort{Filename: "c.pulse"}
	fit := &types.Request{Cohort: at, Windows: []*types.Window{lagWindow("lag_n")},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "fm", Target: "m", Predictors: []string{"lag_n"}}}}
	batch := &types.ComposedRequest{
		Fits: []types.FitSpec{{Name: "fm", Request: fit}},
		Requests: []*types.Request{
			{Cohort: at, Aggregations: []*types.Aggregation{sumField("n", "s")}},
			{Cohort: at, Windows: []*types.Window{lagWindow("lag_n")}, Attributes: []*types.Attribute{
				{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"fm"}`)},
			}},
		},
	}
	env := ValidateComposeWithOptions(batch, opts)
	if len(env.Errors) != 0 {
		t.Fatalf("batch refused: %+v", env.Errors)
	}
	res := env.Data.(*ComposeValidationResult)
	if len(res.Slots) != 2 {
		t.Fatalf("slots %+v", res.Slots)
	}
	if res.Slots[0].PostWindow != nil {
		t.Errorf("slot 0 (no window) post_window %+v", res.Slots[0].PostWindow)
	}
	want := &descriptor.PostWindowPlan{WindowLabels: []string{"lag_n"}, Attributes: []descriptor.PostWindowAttribute{
		{Index: 0, Label: "p", DependsOn: "lag_n", WindowReads: []string{"lag_n"}},
	}}
	if !reflect.DeepEqual(res.Slots[1].PostWindow, want) {
		t.Fatalf("slot 1 post_window %+v, want %+v", res.Slots[1].PostWindow, want)
	}
}

// earlyStopRequest is a draft with a post-window formula (attribute 1
// reads the window label lag_n).
func earlyStopRequest() *types.Request {
	return &types.Request{Windows: []*types.Window{lagWindow("lag_n")}, Attributes: []*types.Attribute{
		postFormula("pre", "n * 2"), postFormula("sat", "lag_n * 2"),
	}}
}

// processEarlyCodes / composeEarlyCodes pin each early-stop case to the
// refusal family it exercises ("" none: a schema-less slot is admitted).
var processEarlyCodes = map[string]string{
	"hidden slot":       string(errors.PULSE_REQUEST_UNKNOWN_FIELD),
	"for_each refusal":  string(errors.PULSE_FOR_EACH_INVALID),
	"unreadable header": string(errors.ENCODING_INVALID),
}

var composeEarlyCodes = map[string]string{
	"for_each":    string(errors.PULSE_FOR_EACH_INVALID),
	"join count":  string(errors.PULSE_JOIN_TOO_MANY),
	"matrix host": string(errors.PULSE_MATRIX_UNSUPPORTED_SOURCE),
	"return":      string(errors.PULSE_RETURN_INVALID),
	"join keys":   string(errors.PULSE_JOIN_FIELD_UNKNOWN),
	"zones":       string(errors.PULSE_TIMEZONE_UNKNOWN),
	"no schema":   "",
}

// TestPredictPostWindow_ProcessEarlyStops: Process predict stopped
// before its field-reference walk still reports the plan, schema-free —
// a hidden slot, an item for_each refusal (indexing the request as
// written) and an unreadable cohort header.
func TestPredictPostWindow_ProcessEarlyStops(t *testing.T) {
	want := &descriptor.PostWindowPlan{WindowLabels: []string{"lag_n"}, Attributes: []descriptor.PostWindowAttribute{
		{Index: 1, Label: "sat", DependsOn: "lag_n", WindowReads: []string{"lag_n"}},
	}}
	cohort := buildTestPulseFile(t, fieldRefSchema())
	hidden := earlyStopRequest()
	hidden.Joins = []*types.JoinSpec{{Right: "r.pulse", On: []types.OnPair{{LeftField: "n", RightField: "n"}}}}
	forEach := earlyStopRequest()
	forEach.Attributes[0].ForEach = &types.ForEach{}
	// The hidden-slot instance hides the joins capability only.
	var enabled []string
	for _, f := range FeatureNames() {
		if f != featJoins {
			enabled = append(enabled, f)
		}
	}
	cases := map[string]struct {
		data []byte
		req  *types.Request
		opts *PredictOptions
	}{
		"hidden slot":       {cohort, hidden, &PredictOptions{Instance: scopedOnly(enabled...)}},
		"for_each refusal":  {cohort, forEach, &PredictOptions{}},
		"unreadable header": {[]byte("not a pulse file"), earlyStopRequest(), &PredictOptions{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := Predict(bytes.NewReader(c.data), c.req, c.opts)
			if len(env.Errors) == 0 || env.Errors[0].Code != processEarlyCodes[name] {
				t.Fatalf("want the %s refusal (%s), got %+v", name, processEarlyCodes[name], env.Errors)
			}
			res := env.Data.(*descriptor.PredictResult)
			if res.Valid {
				t.Fatal("predicted valid")
			}
			if !reflect.DeepEqual(res.PostWindow, want) {
				t.Fatalf("post_window %+v, want %+v (errors %+v)", res.PostWindow, want, env.Errors)
			}
		})
	}
}

// TestPredictPostWindow_ComposeEarlyStops: a Compose slot refused before
// its field references — item for_each, join count, matrix host,
// `return`, join keys, zones — or with no schema to walk still reports
// its plan, classified over the batch's fits (the ATTR_REG_PREDICT
// moves through the fit's window term).
func TestPredictPostWindow_ComposeEarlyStops(t *testing.T) {
	cohort := buildTestPulseFile(t, fieldRefSchema())
	opts := &PredictOptions{SchemaLoader: composeSchemaLoader(t, map[string][]byte{"c.pulse": cohort})}
	at := &types.Cohort{Filename: "c.pulse"}
	fit := &types.Request{Cohort: at, Windows: []*types.Window{lagWindow("lag_n")},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "fm", Target: "m", Predictors: []string{"lag_n"}}}}
	slot := func(mut func(*types.Request)) *types.Request {
		r := &types.Request{Cohort: at, Windows: []*types.Window{lagWindow("lag_n")}, Attributes: []*types.Attribute{
			{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"fm"}`)},
			postFormula("x", "n * 2"),
		}}
		mut(r)
		return r
	}
	join := func(left string) *types.JoinSpec {
		return &types.JoinSpec{Right: "c.pulse", On: []types.OnPair{{LeftField: left, RightField: "n"}}, As: "r_"}
	}
	cases := map[string]*types.Request{
		"for_each":   slot(func(r *types.Request) { r.Attributes[1].ForEach = &types.ForEach{} }),
		"join count": slot(func(r *types.Request) { r.Joins = []*types.JoinSpec{join("n"), join("n")} }),
		"matrix host": slot(func(r *types.Request) {
			r.Matrices = []types.MatrixSpec{{Type: "MAT_CORRELATION"}}
			r.Joins = []*types.JoinSpec{join("n")}
		}),
		"return":    slot(func(r *types.Request) { r.Return = &types.Return{Preset: "bogus"} }),
		"join keys": slot(func(r *types.Request) { r.Joins = []*types.JoinSpec{join("nope")} }),
		"zones":     slot(func(r *types.Request) { r.TimeZone = "Not/AZone" }),
		"no schema": slot(func(r *types.Request) { r.Cohort = &types.Cohort{Filename: "missing.pulse"} }),
	}
	want := &descriptor.PostWindowPlan{WindowLabels: []string{"lag_n"}, Attributes: []descriptor.PostWindowAttribute{
		{Index: 0, Label: "p", DependsOn: "lag_n", WindowReads: []string{"lag_n"}},
	}}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			env := ValidateComposeWithOptions(&types.ComposedRequest{Fits: []types.FitSpec{{Name: "fm", Request: fit}}, Requests: []*types.Request{s}}, opts)
			if want := composeEarlyCodes[name]; (want == "") != (len(env.Errors) == 0) || (want != "" && env.Errors[0].Code != want) {
				t.Fatalf("want the %s refusal (%q), got %+v", name, want, env.Errors)
			}
			res := env.Data.(*ComposeValidationResult)
			if len(res.Slots) != 1 {
				t.Fatalf("slots %+v (errors %+v)", res.Slots, env.Errors)
			}
			if !reflect.DeepEqual(res.Slots[0].PostWindow, want) {
				t.Fatalf("post_window %+v, want %+v (errors %+v)", res.Slots[0].PostWindow, want, env.Errors)
			}
		})
	}
}
