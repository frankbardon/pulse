package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// extensions_windows_first_test.go (U67 E3-S1): an extension aggregator
// whose FieldInputs hook names a window label reads it exactly as a
// built-in aggregation's field does — the request runs windows-first,
// predict says so at every entry point, and the runtime folds the
// window value off the records. Before, the classification could not
// see the hook: predict and the runtime both refused the read as an
// unknown field.

const aggWfxSumOf types.AggregationType = "AGG_WFX_SUM_OF"

// wfxSumOf sums the column params.of names (Field is ignored).
type wfxSumOf struct{ of string }

func (a *wfxSumOf) Aggregate(rows extend.Rows, _ string) (float64, error) {
	var sum float64
	for i := 0; i < rows.Len(); i++ {
		if v, ok := rows.At(i).NumericValue(a.of); ok {
			sum += v
		}
	}
	return sum, nil
}

// wfxParams is the operator's params: of names the summed column,
// scale is an expression-capable number the hook must decode.
type wfxParams struct {
	Of    string  `json:"of"`
	Scale float64 `json:"scale"`
}

// wfxSumOfRegistration declares of through FieldInputs. The hook decodes
// the whole params block strictly (a decode error declares nothing), so
// a scale written as an expression reaches it as the number stand-in on
// both sides, or not at all.
func wfxSumOfRegistration() pulse.AggregatorRegistration {
	return pulse.AggregatorRegistration{
		Name:        aggWfxSumOf,
		Description: "Test-only sum of the column params.of names.",
		Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
			var p wfxParams
			if len(spec.Params) > 0 {
				if err := json.Unmarshal(spec.Params, &p); err != nil {
					return nil, err
				}
			}
			return &wfxSumOf{of: p.Of}, nil
		},
		Accepts: []encoding.FieldType{encoding.FieldTypeF64},
		Params: []pulse.ParamMeta{
			{Name: "of", JSONType: "string", Description: "Column to sum."},
			{Name: "scale", JSONType: "number", Expression: true, Description: "Unused scale."},
		},
		FieldInputs: func(raw json.RawMessage) []string {
			var p wfxParams
			if json.Unmarshal(raw, &p) != nil || p.Of == "" {
				return nil
			}
			return []string{p.Of}
		},
	}
}

func wfxPulse(t *testing.T) *pulse.Pulse {
	t.Helper()
	p, _ := windowParityCohort(t, pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{wfxSumOfRegistration()}})
	return p
}

func wfxLag() []*types.Window {
	return []*types.Window{{Type: types.WIN_LAG, Field: "x", Label: "lag_x", OrderBy: []types.OrderKey{{Field: "t"}}}}
}

// wfxAgg is the extension slot reading params (which name lag_x).
func wfxAgg(params string) *types.Aggregation {
	return &types.Aggregation{Type: aggWfxSumOf, Field: "x", Label: "s", Params: json.RawMessage(params)}
}

// wfxSum is the built-in twin: AGG_SUM over lag_x.
func wfxSum() *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_SUM, Field: "lag_x", Label: "s"}
}

// wfxShapes are the request shapes the hook turns windows-first: an
// ungrouped and a grouped aggregation, and a crosstab cell. agg builds
// the slot (the extension or its built-in twin).
func wfxShapes() []struct {
	name string
	req  func(agg *types.Aggregation) *pulse.Request
} {
	at := &types.Cohort{Filename: "series.pulse"}
	return []struct {
		name string
		req  func(agg *types.Aggregation) *pulse.Request
	}{
		{"ungrouped", func(agg *types.Aggregation) *pulse.Request {
			return &pulse.Request{Cohort: at, Windows: wfxLag(), Aggregations: []*types.Aggregation{agg}}
		}},
		{"grouped", func(agg *types.Aggregation) *pulse.Request {
			return &pulse.Request{Cohort: at, Windows: wfxLag(),
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}, Aggregations: []*types.Aggregation{agg}}
		}},
		{"crosstab cell", func(agg *types.Aggregation) *pulse.Request {
			return &pulse.Request{Cohort: at, Windows: wfxLag(), Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "t"}},
				Cell:    agg,
			}}
		}},
	}
}

// wfxPayload is resp's figures with the operator name normalised, so
// the extension response compares to its built-in twin's.
func wfxPayload(t *testing.T, resp *pulse.Response) string {
	t.Helper()
	b, err := json.Marshal(struct {
		Data     any `json:"data"`
		Crosstab any `json:"crosstab"`
	}{resp.Data, resp.Crosstab})
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), string(aggWfxSumOf), string(types.AGG_SUM))
}

// wfxAssertWindowsFirst fails unless a predict verdict names the
// request windows-first and buffered.
func wfxAssertWindowsFirst(t *testing.T, where string, plan *descriptor.PostWindowPlan, streamable bool, reasons []string) {
	t.Helper()
	if plan == nil || !plan.WindowsFirst {
		t.Fatalf("%s: post_window %+v, want windows_first", where, plan)
	}
	if streamable || !strings.Contains(strings.Join(reasons, "|"), "windows-first") {
		t.Fatalf("%s: streamable %v, reasons %v; want buffered, windows-first", where, streamable, reasons)
	}
}

// TestExtensions_FieldInputsWindowLabelRunsWindowsFirst: Predict,
// PredictBytes and Process agree — the request is valid, windows-first
// and buffered, and the extension sums the lagged column exactly as
// AGG_SUM over the window label does, on every shape.
func TestExtensions_FieldInputsWindowLabelRunsWindowsFirst(t *testing.T) {
	t.Parallel()

	p := wfxPulse(t)
	ctx := context.Background()
	for _, shape := range wfxShapes() {
		t.Run(shape.name, func(t *testing.T) {
			req := shape.req(wfxAgg(`{"of": "lag_x"}`))
			res, err := p.Predict(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Valid {
				t.Fatalf("predict refused: %+v", res)
			}
			wfxAssertWindowsFirst(t, "Predict", res.PostWindow, res.Streamable, res.StreamableReasons)

			got, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			want, err := p.Process(ctx, shape.req(wfxSum()))
			if err != nil {
				t.Fatalf("built-in twin: %v", err)
			}
			if g, w := wfxPayload(t, got), wfxPayload(t, want); g != w {
				t.Fatalf("extension result\n%s\nwant the built-in twin's\n%s", g, w)
			}
		})
	}
}

// TestExtensions_FieldInputsWindowLabelPredictBytes: the in-memory
// predict entry classifies through the same hooks.
func TestExtensions_FieldInputsWindowLabelPredictBytes(t *testing.T) {
	t.Parallel()

	p, fsys := windowParityCohort(t, pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{wfxSumOfRegistration()}})
	data, err := afero.ReadFile(fsys, "series.pulse")
	if err != nil {
		t.Fatal(err)
	}
	req := &pulse.Request{Windows: wfxLag(), Aggregations: []*types.Aggregation{wfxAgg(`{"of": "lag_x"}`)}}
	env, err := p.PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("PredictBytes refused: %+v", env.Errors)
	}
	res := env.Data.(*descriptor.PredictResult)
	wfxAssertWindowsFirst(t, "PredictBytes", res.PostWindow, res.Streamable, res.StreamableReasons)
}

// TestExtensions_FieldInputsWindowLabelCompose: a Compose slot carrying
// the extension read is windows-first in PredictCompose and runs to the
// built-in twin's figures in Compose.
func TestExtensions_FieldInputsWindowLabelCompose(t *testing.T) {
	t.Parallel()

	p := wfxPulse(t)
	ctx := context.Background()
	shape := wfxShapes()[1]
	ext, twin := shape.req(wfxAgg(`{"of": "lag_x"}`)), shape.req(wfxSum())
	ext.Label, twin.Label = "ext", "twin"
	c := &pulse.ComposedRequest{Requests: []*pulse.Request{ext, twin}}
	env, err := p.PredictCompose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("PredictCompose refused: %+v", env.Errors)
	}
	res := env.Data.(*pulse.ComposePredictResult)
	if len(res.Slots) != 2 {
		t.Fatalf("slots %+v", res.Slots)
	}
	wfxAssertWindowsFirst(t, "PredictCompose", res.Slots[0].PostWindow, res.Slots[0].Streamable, res.Slots[0].StreamableReasons)

	out, err := p.Compose(ctx, c)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if g, w := wfxPayload(t, out.Responses[0]), wfxPayload(t, out.Responses[1]); g != w {
		t.Fatalf("extension slot\n%s\nwant the built-in slot's\n%s", g, w)
	}
}

// TestExtensions_FieldInputsWindowLabelExpressionParam: the hook sees an
// expression-capable param written as an expression as the number
// stand-in on both sides (extscalar.StandIns), so its strict decode
// declares the window read at predict exactly as at the runtime.
func TestExtensions_FieldInputsWindowLabelExpressionParam(t *testing.T) {
	t.Parallel()

	p := wfxPulse(t)
	ctx := context.Background()
	shape := wfxShapes()[0]
	req := shape.req(wfxAgg(`{"of": "lag_x", "scale": "1 + 1"}`))
	res, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("predict refused: %+v", res)
	}
	wfxAssertWindowsFirst(t, "Predict", res.PostWindow, res.Streamable, res.StreamableReasons)
	got, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want, err := p.Process(ctx, shape.req(wfxSum()))
	if err != nil {
		t.Fatal(err)
	}
	if g, w := wfxPayload(t, got), wfxPayload(t, want); g != w {
		t.Fatalf("extension result\n%s\nwant\n%s", g, w)
	}
}

// TestExtensions_FieldInputsWindowLabelExplain: Explain classifies with
// the instance's hooks too — a post-window formula read only by the
// extension aggregator is described after the windows.
func TestExtensions_FieldInputsWindowLabelExplain(t *testing.T) {
	t.Parallel()

	p := wfxPulse(t)
	req := &pulse.Request{Cohort: &types.Cohort{Filename: "series.pulse"}, Windows: wfxLag(),
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "dbl", Expression: "(lag_x ?? 0) * 2"}},
		Aggregations: []*types.Aggregation{wfxAgg(`{"of": "dbl"}`)}}
	res, err := p.Explain(context.Background(), descriptor.ExplainRequest{Request: req})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range res.Steps {
		if st.Slot == "attributes[0]" {
			if !strings.Contains(st.Text, "after the windows") {
				t.Fatalf("post-window step text = %q", st.Text)
			}
			return
		}
	}
	t.Fatalf("no attributes[0] step in %+v", res.Steps)
}

// TestExtensions_FieldInputsUnknownNameStillRefused: a hook naming a
// column nothing produces is still the walk's unknown-field refusal on
// both sides, and a request whose hook names no window label keeps the
// historical order.
func TestExtensions_FieldInputsUnknownNameStillRefused(t *testing.T) {
	t.Parallel()

	p := wfxPulse(t)
	ctx := context.Background()
	shape := wfxShapes()[0]
	bad := shape.req(wfxAgg(`{"of": "nope"}`))
	res, err := p.Predict(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	if res.Valid {
		t.Fatal("predict admitted a FieldInputs name nothing produces")
	}
	if _, err := p.Process(ctx, bad); err == nil || !strings.Contains(err.Error(), "FieldInputs references unknown field nope") {
		t.Fatalf("Process err %v, want the unknown-field refusal", err)
	}
	plain := shape.req(wfxAgg(`{"of": "y"}`))
	res, err = p.Predict(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if res.PostWindow == nil || res.PostWindow.WindowsFirst {
		t.Fatalf("post_window %+v, want the historical order", res.PostWindow)
	}
}
