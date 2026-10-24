package exprenv

import (
	"encoding/json"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
)

// Request parameters in expressions (U58 E3-S1).
//
// A request's `params` object (types.Request.Params) names constants
// every request-scoped expression reads as `params.<name>` (or
// `params["<name>"]`): ATTR_FORMULA, FILTER_EXPRESSION, a total()
// argument, a regression holdout and OVERLAY_FORMULA. The values are
// typed, never spliced into the source text:
//
//   - a number keeps its JSON text exactly: an integer that fits int64
//     binds as int64, a larger non-negative one that fits uint64 as
//     uint64, anything else (a fraction, an exponent, a wider integer)
//     as float64 — so a u64 id beyond 2^53 compares exactly against an
//     integer literal, another param or a uint64-bound value;
//   - a string, a bool;
//   - a homogeneous list of one of those ([]int64 / []uint64 / []float64
//     for numbers, by the same rule over every element; []string;
//     []bool; an empty list is []any) — `wave in params.waves`.
//
// Binding is a compile-time constant fold ([Bound.Params]: a patcher
// rewrites each declared `params.<name>` into a constant node before the
// type check), so the per-row env never carries params and a program
// is specific to the values it was compiled with: two requests (or two
// runs of one) with different values never share a program. Predict and
// the runtime compile through the same [Bound.Options], so both
// type-check every expression against the same constants.
//
// Refusals — every one raised identically by predict and the runtime,
// before any record is read:
//
//   - [NormalizeParams]: PULSE_PARAMS_INVALID (details.param,
//     details.reason null_value | invalid_name | mixed_list |
//     unsupported_value);
//   - [ParamsRefusal]: PULSE_PARAMS_FIELD_COLLISION when the name
//     `params` is a column the expression can read (a schema field, a
//     derived column, a named vector), PULSE_PARAMS_UNDECLARED for a
//     `params.<name>` the request does not declare.
//
// A bare `params` (not a member access with a literal name) is not a
// parameter read: it reads the column of that name when one exists and
// otherwise fails the compile as an unknown name, as before.

// ParamsName is the identifier request parameters are read through.
const ParamsName = "params"

// Params is a request's normalized parameters ([NormalizeParams]):
// name → int64 | uint64 | float64 | string | bool | []int64 | []uint64 |
// []float64 | []string | []bool | []any (empty list). Read-only once
// built; a nil or empty Params declares nothing.
type Params map[string]any

// Names returns the declared parameter names, sorted.
func (p Params) Names() []string {
	out := make([]string, 0, len(p))
	for n := range p {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// The PULSE_PARAMS_INVALID reasons.
const (
	ParamsReasonNull        = "null_value"
	ParamsReasonName        = "invalid_name"
	ParamsReasonMixedList   = "mixed_list"
	ParamsReasonUnsupported = "unsupported_value"
)

// paramNameRe is an expr-lang identifier: what `params.<name>` can
// spell.
var paramNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// paramKind is a normalized scalar's family; a list's elements must
// share one.
type paramKind int

const (
	paramNumber paramKind = iota + 1
	paramString
	paramBool
)

func (k paramKind) String() string {
	switch k {
	case paramNumber:
		return "number"
	case paramString:
		return "string"
	case paramBool:
		return "bool"
	}
	return "unknown"
}

// NormalizeParams validates raw (types.Request.Params: decoded with
// json.Number, or built in Go) and returns its typed form. Names are
// judged in sorted order, so the first refusal is deterministic. A nil
// or empty raw is (nil, nil).
func NormalizeParams(raw map[string]any) (Params, *errors.CodedError) {
	if len(raw) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make(Params, len(raw))
	for _, name := range names {
		if !paramNameRe.MatchString(name) {
			return nil, paramsInvalid(name, ParamsReasonName,
				"request params name "+strconv.Quote(name)+" is not an identifier (a letter or underscore, then letters, digits or underscores), so params.<name> cannot spell it")
		}
		v, cerr := normalizeParamValue(name, raw[name])
		if cerr != nil {
			return nil, cerr
		}
		out[name] = v
	}
	return out, nil
}

func paramsInvalid(name, reason, msg string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_PARAMS_INVALID, msg,
		map[string]any{"param": name, "reason": reason})
}

// normalizeParamValue types one value: a scalar, or a homogeneous list.
func normalizeParamValue(name string, v any) (any, *errors.CodedError) {
	if v == nil {
		return nil, paramsInvalid(name, ParamsReasonNull,
			"request params "+strconv.Quote(name)+" is null: a parameter is a number, string, bool or a list of one of those, never null")
	}
	if s, k, ok := normalizeParamScalar(v); ok {
		return s, nil
	} else if k != 0 || !isParamList(v) {
		return nil, paramsInvalid(name, ParamsReasonUnsupported,
			"request params "+strconv.Quote(name)+" is not a number, string, bool or a list of one of those")
	}
	rv := reflect.ValueOf(v)
	n := rv.Len()
	elems := make([]any, n)
	var kind paramKind
	for i := 0; i < n; i++ {
		e := rv.Index(i).Interface()
		if e == nil {
			return nil, paramsInvalid(name, ParamsReasonNull,
				"request params "+strconv.Quote(name)+" element "+strconv.Itoa(i)+" is null: a list holds numbers, strings or bools, never null")
		}
		s, k, ok := normalizeParamScalar(e)
		if !ok {
			return nil, paramsInvalid(name, ParamsReasonUnsupported,
				"request params "+strconv.Quote(name)+" element "+strconv.Itoa(i)+" is not a number, string or bool (a list holds scalars only)")
		}
		if kind != 0 && k != kind {
			return nil, paramsInvalid(name, ParamsReasonMixedList,
				"request params "+strconv.Quote(name)+" mixes "+kind.String()+" and "+k.String()+" elements: a list holds one kind")
		}
		kind = k
		elems[i] = s
	}
	switch kind {
	case 0:
		return []any{}, nil
	case paramString:
		out := make([]string, n)
		for i, e := range elems {
			out[i] = e.(string)
		}
		return out, nil
	case paramBool:
		out := make([]bool, n)
		for i, e := range elems {
			out[i] = e.(bool)
		}
		return out, nil
	}
	return numberList(elems), nil
}

// numberList is the one slice type every element of a number list
// fits: []int64, else []uint64 (all non-negative integers), else
// []float64.
func numberList(elems []any) any {
	allInt, allUint := true, true
	for _, e := range elems {
		switch x := e.(type) {
		case int64:
			if x < 0 {
				allUint = false
			}
		case uint64:
			allInt = false
		default:
			allInt, allUint = false, false
		}
	}
	switch {
	case allInt:
		out := make([]int64, len(elems))
		for i, e := range elems {
			out[i] = e.(int64)
		}
		return out
	case allUint:
		out := make([]uint64, len(elems))
		for i, e := range elems {
			switch x := e.(type) {
			case int64:
				out[i] = uint64(x)
			case uint64:
				out[i] = x
			}
		}
		return out
	}
	out := make([]float64, len(elems))
	for i, e := range elems {
		switch x := e.(type) {
		case int64:
			out[i] = float64(x)
		case uint64:
			out[i] = float64(x)
		case float64:
			out[i] = x
		}
	}
	return out
}

func isParamList(v any) bool {
	k := reflect.ValueOf(v).Kind()
	return k == reflect.Slice || k == reflect.Array
}

// normalizeParamScalar types one scalar. ok=false with kind 0 when v is
// not a scalar (a list, a map, anything else).
func normalizeParamScalar(v any) (any, paramKind, bool) {
	switch x := v.(type) {
	case string:
		return x, paramString, true
	case bool:
		return x, paramBool, true
	case json.Number:
		n, ok := parseParamNumber(string(x))
		return n, paramNumber, ok
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), paramNumber, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := rv.Uint()
		if u <= math.MaxInt64 {
			return int64(u), paramNumber, true
		}
		return u, paramNumber, true
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, paramNumber, false
		}
		// A Go float types exactly as its JSON text would decode, so a
		// request built in Go and the same request read off the wire
		// bind the same constant.
		b, err := json.Marshal(f)
		if err != nil {
			return nil, paramNumber, false
		}
		n, ok := parseParamNumber(string(b))
		return n, paramNumber, ok
	}
	return nil, 0, false
}

// parseParamNumber types a JSON number's text: int64, else uint64, else
// float64 (finite).
func parseParamNumber(s string) (any, bool) {
	if !strings.ContainsAny(s, ".eE") {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, true
		}
		if u, err := strconv.ParseUint(s, 10, 64); err == nil {
			return u, true
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, false
	}
	return f, true
}

// ParamUses is the parse-only reading of how an expression names
// `params`: Names are the literal names it reads as `params.<name>` /
// `params["<name>"]` (first-use order, no repeats); Bare reports any
// other use of the identifier `params` (bare, indexed by a non-literal,
// a callee) outside a let binding of that name. parsed=false when src
// does not parse (the compile reports the syntax error).
type ParamUses struct {
	Names []string
	Bare  bool
}

// ReadParams returns src's [ParamUses].
func ReadParams(src string) (ParamUses, bool) {
	if !strings.Contains(src, ParamsName) {
		return ParamUses{}, true
	}
	tree, err := exprparser.Parse(src)
	if err != nil {
		return ParamUses{}, false
	}
	m := &paramMarker{members: map[*exprast.IdentifierNode]bool{}, seen: map[string]bool{}}
	exprast.Walk(&tree.Node, m)
	if m.let {
		// A let binding named params shadows the namespace everywhere
		// in this expression: none of its uses is a parameter read.
		return ParamUses{}, true
	}
	exprast.Walk(&tree.Node, &paramBareVisitor{m: m})
	return ParamUses{Names: m.names, Bare: m.bare}, true
}

type paramMarker struct {
	members map[*exprast.IdentifierNode]bool
	seen    map[string]bool
	names   []string
	bare    bool
	let     bool
}

func (m *paramMarker) Visit(node *exprast.Node) {
	switch n := (*node).(type) {
	case *exprast.VariableDeclaratorNode:
		if n.Name == ParamsName {
			m.let = true
		}
	case *exprast.MemberNode:
		id, ok := n.Node.(*exprast.IdentifierNode)
		if !ok || id.Value != ParamsName {
			return
		}
		s, ok := n.Property.(*exprast.StringNode)
		if !ok {
			return
		}
		m.members[id] = true
		if !m.seen[s.Value] {
			m.seen[s.Value] = true
			m.names = append(m.names, s.Value)
		}
	}
}

type paramBareVisitor struct{ m *paramMarker }

func (v *paramBareVisitor) Visit(node *exprast.Node) {
	if id, ok := (*node).(*exprast.IdentifierNode); ok && id.Value == ParamsName && !v.m.members[id] {
		v.m.bare = true
	}
}

// ParamsRefusal is the static params rule of one expression src at a
// compile site, shared by predict and the runtime: nil when src reads
// no `params.<name>`; PULSE_PARAMS_FIELD_COLLISION when it does and
// column reports that `params` is a column the expression could read (a
// schema field, a derived column, a named vector); else
// PULSE_PARAMS_UNDECLARED for the first name params does not declare.
func ParamsRefusal(src string, params Params, column bool) *errors.CodedError {
	uses, parsed := ReadParams(src)
	if !parsed || len(uses.Names) == 0 {
		return nil
	}
	if column {
		return errors.NewCodedErrorWithDetails(errors.PULSE_PARAMS_FIELD_COLLISION,
			"expression "+strconv.Quote(src)+" reads params."+uses.Names[0]+", but `params` is also a column of this request (a schema field, derived column or vector named params), which a bare `params` reads",
			map[string]any{"expression": src, "param": uses.Names[0], "field": ParamsName})
	}
	for _, name := range uses.Names {
		if _, ok := params[name]; !ok {
			declared := params.Names()
			return errors.NewCodedErrorWithDetails(errors.PULSE_PARAMS_UNDECLARED,
				"expression "+strconv.Quote(src)+" reads params."+name+", which the request's params does not declare",
				map[string]any{"expression": src, "param": name, "declared": declared})
		}
	}
	return nil
}

// FoldsParams reports whether every use of the identifier `params` in
// src is a declared-or-refused parameter read (no bare use), so a
// compile site that declares no `params` column need not treat the name
// as an env input. The compile-once runtime drops `params` from its
// free names on it, so a parameter read never defers the compile.
func FoldsParams(src string) bool {
	uses, parsed := ReadParams(src)
	return parsed && len(uses.Names) > 0 && !uses.Bare
}

// paramsPatcher folds every declared `params.<name>` into a constant
// node (see the file comment). It is registered FIRST in [Bound.Options]
// so the later patchers (`%`, `**`, the decimal operators) and the type
// check see a constant of the parameter's own type.
type paramsPatcher struct {
	params Params
}

func (p paramsPatcher) Visit(node *exprast.Node) {
	m, ok := (*node).(*exprast.MemberNode)
	if !ok {
		return
	}
	id, ok := m.Node.(*exprast.IdentifierNode)
	if !ok || id.Value != ParamsName {
		return
	}
	s, ok := m.Property.(*exprast.StringNode)
	if !ok {
		return
	}
	v, ok := p.params[s.Value]
	if !ok {
		return
	}
	exprast.Patch(node, &exprast.ConstantNode{Value: v})
}

// paramsOption is the params patcher as an expr option; nil params
// patch nothing (no option).
func paramsOption(params Params) []expr.Option {
	if len(params) == 0 {
		return nil
	}
	return []expr.Option{expr.Patch(paramsPatcher{params: params})}
}
