package synth

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/frankbardon/pulse/errors"
)

// compiledConstraints is a small wrapper that holds compiled programs so
// the per-row hot path avoids re-parsing.
type compiledConstraints struct {
	progs []*vm.Program
	exprs []string
}

func compileConstraints(in []ConstraintSpec, wfs []*writerField) (*compiledConstraints, error) {
	if len(in) == 0 {
		return &compiledConstraints{}, nil
	}
	// Build an environment shape so expr can pick the right comparison
	// operators. We use map[string]any with one entry per field name.
	env := make(map[string]any, len(wfs))
	for _, wf := range wfs {
		env[wf.spec.Name] = sentinelFor(wf.spec.Type)
	}

	out := &compiledConstraints{
		progs: make([]*vm.Program, 0, len(in)),
		exprs: make([]string, 0, len(in)),
	}
	for _, c := range in {
		prog, err := expr.Compile(c.Expr, expr.Env(env), expr.AsBool())
		if err != nil {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("compiling constraint %q: %v", c.Expr, err),
				map[string]any{"expr": c.Expr})
		}
		out.progs = append(out.progs, prog)
		out.exprs = append(out.exprs, c.Expr)
	}
	return out, nil
}

// evaluate returns true when every compiled constraint accepts the row.
// A non-nil error is returned only for runtime evaluation failures
// (typically a programming bug — constraints are compile-time validated).
func (c *compiledConstraints) evaluate(row map[string]any) (bool, error) {
	for i, p := range c.progs {
		out, err := expr.Run(p, row)
		if err != nil {
			return false, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("evaluating constraint %q: %v", c.exprs[i], err),
				map[string]any{"expr": c.exprs[i]})
		}
		ok, isBool := out.(bool)
		if !isBool {
			return false, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("constraint %q did not return bool", c.exprs[i]), nil)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// sentinelFor returns a non-nil zero value of the right Go shape for the
// expr environment. expr inspects the type at compile time, so the
// concrete value does not matter — only its kind.
func sentinelFor(typeName string) any {
	switch typeName {
	case "u8", "u16", "u32", "u64", "f32", "f64",
		"date", "nullable_u4", "nullable_u8", "nullable_u16",
		"h3_cell":
		return float64(0)
	case "packed_bool", "nullable_bool":
		return false
	case "categorical_u8", "categorical_u16", "categorical_u32":
		return ""
	case "decimal128", "nullable_decimal128":
		return float64(0)
	case "point_f64":
		return map[string]any{"lat": float64(0), "lon": float64(0)}
	}
	return float64(0)
}
