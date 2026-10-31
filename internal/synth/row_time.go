package synth

import (
	"fmt"
	"math"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"

	"github.com/frankbardon/pulse/internal/temporal"
)

// trend(t, slope), season(t, unit, weights), between(t, a, b) — the row
// environment's time-effect functions (U52, #363), built beside row()
// and lag() and, like them, consuming NO RNG: each reads the row value
// of a date or datetime field, the field's own declaration and, for a
// per-row zone, the zone the datetime stage resolved this row in.
//
// # What t is
//
// `t` is a BARE identifier naming a declared `date` or `datetime` field.
// The patcher (timePatcher) rewrites each such call onto an internal
// name whose first argument is the field NAME, so the function can read
// the field's null bit, its zone and its range start. trend alone also
// takes any number (`trend(row(), 0.01)` is slope × t); season and
// between over anything but a date / datetime field are refused at
// parse, because neither has a meaning over a bare number.
//
// # Which clock
//
// A `datetime` is read on its FIELD's clock: the fixed `time_zone`, the
// row's `time_zone_field` value as the datetime stage resolved it (so a
// rule that rewrote the zone field does not move the reading), or UTC for
// an unzoned field. A `date` is a calendar day and has no clock. So
// season(ts, "hour", …) is the LOCAL hour a uniform_datetime drew by its
// `hour_weights`, and between(ts, "2026-05-12", "2026-05-14") is those
// three LOCAL days, wherever each row is.
//
// # Semantics
//
//   - trend(t, slope) = slope × elapsed, where elapsed is days (fraction
//     included, on the field's clock) since local midnight of the field's
//     `start` param — so the field must draw with uniform_date or
//     uniform_datetime, which declare one. Over a number, elapsed is t.
//   - season(t, "dow"|"hour"|"month", weights) = weights[k] for the
//     reading's ISO weekday (7 weights, Monday first — the
//     `weekday_weights` order), local hour (24) or calendar month (12,
//     January first). "hour" over a `date` is refused.
//   - between(t, a, b) is true when a <= reading <= b. a and b are naive
//     literals (YYYY-MM-DD or a naive datetime, no offset) read on the
//     same clock; a DATE-ONLY b covers that whole day, so
//     between(ts, "2026-05-12", "2026-05-14") spans 12th 00:00:00 to 14th
//     23:59:59. Over a `date` both bounds must be dates.
//
// A null t returns null, as lag does: an unguarded null in arithmetic or
// a `when` is a loud row-time fault — guard with `!isnull(ts) && …`.
//
// The weights and bounds are literals checked at parse
// (timeCallShape), so a typo costs no generation, and the patcher bakes
// them into the call (bounds as integer wall readings, weights through a
// per-host table) so no row re-parses a literal.

const (
	trendFuncName   = "trend"
	seasonFuncName  = "season"
	betweenFuncName = "between"

	// The internal names the patcher rewrites a field-reading call onto.
	// The ':' cannot be lexed, so no author can call them.
	trendTimeFuncName   = "trend:time"
	seasonTimeFuncName  = "season:time"
	betweenTimeFuncName = "between:time"
)

// timeFuncNames are the expression-visible time functions.
var timeFuncNames = []string{trendFuncName, seasonFuncName, betweenFuncName}

func isTimeFuncName(name string) bool {
	return name == trendFuncName || name == seasonFuncName || name == betweenFuncName
}

// rowTimeKind is the row-time class of a date-family field.
type rowTimeKind int

const (
	rowTimeDate rowTimeKind = iota + 1
	rowTimeDatetime
)

// rowTimeField is what a time function needs from a field's declaration.
type rowTimeField struct {
	kind rowTimeKind
	// zone is a datetime's fixed zone, nil for UTC (or a per-row zone).
	zone      *temporal.Zone
	zoneField string
	// startDay is the field's declared `start` (uniform_date /
	// uniform_datetime), the origin trend counts from.
	startDay int64
	hasStart bool
}

// rowTimeFields derives every date / datetime field's time declaration
// from the spec. A zone or range the field's own sampler would refuse is
// left out here (nil zone / no start) — the sampler build refuses the
// spec before any row is drawn, so nothing is ever generated against the
// fallback.
func rowTimeFields(fields []FieldSpec) map[string]rowTimeField {
	var out map[string]rowTimeField
	for _, f := range fields {
		var tf rowTimeField
		switch f.Type {
		case "date":
			tf.kind = rowTimeDate
		case datetimeFieldType:
			tf.kind = rowTimeDatetime
		default:
			continue
		}
		switch f.Distribution {
		case DistUniformDate, DistUniformDateTime:
			if start, _, err := parseDayRange(f, f.Distribution); err == nil {
				tf.startDay, tf.hasStart = start, true
			}
		}
		if tf.kind == rowTimeDatetime && f.Distribution == DistUniformDateTime {
			if z, zf, err := datetimeZoneParams(f); err == nil {
				tf.zone, tf.zoneField = z, zf
			}
		}
		if out == nil {
			out = make(map[string]rowTimeField)
		}
		out[f.Name] = tf
	}
	return out
}

// seasonUnit is one season() unit: its weight count.
var seasonUnits = map[string]int{"dow": 7, "hour": 24, "month": 12}

// seasonTable is one season() call's unit and weights, baked at patch.
type seasonTable struct {
	unit    string
	weights []float64
}

// timeCall is a well-formed time call's resolved literals.
type timeCall struct {
	field   string // "" for trend over a number
	season  seasonTable
	lo, hi  int64 // between: wall seconds (datetime) or epoch days (date)
	numeric bool  // trend over a non-field number
}

// timeCallShape checks a time call's shape against the declared fields
// and resolves its literals. A non-nil refusal says why it is invalid.
func timeCallShape(call *ast.CallNode, name string, declared map[string]bool, times map[string]rowTimeField) (timeCall, *rowExprRefusal) {
	var tc timeCall
	refuse := func(format string, a ...any) (timeCall, *rowExprRefusal) {
		return timeCall{}, &rowExprRefusal{function: name, field: tc.field, msg: fmt.Sprintf(format, a...)}
	}
	want := 3
	usage := "between(t, a, b)"
	switch name {
	case trendFuncName:
		want, usage = 2, "trend(t, slope)"
	case seasonFuncName:
		usage = `season(t, "dow"|"hour"|"month", [weights])`
	}
	if len(call.Arguments) != want {
		return refuse("%s takes %d arguments, got %d", usage, want, len(call.Arguments))
	}
	id, isIdent := call.Arguments[0].(*ast.IdentifierNode)
	var tf rowTimeField
	if isIdent && declared[id.Value] {
		tf = times[id.Value]
		if tf.kind != 0 {
			tc.field = id.Value
		}
	}
	if tc.field == "" {
		if name == trendFuncName {
			tc.numeric = true
			return tc, nil
		}
		return refuse("%s's first argument must name a declared date or datetime field, e.g. %s", name, usage)
	}
	switch name {
	case trendFuncName:
		if !tf.hasStart {
			return refuse("trend(%s, slope) counts days from the field's start, and %q declares none; draw it with uniform_date / uniform_datetime, or pass a number (trend(row(), slope))", tc.field, tc.field)
		}
	case seasonFuncName:
		unit, ok := call.Arguments[1].(*ast.StringNode)
		if !ok || seasonUnits[unit.Value] == 0 {
			return refuse(`season's unit must be the literal "dow", "hour" or "month"`)
		}
		if unit.Value == "hour" && tf.kind == rowTimeDate {
			return refuse(`season(%s, "hour", …): %q is a date and has no hour; use "dow" or "month", or a datetime field`, tc.field, tc.field)
		}
		n := seasonUnits[unit.Value]
		arr, ok := call.Arguments[2].(*ast.ArrayNode)
		if !ok || len(arr.Nodes) != n {
			return refuse(`season(%s, %q, weights): weights must be a literal list of exactly %d numbers`, tc.field, unit.Value, n)
		}
		w := make([]float64, n)
		for i, node := range arr.Nodes {
			v, ok := numberLiteral(node)
			if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
				return refuse(`season(%s, %q, weights): weights[%d] must be a finite number literal`, tc.field, unit.Value, i)
			}
			w[i] = v
		}
		tc.season = seasonTable{unit: unit.Value, weights: w}
	case betweenFuncName:
		var bounds [2]int64
		for i, arg := range call.Arguments[1:] {
			lit, ok := arg.(*ast.StringNode)
			if !ok {
				return refuse("between(%s, a, b): a and b must be string literals (YYYY-MM-DD or a naive datetime)", tc.field)
			}
			wall, dateOnly, ok := temporal.ParseWall(lit.Value)
			if !ok {
				return refuse("between(%s, a, b): %q is not YYYY-MM-DD or a naive datetime (an offset or Z names an instant; the bound is read on the field's own clock)", tc.field, lit.Value)
			}
			switch {
			case tf.kind == rowTimeDate && !dateOnly:
				return refuse("between(%s, a, b): %q is a date, so its bounds must be dates; %q carries a time", tc.field, tc.field, lit.Value)
			case tf.kind == rowTimeDate:
				bounds[i] = temporal.DateTimeToDay(wall)
			case i == 1 && dateOnly:
				bounds[i] = temporal.DayEndWall(temporal.DateTimeToDay(wall))
			default:
				bounds[i] = wall
			}
		}
		if bounds[1] < bounds[0] {
			return refuse("between(%s, a, b): b is before a", tc.field)
		}
		tc.lo, tc.hi = bounds[0], bounds[1]
	}
	return tc, nil
}

// numberLiteral reads a (possibly negated) integer or float literal.
func numberLiteral(n ast.Node) (float64, bool) {
	switch x := n.(type) {
	case *ast.IntegerNode:
		return float64(x.Value), true
	case *ast.FloatNode:
		return x.Value, true
	case *ast.UnaryNode:
		if x.Operator == "-" {
			if v, ok := numberLiteral(x.Node); ok {
				return -v, true
			}
		}
	}
	return 0, false
}

// timePatcher rewrites each well-formed field-reading time call onto
// its internal name: (field name, field value, baked literals...). A
// call it leaves alone is either trend over a number (registered under
// the public name) or malformed — season / between are not registered
// publicly, so a malformed call cannot compile anywhere, and the walker
// refuses it first with its reason.
type timePatcher struct {
	declared map[string]bool
	times    map[string]rowTimeField
	host     *compiledConstraints
}

func (p timePatcher) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	if !ok || !isTimeFuncName(callee.Value) || p.declared[callee.Value] {
		return
	}
	tc, refusal := timeCallShape(call, callee.Value, p.declared, p.times)
	if refusal != nil || tc.numeric {
		return
	}
	ident := call.Arguments[0]
	name := &ast.StringNode{Value: tc.field}
	name.SetLocation(ident.Location())
	args := []ast.Node{name, ident}
	var internal string
	switch callee.Value {
	case trendFuncName:
		internal = trendTimeFuncName
		args = append(args, call.Arguments[1])
	case seasonFuncName:
		internal = seasonTimeFuncName
		p.host.seasons = append(p.host.seasons, tc.season)
		args = append(args, intLiteral(len(p.host.seasons)-1, ident))
	case betweenFuncName:
		internal = betweenTimeFuncName
		args = append(args, intLiteral(int(tc.lo), ident), intLiteral(int(tc.hi), ident))
	}
	// A NEW call node, not an in-place rename: the checker types the
	// tree before each patcher runs, and `trend` is registered publicly,
	// so the old callee already carries the public signature's type.
	fn := &ast.IdentifierNode{Value: internal}
	fn.SetLocation(callee.Location())
	patched := &ast.CallNode{Callee: fn, Arguments: args}
	patched.SetLocation(call.Location())
	ast.Patch(node, patched)
}

func intLiteral(v int, at ast.Node) ast.Node {
	n := &ast.IntegerNode{Value: v}
	n.SetLocation(at.Location())
	return n
}

// timeFunctionOptions registers the time functions on host.
func timeFunctionOptions(host *compiledConstraints) []expr.Option {
	return []expr.Option{
		expr.Function(trendFuncName, trendNumberBuiltin, new(func(float64, float64) float64)),
		expr.Function(trendTimeFuncName, host.trendTimeBuiltin, new(func(string, float64, float64) float64)),
		expr.Function(seasonTimeFuncName, host.seasonTimeBuiltin, new(func(string, float64, int) float64)),
		expr.Function(betweenTimeFuncName, host.betweenTimeBuiltin, new(func(string, float64, int, int) bool)),
	}
}

// numberParam reads an expression number (an int literal or row() reach
// a float64-typed parameter unconverted).
func numberParam(fn string, v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	}
	return 0, fmt.Errorf("%s expects a number, got %T", fn, v)
}

// trendNumberBuiltin is trend over a plain number: slope × t.
func trendNumberBuiltin(params ...any) (any, error) {
	if len(params) != 2 {
		return nil, fmt.Errorf("%s expects 2 arguments, got %d", trendFuncName, len(params))
	}
	t, err := numberParam(trendFuncName, params[0])
	if err != nil {
		return nil, err
	}
	slope, err := numberParam(trendFuncName, params[1])
	if err != nil {
		return nil, err
	}
	return float64(slope * t), nil
}

// timeReading resolves a field-reading call's (name, value) pair: the
// field's declaration, and — for a datetime — the instant rounded the
// way the writer stores it and the clock it is read on. null reports a
// null field (the call then returns null).
func (c *compiledConstraints) timeReading(fn string, params []any) (tf rowTimeField, value float64, sec int64, zone *temporal.Zone, null bool, err error) {
	name, ok := params[0].(string)
	if !ok {
		return tf, 0, 0, nil, false, fmt.Errorf("%s expects a field name, got %T", fn, params[0])
	}
	tf, ok = c.time[name]
	if !ok {
		return tf, 0, 0, nil, false, fmt.Errorf("%s: %q is not a date or datetime field", fn, name)
	}
	if c.nullMask[name] {
		return tf, 0, 0, nil, true, nil
	}
	value, err = numberParam(fn, params[1])
	if err != nil {
		return tf, 0, 0, nil, false, err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > maxDatetimeSeconds {
		return tf, 0, 0, nil, false, fmt.Errorf("%s(%s): %v is not a representable reading", fn, name, value)
	}
	sec = int64(math.Floor(value + 0.5))
	if tf.kind != rowTimeDatetime {
		return tf, value, sec, nil, false, nil
	}
	zone = tf.zone
	if tf.zoneField != "" {
		z, ok := c.datetimes.rowZoneOf(name)
		if !ok {
			return tf, 0, 0, nil, false, fmt.Errorf("%s(%s): no zone was resolved for this row", fn, name)
		}
		zone = z
	}
	if zone == nil {
		zone = temporal.UTC
	}
	return tf, value, sec, zone, false, nil
}

// trendTimeBuiltin answers trend over a date / datetime field.
func (c *compiledConstraints) trendTimeBuiltin(params ...any) (any, error) {
	if len(params) != 3 {
		return nil, fmt.Errorf("%s expects 2 arguments, got %d", trendFuncName, len(params)-1)
	}
	tf, value, sec, zone, null, err := c.timeReading(trendFuncName, params)
	if err != nil || null {
		return nil, err
	}
	slope, err := numberParam(trendFuncName, params[2])
	if err != nil {
		return nil, err
	}
	var elapsed float64
	if tf.kind == rowTimeDate {
		elapsed = value - float64(tf.startDay)
	} else {
		elapsed = temporal.ElapsedDays(temporal.DayToUnix(tf.startDay), temporal.LocalWall(sec, zone))
	}
	return float64(slope * elapsed), nil
}

// seasonTimeBuiltin answers season: the weight of the reading's weekday,
// hour or month.
func (c *compiledConstraints) seasonTimeBuiltin(params ...any) (any, error) {
	if len(params) != 3 {
		return nil, fmt.Errorf("%s expects 3 arguments, got %d", seasonFuncName, len(params)-1)
	}
	tf, _, sec, zone, null, err := c.timeReading(seasonFuncName, params)
	if err != nil || null {
		return nil, err
	}
	idx, ok := params[2].(int)
	if !ok || idx < 0 || idx >= len(c.seasons) {
		return nil, fmt.Errorf("%s: no compiled weights", seasonFuncName)
	}
	st := c.seasons[idx]
	var k int64
	if tf.kind == rowTimeDate {
		switch st.unit {
		case "dow":
			k = temporal.ISOWeekday(sec) - 1
		case "month":
			k = temporal.MonthIndex(sec) % 12
		}
	} else {
		p := temporal.LocalParts(sec, zone)
		switch st.unit {
		case "dow":
			k = temporal.ISOWeekdayOf(p.Weekday) - 1
		case "hour":
			k = int64(p.Hour)
		case "month":
			k = int64(p.Month) - 1
		}
	}
	return st.weights[k], nil
}

// betweenTimeBuiltin answers between: lo <= reading <= hi on the field's
// clock (a date compares days).
func (c *compiledConstraints) betweenTimeBuiltin(params ...any) (any, error) {
	if len(params) != 4 {
		return nil, fmt.Errorf("%s expects 3 arguments, got %d", betweenFuncName, len(params)-2)
	}
	tf, _, sec, zone, null, err := c.timeReading(betweenFuncName, params)
	if err != nil || null {
		return nil, err
	}
	lo, ok1 := params[2].(int)
	hi, ok2 := params[3].(int)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("%s: no compiled bounds", betweenFuncName)
	}
	r := sec
	if tf.kind == rowTimeDatetime {
		r = temporal.LocalWall(sec, zone)
	}
	return r >= int64(lo) && r <= int64(hi), nil
}
