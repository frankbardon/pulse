package synth

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/tablecall"
	"github.com/frankbardon/pulse/types"
)

// lookup(table, keys...) in the row environment (U79 G5b): a rule's
// `when` / `set_expr` and a constraint read a named lookup table, the
// same reader ATTR_FORMULA / FILTER_EXPRESSION call, over the tables
// Options.LookupTables carries (the Pulse facade fills it from the
// instance: Extensions.LookupTables, LookupTablesDir,
// PULSE_LOOKUP_TABLES_DIR).
//
// Three rules keep it inside the (spec, seed) byte-identity contract:
//
//   - The table name is a STRING LITERAL, refused otherwise before a
//     row exists (lookupCallShape). The tables a spec reads are then a
//     static fact of the spec, so their content digests fold into its
//     identity (Spec.HashWithLookups) and the run's output is a
//     function of (spec, seed, referenced tables). A computed name
//     would make the referenced set a per-row fact no hash can carry.
//   - Every literal call is judged against the tables before the first
//     row is drawn (checkLookupCalls), with processing's own codes: an
//     unregistered table is PULSE_LOOKUP_TABLE_UNKNOWN, a literal null
//     key or an off-axis literal key PULSE_LOOKUP_MISS.
//   - A key that READS a field the row holds as null is a null key
//     (PULSE_LOOKUP_MISS, details.null_key), never its drawn value: a
//     nulled field still carries its drawn value in the row env, so
//     without lookupKeyPatcher a null region would silently read the
//     table at whatever region the sampler drew before the null.
//
// Keys stringify as processing stringifies them (%v of the row value:
// every scalar is a float64, so a whole number reads "3", a packed_bool
// "1" / "0"; a categorical is its level string) and join with "|".

// LookupTable is a named lookup table the row environment's
// lookup(table, keys...) reads: Rows keyed by the "|"-joined key
// arguments, and, when the table declares key axes, Axes — one value
// list per axis in argument order — which the eager pre-row check
// judges literal keys against. A Pulse instance converts its own
// Rows-backed tables into this shape; a callback-backed table has no
// content to digest and is refused by the facade when a spec calls it.
type LookupTable struct {
	Rows map[string]float64
	Axes [][]string
}

// lookupKeyFuncName is the internal function lookupKeyPatcher wraps a
// field-reading key argument in. The colon keeps it out of reach of a
// spec's own syntax.
const lookupKeyFuncName = "lookup:key"

// lookupFunctionOptions declares lookup() (untyped, as processing
// declares it: the result is a float64 the coercion matrix reads) and
// the null-aware key wrapper, both answering from host.
func lookupFunctionOptions(host *compiledConstraints) []expr.Option {
	return []expr.Option{
		expr.Function(exprenv.LookupFunc, host.lookupBuiltin),
		expr.Function(lookupKeyFuncName, host.lookupKeyBuiltin),
	}
}

// lookupBuiltin answers lookup(table, keys...) from host.lookups,
// raising processing's codes (internal/processing lookupBuiltin) for the
// same faults: an unregistered table, a null key, a key no row holds.
func (c *compiledConstraints) lookupBuiltin(args ...any) (any, error) {
	if len(args) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			"lookup() requires at least 1 argument (table name)")
	}
	name, ok := args[0].(string)
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("lookup() table argument must be string, got %T", args[0]))
	}
	table, ok := c.lookups[name]
	if !ok {
		return nil, tablecall.UnknownLookup(name)
	}
	keys := make([]string, 0, len(args)-1)
	for i, a := range args[1:] {
		if a == nil {
			return nil, tablecall.LookupNullKey(name, i)
		}
		keys = append(keys, fmt.Sprintf("%v", a))
	}
	v, found := table.Rows[strings.Join(keys, tablecall.KeySeparator)]
	if !found {
		return nil, tablecall.LookupMiss(name, keys)
	}
	return v, nil
}

// lookupKeyBuiltin answers lookup:key(field..., value): nil when any
// named field is null on the current row, value otherwise.
func (c *compiledConstraints) lookupKeyBuiltin(args ...any) (any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("%s expects at least a value", lookupKeyFuncName)
	}
	for _, a := range args[:len(args)-1] {
		if name, ok := a.(string); ok && c.nullMask[name] {
			return nil, nil
		}
	}
	return args[len(args)-1], nil
}

// lookupKeyPatcher wraps every lookup() key argument that reads a
// declared field in lookup:key(<fields read>, <arg>), so a key computed
// from a null field binds nil. It runs after the isnull patcher, so the
// name inside isnull(x) is already a string and is not counted as a
// read.
type lookupKeyPatcher struct{ declared map[string]bool }

func (p lookupKeyPatcher) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok {
		return
	}
	callee, ok := call.Callee.(*ast.IdentifierNode)
	if !ok || callee.Value != exprenv.LookupFunc || p.declared[exprenv.LookupFunc] {
		return
	}
	for i := 1; i < len(call.Arguments); i++ {
		arg := call.Arguments[i]
		reads := fieldReads(arg, p.declared)
		if len(reads) == 0 {
			continue
		}
		wrapArgs := make([]ast.Node, 0, len(reads)+1)
		for _, name := range reads {
			lit := &ast.StringNode{Value: name}
			lit.SetLocation(arg.Location())
			wrapArgs = append(wrapArgs, lit)
		}
		wrapArgs = append(wrapArgs, arg)
		id := &ast.IdentifierNode{Value: lookupKeyFuncName}
		id.SetLocation(arg.Location())
		wrap := &ast.CallNode{Callee: id, Arguments: wrapArgs}
		wrap.SetLocation(arg.Location())
		call.Arguments[i] = wrap
	}
}

// fieldReads lists, sorted and once each, the declared fields node
// reads as a bare identifier (a callee is a function, not a read).
func fieldReads(node ast.Node, declared map[string]bool) []string {
	c := &fieldReadCollector{declared: declared, callees: map[ast.Node]bool{}, seen: map[string]bool{}}
	ast.Walk(&node, rowCalleeMarker{c.callees})
	ast.Walk(&node, c)
	sort.Strings(c.out)
	return c.out
}

type rowCalleeMarker struct{ callees map[ast.Node]bool }

func (m rowCalleeMarker) Visit(node *ast.Node) {
	if n, ok := (*node).(*ast.CallNode); ok {
		m.callees[n.Callee] = true
	}
}

type fieldReadCollector struct {
	declared map[string]bool
	callees  map[ast.Node]bool
	seen     map[string]bool
	out      []string
}

func (c *fieldReadCollector) Visit(node *ast.Node) {
	n, ok := (*node).(*ast.IdentifierNode)
	if !ok || c.callees[n] || !c.declared[n.Value] || c.seen[n.Value] {
		return
	}
	c.seen[n.Value] = true
	c.out = append(c.out, n.Value)
}

// lookupCallShape is the parse-settled check every row expression runs
// (rowExprStaticRefusal): lookup() names its table as a string literal.
func lookupCallShape(call *ast.CallNode) *rowExprRefusal {
	if len(call.Arguments) == 0 {
		return &rowExprRefusal{function: exprenv.LookupFunc, msg: "lookup() needs a table name: lookup(\"table\", keys...)"}
	}
	if _, ok := call.Arguments[0].(*ast.StringNode); !ok {
		return &rowExprRefusal{function: exprenv.LookupFunc, msg: "lookup() must name its table as a string literal: the tables a spec reads fold into its identity (Spec.HashWithLookups), so they must be known before a row exists"}
	}
	return nil
}

// lookupSite is one row expression a spec carries, with the location
// a refusal names.
type lookupSite struct {
	src string
	// rule is the rule index, -1 for a constraint.
	rule   int
	slot   string
	target string
}

func specExprSites(s *Spec) []lookupSite {
	var out []lookupSite
	for i, r := range s.Rules {
		if r.When != "" {
			out = append(out, lookupSite{src: r.When, rule: i, slot: "when"})
		}
		for _, name := range sortedKeys(r.SetExpr) {
			out = append(out, lookupSite{src: r.SetExpr[name], rule: i, slot: "set_expr", target: name})
		}
	}
	for _, c := range s.Constraints {
		out = append(out, lookupSite{src: c.Expr, rule: -1})
	}
	return out
}

// lookupCalls returns src's lookup() calls (callee not shadowed by a
// declared field), in walk order.
func lookupCalls(src string, declared map[string]bool) []*ast.CallNode {
	if declared[exprenv.LookupFunc] || !strings.Contains(src, exprenv.LookupFunc) {
		return nil
	}
	tree, err := parser.Parse(src)
	if err != nil || tree == nil || tree.Node == nil {
		return nil
	}
	f := &lookupCallFinder{}
	ast.Walk(&tree.Node, f)
	return f.calls
}

type lookupCallFinder struct{ calls []*ast.CallNode }

func (f *lookupCallFinder) Visit(node *ast.Node) {
	if n, ok := (*node).(*ast.CallNode); ok {
		if id, ok := n.Callee.(*ast.IdentifierNode); ok && id.Value == exprenv.LookupFunc {
			f.calls = append(f.calls, n)
		}
	}
}

func specDeclared(s *Spec) map[string]bool {
	out := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		out[f.Name] = true
	}
	return out
}

// ReferencedLookupTables returns the lookup tables s's row expressions
// (rule `when` / `set_expr`, constraints) name, sorted, once each. The
// names are literals (lookupCallShape), so the set is a static fact of
// the spec.
func (s *Spec) ReferencedLookupTables() []string {
	if s == nil {
		return nil
	}
	declared := specDeclared(s)
	seen := map[string]bool{}
	var out []string
	for _, site := range specExprSites(s) {
		for _, call := range lookupCalls(site.src, declared) {
			if len(call.Arguments) == 0 {
				continue
			}
			lit, ok := call.Arguments[0].(*ast.StringNode)
			if !ok || seen[lit.Value] {
				continue
			}
			seen[lit.Value] = true
			out = append(out, lit.Value)
		}
	}
	sort.Strings(out)
	return out
}

// lookupCatalog is tables as tablecall sees them. lookup() is always
// declared in the row env, so HasLookup is true even with no table (a
// literal call then refuses as unknown); range_label is not a row
// function, so no range table is known.
type lookupCatalog map[string]LookupTable

func (c lookupCatalog) HasLookup() bool { return true }

func (c lookupCatalog) LookupAxes(name string) ([][]string, bool) {
	t, ok := c[name]
	if !ok {
		return nil, false
	}
	return t.Axes, true
}

func (c lookupCatalog) RangeKind(string) (string, bool) { return "", false }

// checkLookupCalls judges every lookup() call s makes against tables
// before a row is drawn: tablecall's literal checks (unknown table, a
// literal null key, a literal key off the declared axes) and the arity a
// table's axes fix — more key arguments than axes can never hit, since
// every argument contributes at least one "|" part. Codes are
// processing's; details add the rule location.
func checkLookupCalls(s *Spec, tables map[string]LookupTable) error {
	declared := specDeclared(s)
	cat := lookupCatalog(tables)
	for _, site := range specExprSites(s) {
		calls := lookupCalls(site.src, declared)
		for _, call := range calls {
			var ce *errors.CodedError
			if r := tablecall.Refusal(call, cat); r != nil {
				ce = r
			} else if lit, ok := call.Arguments[0].(*ast.StringNode); ok {
				axes := tables[lit.Value].Axes
				if n := len(call.Arguments) - 1; axes != nil && n > len(axes) {
					ce = errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_MISS,
						fmt.Sprintf("lookup(%q, ...) passes %d keys, and the table declares %d key axes: no row can hold the joined key", lit.Value, n, len(axes)),
						map[string]any{"table": lit.Value, "key_count": n, "axes": len(axes)})
				}
			}
			if ce != nil {
				return lookupSiteError(ce, site)
			}
		}
	}
	return nil
}

// lookupSiteError re-raises ce under its own code with the expression's
// location added to its details and message.
func lookupSiteError(ce *errors.CodedError, site lookupSite) error {
	details := map[string]any{"expr": site.src}
	for k, v := range ce.Details {
		details[k] = v
	}
	where := fmt.Sprintf("constraint %q", site.src)
	if site.rule >= 0 {
		details[errors.DetailSynthRule] = site.rule
		details[errors.DetailSynthRuleSlot] = site.slot
		where = fmt.Sprintf("rule %d %s %q", site.rule, site.slot, site.src)
		if site.target != "" {
			details["field"] = site.target
			where = fmt.Sprintf("rule %d %s[%q] = %q", site.rule, site.slot, site.target, site.src)
		}
	}
	return errors.NewCodedErrorWithDetails(ce.Code, fmt.Sprintf("%s: %s", where, ce.Message), details)
}

// lookupRunError returns the lookup() fault inside an expression
// evaluation error, so a run carries the table's own code (and its
// details), never the generic PROCESSING_RUNTIME wrapper — processing's
// tableRefusal rule.
func lookupRunError(err error) *errors.CodedError {
	for e := err; e != nil; {
		var ce *errors.CodedError
		if !stderrors.As(e, &ce) {
			return nil
		}
		switch ce.Code {
		case errors.PULSE_LOOKUP_MISS, errors.PULSE_LOOKUP_TABLE_UNKNOWN:
			return ce
		}
		e = ce.Cause
	}
	return nil
}

// LookupDigest is the content digest of t that folds into
// Spec.HashWithLookups: SHA-256 over its rows in sorted key order (each
// value's exact float64 bits, so -0, NaN payloads and every last ulp
// count) and its axes, first 16 bytes hex. Two tables with the same
// digest answer every lookup() identically.
func LookupDigest(t LookupTable) string {
	h := sha256.New()
	var buf [8]byte
	writeStr := func(s string) {
		binary.BigEndian.PutUint64(buf[:], uint64(len(s)))
		h.Write(buf[:])
		h.Write([]byte(s))
	}
	writeN := func(n int) {
		binary.BigEndian.PutUint64(buf[:], uint64(n))
		h.Write(buf[:])
	}
	keys := make([]string, 0, len(t.Rows))
	for k := range t.Rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	writeStr("rows")
	writeN(len(keys))
	for _, k := range keys {
		writeStr(k)
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(t.Rows[k]))
		h.Write(buf[:])
	}
	if t.Axes == nil {
		writeStr("no-axes")
	} else {
		writeStr("axes")
		writeN(len(t.Axes))
		for _, axis := range t.Axes {
			if axis == nil {
				writeStr("open")
				continue
			}
			writeN(len(axis))
			for _, v := range axis {
				writeStr(v)
			}
		}
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

// lookupDigestUnregistered stands in for the digest of a referenced
// table tables does not carry (the run refuses it; the hash still has
// to say "absent", not "any").
const lookupDigestUnregistered = "unregistered"

// HashWithLookups is the spec's identity under the lookup tables it
// reads: Hash() when the spec calls no lookup(), otherwise the canonical
// hash of the spec together with the content digest (LookupDigest) of
// every table it names, so a changed table changes the identity and the
// same (spec, seed, tables) always means the same output bytes. Hash()
// alone is table-blind.
func (s *Spec) HashWithLookups(tables map[string]LookupTable) string {
	names := s.ReferencedLookupTables()
	if len(names) == 0 {
		return s.Hash()
	}
	digests := make(map[string]string, len(names))
	for _, name := range names {
		if t, ok := tables[name]; ok {
			digests[name] = LookupDigest(t)
		} else {
			digests[name] = lookupDigestUnregistered
		}
	}
	return types.CanonicalHash("synth", struct {
		Spec    *Spec             `json:"spec"`
		Lookups map[string]string `json:"lookups"`
	}{s, digests})
}
