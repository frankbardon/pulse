package pulse

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// predict_post_window_test.go: every predict entry point reports the
// post-window plan (PredictResult.PostWindow, a Compose slot's
// ComposeSlotPredict.PostWindow) — attributes addressed by index, also
// on a refused request. Predict reads only header and schema, so the
// cohort is tiny.

const postWindowCohort = "post_window.pulse"

func postWindowPulse(t *testing.T) (*Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	var rows [][]string
	for i := 0; i < 6; i++ {
		rows = append(rows, []string{strconv.Itoa(i + 1), strconv.Itoa(2 * i), strconv.Itoa(3*i + 1)})
	}
	createTestPulseFile(t, fsys, postWindowCohort, []string{"week", "x", "y"}, rows)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

func postWindowLag() *types.Window {
	return &types.Window{Type: types.WIN_LAG, Field: "x", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "week"}}}
}

// TestPredictPostWindow_Predict: a draft with a post-window formula
// chain and a slot that cannot move (PULSE_ATTRIBUTE_WINDOW_ORDER) is
// refused, and still reports which attributes run after the windows.
func TestPredictPostWindow_Predict(t *testing.T) {
	t.Parallel()

	p, _ := postWindowPulse(t)
	req := &Request{
		Cohort:  &types.Cohort{Filename: postWindowCohort},
		Windows: []*types.Window{postWindowLag()},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "pre", Expression: "y * 2"},
			{Type: types.ATTR_FORMULA, Label: "dbl", Expression: "lag_x * 2"},
			{Type: types.ATTR_ZSCORE, Field: "lag_x", Label: "z"},
		},
	}
	res, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Valid {
		t.Fatal("a non-row-local slot over a window label predicted valid")
	}
	want := &descriptor.PostWindowPlan{WindowLabels: []string{"lag_x"}, Attributes: []descriptor.PostWindowAttribute{
		{Index: 1, Label: "dbl", DependsOn: "lag_x", WindowReads: []string{"lag_x"}},
	}}
	if !reflect.DeepEqual(res.PostWindow, want) {
		t.Fatalf("post_window %+v, want %+v", res.PostWindow, want)
	}
}

// TestPredictPostWindow_PredictBytes: the in-memory entry reports a
// windows-first plan on the wire, and no post_window key for a request
// without a window.
func TestPredictPostWindow_PredictBytes(t *testing.T) {
	t.Parallel()

	p, fsys := postWindowPulse(t)
	data, err := afero.ReadFile(fsys, postWindowCohort)
	if err != nil {
		t.Fatal(err)
	}
	first := &Request{
		Windows:      []*types.Window{postWindowLag()},
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "half", Expression: "lag_x / 2"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "half", Label: "s"}},
	}
	env, err := p.PredictBytes(context.Background(), data, first)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		PostWindow json.RawMessage `json:"post_window"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	const want = `{"windows_first":true,"window_labels":["lag_x"],"attributes":[{"index":0,"label":"half","depends_on":"lag_x","window_reads":["lag_x"]}]}`
	if string(wire.PostWindow) != want {
		t.Fatalf("post_window %s, want %s (errors %+v)", wire.PostWindow, want, env.Errors)
	}

	plain := &Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y", Label: "s"}}}
	env, err = p.PredictBytes(context.Background(), data, plain)
	if err != nil {
		t.Fatal(err)
	}
	if res := env.Data.(*descriptor.PredictResult); res.PostWindow != nil {
		t.Fatalf("a request with no window reports %+v", res.PostWindow)
	}
}

// TestPredictPostWindow_PredictCompose: a slot scoring a batch fit whose
// predictor is a window label (ATTR_REG_PREDICT) runs that score after
// the windows — read through the fit's terms, not an expression.
func TestPredictPostWindow_PredictCompose(t *testing.T) {
	t.Parallel()

	p, _ := postWindowPulse(t)
	at := &types.Cohort{Filename: postWindowCohort}
	c := &ComposedRequest{
		Fits: []types.FitSpec{{Name: "m", Request: &Request{Cohort: at, Windows: []*types.Window{postWindowLag()},
			Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"lag_x"}}}}}},
		Requests: []*Request{{Label: "score", Cohort: at, Windows: []*types.Window{postWindowLag()},
			Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}}}},
	}
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("PredictCompose refused: %+v", env.Errors)
	}
	res := env.Data.(*ComposePredictResult)
	if len(res.Slots) != 1 {
		t.Fatalf("slots %+v", res.Slots)
	}
	want := &descriptor.PostWindowPlan{WindowLabels: []string{"lag_x"}, Attributes: []descriptor.PostWindowAttribute{
		{Index: 0, Label: "p", DependsOn: "lag_x", WindowReads: []string{"lag_x"}},
	}}
	if !reflect.DeepEqual(res.Slots[0].PostWindow, want) {
		t.Fatalf("slot post_window %+v, want %+v", res.Slots[0].PostWindow, want)
	}
}
