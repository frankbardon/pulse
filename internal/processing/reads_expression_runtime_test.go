package processing

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestOperators_ReadsExpressionMatchesRuntime is the runtime half of the
// Operator.ReadsExpression claim, which decides whether a materialized
// column's description quotes the slot's Expression.
//
// Every built-in attribute and filterer is built twice over the same
// slot — once with no Expression, once with a valid one. An operator
// that declares ReadsExpression must refuse the empty one and accept the
// valid one (the expression is its defining input); one that does not
// must build identically either way (it never reads the slot, so quoting
// it would describe a computation that did not happen).
func TestOperators_ReadsExpressionMatchesRuntime(t *testing.T) {
	m := descx.BuildManifest()
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
	const expr = "x > 0"
	errText := func(err error) string {
		if err == nil {
			return ""
		}
		return err.Error()
	}
	check := func(t *testing.T, flagged bool, without, with error) {
		t.Helper()
		switch {
		case flagged && (without == nil || with != nil):
			t.Errorf("declares ReadsExpression, but no expression -> %v, an expression -> %v", without, with)
		case !flagged && errText(without) != errText(with):
			t.Errorf("the Expression changed the build (%v vs %v) — declare ReadsExpression", without, with)
		}
	}
	flagged := 0
	for _, op := range m.Components.Attributes {
		factory, ok := attributeRegistry[types.AttributeType(op.Name)]
		if !ok {
			t.Errorf("%s: no attribute factory", op.Name)
			continue
		}
		if op.ReadsExpression {
			flagged++
		}
		t.Run(op.Name, func(t *testing.T) {
			build := func(e string) error {
				_, err := factory(&types.Attribute{Type: types.AttributeType(op.Name), Field: "x", Label: "o", Expression: e}, schema)
				return err
			}
			check(t, op.ReadsExpression, build(""), build(expr))
		})
	}
	for _, op := range m.Components.Filterers {
		factory, ok := filtererRegistry[types.FiltererType(op.Name)]
		if !ok {
			t.Errorf("%s: no filterer factory", op.Name)
			continue
		}
		if op.ReadsExpression {
			flagged++
		}
		t.Run(op.Name, func(t *testing.T) {
			build := func(e string) error {
				_, err := factory().Build(&types.Filterer{Type: types.FiltererType(op.Name), Field: "x", Expression: e}, schema)
				return err
			}
			check(t, op.ReadsExpression, build(""), build(expr))
		})
	}
	for _, group := range [][]string{readsExpressionNames(m.Components.Aggregators), readsExpressionNames(m.Components.Groupers), readsExpressionNames(m.Components.Windows), readsExpressionNames(m.Components.Features)} {
		for _, n := range group {
			t.Errorf("%s declares ReadsExpression, but its slot carries no Expression", n)
		}
	}
	if flagged == 0 {
		t.Fatal("no operator declares ReadsExpression")
	}
}

// readsExpressionNames lists the operators in ops that declare
// ReadsExpression.
func readsExpressionNames(ops []descriptor.Operator) []string {
	var out []string
	for _, op := range ops {
		if op.ReadsExpression {
			out = append(out, op.Name)
		}
	}
	return out
}
