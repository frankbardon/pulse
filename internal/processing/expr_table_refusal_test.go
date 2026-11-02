package processing

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestExprTableRefusal_TopLevelCode: a data-dependent lookup() fault —
// a key no row holds, a null key, a table name computed per row — fails
// the run under the table's OWN code at the top of the error (what an
// envelope reports), with its details, never wrapped as
// PROCESSING_RUNTIME "evaluating ... expression"; in a formula and in a
// filter alike.
func TestExprTableRefusal_TopLevelCode(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}}}
	exts := &ExtensionRegistry{LookupTables: map[string]LookupTable{
		"adj": {Rows: map[string]float64{"1": 0.5}},
	}}
	cases := []struct {
		name, expr string
		filter     bool
		code       perr.Code
		detail     string
		want       any
	}{
		{"miss formula", `lookup("adj", score)`, false, perr.PULSE_LOOKUP_MISS, "table", "adj"},
		{"miss filter", `lookup("adj", score) > 0`, true, perr.PULSE_LOOKUP_MISS, "table", "adj"},
		{"null key", `lookup("adj", score > 1 ? nil : "1")`, false, perr.PULSE_LOOKUP_MISS, "null_key", 0},
		{"computed table", `lookup(score > 1 ? "nope" : "adj", "1")`, false, perr.PULSE_LOOKUP_TABLE_UNKNOWN, "table", "nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records := []*Record{
				NewRecord(schema, map[string]float64{"score": 1}),
				NewRecord(schema, map[string]float64{"score": 2}),
			}
			req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}}}
			if tc.filter {
				req.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: tc.expr}}
			} else {
				req.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "score", Expression: tc.expr, Label: "x"}}
			}
			_, err := NewProcessorWithExtensions(schema, exts).Process(context.Background(), req, NewSliceIterator(records))
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("err = %v, want top-level %s", err, tc.code)
			}
			if ce.Details[tc.detail] != tc.want {
				t.Errorf("details.%s = %v, want %v (%v)", tc.detail, ce.Details[tc.detail], tc.want, ce.Details)
			}
		})
	}
}
