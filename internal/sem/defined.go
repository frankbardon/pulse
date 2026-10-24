package sem

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"gonum.org/v1/gonum/mat"
)

// defined.go is lavaan's defined parameters: a model line
//
//	name := expr
//
// names a function of the model's labelled parameters (labels, numeric
// constants, + - * / ^, parentheses, sqrt / exp / log / abs, and the
// names of earlier := lines). It changes no fit: after the fit each
// definition is evaluated at the estimate with forward-mode dual
// numbers over its expression, so its gradient with respect to the free
// parameters is exact, and its standard error is the delta method's
// √(gᵀVg), V the free parameters' covariance (the inverse expected
// information the standard errors read); z, p and the 95% Wald interval
// follow as for a free parameter — lavaan's parameterEstimates rows
// with op ":=". A definition switches the fit to lavaan's conventions
// (Model.Lavaan).
//
// A label names one value wherever it is used: a fixed labelled
// parameter (a labelled marker) enters as its constant, a per-group
// label c(a1, a2)*x as its group's parameter. A definition whose value
// is not finite at the estimate (a division by zero, the log of a
// non-positive value) has every figure NaN.
//
// abs' derivative is its sign (0 at 0): lavaan differentiates a :=
// line by complex step, which reads abs' derivative as 0, so a
// definition through abs() has lavaan's estimate but not its SE.

// OpDefined is a defined parameter's estimate-row operator: lavaan's
// ":=". The row's LVal is the name, RVal the expression without
// whitespace (lavaan's rhs).
const OpDefined = ":="

// Defined-parameter refusal reasons (DefinedError.Reason).
const (
	DefinedReasonSyntax       = "syntax"
	DefinedReasonUnknownLabel = "unknown_label"
	DefinedReasonForwardRef   = "forward_reference"
	DefinedReasonDuplicate    = "duplicate"
)

// DefinedError is a refused := line: Name the defined parameter (""
// when the line has none), Reason one of the DefinedReason* values and
// Ref the offending reference (unknown_label, forward_reference).
type DefinedError struct {
	Name, Reason, Ref, Msg string
}

func (e *DefinedError) Error() string { return e.Msg }

// ConstraintError is a refused lavaan constraint line (==, <, >): only
// equality through shared labels is supported. Op is the operator.
type ConstraintError struct {
	Op, Line string
}

func (e *ConstraintError) Error() string {
	return fmt.Sprintf("the constraint %q (lavaan %s) is not supported: equality constraints are written with a shared label (a*x1 + a*x2), and inequality or nonlinear constraints are out of scope", e.Line, e.Op)
}

// definedParam is one parsed := line.
type definedParam struct {
	name, rhs string
	expr      exprNode
}

// isDefinedLine reports a := line.
func isDefinedLine(line string) bool { return strings.Contains(line, OpDefined) }

// ptrnConstraint is a lavaan constraint line: lhs == rhs, lhs < rhs or
// lhs > rhs, neither side holding another operator character.
var ptrnConstraint = regexp.MustCompile(`^[^=<>~]+(==|<|>)[^=<>~]+$`)

// constraintOp is a lavaan constraint line's operator ("" none).
func constraintOp(line string) string {
	if g := ptrnConstraint.FindStringSubmatch(line); g != nil {
		return g[1]
	}
	return ""
}

// parseDefinedLine reads "name := expr".
func parseDefinedLine(line string) (*definedParam, error) {
	lhs, rhs, _ := strings.Cut(line, OpDefined)
	name := strings.TrimSpace(lhs)
	if !ptrnLabel.MatchString(name) {
		return nil, &DefinedError{Name: name, Reason: DefinedReasonSyntax,
			Msg: fmt.Sprintf("defined parameter %q: the left of := is one name (a letter or _, then letters, digits, _ or .)", name)}
	}
	p := &exprParser{src: rhs}
	if err := p.tokenize(); err != nil {
		return nil, &DefinedError{Name: name, Reason: DefinedReasonSyntax, Msg: "defined parameter " + name + ": " + err.Error()}
	}
	expr, err := p.parse()
	if err != nil {
		return nil, &DefinedError{Name: name, Reason: DefinedReasonSyntax, Msg: "defined parameter " + name + ": " + err.Error()}
	}
	return &definedParam{name: name, rhs: strings.Join(strings.Fields(rhs), ""), expr: expr}, nil
}

// checkDefined validates the definitions against the description's
// labels (every level's): each reference names a label or an earlier
// definition, each name is new.
func checkDefined(defs []*definedParam, labels stringSet) error {
	seen := stringSet{}
	all := stringSet{}
	for _, d := range defs {
		all.add(d.name)
	}
	for _, d := range defs {
		if seen.has(d.name) || labels.has(d.name) {
			return &DefinedError{Name: d.name, Reason: DefinedReasonDuplicate,
				Msg: fmt.Sprintf("defined parameter %q is already a label or a defined parameter of the model; give it a new name", d.name)}
		}
		var bad *DefinedError
		d.expr.refs(func(r string) {
			if bad != nil || labels.has(r) || seen.has(r) {
				return
			}
			if all.has(r) {
				bad = &DefinedError{Name: d.name, Reason: DefinedReasonForwardRef, Ref: r,
					Msg: fmt.Sprintf("defined parameter %q reads %q, which is defined on a later line or by itself; a := line reads only labels and the defined parameters above it (no cycles)", d.name, r)}
				return
			}
			bad = &DefinedError{Name: d.name, Reason: DefinedReasonUnknownLabel, Ref: r,
				Msg: fmt.Sprintf("defined parameter %q reads %q, which is no label of the model; label the parameter in its statement (%s*x)", d.name, r, r)}
		})
		if bad != nil {
			return bad
		}
		seen.add(d.name)
	}
	return nil
}

// labels returns every parameter label the description writes, at
// every level of its per-group modifiers.
func (e *Effects) labels() stringSet {
	out := stringSet{}
	add := func(mu Mult) {
		if mu.Label != "" && !mu.isStart() {
			out.add(mu.Label)
		}
	}
	for _, op := range e.ops {
		l := e.byOp[op]
		for _, lv := range l.keys {
			r := l.m[lv]
			for _, rv := range r.keys {
				mu := r.m[rv]
				add(mu)
				for _, g := range mu.Groups {
					add(g)
				}
			}
		}
	}
	return out
}

// HasDefined reports whether a description parses and carries any :=
// line — a defined parameter, so lavaan conventions.
func HasDefined(desc string) bool {
	e, err := Parse(desc)
	return err == nil && len(e.defs) > 0
}

// DefinedCount is the model's := lines: the estimate rows a fit appends
// after the parameter table.
func (m *Model) DefinedCount() int { return len(m.defined) }

// SpecifiedDefined lists the model's defined-parameter rows with every
// figure NaN (SpecifiedRows' := half).
func SpecifiedDefined(m *Model) []Estimate {
	out := make([]Estimate, len(m.defined))
	for i, d := range m.defined {
		out[i] = definedRow(d)
		out[i].Value, out[i].StdValue = math.NaN(), math.NaN()
	}
	return out
}

func definedRow(d *definedParam) Estimate {
	nan := math.NaN()
	return Estimate{LVal: d.name, Op: OpDefined, RVal: d.rhs, Param: d.name, Label: d.name,
		SE: nan, Z: nan, P: nan, CILower: nan, CIUpper: nan}
}

// labelValue is a label's value at the estimate: a free parameter's
// index into the free vector (free >= 0), else its fixed value; std is
// what the standardized solution reads for it — the first row's
// std_estimate of a free one, the value of a fixed one (lavaan's
// def.function over std.all).
type labelValue struct {
	free       int
	value, std float64
}

// evalDefined evaluates every definition at the free vector x with
// covariance vcov (nil: no standard errors). A label the fit lacks (a
// per-group label of a level without rows) is NaN.
func evalDefined(defs []*definedParam, labels map[string]labelValue, x []float64, vcov *mat.SymDense) []Estimate {
	if len(defs) == 0 {
		return nil
	}
	k := len(x)
	vals := map[string]dual{}
	stds := map[string]float64{}
	nan := dual{v: math.NaN()}
	env := func(name string) dual {
		if d, ok := vals[name]; ok {
			return d
		}
		lv, ok := labels[name]
		if !ok {
			return nan
		}
		if lv.free < 0 {
			return dual{v: lv.value}
		}
		g := make([]float64, k)
		g[lv.free] = 1
		return dual{v: x[lv.free], d: g}
	}
	stdEnv := func(name string) dual {
		if v, ok := stds[name]; ok {
			return dual{v: v}
		}
		if lv, ok := labels[name]; ok {
			return dual{v: lv.std}
		}
		return nan
	}
	out := make([]Estimate, len(defs))
	for i, d := range defs {
		e := definedRow(d)
		v := d.expr.eval(env, k)
		vals[d.name] = v
		stds[d.name] = d.expr.eval(stdEnv, 0).v
		e.Value, e.StdValue = v.v, stds[d.name]
		if !isFinite(e.Value) || !finiteAll(v.d) {
			e.Value, e.StdValue = math.NaN(), math.NaN()
			out[i] = e
			continue
		}
		if !isFinite(e.StdValue) {
			e.StdValue = math.NaN()
		}
		if vcov != nil {
			e.SE = math.Sqrt(quadDense(vcov, v.d))
			e.Z = e.Value / e.SE
			e.P = 2 * (1 - normCDF(math.Abs(e.Z)))
			e.CILower, e.CIUpper = e.Value-waldZ975*e.SE, e.Value+waldZ975*e.SE
		}
		out[i] = e
	}
	return out
}

// quadDense is gᵀVg over a gradient that may be nil (a constant: 0).
func quadDense(v *mat.SymDense, g []float64) float64 {
	if g == nil {
		return 0
	}
	return quad(v, g, g)
}

func isFinite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

func finiteAll(xs []float64) bool {
	for _, x := range xs {
		if !isFinite(x) {
			return false
		}
	}
	return true
}

// dual is a forward-mode dual number: a value and its gradient with
// respect to the free parameters (nil: a constant, gradient 0).
type dual struct {
	v float64
	d []float64
}

// combine returns a·x.d + b·y.d (nil when both are constants).
func combine(a float64, x dual, b float64, y dual) []float64 {
	if x.d == nil && y.d == nil {
		return nil
	}
	n := max(len(x.d), len(y.d))
	out := make([]float64, n)
	for i := range out {
		if x.d != nil {
			out[i] += a * x.d[i]
		}
		if y.d != nil {
			out[i] += b * y.d[i]
		}
	}
	return out
}

func scale(a float64, x dual) []float64 { return combine(a, x, 0, dual{}) }

// exprNode is a parsed := expression.
type exprNode interface {
	eval(env func(string) dual, k int) dual
	refs(visit func(string))
}

type numNode struct{ v float64 }

func (n numNode) eval(func(string) dual, int) dual { return dual{v: n.v} }
func (numNode) refs(func(string))                  {}

type refNode struct{ name string }

func (n refNode) eval(env func(string) dual, _ int) dual { return env(n.name) }
func (n refNode) refs(visit func(string))                { visit(n.name) }

type negNode struct{ x exprNode }

func (n negNode) eval(env func(string) dual, k int) dual {
	x := n.x.eval(env, k)
	return dual{v: -x.v, d: scale(-1, x)}
}
func (n negNode) refs(visit func(string)) { n.x.refs(visit) }

type binNode struct {
	op   byte
	a, b exprNode
}

func (n binNode) eval(env func(string) dual, k int) dual {
	x, y := n.a.eval(env, k), n.b.eval(env, k)
	switch n.op {
	case '+':
		return dual{v: x.v + y.v, d: combine(1, x, 1, y)}
	case '-':
		return dual{v: x.v - y.v, d: combine(1, x, -1, y)}
	case '*':
		return dual{v: x.v * y.v, d: combine(y.v, x, x.v, y)}
	case '/':
		return dual{v: x.v / y.v, d: combine(1/y.v, x, -x.v/(y.v*y.v), y)}
	}
	// '^': d(x^y) = y·x^(y−1)·dx + x^y·ln(x)·dy (the second term only
	// when the exponent varies, so a constant power of a negative base
	// stays defined).
	v := math.Pow(x.v, y.v)
	var b float64
	if y.d != nil {
		b = v * math.Log(x.v)
	}
	return dual{v: v, d: combine(y.v*math.Pow(x.v, y.v-1), x, b, y)}
}

func (n binNode) refs(visit func(string)) { n.a.refs(visit); n.b.refs(visit) }

type callNode struct {
	fn string
	x  exprNode
}

// definedFuncs are the functions a := expression may call.
var definedFuncs = []string{"sqrt", "exp", "log", "abs"}

func (n callNode) eval(env func(string) dual, k int) dual {
	x := n.x.eval(env, k)
	switch n.fn {
	case "sqrt":
		v := math.Sqrt(x.v)
		return dual{v: v, d: scale(0.5/v, x)}
	case "exp":
		v := math.Exp(x.v)
		return dual{v: v, d: scale(v, x)}
	case "log":
		return dual{v: math.Log(x.v), d: scale(1/x.v, x)}
	}
	// abs: the derivative is the sign (0 at 0).
	s := 0.0
	switch {
	case x.v > 0:
		s = 1
	case x.v < 0:
		s = -1
	case math.IsNaN(x.v):
		s = math.NaN()
	}
	return dual{v: math.Abs(x.v), d: scale(s, x)}
}

func (n callNode) refs(visit func(string)) { n.x.refs(visit) }

// exprParser is a recursive-descent parser over R's arithmetic
// precedence: unary minus binds looser than ^ (-a^2 is -(a^2)), ^ is
// right-associative and its exponent may carry a sign (a^-1).
//
//	expr  := term (('+' | '-') term)*
//	term  := unary (('*' | '/') unary)*
//	unary := ('-' | '+') unary | power
//	power := atom ('^' unary)?
//	atom  := number | name | func '(' expr ')' | '(' expr ')'
type exprParser struct {
	src  string
	toks []exprToken
	pos  int
}

type exprToken struct {
	kind byte // 'n' number, 'i' name, else the operator / paren itself
	text string
	num  float64
}

func (p *exprParser) tokenize() error {
	s := p.src
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case strings.IndexByte("+-*/^()", c) >= 0:
			p.toks = append(p.toks, exprToken{kind: c, text: string(c)})
			i++
		case c >= '0' && c <= '9' || c == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9':
			j := i
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}
			if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
				k := j + 1
				if k < len(s) && (s[k] == '+' || s[k] == '-') {
					k++
				}
				if k < len(s) && s[k] >= '0' && s[k] <= '9' {
					for k < len(s) && s[k] >= '0' && s[k] <= '9' {
						k++
					}
					j = k
				}
			}
			v, err := strconv.ParseFloat(s[i:j], 64)
			if err != nil {
				return fmt.Errorf("%q is not a number", s[i:j])
			}
			p.toks = append(p.toks, exprToken{kind: 'n', text: s[i:j], num: v})
			i = j
		case c == '_' || c == '.' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z':
			j := i + 1
			for j < len(s) && (s[j] == '_' || s[j] == '.' || s[j] >= '0' && s[j] <= '9' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= 'a' && s[j] <= 'z') {
				j++
			}
			p.toks = append(p.toks, exprToken{kind: 'i', text: s[i:j]})
			i = j
		default:
			return fmt.Errorf("%q is not part of a := expression (labels, numbers, + - * / ^, parentheses, %s)", string(c), strings.Join(definedFuncs, " / "))
		}
	}
	return nil
}

func (p *exprParser) peek() byte {
	if p.pos < len(p.toks) {
		return p.toks[p.pos].kind
	}
	return 0
}

func (p *exprParser) parse() (exprNode, error) {
	if len(p.toks) == 0 {
		return nil, fmt.Errorf("the right of := is empty")
	}
	n, err := p.expr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("unexpected %q", p.toks[p.pos].text)
	}
	return n, nil
}

func (p *exprParser) expr() (exprNode, error) {
	a, err := p.term()
	for err == nil && (p.peek() == '+' || p.peek() == '-') {
		op := p.toks[p.pos].kind
		p.pos++
		var b exprNode
		if b, err = p.term(); err == nil {
			a = binNode{op: op, a: a, b: b}
		}
	}
	return a, err
}

func (p *exprParser) term() (exprNode, error) {
	a, err := p.unary()
	for err == nil && (p.peek() == '*' || p.peek() == '/') {
		op := p.toks[p.pos].kind
		p.pos++
		var b exprNode
		if b, err = p.unary(); err == nil {
			a = binNode{op: op, a: a, b: b}
		}
	}
	return a, err
}

func (p *exprParser) unary() (exprNode, error) {
	switch p.peek() {
	case '-':
		p.pos++
		x, err := p.unary()
		return negNode{x: x}, err
	case '+':
		p.pos++
		return p.unary()
	}
	return p.power()
}

func (p *exprParser) power() (exprNode, error) {
	a, err := p.atom()
	if err != nil || p.peek() != '^' {
		return a, err
	}
	p.pos++
	b, err := p.unary()
	return binNode{op: '^', a: a, b: b}, err
}

func (p *exprParser) atom() (exprNode, error) {
	if p.pos >= len(p.toks) {
		return nil, fmt.Errorf("the expression ends early")
	}
	t := p.toks[p.pos]
	p.pos++
	switch t.kind {
	case 'n':
		return numNode{v: t.num}, nil
	case 'i':
		if p.peek() != '(' {
			return refNode{name: t.text}, nil
		}
		known := false
		for _, f := range definedFuncs {
			known = known || f == t.text
		}
		if !known {
			return nil, fmt.Errorf("function %s is not supported (%s)", t.text, strings.Join(definedFuncs, ", "))
		}
		p.pos++
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, fmt.Errorf("%s( is not closed", t.text)
		}
		p.pos++
		return callNode{fn: t.text, x: x}, nil
	case '(':
		x, err := p.expr()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, fmt.Errorf("a ( is not closed")
		}
		p.pos++
		return x, nil
	}
	return nil, fmt.Errorf("unexpected %q", t.text)
}
