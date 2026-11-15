// Package optgrad is the forward-mode (dual-number) gradient of a
// Compose `optimize` block's objective and constraints: the exact
// ∇expr at a decision point, fed to optimize.Minimize in place of
// central finite differences.
//
// It walks the CHECKED expr-lang tree a block compiled to
// (vm.Program.Node(): patched — params folded to constants, `**` / `^`
// and float `%` rewritten into their builtins — type-checked and
// constant-folded), the very tree the VM program was generated from, so
// it differentiates exactly what the solver evaluates. Each node yields
// a dual: its value and, when it depends on a decision variable, the
// tangent ∂node/∂xⱼ for every variable (nil when constant, so a
// derivative rule's partial is never read for an argument that does not
// move). Riding the vm.Program itself was the alternative: expr-lang's
// operator overloading needs the variables declared as a non-float64
// type, which the typed built-in signatures (float64 arguments) and the
// block's numeric-result check refuse — a second, differently-typed
// compile of every block. The tree walk needs no second compile, and
// predict (no-execute) judges the same tree.
//
// Coverage: number / bool / string / nil literals and folded constants,
// the decision variables, unary - + ! not, binary + - * / and the
// comparisons (== != < > <= >=), and / or / && / || / ??, the ternary,
// the built-ins abs ceil floor round float min max, and every function
// with a [Rule] — the deterministic maths set (sqrt log exp pow log1p,
// sin cos tan atan2, pi e) and the rewritten `**` / `^` / `%`. Anything
// else — a function without a rule (hill, coef, response, result,
// lookup, an embedder ExprFunction), a collection, a member access, a
// let — is Unsupported, and the block falls back to finite differences
// as a whole (never per term). A caller adds rules through
// [Options.Rules] (the fit readers' and an embedder's derivative hook
// arrive that way).
//
// Determinism: every product feeding an add is barriered
// (float64(a*b)), every tangent sum runs in argument order, and the
// transcendental values and partials come from linalg/detmath (through
// internal/vecmath, the kernels the expression built-ins use), so the
// same point gives the same gradient bits on every architecture.
//
// No-execute leaf: it imports expr-lang's ast / builtin packages,
// internal/exprenv and internal/vecmath only, so predict
// (internal/descriptor, via internal/optblock) and the solver
// (internal/optsolve) share it.
package optgrad

import (
	stderrors "errors"
	"math"
	"slices"

	exprast "github.com/expr-lang/expr/ast"
	exprbuiltin "github.com/expr-lang/expr/builtin"

	"github.com/frankbardon/pulse/internal/exprenv"
)

// Rule is one function's derivative rule.
type Rule struct {
	// Impl computes the function's value from its evaluated arguments
	// (float64, string, bool or nil each); nil uses the expression
	// built-in of the same name (exprenv.Impl), so the value is the
	// VM's, bit for bit.
	Impl func(args ...any) (any, error)
	// Partials are ∂f/∂argᵢ at the arguments (args[i] NaN where the
	// argument is not a number) given the value out, index-aligned with
	// the arguments. Only the partials of arguments that depend on a
	// decision variable are read.
	Partials func(args []float64, out float64) []float64
}

// Options extends the built-in rule table.
type Options struct {
	// Rules are extra rules by function name; one of the same name as a
	// built-in rule replaces it.
	Rules map[string]Rule
}

// Program is one expression ready to differentiate.
type Program struct {
	root  exprast.Node
	vars  map[string]int
	n     int
	rules map[string]Rule
}

// Compile prepares the checked tree node over the decision variables
// vars (block order). unsupported names every construct without a
// derivative rule, sorted and unique; when it is non-empty the program
// is nil.
func Compile(node exprast.Node, vars []string, opts Options) (prog *Program, unsupported []string) {
	rules := ruleTable(opts)
	idx := make(map[string]int, len(vars))
	for j, v := range vars {
		idx[v] = j
	}
	seen := map[string]bool{}
	walk(node, idx, rules, seen)
	if len(seen) > 0 {
		for k := range seen {
			unsupported = append(unsupported, k)
		}
		slices.Sort(unsupported)
		return nil, unsupported
	}
	return &Program{root: node, vars: idx, n: len(vars), rules: rules}, nil
}

// Unsupported is Compile's unsupported list alone.
func Unsupported(node exprast.Node, vars []string, opts Options) []string {
	_, u := Compile(node, vars, opts)
	return u
}

// ErrEval marks an evaluation the dual walk cannot carry out (an
// operand of the wrong kind, a function's own error): the caller
// re-evaluates the VM program to surface the real fault.
var ErrEval = stderrors.New("optgrad: evaluation fault")

// Eval is the expression's value and gradient at x (len(x) = the
// variable count). A nil result is NaN with an all-NaN gradient (the
// VM's nil reads as NaN); a constant result has an all-zero gradient.
func (p *Program) Eval(x []float64) (float64, []float64, error) {
	e := &evaluator{p: p, x: x}
	r, err := e.eval(p.root)
	if err != nil {
		return 0, nil, err
	}
	g := make([]float64, p.n)
	switch r.k {
	case kindNil:
		for j := range g {
			g[j] = math.NaN()
		}
		return math.NaN(), g, nil
	case kindNum:
		copy(g, r.d)
		return r.v, g, nil
	}
	return 0, nil, ErrEval
}

// kind is a dual's value kind.
type kind uint8

const (
	kindNum kind = iota
	kindBool
	kindStr
	kindNil
)

// dual is one node's value and, for a number depending on a variable,
// its tangent d (nil when constant).
type dual struct {
	k kind
	v float64
	d []float64
	b bool
	s string
}

func num(v float64) dual { return dual{k: kindNum, v: v} }

// walk records in seen every construct under node without a rule.
func walk(node exprast.Node, vars map[string]int, rules map[string]Rule, seen map[string]bool) {
	switch n := node.(type) {
	case *exprast.NilNode, *exprast.IntegerNode, *exprast.FloatNode, *exprast.BoolNode, *exprast.StringNode:
	case *exprast.ConstantNode:
		if _, ok := constant(n.Value); !ok {
			seen[typeName(n.Value)] = true
		}
	case *exprast.IdentifierNode:
		if _, ok := vars[n.Value]; !ok {
			seen["identifier "+n.Value] = true
		}
	case *exprast.UnaryNode:
		switch n.Operator {
		case "-", "+", "!", "not":
		default:
			seen["operator "+n.Operator] = true
		}
		walk(n.Node, vars, rules, seen)
	case *exprast.BinaryNode:
		if !binarySupported(n.Operator) {
			seen["operator "+n.Operator] = true
		}
		walk(n.Left, vars, rules, seen)
		walk(n.Right, vars, rules, seen)
	case *exprast.ConditionalNode:
		walk(n.Cond, vars, rules, seen)
		walk(n.Exp1, vars, rules, seen)
		walk(n.Exp2, vars, rules, seen)
	case *exprast.CallNode:
		id, ok := n.Callee.(*exprast.IdentifierNode)
		if !ok {
			seen["call"] = true
		} else if _, ok := rules[id.Value]; !ok {
			seen[displayName(id.Value)] = true
		}
		for _, a := range n.Arguments {
			walk(a, vars, rules, seen)
		}
	case *exprast.BuiltinNode:
		if _, ok := builtinRules[n.Name]; !ok {
			seen[n.Name] = true
		}
		for _, a := range n.Arguments {
			walk(a, vars, rules, seen)
		}
	default:
		seen[nodeName(node)] = true
	}
}

// displayName is the name a rewritten operator's builtin is reported
// under: the operator a block wrote.
func displayName(name string) string {
	switch name {
	case exprenv.PowFuncName:
		return "operator **"
	case exprenv.PowCaretFuncName:
		return "operator ^"
	case exprenv.ModFuncName:
		return "operator %"
	}
	return name
}

// nodeName names a construct the walk does not differentiate.
func nodeName(node exprast.Node) string {
	switch node.(type) {
	case *exprast.ArrayNode:
		return "array"
	case *exprast.MapNode, *exprast.PairNode:
		return "map"
	case *exprast.MemberNode, *exprast.ChainNode:
		return "member access"
	case *exprast.SliceNode:
		return "slice"
	case *exprast.PredicateNode, *exprast.PointerNode:
		return "predicate"
	case *exprast.VariableDeclaratorNode, *exprast.SequenceNode:
		return "let"
	case *exprast.BytesNode:
		return "bytes"
	}
	return "expression"
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "nil"
	case []any:
		return "array"
	case map[string]any:
		return "map"
	}
	return "constant"
}

// constant is a folded constant as a dual (false for a collection).
func constant(v any) (dual, bool) {
	switch c := v.(type) {
	case nil:
		return dual{k: kindNil}, true
	case bool:
		return dual{k: kindBool, b: c}, true
	case string:
		return dual{k: kindStr, s: c}, true
	}
	if f, ok := toFloat(v); ok {
		return num(f), true
	}
	return dual{}, false
}

// toFloat converts a numeric value to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

func binarySupported(op string) bool {
	switch op {
	case "+", "-", "*", "/", "%", "==", "!=", "<", ">", "<=", ">=", "and", "&&", "or", "||", "??":
		return true
	}
	return false
}

// evaluator is one Eval's state.
type evaluator struct {
	p *Program
	x []float64
}

func (e *evaluator) eval(node exprast.Node) (dual, error) {
	switch n := node.(type) {
	case *exprast.NilNode:
		return dual{k: kindNil}, nil
	case *exprast.IntegerNode:
		return num(float64(n.Value)), nil
	case *exprast.FloatNode:
		return num(n.Value), nil
	case *exprast.BoolNode:
		return dual{k: kindBool, b: n.Value}, nil
	case *exprast.StringNode:
		return dual{k: kindStr, s: n.Value}, nil
	case *exprast.ConstantNode:
		if c, ok := constant(n.Value); ok {
			return c, nil
		}
	case *exprast.IdentifierNode:
		if j, ok := e.p.vars[n.Value]; ok {
			d := make([]float64, e.p.n)
			d[j] = 1
			return dual{k: kindNum, v: e.x[j], d: d}, nil
		}
	case *exprast.UnaryNode:
		return e.unary(n)
	case *exprast.BinaryNode:
		return e.binary(n)
	case *exprast.ConditionalNode:
		c, err := e.eval(n.Cond)
		if err != nil {
			return dual{}, err
		}
		if c.k != kindBool {
			return dual{}, ErrEval
		}
		if c.b {
			return e.eval(n.Exp1)
		}
		return e.eval(n.Exp2)
	case *exprast.CallNode:
		id, ok := n.Callee.(*exprast.IdentifierNode)
		if !ok {
			break
		}
		r, ok := e.p.rules[id.Value]
		if !ok {
			break
		}
		args, err := e.args(n.Arguments)
		if err != nil {
			return dual{}, err
		}
		impl := r.Impl
		if impl == nil {
			impl = exprenv.Impl(id.Value)
		}
		if impl == nil {
			break
		}
		return e.apply(impl, r.Partials, args)
	case *exprast.BuiltinNode:
		r, ok := builtinRules[n.Name]
		if !ok {
			break
		}
		args, err := e.args(n.Arguments)
		if err != nil {
			return dual{}, err
		}
		return e.apply(builtinImpl(n.Name), r, args)
	}
	return dual{}, ErrEval
}

func (e *evaluator) args(nodes []exprast.Node) ([]dual, error) {
	out := make([]dual, len(nodes))
	for i, a := range nodes {
		v, err := e.eval(a)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// apply calls impl on args and chains partials over the arguments that
// depend on a variable, in argument order.
func (e *evaluator) apply(impl func(args ...any) (any, error), partials func([]float64, float64) []float64, args []dual) (dual, error) {
	raw := make([]any, len(args))
	nums := make([]float64, len(args))
	dep := false
	for i, a := range args {
		nums[i] = math.NaN()
		switch a.k {
		case kindNum:
			raw[i], nums[i] = a.v, a.v
			dep = dep || a.d != nil
		case kindBool:
			raw[i] = a.b
		case kindStr:
			raw[i] = a.s
		}
	}
	out, err := impl(raw...)
	if err != nil {
		return dual{}, ErrEval
	}
	if out == nil {
		return dual{k: kindNil}, nil
	}
	v, ok := toFloat(out)
	if !ok {
		c, ok := constant(out)
		if !ok {
			return dual{}, ErrEval
		}
		return c, nil
	}
	r := num(v)
	if !dep {
		return r, nil
	}
	p := partials(nums, v)
	r.d = make([]float64, e.p.n)
	for i, a := range args {
		if a.k != kindNum || a.d == nil {
			continue
		}
		addScaled(r.d, p[i], a.d)
	}
	return r, nil
}

// addScaled is dst += c·src, each product barriered, in index order.
func addScaled(dst []float64, c float64, src []float64) {
	for j := range dst {
		dst[j] += float64(c * src[j])
	}
}

func (e *evaluator) unary(n *exprast.UnaryNode) (dual, error) {
	a, err := e.eval(n.Node)
	if err != nil {
		return dual{}, err
	}
	switch n.Operator {
	case "!", "not":
		if a.k != kindBool {
			return dual{}, ErrEval
		}
		return dual{k: kindBool, b: !a.b}, nil
	}
	if a.k != kindNum {
		return dual{}, ErrEval
	}
	if n.Operator == "+" {
		return a, nil
	}
	r := num(-a.v)
	if a.d != nil {
		r.d = make([]float64, len(a.d))
		for j, v := range a.d {
			r.d[j] = -v
		}
	}
	return r, nil
}

func (e *evaluator) binary(n *exprast.BinaryNode) (dual, error) {
	switch n.Operator {
	case "and", "&&", "or", "||":
		return e.logical(n)
	case "??":
		l, err := e.eval(n.Left)
		if err != nil {
			return dual{}, err
		}
		if l.k != kindNil {
			return l, nil
		}
		return e.eval(n.Right)
	}
	l, err := e.eval(n.Left)
	if err != nil {
		return dual{}, err
	}
	r, err := e.eval(n.Right)
	if err != nil {
		return dual{}, err
	}
	switch n.Operator {
	case "==":
		return dual{k: kindBool, b: equal(l, r)}, nil
	case "!=":
		return dual{k: kindBool, b: !equal(l, r)}, nil
	}
	if l.k != kindNum || r.k != kindNum {
		return dual{}, ErrEval
	}
	switch n.Operator {
	case "<":
		return dual{k: kindBool, b: l.v < r.v}, nil
	case ">":
		return dual{k: kindBool, b: l.v > r.v}, nil
	case "<=":
		return dual{k: kindBool, b: l.v <= r.v}, nil
	case ">=":
		return dual{k: kindBool, b: l.v >= r.v}, nil
	case "+":
		return linear(l, r, 1), nil
	case "-":
		return linear(l, r, -1), nil
	case "*":
		return product(l, r), nil
	case "/":
		return quotient(l, r), nil
	case "%":
		// An int % int the `%` patcher left to expr-lang: both operands
		// constant integers (a variable is float64 and rewritten).
		if l.d != nil || r.d != nil || r.v == 0 {
			return dual{}, ErrEval
		}
		return num(float64(int64(l.v) % int64(r.v))), nil
	}
	return dual{}, ErrEval
}

// logical is and / or with expr-lang's short circuit.
func (e *evaluator) logical(n *exprast.BinaryNode) (dual, error) {
	l, err := e.eval(n.Left)
	if err != nil {
		return dual{}, err
	}
	if l.k != kindBool {
		return dual{}, ErrEval
	}
	and := n.Operator == "and" || n.Operator == "&&"
	if and && !l.b {
		return l, nil
	}
	if !and && l.b {
		return l, nil
	}
	r, err := e.eval(n.Right)
	if err != nil {
		return dual{}, err
	}
	if r.k != kindBool {
		return dual{}, ErrEval
	}
	return r, nil
}

// equal is expr-lang's == over the dual kinds: numbers by value, the
// rest by kind and value.
func equal(a, b dual) bool {
	if a.k != b.k {
		return false
	}
	switch a.k {
	case kindNum:
		return a.v == b.v
	case kindBool:
		return a.b == b.b
	case kindStr:
		return a.s == b.s
	}
	return true
}

// linear is l + sign·r.
func linear(l, r dual, sign float64) dual {
	out := num(l.v + float64(sign*r.v))
	if l.d == nil && r.d == nil {
		return out
	}
	out.d = make([]float64, max(len(l.d), len(r.d)))
	if l.d != nil {
		copy(out.d, l.d)
	}
	if r.d != nil {
		addScaled(out.d, sign, r.d)
	}
	return out
}

// product is l·r: d = r·dl + l·dr.
func product(l, r dual) dual {
	out := num(float64(l.v * r.v))
	if l.d == nil && r.d == nil {
		return out
	}
	out.d = make([]float64, max(len(l.d), len(r.d)))
	if l.d != nil {
		addScaled(out.d, r.v, l.d)
	}
	if r.d != nil {
		addScaled(out.d, l.v, r.d)
	}
	return out
}

// quotient is l/r: d = (dl − q·dr) / r.
func quotient(l, r dual) dual {
	q := l.v / r.v
	out := num(q)
	if l.d == nil && r.d == nil {
		return out
	}
	n := max(len(l.d), len(r.d))
	out.d = make([]float64, n)
	for j := 0; j < n; j++ {
		var a, b float64
		if l.d != nil {
			a = l.d[j]
		}
		if r.d != nil {
			b = float64(q * r.d[j])
		}
		out.d[j] = (a - b) / r.v
	}
	return out
}

// builtinImpl is expr-lang's own built-in name as a func.
func builtinImpl(name string) func(args ...any) (any, error) {
	fn := exprbuiltin.Builtins[exprbuiltin.Index[name]]
	return func(args ...any) (out any, err error) {
		defer func() {
			if r := recover(); r != nil {
				out, err = nil, ErrEval
			}
		}()
		if fn.Fast != nil && len(args) == 1 {
			return fn.Fast(args[0]), nil
		}
		if fn.Func != nil {
			return fn.Func(args...)
		}
		return nil, ErrEval
	}
}
