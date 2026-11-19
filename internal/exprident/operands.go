package exprident

import (
	"slices"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"
)

// The operand kinds NullableOperands reports: what a nil read meets.
const (
	// OperandArithmetic: an operand of + - * / % ** ^ (string `+` too).
	OperandArithmetic = "arithmetic"
	// OperandCompare: an operand of an ordering compare < <= > >=.
	OperandCompare = "compare"
	// OperandUnaryMinus: the operand of a unary minus.
	OperandUnaryMinus = "unary_minus"
	// OperandIndex: the value indexed or sliced by a non-optional
	// `v[i]` / `v.name` / `v[a:b]`, or a slice bound.
	OperandIndex = "index"
	// OperandFunction: an argument of a built-in function at a position
	// that rejects nil (NilRejectingArgument).
	OperandFunction = "function"
)

// OperandRead is one nullable name reaching a nil-rejecting operand:
// the name and the kind of its first unguarded such use.
type OperandRead struct {
	Field string
	Kind  string
}

// NullableOperands reports every name nullable reports true for that
// can reach, as nil, an operand that raises an evaluation error on nil
// — in first-use order, each with the kind of its first such use;
// parsed=false when the expression does not parse.
//
// These are the operands expr-lang's untyped null-row program rejects
// (pinned by running it): arithmetic, ordering compares, unary minus,
// a non-optional index / member / slice, and the argument positions of
// the built-ins NilRejectingArgument names. `==`, `!=`, `in`,
// `matches`, `contains`, `startsWith`, `endsWith`, `??`, unary `+` and
// `?.` take nil and never report. An operand's own nil reading is
// NullableResult's (a ternary, `??`, a let, `$env.name`), so
// `(x ?? 0) + 1` and `(x != nil ? x : 0) * 2` are silent.
//
// Guards: a ternary condition's nil checks guard its branches (see
// NullableResult); `a && b` evaluates b only when a held, so a's
// non-nil proofs guard b, and `a || b` evaluates b only when a failed,
// so a's failed nil checks guard b. A name a guard proves non-nil never
// reports, a let alias of it included. total() arguments are never
// judged — a null there drops the row from the sum, it never fails the
// run — and neither is a function the table does not name (an
// embedder function, or one that takes nil). A predicate body is
// judged as if it runs (over an empty list it does not).
func NullableOperands(src string, nullable func(name string) bool) (reads []OperandRead, parsed bool) {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil, false
	}
	w := &operandWalker{nullable: nullable}
	w.walk(tree.Node, nil, nil)
	return w.reads, true
}

type operandWalker struct {
	nullable func(string) bool
	reads    []OperandRead
}

// reject records every unguarded nullable name operand's value can be
// nil through, under kind.
func (w *operandWalker) reject(operand exprast.Node, kind string, guarded map[string]bool, lets map[string]nullReading) {
	if p, ok := operand.(*exprast.PredicateNode); ok {
		operand = p.Node
	}
	for _, f := range resultReading(operand, guarded, lets, w.nullable).fields {
		if guarded[f] || slices.ContainsFunc(w.reads, func(r OperandRead) bool { return r.Field == f }) {
			continue
		}
		w.reads = append(w.reads, OperandRead{Field: f, Kind: kind})
	}
}

func (w *operandWalker) walk(node exprast.Node, guarded map[string]bool, lets map[string]nullReading) {
	switch n := node.(type) {
	case nil:
	case *exprast.UnaryNode:
		w.walk(n.Node, guarded, lets)
		if n.Operator == "-" {
			w.reject(n.Node, OperandUnaryMinus, guarded, lets)
		}
	case *exprast.BinaryNode:
		w.walk(n.Left, guarded, lets)
		switch n.Operator {
		case "&&", "and":
			whenTrue, _ := nilGuards(n.Left)
			w.walk(n.Right, withGuards(guarded, whenTrue), lets)
			return
		case "||", "or":
			_, whenFalse := nilGuards(n.Left)
			w.walk(n.Right, withGuards(guarded, whenFalse), lets)
			return
		}
		w.walk(n.Right, guarded, lets)
		var kind string
		switch n.Operator {
		case "+", "-", "*", "/", "%", "**", "^":
			kind = OperandArithmetic
		case "<", "<=", ">", ">=":
			kind = OperandCompare
		default:
			return
		}
		w.reject(n.Left, kind, guarded, lets)
		w.reject(n.Right, kind, guarded, lets)
	case *exprast.ConditionalNode:
		w.walk(n.Cond, guarded, lets)
		whenTrue, whenFalse := nilGuards(n.Cond)
		w.walk(n.Exp1, withGuards(guarded, whenTrue), lets)
		w.walk(n.Exp2, withGuards(guarded, whenFalse), lets)
	case *exprast.ChainNode:
		w.walk(n.Node, guarded, lets)
	case *exprast.MemberNode:
		if _, env := envMember(n); env {
			return
		}
		w.walk(n.Node, guarded, lets)
		w.walk(n.Property, guarded, lets)
		if !n.Optional {
			w.reject(n.Node, OperandIndex, guarded, lets)
		}
	case *exprast.SliceNode:
		w.walk(n.Node, guarded, lets)
		w.walk(n.From, guarded, lets)
		w.walk(n.To, guarded, lets)
		w.reject(n.Node, OperandIndex, guarded, lets)
		if n.From != nil {
			w.reject(n.From, OperandIndex, guarded, lets)
		}
		if n.To != nil {
			w.reject(n.To, OperandIndex, guarded, lets)
		}
	case *exprast.CallNode:
		name := ""
		if id, ok := n.Callee.(*exprast.IdentifierNode); ok {
			name = id.Value
		} else {
			w.walk(n.Callee, guarded, lets)
		}
		w.call(name, n.Arguments, guarded, lets)
	case *exprast.BuiltinNode:
		w.call(n.Name, n.Arguments, guarded, lets)
	case *exprast.PredicateNode:
		w.walk(n.Node, guarded, lets)
	case *exprast.SequenceNode:
		for _, s := range n.Nodes {
			w.walk(s, guarded, lets)
		}
	case *exprast.VariableDeclaratorNode:
		w.walk(n.Value, guarded, lets)
		inner := map[string]nullReading{}
		for k, v := range lets {
			inner[k] = v
		}
		inner[n.Name] = resultReading(n.Value, guarded, lets, w.nullable)
		shadowed := guarded
		if guarded[n.Name] {
			shadowed = withGuards(guarded, nil)
			delete(shadowed, n.Name)
		}
		w.walk(n.Expr, shadowed, inner)
	case *exprast.ArrayNode:
		for _, e := range n.Nodes {
			w.walk(e, guarded, lets)
		}
	case *exprast.MapNode:
		for _, p := range n.Pairs {
			w.walk(p, guarded, lets)
		}
	case *exprast.PairNode:
		w.walk(n.Key, guarded, lets)
		w.walk(n.Value, guarded, lets)
	}
}

// call walks a call's arguments and rejects those at a nil-rejecting
// position. total()'s argument is never judged (a null drops the row).
func (w *operandWalker) call(name string, args []exprast.Node, guarded map[string]bool, lets map[string]nullReading) {
	if name == totalFunc {
		return
	}
	for i, a := range args {
		w.walk(a, guarded, lets)
		if name != "" && NilRejectingArgument(name, len(args), i) {
			w.reject(a, OperandFunction, guarded, lets)
		}
	}
}

// totalFunc is total()'s name (internal/totalcall.Func; this leaf does
// not import it — TestNullableOperands_TotalName pins the two equal).
const totalFunc = "total"

// nilArgs is which arguments of a built-in reject nil: positions from
// through to (inclusive; to < 0 is the last), when the call passes at
// least minArgs arguments.
type nilArgs struct {
	from, to, minArgs int
}

var (
	everyArg     = nilArgs{from: 0, to: -1}
	firstArg     = nilArgs{from: 0, to: 0}
	afterFirst   = nilArgs{from: 1, to: -1}
	everyOfTwoUp = nilArgs{from: 0, to: -1, minArgs: 2}
)

// nilRejecting is the built-in functions that raise an evaluation error
// on a nil argument, and which arguments. Derived by running every
// expr-lang built-in and every Pulse expression function with a nil
// argument in each position through the untyped null-row program, and
// pinned by TestNilRejectingFunctions_MatchRunning (heavy, which
// re-derives it) and TestNilRejectingFunctions_Pinned. A name absent
// here takes nil (sin, log1p, hill, string, first, get, the set
// helpers, the calendar helpers, range_label, ...) or is not judged (an
// embedder function). lookup()'s key positions raise PULSE_LOOKUP_MISS
// (details.null_key).
var nilRejecting = map[string]nilArgs{
	// Maths.
	"abs": everyArg, "ceil": everyArg, "floor": everyArg, "round": everyArg,
	"int": everyArg, "float": everyArg,
	"sqrt": everyArg, "log": everyArg, "exp": everyArg, "pow": everyArg,
	"mean": everyArg, "median": everyArg, "sum": everyArg,
	"max": everyOfTwoUp, "min": everyOfTwoUp,
	// Strings.
	"len": everyArg, "lower": everyArg, "upper": everyArg, "trim": everyArg,
	"trimPrefix": everyArg, "trimSuffix": everyArg, "split": everyArg,
	"splitAfter": everyArg, "replace": everyArg, "repeat": everyArg,
	"indexOf": everyArg, "lastIndexOf": everyArg, "hasPrefix": everyArg,
	"hasSuffix": everyArg, "join": everyArg, "toBase64": everyArg,
	"fromBase64": everyArg, "fromJSON": everyArg, "date": everyArg,
	"duration": everyArg,
	// Lists.
	"concat": everyArg, "flatten": everyArg, "reverse": everyArg,
	"uniq": everyArg, "take": everyArg,
	"all": firstArg, "any": firstArg, "none": firstArg, "one": firstArg,
	"filter": firstArg, "map": firstArg, "count": firstArg, "find": firstArg,
	"findIndex": firstArg, "findLast": firstArg, "findLastIndex": firstArg,
	"groupBy": firstArg, "reduce": firstArg,
	"sortBy": {from: 0, to: 1},
	// Set membership against a nil candidate list.
	"has_any": afterFirst, "has_all": afterFirst, "has_none": afterFirst,
	// Vectors (a null vector field binds nil, never a NaN vector).
	"vsum": everyArg, "vmean": everyArg, "vmin": everyArg, "vmax": everyArg,
	"vsd": everyArg, "vcount": everyArg, "dot": everyArg, "norm": everyArg,
	"dist": everyArg, "cosine": everyArg, "argmax": everyArg, "argmin": everyArg,
	"vcenter": everyArg, "vzscore": everyArg, "vnormalize": everyArg,
	// Table keys.
	"lookup": afterFirst,
}

// NilRejectingArgument reports whether built-in name raises an
// evaluation error when argument pos (0-based) of an arity-argument call
// is nil.
func NilRejectingArgument(name string, arity, pos int) bool {
	r, ok := nilRejecting[name]
	if !ok || arity < r.minArgs || pos < r.from {
		return false
	}
	return r.to < 0 || pos <= r.to
}

// NilRejectingFunctions returns every function name NilRejectingArgument
// can report true for, sorted.
func NilRejectingFunctions() []string {
	out := make([]string, 0, len(nilRejecting))
	for n := range nilRejecting {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}
