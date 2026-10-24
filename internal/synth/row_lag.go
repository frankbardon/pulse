package synth

import (
	"fmt"
	"maps"
	"sort"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
)

// lag(name, n[, default]) — the synth row environment's one cross-row
// read (U58).
//
// It returns the FINAL value of declared field `name` on the n-th
// previous ACCEPTED row: the value the row map held after the rule pass
// (the value a same-row expression would have read off that row, before
// encodeRow rounds an integer field or narrows an f32), typed the way
// the field reads in an expression — float64 for every scalar, string
// for a categorical, map[string]bool for a set, []float64 for a vector.
// While fewer than n rows have been accepted it returns `default`, or
// null when no default is given; a lagged row whose field was null also
// returns null (SQL LAG's split: the default stands in for a row that
// does not exist, never for a null that does). An unguarded null in
// arithmetic is a loud row-time fault, never a silent 0 — write a
// default or `?? x`.
//
// # Why the arguments are literals, checked at parse
//
// `name` must be a string literal naming a declared field and `n` an
// integer literal from 1 to maxLagOffset. Both are what sizes the state
// (lagBuffer keeps only the lagged fields, and only max n rows of them),
// so both must be known before the first row; a computed name or offset
// would need every field of every earlier row kept. The refusals are
// PULSE_SYNTH_LAG_FIELD_INVALID and PULSE_SYNTH_LAG_OFFSET_INVALID, raised
// by rowExprWalker, so they fire wherever the other parse-settled
// refusals do: validateRules, validateSpec's constraint walk, and again
// at compileRules / compileConstraints for a Spec that bypassed
// validation.
//
// # Determinism
//
// It consumes no RNG: it reads a buffer generate commits AFTER a row is
// accepted and encoded (beside ruleApplier.commitRow), so a rejected
// attempt never enters it and a spec that does not call lag carries no
// buffer at all — byte-identical to output from before lag existed.
// Self-lag (`ad: spend + 0.6 * lag("ad", 1, 0)`) is well defined: the
// previous row was complete before this one started.
//
// # Typing
//
// expr-lang types a function once per name, but lag's return type is
// the TARGET field's. lagPatcher therefore rewrites every well-formed
// call, before the type check, onto one of four internal names
// (lagFuncNames) whose signatures carry that class, so validateRuleExpr
// sees `lag("ad", 1, 0)` as float64 and the coercion matrix's parse-time
// half keeps working. The internal names contain a ':' the lexer cannot
// produce, so no author can call them; the bare name `lag` itself is NOT
// registered, so a call the patcher did not rewrite (an invalid shape
// the walker refuses first on every spec path) cannot compile anywhere
// — the --suggest-rules detectors, which only compile, accept exactly
// what the loader accepts.

// lagFuncName is the expression-visible name.
const lagFuncName = "lag"

// maxLagOffset is the largest n lag accepts. lagBuffer holds max-n rows
// of each lagged field, so the cap bounds that state at 1024 values per
// lagged field — four years of weekly rows, nearly three of daily ones —
// far beyond any adstock or AR horizon, while a typo of 10000000 is
// refused rather than allocated.
const maxLagOffset = 1024

// lagClass is the row-shape class of a lagged field.
type lagClass int

const (
	lagNumber lagClass = iota
	lagCategory
	lagSet
	lagVector
)

// lagFuncNames are the internal, per-class function names lagPatcher
// rewrites a call onto.
var lagFuncNames = [...]string{
	lagNumber:   "lag:number",
	lagCategory: "lag:category",
	lagSet:      "lag:set",
	lagVector:   "lag:vector",
}

// lagClassOf derives the class from the env sentinel (sentinelFor), the
// one place a field's row shape is decided.
func lagClassOf(sentinel any) lagClass {
	switch sentinel.(type) {
	case string:
		return lagCategory
	case map[string]bool:
		return lagSet
	case []float64:
		return lagVector
	default:
		return lagNumber
	}
}

// lagFunctionOptions registers the four typed internal functions on host.
// The default's parameter is typed for number and category, so
// `lag("region", 1, 0)` is a compile refusal; set and vector defaults are
// `any` because expr cannot write a map[string]bool or []float64 literal
// (an option list `["a"]` or `[0.5, 1]` is converted at run time).
func lagFunctionOptions(host *compiledConstraints) []expr.Option {
	return []expr.Option{
		expr.Function(lagFuncNames[lagNumber], host.lagBuiltin,
			new(func(string, int) float64), new(func(string, int, float64) float64)),
		expr.Function(lagFuncNames[lagCategory], host.lagBuiltin,
			new(func(string, int) string), new(func(string, int, string) string)),
		expr.Function(lagFuncNames[lagSet], host.lagBuiltin,
			new(func(string, int) map[string]bool), new(func(string, int, any) map[string]bool)),
		expr.Function(lagFuncNames[lagVector], host.lagBuiltin,
			new(func(string, int) []float64), new(func(string, int, any) []float64)),
	}
}

// lagCallShape reports whether call is a well-formed lag call over the
// declared fields: 2 or 3 arguments, a string-literal declared field and
// an integer literal in range. A non-nil refusal says why not.
func lagCallShape(call *ast.CallNode, declared map[string]bool) (field string, n int, refusal *rowExprRefusal) {
	if len(call.Arguments) != 2 && len(call.Arguments) != 3 {
		return "", 0, &rowExprRefusal{function: lagFuncName, msg: fmt.Sprintf(
			"lag takes (name, n) or (name, n, default), got %d argument(s)", len(call.Arguments))}
	}
	lit, ok := call.Arguments[0].(*ast.StringNode)
	if !ok {
		return "", 0, &rowExprRefusal{code: errors.PULSE_SYNTH_LAG_FIELD_INVALID, function: lagFuncName, msg: "lag's first argument must be a string literal naming a declared field, e.g. lag(\"spend\", 1)"}
	}
	if !declared[lit.Value] {
		return "", 0, &rowExprRefusal{code: errors.PULSE_SYNTH_LAG_FIELD_INVALID, function: lagFuncName, field: lit.Value, msg: fmt.Sprintf(
			"lag names field %q, which the spec does not declare", lit.Value)}
	}
	num, ok := call.Arguments[1].(*ast.IntegerNode)
	if !ok || num.Value < 1 || num.Value > maxLagOffset {
		return "", 0, &rowExprRefusal{code: errors.PULSE_SYNTH_LAG_OFFSET_INVALID, function: lagFuncName, field: lit.Value, msg: fmt.Sprintf(
			"lag(%q, n): n must be an integer literal from 1 to %d", lit.Value, maxLagOffset)}
	}
	return lit.Value, num.Value, nil
}

// lagPatcher rewrites each well-formed lag call onto its field's typed
// internal name. A call it leaves alone keeps the unregistered name and
// fails to compile.
type lagPatcher struct {
	classes  map[string]lagClass
	declared map[string]bool
}

func (p lagPatcher) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	// A declared field named `lag` shadows the built-in (the walker
	// refuses the call); leaving it alone keeps that refusal's shape.
	if !ok || callee.Value != lagFuncName || p.declared[lagFuncName] {
		return
	}
	field, _, refusal := lagCallShape(call, p.declared)
	if refusal != nil {
		return
	}
	callee.Value = lagFuncNames[p.classes[field]]
}

// lagPatcherFor builds the patcher from the env sentinels.
func lagPatcherFor(env map[string]any, names map[string]bool) lagPatcher {
	classes := make(map[string]lagClass, len(env))
	for name, sentinel := range env {
		classes[name] = lagClassOf(sentinel)
	}
	return lagPatcher{classes: classes, declared: names}
}

// lagBuiltin answers every lag call compiled against host. The patcher
// and the walker guarantee (string literal, int literal in range); the
// checks below are the backstop for a Spec that reached the row loop
// some other way.
func (c *compiledConstraints) lagBuiltin(params ...any) (any, error) {
	if len(params) != 2 && len(params) != 3 {
		return nil, fmt.Errorf("%s expects 2 or 3 arguments, got %d", lagFuncName, len(params))
	}
	name, ok := params[0].(string)
	if !ok {
		return nil, fmt.Errorf("%s expects a field name, got %T", lagFuncName, params[0])
	}
	n, ok := params[1].(int)
	if !ok {
		return nil, fmt.Errorf("%s expects an integer offset, got %T", lagFuncName, params[1])
	}
	if c.lag == nil {
		return nil, fmt.Errorf("%s(%q, %d): no lag state for this expression", lagFuncName, name, n)
	}
	v, rowExists, err := c.lag.read(name, n)
	if err != nil {
		return nil, err
	}
	if rowExists {
		return v, nil
	}
	if len(params) == 2 {
		return nil, nil
	}
	return c.lag.defaultValue(name, params[2])
}

// lagBuffer is the accept-only cross-row state: for each lagged field a
// ring of the last `size` accepted rows' final values and null bits.
// One per generate() call, shared by the constraint host and the rule
// host; nil when no expression calls lag.
type lagBuffer struct {
	size   int
	fields []string // sorted; commit order (no output depends on it)
	slot   map[string]int
	class  []lagClass
	vals   [][]any
	nulls  [][]bool
	head   int // next write position
	count  int // accepted rows committed so far
}

// newLagBuffer sizes the buffer from every well-formed lag call in the
// spec's rules and constraints. Returns nil when there is none.
func newLagBuffer(s *Spec, wfs []*writerField) *lagBuffer {
	declared := make(map[string]bool, len(wfs))
	classes := make(map[string]lagClass, len(wfs))
	for _, wf := range wfs {
		declared[wf.spec.Name] = true
		classes[wf.spec.Name] = lagClassOf(sentinelFor(wf.spec.Type))
	}
	maxN := 0
	lagged := map[string]bool{}
	for _, src := range rowExprSources(s) {
		for field, n := range lagCalls(src, declared) {
			lagged[field] = true
			if n > maxN {
				maxN = n
			}
		}
	}
	if len(lagged) == 0 {
		return nil
	}
	b := &lagBuffer{size: maxN, slot: make(map[string]int, len(lagged))}
	for name := range lagged {
		b.fields = append(b.fields, name)
	}
	sort.Strings(b.fields)
	for i, name := range b.fields {
		b.slot[name] = i
		b.class = append(b.class, classes[name])
		b.vals = append(b.vals, make([]any, maxN))
		b.nulls = append(b.nulls, make([]bool, maxN))
	}
	return b
}

// rowExprSources lists every expression compiled against the row
// environment: each rule's `when` and `set_expr` values, then each
// constraint.
func rowExprSources(s *Spec) []string {
	var out []string
	for _, r := range s.Rules {
		if r.When != "" {
			out = append(out, r.When)
		}
		for _, name := range sortedKeys(r.SetExpr) {
			out = append(out, r.SetExpr[name])
		}
	}
	for _, c := range s.Constraints {
		out = append(out, c.Expr)
	}
	return out
}

// lagCalls returns each well-formed lag call in src as field -> largest n.
func lagCalls(src string, declared map[string]bool) map[string]int {
	tree, err := parser.Parse(src)
	if err != nil || tree == nil || tree.Node == nil {
		return nil
	}
	c := &lagCallCollector{declared: declared, out: map[string]int{}}
	ast.Walk(&tree.Node, c)
	return c.out
}

type lagCallCollector struct {
	declared map[string]bool
	out      map[string]int
}

func (c *lagCallCollector) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	if !ok || callee.Value != lagFuncName || c.declared[lagFuncName] {
		return
	}
	field, n, refusal := lagCallShape(call, c.declared)
	if refusal != nil {
		return
	}
	if n > c.out[field] {
		c.out[field] = n
	}
}

// commit records the just-ACCEPTED row. A set's map and a vector's
// slice are copied: the next row may reuse them. nil-safe.
func (b *lagBuffer) commit(row map[string]any, nullMask map[string]bool) {
	if b == nil {
		return
	}
	for i, name := range b.fields {
		v := row[name]
		switch t := v.(type) {
		case map[string]bool:
			v = maps.Clone(t)
		case []float64:
			v = append([]float64(nil), t...)
		}
		b.vals[i][b.head] = v
		b.nulls[i][b.head] = nullMask[name]
	}
	b.head = (b.head + 1) % b.size
	if b.count < b.size {
		b.count++
	}
}

// read returns the n-th previous accepted row's value of name, or
// rowExists=false while fewer than n rows have been accepted. A null
// field reads as nil.
func (b *lagBuffer) read(name string, n int) (v any, rowExists bool, err error) {
	i, ok := b.slot[name]
	if !ok || n < 1 || n > b.size {
		return nil, false, fmt.Errorf("%s(%q, %d): not a lag the spec declared", lagFuncName, name, n)
	}
	if n > b.count {
		return nil, false, nil
	}
	pos := (b.head - n + b.size) % b.size
	if b.nulls[i][pos] {
		return nil, true, nil
	}
	return b.vals[i][pos], true, nil
}

// defaultValue converts a default onto the field's row shape: an integer
// to float64 for a scalar (an int-typed expression such as row() reaches
// here unconverted), an option list to a set selection, a number list to
// a vector. A nil default is null.
func (b *lagBuffer) defaultValue(name string, d any) (any, error) {
	if d == nil {
		return nil, nil
	}
	switch b.class[b.slot[name]] {
	case lagNumber:
		switch t := d.(type) {
		case float64:
			return t, nil
		case int:
			return float64(t), nil
		}
	case lagCategory:
		if s, ok := d.(string); ok {
			return s, nil
		}
	case lagSet:
		switch t := d.(type) {
		case map[string]bool:
			return maps.Clone(t), nil
		case []any:
			sel := make(map[string]bool, len(t))
			for _, e := range t {
				s, ok := e.(string)
				if !ok {
					return nil, fmt.Errorf("%s(%q): a set default must list option names, got %T", lagFuncName, name, e)
				}
				sel[s] = true
			}
			return sel, nil
		}
	case lagVector:
		switch t := d.(type) {
		case []float64:
			return append([]float64(nil), t...), nil
		case []any:
			out := make([]float64, len(t))
			for i, e := range t {
				switch x := e.(type) {
				case float64:
					out[i] = x
				case int:
					out[i] = float64(x)
				default:
					return nil, fmt.Errorf("%s(%q): a vector default must list numbers, got %T", lagFuncName, name, e)
				}
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("%s(%q): default %v (%T) does not fit the field", lagFuncName, name, d, d)
}
