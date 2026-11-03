// Package resultcall is the ONE rule set for result(slot, aggregation):
// the request-bound expression function that reads one ungrouped
// aggregation value off a Compose slot's response. It finds the calls
// an expression makes, judges them statically against the batch's
// slots (Catalog), names the slots a reader depends on (Slots — the
// Compose result pre-phase's run set), and reads the value off a
// finished response at run time (Reader).
//
// Scope: result() is in reach of a Compose `optimize` block only —
// its bounds and starts (internal/scalarslot, with a Catalog) and its
// objective and constraints (internal/optblock). Every other scalar
// slot — a window or extension param, an AGG_CONSTANT expr, an inline
// model number, any slot's or fit request's — is judged with a nil
// Catalog and refuses a result() call (ReasonOutOfScope). Nothing a
// slot computes reads an optimize block, so the dependency graph is
// one level deep: optimize → slots, never slot → slot, and no cycle
// can form.
//
// Every argument is a string literal, so predict knows the graph
// without running anything: a computed argument is ReasonArgsNotLiteral.
// The value itself is never known at predict (the slot has not run):
// a bound reading it is listed under unresolved_scalars.
//
// No-execute leaf: types, errors and exprenv only, never the engine
// (TestResultCall_ImportBoundary), so internal/descriptor may import it.
package resultcall

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/types"
)

// Func is the reader's name (exprenv.ResultFunc), reserved against
// embedder functions and decision-variable names.
const Func = exprenv.ResultFunc

// The PULSE_RESULT_INVALID reasons (details.reason).
const (
	// ReasonOutOfScope: result() is called outside a Compose optimize
	// block (details.expression).
	ReasonOutOfScope = "out_of_scope"
	// ReasonArgsNotLiteral: an argument is not a string literal, so the
	// slot it reads is not known before the run.
	ReasonArgsNotLiteral = "args_not_literal"
	// ReasonSlotUnknown: no Compose slot carries the label (details
	// available: the batch's slot labels).
	ReasonSlotUnknown = "slot_unknown"
	// ReasonAggregationUnknown: the slot has no aggregation with the
	// label (details available: its aggregation labels).
	ReasonAggregationUnknown = "aggregation_unknown"
	// ReasonGrouped: the slot is grouped (groups or a crosstab), so the
	// aggregation has no single value.
	ReasonGrouped = "grouped"
	// ReasonNull: at run time the value is null (undefined — no row
	// reached the aggregation, or its figure is NaN).
	ReasonNull = "null"
	// ReasonNotNumeric: the aggregation writes no single number —
	// statically, its type is non-scalar (CatalogWith); at run time,
	// the value is not a number (a map, a list, a string, a bool —
	// details type).
	ReasonNotNumeric = "not_numeric"
	// ReasonNotFinite: at run time the value is an infinity (details
	// value).
	ReasonNotFinite = "not_finite"
)

// Slot is one Compose slot as result() sees it.
type Slot struct {
	// Index is the slot's position in ComposedRequest.Requests (sweep
	// and banner slots included, after expansion).
	Index int
	// Label is the slot's effective label: its own, else the Compose
	// default request_<index+1>.
	Label string
	// Aggregations are its aggregations' effective labels
	// (types.AggregationLabelOf), in slot order.
	Aggregations []string
	// Grouped reports groups or a crosstab: no single value per label.
	Grouped bool
	// NonNumeric are the labels of its aggregations whose value is not
	// one number (CatalogWith's nonNumeric judgement), in slot order.
	NonNumeric []string
}

// Catalog is the batch's slots, the names a literal result() call is
// judged against. A nil *Catalog is "result() out of scope".
type Catalog struct {
	slots []Slot
}

// CatalogOf is the catalog of the batch's slots (the effective request,
// after sweep and banner expansion), index-aligned; a nil slot is
// skipped. A label two slots share resolves to the first (the batch's
// label rule refuses the pair later). It judges no aggregation
// non-numeric: CatalogWith(requests, nil).
func CatalogOf(requests []*types.Request) *Catalog {
	return CatalogWith(requests, nil)
}

// CatalogWith is CatalogOf with each aggregation judged by nonNumeric
// (nil: none is) — the instance's vectors.AggregationKinds.
// IsNonScalarSlot, so a map-, array- or label-valued aggregator and a
// non-numeric AGG_CONSTANT are refused statically (ReasonNotNumeric)
// by predict and the runtime alike, before any slot runs. The caller
// passes the judgement so this leaf stays engine-free.
func CatalogWith(requests []*types.Request, nonNumeric func(*types.Aggregation) bool) *Catalog {
	c := &Catalog{}
	for i, r := range requests {
		if r == nil {
			continue
		}
		s := Slot{Index: i, Label: EffectiveLabel(r, i), Grouped: len(r.Groups) > 0 || r.Crosstab != nil}
		for _, a := range r.Aggregations {
			if a != nil {
				label := types.AggregationLabelOf(a)
				s.Aggregations = append(s.Aggregations, label)
				if nonNumeric != nil && nonNumeric(a) {
					s.NonNumeric = append(s.NonNumeric, label)
				}
			}
		}
		c.slots = append(c.slots, s)
	}
	return c
}

// EffectiveLabel is slot i's label as Compose stamps it: its own, else
// request_<i+1>.
func EffectiveLabel(r *types.Request, i int) string {
	if r != nil && r.Label != "" {
		return r.Label
	}
	return "request_" + strconv.Itoa(i+1)
}

// Lookup returns the slot labelled label.
func (c *Catalog) Lookup(label string) (Slot, bool) {
	if c == nil {
		return Slot{}, false
	}
	for _, s := range c.slots {
		if s.Label == label {
			return s, true
		}
	}
	return Slot{}, false
}

// labels are the catalog's slot labels, in slot order.
func (c *Catalog) labels() []string {
	out := make([]string, 0, len(c.slots))
	for _, s := range c.slots {
		out = append(out, s.Label)
	}
	return out
}

// Call is one result() call: its two arguments when both are string
// literals (Literal), else Literal false.
type Call struct {
	Slot, Aggregation string
	Literal           bool
}

// Calls are src's result() calls in walk order (an argument's calls
// before the call itself); nil when it makes none or does not parse
// (the compile reports that). A call whose argument count is not two
// is left to the compile, which declares result(string, string).
func Calls(src string) []Call {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	v := &visitor{}
	exprast.Walk(&tree.Node, v)
	return v.calls
}

type visitor struct{ calls []Call }

func (v *visitor) Visit(node *exprast.Node) {
	n, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	id, ok := n.Callee.(*exprast.IdentifierNode)
	if !ok || id.Value != Func {
		return
	}
	if len(n.Arguments) != 2 {
		// Out of scope regardless of arity; in scope the compile's
		// typed declaration reports the count.
		v.calls = append(v.calls, Call{Literal: false})
		return
	}
	slot, okS := n.Arguments[0].(*exprast.StringNode)
	agg, okA := n.Arguments[1].(*exprast.StringNode)
	if !okS || !okA {
		v.calls = append(v.calls, Call{Literal: false})
		return
	}
	v.calls = append(v.calls, Call{Slot: slot.Value, Aggregation: agg.Value, Literal: true})
}

// Refusal is the first PULSE_RESULT_INVALID src's result() calls raise
// against cat, in walk order — nil when every call passes or src makes
// none. A nil cat refuses any call (ReasonOutOfScope); otherwise a
// two-argument call needs both arguments string literals naming a slot
// of the batch, an aggregation label of that slot, an ungrouped slot
// and an aggregation the catalog does not judge non-numeric. Predict and the runtime's entry check run the same rule, so
// both refuse identically.
func Refusal(src string, cat *Catalog) *errors.CodedError {
	for _, c := range Calls(src) {
		if cerr := check(src, c, cat); cerr != nil {
			return cerr
		}
	}
	return nil
}

func check(src string, c Call, cat *Catalog) *errors.CodedError {
	if cat == nil {
		// Only the expression: a slot-located refusal already carries
		// its own location keys (details.slot is a slot path there).
		d := map[string]any{"expression": src}
		return invalid(ReasonOutOfScope, "result() reads a Compose slot's aggregation only in an optimize block (its bounds, starts, objective and constraints); "+
			"a slot, a fit request and any other scalar param cannot read it", d)
	}
	if !c.Literal {
		return invalid(ReasonArgsNotLiteral, "result(slot, aggregation) takes two string literals, so the slot it reads is known before the run",
			map[string]any{"expression": src})
	}
	s, ok := cat.Lookup(c.Slot)
	if !ok {
		return invalid(ReasonSlotUnknown, call(c)+": no Compose slot is labelled "+strconv.Quote(c.Slot),
			map[string]any{"slot": c.Slot, "aggregation": c.Aggregation, "available": cat.labels()})
	}
	if !slices.Contains(s.Aggregations, c.Aggregation) {
		avail := slices.Clone(s.Aggregations)
		if avail == nil {
			avail = []string{}
		}
		return invalid(ReasonAggregationUnknown, call(c)+": slot "+strconv.Quote(c.Slot)+" has no aggregation labelled "+strconv.Quote(c.Aggregation),
			map[string]any{"slot": c.Slot, "aggregation": c.Aggregation, "available": avail})
	}
	if s.Grouped {
		return invalid(ReasonGrouped, call(c)+": slot "+strconv.Quote(c.Slot)+" is grouped (groups or a crosstab), so the aggregation has no single value",
			map[string]any{"slot": c.Slot, "aggregation": c.Aggregation})
	}
	if slices.Contains(s.NonNumeric, c.Aggregation) {
		return invalid(ReasonNotNumeric, call(c)+": aggregation "+strconv.Quote(c.Aggregation)+" of slot "+strconv.Quote(c.Slot)+
			" writes no single number (a map, a list or a string), so it cannot be read as one",
			map[string]any{"slot": c.Slot, "aggregation": c.Aggregation})
	}
	return nil
}

// Slots are the indices of the slots srcs' literal result() calls read
// that cat knows, sorted and unique — the result pre-phase's run set.
// A call Refusal refuses reads nothing.
func Slots(srcs []string, cat *Catalog) []int {
	var out []int
	for _, src := range srcs {
		for _, c := range Calls(src) {
			if !c.Literal {
				continue
			}
			if s, ok := cat.Lookup(c.Slot); ok && !slices.Contains(out, s.Index) {
				out = append(out, s.Index)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Reader is result()'s run-time implementation over the batch's
// responses (index-aligned with the catalog's slots). Every slot a
// call reads ran in the pre-phase, so a missing response is an
// internal fault.
type Reader struct {
	Catalog   *Catalog
	Responses []*types.Response
}

// Func is the exprenv.Bound.Result implementation: the value
// result(slot, aggregation) reads (a float64), or its coded refusal.
func (r *Reader) Func(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL, "result() called with "+strconv.Itoa(len(args))+" arguments")
	}
	slot, _ := args[0].(string)
	agg, _ := args[1].(string)
	v, cerr := r.Value(slot, agg)
	if cerr != nil {
		return nil, cerr
	}
	return v, nil
}

// Value is the aggregation agg of the slot labelled slot, read off its
// finished response: a finite number, else PULSE_RESULT_INVALID
// (ReasonGrouped when the response is not one row, ReasonNull,
// ReasonNotNumeric, ReasonNotFinite).
func (r *Reader) Value(slot, agg string) (float64, *errors.CodedError) {
	c := Call{Slot: slot, Aggregation: agg, Literal: true}
	if r == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL, call(c)+": no result reader is bound")
	}
	if cerr := check("", c, r.Catalog); cerr != nil {
		return 0, cerr
	}
	s, _ := r.Catalog.Lookup(slot)
	if s.Index >= len(r.Responses) || r.Responses[s.Index] == nil {
		return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL, call(c)+": slot "+strconv.Quote(slot)+" has not run before its reader")
	}
	resp := r.Responses[s.Index]
	if len(resp.Data) != 1 {
		return 0, invalid(ReasonGrouped, call(c)+": slot "+strconv.Quote(slot)+" answered "+strconv.Itoa(len(resp.Data))+" rows, not one",
			map[string]any{"slot": slot, "aggregation": agg, "rows": len(resp.Data)})
	}
	return number(c, resp.Data[0][agg])
}

// number is v as a finite float64, or its refusal.
func number(c Call, v any) (float64, *errors.CodedError) {
	d := map[string]any{"slot": c.Slot, "aggregation": c.Aggregation}
	var f float64
	switch x := v.(type) {
	case nil:
		return 0, invalid(ReasonNull, call(c)+" is null (undefined): no row reached the aggregation, or its figure is undefined", d)
	case float64:
		f = x
	case float32:
		f = float64(x)
	case int:
		f = float64(x)
	case int64:
		f = float64(x)
	case int32:
		f = float64(x)
	case uint:
		f = float64(x)
	case uint64:
		f = float64(x)
	case uint32:
		f = float64(x)
	case json.Number:
		p, err := strconv.ParseFloat(string(x), 64)
		if err != nil {
			d["type"] = "string"
			return 0, invalid(ReasonNotNumeric, call(c)+" is not a number", d)
		}
		f = p
	default:
		d["type"] = kindOf(v)
		return 0, invalid(ReasonNotNumeric, call(c)+" is a "+d["type"].(string)+", not a number", d)
	}
	if math.IsNaN(f) {
		return 0, invalid(ReasonNull, call(c)+" is null (undefined): no row reached the aggregation, or its figure is undefined", d)
	}
	if math.IsInf(f, 0) {
		d["value"] = strconv.FormatFloat(f, 'g', -1, 64)
		return 0, invalid(ReasonNotFinite, call(c)+" is "+d["value"].(string)+", not a finite number", d)
	}
	return f, nil
}

// kindOf names a non-number value's JSON kind.
func kindOf(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case map[string]any:
		return "object"
	case []any, []float64, []string:
		return "array"
	}
	return fmt.Sprintf("%T", v)
}

// call renders c as written.
func call(c Call) string {
	return "result(" + strconv.Quote(c.Slot) + ", " + strconv.Quote(c.Aggregation) + ")"
}

func invalid(reason, what string, extra map[string]any) *errors.CodedError {
	d := make(map[string]any, len(extra)+1)
	for k, v := range extra {
		d[k] = v
	}
	d["reason"] = reason
	return errors.NewCodedErrorWithDetails(errors.PULSE_RESULT_INVALID, what, d)
}
