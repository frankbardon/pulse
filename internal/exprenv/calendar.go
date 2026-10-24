package exprenv

import (
	stderrors "errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
	exprast "github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/builtin"
	"github.com/expr-lang/expr/conf"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/internal/vecmath"
)

// Calendar helpers and log1p (U45, TODO #316).
//
// A `date` field binds as float64 epoch DAYS and a `datetime` field as
// float64 epoch SECONDS, so the two are indistinguishable by type in the
// env. The day helpers — weekday, month_index, date_trunc — therefore
// read epoch DAYS only, and to_day(ts) is the one bridge from an
// instant: the local epoch day a wall clock shows at ts in the request's
// zone (request `time_zone` → Options.DefaultTimeZone → UTC, the
// resolution ATTR_DATE_PART inherits). Every calendar computation runs
// in internal/temporal (TestNoZoneMathOutsideTemporal).
//
// The guard ([CalendarGuard]) closes the trap the shared float64 opens:
// a `datetime` field passed bare to a day helper (weekday(ts)) would
// silently read seconds as days, so it is refused at COMPILE with
// PULSE_FORMULA_COMPILE_ERROR naming to_day(); a literal date_trunc unit
// outside week / month / quarter / year is refused there too. Predict
// and the engine both append the guard over the schema, so the verdict
// is the same on both sides.
//
// A null argument (nil) answers nil — a null result — for every one of
// these helpers, log1p included; a non-finite number answers NaN.

// The calendar helper and log1p names.
const (
	FuncLog1p      = "log1p"
	FuncWeekday    = "weekday"
	FuncMonthIndex = "month_index"
	FuncDateTrunc  = "date_trunc"
	FuncToDay      = "to_day"
)

// ErrUnknownTruncUnit is the cause of [UnknownTruncUnit]: a date_trunc
// unit read at run time (not a literal) outside the accepted set.
var ErrUnknownTruncUnit = stderrors.New("unknown date_trunc unit")

// UnknownTruncUnit is the coded refusal of a date_trunc unit outside
// week / month / quarter / year met at run time (a unit computed from a
// field): PROCESSING_CONFIG {function, unit, valid}, cause
// [ErrUnknownTruncUnit]. A literal unit is refused at compile instead.
func UnknownTruncUnit(unit string) *errors.CodedError {
	ce := errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		truncUnitReason(unit),
		map[string]any{"function": FuncDateTrunc, "unit": unit, "valid": temporal.TruncUnits()})
	ce.Cause = ErrUnknownTruncUnit
	return ce
}

func truncUnitReason(unit string) string {
	quoted := make([]string, 0, 4)
	for _, u := range temporal.TruncUnits() {
		quoted = append(quoted, strconv.Quote(u))
	}
	return "date_trunc(): unit " + strconv.Quote(unit) + " is not one of " + strings.Join(quoted, ", ")
}

// calendarSignatures are the typed overloads the calendar helpers are
// declared with, so a string or a decimal argument fails at compile.
var calendarSignatures = map[string][]any{
	FuncWeekday:    {new(func(float64) float64)},
	FuncMonthIndex: {new(func(float64) float64)},
	FuncDateTrunc:  {new(func(string, float64) float64)},
	FuncToDay:      {new(func(float64) float64)},
}

// maxDayMagnitude bounds the epoch day a day helper reads (about 2.7
// million years either side of 1970); a larger one answers NaN rather
// than overflow the seconds a calendar reading converts to.
const maxDayMagnitude = 1e9

// maxSecondMagnitude bounds the epoch second to_day reads (2^53, the
// largest float64 every integer below is exact for).
const maxSecondMagnitude = 1 << 53

// calendarDay reads a day helper's epoch-day argument: floor of the
// number. ok is false (the answer is NaN) for a non-finite or
// out-of-range one.
func calendarDay(fn string, i int, a any) (day int64, ok bool, err error) {
	x, err := scalarArg(fn, i, a)
	if err != nil {
		return 0, false, err
	}
	if math.IsNaN(x) || math.Abs(x) > maxDayMagnitude {
		return 0, false, nil
	}
	return int64(math.Floor(x)), true, nil
}

// calendarUnary is weekday / month_index: nil for a nil day, NaN for an
// unreadable one, else k(day).
func calendarUnary(fn string, k func(int64) int64) exprBuiltinFunc {
	return func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, builtinArityError(fn, 1, len(args))
		}
		if args[0] == nil {
			return nil, nil
		}
		day, ok, err := calendarDay(fn, 0, args[0])
		if err != nil || !ok {
			return math.NaN(), err
		}
		return float64(k(day)), nil
	}
}

func dateTruncBuiltin(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, builtinArityError(FuncDateTrunc, 2, len(args))
	}
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	unit, isStr := args[0].(string)
	if !isStr {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("date_trunc(): argument 1 is %T, not a unit string", args[0]),
			map[string]any{"function": FuncDateTrunc, "argument": 1})
	}
	if !temporal.IsTruncUnit(unit) {
		return nil, UnknownTruncUnit(unit)
	}
	day, ok, err := calendarDay(FuncDateTrunc, 1, args[1])
	if err != nil || !ok {
		return math.NaN(), err
	}
	start, _ := temporal.TruncDay(unit, day)
	return float64(start), nil
}

// ToDayFunc is to_day() in zone z: the local epoch day of an instant
// (float64 epoch seconds, floored) on a wall clock in z (nil: UTC);
// nil for a nil instant, NaN for a non-finite or out-of-range one. The
// engine binds the request's resolved zone through [Bound.Zone]; the
// always-on registration is UTC.
func ToDayFunc(z *temporal.Zone) func(args ...any) (any, error) {
	if z == nil {
		z = temporal.UTC
	}
	return func(args ...any) (any, error) {
		if len(args) != 1 {
			return nil, builtinArityError(FuncToDay, 1, len(args))
		}
		if args[0] == nil {
			return nil, nil
		}
		x, err := scalarArg(FuncToDay, 0, args[0])
		if err != nil {
			return nil, err
		}
		if math.IsNaN(x) || math.Abs(x) > maxSecondMagnitude {
			return math.NaN(), nil
		}
		return float64(temporal.LocalDay(int64(math.Floor(x)), z)), nil
	}
}

// log1pBuiltin is log1p(x) = ln(1 + x), accurate for x near 0; nil for
// a nil x. The kernel is vecmath.Log1p (internal/detmath's portable
// port), the one FEAT_LOG computes with, so the two agree bit for bit.
func log1pBuiltin(args ...any) (any, error) {
	if len(args) != 1 {
		return nil, builtinArityError(FuncLog1p, 1, len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	x, err := scalarArg(FuncLog1p, 0, args[0])
	if err != nil {
		return nil, err
	}
	return vecmath.Log1p(x), nil
}

// calendarDayArg is the argument position of the epoch day each day
// helper reads — the one the datetime guard inspects.
var calendarDayArg = map[string]int{FuncWeekday: 0, FuncMonthIndex: 0, FuncDateTrunc: 1}

// calendarGuardPrefix names the per-refusal failure builtins the guard
// writes (one per refused call, each carrying its own reason).
const calendarGuardPrefix = "__pulse_calendar_refused_"

// CalendarGuard is the compile-time calendar check over schema, an
// expr.Option every expression compile appends (the engine's and
// predict's, so their verdicts agree). It refuses:
//
//   - a `datetime` schema field passed bare (an identifier; a `let`
//     cannot redeclare a field name) as the day of weekday /
//     month_index / date_trunc — it holds epoch seconds; to_day()
//     converts it;
//   - a literal date_trunc unit outside week / month / quarter / year.
//
// The refused call is wrapped in a failure builtin whose Validate
// raises the reason, so expr-lang's own check reports it at the call's
// location: the compile fails as PULSE_FORMULA_COMPILE_ERROR at every
// site. A nil schema checks only the unit.
func CalendarGuard(schema *encoding.Schema) expr.Option {
	return func(c *conf.Config) {
		c.Visitors = append(c.Visitors, &calendarGuard{conf: c, schema: schema})
	}
}

type calendarGuard struct {
	conf   *conf.Config
	schema *encoding.Schema
	n      int // failure builtins written so far
}

func (g *calendarGuard) Visit(node *exprast.Node) {
	if n, ok := (*node).(*exprast.CallNode); ok {
		g.visitCall(node, n)
	}
}

func (g *calendarGuard) visitCall(node *exprast.Node, n *exprast.CallNode) {
	id, ok := n.Callee.(*exprast.IdentifierNode)
	if !ok {
		return
	}
	pos, isDayHelper := calendarDayArg[id.Value]
	if !isDayHelper {
		return
	}
	if id.Value == FuncDateTrunc && len(n.Arguments) == 2 {
		if s, lit := n.Arguments[0].(*exprast.StringNode); lit && !temporal.IsTruncUnit(s.Value) {
			g.wrap(node, truncUnitReason(s.Value))
			return
		}
	}
	if pos >= len(n.Arguments) || g.schema == nil {
		return
	}
	arg, ok := n.Arguments[pos].(*exprast.IdentifierNode)
	if !ok {
		return
	}
	f := g.schema.Field(arg.Value)
	if f == nil || f.Type != encoding.FieldTypeDateTime {
		return
	}
	g.wrap(node, fmt.Sprintf(
		"%s() reads epoch days but %q is a datetime field (epoch seconds): wrap it in to_day(), as in %s(%sto_day(%s))",
		id.Value, arg.Value, id.Value, truncPrefix(id.Value, n), arg.Value))
}

// truncPrefix is the unit argument a date_trunc suggestion repeats.
func truncPrefix(fn string, n *exprast.CallNode) string {
	if fn != FuncDateTrunc {
		return ""
	}
	if s, ok := n.Arguments[0].(*exprast.StringNode); ok {
		return strconv.Quote(s.Value) + ", "
	}
	return "unit, "
}

// wrap replaces the call at node with a failure builtin over it whose
// Validate raises reason at the call's location.
func (g *calendarGuard) wrap(node *exprast.Node, reason string) {
	name := calendarGuardPrefix + strconv.Itoa(g.n)
	g.n++
	g.conf.Functions[name] = &builtin.Function{
		Name: name,
		Func: func(args ...any) (any, error) { return args[0], nil },
		Validate: func([]reflect.Type) (reflect.Type, error) {
			return nil, stderrors.New(reason)
		},
	}
	call := *node
	w := &exprast.CallNode{Callee: &exprast.IdentifierNode{Value: name}, Arguments: []exprast.Node{call}}
	w.SetLocation(call.Location())
	*node = w
}
