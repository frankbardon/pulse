// Package sem is the structural equation model (SEM) estimator behind
// MAT_SEM: a lavaan / semopy model description fitted by maximum
// likelihood to a sample covariance matrix.
//
// Its default setup is semopy 2.3.11's default covariance-structure
// model (semopy.Model, obj="MLW", solver="SLSQP", mimic_lavaan=False):
// the same variable classification, parameter set and fixings, the same
// Wishart ML objective minimised by a port of SciPy's SLSQP (slsqp.go)
// with semopy's settings, the same expected-information standard errors
// and the same fit statistics — so a fit over the same data gives
// semopy's numbers, its early stop at ftol included. Options select a
// converging Fisher-scoring solver and the rank-2 derivative
// computation instead.
//
// A mean structure (ModelOptions.MeanStructure, or a `~ 1` statement)
// is a lavaan-only feature: the model gains intercepts and latent
// means, and the fit follows lavaan's conventions instead (Model.Lavaan:
// Fisher scoring to the optimum, unbounded variances, lavaan's
// statistics and Wald intervals), reproducing lavaan's ML fit.
//
// The package reads only a covariance matrix and a row count (and the
// sample means under a mean structure); it is
// no-execute safe (stdlib, gonum) and imported by both the resolver
// (internal/vectors: model validation at predict time) and the engine
// (internal/processing: the finalizer).
package sem

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Operators of the model syntax.
const (
	OpRegression  = "~"
	OpMeasurement = "=~"
	OpCovariance  = "~~"
	// OpIntercept is a "~ 1" term: an observed variable's intercept or
	// a latent variable's mean (lavaan's mean-structure syntax). It
	// switches the fit to lavaan conventions (Model.Lavaan).
	OpIntercept = "~1"
	// OpThreshold is an ordered variable's threshold row ("x | t1"):
	// never model syntax, only an ordinal fit's estimates (FitOrdinal).
	OpThreshold = "|"
	// OpScale is an ordered variable's scale-factor row ("x ~*~ x",
	// the delta parameterization's Δ): never model syntax, only a
	// multi-group ordinal fit's estimates (FitOrdinalGroups).
	OpScale = "~*~"
)

// Mult is the multiplier written before a right-hand variable: none
// (a free parameter), a fixed number ("0.5*x"), a parameter label
// ("a*x", equality across every place the label is used) or lavaan's
// NA ("NA*x": free, unlabelled — Set without Fixed or Label). A
// per-group modifier ("c(a1,a2)*x", "c(0.5,NA)*x") carries one of
// those per group in Groups, in level order; the model of group g
// reads Groups[g] (selectGroup).
type Mult struct {
	Set    bool
	Fixed  bool
	Value  float64
	Label  string
	Groups []Mult
	// auto marks a fixing the specification made (parameter.auto).
	auto bool
}

// Effects maps operator -> left variable -> right variable -> multiplier,
// keeping first-seen order at every level (semopy iterates dicts in
// insertion order, which fixes its parameter order).
type Effects struct {
	ops  []string
	byOp map[string]*lhsMap
	// defs are the := lines (defined parameters), in order.
	defs []*definedParam
}

type lhsMap struct {
	keys []string
	m    map[string]*rhsMap
}

type rhsMap struct {
	keys []string
	m    map[string]Mult
}

func newEffects() *Effects { return &Effects{byOp: map[string]*lhsMap{}} }

func (e *Effects) lhs(op string) *lhsMap {
	l, ok := e.byOp[op]
	if !ok {
		l = &lhsMap{m: map[string]*rhsMap{}}
		e.byOp[op] = l
		e.ops = append(e.ops, op)
	}
	return l
}

func (l *lhsMap) rhs(name string) *rhsMap {
	r, ok := l.m[name]
	if !ok {
		r = &rhsMap{m: map[string]Mult{}}
		l.m[name] = r
		l.keys = append(l.keys, name)
	}
	return r
}

func (r *rhsMap) set(name string, m Mult) {
	if _, ok := r.m[name]; !ok {
		r.keys = append(r.keys, name)
	}
	r.m[name] = m
}

func (r *rhsMap) has(name string) bool { _, ok := r.m[name]; return ok }

// rTerm is one right-hand term: an optional multiplier — a number, a
// label, NA or a per-group c(...) list — then a variable (or 1).
const rTerm = `(?:c\s*\([^()+*]*\)\s*\*\s*\w[\w.]*|-?\w[\w.-]*(?:\s*\*\s*\w[\w.]*)?)`

var (
	ptrnEffect = regexp.MustCompile(`^(\w[\w.]*(?:\s*,\s*[\w.]*)*)\s*(=~|~~|~)\s*(` + rTerm + `(?:\s*\+\s*` + rTerm + `)*)$`)
	ptrnRValue = regexp.MustCompile(`^((-?\w[\w.-]*\*|c\([^()+*]*\)\*)?\w[\w.]*)$`)
	ptrnLabel  = regexp.MustCompile(`^[A-Za-z_][\w.]*$`)
)

// Parse reads a model description in semopy / lavaan syntax: one
// statement per line, `#` comments, `=~` (measurement), `~`
// (regression) and `~~` (covariance), comma-separated left sides,
// `+`-separated right sides and optional `mult*` prefixes (a number, a
// label, NA, or lavaan's per-group list c(m1, m2, ...)). A `1` term
// on a `~` right side (`x ~ 1`, `x ~ 0*1`, `x ~ a*1`, `y ~ 1 + x`) is
// an intercept / latent mean, kept under OpIntercept. A `name := expr`
// line is a defined parameter (defined.go); a lavaan constraint line
// (==, <, >) is a *ConstraintError. semopy's operation commands (START,
// BOUND, DEFINE, ...) are not supported.
func Parse(desc string) (*Effects, error) {
	desc = strings.ReplaceAll(desc, "∼", "~")
	e := newEffects()
	n := 0
	for lineNo, line := range strings.Split(desc, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isDefinedLine(line) {
			d, err := parseDefinedLine(line)
			if err != nil {
				return nil, fmt.Errorf("model line %d: %w", lineNo+1, err)
			}
			e.defs = append(e.defs, d)
			continue
		}
		if op := constraintOp(line); op != "" {
			return nil, fmt.Errorf("model line %d: %w", lineNo+1, &ConstraintError{Op: op, Line: line})
		}
		g := ptrnEffect.FindStringSubmatch(line)
		if g == nil {
			return nil, fmt.Errorf("model line %d: unsupported syntax %q", lineNo+1, line)
		}
		lefts, op, rights := g[1], g[2], g[3]
		rv, err := parseRValues(rights)
		if err != nil {
			return nil, fmt.Errorf("model line %d: %w", lineNo+1, err)
		}
		icpt, hasIcpt := rv.m[interceptTerm]
		if hasIcpt {
			if op != OpRegression {
				return nil, fmt.Errorf("model line %d: the intercept term 1 belongs on a ~ statement, not %s", lineNo+1, op)
			}
			rv = rv.without(interceptTerm)
		}
		for _, left := range strings.Split(lefts, ",") {
			left = strings.TrimSpace(left)
			if left == "" {
				return nil, fmt.Errorf("model line %d: empty left-hand variable", lineNo+1)
			}
			if hasIcpt {
				e.lhs(OpIntercept).rhs(left).set(interceptTerm, icpt)
			}
			if len(rv.keys) == 0 {
				continue
			}
			r := e.lhs(op).rhs(left)
			for _, k := range rv.keys {
				r.set(k, rv.m[k])
			}
		}
		n++
	}
	if n == 0 {
		return nil, fmt.Errorf("model has no statements")
	}
	return e, nil
}

// interceptTerm is the right-hand "variable" of an intercept: `x ~ 1`.
const interceptTerm = "1"

// HasIntercepts reports whether a description parses and carries any
// `~ 1` statement — a mean structure, so lavaan conventions.
func HasIntercepts(desc string) bool {
	e, err := Parse(desc)
	if err != nil {
		return false
	}
	_, ok := e.byOp[OpIntercept]
	return ok
}

// HasGroupModifiers reports whether a description parses and carries
// any per-group modifier c(...) — a multi-group construct.
func HasGroupModifiers(desc string) bool {
	e, err := Parse(desc)
	if err != nil {
		return false
	}
	for _, op := range e.ops {
		l := e.byOp[op]
		for _, lv := range l.keys {
			r := l.m[lv]
			for _, rv := range r.keys {
				if len(r.m[rv].Groups) > 0 {
					return true
				}
			}
		}
	}
	return false
}

func (r *rhsMap) without(name string) *rhsMap {
	out := &rhsMap{m: map[string]Mult{}}
	for _, k := range r.keys {
		if k != name {
			out.set(k, r.m[k])
		}
	}
	return out
}

func parseRValues(tok string) (*rhsMap, error) {
	tok = strings.ReplaceAll(tok, " ", "")
	tok = strings.ReplaceAll(tok, "\t", "")
	out := &rhsMap{m: map[string]Mult{}}
	for _, t := range strings.Split(tok, "+") {
		if !ptrnRValue.MatchString(t) {
			return nil, fmt.Errorf("%q is not a valid right-hand term", t)
		}
		var m Mult
		name := t
		if i := strings.LastIndexByte(t, '*'); i >= 0 {
			mult, v := t[:i], t[i+1:]
			name = v
			if strings.HasPrefix(mult, "c(") {
				var err error
				if m, err = parseModifierList(mult, t); err != nil {
					return nil, err
				}
			} else {
				m = parseModifier(mult)
			}
		}
		out.set(name, m)
	}
	return out, nil
}

// parseModifier reads one multiplier: a number (fixed), NA (free,
// unlabelled) or a label.
func parseModifier(mult string) Mult {
	if mult == "NA" {
		return Mult{Set: true}
	}
	if f, err := strconv.ParseFloat(mult, 64); err == nil {
		return Mult{Set: true, Fixed: true, Value: f}
	}
	return Mult{Set: true, Label: mult}
}

// parseModifierList reads lavaan's per-group modifier c(m1, m2, ...):
// one number, NA or label per group.
func parseModifierList(mult, term string) (Mult, error) {
	body := strings.TrimSuffix(strings.TrimPrefix(mult, "c("), ")")
	out := Mult{Set: true}
	for _, e := range strings.Split(body, ",") {
		g := parseModifier(e)
		if e == "" || (g.Label != "" && !ptrnLabel.MatchString(e)) {
			return Mult{}, fmt.Errorf("%q: a per-group modifier c(...) lists one number, NA or label per group", term)
		}
		out.Groups = append(out.Groups, g)
	}
	return out, nil
}

// arity is the per-group modifiers' common length: 0 without any, an
// error when two lists differ (one of them cannot match the group
// count).
func (e *Effects) arity() (int, error) {
	n := 0
	for _, op := range e.ops {
		l := e.byOp[op]
		for _, lv := range l.keys {
			r := l.m[lv]
			for _, rv := range r.keys {
				k := len(r.m[rv].Groups)
				if k == 0 {
					continue
				}
				if n != 0 && k != n {
					return 0, fmt.Errorf("per-group modifiers c(...) list %d and %d values; each lists one per group", n, k)
				}
				n = k
			}
		}
	}
	return n, nil
}

// selectGroup resolves every per-group modifier to group g's entry.
func (e *Effects) selectGroup(g int) {
	for _, op := range e.ops {
		l := e.byOp[op]
		for _, lv := range l.keys {
			r := l.m[lv]
			for _, rv := range r.keys {
				if mu := r.m[rv]; len(mu.Groups) > g {
					r.m[rv] = mu.Groups[g]
				}
			}
		}
	}
}
