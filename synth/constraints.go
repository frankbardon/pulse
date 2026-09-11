package synth

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
	"github.com/frankbardon/pulse/errors"
)

// isnullFuncName is the expression-visible name of the null predicate.
const isnullFuncName = "isnull"

// compiledConstraints is a small wrapper that holds compiled programs so
// the per-row hot path avoids re-parsing.
type compiledConstraints struct {
	progs []*vm.Program
	exprs []string

	// fields is the set of field names the Spec declared, used to reject
	// isnull("typo") at run time (loudly) rather than answering false
	// for a field that does not exist.
	fields map[string]bool

	// nullMask is the CURRENT row's null state. evaluate sets it before
	// running any program and the isnull builtin — a method value closed
	// over this struct, baked into every compiled program — reads it.
	// The mask cannot ride the runtime env map: expr.Run's env is the row
	// itself and a row carries a value for a nulled field too (see
	// nullableSampler.next, which returns the drawn value alongside
	// isNull=true), so nullness is simply not derivable from the env.
	// One compiledConstraints per generate() call, one row at a time —
	// not safe for concurrent evaluation.
	nullMask map[string]bool
}

// rowExprEnv builds the expr environment shape for a set of declared
// fields: one entry per field name, carrying a sentinel of the Go type
// the ROW will hold for it (see sentinelFor). The second return is the
// declared-name set, used both to reject isnull("typo") and to gate the
// bare-identifier patch.
//
// It takes []FieldSpec rather than []*writerField so the two callers can
// share it: compileConstraints runs after buildSchema and has writer
// fields, while rule validation (synth/rules.go) runs BEFORE buildSchema
// and has only the Spec. Compiling a rule's `when` against a
// hand-rolled second environment is exactly the drift that produced
// issue #258, so there is one builder and both go through it.
func rowExprEnv(fields []FieldSpec) (env map[string]any, names map[string]bool) {
	env = make(map[string]any, len(fields))
	names = make(map[string]bool, len(fields))
	for _, f := range fields {
		env[f.Name] = sentinelFor(f.Type)
		names[f.Name] = true
	}
	return env, names
}

// rowExprOptions returns the expr.Option set every expression compiled
// against a generated row shares: the environment, the isnull builtin
// and the bare-identifier patch that feeds it. isnull is passed in
// rather than looked up because it must close over the evaluating
// compiledConstraints' current null mask; a validation-only caller
// supplies a throwaway probe whose mask is never read.
//
// extra is appended last, so a caller wanting expr.AsBool() (a `when`
// predicate or a constraint) adds it and a caller compiling a value
// expression (`set_expr`, whose return type is the coercion matrix's
// business, not the compiler's) does not.
func rowExprOptions(env map[string]any, names map[string]bool, isnull func(...any) (any, error), extra ...expr.Option) []expr.Option {
	opts := []expr.Option{
		expr.Env(env),
		expr.Function(isnullFuncName, isnull, new(func(string) bool)),
		expr.Patch(isnullIdentifierPatcher{fields: names}),
	}
	return append(opts, extra...)
}

func compileConstraints(in []ConstraintSpec, wfs []*writerField) (*compiledConstraints, error) {
	if len(in) == 0 {
		return &compiledConstraints{}, nil
	}
	specs := make([]FieldSpec, 0, len(wfs))
	for _, wf := range wfs {
		specs = append(specs, wf.spec)
	}
	env, names := rowExprEnv(specs)

	out := &compiledConstraints{
		progs:  make([]*vm.Program, 0, len(in)),
		exprs:  make([]string, 0, len(in)),
		fields: names,
	}
	opts := rowExprOptions(env, names, out.isnullBuiltin, expr.AsBool())
	for _, c := range in {
		prog, err := expr.Compile(c.Expr, opts...)
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

// isnullBuiltin answers the null state of one field on the row being
// evaluated. Takes the field NAME, not its value: a nulled field still
// carries a drawn value in the row map, so only the mask knows.
func (c *compiledConstraints) isnullBuiltin(params ...any) (any, error) {
	if len(params) != 1 {
		return nil, fmt.Errorf("%s expects exactly 1 argument, got %d", isnullFuncName, len(params))
	}
	name, ok := params[0].(string)
	if !ok {
		return nil, fmt.Errorf("%s expects a field name, got %T", isnullFuncName, params[0])
	}
	if !c.fields[name] {
		return nil, fmt.Errorf("%s: unknown field %q", isnullFuncName, name)
	}
	return c.nullMask[name], nil
}

// isnullIdentifierPatcher rewrites isnull(field) into isnull("field") so
// an author can name the field bare, the way every other reference in a
// constraint reads, instead of having to remember that this one argument
// is a name and not a value. It fires only for a single-argument call of
// isnull whose argument is an identifier the Spec actually declared, so
// isnull(nosuchfield) still reaches expr's own unknown-name error rather
// than being silently turned into a string literal.
type isnullIdentifierPatcher struct{ fields map[string]bool }

func (p isnullIdentifierPatcher) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	if !ok || callee.Value != isnullFuncName {
		return
	}
	if len(call.Arguments) != 1 {
		return
	}
	ident, ok := call.Arguments[0].(*ast.IdentifierNode)
	if !ok || !p.fields[ident.Value] {
		return
	}
	lit := &ast.StringNode{Value: ident.Value}
	lit.SetLocation(ident.Location())
	call.Arguments[0] = lit
}

// evaluate returns true when every compiled constraint accepts the row.
// nullMask is the row's null state, made visible to expressions through
// the isnull builtin; it may be nil when no field is nullable.
// A non-nil error is returned only for runtime evaluation failures
// (typically a programming bug — constraints are compile-time validated).
func (c *compiledConstraints) evaluate(row map[string]any, nullMask map[string]bool) (bool, error) {
	c.nullMask = nullMask
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
// concrete value does not matter — only its kind, and the kind must be
// the one the ROW actually carries or every expression touching the
// field fails at run time with "invalid operation: T(U)".
//
// The mapping is derived from fieldTypeFromName rather than from a
// second hand-maintained list of type-name strings, because the two
// drifted: the switch this replaced typed packed_bool as a Go bool while
// bernoulliSampler.next emits 1.0 / 0.0, so every constraint over a
// boolean field — 90 of 122 fields on the motivating survey cohort —
// errored at run time (issue #258). It also carried arms for
// nullable_bool / nullable_u4 / nullable_u8 / nullable_u16 /
// nullable_decimal128, none of which fieldTypeFromName can build, so no
// spec naming them ever reaches the writer; they are gone.
//
// Row shapes, per synth/distributions.go: every scalar arrives as
// float64 (including packed_bool, date and decimal128), a categorical
// as a string, a set as a map[string]bool.
// TestSentinelFor_MatchesDrawnRowValueForEveryDeclarableType asserts
// that against a value actually drawn from each type's sampler, so a
// field type added to fieldTypeFromName cannot acquire a wrong env type
// silently.
func sentinelFor(typeName string) any {
	ft, ok := fieldTypeFromName(typeName)
	if !ok {
		return float64(0)
	}
	switch {
	case ft.IsCategorical():
		return ""
	case ft.IsSet():
		return map[string]bool{}
	default:
		return float64(0)
	}
}
