// Package extscalar is the pre-resolution pass of an embedder extension
// operator's expression-capable params: a number param whose
// registration sets pulse.ParamMeta.Expression takes a JSON number or a
// scalar-expression string (internal/scalarslot). Resolve rewrites every
// such expression in a request's slots to the number it evaluates to,
// BEFORE the extension's own factory decodes its params, so the
// embedder's decode only ever sees a plain number — exactly as
// internal/windowscalar does for the built-in window params.
//
// Every slot the request params check walks is covered, in its order
// (descriptor.ParamsRefusal): filterers, aggregations, attributes,
// groups, windows, features, tests, post_tests, matrices (and a
// MAT_DISTANCE params.between grouper), regressions, then the crosstab
// rows, columns, cell / cells and margin_aggregations. Built-in
// operators are never touched (a built-in's expression params are
// internal/windowscalar's).
//
// Predict and the runtime run the same pass. Predict (a static
// judgement: scalarslot.Env.Declared(), run false) resolves an
// expression over literals and params and refuses a bad one; one calling
// a request-bound function — coef(), sweep_axis(), lookup(),
// range_label(), an embedder function — is checked but not run and is
// reported Unresolved. The runtime (the request's bound models, run
// true) resolves every expression.
//
// No-execute leaf: types, errors, paramread, exprenv and the scalar-slot
// evaluator only (TestExtScalar_ImportBoundary), so internal/descriptor
// may import it.
package extscalar

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/types"
)

// Flagged returns the expression-capable params keys of extension
// operator op in category cat (paramread.Test for both test tiers); nil
// when it has none, when op is a built-in, or when the instance hides
// or does not register it.
type Flagged func(cat paramread.Category, op string) []string

// The extension scalar-param reasons (PULSE_REQUEST_PARAM_INVALID
// details.reason): "param_" + the scalar slot's slot-neutral reason —
// the built-in window params' own reason set.
const (
	ReasonParamInvalid    = "param_" + scalarslot.ReasonInvalid
	ReasonParamNotNumeric = "param_" + scalarslot.ReasonNotNumeric
	ReasonParamNotFinite  = "param_" + scalarslot.ReasonNotFinite
	ReasonParamNotInteger = "param_" + scalarslot.ReasonNotInteger
)

// Unresolved is one flagged param a static judgement could not compute.
type Unresolved struct {
	// Path locates the param: "<slot>.params.<key>"
	// (aggregations[0].params.scale).
	Path string
	// Expression is the param's expression as written.
	Expression string
	// Reads are the request-bound functions it calls, sorted.
	Reads []string
}

// Result is a request after the pass.
type Result struct {
	// Request is the request the factories see: req itself when no
	// flagged param carries an expression, else a copy whose slots'
	// params carry the resolved numbers (a static judgement leaves an
	// unresolved or refused expression as written). The caller's
	// request is never mutated.
	Request *types.Request
	// Unresolved are the params a static judgement left to the runtime,
	// in walk order.
	Unresolved []Unresolved
	// Refusals are every refused param, in walk order: the runtime
	// raises the first, predict reports each.
	Refusals []*errors.CodedError
}

// Resolve runs the pass over every slot of req whose extension operator
// flags an expression-capable param (flagged) carrying a string. A nil
// flagged, or a request with no such param, comes back as itself.
func Resolve(req *types.Request, flagged Flagged, env EnvFunc, run bool) Result {
	if req == nil || flagged == nil {
		return Result{Request: req}
	}
	r := &resolver{flagged: flagged, envFn: env, run: run}
	out := r.request(req)
	return Result{Request: out, Unresolved: r.unresolved, Refusals: r.refusals}
}

// ResolveFilterers is Resolve over a filterer list alone (a facet's),
// each located at prefix[i].
func ResolveFilterers(prefix string, fs []*types.Filterer, flagged Flagged, env EnvFunc, run bool) ([]*types.Filterer, []Unresolved, []*errors.CodedError) {
	if flagged == nil {
		return fs, nil, nil
	}
	r := &resolver{flagged: flagged, envFn: env, run: run}
	out := r.filterers(prefix, fs)
	return out, r.unresolved, r.refusals
}

// EnvFunc builds the environment an expression is judged against and
// the bound it runs over (the runtime's implementations, or
// env.Declared() for a static judgement). It is called at most once,
// on the first expression met, so a request without one builds nothing.
type EnvFunc func() (scalarslot.Env, exprenv.Bound)

type resolver struct {
	flagged    Flagged
	envFn      EnvFunc
	built      bool
	env        scalarslot.Env
	bound      exprenv.Bound
	run        bool
	unresolved []Unresolved
	refusals   []*errors.CodedError
}

func slotPath(prefix string, i int) string {
	if i < 0 {
		return prefix
	}
	return prefix + "[" + strconv.Itoa(i) + "]"
}

// request walks req's slots in the params check's order and returns
// req, or a copy carrying every rewritten slot.
func (r *resolver) request(req *types.Request) *types.Request {
	var c *types.Request
	cp := func() *types.Request {
		if c == nil {
			v := *req
			c = &v
		}
		return c
	}
	if fs := r.filterers("filterers", req.Filterers); !sameSlice(fs, req.Filterers) {
		cp().Filterers = fs
	}
	if as := r.aggregations("aggregations", req.Aggregations); !sameSlice(as, req.Aggregations) {
		cp().Aggregations = as
	}
	if as := resolveList(r, "attributes", paramread.Attribute, req.Attributes,
		func(a *types.Attribute) (string, json.RawMessage) { return string(a.Type), a.Params },
		func(a *types.Attribute, p json.RawMessage) { a.Params = p }); !sameSlice(as, req.Attributes) {
		cp().Attributes = as
	}
	if gs := r.groups("groups", req.Groups); !sameSlice(gs, req.Groups) {
		cp().Groups = gs
	}
	if ws := resolveList(r, "windows", paramread.Window, req.Windows,
		func(w *types.Window) (string, json.RawMessage) { return string(w.Type), w.Params },
		func(w *types.Window, p json.RawMessage) { w.Params = p }); !sameSlice(ws, req.Windows) {
		cp().Windows = ws
	}
	if fs := resolveList(r, "features", paramread.Feature, req.Features,
		func(f *types.Feature) (string, json.RawMessage) { return string(f.Type), f.Params },
		func(f *types.Feature, p json.RawMessage) { f.Params = p }); !sameSlice(fs, req.Features) {
		cp().Features = fs
	}
	if ts := r.tests("tests", req.Tests); !sameSlice(ts, req.Tests) {
		cp().Tests = ts
	}
	if ts := r.tests("post_tests", req.PostTests); !sameSlice(ts, req.PostTests) {
		cp().PostTests = ts
	}
	if ms := r.matrices(req.Matrices); ms != nil {
		cp().Matrices = ms
	}
	if rs := resolveList(r, "regressions", paramread.Regression, req.Regressions,
		func(s *types.RegressionSpec) (string, json.RawMessage) { return string(s.Type), s.Params },
		func(s *types.RegressionSpec, p json.RawMessage) { s.Params = p }); !sameSlice(rs, req.Regressions) {
		cp().Regressions = rs
	}
	if ct := r.crosstab(req.Crosstab); ct != req.Crosstab {
		cp().Crosstab = ct
	}
	if c == nil {
		return req
	}
	return c
}

func (r *resolver) filterers(prefix string, fs []*types.Filterer) []*types.Filterer {
	return resolveList(r, prefix, paramread.Filterer, fs,
		func(f *types.Filterer) (string, json.RawMessage) { return string(f.Type), f.Params },
		func(f *types.Filterer, p json.RawMessage) { f.Params = p })
}

func (r *resolver) aggregations(prefix string, as []*types.Aggregation) []*types.Aggregation {
	return resolveList(r, prefix, paramread.Aggregator, as,
		func(a *types.Aggregation) (string, json.RawMessage) { return string(a.Type), a.Params },
		func(a *types.Aggregation, p json.RawMessage) { a.Params = p })
}

func (r *resolver) groups(prefix string, gs []*types.Group) []*types.Group {
	return resolveList(r, prefix, paramread.Grouper, gs,
		func(g *types.Group) (string, json.RawMessage) { return string(g.Type), g.Params },
		func(g *types.Group, p json.RawMessage) { g.Params = p })
}

// tests walks a test list; an extension test of either tier is
// registered under paramread.Test.
func (r *resolver) tests(prefix string, ts []*types.Test) []*types.Test {
	return resolveList(r, prefix, paramread.Test, ts,
		func(t *types.Test) (string, json.RawMessage) { return string(t.Type), t.Params },
		func(t *types.Test, p json.RawMessage) { t.Params = p })
}

// matrices walks the matrix specs (their own params, then a
// MAT_DISTANCE params.between grouper's); nil when none changed.
func (r *resolver) matrices(ms []types.MatrixSpec) []types.MatrixSpec {
	var out []types.MatrixSpec
	for i := range ms {
		path := slotPath("matrices", i)
		raw, changed := r.params(path, paramread.Matrix, string(ms[i].Type), ms[i].Params)
		if between, ok := r.between(path, ms[i].Type, raw); ok {
			raw, changed = between, true
		}
		if !changed {
			continue
		}
		if out == nil {
			out = slices.Clone(ms)
		}
		out[i].Params = raw
	}
	return out
}

// between resolves a MAT_DISTANCE params.between grouper's flagged
// params, returning the matrix params rewritten (false: nothing to
// rewrite). The grouper is read loosely: a malformed between is the
// matrix's own decode to refuse.
func (r *resolver) between(path string, t types.MatrixType, raw json.RawMessage) (json.RawMessage, bool) {
	if t != types.MAT_DISTANCE || len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil || len(m["between"]) == 0 {
		return nil, false
	}
	var g map[string]json.RawMessage
	if json.Unmarshal(m["between"], &g) != nil || g == nil {
		return nil, false
	}
	var op string
	if json.Unmarshal(g["type"], &op) != nil || op == "" {
		return nil, false
	}
	p, changed := r.params(path+".params.between", paramread.Grouper, op, g["params"])
	if !changed {
		return nil, false
	}
	g["params"] = p
	gb, err := json.Marshal(g)
	if err != nil {
		return nil, false
	}
	m["between"] = gb
	out, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}
	return out, true
}

func (r *resolver) crosstab(ct *types.CrosstabSpec) *types.CrosstabSpec {
	if ct == nil {
		return nil
	}
	var c *types.CrosstabSpec
	cp := func() *types.CrosstabSpec {
		if c == nil {
			v := *ct
			c = &v
		}
		return c
	}
	if gs := r.groups("crosstab.rows", ct.Rows); !sameSlice(gs, ct.Rows) {
		cp().Rows = gs
	}
	if gs := r.groups("crosstab.columns", ct.Columns); !sameSlice(gs, ct.Columns) {
		cp().Columns = gs
	}
	if ct.HasMeasures() {
		if as := r.aggregations("crosstab.cells", ct.Cells); !sameSlice(as, ct.Cells) {
			cp().Cells = as
		}
	} else if ct.Cell != nil {
		if p, ok := r.params("crosstab.cell", paramread.Aggregator, string(ct.Cell.Type), ct.Cell.Params); ok {
			a := *ct.Cell
			a.Params = p
			cp().Cell = &a
		}
	}
	if as := r.aggregations("crosstab.margin_aggregations", ct.MarginAggregations); !sameSlice(as, ct.MarginAggregations) {
		cp().MarginAggregations = as
	}
	if c == nil {
		return ct
	}
	return c
}

// resolveList resolves each slot of list, copying the list and the
// slot on its first rewrite; list itself when none changed.
func resolveList[T any](r *resolver, prefix string, cat paramread.Category, list []*T,
	get func(*T) (string, json.RawMessage), set func(*T, json.RawMessage)) []*T {
	var out []*T
	for i, s := range list {
		if s == nil {
			continue
		}
		op, raw := get(s)
		p, ok := r.params(slotPath(prefix, i), cat, op, raw)
		if !ok {
			continue
		}
		if out == nil {
			out = slices.Clone(list)
		}
		cp := *s
		set(&cp, p)
		out[i] = &cp
	}
	if out == nil {
		return list
	}
	return out
}

// sameSlice reports whether a and b are the same slice (resolveList
// hands back its input when nothing changed).
func sameSlice[T any](a, b []*T) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// params resolves one slot's params object: every flagged key carrying
// a string is evaluated, and a resolved one written as its number. It
// returns the rewritten object and true when any key was rewritten.
func (r *resolver) params(path string, cat paramread.Category, op string, raw json.RawMessage) (json.RawMessage, bool) {
	if op == "" || len(bytes.TrimSpace(raw)) == 0 {
		return raw, false
	}
	keys := r.flagged(cat, op)
	if len(keys) == 0 {
		return raw, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return raw, false
	}
	changed := false
	for _, k := range keys {
		src, ok := exprAt(m, k)
		if !ok {
			continue
		}
		at := path + ".params." + k
		if !r.built {
			r.env, r.bound = r.envFn()
			r.built = true
		}
		val, ref := scalarslot.Eval(src, scalarslot.Real, r.env, r.bound, r.run)
		switch {
		case ref != nil:
			r.refusals = append(r.refusals, refusal(at, op, k, src, ref))
		case !val.Resolved:
			r.unresolved = append(r.unresolved, Unresolved{Path: at, Expression: src, Reads: val.Reads})
		default:
			m[k] = Number(val.Number)
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	out, err := json.Marshal(m)
	if err != nil {
		// A map of raw messages each already valid JSON always
		// marshals; keep the slot as written for the factory to judge.
		return raw, false
	}
	return out, true
}

// refusal locates a scalar-slot refusal at the param's path: a reason
// refusal is PULSE_REQUEST_PARAM_INVALID reason param_<reason>; a
// shared code (PULSE_PARAMS_*, PULSE_MODEL_*, a table code, a failed
// fit's own code) keeps its code, located.
func refusal(path, op, key, src string, r *scalarslot.Refusal) *errors.CodedError {
	if r.Coded != nil {
		d := make(map[string]any, len(r.Coded.Details)+3)
		for k, v := range r.Coded.Details {
			d[k] = v
		}
		d["path"], d["operator"], d["param"] = path, op, key
		out := errors.NewCodedErrorWithDetails(r.Coded.Code,
			"operator "+op+" param "+strconv.Quote(key)+" at "+path+": "+r.Coded.Message, d)
		out.Cause = r.Coded.Cause
		return out
	}
	d := make(map[string]any, len(r.Details)+5)
	for k, v := range r.Details {
		d[k] = v
	}
	d["path"], d["operator"], d["param"] = path, op, key
	d["reason"], d["expression"] = "param_"+r.Reason, src
	return errors.NewCodedErrorWithDetails(errors.PULSE_REQUEST_PARAM_INVALID,
		"operator "+op+" param "+strconv.Quote(key)+" at "+path+" "+strconv.Quote(src)+" "+r.What, d)
}

// Number is v as the JSON literal the extension's factory decodes: an
// integral value of magnitude below 2^53 as a plain integer (so a
// factory decoding the param into an int field reads it), else the
// shortest decimal that round-trips v.
func Number(v float64) json.RawMessage {
	if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
		return json.RawMessage(strconv.FormatInt(int64(v), 10))
	}
	return json.RawMessage(strconv.FormatFloat(v, 'g', -1, 64))
}

// exprAt is m[key] when it is a JSON string.
func exprAt(m map[string]json.RawMessage, key string) (string, bool) {
	v := bytes.TrimSpace(m[key])
	if len(v) == 0 || v[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", false
	}
	return s, true
}
