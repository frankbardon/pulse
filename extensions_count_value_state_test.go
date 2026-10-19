package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// U34 #216 / #230: an extension aggregator declares CountSemantic (its
// value is a count carried as a float64, written exact under
// return.precision) and ValueState (its state grows with the distinct
// values seen, priced by the memory estimate like a built-in
// value-state aggregator).

const (
	aggCvCount   types.AggregationType = "AGG_CV_COUNT"
	aggCvMeasure types.AggregationType = "AGG_CV_MEASURE"
	aggCvValue   types.AggregationType = "AGG_CV_VALUE"
	aggCvPlain   types.AggregationType = "AGG_CV_PLAIN"
)

// cvValue has more significant digits than any precision the tests
// ask for, so a rounded figure always differs from the exact one.
const cvValue = 12345.678

func cvRegistration(name types.AggregationType) pulse.AggregatorRegistration {
	return pulse.AggregatorRegistration{
		Name:        name,
		Description: "Test-only constant aggregator.",
		Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
			return &stubAggregator{value: cvValue}, nil
		},
		Streamable: true,
	}
}

func cvExtensions() pulse.Extensions {
	count := cvRegistration(aggCvCount)
	count.CountSemantic = true
	value := cvRegistration(aggCvValue)
	value.ValueState = true
	return pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
		count, cvRegistration(aggCvMeasure), value, cvRegistration(aggCvPlain),
	}}
}

func cvPulse(t *testing.T, l pulse.Limits) (*pulse.Pulse, string) {
	t.Helper()
	fs, cohort := limitsCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs, Extensions: cvExtensions(), Limits: l})
	if err != nil {
		t.Fatal(err)
	}
	return p, cohort
}

// wireTree is the wire JSON of resp decoded into a generic tree.
func wireTree(t *testing.T, resp *types.Response) map[string]any {
	t.Helper()
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestExtensions_CountSemanticExactUnderPrecision: under
// return.precision a CountSemantic extension aggregation's data column,
// a count crosstab cell's matrix figures (single cell and every-measure
// multi-measure form) and a count auxiliary margin aggregation's
// figures are written exactly as the unshaped run writes them, while
// the same constant from an undeclared extension rounds.
func TestExtensions_CountSemanticExactUnderPrecision(t *testing.T) {
	p, cohort := cvPulse(t, pulse.Limits{})
	ctx := context.Background()
	rows := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}
	cols := []*types.Group{{Type: types.GROUP_RANGE, Field: "a", Interval: 10}}
	margins := types.CrosstabMargins{Rows: true, Columns: true, Grand: true}
	cases := []struct {
		name string
		// part is the response key compared exact / rounded.
		part string
		req  func(at types.AggregationType) *types.Request
	}{
		{"aggregation column", "data", func(at types.AggregationType) *types.Request {
			return &types.Request{Groups: rows, Aggregations: []*types.Aggregation{{Type: at, Field: "a", Label: "v"}}}
		}},
		{"crosstab cell", "crosstab", func(at types.AggregationType) *types.Request {
			return &types.Request{Crosstab: &types.CrosstabSpec{Rows: rows, Columns: cols, Margins: margins,
				Cell: &types.Aggregation{Type: at, Field: "a", Label: "v"}}}
		}},
		{"crosstab measures", "crosstab", func(at types.AggregationType) *types.Request {
			return &types.Request{Crosstab: &types.CrosstabSpec{Rows: rows, Columns: cols, Margins: margins,
				Cells: []*types.Aggregation{{Type: at, Field: "a", Label: "v"}, {Type: at, Field: "b", Label: "w"}}}}
		}},
		{"margin aggregation", "components", func(at types.AggregationType) *types.Request {
			return &types.Request{Crosstab: &types.CrosstabSpec{Rows: rows, Columns: cols, Margins: margins,
				Cell:               &types.Aggregation{Type: types.AGG_SUM, Field: "a", Label: "s"},
				MarginAggregations: []*types.Aggregation{{Type: at, Field: "a", Label: "v"}}}}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func(at types.AggregationType, prec int) any {
				req := c.req(at)
				req.Cohort = &types.Cohort{Filename: cohort}
				if prec > 0 {
					req.Return = &types.Return{Precision: prec}
				}
				resp, err := p.Process(ctx, req)
				if err != nil {
					t.Fatalf("%s: %v", at, err)
				}
				tree := wireTree(t, resp)
				part := tree[c.part]
				if c.part == "components" {
					// Only the auxiliary margin figures: the cell
					// components' sums round on both arms.
					ct := tree["components"].(map[string]any)["crosstab"].(map[string]any)
					part = []any{ct["row_margin_aggregations"], ct["column_margin_aggregations"], ct["grand_total_aggregations"]}
				}
				b, _ := json.Marshal(part)
				return string(b)
			}
			for _, at := range []types.AggregationType{aggCvCount, aggCvMeasure} {
				base, shaped := run(at, 0), run(at, 2)
				if at == aggCvCount && shaped != base {
					t.Errorf("count-semantic %s rounded under precision:\n got %s\nwant %s", c.part, shaped, base)
				}
				if at == aggCvMeasure && shaped == base {
					t.Errorf("undeclared extension %s not rounded under precision (gate vacuous): %s", c.part, shaped)
				}
			}
		})
	}
}

// TestExtensions_CountSemanticVectorRefused: CountSemantic on a
// VectorInput (or VectorInput + ArrayValued) aggregator is refused at
// pulse.New with PULSE_EXTENSION_PARAM_INVALID, reason
// count_semantic_vector_input; on a single-field aggregator it passes.
func TestExtensions_CountSemanticVectorRefused(t *testing.T) {
	for _, tc := range []struct {
		name               string
		vector, arrayValue bool
		want               bool
	}{
		{"vector input", true, false, true},
		{"array valued", true, true, true},
		{"single field", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := cvRegistration(aggCvCount)
			r.CountSemantic, r.VectorInput, r.ArrayValued = true, tc.vector, tc.arrayValue
			_, err := pulse.New(newTestOptions(pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{r}}))
			if !tc.want {
				if err != nil {
					t.Fatalf("single-field CountSemantic refused: %v", err)
				}
				return
			}
			assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
			var ce *perr.CodedError
			_ = stderrors.As(err, &ce)
			if ce.Details["reason"] != "count_semantic_vector_input" || ce.Details["name"] != string(aggCvCount) {
				t.Fatalf("details = %v", ce.Details)
			}
		})
	}
}

// TestExtensions_CountSemanticValueStateInManifest: both declarations
// project onto the manifest's extensions block (omitted when false).
func TestExtensions_CountSemanticValueStateInManifest(t *testing.T) {
	p, _ := cvPulse(t, pulse.Limits{})
	got := map[string][2]bool{}
	for _, a := range p.Manifest(context.Background()).Extensions.Aggregators {
		got[a.Name] = [2]bool{a.CountSemantic, a.ValueState}
	}
	want := map[string][2]bool{
		string(aggCvCount): {true, false}, string(aggCvMeasure): {false, false},
		string(aggCvValue): {false, true}, string(aggCvPlain): {false, false},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("manifest flags = %v, want %v", got, want)
	}
}

// TestExtensions_ValueStateMemoryEstimate: a ValueState extension
// aggregator's MaxEstimatedMemory estimate equals a built-in
// value-state aggregator's (AGG_MODE) on the streaming and the buffered
// arm, an undeclared one equals a fixed accumulator's (AGG_SUM), and
// predict and the runtime agree: a limit between the two refuses the
// ValueState slot on both and admits the undeclared one.
func TestExtensions_ValueStateMemoryEstimate(t *testing.T) {
	ctx := context.Background()
	arms := map[string]func(cohort string, at types.AggregationType) *types.Request{
		"streaming": func(cohort string, at types.AggregationType) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Aggregations: []*types.Aggregation{{Type: at, Field: "a", Label: "v"}}}
		},
		"buffered": func(cohort string, at types.AggregationType) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Aggregations: []*types.Aggregation{{Type: at, Field: "a", Label: "v"},
					{Type: types.AGG_MEDIAN, Field: "a", Label: "m"}}}
		},
	}
	for name, mk := range arms {
		t.Run(name, func(t *testing.T) {
			probe, cohort := cvPulse(t, pulse.Limits{MaxEstimatedMemory: 1})
			estimate := func(at types.AggregationType) int64 {
				res, err := probe.Predict(ctx, mk(cohort, at))
				if err != nil {
					t.Fatal(err)
				}
				if len(res.LimitFindings) != 1 {
					t.Fatalf("%s findings = %+v", at, res.LimitFindings)
				}
				return res.LimitFindings[0].Estimated
			}
			mode, value := estimate(types.AGG_MODE), estimate(aggCvValue)
			sum, plain := estimate(types.AGG_SUM), estimate(aggCvPlain)
			if value != mode {
				t.Errorf("ValueState extension estimate %d, built-in AGG_MODE %d", value, mode)
			}
			if plain != sum {
				t.Errorf("undeclared extension estimate %d, built-in AGG_SUM %d", plain, sum)
			}
			if value <= plain {
				t.Fatalf("ValueState estimate %d not above undeclared %d", value, plain)
			}

			p, _ := cvPulse(t, pulse.Limits{MaxEstimatedMemory: value - 1})
			res, err := p.Predict(ctx, mk(cohort, aggCvValue))
			if err != nil {
				t.Fatal(err)
			}
			if res.Valid || len(res.LimitFindings) != 1 || res.LimitFindings[0].Estimated != value {
				t.Fatalf("predict valid=%v findings=%+v, want refusal at %d", res.Valid, res.LimitFindings, value)
			}
			_, perr := p.Process(ctx, mk(cohort, aggCvValue))
			d := requireLimitExceeded(t, perr, "max_estimated_memory", value-1, "Options.Limits.MaxEstimatedMemory")
			if d["observed"] != value {
				t.Fatalf("runtime observed %v, predict estimated %d", d["observed"], value)
			}
			if res, err := p.Predict(ctx, mk(cohort, aggCvPlain)); err != nil || !res.Valid {
				t.Fatalf("undeclared under the limit: valid=%v err=%v findings=%+v", res.Valid, err, res.LimitFindings)
			}
			if _, err := p.Process(ctx, mk(cohort, aggCvPlain)); err != nil {
				t.Fatalf("undeclared under the limit: %v", err)
			}
		})
	}
}
