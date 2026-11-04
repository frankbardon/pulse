// Package windowscalar is the pre-resolution pass of a window slot's
// scalar-expression params: the real-valued built-in window params —
// WIN_DISTRIBUTED_LAG decay / shape / scale and max_lag (an exact
// integer), WIN_EWMA alpha / decay — take a JSON number or an expression
// string (internal/scalarslot). Resolve rewrites every expression in a
// window's params object to the number it evaluates to, BEFORE the
// operator's own reading (internal/windowparams over distlagparams /
// ewmaparams) runs, so the per-operator range checks are untouched and
// judge the resolved number exactly as they judge a literal.
//
// Predict and the runtime run the same pass. Predict (a static
// judgement: scalarslot.Env.Declared(), run false) resolves an
// expression over literals and params and range-checks it as a literal;
// one calling a request-bound function — coef(), sweep_axis(),
// lookup(), range_label(), an embedder function — is checked but not
// run: its place holds a stand-in number, its key is Deferred (the
// reading skips every rule that reads its value,
// windowparams.CheckDeferred) and it is reported Unresolved. The runtime
// (the request's bound models, run true) resolves every expression and
// the reading judges every value.
//
// Every other window param stays literal: WIN_LAG / WIN_LEAD offset and
// WIN_PCT_CHANGE / WIN_DELTA periods refuse a string exactly as before
// (their reading's "malformed params").
//
// No-execute leaf: types, errors, the scalar-slot evaluator and the
// window reading only (TestWindowScalar_ImportBoundary), so
// internal/descriptor may import it.
package windowscalar

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/types"
)

// Param is one scalar window param: its params key and value kind.
type Param struct {
	Key  string
	Kind scalarslot.Kind
}

// params are the scalar params per window type, in reading order.
var params = map[types.WindowType][]Param{
	types.WIN_EWMA: {
		{Key: "alpha", Kind: scalarslot.Real},
		{Key: "decay", Kind: scalarslot.Real},
	},
	types.WIN_DISTRIBUTED_LAG: {
		{Key: "decay", Kind: scalarslot.Real},
		{Key: "shape", Kind: scalarslot.Real},
		{Key: "scale", Kind: scalarslot.Real},
		{Key: "max_lag", Kind: scalarslot.Integer},
	},
}

// Params returns the scalar params of window type t, in reading order
// (nil: none — every param of t is literal). The result is a copy.
func Params(t types.WindowType) []Param {
	return append([]Param(nil), params[t]...)
}

// IsScalar reports whether key is a scalar param of window type t.
func IsScalar(t types.WindowType, key string) bool {
	for _, p := range params[t] {
		if p.Key == key {
			return true
		}
	}
	return false
}

// Expr is one scalar param written as an expression string.
type Expr struct {
	Key string
	Src string
}

// Exprs returns w's scalar params whose value is an expression string,
// in reading order; nil when none (or when params is not a JSON object:
// the reading refuses that).
func Exprs(w *types.Window) []Expr {
	if w == nil || len(params[w.Type]) == 0 {
		return nil
	}
	m, ok := object(w.Params)
	if !ok {
		return nil
	}
	var out []Expr
	for _, p := range params[w.Type] {
		if src, ok := exprAt(m, p.Key); ok {
			out = append(out, Expr{Key: p.Key, Src: src})
		}
	}
	return out
}

// Unresolved is one scalar param a static judgement could not compute.
type Unresolved struct {
	// Key is the params key (decay, max_lag, …).
	Key string
	// Expression is the param's expression as written.
	Expression string
	// Reads are the request-bound functions it calls, sorted.
	Reads []string
}

// Resolved is a window after the pass.
type Resolved struct {
	// Window is the window the reading judges: w itself when no param
	// is an expression, else a copy whose params carry the resolved
	// numbers (a stand-in for a deferred one).
	Window *types.Window
	// Unresolved are the params a static judgement left to the runtime.
	Unresolved []Unresolved
	// Deferred are the keys whose value is a stand-in — unresolved, or
	// refused here — so the reading skips every rule reading it
	// (windowparams.CheckDeferred). Nil when none.
	Deferred map[string]bool
}

// The window scalar-param reasons (PULSE_WINDOW_INVALID details.reason):
// "param_" + the scalar slot's slot-neutral reason.
const (
	ReasonParamInvalid    = "param_" + scalarslot.ReasonInvalid
	ReasonParamNotNumeric = "param_" + scalarslot.ReasonNotNumeric
	ReasonParamNotFinite  = "param_" + scalarslot.ReasonNotFinite
	ReasonParamNotInteger = "param_" + scalarslot.ReasonNotInteger
)

// Refusal is one scalar param's refusal: a reason refusal
// (PULSE_WINDOW_INVALID details.reason param_<reason>) or a shared code
// (PULSE_PARAMS_*, PULSE_MODEL_*, a table code, a failed fit's own
// code) located at the param. Message is the operator-neutral rule, as
// windowparams.Refusal's: the runtime prefixes "TYPE: ", predict
// "window[i] (TYPE): ".
type Refusal struct {
	Op      types.WindowType
	Code    errors.Code
	Message string
	Details map[string]any
	// Cause is a shared code's own cause (a failed fit's
	// model.ErrFitFailed); nil for a reason refusal.
	Cause error
}

// Coded returns the refusal as the runtime raises it: the code, the
// message prefixed with the operator name.
func (r *Refusal) Coded() *errors.CodedError {
	out := errors.NewCodedErrorWithDetails(r.Code, string(r.Op)+": "+r.Message, cloneDetails(r.Details, 0))
	out.Cause = r.Cause
	return out
}

// At returns the refusal as predict reports it for windows[i]: the
// message prefixed "window[i] (TYPE): ", details.window_index and
// details.type added.
func (r *Refusal) At(i int) *errors.CodedError {
	d := cloneDetails(r.Details, 2)
	d["window_index"] = i
	d["type"] = string(r.Op)
	out := errors.NewCodedErrorWithDetails(r.Code, "window["+strconv.Itoa(i)+"] ("+string(r.Op)+"): "+r.Message, d)
	out.Cause = r.Cause
	return out
}

func cloneDetails(d map[string]any, extra int) map[string]any {
	out := make(map[string]any, len(d)+extra)
	for k, v := range d {
		out[k] = v
	}
	return out
}

// Resolve runs the pass over w: every scalar param written as an
// expression string is evaluated (scalarslot.Eval over env and bound;
// run as scalarslot.Eval) and its number written in its place. It
// returns every refusal, in reading order — the runtime raises the
// first, predict reports each. A window with no expression param comes
// back as itself, byte-identical.
func Resolve(w *types.Window, env scalarslot.Env, bound exprenv.Bound, run bool) (Resolved, []*Refusal) {
	if len(Exprs(w)) == 0 {
		return Resolved{Window: w}, nil
	}
	m, _ := object(w.Params)
	out := Resolved{}
	var refs []*Refusal
	defer_ := func(p Param) {
		if out.Deferred == nil {
			out.Deferred = map[string]bool{}
		}
		out.Deferred[p.Key] = true
		m[p.Key] = standIn(p.Kind)
	}
	for _, p := range params[w.Type] {
		src, ok := exprAt(m, p.Key)
		if !ok {
			continue
		}
		val, r := scalarslot.Eval(src, p.Kind, env, bound, run)
		switch {
		case r != nil:
			refs = append(refs, refusal(w.Type, p.Key, src, r))
			defer_(p)
		case !val.Resolved:
			out.Unresolved = append(out.Unresolved, Unresolved{Key: p.Key, Expression: src, Reads: val.Reads})
			defer_(p)
		default:
			m[p.Key] = number(val.Number, p.Kind)
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		// A map of raw messages each already valid JSON always
		// marshals; keep the slot as written for the reading to judge.
		return Resolved{Window: w}, refs
	}
	cp := *w
	cp.Params = raw
	out.Window = &cp
	return out, refs
}

// refusal locates a scalar-slot refusal at params.key.
func refusal(op types.WindowType, key, src string, r *scalarslot.Refusal) *Refusal {
	subject := "params." + key
	if r.Coded != nil {
		d := cloneDetails(r.Coded.Details, 1)
		d["param"] = key
		return &Refusal{Op: op, Code: r.Coded.Code, Message: subject + ": " + r.Coded.Message, Details: d, Cause: r.Coded.Cause}
	}
	d := cloneDetails(r.Details, 3)
	d["reason"] = "param_" + r.Reason
	d["param"] = key
	d["expression"] = src
	return &Refusal{Op: op, Code: errors.PULSE_WINDOW_INVALID,
		Message: subject + " " + strconv.Quote(src) + " " + r.What, Details: d}
}

// number is v as the JSON literal the reading decodes: an integer
// slot's exact integer, else the shortest decimal that round-trips v —
// so WIN_EWMA's exact-decimal decay reads an expression yielding 0.7
// exactly as it reads the literal 0.7.
func number(v float64, kind scalarslot.Kind) json.RawMessage {
	if kind == scalarslot.Integer {
		return json.RawMessage(strconv.FormatInt(int64(v), 10))
	}
	return json.RawMessage(strconv.FormatFloat(v, 'g', -1, 64))
}

// standIn is a deferred param's placeholder number: it decodes as the
// kind, and the reading never reads it (CheckDeferred). -1 is out of
// range for every scalar param, so a reading that missed a deferral
// refuses rather than passes.
func standIn(scalarslot.Kind) json.RawMessage { return json.RawMessage("-1") }

// object decodes a params object (absent: an empty one).
func object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]json.RawMessage{}, true
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// exprAt is m[key] when it is a JSON string.
func exprAt(m map[string]json.RawMessage, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	v = bytes.TrimSpace(v)
	if len(v) == 0 || v[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", false
	}
	return s, true
}
