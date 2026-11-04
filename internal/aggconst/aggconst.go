// Package aggconst is the one reading of an AGG_CONSTANT slot: its
// params `{type, value | expr}` judged into the typed constant the
// aggregator emits, and the pre-resolution pass that evaluates an `expr`
// (a scalar-expression slot, internal/scalarslot) into the literal
// `value` it stands for before any operator is built.
//
// Predict and the runtime read a slot through the same functions, so a
// request is refused (or its constant resolved) identically on both
// sides: predict runs Resolve as a static judgement (scalarslot
// Env.Declared(), run false) and reports an expression calling a
// request-bound function (coef(), sweep_axis(), lookup(), …) as
// unresolved; the runtime runs it with the request's bound models (run
// true) and the aggregator factory reads only the rewritten literal.
//
// The wire value per type is what the existing aggregators write for a
// field of that type (AGG_MIN on a numeric, date or datetime field, the
// decimal arm's string, AGG_SET_UNION's label list, AGG_VEC_SUM's
// array): a number for u4…u64, f32 (rounded through float32), f64,
// packed_bool (0 / 1), date (epoch days) and datetime (epoch seconds); a
// decimal string at the literal's own scale for decimal128; the label
// for categorical_*; the label list for set_*; the element array for
// vec_*. `value: null` emits null on every type.
//
// No-execute leaf: types, errors, encoding, exprenv and the scalar-slot
// evaluator only (TestAggConst_ImportBoundary), so internal/descriptor
// may import it.
package aggconst

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
)

// The params keys.
const (
	ParamType  = "type"
	ParamValue = "value"
	ParamExpr  = "expr"
)

// The refusal reasons (PROCESSING_CONFIG details.reason).
const (
	// ReasonParamsInvalid: params is not a JSON object, or carries a key
	// other than type / value / expr (details.key).
	ReasonParamsInvalid = "params_invalid"
	// ReasonTypeInvalid: params.type is absent or not one of the 22
	// field-type names.
	ReasonTypeInvalid = "type_invalid"
	// ReasonValueAndExpr: both params.value and params.expr are set.
	ReasonValueAndExpr = "value_and_expr"
	// ReasonValueMissing: neither params.value nor params.expr is set.
	ReasonValueMissing = "value_missing"
	// ReasonValueInvalid: the literal params.value does not fit
	// params.type (details.type; the rule in the message).
	ReasonValueInvalid = "value_invalid"
	// ReasonExprTypeUnsupported: params.expr on a type an expression
	// cannot produce (decimal128, categorical_*, set_*, vec_*).
	ReasonExprTypeUnsupported = "expr_type_unsupported"
	// ReasonExprOutOfRange: the expression's number does not fit
	// params.type (details.value).
	ReasonExprOutOfRange = "expr_out_of_range"
	// ReasonFieldNotAccepted: the slot names a field, vector or fields;
	// AGG_CONSTANT reads no column.
	ReasonFieldNotAccepted = "field_not_accepted"
	// ReasonLabelMissing: the slot carries no label; a constant has no
	// field to name its column after.
	ReasonLabelMissing = "label_missing"
)

// The expression reasons: "expr_" + the scalar slot's slot-neutral
// reason.
const (
	ReasonExprInvalid    = "expr_" + scalarslot.ReasonInvalid
	ReasonExprNotNumeric = "expr_" + scalarslot.ReasonNotNumeric
	ReasonExprNotFinite  = "expr_" + scalarslot.ReasonNotFinite
	ReasonExprNotInteger = "expr_" + scalarslot.ReasonNotInteger
)

// The vector dimension bounds (the vec_* field types' own).
const (
	MinVectorDim = 2
	MaxVectorDim = 1024
)

// maxExactU64 is the largest u64 constant accepted: the u64 wire value
// is a float64 echo (as AGG_MIN's), exact only up to 2^53.
const maxExactU64 = 1 << 53

// The date / datetime literal layouts. A date is ISO YYYY-MM-DD; a
// datetime is RFC 3339 with a zone offset (Z or ±hh:mm) and whole
// seconds.
const (
	dateLayout     = "2006-01-02"
	datetimeLayout = time.RFC3339
)

// The epoch-day and epoch-second bounds of a date / datetime constant:
// years 0001 through 9999, the range the literal layouts write, so an
// expression's number always renders back to a literal.
var (
	minTime    = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	maxTime    = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	minDateDay = temporal.TimeToDay(minTime)
	maxDateDay = temporal.TimeToDay(maxTime)
)

// Constant is one resolved AGG_CONSTANT value.
type Constant struct {
	// Type is params.type.
	Type encoding.FieldType
	// Null marks `value: null`: the slot emits null.
	Null bool
	// Number is the value of a numeric-family type (u4…u64, f32, f64,
	// packed_bool, date in epoch days, datetime in epoch seconds).
	Number float64
	// Decimal, Scale and Precision are a decimal128 value: the literal's
	// digits, its own scale and its digit count.
	Decimal   encoding.Decimal128
	Scale     uint8
	Precision uint8
	// Label is a categorical_* value.
	Label string
	// Labels is a set_* value: the member labels as written (never nil
	// on a non-null set; an empty list is the empty selection).
	Labels []string
	// Vector is a vec_* value: the elements (a vec_f32's rounded through
	// float32).
	Vector []float64
}

// Numeric reports whether type ft is in the numeric family: its value
// is one number, and an expression may produce it.
func Numeric(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64, encoding.FieldTypePackedBool,
		encoding.FieldTypeDate, encoding.FieldTypeDateTime:
		return true
	}
	return false
}

// ExprTypes returns the type names params.expr accepts, sorted.
func ExprTypes() []string {
	var out []string
	for ft := encoding.FieldType(0); ft.IsKnown(); ft++ {
		if Numeric(ft) {
			out = append(out, ft.String())
		}
	}
	sort.Strings(out)
	return out
}

// kindOf is the scalar-slot kind an expression of type ft is judged as.
func kindOf(ft encoding.FieldType) scalarslot.Kind {
	if ft == encoding.FieldTypeF32 || ft == encoding.FieldTypeF64 {
		return scalarslot.Real
	}
	return scalarslot.Integer
}

// Refusal is one AGG_CONSTANT refusal: a reason refusal
// (PROCESSING_CONFIG details.reason) or a shared code (PULSE_PARAMS_*,
// PULSE_MODEL_*, a table code, a failed fit's own code) located at
// params.expr. Message is the operator-neutral rule: the runtime
// prefixes "AGG_CONSTANT: ", predict "<slot>: AGG_CONSTANT ".
type Refusal struct {
	Code    errors.Code
	Message string
	Details map[string]any
	// Cause is a shared code's own cause; nil for a reason refusal.
	Cause error
}

// Coded returns the refusal as the runtime raises it.
func (r *Refusal) Coded() *errors.CodedError {
	d := cloneDetails(r.Details, 1)
	d["aggregator"] = string(types.AGG_CONSTANT)
	out := errors.NewCodedErrorWithDetails(r.Code, string(types.AGG_CONSTANT)+": "+r.Message, d)
	out.Cause = r.Cause
	return out
}

// At returns the refusal as predict reports it for the slot at path
// ("aggregations[0]", "crosstab.cell", …): details.slot added.
func (r *Refusal) At(path string) *errors.CodedError {
	d := cloneDetails(r.Details, 2)
	d["aggregator"] = string(types.AGG_CONSTANT)
	d["slot"] = path
	out := errors.NewCodedErrorWithDetails(r.Code, path+": "+string(types.AGG_CONSTANT)+": "+r.Message, d)
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

func reason(r, param, msg string, extra map[string]any) *Refusal {
	d := cloneDetails(extra, 2)
	d["reason"] = r
	if param != "" {
		d["param"] = param
	}
	return &Refusal{Code: errors.PROCESSING_CONFIG, Message: msg, Details: d}
}

// CheckSlot judges the slot's shape: no field, vector or fields (a
// constant reads no column) and a label.
func CheckSlot(agg *types.Aggregation) *Refusal {
	if agg == nil {
		return nil
	}
	if agg.Field != "" || agg.Vector != "" || len(agg.Fields) > 0 {
		return reason(ReasonFieldNotAccepted, "", "reads no column; drop field, vector and fields from the slot", nil)
	}
	if agg.Label == "" {
		return reason(ReasonLabelMissing, "", "needs a label: a constant has no field to name its column after", nil)
	}
	return nil
}

// Spec is a slot's params as written.
type Spec struct {
	Type encoding.FieldType
	// Value is params.value as written (the JSON literal); HasValue
	// reports the key is present (`null` included).
	Value    json.RawMessage
	HasValue bool
	// Expr is params.expr; HasExpr reports the key is present.
	Expr    string
	HasExpr bool
}

// Parse reads raw as an AGG_CONSTANT params object.
func Parse(raw json.RawMessage) (Spec, *Refusal) {
	var m map[string]json.RawMessage
	paramread.Read(paramread.Aggregator, string(types.AGG_CONSTANT), ParamType, ParamValue, ParamExpr)
	if len(bytes.TrimSpace(raw)) == 0 || paramread.Probe(raw, &m) != nil || m == nil {
		return Spec{}, reason(ReasonParamsInvalid, "", "params must be an object {type, value | expr}", nil)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch k {
		case ParamType, ParamValue, ParamExpr:
		default:
			return Spec{}, reason(ReasonParamsInvalid, k, "params."+k+" is not an AGG_CONSTANT param (type, value, expr)", map[string]any{"key": k})
		}
	}
	var s Spec
	var name string
	if t, ok := m[ParamType]; !ok || json.Unmarshal(t, &name) != nil {
		return Spec{}, reason(ReasonTypeInvalid, ParamType, "params.type must be a field-type name (u8, f64, date, categorical_u8, set_u8, vec_f64, …)", nil)
	}
	ft, ok := encoding.ParseFieldType(name)
	if !ok || !ft.IsKnown() {
		return Spec{}, reason(ReasonTypeInvalid, ParamType, "params.type "+strconv.Quote(name)+" is not a field-type name", map[string]any{"type": name})
	}
	s.Type = ft
	if v, ok := m[ParamValue]; ok {
		s.Value, s.HasValue = bytes.TrimSpace(v), true
	}
	if e, ok := m[ParamExpr]; ok {
		s.HasExpr = true
		if json.Unmarshal(e, &s.Expr) != nil || strings.TrimSpace(s.Expr) == "" {
			return Spec{}, reason(ReasonExprInvalid, ParamExpr, "params.expr must be a non-empty expression string", map[string]any{"type": name})
		}
	}
	switch {
	case s.HasValue && s.HasExpr:
		return Spec{}, reason(ReasonValueAndExpr, "", "params.value and params.expr are mutually exclusive: set exactly one", map[string]any{"type": name})
	case !s.HasValue && !s.HasExpr:
		return Spec{}, reason(ReasonValueMissing, ParamValue, "needs params.value (a literal; null emits null) or params.expr (a scalar expression)", map[string]any{"type": name})
	}
	if s.HasExpr && !Numeric(ft) {
		return Spec{}, reason(ReasonExprTypeUnsupported, ParamExpr,
			"params.expr yields a number, which a "+name+" constant cannot take; write params.value (expr takes "+strings.Join(ExprTypes(), ", ")+")",
			map[string]any{"type": name})
	}
	return s, nil
}

// Literal judges the literal raw (params.value) against type ft.
func Literal(ft encoding.FieldType, raw json.RawMessage) (Constant, *Refusal) {
	raw = bytes.TrimSpace(raw)
	if string(raw) == "null" {
		return Constant{Type: ft, Null: true}, nil
	}
	bad := func(rule string) (Constant, *Refusal) {
		return Constant{}, reason(ReasonValueInvalid, ParamValue, "params.value "+string(raw)+" is not a "+ft.String()+" value: "+rule,
			map[string]any{"type": ft.String()})
	}
	c := Constant{Type: ft}
	switch {
	case ft == encoding.FieldTypePackedBool:
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return bad("write true or false")
		}
		if b {
			c.Number = 1
		}
		return c, nil
	case isUnsigned(ft):
		n, ok := literalInteger(raw)
		if !ok {
			return bad("write an integer")
		}
		if r := unsignedRange(ft, n); r != "" {
			return bad(r)
		}
		c.Number = n
		return c, nil
	case ft == encoding.FieldTypeF32 || ft == encoding.FieldTypeF64:
		var n json.Number
		if !isNumber(raw) || json.Unmarshal(raw, &n) != nil {
			return bad("write a number")
		}
		v, err := n.Float64()
		if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
			return bad("write a finite number")
		}
		v, ok := floatOf(ft, v)
		if !ok {
			return bad("exceeds the float32 range")
		}
		c.Number = v
		return c, nil
	case ft == encoding.FieldTypeDate:
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return bad("write an ISO date string, YYYY-MM-DD")
		}
		t, err := time.Parse(dateLayout, s)
		if err != nil {
			return bad("write an ISO date string, YYYY-MM-DD")
		}
		c.Number = float64(temporal.TimeToDay(t))
		return c, nil
	case ft == encoding.FieldTypeDateTime:
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return bad("write an RFC 3339 datetime string with a zone offset, e.g. 2024-01-15T10:30:00Z")
		}
		t, err := time.Parse(datetimeLayout, s)
		if err != nil {
			return bad("write an RFC 3339 datetime string with a zone offset, e.g. 2024-01-15T10:30:00Z")
		}
		if t.Nanosecond() != 0 {
			return bad("a datetime is whole seconds; drop the fractional seconds")
		}
		u := t.Unix()
		if u < minTime.Unix() || u > maxTime.Unix() {
			return bad("the instant must fall in years 0001 through 9999 (UTC)")
		}
		c.Number = float64(u)
		return c, nil
	case ft.IsDecimal():
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return bad("write an exact decimal string, e.g. \"12.50\"")
		}
		d, scale, err := encoding.ParseDecimal128(s)
		if err != nil {
			return bad("write an exact decimal string (an optional sign, digits, one '.'; no exponent), e.g. \"12.50\"")
		}
		c.Decimal, c.Scale, c.Precision = d, scale, decimalPrecision(d, scale)
		return c, nil
	case ft.IsCategorical():
		var s string
		if json.Unmarshal(raw, &s) != nil || s == "" {
			return bad("write a non-empty label string")
		}
		c.Label = s
		return c, nil
	case ft.IsSet():
		var labels []string
		if json.Unmarshal(raw, &labels) != nil || labels == nil {
			return bad("write a list of member label strings")
		}
		seen := make(map[string]bool, len(labels))
		for _, l := range labels {
			if l == "" {
				return bad("a member label is non-empty")
			}
			if seen[l] {
				return bad("member " + strconv.Quote(l) + " is listed twice")
			}
			seen[l] = true
		}
		if max := int(ft.MaxSetEntries()); len(labels) > max {
			return bad("a " + ft.String() + " holds at most " + strconv.Itoa(max) + " members")
		}
		c.Labels = labels
		return c, nil
	case ft.IsVector():
		var raws []json.RawMessage
		if json.Unmarshal(raw, &raws) != nil || raws == nil {
			return bad("write a list of numbers")
		}
		if len(raws) < MinVectorDim || len(raws) > MaxVectorDim {
			return bad("a vector has " + strconv.Itoa(MinVectorDim) + " to " + strconv.Itoa(MaxVectorDim) + " elements")
		}
		out := make([]float64, len(raws))
		for i, e := range raws {
			var n json.Number
			if !isNumber(bytes.TrimSpace(e)) || json.Unmarshal(e, &n) != nil {
				return bad("element " + strconv.Itoa(i) + " is not a number")
			}
			v, err := n.Float64()
			if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
				return bad("element " + strconv.Itoa(i) + " is not a finite number")
			}
			elem := encoding.FieldTypeF64
			if ft == encoding.FieldTypeVecF32 {
				elem = encoding.FieldTypeF32
			}
			v, ok := floatOf(elem, v)
			if !ok {
				return bad("element " + strconv.Itoa(i) + " exceeds the float32 range")
			}
			out[i] = v
		}
		c.Vector = out
		return c, nil
	}
	return bad("unsupported type")
}

// FromNumber judges an expression's number v (already finite, and an
// exact integer for an Integer-kind type) against type ft.
func FromNumber(ft encoding.FieldType, v float64) (Constant, *Refusal) {
	out := func(rule string) (Constant, *Refusal) {
		s := strconv.FormatFloat(v, 'g', -1, 64)
		return Constant{}, reason(ReasonExprOutOfRange, ParamExpr, "params.expr yields "+s+", not a "+ft.String()+" value: "+rule,
			map[string]any{"type": ft.String(), "value": v})
	}
	c := Constant{Type: ft}
	switch {
	case ft == encoding.FieldTypePackedBool:
		if v != 0 && v != 1 {
			return out("a packed_bool is 0 or 1")
		}
	case isUnsigned(ft):
		if r := unsignedRange(ft, v); r != "" {
			return out(r)
		}
	case ft == encoding.FieldTypeF32:
		f, ok := floatOf(ft, v)
		if !ok {
			return out("exceeds the float32 range")
		}
		v = f
	case ft == encoding.FieldTypeDate:
		if int64(v) < minDateDay || int64(v) > maxDateDay {
			return out("an epoch day in years 0001 through 9999")
		}
	case ft == encoding.FieldTypeDateTime:
		if int64(v) < minTime.Unix() || int64(v) > maxTime.Unix() {
			return out("an epoch second in years 0001 through 9999 (UTC)")
		}
	case ft == encoding.FieldTypeF64:
	default:
		return Constant{}, reason(ReasonExprTypeUnsupported, ParamExpr, "params.expr cannot produce a "+ft.String()+" constant", map[string]any{"type": ft.String()})
	}
	c.Number = v
	return c, nil
}

// LiteralJSON renders a numeric-family constant as the params.value
// literal that reads back to it (Literal(c.Type, c.LiteralJSON()) == c):
// the pre-resolution pass writes it in place of params.expr.
func (c Constant) LiteralJSON() json.RawMessage {
	switch {
	case c.Null:
		return json.RawMessage("null")
	case c.Type == encoding.FieldTypePackedBool:
		if c.Number == 1 {
			return json.RawMessage("true")
		}
		return json.RawMessage("false")
	case isUnsigned(c.Type):
		return json.RawMessage(strconv.FormatUint(uint64(c.Number), 10))
	case c.Type == encoding.FieldTypeDate:
		return quote(temporal.DayToTime(int64(c.Number)).Format(dateLayout))
	case c.Type == encoding.FieldTypeDateTime:
		return quote(time.Unix(int64(c.Number), 0).UTC().Format(datetimeLayout))
	}
	return json.RawMessage(strconv.FormatFloat(c.Number, 'g', -1, 64))
}

func quote(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// Read judges a slot's params when it carries no expression: the
// aggregator factory's reading, after the pre-resolution pass rewrote
// every expression into its literal. A remaining expression is the
// caller's fault (a path that skipped the pass): PROCESSING_INTERNAL.
func Read(raw json.RawMessage) (Constant, *Refusal) {
	s, r := Parse(raw)
	if r != nil {
		return Constant{}, r
	}
	if s.HasExpr {
		return Constant{}, &Refusal{Code: errors.PROCESSING_INTERNAL,
			Message: "params.expr " + strconv.Quote(s.Expr) + " reached the aggregator unresolved; the request skipped the scalar pre-resolution pass",
			Details: map[string]any{"param": ParamExpr}}
	}
	return Literal(s.Type, s.Value)
}

// Unresolved is one AGG_CONSTANT expression a static judgement could
// not compute.
type Unresolved struct {
	// Path is the slot path + ".params.expr".
	Path string
	// Expression is params.expr as written.
	Expression string
	// Reads are the request-bound functions it calls, sorted.
	Reads []string
}

// Eval judges one slot's params: its literal, or its expression
// evaluated over env and bound (run as scalarslot.Eval). ok false with
// no refusal: a static judgement left the expression to the runtime
// (unresolved names it).
func Eval(raw json.RawMessage, env scalarslot.Env, bound exprenv.Bound, run bool) (c Constant, unresolved *Unresolved, ok bool, r *Refusal) {
	s, r := Parse(raw)
	if r != nil {
		return Constant{}, nil, false, r
	}
	if !s.HasExpr {
		c, r := Literal(s.Type, s.Value)
		return c, nil, r == nil, r
	}
	val, sr := scalarslot.Eval(s.Expr, kindOf(s.Type), env, bound, run)
	if sr != nil {
		return Constant{}, nil, false, exprRefusal(s, sr)
	}
	if !val.Resolved {
		return Constant{}, &Unresolved{Expression: s.Expr, Reads: val.Reads}, false, nil
	}
	c, r = FromNumber(s.Type, val.Number)
	return c, nil, r == nil, r
}

// exprRefusal locates a scalar-slot refusal at params.expr.
func exprRefusal(s Spec, r *scalarslot.Refusal) *Refusal {
	if r.Coded != nil {
		d := cloneDetails(r.Coded.Details, 1)
		d["param"] = ParamExpr
		return &Refusal{Code: r.Coded.Code, Message: "params.expr: " + r.Coded.Message, Details: d, Cause: r.Coded.Cause}
	}
	d := cloneDetails(r.Details, 4)
	d["reason"] = "expr_" + r.Reason
	d["param"] = ParamExpr
	d["expression"] = s.Expr
	d["type"] = s.Type.String()
	return &Refusal{Code: errors.PROCESSING_CONFIG, Message: "params.expr " + strconv.Quote(s.Expr) + " " + r.What, Details: d}
}

// HasExpr reports whether agg is an AGG_CONSTANT slot whose params
// carry an expr key (cheap: no evaluation).
func HasExpr(agg *types.Aggregation) bool {
	if agg == nil || agg.Type != types.AGG_CONSTANT {
		return false
	}
	var m map[string]json.RawMessage
	if paramread.Probe(agg.Params, &m) != nil {
		return false
	}
	_, ok := m[ParamExpr]
	return ok
}

// NonNumeric reports whether agg is an AGG_CONSTANT slot whose
// params.type writes something other than one number into a row (a
// decimal string, a label, a label list, a number array): a consumer
// that reads one number per row refuses it, as it refuses a non-scalar
// aggregation type. A params object that does not parse answers false
// (the reading refuses it first).
func NonNumeric(agg *types.Aggregation) bool {
	if agg == nil || agg.Type != types.AGG_CONSTANT {
		return false
	}
	var p struct {
		Type string `json:"type"`
	}
	if paramread.Probe(agg.Params, &p) != nil {
		return false
	}
	ft, ok := encoding.ParseFieldType(p.Type)
	return ok && ft.IsKnown() && !Numeric(ft)
}

// SlotRefusal pairs a refusal with the slot path it was found at.
type SlotRefusal struct {
	Path    string
	Refusal *Refusal
}

// Result is a request after the pass.
type Result struct {
	// Request is req itself when no AGG_CONSTANT slot carries an
	// expression; otherwise a copy whose such slots carry the resolved
	// literal in params.value (an unresolved or refused one is kept as
	// written).
	Request *types.Request
	// Unresolved are the expressions a static judgement left to the
	// runtime, in slot walk order.
	Unresolved []Unresolved
	// Refusals are the refused expressions, in slot walk order.
	Refusals []SlotRefusal
}

// EnvFunc builds the environment an expression is judged against and
// the bound it runs over; called at most once, on the first expression.
type EnvFunc func() (scalarslot.Env, exprenv.Bound)

// ResolveRequest is the pre-resolution pass over every AGG_CONSTANT
// slot of req (aggregations, the crosstab cell or cells, its margin
// aggregations): each params.expr is evaluated (Eval over env; run as
// scalarslot.Eval) and the slot's params rewritten to {type, value} with
// the literal it stands for. Only the expression is judged here; the
// slot's other refusals are the reading's (Read / CheckSlot). The
// caller's request and slots are never mutated.
func ResolveRequest(req *types.Request, env EnvFunc, run bool) Result {
	if req == nil || !anyExpr(req) {
		return Result{Request: req}
	}
	var res Result
	var built bool
	var e scalarslot.Env
	var b exprenv.Bound
	resolve := func(path string, a *types.Aggregation) *types.Aggregation {
		if !HasExpr(a) {
			return a
		}
		if !built {
			e, b = env()
			built = true
		}
		c, un, ok, r := Eval(a.Params, e, b, run)
		switch {
		case r != nil:
			res.Refusals = append(res.Refusals, SlotRefusal{Path: path, Refusal: r})
			return a
		case un != nil:
			un.Path = path + ".params." + ParamExpr
			res.Unresolved = append(res.Unresolved, *un)
			return a
		case !ok:
			return a
		}
		params, err := json.Marshal(map[string]json.RawMessage{ParamType: quote(c.Type.String()), ParamValue: c.LiteralJSON()})
		if err != nil {
			return a
		}
		cp := *a
		cp.Params = params
		return &cp
	}
	out := *req
	out.Aggregations = resolveList("aggregations", req.Aggregations, resolve)
	if ct := req.Crosstab; ct != nil {
		spec := *ct
		if ct.HasMeasures() {
			spec.Cells = resolveList("crosstab.cells", ct.Cells, resolve)
		} else if ct.Cell != nil {
			spec.Cell = resolve("crosstab.cell", ct.Cell)
		}
		spec.MarginAggregations = resolveList("crosstab.margin_aggregations", ct.MarginAggregations, resolve)
		out.Crosstab = &spec
	}
	res.Request = &out
	return res
}

func resolveList(prefix string, as []*types.Aggregation, f func(string, *types.Aggregation) *types.Aggregation) []*types.Aggregation {
	if len(as) == 0 {
		return as
	}
	out := make([]*types.Aggregation, len(as))
	for i, a := range as {
		out[i] = f(prefix+"["+strconv.Itoa(i)+"]", a)
	}
	return out
}

// anyExpr reports whether any AGG_CONSTANT slot of req carries an expr.
func anyExpr(req *types.Request) bool {
	for _, a := range req.Aggregations {
		if HasExpr(a) {
			return true
		}
	}
	if ct := req.Crosstab; ct != nil {
		for _, a := range ct.CellAggregations() {
			if HasExpr(a) {
				return true
			}
		}
		for _, a := range ct.MarginAggregations {
			if HasExpr(a) {
				return true
			}
		}
	}
	return false
}

func isUnsigned(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64:
		return true
	}
	return false
}

// unsignedRange is the rule an unsigned value v breaks, "" when it fits.
func unsignedRange(ft encoding.FieldType, v float64) string {
	var max float64
	switch ft {
	case encoding.FieldTypeU4:
		max = 15
	case encoding.FieldTypeU8:
		max = math.MaxUint8
	case encoding.FieldTypeU16:
		max = math.MaxUint16
	case encoding.FieldTypeU32:
		max = math.MaxUint32
	case encoding.FieldTypeU64:
		max = maxExactU64
	}
	if v < 0 || v > max {
		rule := "an integer in 0.." + strconv.FormatFloat(max, 'f', -1, 64)
		if ft == encoding.FieldTypeU64 {
			rule += " (a u64 value is a float64 on the wire, exact only up to 2^53)"
		}
		return rule
	}
	return ""
}

// literalInteger reads a JSON number that is an exact integer. A
// magnitude past 2^53 comes back as ±Inf: out of every unsigned range,
// never rounded into one.
func literalInteger(raw json.RawMessage) (float64, bool) {
	var n json.Number
	if !isNumber(raw) || json.Unmarshal(raw, &n) != nil {
		return 0, false
	}
	if i, err := n.Int64(); err == nil {
		if i > maxExactU64 || i < -maxExactU64 {
			return math.Inf(int(sign(i))), true
		}
		return float64(i), true
	}
	v, err := n.Float64()
	switch {
	case err != nil && !math.IsInf(v, 0):
		return 0, false
	case math.IsInf(v, 0) || math.Abs(v) > maxExactU64:
		return math.Inf(int(math.Copysign(1, v))), true
	case math.Trunc(v) != v:
		return 0, false
	}
	return v, true
}

func sign(i int64) int64 {
	if i < 0 {
		return -1
	}
	return 1
}

// isNumber reports whether raw is a JSON number literal (not a string
// holding one).
func isNumber(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	c := raw[0]
	return c == '-' || (c >= '0' && c <= '9')
}

// floatOf is v as a value of float type ft: f32 rounds through float32
// (the wire echo of an f32 field) and refuses an overflow.
func floatOf(ft encoding.FieldType, v float64) (float64, bool) {
	if ft != encoding.FieldTypeF32 {
		return v, true
	}
	if math.Abs(v) > math.MaxFloat32 {
		return 0, false
	}
	return float64(float32(v)), true
}

// decimalPrecision is the digit count of d at scale (at least the
// scale, at least 1).
func decimalPrecision(d encoding.Decimal128, scale uint8) uint8 {
	m := d.Mantissa()
	digits := len(strings.TrimPrefix(m.String(), "-"))
	if m.Sign() == 0 {
		digits = 1
	}
	if digits < int(scale) {
		digits = int(scale)
	}
	if digits < 1 {
		digits = 1
	}
	return uint8(digits)
}
