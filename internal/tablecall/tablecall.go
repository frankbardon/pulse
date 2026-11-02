// Package tablecall finds the lookup(table, keys...) and
// range_label(table, x[, unmatched]) calls an expression makes with
// literal arguments and judges them against the instance's named tables
// — the static half of the two table readers, shared by the
// no-execute field-reference pass (internal/descriptor: predict and the
// runtime's entry check), the OVERLAY_FORMULA gate (internal/
// formulaident, which predict and the runtime overlay dispatchers both
// run) and the optimize block compile (internal/optblock), so every
// side refuses the same call with the same code, message and details.
// The refusal constructors are the evaluating builtins' own
// (internal/processing), so an evaluated call and a judged one agree to
// the byte. A call with a computed argument is left to evaluation.
//
// The checks, each the code the evaluation raises:
//
//   - lookup with a literal table the instance does not register:
//     PULSE_LOOKUP_TABLE_UNKNOWN {table};
//   - lookup whose key arguments are ALL literals (string, number,
//     bool, nil) against a table declaring key axes: a nil key is
//     PULSE_LOOKUP_MISS {table, null_key}; a joined key with the wrong
//     number of parts, or a part off its axis, is PULSE_LOOKUP_MISS
//     {table, keys} — every Rows key of a table with axes is one value
//     per axis, each on its axis, and no axis value holds the "|"
//     separator (pulse.New), so such a key can never hit;
//   - range_label with a literal table the instance does not register
//     (or hides with capability:range_tables): PULSE_RANGE_TABLE_UNKNOWN
//     {operator, table};
//   - range_label over a value whose static type no table kind matches
//     (a string, a bool, a list): PULSE_RANGE_TABLE_KIND_MISMATCH
//     {operator, table, want, got}.
//
// lookup is judged only when the instance registers a LookupTable (it
// is the built-in then; otherwise the name is undeclared, or an
// embedder function's).
package tablecall

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/daterange"
	"github.com/frankbardon/pulse/internal/exprenv"
)

// KeySeparator joins lookup() key arguments into a Rows key.
const KeySeparator = "|"

// Catalog is the instance's named tables as a static check sees them.
type Catalog interface {
	// HasLookup reports whether lookup() is the built-in (the instance
	// registers at least one LookupTable).
	HasLookup() bool
	// LookupAxes returns the registered lookup table's declared axis
	// values, one slice per axis in argument order (nil: the table
	// declares no axes; a nil entry: that axis's values are unknown and
	// it is not judged); ok false when no such table is registered.
	LookupAxes(table string) (axes [][]string, ok bool)
	// RangeKind returns the range table's kind (daterange.KindNumeric /
	// KindDate); ok false when it is unknown or hidden.
	RangeKind(table string) (kind string, ok bool)
}

// SourceRefusal is Refusal over src's parse tree (nil when src does not
// parse: the compile reports it). Only literal nodes are typed there.
func SourceRefusal(src string, cat Catalog) *errors.CodedError {
	if cat == nil || !mayCall(src) {
		return nil
	}
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	return Refusal(tree.Node, cat)
}

// Refusal is the first refusal over node's literal lookup() and
// range_label() calls, in walk order (an argument's calls before the
// call itself, left before right); nil when every call passes. node may
// be a parse tree or a checked program's tree (vm.Program.Node), whose
// typed nodes let the range_label value check see a field's type.
func Refusal(node exprast.Node, cat Catalog) *errors.CodedError {
	if cat == nil || node == nil {
		return nil
	}
	v := &visitor{cat: cat}
	exprast.Walk(&node, v)
	return v.out
}

// mayCall reports whether src may call either reader.
func mayCall(src string) bool {
	return strings.Contains(src, exprenv.LookupFunc) || strings.Contains(src, exprenv.RangeLabelFunc)
}

type visitor struct {
	cat Catalog
	out *errors.CodedError
}

func (v *visitor) Visit(node *exprast.Node) {
	if v.out != nil {
		return
	}
	n, ok := (*node).(*exprast.CallNode)
	if !ok {
		return
	}
	id, ok := n.Callee.(*exprast.IdentifierNode)
	if !ok {
		return
	}
	switch id.Value {
	case exprenv.LookupFunc:
		if v.cat.HasLookup() {
			v.out = v.lookup(n.Arguments)
		}
	case exprenv.RangeLabelFunc:
		v.out = v.rangeLabel(n.Arguments)
	}
}

// lookup judges one lookup(table, keys...) call.
func (v *visitor) lookup(args []exprast.Node) *errors.CodedError {
	if len(args) == 0 {
		return nil
	}
	tv, ok := literal(args[0])
	table, isStr := tv.(string)
	if !ok || !isStr {
		return nil
	}
	axes, ok := v.cat.LookupAxes(table)
	if !ok {
		return UnknownLookup(table)
	}
	keys := make([]string, 0, len(args)-1)
	for i, a := range args[1:] {
		k, ok := literal(a)
		if !ok {
			return nil
		}
		if k == nil {
			return LookupNullKey(table, i)
		}
		keys = append(keys, fmt.Sprint(k))
	}
	if axes == nil {
		return nil
	}
	parts := strings.Split(strings.Join(keys, KeySeparator), KeySeparator)
	if len(parts) != len(axes) {
		return LookupMiss(table, keys)
	}
	for i, p := range parts {
		if axes[i] != nil && !contains(axes[i], p) {
			return LookupMiss(table, keys)
		}
	}
	return nil
}

// rangeLabel judges one range_label(table, x[, unmatched]) call.
func (v *visitor) rangeLabel(args []exprast.Node) *errors.CodedError {
	if len(args) < 2 || len(args) > 3 {
		return nil
	}
	tv, ok := literal(args[0])
	table, isStr := tv.(string)
	if !ok || !isStr {
		return nil
	}
	kind, ok := v.cat.RangeKind(table)
	if !ok {
		return UnknownRange(table)
	}
	if got := mismatchKind(args[1]); got != "" {
		return KindMismatch(table, kind, got)
	}
	return nil
}

// literal returns node's value when it is a literal (as the evaluated
// argument binds it), false otherwise.
func literal(node exprast.Node) (any, bool) {
	switch n := node.(type) {
	case *exprast.StringNode:
		return n.Value, true
	case *exprast.IntegerNode:
		return n.Value, true
	case *exprast.FloatNode:
		return n.Value, true
	case *exprast.BoolNode:
		return n.Value, true
	case *exprast.NilNode:
		return nil, true
	case *exprast.ConstantNode:
		switch n.Value.(type) {
		case nil, string, bool, int, int64, float64:
			return n.Value, true
		}
	}
	return nil, false
}

var (
	stringType   = reflect.TypeOf("")
	boolType     = reflect.TypeOf(false)
	stringsType  = reflect.TypeOf([]string(nil))
	anysType     = reflect.TypeOf([]any(nil))
	float64sType = reflect.TypeOf([]float64(nil))
)

// mismatchKind names the kind of a range_label value no table kind
// matches — "string", "bool", "list", or a slice's Go type, as the
// evaluating builtin names it — or "" when the value's type is numeric,
// untyped or not visible.
func mismatchKind(node exprast.Node) string {
	switch node.(type) {
	case *exprast.StringNode:
		return "string"
	case *exprast.BoolNode:
		return "bool"
	}
	// An unchecked node (a parse tree) reads as interface: not visible.
	t := node.Type()
	if t == nil || t.Kind() == reflect.Interface {
		return ""
	}
	switch {
	case t == stringType:
		return "string"
	case t == boolType:
		return "bool"
	case t == stringsType, t == anysType, t == float64sType:
		return "list"
	case t.Kind() == reflect.Slice:
		return t.String()
	}
	return ""
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

// UnknownLookup is lookup()'s PULSE_LOOKUP_TABLE_UNKNOWN for table.
func UnknownLookup(table string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_TABLE_UNKNOWN,
		"lookup table "+strconv.Quote(table)+" is not registered",
		map[string]any{"table": table})
}

// LookupNullKey is lookup()'s PULSE_LOOKUP_MISS for a null key at
// position i (0-based among the keys, the table name not counted).
func LookupNullKey(table string, i int) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_MISS,
		"lookup("+strconv.Quote(table)+", ...) key "+strconv.Itoa(i)+" (0-based, after the table name) is null",
		map[string]any{"table": table, "null_key": i})
}

// LookupMiss is lookup()'s PULSE_LOOKUP_MISS for keys (each stringified
// as the builtin does, %v) no row holds.
func LookupMiss(table string, keys []string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_MISS,
		"lookup("+strconv.Quote(table)+", "+strings.Join(keys, ", ")+") returned no result",
		map[string]any{"table": table, "keys": keys})
}

// UnknownRange is range_label()'s PULSE_RANGE_TABLE_UNKNOWN for table
// (the shared range-table resolver's message with operator
// range_label).
func UnknownRange(table string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_TABLE_UNKNOWN,
		exprenv.RangeLabelFunc+": unknown range table "+strconv.Quote(table),
		map[string]any{"operator": exprenv.RangeLabelFunc, "table": table})
}

// KindMismatch is range_label()'s PULSE_RANGE_TABLE_KIND_MISMATCH: a
// value of kind got against table, a want table.
func KindMismatch(table, want, got string) *errors.CodedError {
	reads := "a number"
	if want == daterange.KindDate {
		reads = "an epoch-day number (a date field, or to_day(datetime))"
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_TABLE_KIND_MISMATCH,
		exprenv.RangeLabelFunc+": range table "+strconv.Quote(table)+" is a "+want+" table; it matches "+reads+", got a "+got,
		map[string]any{"operator": exprenv.RangeLabelFunc, "table": table, "want": want, "got": got})
}
