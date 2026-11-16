package vectors

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCategoricalWindowRefusal: a lagged category (a WIN_LAG of a
// categorical) is read as a GROUP_CATEGORY key or by another copy
// window; every other reader — an aggregation, another grouper, a value
// window or WIN_LIFT base, a regression, a matrix member, a post-window
// attribute — is PULSE_FIELD_TYPE_UNSUPPORTED.
func TestCategoricalWindowRefusal(t *testing.T) {
	t.Parallel()

	d := encoding.NewDictionary()
	_, _ = d.Add("a")
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "t", Type: encoding.FieldTypeU16},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
	}}
	lag := func() *types.Window {
		return &types.Window{Type: types.WIN_LAG, Field: "c", Label: "lc", OrderBy: []types.OrderKey{{Field: "t"}}}
	}
	later := func(typ types.WindowType, field, params string) *types.Window {
		w := &types.Window{Type: typ, Field: field, Label: "w2", OrderBy: []types.OrderKey{{Field: "t"}}}
		if params != "" {
			w.Params = []byte(params)
		}
		return w
	}
	count := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}
	for name, c := range map[string]struct {
		req    *types.Request
		refuse bool
	}{
		"record rows":   {req: &types.Request{Windows: []*types.Window{lag()}}},
		"group key":     {req: &types.Request{Windows: []*types.Window{lag()}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "lc"}}, Aggregations: count}},
		"crosstab axis": {req: &types.Request{Windows: []*types.Window{lag()}, Crosstab: &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "lc"}}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "c"}}, Cell: count[0]}}},
		"lead of lag":   {req: &types.Request{Windows: []*types.Window{lag(), later(types.WIN_LEAD, "lc", "")}}},
		"aggregation":   {req: &types.Request{Windows: []*types.Window{lag()}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "lc", Label: "n"}}}, refuse: true},
		"other grouper": {req: &types.Request{Windows: []*types.Window{lag()}, Groups: []*types.Group{{Type: types.GROUP_ROUNDED, Field: "lc", Interval: 1}}, Aggregations: count}, refuse: true},
		"value window":  {req: &types.Request{Windows: []*types.Window{lag(), later(types.WIN_RUNNING_SUM, "lc", "")}}, refuse: true},
		"lift base":     {req: &types.Request{Windows: []*types.Window{lag(), later(types.WIN_LIFT, "x", `{"base": "lc"}`)}}, refuse: true},
		"regression":    {req: &types.Request{Windows: []*types.Window{lag()}, Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "x", Predictors: []string{"lc"}}}}, refuse: true},
		"matrix member": {req: &types.Request{Windows: []*types.Window{lag()}, Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Name: "m", Fields: []string{"x", "lc"}}}}, refuse: true},
		"post-window":   {req: &types.Request{Windows: []*types.Window{lag()}, Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: `lc == "a" ? 1 : 0`}}}, refuse: true},
	} {
		t.Run(name, func(t *testing.T) {
			cerr := CategoricalWindowRefusal(c.req, schema, Classifier{})
			if !c.refuse {
				if cerr != nil {
					t.Fatalf("refused: %v", cerr)
				}
				return
			}
			if cerr == nil || cerr.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED || cerr.Details["field_type"] != "categorical_u8" {
				t.Fatalf("refusal %v, want PULSE_FIELD_TYPE_UNSUPPORTED over categorical_u8", cerr)
			}
		})
	}
}
