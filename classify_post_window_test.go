package pulse

import (
	"context"
	stderrors "errors"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// classify_post_window_test.go: Pulse.ClassifyPostWindow reports, with
// no cohort, the post_window Predict reports over a cohort with the
// same schema — over the instance's afero.Fs for a saved model and a
// join's right cohort — and the runtime runs the attributes it lists
// after the windows.

const (
	classifyRightCohort = "post_window_right.pulse"
	classifyModelPath   = "lm.pulse-model.json"
)

// classifyModelFile is a saved OLS fit y ~ lag_x: an ATTR_REG_PREDICT
// bound to it reads the window label lag_x through the model's terms.
const classifyModelFile = `{"model_format_version": 1, "type": "REG_OLS", "target": "y",
	"predictors": ["lag_x"], "terms": ["lag_x"],
	"coefficients": {"(intercept)": 0.5, "lag_x": 2}, "n_obs": 6}`

// classifyPulse is postWindowPulse plus a right cohort and the saved
// model file on the same filesystem, and the left cohort's schema.
func classifyPulse(t *testing.T) (*Pulse, *encoding.Schema) {
	t.Helper()
	p, fsys := postWindowPulse(t)
	var rows [][]string
	for i := 0; i < 6; i++ {
		rows = append(rows, []string{strconv.Itoa(i + 1), strconv.Itoa(5 * i)})
	}
	createTestPulseFile(t, fsys, classifyRightCohort, []string{"week", "x"}, rows)
	if err := afero.WriteFile(fsys, classifyModelPath, []byte(classifyModelFile), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := p.Open(context.Background(), postWindowCohort)
	if err != nil {
		t.Fatal(err)
	}
	return p, c.Schema()
}

// classifyRequests are the shapes the facade must classify exactly as
// Predict: a post-window chain beside a record-only formula,
// windows-first, a saved model's score, a window shadowed by a joined
// field, and no window.
func classifyRequests() map[string]*Request {
	lag := postWindowLag
	return map[string]*Request{
		"chain": {Windows: []*types.Window{lag()}, Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "pre", Expression: "y * 2"},
			{Type: types.ATTR_FORMULA, Label: "dbl", Expression: "lag_x == nil ? nil : lag_x * 2"},
			{Type: types.ATTR_FORMULA, Label: "quad", Expression: "dbl == nil ? nil : dbl * 2"},
		}},
		"windows-first": {Windows: []*types.Window{lag()},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "half", Expression: "lag_x / 2"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "half", Label: "s"}}},
		"saved model": {Models: []types.ModelRef{{Name: "lm", Path: classifyModelPath}}, Windows: []*types.Window{lag()},
			Attributes: []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: []byte(`{"fit":"lm"}`)}}},
		"joined shadow": {
			Joins:   []*types.JoinSpec{{Right: classifyRightCohort, On: []types.OnPair{{LeftField: "week", RightField: "week"}}, As: "r_"}},
			Windows: []*types.Window{{Type: types.WIN_LAG, Field: "y", Label: "r_x", OrderBy: []types.OrderKey{{Field: "week"}}}, lag()},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Label: "f", Expression: "r_x * 2"},
				{Type: types.ATTR_FORMULA, Label: "g", Expression: "lag_x * 2"},
			}},
		"no window": {Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "y * 2"}}},
	}
}

// TestClassifyPostWindow_MatchesPredict: on every shape the cohort-less
// plan is Predict's post_window over the cohort; the saved model and
// the join really were read (the score moves; the shadowed formula
// does not).
func TestClassifyPostWindow_MatchesPredict(t *testing.T) {
	t.Parallel()

	p, schema := classifyPulse(t)
	ctx := context.Background()
	for name, req := range classifyRequests() {
		t.Run(name, func(t *testing.T) {
			got, err := p.ClassifyPostWindow(ctx, req, schema)
			if err != nil {
				t.Fatal(err)
			}
			at := *req
			at.Cohort = &types.Cohort{Filename: postWindowCohort}
			res, err := p.Predict(ctx, &at)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, res.PostWindow) {
				t.Fatalf("classified %+v, predict %+v (errors %+v)", got, res.PostWindow, res.Errors)
			}
			switch name {
			case "saved model":
				if got == nil || len(got.Attributes) != 1 || got.Attributes[0].Label != "p" {
					t.Fatalf("the saved model's score did not move: %+v", got)
				}
			case "joined shadow":
				if got == nil || len(got.Attributes) != 1 || got.Attributes[0].Label != "g" {
					t.Fatalf("the joined field r_x did not shadow its window: %+v", got)
				}
			case "no window":
				if got != nil {
					t.Fatalf("no window: %+v", got)
				}
			}
		})
	}
}

// TestClassifyPostWindow_RuntimeRunsThePlan: the attributes the plan
// lists are the ones the runtime computes from the window outputs —
// every row's dbl is twice its lag_x and quad twice dbl (null where the
// lag is), the saved model's score is its fit applied to lag_x — and
// pre, which the plan leaves before the windows, is the record's own.
func TestClassifyPostWindow_RuntimeRunsThePlan(t *testing.T) {
	t.Parallel()

	p, schema := classifyPulse(t)
	ctx := context.Background()
	reqs := classifyRequests()
	for _, name := range []string{"chain", "saved model"} {
		t.Run(name, func(t *testing.T) {
			req := reqs[name]
			plan, err := p.ClassifyPostWindow(ctx, req, schema)
			if err != nil {
				t.Fatal(err)
			}
			moved := map[string]bool{}
			for _, a := range plan.Attributes {
				moved[a.Label] = true
			}
			at := *req
			at.Cohort = &types.Cohort{Filename: postWindowCohort}
			resp, err := p.Process(ctx, &at)
			if err != nil {
				t.Fatal(err)
			}
			if len(resp.Data) != 6 {
				t.Fatalf("%d rows", len(resp.Data))
			}
			for i, row := range resp.Data {
				lag, has := row["lag_x"].(float64)
				want := map[string]any{}
				switch name {
				case "chain":
					want["pre"] = row["y"].(float64) * 2
					if has {
						want["dbl"], want["quad"] = 2*lag, 4*lag
					} else {
						want["dbl"], want["quad"] = nil, nil
					}
				case "saved model":
					want["p"] = nil
					if has {
						want["p"] = 0.5 + 2*lag
					}
				}
				for k, w := range want {
					if wf, ok := w.(float64); ok {
						g, ok := row[k].(float64)
						if !ok || math.Abs(g-wf) > 1e-12 {
							t.Fatalf("row %d: %s = %v, want %v", i, k, row[k], wf)
						}
					} else if row[k] != nil {
						t.Fatalf("row %d: %s = %v over a null lag", i, k, row[k])
					}
					// Only a slot the plan moves reads a window output.
					if k == "pre" && moved[k] {
						t.Fatalf("plan moved pre, a record-only formula: %+v", plan)
					}
					if k != "pre" && !moved[k] {
						t.Fatalf("the runtime computed %s from the windows but the plan keeps it before them: %+v", k, plan)
					}
				}
			}
		})
	}
}

// TestClassifyPostWindow_Errors: a nil request is SERVICE_VALIDATION, a
// cancelled ctx its own error; a nil schema classifies schema-free.
func TestClassifyPostWindow_Errors(t *testing.T) {
	t.Parallel()

	p, _ := classifyPulse(t)
	_, err := p.ClassifyPostWindow(context.Background(), nil, nil)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
		t.Fatalf("nil request: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.ClassifyPostWindow(ctx, classifyRequests()["chain"], nil); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx: %v", err)
	}
	plan, err := p.ClassifyPostWindow(context.Background(), classifyRequests()["chain"], nil)
	if err != nil || plan == nil || len(plan.Attributes) != 2 {
		t.Fatalf("schema-free plan %+v, %v", plan, err)
	}
}
