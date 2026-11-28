package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// predict_parity_arity_cell_interval_test.go pins three predict ≡
// runtime refusals: a reserved built-in's arity fault the expression
// compile already refuses is reported once, as the compile error the
// run raises (#780); a crosstab cell or margin aggregation naming no
// field is refused as the aggregations list refuses one, never counted
// to zeros (#793); a negative GROUP_RANGE / GROUP_ROUNDED interval is
// PROCESSING_CONFIG on both sides, never silently 1 (#506).

func TestPredictParity_ArityCellInterval(t *testing.T) {
	t.Parallel()
	p := opConfigPulse(t)
	base := func(mut func(r *Request)) func() *Request {
		return func() *Request {
			r := &Request{Cohort: &types.Cohort{Filename: opConfigCohort},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "age", Label: "v"}}}
			mut(r)
			return r
		}
	}
	formula := func(expr string) func(r *Request) {
		return func(r *Request) {
			r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "d", Expression: expr}}
		}
	}
	filter := func(expr string) func(r *Request) {
		return func(r *Request) {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}}
		}
	}
	crosstab := func(cell, margin *types.Aggregation) func(r *Request) {
		return func(r *Request) {
			r.Aggregations = nil
			r.Crosstab = &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "quarter"}},
				Cell:    cell, Margins: types.CrosstabMargins{Grand: true}}
			if margin != nil {
				r.Crosstab.MarginAggregations = []*types.Aggregation{margin}
			}
		}
	}
	group := func(op types.GroupType, interval float64) func(r *Request) {
		return func(r *Request) {
			r.Groups = []*types.Group{{Type: op, Field: "age", Interval: interval}}
		}
	}
	cases := []struct {
		name   string
		refuse bool
		build  func() *Request
	}{
		// #780: the compile error alone, as the run reports it.
		{"arity/formula", true, base(formula("to_day()"))},
		{"arity/formula_total_arg", true, base(formula("total(to_day())"))},
		{"arity/filter", true, base(filter("to_day() > 0"))},
		{"arity/formula_ok", false, base(formula("age * 2"))},
		// #793: a fieldless cell / margin aggregation.
		{"cell/count_no_field", true, base(crosstab(&types.Aggregation{Type: types.AGG_COUNT}, nil))},
		{"cell/sum_no_field", true, base(crosstab(&types.Aggregation{Type: types.AGG_SUM}, nil))},
		{"cell/margin_no_field", true, base(crosstab(&types.Aggregation{Type: types.AGG_COUNT, Field: "age"},
			&types.Aggregation{Type: types.AGG_COUNT}))},
		{"cell/count_ok", false, base(crosstab(&types.Aggregation{Type: types.AGG_COUNT, Field: "age"}, nil))},
		// #506: a negative interval; 0 is the unset slot.
		{"interval/range_negative", true, base(group(types.GROUP_RANGE, -5))},
		{"interval/rounded_negative", true, base(group(types.GROUP_ROUNDED, -0.5))},
		{"interval/range_unset", false, base(group(types.GROUP_RANGE, 0))},
		{"interval/rounded_ok", false, base(group(types.GROUP_ROUNDED, 5))},
		{"interval/crosstab_row", true, base(func(r *Request) {
			crosstab(&types.Aggregation{Type: types.AGG_COUNT, Field: "age"}, nil)(r)
			r.Crosstab.Rows = []*types.Group{{Type: types.GROUP_RANGE, Field: "age", Interval: -2}}
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPredictRuntimeParity(t, p, tc.build, tc.refuse)
		})
	}
}
