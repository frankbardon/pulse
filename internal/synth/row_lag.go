package synth

import (
	"fmt"
	"maps"
	"sort"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
)

// lag(name, n[, default[, partition]]) — the synth row environment's
// one cross-row read (U58; the partition argument #572).
//
// It returns the FINAL value of declared field `name` on the n-th
// previous ACCEPTED row: the value the row map held after the rule pass
// (the value a same-row expression would have read off that row, before
// encodeRow rounds an integer field or narrows an f32), typed the way
// the field reads in an expression — float64 for every scalar, string
// for a categorical, map[string]bool for a set, []float64 for a vector.
// While fewer than n rows have been accepted it returns `default`, or
// null when no default is given; a lagged row whose field was null also
// returns null (SQL LAG's split: the default stands in for a row that
// does not exist, never for a null that does). An unguarded null in
// arithmetic is a loud row-time fault, never a silent 0 — write a
// default or `?? x`.
//
// # Why the arguments are literals, checked at parse
//
// `name` must be a string literal naming a declared field and `n` an
// integer literal from 1 to maxLagOffset. Both are what sizes the state
// (lagBuffer keeps only the lagged fields, and only max n rows of them),
// so both must be known before the first row; a computed name or offset
// would need every field of every earlier row kept. The refusals are
// PULSE_SYNTH_LAG_FIELD_INVALID and PULSE_SYNTH_LAG_OFFSET_INVALID, raised
// by rowExprWalker, so they fire wherever the other parse-settled
// refusals do: validateRules, validateSpec's constraint walk, and again
// at compileRules / compileConstraints for a Spec that bypassed
// validation.
//
// # Determinism
//
// It consumes no RNG: it reads a buffer generate commits AFTER a row is
// accepted and encoded (beside ruleApplier.commitRow), so a rejected
// attempt never enters it and a spec that does not call lag carries no
// buffer at all — byte-identical to output from before lag existed.
// Self-lag (`ad: spend + 0.6 * lag("ad", 1, 0)`) is well defined: the
// previous row was complete before this one started.
//
// # Typing
//
// expr-lang types a function once per name, but lag's return type is
// the TARGET field's. lagPatcher therefore rewrites every well-formed
// call, before the type check, onto one of four internal names
// (lagFuncNames) whose signatures carry that class, so validateRuleExpr
// sees `lag("ad", 1, 0)` as float64 and the coercion matrix's parse-time
// half keeps working. The internal names contain a ':' the lexer cannot
// produce, so no author can call them; the bare name `lag` itself is NOT
// registered, so a call the patcher did not rewrite (an invalid shape
// the walker refuses first on every spec path) cannot compile anywhere
// — the --suggest-rules detectors, which only compile, accept exactly
// what the loader accepts.
//
// # Partitions
//
// A 4th argument, a string literal naming a declared categorical_*
// field, keys the history by that field's value: `lag("spend", 1, 0,
// "geo")` reads spend from the previous accepted row whose geo equals
// this row's geo, and returns the default on the first row of each geo.
// Each distinct value (null is its own value, SQL PARTITION BY's rule)
// gets its own ring, created on that value's first committed row and
// filled ONLY by rows carrying it. `nil` is accepted as the default in
// this form (the patcher drops it) so a partitioned read can still return
// null before n rows exist.
//
// The read keys on the partition's value on the CURRENT row, so that
// value must be final when the call runs: a rule at or after the one
// holding the call that writes the partition (set, set_expr, set_null, a
// non-gate null_together member) would commit the row under a different
// key than the one the read used, and the series would silently cross.
// lagPartitionRefusal refuses it, with the shape faults (not a literal,
// undeclared, not categorical), as PULSE_SYNTH_LAG_PARTITION_INVALID. A
// constraint runs after every rule, so its partition is always final.
//
// Nothing orders the partitions relative to each other: rows interleave
// in whatever order the draws produce them, and a partition's history is
// its rows in accepted order. A spec without a partition argument builds
// no partition state and is byte-identical to before the argument
// existed.

// lagFuncName is the expression-visible name.
const lagFuncName = "lag"

// maxLagOffset is the largest n lag accepts. lagBuffer holds max-n rows
// of each lagged field, so the cap bounds that state at 1024 values per
// lagged field — four years of weekly rows, nearly three of daily ones —
// far beyond any adstock or AR horizon, while a typo of 10000000 is
// refused rather than allocated.
const maxLagOffset = 1024

// lagClass is the row-shape class of a lagged field.
type lagClass int

const (
	lagNumber lagClass = iota
	lagCategory
	lagSet
	lagVector
)

// lagFuncNames are the internal, per-class function names lagPatcher
// rewrites a call onto.
var lagFuncNames = [...]string{
	lagNumber:   "lag:number",
	lagCategory: "lag:category",
	lagSet:      "lag:set",
	lagVector:   "lag:vector",
}

// lagPartFuncNames are the partitioned twins: the call carries the
// partition field's name and its current value as two trailing
// arguments, after the default when one is given.
var lagPartFuncNames = [...]string{
	lagNumber:   "lag:number:part",
	lagCategory: "lag:category:part",
	lagSet:      "lag:set:part",
	lagVector:   "lag:vector:part",
}

// lagClassOf derives the class from the env sentinel (sentinelFor), the
// one place a field's row shape is decided.
func lagClassOf(sentinel any) lagClass {
	switch sentinel.(type) {
	case string:
		return lagCategory
	case map[string]bool:
		return lagSet
	case []float64:
		return lagVector
	default:
		return lagNumber
	}
}

// lagFunctionOptions registers the four typed internal functions on host.
// The default's parameter is typed for number and category, so
// `lag("region", 1, 0)` is a compile refusal; set and vector defaults are
// `any` because expr cannot write a map[string]bool or []float64 literal
// (an option list `["a"]` or `[0.5, 1]` is converted at run time).
func lagFunctionOptions(host *compiledConstraints) []expr.Option {
	return []expr.Option{
		expr.Function(lagFuncNames[lagNumber], host.lagBuiltin,
			new(func(string, int) float64), new(func(string, int, float64) float64)),
		expr.Function(lagFuncNames[lagCategory], host.lagBuiltin,
			new(func(string, int) string), new(func(string, int, string) string)),
		expr.Function(lagFuncNames[lagSet], host.lagBuiltin,
			new(func(string, int) map[string]bool), new(func(string, int, any) map[string]bool)),
		expr.Function(lagFuncNames[lagVector], host.lagBuiltin,
			new(func(string, int) []float64), new(func(string, int, any) []float64)),
		// The partitioned twins: (name, n, partition, value) for a nil
		// default, (name, n, default, partition, value) otherwise. The
		// value is `any` so a non-categorical partition reaches
		// lagPartitionRefusal's coded error rather than a type fault.
		expr.Function(lagPartFuncNames[lagNumber], host.lagBuiltin,
			new(func(string, int, string, any) float64), new(func(string, int, float64, string, any) float64)),
		expr.Function(lagPartFuncNames[lagCategory], host.lagBuiltin,
			new(func(string, int, string, any) string), new(func(string, int, string, string, any) string)),
		expr.Function(lagPartFuncNames[lagSet], host.lagBuiltin,
			new(func(string, int, string, any) map[string]bool), new(func(string, int, any, string, any) map[string]bool)),
		expr.Function(lagPartFuncNames[lagVector], host.lagBuiltin,
			new(func(string, int, string, any) []float64), new(func(string, int, any, string, any) []float64)),
	}
}

// lagCallShape reports whether call is a well-formed lag call over the
// declared fields: 2 to 4 arguments, a string-literal declared field, an
// integer literal in range and, with 4, a string-literal declared
// partition field. A non-nil refusal says why not. The partition's TYPE
// and its ordering against the rules are lagPartitionRefusal's: the
// shared walker sees names, not types.
func lagCallShape(call *ast.CallNode, declared map[string]bool) (field string, n int, refusal *rowExprRefusal) {
	field, n, _, refusal = lagCallShapePart(call, declared)
	return field, n, refusal
}

// lagCallShapePart is lagCallShape returning the partition field too
// ("" when the call has none).
func lagCallShapePart(call *ast.CallNode, declared map[string]bool) (field string, n int, partition string, refusal *rowExprRefusal) {
	if len(call.Arguments) < 2 || len(call.Arguments) > 4 {
		return "", 0, "", &rowExprRefusal{function: lagFuncName, msg: fmt.Sprintf(
			"lag takes (name, n), (name, n, default) or (name, n, default, partition), got %d argument(s)", len(call.Arguments))}
	}
	lit, ok := call.Arguments[0].(*ast.StringNode)
	if !ok {
		return "", 0, "", &rowExprRefusal{code: errors.PULSE_SYNTH_LAG_FIELD_INVALID, function: lagFuncName, msg: "lag's first argument must be a string literal naming a declared field, e.g. lag(\"spend\", 1)"}
	}
	if !declared[lit.Value] {
		return "", 0, "", &rowExprRefusal{code: errors.PULSE_SYNTH_LAG_FIELD_INVALID, function: lagFuncName, field: lit.Value, msg: fmt.Sprintf(
			"lag names field %q, which the spec does not declare", lit.Value)}
	}
	num, ok := call.Arguments[1].(*ast.IntegerNode)
	if !ok || num.Value < 1 || num.Value > maxLagOffset {
		return "", 0, "", &rowExprRefusal{code: errors.PULSE_SYNTH_LAG_OFFSET_INVALID, function: lagFuncName, field: lit.Value, msg: fmt.Sprintf(
			"lag(%q, n): n must be an integer literal from 1 to %d", lit.Value, maxLagOffset)}
	}
	if len(call.Arguments) == 4 {
		part, ok := call.Arguments[3].(*ast.StringNode)
		if !ok {
			return "", 0, "", &rowExprRefusal{code: errors.PULSE_SYNTH_LAG_PARTITION_INVALID, function: lagFuncName, field: lit.Value, msg: fmt.Sprintf(
				"lag(%q, n, default, partition): the partition must be a string literal naming a declared categorical field, e.g. lag(%q, 1, 0, \"geo\")", lit.Value, lit.Value)}
		}
		if !declared[part.Value] {
			return "", 0, "", &rowExprRefusal{code: errors.PULSE_SYNTH_LAG_PARTITION_INVALID, function: lagFuncName, field: lit.Value, msg: fmt.Sprintf(
				"lag(%q, n, default, %q): the partition names a field the spec does not declare", lit.Value, part.Value)}
		}
		return lit.Value, num.Value, part.Value, nil
	}
	return lit.Value, num.Value, "", nil
}

// lagPatcher rewrites each well-formed lag call onto its field's typed
// internal name. A call it leaves alone keeps the unregistered name and
// fails to compile.
type lagPatcher struct {
	classes  map[string]lagClass
	declared map[string]bool
}

func (p lagPatcher) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	// A declared field named `lag` shadows the built-in (the walker
	// refuses the call); leaving it alone keeps that refusal's shape.
	if !ok || callee.Value != lagFuncName || p.declared[lagFuncName] {
		return
	}
	field, _, partition, refusal := lagCallShapePart(call, p.declared)
	if refusal != nil {
		return
	}
	if partition == "" {
		callee.Value = lagFuncNames[p.classes[field]]
		return
	}
	// The partitioned twin: append the partition field's CURRENT value
	// (a bare read of it) after its name, and drop a nil default so the
	// typed overloads need no nil-accepting parameter.
	callee.Value = lagPartFuncNames[p.classes[field]]
	ident := &ast.IdentifierNode{Value: partition}
	ident.SetLocation(call.Arguments[3].Location())
	args := []ast.Node{call.Arguments[0], call.Arguments[1]}
	if _, isNil := call.Arguments[2].(*ast.NilNode); !isNil {
		args = append(args, call.Arguments[2])
	}
	call.Arguments = append(args, call.Arguments[3], ident)
}

// lagPatcherFor builds the patcher from the env sentinels.
func lagPatcherFor(env map[string]any, names map[string]bool) lagPatcher {
	classes := make(map[string]lagClass, len(env))
	for name, sentinel := range env {
		classes[name] = lagClassOf(sentinel)
	}
	return lagPatcher{classes: classes, declared: names}
}

// lagBuiltin answers every lag call compiled against host. The patcher
// and the walker guarantee (string literal, int literal in range); the
// checks below are the backstop for a Spec that reached the row loop
// some other way.
//
// The patched arities: 2 (name, n), 3 (name, n, default), 4 (name, n,
// partition, value) and 5 (name, n, default, partition, value).
func (c *compiledConstraints) lagBuiltin(params ...any) (any, error) {
	if len(params) < 2 || len(params) > 5 {
		return nil, fmt.Errorf("%s expects 2 to 5 arguments, got %d", lagFuncName, len(params))
	}
	name, ok := params[0].(string)
	if !ok {
		return nil, fmt.Errorf("%s expects a field name, got %T", lagFuncName, params[0])
	}
	n, ok := params[1].(int)
	if !ok {
		return nil, fmt.Errorf("%s expects an integer offset, got %T", lagFuncName, params[1])
	}
	if c.lag == nil {
		return nil, fmt.Errorf("%s(%q, %d): no lag state for this expression", lagFuncName, name, n)
	}
	var (
		v         any
		rowExists bool
		err       error
		def       any
	)
	switch len(params) {
	case 2, 3:
		v, rowExists, err = c.lag.read(name, n)
		if len(params) == 3 {
			def = params[2]
		}
	default:
		tail := params[len(params)-2:]
		partition, ok := tail[0].(string)
		if !ok {
			return nil, fmt.Errorf("%s expects a partition field name, got %T", lagFuncName, tail[0])
		}
		key, kerr := lagPartKeyOf(partition, tail[1], c.nullMask[partition])
		if kerr != nil {
			return nil, kerr
		}
		v, rowExists, err = c.lag.readPartition(partition, key, name, n)
		if len(params) == 5 {
			def = params[2]
		}
	}
	if err != nil {
		return nil, err
	}
	if rowExists {
		return v, nil
	}
	return c.lag.defaultValue(name, def)
}

// lagPartKey is one partition value: a categorical label, or null (its
// own partition, SQL PARTITION BY's rule).
type lagPartKey struct {
	null  bool
	label string
}

// lagPartKeyOf derives the key from the partition field's row value and
// null bit. A nulled field still carries a drawn value in the row map,
// so the bit decides first.
func lagPartKeyOf(partition string, v any, isNull bool) (lagPartKey, error) {
	if isNull {
		return lagPartKey{null: true}, nil
	}
	s, ok := v.(string)
	if !ok {
		return lagPartKey{}, fmt.Errorf("%s: partition field %q holds %T, not a categorical label", lagFuncName, partition, v)
	}
	return lagPartKey{label: s}, nil
}

// lagBuffer is the accept-only cross-row state: for each lagged field a
// ring of the last `size` accepted rows' final values and null bits.
// One per generate() call, shared by the constraint host and the rule
// host; nil when no expression calls lag.
//
// The root ring serves the unpartitioned calls (size 0 and no fields
// when every call is partitioned); parts holds one lagPartition per
// partition field, nil when no call partitions — so a spec without the
// argument carries exactly the state it did before it existed.
type lagBuffer struct {
	size   int
	fields []string // sorted; commit order (no output depends on it)
	slot   map[string]int
	class  []lagClass
	vals   [][]any
	nulls  [][]bool
	head   int // next write position
	count  int // accepted rows committed so far

	parts    []*lagPartition // sorted by partition field
	partSlot map[string]int
	// classOf is every lagged field's class, partitioned or not, for
	// defaultValue.
	classOf map[string]lagClass
}

// lagPartition is the state of every lag call partitioned by one field:
// the lagged fields and largest n under it (the template each new ring
// is sized from) and one ring per distinct partition value seen on an
// accepted row.
type lagPartition struct {
	field  string
	size   int
	fields []string
	class  []lagClass
	rings  map[lagPartKey]*lagBuffer
}

// newLagRing sizes one ring for fields.
func newLagRing(size int, fields []string, class []lagClass) *lagBuffer {
	b := &lagBuffer{size: size, fields: fields, class: class, slot: make(map[string]int, len(fields))}
	for i, name := range fields {
		b.slot[name] = i
		b.vals = append(b.vals, make([]any, size))
		b.nulls = append(b.nulls, make([]bool, size))
	}
	return b
}

// newLagBuffer sizes the buffer from every well-formed lag call in the
// spec's rules and constraints. Returns nil when there is none.
func newLagBuffer(s *Spec, wfs []*writerField) *lagBuffer {
	declared := make(map[string]bool, len(wfs))
	classes := make(map[string]lagClass, len(wfs))
	for _, wf := range wfs {
		declared[wf.spec.Name] = true
		classes[wf.spec.Name] = lagClassOf(sentinelFor(wf.spec.Type))
	}
	// Largest n per (partition, field); partition "" is the root ring.
	maxN := map[string]int{}
	lagged := map[string]map[string]bool{}
	for _, src := range rowExprSources(s) {
		for key, n := range lagCalls(src, declared) {
			if lagged[key.partition] == nil {
				lagged[key.partition] = map[string]bool{}
			}
			lagged[key.partition][key.field] = true
			if n > maxN[key.partition] {
				maxN[key.partition] = n
			}
		}
	}
	if len(lagged) == 0 {
		return nil
	}
	ringFields := func(set map[string]bool) ([]string, []lagClass) {
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		cls := make([]lagClass, len(names))
		for i, name := range names {
			cls[i] = classes[name]
		}
		return names, cls
	}
	rootFields, rootClass := ringFields(lagged[""])
	b := newLagRing(maxN[""], rootFields, rootClass)
	b.classOf = map[string]lagClass{}
	for _, set := range lagged {
		for name := range set {
			b.classOf[name] = classes[name]
		}
	}
	partitions := make([]string, 0, len(lagged))
	for p := range lagged {
		if p != "" {
			partitions = append(partitions, p)
		}
	}
	sort.Strings(partitions)
	if len(partitions) > 0 {
		b.partSlot = make(map[string]int, len(partitions))
	}
	for i, p := range partitions {
		fields, cls := ringFields(lagged[p])
		b.partSlot[p] = i
		b.parts = append(b.parts, &lagPartition{field: p, size: maxN[p], fields: fields, class: cls,
			rings: map[lagPartKey]*lagBuffer{}})
	}
	return b
}

// rowExprSources lists every expression compiled against the row
// environment: each rule's `when` and `set_expr` values, then each
// constraint.
func rowExprSources(s *Spec) []string {
	var out []string
	for _, r := range s.Rules {
		if r.When != "" {
			out = append(out, r.When)
		}
		for _, name := range sortedKeys(r.SetExpr) {
			out = append(out, r.SetExpr[name])
		}
	}
	for _, c := range s.Constraints {
		out = append(out, c.Expr)
	}
	return out
}

// lagCallKey is one lagged field under one partition ("" for none).
type lagCallKey struct {
	field     string
	partition string
}

// lagCalls returns each well-formed lag call in src as (field,
// partition) -> largest n.
func lagCalls(src string, declared map[string]bool) map[lagCallKey]int {
	tree, err := parser.Parse(src)
	if err != nil || tree == nil || tree.Node == nil {
		return nil
	}
	c := &lagCallCollector{declared: declared, out: map[lagCallKey]int{}}
	ast.Walk(&tree.Node, c)
	return c.out
}

type lagCallCollector struct {
	declared map[string]bool
	out      map[lagCallKey]int
}

func (c *lagCallCollector) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	if !ok || callee.Value != lagFuncName || c.declared[lagFuncName] {
		return
	}
	field, n, partition, refusal := lagCallShapePart(call, c.declared)
	if refusal != nil {
		return
	}
	key := lagCallKey{field: field, partition: partition}
	if n > c.out[key] {
		c.out[key] = n
	}
}

// commit records the just-ACCEPTED row: into the root ring, and into
// each partition's ring for the row's FINAL partition value (created on
// that value's first row). A set's map and a vector's slice are copied:
// the next row may reuse them. nil-safe.
func (b *lagBuffer) commit(row map[string]any, nullMask map[string]bool) {
	if b == nil {
		return
	}
	b.commitRing(row, nullMask)
	for _, p := range b.parts {
		// lagPartitionRefusal admitted only categorical partitions, so the
		// value is a label; anything else keys as the empty label rather
		// than failing a row that was already accepted.
		key := lagPartKey{null: nullMask[p.field]}
		if !key.null {
			key.label, _ = row[p.field].(string)
		}
		ring := p.rings[key]
		if ring == nil {
			ring = newLagRing(p.size, p.fields, p.class)
			p.rings[key] = ring
		}
		ring.commitRing(row, nullMask)
	}
}

// commitRing writes one row into this ring. A ring with no fields (the
// root when every call is partitioned) holds nothing.
func (b *lagBuffer) commitRing(row map[string]any, nullMask map[string]bool) {
	if b.size == 0 {
		return
	}
	for i, name := range b.fields {
		v := row[name]
		switch t := v.(type) {
		case map[string]bool:
			v = maps.Clone(t)
		case []float64:
			v = append([]float64(nil), t...)
		}
		b.vals[i][b.head] = v
		b.nulls[i][b.head] = nullMask[name]
	}
	b.head = (b.head + 1) % b.size
	if b.count < b.size {
		b.count++
	}
}

// read returns the n-th previous accepted row's value of name, or
// rowExists=false while fewer than n rows have been accepted. A null
// field reads as nil.
func (b *lagBuffer) read(name string, n int) (v any, rowExists bool, err error) {
	i, ok := b.slot[name]
	if !ok || n < 1 || n > b.size {
		return nil, false, fmt.Errorf("%s(%q, %d): not a lag the spec declared", lagFuncName, name, n)
	}
	if n > b.count {
		return nil, false, nil
	}
	pos := (b.head - n + b.size) % b.size
	if b.nulls[i][pos] {
		return nil, true, nil
	}
	return b.vals[i][pos], true, nil
}

// readPartition is read over the ring of one partition value: the n-th
// previous accepted row CARRYING that value. A value with no committed
// row yet has no history (rowExists=false).
func (b *lagBuffer) readPartition(partition string, key lagPartKey, name string, n int) (v any, rowExists bool, err error) {
	pi, ok := b.partSlot[partition]
	if !ok {
		return nil, false, fmt.Errorf("%s(%q, %d, _, %q): not a partition the spec declared", lagFuncName, name, n, partition)
	}
	p := b.parts[pi]
	ring := p.rings[key]
	if ring == nil {
		if i := sort.SearchStrings(p.fields, name); i == len(p.fields) || p.fields[i] != name || n < 1 || n > p.size {
			return nil, false, fmt.Errorf("%s(%q, %d, _, %q): not a lag the spec declared", lagFuncName, name, n, partition)
		}
		return nil, false, nil
	}
	return ring.read(name, n)
}

// defaultValue converts a default onto the field's row shape: an integer
// to float64 for a scalar (an int-typed expression such as row() reaches
// here unconverted), an option list to a set selection, a number list to
// a vector. A nil default is null.
func (b *lagBuffer) defaultValue(name string, d any) (any, error) {
	if d == nil {
		return nil, nil
	}
	class, ok := b.classOf[name]
	if !ok {
		class = b.class[b.slot[name]]
	}
	switch class {
	case lagNumber:
		switch t := d.(type) {
		case float64:
			return t, nil
		case int:
			return float64(t), nil
		}
	case lagCategory:
		if s, ok := d.(string); ok {
			return s, nil
		}
	case lagSet:
		switch t := d.(type) {
		case map[string]bool:
			return maps.Clone(t), nil
		case []any:
			sel := make(map[string]bool, len(t))
			for _, e := range t {
				s, ok := e.(string)
				if !ok {
					return nil, fmt.Errorf("%s(%q): a set default must list option names, got %T", lagFuncName, name, e)
				}
				sel[s] = true
			}
			return sel, nil
		}
	case lagVector:
		switch t := d.(type) {
		case []float64:
			return append([]float64(nil), t...), nil
		case []any:
			out := make([]float64, len(t))
			for i, e := range t {
				switch x := e.(type) {
				case float64:
					out[i] = x
				case int:
					out[i] = float64(x)
				default:
					return nil, fmt.Errorf("%s(%q): a vector default must list numbers, got %T", lagFuncName, name, e)
				}
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("%s(%q): default %v (%T) does not fit the field", lagFuncName, name, d, d)
}

// lagPartitionRefusal is the spec-level half of the partition argument's
// validation, the half the shared walker cannot settle because it sees
// names, not types or rule order: every partition must name a
// categorical_* field, and no rule at or after the one holding the call
// may write it. Rule order is the read order (rules run in declaration
// order, each reading the row as the earlier rules left it), so a write
// in the SAME rule lands after the read too. Constraints run after every
// rule, so only the type is checked there.
//
// Called by validateSpec, by ApplyRulesFile on its probe, and by
// generate for a Spec that bypassed both — one function, so the code,
// message and details agree on every path. Malformed calls are skipped:
// the walker refuses them with their own codes first.
func lagPartitionRefusal(s *Spec) error {
	declared := make(map[string]bool, len(s.Fields))
	typeOf := make(map[string]string, len(s.Fields))
	for _, f := range s.Fields {
		declared[f.Name] = true
		typeOf[f.Name] = f.Type
	}
	categorical := func(name string) bool {
		ft, ok := fieldTypeFromName(typeOf[name])
		return ok && ft.IsCategorical()
	}
	// lastWriter[f] is the LAST rule index writing f.
	lastWriter := map[string]int{}
	for i, r := range s.Rules {
		for _, name := range lagRuleWrites(r) {
			lastWriter[name] = i
		}
	}
	type site struct {
		rule        int // -1 for a constraint
		slot, field string
		src         string
	}
	var sites []site
	for i, r := range s.Rules {
		if r.When != "" {
			sites = append(sites, site{rule: i, slot: "when", src: r.When})
		}
		for _, name := range sortedKeys(r.SetExpr) {
			sites = append(sites, site{rule: i, slot: "set_expr", field: name, src: r.SetExpr[name]})
		}
	}
	for _, c := range s.Constraints {
		sites = append(sites, site{rule: -1, src: c.Expr})
	}
	for _, st := range sites {
		calls := lagCalls(st.src, declared)
		keys := make([]lagCallKey, 0, len(calls))
		for k := range calls {
			if k.partition != "" {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(a, b int) bool {
			if keys[a].partition != keys[b].partition {
				return keys[a].partition < keys[b].partition
			}
			return keys[a].field < keys[b].field
		})
		for _, k := range keys {
			details := map[string]any{"function": lagFuncName, "expr": st.src, "lag_field": k.field, "lag_partition": k.partition}
			var msg string
			if !categorical(k.partition) {
				msg = fmt.Sprintf("lag(%q, n, default, %q): the partition field is %s, not categorical_*; only a categorical field's labels can key the history",
					k.field, k.partition, typeOf[k.partition])
			} else if w, ok := lastWriter[k.partition]; ok && st.rule >= 0 && w >= st.rule {
				details["writer_rule"] = w
				msg = fmt.Sprintf("lag(%q, n, default, %q): rule %d writes %q at or after this read, so the partition this row is read under is not the one it is committed under; move that write before rule %d, or read lag from a constraint",
					k.field, k.partition, w, k.partition, st.rule)
			} else {
				continue
			}
			if st.rule < 0 {
				return errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_LAG_PARTITION_INVALID,
					fmt.Sprintf("constraint %q %s", st.src, msg), details)
			}
			details[errors.DetailSynthRule] = st.rule
			details[errors.DetailSynthRuleSlot] = st.slot
			if st.field != "" {
				details["field"] = st.field
			}
			return errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_LAG_PARTITION_INVALID,
				fmt.Sprintf("rule %d: %s", st.rule, msg), details)
		}
	}
	return nil
}

// lagRuleWrites lists the fields rule r can change: set, set_expr,
// set_null and every null_together member but the gate (block[0], whose
// own state is copied, never written).
func lagRuleWrites(r RuleSpec) []string {
	out := append(sortedKeys(r.Set), sortedKeys(r.SetExpr)...)
	out = append(out, r.SetNull...)
	if len(r.NullTogether) > 1 {
		out = append(out, r.NullTogether[1:]...)
	}
	return out
}
