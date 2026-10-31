package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestFilterLateLabel_RefusedLikePredict: a filterer naming a label a
// LATER pipeline step writes — a window's, or an attribute's other than
// a before-filters ATTR_PREVIOUS_STATE — is refused once, with the code
// and message the runtime returns, and the message names the cause
// (filters run before that step) rather than only an unknown name. The
// window's own source and order fields exist over the record rows, so
// predict reports no other error; a field-keyed filter's values are
// never judged against a column that is not there.
func TestFilterLateLabel_RefusedLikePredict(t *testing.T) {
	t.Parallel()

	fs, cohort := zoneCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	lag := func() []*types.Window {
		return []*types.Window{{Type: types.WIN_LAG, Field: "n", Label: "prev", OrderBy: []types.OrderKey{{Field: "d"}}, PartitionBy: []string{"cat"}}}
	}
	zscore := func() []*types.Attribute {
		return []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "n", Label: "z"}}
	}
	expr := func(src string) []*types.Filterer {
		return []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: src}}
	}
	cases := map[string]struct {
		code errors.Code
		want string
		req  func() *types.Request
	}{
		"expression_window": {errors.PULSE_FORMULA_COMPILE_ERROR, "prev is the label of window[0] (WIN_LAG); filters run before windows", func() *types.Request {
			return &types.Request{Windows: lag(), Filterers: expr("prev > 1")}
		}},
		"expression_window_row_number": {errors.PULSE_FORMULA_COMPILE_ERROR, "rn is the label of window[0] (WIN_ROW_NUMBER); filters run before windows", func() *types.Request {
			return &types.Request{Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "d"}}}}, Filterers: expr("rn <= 3")}
		}},
		"expression_attribute": {errors.PULSE_FORMULA_COMPILE_ERROR, "z is the label of attribute[0] (ATTR_ZSCORE); filters run before attributes", func() *types.Request {
			return &types.Request{Attributes: zscore(), Filterers: expr("z > 1")}
		}},
		"include_window": {errors.SERVICE_VALIDATION, "prev is the label of window[0] (WIN_LAG); filters run before windows", func() *types.Request {
			return &types.Request{Windows: lag(), Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "prev", Values: []string{"1"}}}}
		}},
		"exclude_attribute": {errors.SERVICE_VALIDATION, "z is the label of attribute[0] (ATTR_ZSCORE); filters run before attributes", func() *types.Request {
			return &types.Request{Attributes: zscore(), Filterers: []*types.Filterer{{Type: types.FILTER_EXCLUDE, Field: "z", Values: []string{"x"}}}}
		}},
	}
	ctx := context.Background()
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req := func() *types.Request {
				r := c.req()
				r.Cohort = &types.Cohort{Filename: cohort}
				return r
			}
			_, rerr := p.Process(ctx, req())
			ce := requireCode(t, rerr, c.code)
			if !strings.Contains(ce.Message, c.want) {
				t.Fatalf("runtime message %q does not name the cause %q", ce.Message, c.want)
			}
			env := predictEnvelope(t, p, fs, cohort, req())
			sameEntry(t, env, rerr)
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly the one refusal", env.Errors)
			}
			if env.Data.(*descriptor.PredictResult).Valid {
				t.Fatal("predict Valid=true")
			}
			_, serr := p.ProcessStream(ctx, req())
			if se := requireCode(t, serr, c.code); se.Message != ce.Message {
				t.Fatalf("stream = %q, process = %q", se.Message, ce.Message)
			}
		})
	}
}

// TestFilterLateLabel_UnrelatedUnknownNameUnchanged: an unknown name
// that no later step writes keeps the plain unknown-name refusal, and a
// before-filters ATTR_PREVIOUS_STATE label stays readable.
func TestFilterLateLabel_UnrelatedUnknownNameUnchanged(t *testing.T) {
	t.Parallel()

	fs, cohort := zoneCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	req := &types.Request{
		Cohort:    &types.Cohort{Filename: cohort},
		Windows:   []*types.Window{{Type: types.WIN_LAG, Field: "n", Label: "prev", OrderBy: []types.OrderKey{{Field: "d"}}}},
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "zz > 1"}},
	}
	_, rerr := p.Process(context.Background(), req)
	ce := requireCode(t, rerr, errors.PULSE_FORMULA_COMPILE_ERROR)
	if want := `compiling filter expression "zz > 1": unknown name zz (1:1)`; ce.Message != want {
		t.Fatalf("message = %q, want %q", ce.Message, want)
	}
	if _, ok := ce.Details["label_source"]; ok {
		t.Fatalf("details = %v carry a label_source for a name no step writes", ce.Details)
	}

	// A before-filters attribute's label: an expression filter reads it,
	// a field-keyed filter is refused as before — never told the label
	// is written after the filters, which would be false.
	prev := func(fil *types.Filterer) *types.Request {
		return &types.Request{
			Cohort:     &types.Cohort{Filename: cohort},
			Attributes: []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "cat", SubjectField: "n", OrderField: "d", Label: "was"}},
			Filterers:  []*types.Filterer{fil},
		}
	}
	if _, err := p.Process(context.Background(), prev(&types.Filterer{Type: types.FILTER_EXPRESSION, Expression: `was == "a"`})); err != nil {
		t.Fatalf("expression over a previous-state label: %v", err)
	}
	_, ierr := p.Process(context.Background(), prev(&types.Filterer{Type: types.FILTER_INCLUDE, Field: "was", Values: []string{"a"}}))
	ie := requireCode(t, ierr, errors.SERVICE_VALIDATION)
	if want := "filter references unknown field: was"; ie.Message != want {
		t.Fatalf("message = %q, want %q", ie.Message, want)
	}
}

// TestFilterLateLabel_WindowOverAggregationKeepsItsOwnRefusal: beside
// an ungrouped aggregation the windows run over the one OUTPUT row, so a
// window reading the record field n (ordered by d) is refused on its
// own — predict reports it after the filter's refusal, and the runtime
// returns that same entry once the filter is dropped. Neither is a
// false field-missing report caused by the filter.
func TestFilterLateLabel_WindowOverAggregationKeepsItsOwnRefusal(t *testing.T) {
	t.Parallel()

	fs, cohort := zoneCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	req := func(filtered bool) *types.Request {
		r := &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "s"}},
			Windows:      []*types.Window{{Type: types.WIN_LAG, Field: "n", Label: "prev", OrderBy: []types.OrderKey{{Field: "d"}}}},
		}
		if filtered {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "prev > 1"}}
		}
		return r
	}
	ctx := context.Background()
	_, rerr := p.Process(ctx, req(true))
	requireCode(t, rerr, errors.PULSE_FORMULA_COMPILE_ERROR)
	env := predictEnvelope(t, p, fs, cohort, req(true))
	sameEntry(t, env, rerr)
	if len(env.Errors) < 2 {
		t.Fatalf("predict errors = %+v, want the filter's refusal then the window's", env.Errors)
	}
	_, werr := p.Process(ctx, req(false))
	we := requireCode(t, werr, errors.PULSE_WINDOW_INVALID)
	if got := env.Errors[1]; got.Code != string(we.Code) || got.Message != we.Message {
		t.Fatalf("predict's second error = %s %q, runtime without the filter = %s %q", got.Code, got.Message, we.Code, we.Message)
	}
}
