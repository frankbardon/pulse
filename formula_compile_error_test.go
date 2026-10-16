package pulse_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestFormulaCompileError_Process: through the facade, an expression
// that does not compile — a formula attribute, an expression filter, a
// total() argument — is PULSE_FORMULA_COMPILE_ERROR as the OUTERMOST
// coded error (the CLI envelope and MCP read that one), with expr-lang's
// reason in the message and details.compile_error and the source in
// details.expression. It used to be PROCESSING_RUNTIME without the
// reason (U45 #315).
func TestFormulaCompileError_Process(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	sum := []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}}
	for _, c := range []struct {
		name, expr, reason string
		req                *types.Request
	}{
		{"formula syntax", "qty *", "unexpected token EOF", &types.Request{
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "qty *"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}}}},
		{"filter type", `qty >= "a"`, "invalid operation", &types.Request{
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `qty >= "a"`}},
			Aggregations: sum}},
		{"filter unknown function", "nosuchfn(qty) > 1", "unknown name nosuchfn", &types.Request{
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "nosuchfn(qty) > 1"}},
			Aggregations: sum}},
		{"total argument", `qty + "x"`, "invalid operation", &types.Request{
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: `score / total(qty + "x")`}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.req.Cohort = &types.Cohort{Filename: "v.pulse"}
			_, err := p.Process(ctx, c.req)
			ce := codeOf(t, err)
			if ce.Code != perr.PULSE_FORMULA_COMPILE_ERROR {
				t.Fatalf("Process = %v, want PULSE_FORMULA_COMPILE_ERROR", err)
			}
			reason, _ := ce.Details["compile_error"].(string)
			if !strings.Contains(reason, c.reason) || ce.Details["expression"] != c.expr {
				t.Fatalf("details = %v, want compile_error naming %q, expression %q", ce.Details, c.reason, c.expr)
			}
			if !strings.Contains(ce.Error(), reason) {
				t.Fatalf("Error() = %q, want the reason %q on the wire", ce.Error(), reason)
			}
		})
	}
}
