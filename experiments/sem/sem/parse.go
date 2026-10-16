// Package sem is an experimental structural equation modelling (SEM)
// estimator packaged as a Pulse extension MAT_* matrix operator.
//
// It reproduces semopy's default covariance-structure model
// (semopy.Model, obj="MLW", mimic_lavaan=False): the same variable
// classification, the same parameter set and fixings, the same Wishart
// maximum-likelihood objective, the same expected-information standard
// errors and the same fit statistics, so a fit over the same data gives
// the same numbers.
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
)

// Mult is the multiplier written before a right-hand variable: none
// (a free parameter), a fixed number ("0.5*x") or a parameter label
// ("a*x", equality across every place the label is used).
type Mult struct {
	Set   bool
	Fixed bool
	Value float64
	Label string
}

// Effects maps operator -> left variable -> right variable -> multiplier,
// keeping first-seen order at every level (semopy iterates dicts in
// insertion order, which fixes its parameter order).
type Effects struct {
	ops  []string
	byOp map[string]*lhsMap
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

var (
	ptrnEffect = regexp.MustCompile(`^(\w[\w.]*(?:\s*,\s*[\w.]*)*)\s*(=~|~~|~)\s*(-?\w[\w.-]*(?:\s*\*\s*\w[\w.]*)?(?:\s*\+\s*-?\w[\w.-]*(?:\s*\*\s*\w[\w.]*)?)*)$`)
	ptrnRValue = regexp.MustCompile(`^((-?\w[\w.-]*\*)?\w[\w.]*)$`)
)

// Parse reads a model description in semopy / lavaan syntax: one
// statement per line, `#` comments, `=~` (measurement), `~`
// (regression) and `~~` (covariance), comma-separated left sides,
// `+`-separated right sides and optional `mult*` prefixes. semopy's
// operation commands (START, BOUND, DEFINE, ...) are not supported.
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
		g := ptrnEffect.FindStringSubmatch(line)
		if g == nil {
			return nil, fmt.Errorf("model line %d: unsupported syntax %q", lineNo+1, line)
		}
		lefts, op, rights := g[1], g[2], g[3]
		rv, err := parseRValues(rights)
		if err != nil {
			return nil, fmt.Errorf("model line %d: %w", lineNo+1, err)
		}
		for _, left := range strings.Split(lefts, ",") {
			left = strings.TrimSpace(left)
			if left == "" {
				return nil, fmt.Errorf("model line %d: empty left-hand variable", lineNo+1)
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
			m.Set = true
			if f, err := strconv.ParseFloat(mult, 64); err == nil {
				m.Fixed, m.Value = true, f
			} else {
				m.Label = mult
			}
		}
		out.set(name, m)
	}
	return out, nil
}
