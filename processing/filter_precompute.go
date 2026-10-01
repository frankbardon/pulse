package processing

import (
	"sync"
	"sync/atomic"

	exprast "github.com/expr-lang/expr/ast"
	exprbuiltin "github.com/expr-lang/expr/builtin"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// Filter precompute over parent-group dictionary entries (format 0x02).
//
// A deduped cohort stores each distinct parent tuple ONCE, in a group
// dictionary, and every row carries a u32 entry index into it. A filter
// whose every input field is a member of ONE group therefore has at most
// entry_count distinct answers, whatever the row count and whatever the
// row order: evaluate the predicate once per dictionary entry and every
// row's verdict is a bit test on its entry index.
//
// Where it plugs in: the FilterFunc itself. buildFilterFuncs /
// BuildFilters wrap each ELIGIBLE filterer's FilterFunc (grouped build
// schema only) in precomputedFilter.eval, so every execution arm that
// filters — streaming, buffered, projected, fused crosstab, parallel
// decode, parallel shards, Compose, ProcessChain stage 0, facet, sample
// — takes the precompute with no per-arm wiring, and applyFilterPass's
// {n_in, n_out, n_null_input} walk is untouched: n_in and n_out follow
// the verdict, which is the per-row verdict by construction, and
// n_null_input still reads the record's own null state.
//
// Where the entry index comes from: the grouped reuse decoder hands the
// record its row's entries through internal/encoding.GroupIndexRecord
// (Record.SetGroupIndices). Any record that does not carry a valid index
// — a map decode that does not forward RecordReader.GroupIndex (lookup,
// the shard reducer, the projection-without-plan fallback; filter-to-
// file DOES forward it), a joined or synthetic record, a
// record whose member field was rewritten after decode, an ungrouped
// schema — is evaluated per row by the unchanged FilterFunc. The
// fallback is per ROW, so correctness never depends on which arm ran.
//
// Eligibility (precomputeInputs): a built-in filterer that is a pure
// function of the named field(s) of ONE record. Every built-in filterer
// qualifies on its Field; FILTER_EXPRESSION qualifies when its AST names
// only schema fields and calls only pure functions (expr-lang built-ins
// other than now(), the set built-ins, and lookup() when every lookup
// table is a static Rows map). Extension filterers never qualify: the
// registration declares no purity, and a FieldInputs hook alone does
// not prove a factory keeps no cross-record state. Per schema
// (verdictTable), every input must then resolve to a unique field name
// that is a member of the same group; a filter spanning two groups, or a
// group member and a row field, is evaluated per row — the verdict
// space would be the product of the two, so there is no small table to
// precompute.
//
// Evaluation is LAZY per entry: an entry is evaluated the first time a
// row carrying it reaches the filter, so the work is the number of
// DISTINCT entries reached — never more than the per-row evaluation
// count, even behind a selective earlier filter. An entry whose
// evaluation errors is marked per-row and re-evaluated on each of its
// rows, so an error surfaces exactly when, and only if, the per-row path
// would raise it.
//
// Memory: two bits per dictionary entry per (filter, schema), allocated
// on the first grouped row — 27 KB for a 109K-entry group. Derived at
// read time and never persisted.

// Entry states, two bits per entry.
const (
	verdictUnknown = 0
	verdictFail    = 1
	verdictPass    = 2
	verdictPerRow  = 3
)

// filterPrecomputeEnabled is the process-wide switch (on by default).
var filterPrecomputeEnabled atomic.Bool

func init() { filterPrecomputeEnabled.Store(true) }

// SetFilterPrecompute turns the filter precompute on or off process-wide
// and returns the previous setting. It changes speed only, never a
// result: disabled, every filter is evaluated per row. It exists for
// benchmarks and A/B diagnosis, and takes effect for FilterFuncs built
// after the call.
func SetFilterPrecompute(enabled bool) (previous bool) {
	return filterPrecomputeEnabled.Swap(enabled)
}

// Process-wide diagnostic counters (see FilterPrecomputeStats). Only
// table construction and per-entry evaluation touch them — never the
// per-row bit test — so they cost nothing on the hot path.
var (
	precomputeTables      atomic.Int64
	precomputeEntryEvals  atomic.Int64
	precomputeTableBytes  atomic.Int64
	precomputeIneligibles atomic.Int64
)

// FilterPrecomputeStatsSnapshot is a snapshot of the process-wide filter
// precompute counters. All fields are monotonic totals since process
// start.
type FilterPrecomputeStatsSnapshot struct {
	// Tables is the number of per-(filter, schema) verdict tables built.
	Tables int64
	// EntryEvaluations is the number of times a filter predicate was
	// evaluated against one dictionary entry.
	EntryEvaluations int64
	// TableBytes is the verdict-table memory allocated (2 bits/entry).
	TableBytes int64
	// Ineligible counts (filter, grouped schema) pairs resolved to the
	// per-row path because the inputs do not live in one group.
	Ineligible int64
}

// FilterPrecomputeStats returns the process-wide filter precompute
// counters. Diagnostic only; the deterministic work gates read deltas.
func FilterPrecomputeStats() FilterPrecomputeStatsSnapshot {
	return FilterPrecomputeStatsSnapshot{
		Tables:           precomputeTables.Load(),
		EntryEvaluations: precomputeEntryEvals.Load(),
		TableBytes:       precomputeTableBytes.Load(),
		Ineligible:       precomputeIneligibles.Load(),
	}
}

// wrapFilterPrecompute returns fn wrapped in the per-entry precompute
// when the build schema declares parent groups and the filterer is
// eligible, and fn unchanged otherwise — an ungrouped cohort never sees
// the wrapper at all.
func wrapFilterPrecompute(fn FilterFunc, f *types.Filterer, schema *encoding.Schema, exts *ExtensionRegistry) FilterFunc {
	if fn == nil || schema == nil || !schema.HasGroups() || !filterPrecomputeEnabled.Load() {
		return fn
	}
	inputs, ok := precomputeInputs(f, exts)
	if !ok {
		return fn
	}
	// Resolve against the build schema once: a filter that cannot be
	// precomputed there (a row field, two groups, a mix) keeps its
	// FilterFunc bare, paying no per-row wrapper cost. Records decoded
	// under another schema re-resolve per schema in the wrapper.
	if g, _ := precomputeGroup(schema, inputs); g < 0 {
		precomputeIneligibles.Add(1)
		return fn
	}
	pf := &precomputedFilter{inner: fn, inputs: inputs}
	return pf.eval
}

// precomputedFilter is one filterer's precompute: the unchanged per-row
// FilterFunc plus a lazily built verdict table per record schema.
type precomputedFilter struct {
	inner  FilterFunc
	inputs []string

	last atomic.Pointer[verdictTable]
	mu   sync.Mutex // guards tables and every table's scratch / fills
	// tables caches a table per record schema. A scan sees one schema;
	// a shard archive sees one per shard, keyed by the shard's own
	// dictionary.
	tables map[*encoding.Schema]*verdictTable
}

// verdictTable is a filter's precomputed verdicts over one schema's
// group dictionary.
type verdictTable struct {
	schema  *encoding.Schema
	g       int      // the group every input belongs to; -1 = per-row
	words   []uint64 // 2 bits per entry, accessed atomically
	scratch *Record  // entry-evaluation record, under precomputedFilter.mu
	dec     *encx.GroupEntryDecoder
}

func (p *precomputedFilter) eval(rec *Record) (bool, error) {
	if rec == nil {
		return p.inner(rec)
	}
	t := p.last.Load()
	if t == nil || t.schema != rec.schema {
		t = p.table(rec.schema)
	}
	if t.g < 0 {
		return p.inner(rec)
	}
	e, ok := rec.GroupIndex(t.g)
	if !ok || int(e>>5) >= len(t.words) {
		return p.inner(rec)
	}
	shift := (e & 31) * 2
	st := (atomic.LoadUint64(&t.words[e>>5]) >> shift) & 3
	if st == verdictUnknown {
		st = p.fill(t, e)
	}
	switch st {
	case verdictPass:
		return true, nil
	case verdictFail:
		return false, nil
	}
	return p.inner(rec)
}

// table returns the verdict table for schema s, building it on first
// sight.
func (p *precomputedFilter) table(s *encoding.Schema) *verdictTable {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.tables[s]
	if !ok {
		t = newVerdictTable(s, p.inputs)
		if p.tables == nil {
			p.tables = make(map[*encoding.Schema]*verdictTable)
		}
		p.tables[s] = t
	}
	p.last.Store(t)
	return t
}

// newVerdictTable resolves inputs against s and allocates the table, or
// returns a per-row table (g = -1) when they do not live in one group.
func newVerdictTable(s *encoding.Schema, inputs []string) *verdictTable {
	t := &verdictTable{schema: s, g: -1}
	if s == nil || !s.HasGroups() {
		return t
	}
	g, fields := precomputeGroup(s, inputs)
	if g < 0 {
		precomputeIneligibles.Add(1)
		return t
	}
	dec, err := encx.NewGroupEntryDecoder(s, g, fields)
	if err != nil {
		precomputeIneligibles.Add(1)
		return t
	}
	n := s.GroupEntryCount(g)
	t.g = g
	t.dec = dec
	t.words = make([]uint64, (n+31)/32)
	t.scratch = NewReusableRecord(s)
	precomputeTables.Add(1)
	precomputeTableBytes.Add(int64(len(t.words) * 8))
	return t
}

// precomputeGroup returns the group every input is a member of and the
// inputs' field indices, or -1. A name that appears more than once in
// the schema is refused: a positional record resolves it to one shared
// slot, and which occurrence's value it holds is a decode-order fact,
// not a per-entry one.
func precomputeGroup(s *encoding.Schema, inputs []string) (int, []int) {
	g := -1
	fields := make([]int, 0, len(inputs))
	for _, name := range inputs {
		fi := -1
		for i := range s.Fields {
			if s.Fields[i].Name != name {
				continue
			}
			if fi >= 0 {
				return -1, nil
			}
			fi = i
		}
		if fi < 0 {
			return -1, nil
		}
		fg, _, ok := s.FieldGroup(fi)
		if !ok || (g >= 0 && fg != g) {
			return -1, nil
		}
		g = fg
		fields = append(fields, fi)
	}
	return g, fields
}

// fill evaluates entry e of t's group and records the verdict.
func (p *precomputedFilter) fill(t *verdictTable, e uint32) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := &t.words[e>>5]
	shift := (e & 31) * 2
	if st := (atomic.LoadUint64(w) >> shift) & 3; st != verdictUnknown {
		return st
	}
	precomputeEntryEvals.Add(1)
	st := uint64(verdictPerRow)
	sc := t.scratch
	sc.ClearForRow()
	if err := t.dec.Decode(int(e), sc); err == nil {
		switch ok, ferr := p.inner(sc); {
		case ferr != nil:
			// re-evaluated (and the error raised) per row, only on the
			// rows that reach it
		case ok:
			st = verdictPass
		default:
			st = verdictFail
		}
	}
	atomic.OrUint64(w, st<<shift)
	return st
}

// precomputeInputs returns the schema field names filterer f reads when
// it is a pure per-record predicate of them, and ok=false when it is not
// eligible for per-entry precompute.
func precomputeInputs(f *types.Filterer, exts *ExtensionRegistry) ([]string, bool) {
	if f == nil {
		return nil, false
	}
	if exts != nil {
		if _, custom := exts.Filterers[f.Type]; custom {
			return nil, false
		}
	}
	switch f.Type {
	case types.FILTER_INCLUDE, types.FILTER_EXCLUDE, types.FILTER_RANGE,
		types.FILTER_DATE_RANGES, types.FILTER_NULL, types.FILTER_TRUE, types.FILTER_FALSE,
		types.FILTER_SET_CONTAINS_ANY, types.FILTER_SET_CONTAINS_ALL,
		types.FILTER_SET_CONTAINS_NONE, types.FILTER_SET_EQUALS:
		if f.Field == "" {
			return nil, false
		}
		return []string{f.Field}, true
	case types.FILTER_EXPRESSION:
		return exprPureInputs(f.Expression, exts)
	}
	return nil, false
}

// exprPureInputs returns the free identifiers of a filter expression
// when every call in it is to a pure function. Every non-callee
// identifier is returned; precomputeGroup then requires each to be a
// member field of one group, so a let-bound name, $env, or a feature
// output column (none of which is a group member) sends the filter per
// row.
func exprPureInputs(src string, exts *ExtensionRegistry) ([]string, bool) {
	if src == "" {
		return nil, false
	}
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil, false
	}
	v := &exprPurityVisitor{exts: exts, callees: map[*exprast.IdentifierNode]bool{}, seen: map[string]bool{}}
	// Two walks: Walk is post-order, so a CallNode is visited after its
	// callee identifier; collect callees first.
	exprast.Walk(&tree.Node, &exprCalleeVisitor{v: v})
	if v.impure {
		return nil, false
	}
	exprast.Walk(&tree.Node, v)
	if v.impure || len(v.idents) == 0 {
		return nil, false
	}
	return v.idents, true
}

type exprCalleeVisitor struct{ v *exprPurityVisitor }

func (c *exprCalleeVisitor) Visit(node *exprast.Node) {
	switch n := (*node).(type) {
	case *exprast.CallNode:
		id, ok := n.Callee.(*exprast.IdentifierNode)
		if !ok || !c.v.pureFunc(id.Value) {
			c.v.impure = true
			return
		}
		c.v.callees[id] = true
	case *exprast.BuiltinNode:
		if !c.v.pureFunc(n.Name) {
			c.v.impure = true
		}
	}
}

type exprPurityVisitor struct {
	exts    *ExtensionRegistry
	callees map[*exprast.IdentifierNode]bool
	seen    map[string]bool
	idents  []string
	impure  bool
}

func (v *exprPurityVisitor) Visit(node *exprast.Node) {
	id, ok := (*node).(*exprast.IdentifierNode)
	if !ok || v.callees[id] || v.seen[id.Value] {
		return
	}
	v.seen[id.Value] = true
	v.idents = append(v.idents, id.Value)
}

// exprPureSetBuiltins are the set built-ins setExprOptions registers.
var exprPureSetBuiltins = map[string]bool{
	"has_any": true, "has_all": true, "has_none": true,
	"popcount": true, "set_union": true, "set_intersect": true,
	"set_diff": true, "set_xor": true,
	exprModFuncName: true, // the builtin `%` rewrites into (expr_mod.go)
}

// pureFunc reports whether a call to name is a pure function of its
// arguments. An embedder ExprFunction is never assumed pure (it may read
// a clock, a counter or a remote service), even when it shadows a
// built-in; lookup() is pure only when every table is a static Rows map.
func (v *exprPurityVisitor) pureFunc(name string) bool {
	if v.exts != nil {
		for _, fn := range v.exts.ExprFunctions {
			if fn.Name == name {
				return false
			}
		}
	}
	if name == "lookup" {
		if v.exts == nil || len(v.exts.LookupTables) == 0 {
			return false
		}
		for _, t := range v.exts.LookupTables {
			if t.Lookup != nil {
				return false
			}
		}
		return true
	}
	if exprPureSetBuiltins[name] {
		return true
	}
	if name == "now" {
		return false
	}
	_, ok := exprbuiltin.Index[name]
	return ok
}
