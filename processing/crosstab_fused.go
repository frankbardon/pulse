package processing

import (
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// FusedCrosstabState is the per-record streaming accumulator used by the
// fused crosstab execution path. It composes a row-axis grouper chain, a
// column-axis grouper chain, and per-cell streaming aggregator state so
// the orchestrator can decode the cohort exactly once and route every
// filter-passing record into the correct cell / row-margin / column-
// margin / grand-margin / cross-axis-margin accumulator with no record
// buffering.
//
// Output contract: Finalize() returns a *types.Response byte-equal to
// the buffered RunCrosstab output for any (spec, schema, ext, record
// stream) the CanFuseCrosstab gate accepts — same MatrixPayload /
// long-shape Data row order, same normalization and divide-by-zero
// policy, same margin recomputation rule. The orchestrator is
// responsible for dispatching to the fused path only when
// CanFuseCrosstab holds; the defensive guards in this file echo that
// gate so a mis-routed request fails fast with a typed CodedError
// instead of producing a diverging result.
//
// Hot-path representation: rather than addressing cell and
// margin accumulators through a string-keyed map per record, the state
// interns each axis composite key into a per-axis integer index the
// first time it is observed and stores accumulators in a 2D slice
// indexed by (rowIdx, colIdx). Per-record lookup drops from two hash
// probes (a strings.Join allocation plus a map[crosstabCellKey]
// lookup) to two map[string]int probes (one per axis) plus pure slice
// indexing into cells/rowMargins/colMargins.
//
// Memory cost is O(cells + row margins + col margins + cross margins)
// rather than O(records): each cell holds one OnlineAggregator instance
// (the same one the streaming Process path drives), no record bucket.
// Significantly less heap pressure on wide cohorts where the buffered
// RunCrosstab materialises every filter-passing record.
type FusedCrosstabState struct {
	spec   *types.CrosstabSpec
	schema *encoding.Schema
	exts   *ExtensionRegistry

	// disableComponents mirrors Processor.disableComponents — set by
	// RunCrosstabFused before Update / Finalize drains the iterator so
	// the Components-emission tail in Finalize can be skipped under the
	// pulse.Options.DisableComponents / Request.DisableComponents
	// opt-out. Default false (components emitted, current behavior).
	disableComponents bool

	// Cell aggregator wiring. cellFactory is the constructor used to
	// lazily build a per-cell OnlineAggregator the first time a record
	// lands in that cell. cellField + cellLabel + cellAgg are the spec
	// fields shared by every cell / margin so Finalize can dispatch the
	// rich-aggregator path uniformly.
	cellAgg     *types.Aggregation
	cellLabel   string
	cellField   string
	cellFactory AggregatorFactory

	// Axis grouper instances. Each entry is a fusedAxisGrouper —
	// the constructed grouper plus its pre-resolved keying shape —
	// built once at NewFusedCrosstabState time. The keying method is
	// called per record per axis in Update; for a single-key axis the
	// only per-record allocation is the composite-key string.
	rowGroupers []fusedAxisGrouper
	colGroupers []fusedAxisGrouper

	// Per-axis key derivation. rowKeyer / colKeyer wrap rowGroupers /
	// colGroupers with the per-record scratch the cartesian-product
	// derivation writes into. Two instances, not one, because Update
	// holds the row result while it derives the column result and a
	// fusedAxisKeys value aliases its producing keyer's scratch.
	rowKeyer *fusedAxisKeyer
	colKeyer *fusedAxisKeyer

	// Per-axis key interners. rowIndex maps a composite axis key to the
	// integer index assigned at first sight; rowKeys preserves insertion
	// order so Finalize can re-emit sorted-by-string output without
	// re-deriving the (key → index) mapping. Mirror story for columns.
	// The interner replaces the per-record hash-table lookup at the
	// cells / rowMargins / colMargins map with single-int slice
	// addressing, and consolidates the per-record key normalization
	// (strings.Join) into the interner's hash probe.
	rowIndex map[string]int
	rowKeys  []string
	rowAxis  []types.AxisKey

	colIndex map[string]int
	colKeys  []string
	colAxis  []types.AxisKey

	// Per-cell accumulator matrix indexed by [rowIdx][colIdx]. The slice
	// grows column-wise as new columns are observed; existing rows are
	// extended in lockstep so cells[rowIdx][colIdx] is well-defined for
	// every observed (rowIdx, colIdx). Unobserved cells stay nil; the
	// Update path lazy-constructs the OnlineAggregator the first time
	// it routes a record into the cell.
	cells [][]OnlineAggregator

	// Per-cell record-count matrix indexed by [rowIdx][colIdx],
	// kept in lockstep with `cells` above so cellCounts[r][c] mirrors the
	// number of records routed through cells[r][c]. Incremented in the
	// hot loop alongside cell.UpdateRow; the same intern-growth path
	// that extends the cells matrix on first-sight axis keys extends
	// this slice in parallel (zero-initialised int rows). includedRecords
	// is the running sum — equal to sum(cellCounts) by construction and
	// reused at Finalize so the populateCrosstabComponents helper
	// doesn't re-iterate the matrix.
	cellCounts      [][]int
	includedRecords int

	// Per-cell null-input counter matrix indexed by
	// [rowIdx][colIdx], kept in lockstep with `cells` and `cellCounts`.
	// Incremented in Update when the record's cell-field value is null
	// (NumericValue returns ok=false) AND both axis composite keys
	// resolved (so the record landed in (rowIdx, colIdx)). cellCounts[r][c]
	// + cellNNull[r][c] = total records routed to (r, c). Used at
	// Finalize to populate the per-cell {n, n_null} universal floor for
	// CellComponents[r][c] byte-equal to the buffered path's
	// runCellAggregation walk.
	cellNNull [][]int

	// Per-row and per-column margin accumulators indexed by rowIdx /
	// colIdx. Allocated only when the corresponding margin slot in the
	// spec is enabled.
	rowMargins  []OnlineAggregator
	colMargins  []OnlineAggregator
	grandMargin OnlineAggregator

	// Per-margin record-count + null-input bookkeeping. Tracked
	// alongside each rowMargins / colMargins / grandMargin UpdateRow
	// call so the Finalize-time CrosstabComponents.RowMarginCounts /
	// ColumnMarginCounts / GrandTotalCount + RowMarginComponents /
	// ColumnMarginComponents / GrandTotalComponents emission can build
	// the universal floor {n, n_null} without re-scanning records. n =
	// non-null inputs to the margin field; nNull = null inputs the
	// margin aggregator skipped — together they tile the record count
	// routed through each margin slot.
	//
	// Slices grow in lockstep with rowMargins / colMargins via the
	// interner growth path. grandMarginCount / grandMarginNNull are
	// scalar counters since the grand margin is a single accumulator.
	rowMarginCount   []int
	rowMarginNNull   []int
	colMarginCount   []int
	colMarginNNull   []int
	grandMarginCount int
	grandMarginNNull int

	// Cross-axis margin map. Populated when spec.NormalizeWithin != nil
	// and spec.NormalizeOrDefault() is row or column. The key is the
	// (truncated rowPrefix, truncated colPrefix) pair per the depth rules
	// in processing/crosstab.go::crossActive (lines 410–448). Always
	// scalar-aggregated — the map cell gate above rejects map-valued
	// aggregators paired with normalization.
	//
	// Cross-margin cardinality is typically much smaller than cell
	// cardinality (one entry per outer-prefix slab, not one per cell)
	// so the cost of the additional hash probe is dwarfed by the per-
	// record cell update; kept as a map for simplicity.
	crossMargins  map[crosstabCellKey]OnlineAggregator
	crossActive   bool
	crossRowDepth int
	crossColDepth int

	// Same-axis partial-depth (normalize_level) bookkeeping. When
	// rowNormLevel != leaf the row denominator switches from the leaf
	// row margin to a margin keyed by the depth-truncated row prefix; the
	// fused path materialises that partial-margin accumulator alongside
	// the leaf one. Mirror story for column. We address each partial
	// margin via a dedicated interner so the partial accumulator slice
	// indexes parallel the leaf-axis interner structure.
	partialRowIndex   map[string]int
	partialRowKeys    []string
	partialRowMargins []OnlineAggregator
	partialColIndex   map[string]int
	partialColKeys    []string
	partialColMargins []OnlineAggregator
	rowNormLevel      int
	colNormLevel      int

	// Row counters.
	totalRows    int64
	filteredRows int64
}

// NewFusedCrosstabState constructs a FusedCrosstabState. Validates the
// spec against the gate's structural assumptions (every axis grouper
// must implement one of the two per-record keying interfaces —
// StreamableGrouper or MultiKeyStreamingGrouper — and the cell
// aggregator must be online) and
// pre-builds the per-axis grouper instances so the per-record hot path
// can compute composite keys with a single method call per axis entry.
//
// req-level slots that the gate excludes (Tests / Features /
// Regressions / Windows / Joins / two-pass attributes / Groups /
// top-level Aggregations) are NOT validated here — the caller is
// responsible for dispatching only when CanFuseCrosstab(req) returned
// true. AssertCanFuse is the defensive sibling that callers can use to
// fail fast when their dispatch shortcut may have missed an edge case.
func NewFusedCrosstabState(spec *types.CrosstabSpec, schema *encoding.Schema, ext *ExtensionRegistry) (*FusedCrosstabState, error) {
	if spec == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"NewFusedCrosstabState called with nil spec")
	}
	if err := validateCrosstabSpec(spec, nil); err != nil {
		return nil, err
	}
	if spec.Cell == nil {
		return nil, errors.NewCodedError(errors.PULSE_CROSSTAB_MISSING_CELL,
			"fused crosstab requires a Cell aggregation")
	}
	// Map-valued cell aggregators are incompatible with normalization;
	// mirror the same gate the buffered RunCrosstab applies.
	if spec.NormalizeOrDefault() != types.CrosstabNormalizeNone && spec.Cell.Type.MapValued() {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_CROSSTAB_NORMALIZE_MAP_VALUED,
			"crosstab normalize="+string(spec.NormalizeOrDefault())+
				" is incompatible with map-valued cell aggregator "+
				string(spec.Cell.Type),
			map[string]any{
				"aggregation": string(spec.Cell.Type),
				"normalize":   string(spec.NormalizeOrDefault()),
			})
	}

	cellFactory, ok := ext.LookupAggregator(spec.Cell.Type)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown crosstab cell aggregation type: %s", spec.Cell.Type),
			map[string]any{"aggregation": string(spec.Cell.Type)})
	}
	// Probe-construct the cell aggregator once to confirm it supports
	// the OnlineAggregator interface — the fused path drives every
	// accumulator through UpdateRow per record, so a non-online cell
	// aggregator (AGG_MEDIAN, AGG_PERCENTILE, AGG_ZSCORE) is excluded
	// by the gate and must surface as a typed error if mis-routed.
	probe, err := cellFactory(spec.Cell, schema)
	if err != nil {
		return nil, err
	}
	if _, ok := probe.(OnlineAggregator); !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("fused crosstab requires online cell aggregator; %s is buffered-only", spec.Cell.Type),
			map[string]any{"aggregation": string(spec.Cell.Type)})
	}

	rowGroupers, err := buildStreamableAxis(spec.Rows, schema, ext, "rows")
	if err != nil {
		return nil, err
	}
	colGroupers, err := buildStreamableAxis(spec.Columns, schema, ext, "columns")
	if err != nil {
		return nil, err
	}

	st := &FusedCrosstabState{
		spec:         spec,
		schema:       schema,
		exts:         ext,
		cellAgg:      spec.Cell,
		cellLabel:    spec.CellLabel(),
		cellField:    spec.Cell.Field,
		cellFactory:  cellFactory,
		rowGroupers:  rowGroupers,
		colGroupers:  colGroupers,
		rowKeyer:     newFusedAxisKeyer(rowGroupers),
		colKeyer:     newFusedAxisKeyer(colGroupers),
		rowIndex:     make(map[string]int, 64),
		colIndex:     make(map[string]int, 64),
		rowNormLevel: -1,
		colNormLevel: -1,
	}

	// Pre-size cells slice header to skip the first append's grow when
	// the cohort lands at least one record on the (0,0) cell.
	st.cells = make([][]OnlineAggregator, 0, 16)

	if spec.NeedsGrandMargin() {
		st.grandMargin, err = newOnlineCell(cellFactory, spec.Cell, schema)
		if err != nil {
			return nil, err
		}
	}

	// Partial-depth (normalize_level) bookkeeping. When the configured
	// level is the leaf the partial accumulators alias the leaf margin
	// (no extra accumulators needed); only the truncate-to-prefix case
	// allocates a parallel interner + slice.
	mode := spec.NormalizeOrDefault()
	if mode == types.CrosstabNormalizeRow {
		st.rowNormLevel = spec.NormalizeLevelOrLeaf(len(spec.Rows))
		if st.rowNormLevel < len(spec.Rows)-1 {
			st.partialRowIndex = make(map[string]int, 16)
		}
	}
	if mode == types.CrosstabNormalizeColumn {
		st.colNormLevel = spec.NormalizeLevelOrLeaf(len(spec.Columns))
		if st.colNormLevel < len(spec.Columns)-1 {
			st.partialColIndex = make(map[string]int, 16)
		}
	}

	// Cross-axis (normalize_within) bookkeeping.
	if mode != types.CrosstabNormalizeNone && spec.NormalizeWithin != nil {
		switch mode {
		case types.CrosstabNormalizeRow:
			st.crossRowDepth = spec.NormalizeLevelOrLeaf(len(spec.Rows))
			st.crossColDepth = *spec.NormalizeWithin
		case types.CrosstabNormalizeColumn:
			st.crossColDepth = spec.NormalizeLevelOrLeaf(len(spec.Columns))
			st.crossRowDepth = *spec.NormalizeWithin
		}
		st.crossMargins = make(map[crosstabCellKey]OnlineAggregator)
		st.crossActive = true
	}

	return st, nil
}

// fusedAxisGrouper is one position on a fused crosstab axis: the
// constructed grouper instance plus its pre-resolved per-record keying
// shape, so the hot path branches on a nil check instead of repeating a
// type assertion at every call site.
//
// Exactly one keying shape is authoritative per entry:
//
//   - single != nil — StreamableGrouper. The record maps to exactly one
//     bucket key (GROUP_CATEGORY / RANGE / ROUNDED / DATE /
//     DATE_RANGES / SET_VALUE and streamable extension groupers).
//   - multi != nil — MultiKeyStreamingGrouper. The record fans into N
//     bucket keys (GROUP_SET_PER_ELEMENT: one bucket per selected set
//     bit), so no single KeyFor answer exists.
//
// When an instance satisfies BOTH interfaces the fan-out shape wins and
// single is left nil, mirroring Processor.processStreamingGrouped which
// prefers KeysForRow over KeyForRow when both are present. Leaving
// single nil is deliberate: a reader that accidentally took the narrow
// path on a fanning grouper would silently drop buckets, and a nil
// dereference is a louder failure than wrong numbers. No built-in
// grouper implements both; an extension grouper could.
type fusedAxisGrouper struct {
	// grouper is the constructed instance, retained so readers needing
	// the Grouper-level optional interfaces (MetaGrouper for the
	// Finalize-time Components() emission) can assert against one value
	// regardless of the keying shape.
	grouper Grouper

	// single / multi are the pre-resolved keying shapes; see the type
	// doc for the mutual-exclusion rule.
	single StreamableGrouper
	multi  MultiKeyStreamingGrouper

	// field is the axis position's bound field name, taken from the
	// *types.Group spec. StreamableGrouper implementations bind their
	// field at factory time and ignore it; MultiKeyStreamingGrouper
	// takes it as a KeysForRow argument, exactly as the buffered
	// PartitionByAxis passes grp.Field to Grouper.Group.
	field string
}

// fansOut reports whether this axis position multiplies one record into
// N bucket keys. Callers routing records branch on this.
func (a fusedAxisGrouper) fansOut() bool { return a.multi != nil }

// keysForRow derives EVERY bucket key rec contributes at this axis
// position, in a single call. A single-key position appends its one key
// to buf and returns it (so a single-key axis allocates nothing here
// when the caller hands over a warm scratch slot); a fan-out position
// returns the grouper's own key slice — one entry per selected set
// element.
//
// ok=false means the position did not resolve for rec: a null / missing
// field on a single-key grouper, or a null set (or a set that emptied
// after Include filtering) on a fan-out grouper. Either way the axis
// stops here, matching the buffered path where such a record lands in
// no bucket of this position's partition and therefore in no composite
// bucket downstream. ErrGrouperKeyNull is normalised into ok=false so
// callers branch on one shape.
//
// The field argument is passed to the multi-key shape on purpose:
// MultiKeyStreamingGrouper.KeysForRow takes the field name because
// setPerElementGrouper is NOT field-bound — buffered PartitionByAxis
// hands grp.Field to Grouper.Group the same way. StreamableGrouper
// implementations bind their field at factory time, which is why
// streamableKeyForRow passes "" instead. Do not copy that "" into the
// multi path.
//
// Delegates the single-key case to streamableKeyForRow so the
// KeyForRow side-effect path (liveBuckets accumulation for
// MetaGrouper.Components) is preserved; the multi-key case has the
// mirror side effect inside setPerElementGrouper.KeysForRow.
func (a fusedAxisGrouper) keysForRow(rec *Record, buf []string) ([]string, bool, error) {
	if a.multi != nil {
		keys, ok, err := a.multi.KeysForRow(rec, a.field)
		if err != nil {
			if stderrors.Is(err, ErrGrouperKeyNull) {
				return nil, false, nil
			}
			return nil, false, err
		}
		// An empty key slice with ok=true is not a valid
		// MultiKeyStreamingGrouper response; treat it as ok=false rather
		// than emitting a zero-width product level that would silently
		// erase the axis.
		if !ok || len(keys) == 0 {
			return nil, false, nil
		}
		return keys, true, nil
	}
	if a.single == nil {
		// Neither keying shape resolved. buildStreamableAxis rejects
		// this at construction, so reaching here means an axis entry was
		// assembled outside that path.
		return nil, false, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"fused crosstab axis grouper implements neither keying interface",
			map[string]any{"field": a.field})
	}
	key, ok, err := streamableKeyForRow(a.single, rec)
	if err != nil {
		if stderrors.Is(err, ErrGrouperKeyNull) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	return append(buf, key), true, nil
}

// classifyFusedAxisGrouper resolves the keying shape of an already
// constructed (and extension-aware-hooked) grouper instance. Returns a
// zero-value entry with both shapes nil when the instance implements
// neither keying interface — callers treat that as ineligible.
func classifyFusedAxisGrouper(instance Grouper, field string) fusedAxisGrouper {
	entry := fusedAxisGrouper{grouper: instance, field: field}
	if multi, ok := instance.(MultiKeyStreamingGrouper); ok {
		entry.multi = multi
		return entry
	}
	if single, ok := instance.(StreamableGrouper); ok {
		entry.single = single
	}
	return entry
}

// buildStreamableAxis constructs the per-axis grouper chain. Every
// instance must implement one of the two per-record keying interfaces —
// StreamableGrouper (one key per record) or MultiKeyStreamingGrouper
// (N keys per record, the GROUP_SET_PER_ELEMENT fan-out); the resolved
// shape is recorded on the returned fusedAxisGrouper so the per-record
// path never re-asserts. A grouper implementing neither (e.g.
// GROUP_QUANTILE, which needs a finalize-time sorted view) surfaces a
// typed CodedError so the caller can dispatch back to the buffered
// RunCrosstab path. axisName is the human-readable axis label
// ("rows" / "columns") used in the error details.
//
// Mirrors axisStreamable in crosstab_fused_gate.go — the gate probes
// the same interfaces on a throwaway instance, this builds the
// long-lived ones. The two must stay in lockstep: anything the gate
// admits, this must construct.
func buildStreamableAxis(axis []*types.Group, schema *encoding.Schema, ext *ExtensionRegistry, axisName string) ([]fusedAxisGrouper, error) {
	if len(axis) == 0 {
		return nil, nil
	}
	out := make([]fusedAxisGrouper, 0, len(axis))
	for i, grp := range axis {
		if grp == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("fused crosstab %s axis has nil grouper at position %d", axisName, i),
				map[string]any{"axis": axisName, "position": i})
		}
		factory, ok := ext.LookupGrouper(grp.Type)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown group type: %s", grp.Type),
				map[string]any{"axis": axisName, "position": i, "group_type": string(grp.Type)})
		}
		instance, err := factory(grp, schema)
		if err != nil {
			return nil, err
		}
		// Must run BEFORE the interface assertion: a named range
		// `table:` on GROUP_DATE_RANGES resolves lazily through the
		// ExtensionAware hook, and the grouper is not usable until it has.
		ApplyGrouperExtensions(instance, ext)
		entry := classifyFusedAxisGrouper(instance, grp.Field)
		if entry.single == nil && entry.multi == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("fused crosstab %s axis grouper %s implements neither StreamableGrouper nor MultiKeyStreamingGrouper", axisName, grp.Type),
				map[string]any{"axis": axisName, "position": i, "group_type": string(grp.Type)})
		}
		out = append(out, entry)
	}
	return out, nil
}

// newOnlineCell constructs a fresh per-cell / per-margin streaming
// aggregator instance. Every cell and every margin runs its own
// instance so the running state for one bucket does not leak into
// another; the factory is cheap on the cell aggregators the gate admits.
func newOnlineCell(factory AggregatorFactory, agg *types.Aggregation, schema *encoding.Schema) (OnlineAggregator, error) {
	instance, err := factory(agg, schema)
	if err != nil {
		return nil, err
	}
	online, ok := instance.(OnlineAggregator)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("fused crosstab cell aggregator %s is not online", agg.Type),
			map[string]any{"aggregation": string(agg.Type)})
	}
	return online, nil
}

// AssertCanFuse is the defensive companion check the orchestrator calls
// before driving the state with records. The static gate
// (CanFuseCrosstab) is the load-bearing check; this
// method echoes the most failure-prone subset so a stale gate that
// drifts past a newly added request slot still fails fast with a typed
// error rather than producing a divergent fused result.
//
// Returns nil when the request is structurally compatible with the
// fused path. Returns a typed CodedError otherwise.
func (s *FusedCrosstabState) AssertCanFuse(req *types.Request) error {
	if req == nil {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"FusedCrosstabState.AssertCanFuse called with nil request")
	}
	if len(req.Tests) > 0 {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"fused crosstab path does not implement tier-1 tests; gate should have rejected",
			map[string]any{"tests": len(req.Tests)})
	}
	if len(req.PostTests) > 0 {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"fused crosstab path does not implement tier-2 post-tests; gate should have rejected",
			map[string]any{"post_tests": len(req.PostTests)})
	}
	if len(req.Features) > 0 {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"fused crosstab path does not implement features; gate should have rejected",
			map[string]any{"features": len(req.Features)})
	}
	if len(req.Regressions) > 0 {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"fused crosstab path does not implement regressions; gate should have rejected",
			map[string]any{"regressions": len(req.Regressions)})
	}
	if len(req.Windows) > 0 {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"fused crosstab path does not implement windows; gate should have rejected",
			map[string]any{"windows": len(req.Windows)})
	}
	if len(req.Joins) > 0 {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"fused crosstab path does not implement joins; gate should have rejected",
			map[string]any{"joins": len(req.Joins)})
	}
	// Two-pass attributes (ZSCORE / TSCORE / NORMALIZED / regression-
	// derived) need population stats over the filter-passing record set
	// before the per-record bucket route can value them; reject so the
	// fallback path picks them up.
	for _, attr := range req.Attributes {
		if attr == nil {
			continue
		}
		if requiresTwoPass(attr.Type) {
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("fused crosstab path does not implement two-pass attribute %s; gate should have rejected", attr.Type),
				map[string]any{"attribute": string(attr.Type)})
		}
	}
	return nil
}

// internRowKey returns the integer index assigned to rowKey, allocating
// a new index on first sight and recording the per-grouper tuple for
// Finalize's MatrixPayload re-emission.
func (s *FusedCrosstabState) internRowKey(rowKey string, tuple types.AxisKey) int {
	if idx, ok := s.rowIndex[rowKey]; ok {
		return idx
	}
	idx := len(s.rowKeys)
	s.rowIndex[rowKey] = idx
	s.rowKeys = append(s.rowKeys, rowKey)
	s.rowAxis = append(s.rowAxis, tuple)
	// Grow the cells matrix to carry one row per interned row index.
	// The new row is sized to the current column count so existing
	// (rowIdx<idx, colIdx) cells remain at their indices.
	s.cells = append(s.cells, make([]OnlineAggregator, len(s.colKeys)))
	// cellCounts grows in lockstep with cells so the buffered and
	// fused paths can later read the same (r, c) coordinates.
	s.cellCounts = append(s.cellCounts, make([]int, len(s.colKeys)))
	// cellNNull grows in lockstep with cellCounts so the per-cell
	// universal floor (n, n_null) is addressable by the same (r, c)
	// coordinates regardless of whether the column was interned before or
	// after the row.
	s.cellNNull = append(s.cellNNull, make([]int, len(s.colKeys)))
	if s.rowMargins != nil || s.spec.NeedsRowMargin() {
		// Lazy-init row margins slice on first row to skip the
		// allocation when no row-margin is requested.
		if s.rowMargins == nil {
			s.rowMargins = make([]OnlineAggregator, 0, 16)
		}
		s.rowMargins = append(s.rowMargins, nil)
		// Per-row count + null bookkeeping in lockstep with the
		// margin accumulator slice so Finalize can address every margin
		// slot by rowIdx without re-scanning records.
		s.rowMarginCount = append(s.rowMarginCount, 0)
		s.rowMarginNNull = append(s.rowMarginNNull, 0)
	}
	return idx
}

// internColKey returns the integer index assigned to colKey, allocating
// a new index on first sight and extending every existing row's column
// slice in lockstep so cells[rowIdx][colIdx] is addressable for every
// interned rowIdx.
func (s *FusedCrosstabState) internColKey(colKey string, tuple types.AxisKey) int {
	if idx, ok := s.colIndex[colKey]; ok {
		return idx
	}
	idx := len(s.colKeys)
	s.colIndex[colKey] = idx
	s.colKeys = append(s.colKeys, colKey)
	s.colAxis = append(s.colAxis, tuple)
	// Extend every existing row by one column. Doubling capacity is
	// unnecessary here since the column count grows independently
	// per cohort and we want stable index addressing.
	for i := range s.cells {
		s.cells[i] = append(s.cells[i], nil)
	}
	// cellCounts matches the cells matrix shape — extend each
	// existing row by one zero-initialised column slot.
	for i := range s.cellCounts {
		s.cellCounts[i] = append(s.cellCounts[i], 0)
	}
	// cellNNull mirrors the cellCounts shape — extend each
	// existing row by one zero-initialised column slot so the per-cell
	// null counter is addressable for every observed (rowIdx, colIdx).
	for i := range s.cellNNull {
		s.cellNNull[i] = append(s.cellNNull[i], 0)
	}
	if s.colMargins != nil || s.spec.NeedsColumnMargin() {
		if s.colMargins == nil {
			s.colMargins = make([]OnlineAggregator, 0, 16)
		}
		s.colMargins = append(s.colMargins, nil)
		// Per-column count + null bookkeeping in lockstep.
		s.colMarginCount = append(s.colMarginCount, 0)
		s.colMarginNNull = append(s.colMarginNNull, 0)
	}
	return idx
}

// internPartialRowKey is the partial-depth (normalize_level) sibling of
// internRowKey. Constructs the partial accumulator on first sight.
func (s *FusedCrosstabState) internPartialRowKey(key string) (int, error) {
	if idx, ok := s.partialRowIndex[key]; ok {
		return idx, nil
	}
	idx := len(s.partialRowKeys)
	s.partialRowIndex[key] = idx
	s.partialRowKeys = append(s.partialRowKeys, key)
	agg, err := newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
	if err != nil {
		return -1, err
	}
	s.partialRowMargins = append(s.partialRowMargins, agg)
	return idx, nil
}

// internPartialColKey is the column-axis sibling of internPartialRowKey.
func (s *FusedCrosstabState) internPartialColKey(key string) (int, error) {
	if idx, ok := s.partialColIndex[key]; ok {
		return idx, nil
	}
	idx := len(s.partialColKeys)
	s.partialColIndex[key] = idx
	s.partialColKeys = append(s.partialColKeys, key)
	agg, err := newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
	if err != nil {
		return -1, err
	}
	s.partialColMargins = append(s.partialColMargins, agg)
	return idx, nil
}

// Update folds a single filter-passing record into the cell, margin,
// and cross-margin accumulators it touches. The caller is responsible
// for running filters / row-local attributes / features / etc. before
// passing the record in — by the time Update sees a record it is
// already a filter-passing observation in the same sense the buffered
// RunCrosstab path treats its filtered slice. Records skipped by the
// caller's filter contribute to totalRows only via the totalRows
// counter the caller separately advances.
//
// Axis-key nullity is tracked independently per axis. A record whose
// row-axis composite key is non-null AND col-axis composite key is
// non-null lands a cell update at the (rowIdx, colIdx) intersection.
// A record whose row-axis key is non-null but col-axis key is null
// (or vice versa) still contributes to the appropriate axis margin
// (rowMargin via the row key, colMargin via the col key), matching the
// buffered RunCrosstab path: there `PartitionByAxis(spec.Rows, filtered)`
// builds the row partition over all filtered records with valid row
// keys regardless of column key nullity (and vice versa). The grand
// margin counts every filter-passing record regardless of any axis
// nullity. Partial-depth (normalize_level) margins follow the same
// per-axis gate: row partial-margin updates whenever the row axis key
// is non-null. Cross-axis (normalize_within) margins update only when
// BOTH the row prefix at crossRowDepth AND the column prefix at
// crossColDepth are non-null — mirroring the buffered joined-partition
// behaviour at processing/crosstab.go::crossActive (lines ~410-448),
// where the partition only contains records whose prefix groupers all
// produce non-null keys.
func (s *FusedCrosstabState) Update(rec *Record) error {
	if rec == nil {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"FusedCrosstabState.Update called with nil record")
	}
	s.filteredRows++

	rowKeys, err := s.rowKeyer.derive(rec)
	if err != nil {
		return err
	}
	colKeys, err := s.colKeyer.derive(rec)
	if err != nil {
		return err
	}

	// E2-S2 routing shim. Derivation above now yields a SET of composite
	// keys per axis; routing a record into every cell / margin of that
	// set (cells per distinct full-key pair, margins per deduped prefix)
	// is E2-S3 and lands in the block below. Until then the routing
	// reads the first member of each key set, which is EXACT for a
	// single-key axis — the set has exactly one member at every depth,
	// so this path stays byte-identical to the pre-E2 implementation —
	// and deliberately partial for a fan-out axis, which the fused gate
	// admits as of E2-S1. A fan-out axis therefore lands its record in
	// one of its buckets rather than all of them; it no longer errors,
	// and per-position Components stay correct because keysForRow ran
	// once per position with all its side effects.
	rowOk, rowDepth := rowKeys.ok, rowKeys.depth
	colOk, colDepth := colKeys.ok, colKeys.depth
	var rowKey, colKey string
	var rowTuple, colTuple types.AxisKey
	if rowOk {
		rowKey, rowTuple = rowKeys.keys()[0], rowKeys.tuples[0]
	}
	if colOk {
		colKey, colTuple = colKeys.keys()[0], colKeys.tuples[0]
	}

	// Grand margin counts every filter-passing record regardless of axis
	// nullity — mirrors the buffered RunCrosstab path where
	// spec.NeedsGrandMargin aggregates over all of `filtered`, never
	// touching the per-axis partitions.
	if s.grandMargin != nil {
		if err := s.grandMargin.UpdateRow(rec, s.cellField); err != nil {
			return err
		}
		// Grand-margin universal-floor counters. Routed through
		// the same NumericValue probe used by the cell + buffered paths
		// so n_null is byte-equal across paths.
		s.grandMarginCount++
		if _, ok := rec.NumericValue(s.cellField); !ok {
			s.grandMarginNNull++
		}
	}

	// Row-axis updates (independent of column-axis nullity). A non-null
	// composite row key is enough to intern the row and update its row
	// margin; the buffered PartitionByAxis(spec.Rows, filtered) bucket for
	// this row key contains every record with this row key, even those
	// whose column key is null.
	var rowIdx int
	if rowOk {
		rowIdx = s.internRowKey(rowKey, rowTuple)
		if s.rowMargins != nil {
			mar := s.rowMargins[rowIdx]
			if mar == nil {
				mar, err = newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
				if err != nil {
					return err
				}
				s.rowMargins[rowIdx] = mar
			}
			if err := mar.UpdateRow(rec, s.cellField); err != nil {
				return err
			}
			// Row-margin universal-floor counters. NumericValue
			// probe mirrors the cell-path null tracking so the buffered
			// runCellAggregation walk and the fused Update loop emit
			// byte-equal (n, n_null) on the row margin.
			s.rowMarginCount[rowIdx]++
			if _, ok := rec.NumericValue(s.cellField); !ok {
				s.rowMarginNNull[rowIdx]++
			}
		}
		if s.partialRowIndex != nil {
			pkey := truncateCompositeKey(rowKey, s.rowNormLevel)
			pIdx, perr := s.internPartialRowKey(pkey)
			if perr != nil {
				return perr
			}
			if err := s.partialRowMargins[pIdx].UpdateRow(rec, s.cellField); err != nil {
				return err
			}
		}
	}

	// Column-axis updates (independent of row-axis nullity). Mirror story
	// for the col margin and column partial-depth margin.
	var colIdx int
	if colOk {
		colIdx = s.internColKey(colKey, colTuple)
		if s.colMargins != nil {
			mar := s.colMargins[colIdx]
			if mar == nil {
				mar, err = newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
				if err != nil {
					return err
				}
				s.colMargins[colIdx] = mar
			}
			if err := mar.UpdateRow(rec, s.cellField); err != nil {
				return err
			}
			// Column-margin universal-floor counters in lockstep
			// with the margin accumulator update.
			s.colMarginCount[colIdx]++
			if _, ok := rec.NumericValue(s.cellField); !ok {
				s.colMarginNNull[colIdx]++
			}
		}
		if s.partialColIndex != nil {
			pkey := truncateCompositeKey(colKey, s.colNormLevel)
			pIdx, perr := s.internPartialColKey(pkey)
			if perr != nil {
				return perr
			}
			if err := s.partialColMargins[pIdx].UpdateRow(rec, s.cellField); err != nil {
				return err
			}
		}
	}

	// Cell update — only when BOTH axis composite keys are non-null. The
	// cells matrix grows lazily via the interners above; if a row was
	// interned for margin tracking but no record landed both keys, the
	// (rowIdx, colIdx) slot stays nil and Finalize emits an absent cell.
	if rowOk && colOk {
		cell := s.cells[rowIdx][colIdx]
		if cell == nil {
			cell, err = newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
			if err != nil {
				return err
			}
			s.cells[rowIdx][colIdx] = cell
		}
		if err := cell.UpdateRow(rec, s.cellField); err != nil {
			return err
		}
		// Per-cell record-count tracking sits in the same
		// instruction sequence as the aggregator UpdateRow — a single
		// pointer-add into the matrix slot. includedRecords mirrors the
		// running sum so Finalize can populate
		// Components.Crosstab.IncludedRecords / ExcludedRecords without a
		// second matrix pass.
		s.cellCounts[rowIdx][colIdx]++
		s.includedRecords++
		// Per-cell null-input counter. NumericValue probe mirrors
		// the buffered runCellAggregation walk — a record whose
		// cell-field value is null contributes to cellNNull but still
		// counts toward cellCounts (every record routed to the cell
		// shows up in CellCounts; the (n, n_null) split splits it into
		// non-null contributors vs. null inputs). cellCounts =
		// cellNNull + cell.frozenN (the orchestrator-visible n).
		if _, ok := rec.NumericValue(s.cellField); !ok {
			s.cellNNull[rowIdx][colIdx]++
		}
	}

	// Cross-axis (normalize_within) margin. Buffered
	// processing/crosstab.go::crossActive partitions filtered records by
	// spec.Rows[:rowDepth+1] ++ spec.Columns[:colDepth+1] and only buckets
	// records whose prefix groupers all produce non-null keys. The fused
	// equivalent: gate on row prefix at crossRowDepth+1 succeeding AND col
	// prefix at crossColDepth+1 succeeding. Note this is per-PREFIX (not
	// full-axis) — a record whose deeper grouper is null but whose prefix
	// groupers all succeed still contributes to the cross margin.
	if s.crossActive {
		if rowDepth > s.crossRowDepth && colDepth > s.crossColDepth {
			rPart := rowKeys.prefixes(s.crossRowDepth)[0]
			cPart := colKeys.prefixes(s.crossColDepth)[0]
			ckey := crosstabCellKey{row: rPart, col: cPart}
			mar, present := s.crossMargins[ckey]
			if !present {
				mar, err = newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
				if err != nil {
					return err
				}
				s.crossMargins[ckey] = mar
			}
			if err := mar.UpdateRow(rec, s.cellField); err != nil {
				return err
			}
		}
	}
	return nil
}

// AddTotalRow advances the unfiltered row counter. Called once per
// physical record the orchestrator decodes from the cohort regardless
// of whether the record passes the filter chain. Separate from Update
// so the orchestrator can advance the total even on filtered-out
// records.
func (s *FusedCrosstabState) AddTotalRow() {
	s.totalRows++
}

// SetTotalRows lets callers set the counter directly when they have
// already counted (e.g. CountRecords header-fast path) or when they
// process rows in bulk outside the Update loop. Either AddTotalRow or
// SetTotalRows is enough; both interfaces are provided so the
// orchestrator can pick whichever is more convenient.
func (s *FusedCrosstabState) SetTotalRows(n int64) {
	s.totalRows = n
}

// fusedAxisKeys is the per-record key-derivation result for one fused
// crosstab axis.
//
// An axis carrying only single-key groupers resolves to exactly one
// composite key. An axis carrying one or more MultiKeyStreamingGrouper
// positions resolves to a SET of composite keys — the cartesian product
// of every position's key set, taken in position order. That is not an
// invention: buffered PartitionByAxis (processing/crosstab.go:48) calls
// grouper.Group(records, field) for axis[0], recurses into axis[1:]
// once per resulting bucket, and composes k + sep + childKey, so a
// record selecting N labels at one position and M at another appears in
// N*M composite buckets.
//
// levels[d] is the deduped set of composite keys formed by joining
// positions 0..d inclusive; len(levels) == depth, the number of
// positions that produced a key before the first unresolved one. The
// full-axis key set is levels[depth-1] and is meaningful only when ok
// is true (depth == len(groupers)). When ok is false the axis did not
// fully resolve, and the prefix levels that DID resolve are still
// returned so the partial-depth (normalize_level) and cross-axis
// (normalize_within) margin consumers keep their inputs — the same
// contract the single-key predecessor of this type carried.
//
// tuples is parallel to keys(): tuples[j] is the types.AxisKey
// component tuple for keys()[j]. Populated only when ok.
//
// Lifetime: levels aliases scratch owned by the fusedAxisKeyer that
// produced the value and is invalidated by the next derive call on THAT
// keyer. The row axis and the column axis own separate keyers, so a
// caller may hold both results at once — which Update does. The
// types.AxisKey entries in tuples are freshly allocated per record
// because the axis-key interner retains them.
type fusedAxisKeys struct {
	levels [][]string
	tuples []types.AxisKey
	depth  int
	ok     bool
}

// keys returns the full-axis composite key set, or nil when the axis
// did not fully resolve for this record.
func (k fusedAxisKeys) keys() []string {
	if !k.ok {
		return nil
	}
	return k.levels[k.depth-1]
}

// prefixes returns the deduped composite prefix-key set at depth d
// (positions 0..d inclusive). Callers must establish d < k.depth first;
// the cross-axis margin gate in Update does exactly that.
func (k fusedAxisKeys) prefixes(d int) []string { return k.levels[d] }

// fusedAxisKeyer owns one axis's grouper chain plus the per-record
// scratch its key sets are derived into. One keyer per axis.
//
// THE ONCE-PER-RECORD RULE. derive calls each position's keysForRow
// exactly once per record and only then forms the cartesian product
// from the resulting per-position key sets. This is structural rather
// than incidental: the product is formed by expandAxisLevel, which
// takes strings only — no Grouper, no *Record — so the product walk
// cannot re-derive a position's keys even by accident.
//
// The rule is load-bearing for Components parity, not just for speed.
// KeysForRow has a side effect: setPerElementGrouper.trackSetPerElementRow
// folds one observation per selected label into liveBuckets, which
// MetaGrouper.Components() emits at Finalize. Buffered builds those
// counts by re-running each axis position's grouper independently over
// the FULL filtered set (Processor.axisComponentsFor,
// processing/crosstab.go:251), so each record must be observed exactly
// once per position, cohort-wide. Deriving keys inside the product walk
// would call KeysForRow once per parent key and inflate every bucket
// count by the width of the fan in front of it — silently, because the
// cell values would stay correct while Components.buckets[].count went
// wrong.
type fusedAxisKeyer struct {
	groupers []fusedAxisGrouper

	// levels / comp / parent are the per-depth scratch, all reused
	// across records. levels[d] holds the composite keys at depth d;
	// comp[d][j] is the key position d contributed to levels[d][j];
	// parent[d][j] is the index of that element's parent in levels[d-1].
	// The parent chain lets tupleFor rebuild a types.AxisKey with a
	// single allocation per composite key instead of carrying a growing
	// tuple slice through every level of the product.
	levels [][]string
	comp   [][]string
	parent [][]int

	// keyBuf is the one-element landing slot a single-key position
	// writes its key into, so a single-key axis allocates nothing per
	// record beyond the tuple the interner retains.
	keyBuf [1]string

	// dedup is the scratch a fan-out position's key slice is deduped
	// into. Deduping per position (rather than over the assembled
	// product) keeps every level duplicate-free by induction without a
	// quadratic membership scan over the product.
	dedup []string

	// tuples is the reused outer slice for the per-record tuple set; the
	// types.AxisKey elements themselves are freshly allocated.
	tuples []types.AxisKey
}

// newFusedAxisKeyer wraps an already-constructed axis grouper chain with
// its derivation scratch. The groupers slice is shared with the
// FusedCrosstabState field it was built from; the keyer never mutates it.
func newFusedAxisKeyer(groupers []fusedAxisGrouper) *fusedAxisKeyer {
	k := &fusedAxisKeyer{groupers: groupers}
	k.ensureDepth(len(groupers))
	return k
}

// ensureDepth grows the per-depth scratch so indices 0..n-1 are
// addressable. Called once at construction; the inner slices grow
// themselves on first use and are reused thereafter.
func (k *fusedAxisKeyer) ensureDepth(n int) {
	for len(k.levels) < n {
		k.levels = append(k.levels, nil)
		k.comp = append(k.comp, nil)
		k.parent = append(k.parent, nil)
	}
}

// derive computes the composite key set for one record across the axis's
// ordered grouper chain, plus the per-depth prefix key sets, plus the
// types.AxisKey tuple for each full key.
//
// Null semantics are per position and unchanged from the single-key
// predecessor: a position returning ok=false (or ErrGrouperKeyNull)
// stops the walk, ok is false, and the prefix levels already built are
// still returned. A fan-out position returning ok=false — a null set,
// or a set that emptied after Include filtering — collapses the whole
// axis exactly the same way, matching buffered, where such a record
// lands in no bucket of that position's partition.
func (k *fusedAxisKeyer) derive(rec *Record) (fusedAxisKeys, error) {
	n := len(k.groupers)
	if n == 0 {
		// Empty axis is a programming bug — validateCrosstabSpec rejects
		// zero-grouper axes up-front so reaching here means the gate
		// drifted. Surface a typed error rather than a silent placement.
		return fusedAxisKeys{}, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"fused crosstab axis has zero groupers; spec validation should have rejected")
	}

	// Single-position single-key axis — the overwhelmingly common shape,
	// and the successor of the old case-1 fast path. Skips the product
	// machinery entirely and reuses the warm scratch, so the only
	// per-record allocation left is the types.AxisKey the axis interner
	// retains (2 allocs / 32 B, down from the pre-fan-out derivation's
	// 3 / 48 B, which built a fresh partial-key slice per record too).
	if n == 1 && !k.groupers[0].fansOut() {
		keys, ok, err := k.groupers[0].keysForRow(rec, k.keyBuf[:0])
		if err != nil {
			return fusedAxisKeys{}, err
		}
		if !ok {
			return fusedAxisKeys{levels: k.levels[:0]}, nil
		}
		k.levels[0] = append(k.levels[0][:0], keys[0])
		k.tuples = append(k.tuples[:0], types.AxisKey{keys[0]})
		return fusedAxisKeys{levels: k.levels[:1], tuples: k.tuples, depth: 1, ok: true}, nil
	}

	depth := 0
	for i := 0; i < n; i++ {
		// Exactly one derivation call per position per record. Nothing
		// below this line may call back into a grouper.
		keys, ok, err := k.groupers[i].keysForRow(rec, k.keyBuf[:0])
		if err != nil {
			return fusedAxisKeys{}, err
		}
		if !ok {
			break
		}
		if len(keys) > 1 {
			k.dedup = dedupeAxisKeys(k.dedup[:0], keys)
			keys = k.dedup
		}
		if i == 0 {
			k.levels[0] = append(k.levels[0][:0], keys...)
			// comp[0] aliases levels[0]: at depth 0 the composite key IS
			// the component key. Re-aliased every record, so the [:0]
			// reuse above stays correct.
			k.comp[0] = k.levels[0]
		} else {
			k.levels[i], k.comp[i], k.parent[i] = expandAxisLevel(
				k.levels[i-1], keys,
				k.levels[i][:0], k.comp[i][:0], k.parent[i][:0])
		}
		depth = i + 1
	}

	res := fusedAxisKeys{levels: k.levels[:depth], depth: depth, ok: depth == n}
	if res.ok {
		full := k.levels[depth-1]
		k.tuples = k.tuples[:0]
		for j := range full {
			k.tuples = append(k.tuples, k.tupleFor(depth, j))
		}
		res.tuples = k.tuples
	}
	return res, nil
}

// tupleFor rebuilds the types.AxisKey for element j of the level at the
// given depth by walking the parent chain back to depth 0. One
// allocation per composite key, sized exactly.
func (k *fusedAxisKeyer) tupleFor(depth, j int) types.AxisKey {
	t := make(types.AxisKey, depth)
	for d := depth - 1; d > 0; d-- {
		t[d] = k.comp[d][j]
		j = k.parent[d][j]
	}
	t[0] = k.comp[0][j]
	return t
}

// expandAxisLevel forms one level of the axis cartesian product: every
// composite key already accumulated in prev, crossed with every key the
// next position produced. Parent-major order, so the emitted sequence
// matches the buffered PartitionByAxis recursion, which walks axis[0]'s
// buckets and recurses into axis[1:] within each one.
//
// It takes strings only — no Grouper, no *Record. That is deliberate:
// it makes the once-per-record derivation rule an invariant of the code
// shape rather than a convention a later edit could quietly break. See
// fusedAxisKeyer for why the rule matters.
//
// prev is duplicate-free by induction and keys is deduped by the
// caller, so the emitted level is duplicate-free without a quadratic
// membership scan over the product. (Two entries could only collide if
// a bucket key itself contained crosstabAxisKeySep, which is equally
// ambiguous on the buffered path.)
func expandAxisLevel(prev, keys, dstKeys, dstComp []string, dstParent []int) ([]string, []string, []int) {
	for pi, parent := range prev {
		for _, key := range keys {
			dstKeys = append(dstKeys, parent+crosstabAxisKeySep+key)
			dstComp = append(dstComp, key)
			dstParent = append(dstParent, pi)
		}
	}
	return dstKeys, dstComp, dstParent
}

// dedupeAxisKeys appends the distinct members of keys to dst, preserving
// first-seen order. Linear scan: a position's fan width is bounded by
// the bound set field's dictionary size (<= 64 for set_u64, typically a
// handful), so a map would cost more than it saves. Only called for
// positions that returned more than one key, so single-key axes never
// pay for it.
func dedupeAxisKeys(dst []string, keys []string) []string {
	for _, key := range keys {
		duplicate := false
		for _, seen := range dst {
			if seen == key {
				duplicate = true
				break
			}
		}
		if !duplicate {
			dst = append(dst, key)
		}
	}
	return dst
}

// streamableKeyForRow drives a SINGLE-KEY axis grouper through its
// KeyForRow side-effect path when available so MetaGrouper.Components()
// observes the per-axis-position bucket accumulation under the fused
// crosstab path. Falls back to KeyFor for streamable groupers that do
// not implement the per-record StreamingGrouper sibling (extension
// groupers without bucket-count emission). The returned (key, ok)
// pair mirrors StreamingGrouper.KeyForRow: ok=false signals "skip the
// row" (null axis key) without a sentinel error; a non-nil error
// surfaces a typed CodedError unchanged.
//
// Callers go through fusedAxisGrouper.keyForRow rather than calling
// this directly, so the fan-out shape cannot reach it.
//
// The side effect populates the grouper's liveBuckets map (or
// the per-grouper equivalent) so the Finalize-time MetaGrouper
// Components() call returns the cohort-wide buckets emission. The
// alternative — re-running each grouper at Finalize against a
// materialised record slice — defeats the fused path's memory profile
// (O(cells + margins), not O(records)).
func streamableKeyForRow(g StreamableGrouper, rec *Record) (string, bool, error) {
	if sg, ok := g.(StreamingGrouper); ok {
		key, ok, err := sg.KeyForRow(rec, "")
		return key, ok, err
	}
	key, err := g.KeyFor(rec)
	if err != nil {
		if stderrors.Is(err, ErrGrouperKeyNull) {
			return "", false, nil
		}
		return "", false, err
	}
	return key, true, nil
}

// TotalRows returns the total-row counter for use after Finalize. The
// orchestrator's metadata block reads from this. Exposed publicly so
// tests can assert the bookkeeping without inspecting unexported state.
func (s *FusedCrosstabState) TotalRows() int64 { return s.totalRows }

// FilteredRows returns the count of records that reached Update. The
// orchestrator's metadata block reads from this. Exposed publicly for
// the same reason as TotalRows.
func (s *FusedCrosstabState) FilteredRows() int64 { return s.filteredRows }

// Finalize closes the streaming accumulators and emits the *types.Response
// in the same shape RunCrosstab produces — MatrixPayload for shape=matrix,
// long-form Response.Data with margin tags for shape=long. Normalization
// (mode + normalize_level + normalize_within) is applied here using the
// same denominator + divide-by-zero rules the buffered path uses.
//
// Safe to call exactly once after the last Update. Subsequent calls
// observe drained accumulator state and produce undefined output.
func (s *FusedCrosstabState) Finalize() (*types.Response, error) {
	if s.totalRows < s.filteredRows {
		// The orchestrator forgot to call AddTotalRow alongside Update;
		// keep metadata internally consistent rather than emitting a
		// total that is smaller than the filtered count.
		s.totalRows = s.filteredRows
	}

	// Deterministic key emission order: order by per-axis-position include
	// list, falling through to alphabetical when a position carries no
	// Include. The buffered RunCrosstab does the same via PartitionByAxis's
	// per-level orderKeysByInclude recursion; the fused path reproduces that
	// as a flat multi-key sort over the decomposed (key, tuple) pairs using
	// the shared orderCompositeKeysByAxisInclude helper, so the two outputs
	// match byte-for-byte. With no Include on any position the helper funnels
	// through sort.Strings, preserving byte-identity with the prior flat sort.
	// We order the row/col axis key permutations so we can walk the slice in
	// the canonical order without materializing every cell into a string-keyed
	// map.
	rowKeys := orderCompositeKeysByAxisInclude(s.rowKeys, s.rowAxis, s.spec.Rows)
	colKeys := orderCompositeKeysByAxisInclude(s.colKeys, s.colAxis, s.spec.Columns)

	// Build per-key tuple lookup for the downstream MatrixPayload /
	// long-shape emitters.
	rowTuples := make(map[string]types.AxisKey, len(rowKeys))
	for i, k := range s.rowKeys {
		rowTuples[k] = s.rowAxis[i]
	}
	colTuples := make(map[string]types.AxisKey, len(colKeys))
	for i, k := range s.colKeys {
		colTuples[k] = s.colAxis[i]
	}

	// Finalize every cell and margin to scalar/rich values. The cells
	// matrix is sparse — unobserved (rowIdx, colIdx) pairs hold nil
	// accumulators and stay out of the emitted map (which the renderer
	// treats as Present=false).
	//
	// finalizeCells also emits the per-cell components map
	// alongside the (rich/scalar) value and present flag. The components
	// map is built at Finalize time only (no allocation in the per-record
	// hot loop) — for each cell with at least one record, the orchestrator
	// merges the universal floor {n, n_null} with the cell aggregator's
	// MetaAggregator.Components() output via buildCellComponentMap. Cells
	// with no records (cells[r][c] == nil) emit no entry; the matrix
	// builder writes nil at [r][c] for those slots.
	cellValues, cellPresent, cellComponentsMap, err := s.finalizeCells()
	if err != nil {
		return nil, err
	}
	rowMargins, rowMarginPresent, rowMarginCountsMap, rowMarginComponentsMap, err := s.finalizeRowMargins()
	if err != nil {
		return nil, err
	}
	colMargins, colMarginPresent, colMarginCountsMap, colMarginComponentsMap, err := s.finalizeColMargins()
	if err != nil {
		return nil, err
	}
	var grandValue any
	var grandPresent bool
	var grandComponentsMap map[string]any
	if s.grandMargin != nil {
		scalar, err := s.grandMargin.Finalize()
		if err != nil {
			return nil, err
		}
		v, err := dispatchAggregatorCellResult(s.grandMargin, scalar)
		if err != nil {
			return nil, err
		}
		grandValue = v
		grandPresent = true
		// Grand-total components — universal floor (n = non-null
		// inputs to the cell field, n_null = null inputs) merged with the
		// cell aggregator's MetaAggregator.Components() output. Mirrors
		// the buffered grand-margin emission so the buffered/fused
		// parity gate holds across the grand-total slot too.
		n := s.grandMarginCount - s.grandMarginNNull
		compMap, cerr := buildCellComponentMap(s.grandMargin, n, s.grandMarginNNull)
		if cerr != nil {
			return nil, cerr
		}
		grandComponentsMap = compMap
	}

	// Partial-depth (normalize_level) denominators. When the leaf was
	// selected the partial map aliases the leaf margin map (coerce to
	// float64 since the gate above already rejected map-valued cells
	// when normalize != none).
	mode := s.spec.NormalizeOrDefault()
	var partialRowDenom map[string]float64
	var partialRowDenomPresent map[string]bool
	var partialColDenom map[string]float64
	var partialColDenomPresent map[string]bool
	var leafRowToPartial map[string]string
	var leafColToPartial map[string]string

	if mode == types.CrosstabNormalizeRow {
		if s.partialRowIndex != nil {
			partialRowDenom, partialRowDenomPresent, err = s.finalizePartialRowMargins()
			if err != nil {
				return nil, err
			}
			leafRowToPartial = buildLeafToPartial(rowKeys, s.rowNormLevel)
		} else {
			partialRowDenom = coerceAnyMarginMap(rowMargins)
			partialRowDenomPresent = rowMarginPresent
		}
	}
	if mode == types.CrosstabNormalizeColumn {
		if s.partialColIndex != nil {
			partialColDenom, partialColDenomPresent, err = s.finalizePartialColMargins()
			if err != nil {
				return nil, err
			}
			leafColToPartial = buildLeafToPartial(colKeys, s.colNormLevel)
		} else {
			partialColDenom = coerceAnyMarginMap(colMargins)
			partialColDenomPresent = colMarginPresent
		}
	}

	// Cross-axis (normalize_within) denominators.
	var crossDenom map[crosstabCellKey]float64
	var crossLeafRowToPartial map[string]string
	var crossLeafColToPartial map[string]string
	if s.crossActive {
		crossDenom = make(map[crosstabCellKey]float64, len(s.crossMargins))
		for k, agg := range s.crossMargins {
			scalar, err := agg.Finalize()
			if err != nil {
				return nil, err
			}
			v, err := dispatchAggregatorCellResult(agg, scalar)
			if err != nil {
				return nil, err
			}
			crossDenom[k] = coerceFloat64(v)
		}
		crossLeafRowToPartial = buildLeafToPartial(rowKeys, s.crossRowDepth)
		crossLeafColToPartial = buildLeafToPartial(colKeys, s.crossColDepth)
	}

	// Apply normalization. Same divide-by-zero policy: a missing
	// denominator or a zero denominator drops the cell from the present
	// set (downstream renderer surfaces Present=false).
	if mode != types.CrosstabNormalizeNone {
		grandScalar := coerceFloat64(grandValue)
		normalized := make(map[crosstabCellKey]any, len(cellValues))
		for ck, v := range cellValues {
			var denom float64
			var ok bool
			if s.crossActive {
				key := crosstabCellKey{
					row: crossLeafRowToPartial[ck.row],
					col: crossLeafColToPartial[ck.col],
				}
				if d, present := crossDenom[key]; present {
					denom, ok = d, true
				}
			} else {
				rLookup := ck.row
				if leafRowToPartial != nil {
					rLookup = leafRowToPartial[ck.row]
				}
				cLookup := ck.col
				if leafColToPartial != nil {
					cLookup = leafColToPartial[ck.col]
				}
				denom, ok = normalizeDenominator(mode, rLookup, cLookup,
					partialRowDenom, partialRowDenomPresent,
					partialColDenom, partialColDenomPresent,
					grandScalar, grandPresent)
			}
			if !ok || denom == 0 {
				cellPresent[ck] = false
				continue
			}
			normalized[ck] = coerceFloat64(v) / denom
		}
		cellValues = normalized
	}

	// Build response. Metadata mirrors the buffered Response shape.
	resp := &types.Response{
		Metadata: &types.ResponseMetadata{
			TotalRows:    s.totalRows,
			FilteredRows: s.filteredRows,
		},
	}

	// Re-materialise sorted RowKeys / ColumnKeys as []AxisKey tuples,
	// preserving the per-grouper key list captured at first sight.
	rowPart := &CrosstabAxisPartition{Keys: rowKeys, Tuples: tuplesForKeys(rowKeys, rowTuples)}
	colPart := &CrosstabAxisPartition{Keys: colKeys, Tuples: tuplesForKeys(colKeys, colTuples)}

	// Convert the insertion-order [rowIdx][colIdx] cellCounts
	// matrix to a {(rowKey, colKey) → count} map keyed by composite axis
	// key string. populateCrosstabComponents then re-projects it into a
	// matrix indexed by the sorted rowKeys / colKeys above so the layout
	// matches buildMatrixPayload's Cells coordinates byte-for-byte across
	// buffered and fused paths.
	cellCountsMap := make(map[crosstabCellKey]int, len(s.rowKeys)*len(s.colKeys))
	for rIdx, row := range s.cellCounts {
		rKey := s.rowKeys[rIdx]
		for cIdx, n := range row {
			if n == 0 {
				continue
			}
			cellCountsMap[crosstabCellKey{rKey, s.colKeys[cIdx]}] = n
		}
	}

	shape := s.spec.ShapeOrDefault()
	if shape == types.CrosstabShapeMatrix {
		resp.Crosstab = &types.CrosstabResult{
			Shape: shape,
			Matrix: buildMatrixPayload(s.spec, rowPart, colPart,
				cellValues, cellPresent,
				rowMargins, rowMarginPresent,
				colMargins, colMarginPresent,
				grandValue, grandPresent,
				s.cellLabel, mode),
		}
	} else {
		resp.Crosstab = &types.CrosstabResult{Shape: shape}
		resp.Data = buildLongRowsFused(s.spec, rowPart, colPart,
			cellValues, cellPresent,
			rowMargins, rowMarginPresent,
			colMargins, colMarginPresent,
			grandValue, grandPresent,
			s.cellLabel,
			partialRowDenom, partialRowDenomPresent, s.rowNormLevel,
			partialColDenom, partialColDenomPresent, s.colNormLevel)
	}

	// Emit per-cell record-count matrix on Response.Components.
	// Layout indexed identically to MatrixPayload.Cells via the sorted
	// rowKeys / colKeys above. excludedRecords = filteredRows -
	// includedRecords; includedRecords is the running sum maintained in
	// Update so no second matrix pass is needed.
	excludedRecords := int(s.filteredRows) - s.includedRecords
	if excludedRecords < 0 {
		excludedRecords = 0
	}
	// Margin counts + components flow only when the display flag
	// is set — the normalization-only path computed the accumulators but
	// the consumer-facing emission is gated by the display flag (matches
	// MatrixPayload.RowMargins / ColumnMargins / GrandTotal).
	var rowMarginCountsSlot map[string]int
	var rowMarginComponentsSlot map[string]map[string]any
	if s.spec.Margins.Rows {
		rowMarginCountsSlot = rowMarginCountsMap
		rowMarginComponentsSlot = rowMarginComponentsMap
	}
	var colMarginCountsSlot map[string]int
	var colMarginComponentsSlot map[string]map[string]any
	if s.spec.Margins.Columns {
		colMarginCountsSlot = colMarginCountsMap
		colMarginComponentsSlot = colMarginComponentsMap
	}
	var grandMarginCountSlot int
	var grandMarginComponentsSlot map[string]any
	if s.spec.Margins.Grand {
		grandMarginCountSlot = s.grandMarginCount
		grandMarginComponentsSlot = grandComponentsMap
	}
	// Components emission block — gated by disableComponents (mirrors
	// the processor flag set by RunCrosstabFused). Skipping here drops
	// the MetaGrouper.Components walk + projection work entirely.
	if !s.disableComponents {
		// Per-axis grouper components emission. Each axis grouper
		// already accumulated liveBuckets via its keying side-effect
		// path during Update (KeyForRow for single-key entries,
		// KeysForRow for fan-out entries); we call Components() now to capture the
		// per-axis-position bucket emission and project it onto the sorted
		// rowKeys / colKeys. The fused path constructs row / column tuple
		// vectors aligned with the sorted axis-key order via tuplesForKeys
		// above; projectAxisKeyComponents then walks each tuple position to
		// index the matching bucket from the per-position Components map.
		rowAxisComponents, err := s.axisComponents(s.rowGroupers)
		if err != nil {
			return nil, err
		}
		colAxisComponents, err := s.axisComponents(s.colGroupers)
		if err != nil {
			return nil, err
		}
		rowKeyComponents := projectAxisKeyComponents(s.spec.Rows, rowPart.Tuples, rowAxisComponents)
		colKeyComponents := projectAxisKeyComponents(s.spec.Columns, colPart.Tuples, colAxisComponents)
		populateCrosstabComponents(resp, rowKeys, colKeys,
			cellCountsMap, cellComponentsMap,
			rowMarginCountsSlot, rowMarginComponentsSlot,
			colMarginCountsSlot, colMarginComponentsSlot,
			grandMarginCountSlot, grandMarginComponentsSlot, s.spec.Margins.Grand,
			s.includedRecords, excludedRecords,
			rowKeyComponents, colKeyComponents)
	}

	return resp, nil
}

// axisComponents captures the per-axis-position MetaGrouper.Components()
// emission for an ordered axis-grouper chain. Each grouper is queried
// once after Update has drained the cohort, so liveBuckets reflects
// every record routed through the axis (populated by the per-record
// keying call during Update). Returns nil when the chain is empty;
// entries are nil when the grouper does not implement MetaGrouper
// (extension groupers without bucket-count emission). The MetaGrouper
// assertion targets fusedAxisGrouper.grouper, so it resolves the same
// way on single-key and fan-out entries. Used by Finalize to feed
// projectAxisKeyComponents in lockstep with the buffered path.
func (s *FusedCrosstabState) axisComponents(chain []fusedAxisGrouper) ([]map[string]any, error) {
	if len(chain) == 0 {
		return nil, nil
	}
	out := make([]map[string]any, len(chain))
	for i, g := range chain {
		// Assert against the retained Grouper instance, not the keying
		// shape, so MetaGrouper resolves identically on single-key and
		// fan-out entries.
		meta, ok := g.grouper.(MetaGrouper)
		if !ok {
			continue
		}
		op, err := meta.Components()
		if err != nil {
			return nil, err
		}
		out[i] = op
	}
	return out, nil
}

// finalizeCells walks the 2D cell slice in (rowIdx, colIdx) insertion
// order and builds the string-keyed result map the downstream renderers
// consume. Nil entries (cells never touched by a record) are silently
// dropped from the present set — matches the buffered path's behaviour
// where a (rowKey, colKey) pair with no records lands no cell value.
//
// Also emits the per-cell components map (keyed by composite
// axis-key pair) — Finalize-time emission so the hot decode loop pays
// no allocation cost for the component bookkeeping. For each cell with
// at least one record routed, the orchestrator calls
// MetaAggregator.Components() on the post-Finalize aggregator instance
// and merges the result with the universal floor {n, n_null} via
// buildCellComponentMap. The floor's n is derived as cellCounts -
// cellNNull (records routed to cell minus null inputs); cellNNull is
// the per-cell null counter tracked in Update. populateCrosstabComponents
// then projects the insertion-order map back into the sorted (r, c)
// matrix layout, writing nil at coordinates where no record landed.
func (s *FusedCrosstabState) finalizeCells() (map[crosstabCellKey]any, map[crosstabCellKey]bool, map[crosstabCellKey]map[string]any, error) {
	// Pre-size to the dense cap, even though sparsely populated cells
	// will leave headroom — saves rehashes on the dense common case.
	nCells := len(s.rowKeys) * len(s.colKeys)
	values := make(map[crosstabCellKey]any, nCells)
	present := make(map[crosstabCellKey]bool, nCells)
	components := make(map[crosstabCellKey]map[string]any, nCells)
	for rIdx, row := range s.cells {
		rKey := s.rowKeys[rIdx]
		for cIdx, agg := range row {
			if agg == nil {
				continue
			}
			scalar, err := agg.Finalize()
			if err != nil {
				return nil, nil, nil, err
			}
			v, err := dispatchAggregatorCellResult(agg, scalar)
			if err != nil {
				return nil, nil, nil, err
			}
			ck := crosstabCellKey{row: rKey, col: s.colKeys[cIdx]}
			values[ck] = v
			present[ck] = true
			// Per-cell components emission. n = records routed
			// to (r, c) minus null inputs; n_null = null inputs. Matches
			// the buffered path's runCellAggregation walk byte-for-byte.
			nNull := s.cellNNull[rIdx][cIdx]
			n := s.cellCounts[rIdx][cIdx] - nNull
			compMap, cerr := buildCellComponentMap(agg, n, nNull)
			if cerr != nil {
				return nil, nil, nil, cerr
			}
			components[ck] = compMap
		}
	}
	return values, present, components, nil
}

// finalizeRowMargins drives Finalize on every row-margin accumulator
// and builds the string-keyed result map the downstream renderers
// consume. Nil entries (rows present in the interner but never reached
// a row-margin update) are dropped.
//
// Also emits the per-row-margin counts + components maps. The
// counts ride on the same {(rowKey) → count} shape populateCrosstab-
// Components projects into RowMarginCounts; the components map carries
// the universal floor merged with the MetaAggregator output via
// buildCellComponentMap. Both keys mirror the value-map (rowKey → ...)
// so a single per-key lookup at Finalize-time addresses all three.
func (s *FusedCrosstabState) finalizeRowMargins() (map[string]any, map[string]bool, map[string]int, map[string]map[string]any, error) {
	if s.rowMargins == nil {
		return nil, nil, nil, nil, nil
	}
	values := make(map[string]any, len(s.rowMargins))
	present := make(map[string]bool, len(s.rowMargins))
	counts := make(map[string]int, len(s.rowMargins))
	components := make(map[string]map[string]any, len(s.rowMargins))
	for i, agg := range s.rowMargins {
		key := s.rowKeys[i]
		// Always emit the count + components for the row key when any
		// row-axis update landed (rowMarginCount[i] > 0), even when the
		// aggregator instance is still nil. The cell builder only
		// allocates the OnlineAggregator on first UpdateRow, so a row
		// present in the interner but with zero updates stays nil and
		// is dropped from both the value map and the count/component
		// vectors.
		if agg == nil {
			continue
		}
		scalar, err := agg.Finalize()
		if err != nil {
			return nil, nil, nil, nil, err
		}
		v, err := dispatchAggregatorCellResult(agg, scalar)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		values[key] = v
		present[key] = true
		counts[key] = s.rowMarginCount[i]
		n := s.rowMarginCount[i] - s.rowMarginNNull[i]
		compMap, cerr := buildCellComponentMap(agg, n, s.rowMarginNNull[i])
		if cerr != nil {
			return nil, nil, nil, nil, cerr
		}
		components[key] = compMap
	}
	return values, present, counts, components, nil
}

// finalizeColMargins is the column-axis sibling of finalizeRowMargins.
func (s *FusedCrosstabState) finalizeColMargins() (map[string]any, map[string]bool, map[string]int, map[string]map[string]any, error) {
	if s.colMargins == nil {
		return nil, nil, nil, nil, nil
	}
	values := make(map[string]any, len(s.colMargins))
	present := make(map[string]bool, len(s.colMargins))
	counts := make(map[string]int, len(s.colMargins))
	components := make(map[string]map[string]any, len(s.colMargins))
	for i, agg := range s.colMargins {
		key := s.colKeys[i]
		if agg == nil {
			continue
		}
		scalar, err := agg.Finalize()
		if err != nil {
			return nil, nil, nil, nil, err
		}
		v, err := dispatchAggregatorCellResult(agg, scalar)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		values[key] = v
		present[key] = true
		counts[key] = s.colMarginCount[i]
		n := s.colMarginCount[i] - s.colMarginNNull[i]
		compMap, cerr := buildCellComponentMap(agg, n, s.colMarginNNull[i])
		if cerr != nil {
			return nil, nil, nil, nil, cerr
		}
		components[key] = compMap
	}
	return values, present, counts, components, nil
}

// finalizePartialRowMargins is the scalar-only variant for partial-
// depth (normalize_level) denominators on the row axis. Map-valued
// aggregators are rejected upstream when normalize != none, so every
// margin here is guaranteed scalar; we still funnel through
// dispatchAggregatorCellResult + coerceFloat64 so a future numeric-cell
// rich payload (e.g. dictionary-of-floats) continues to coerce
// predictably.
func (s *FusedCrosstabState) finalizePartialRowMargins() (map[string]float64, map[string]bool, error) {
	values := make(map[string]float64, len(s.partialRowMargins))
	present := make(map[string]bool, len(s.partialRowMargins))
	for i, agg := range s.partialRowMargins {
		if agg == nil {
			continue
		}
		scalar, err := agg.Finalize()
		if err != nil {
			return nil, nil, err
		}
		v, err := dispatchAggregatorCellResult(agg, scalar)
		if err != nil {
			return nil, nil, err
		}
		key := s.partialRowKeys[i]
		values[key] = coerceFloat64(v)
		present[key] = true
	}
	return values, present, nil
}

// finalizePartialColMargins is the column-axis sibling of
// finalizePartialRowMargins.
func (s *FusedCrosstabState) finalizePartialColMargins() (map[string]float64, map[string]bool, error) {
	values := make(map[string]float64, len(s.partialColMargins))
	present := make(map[string]bool, len(s.partialColMargins))
	for i, agg := range s.partialColMargins {
		if agg == nil {
			continue
		}
		scalar, err := agg.Finalize()
		if err != nil {
			return nil, nil, err
		}
		v, err := dispatchAggregatorCellResult(agg, scalar)
		if err != nil {
			return nil, nil, err
		}
		key := s.partialColKeys[i]
		values[key] = coerceFloat64(v)
		present[key] = true
	}
	return values, present, nil
}

// tuplesForKeys returns the per-key axis tuple list in the same order
// as keys. Used to populate CrosstabAxisPartition.Tuples for the
// matrix / long-shape builders without re-parsing composite keys.
func tuplesForKeys(keys []string, tuples map[string]types.AxisKey) []types.AxisKey {
	out := make([]types.AxisKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, tuples[k])
	}
	return out
}

// buildLongRowsFused mirrors processing/crosstab.go::buildLongRows but
// adapts the (rowPart, colPart) inputs to the lightweight pair the
// fused state carries: only Keys + Tuples are populated, Records is
// nil (the fused path never materialises record buckets). The shape of
// the emitted row sequence — cell rows in sorted (row, col) order, then
// row margins, then column margins, then grand, then partial-depth
// margins — is identical to the buffered emitter.
func buildLongRowsFused(spec *types.CrosstabSpec,
	rowPart, colPart *CrosstabAxisPartition,
	cellValues map[crosstabCellKey]any,
	cellPresent map[crosstabCellKey]bool,
	rowMargins map[string]any, rowPresent map[string]bool,
	colMargins map[string]any, colPresent map[string]bool,
	grand any, grandPresent bool,
	cellLabel string,
	partialRowMargins map[string]float64, partialRowPresent map[string]bool,
	rowNormLevel int,
	partialColMargins map[string]float64, partialColPresent map[string]bool,
	colNormLevel int,
) []map[string]any {
	// The partial-depth long-shape emission needs an ordered iteration
	// over the partial keys. The buffered emitter walks
	// partialRowPart.Keys / partialColPart.Keys (sorted by composite key
	// via PartitionByAxis). The fused emitter rebuilds the equivalent
	// sorted list from the partial-margin map's keys, attaching the
	// per-grouper key tuple at each level by splitting the composite key
	// on crosstabAxisKeySep — partition shape isn't preserved on the
	// fused path because we never partition by raw records.
	partialRowPart := buildPartialAxisFromKeys(partialRowMargins, rowNormLevel, spec.Rows)
	partialColPart := buildPartialAxisFromKeys(partialColMargins, colNormLevel, spec.Columns)
	return buildLongRows(spec,
		rowPart, colPart,
		cellValues, cellPresent,
		rowMargins, rowPresent,
		colMargins, colPresent,
		grand, grandPresent,
		cellLabel,
		partialRowPart, partialRowMargins, partialRowPresent, rowNormLevel,
		partialColPart, partialColMargins, partialColPresent, colNormLevel)
}

// buildPartialAxisFromKeys constructs a CrosstabAxisPartition with only
// Keys + Tuples populated, derived from a partial-margin map's keys.
// Used by buildLongRowsFused to feed the existing buffered emitter
// without exposing the fused state's internal representation.
//
// axis is the full row/column axis spec; partial keys span the leading
// level+1 positions, so ordering is applied against axis[:level+1] to
// honor each leading position's Include list. The buffered path derives
// the same partial axis via PartitionByAxis(spec.Rows[:level+1], …),
// whose per-level orderKeysByInclude produces this exact order — so the
// fused partial margins stay byte-equal to buffered.
func buildPartialAxisFromKeys(margins map[string]float64, level int, axis []*types.Group) *CrosstabAxisPartition {
	if len(margins) == 0 || level < 0 {
		return nil
	}
	keys := make([]string, 0, len(margins))
	for k := range margins {
		keys = append(keys, k)
	}
	tuples := make([]types.AxisKey, 0, len(keys))
	for _, k := range keys {
		parts := strings.Split(k, crosstabAxisKeySep)
		tuple := make(types.AxisKey, 0, len(parts))
		for _, p := range parts {
			tuple = append(tuple, p)
		}
		tuples = append(tuples, tuple)
	}
	// Restrict the ordering axis to the leading level+1 positions the
	// partial keys actually span; guard against an over-long level.
	leading := axis
	if level+1 <= len(axis) {
		leading = axis[:level+1]
	}
	keys = orderCompositeKeysByAxisInclude(keys, tuples, leading)
	// Rebuild tuples parallel to the newly ordered keys.
	tuples = tuples[:0]
	for _, k := range keys {
		parts := strings.Split(k, crosstabAxisKeySep)
		tuple := make(types.AxisKey, 0, len(parts))
		for _, p := range parts {
			tuple = append(tuple, p)
		}
		tuples = append(tuples, tuple)
	}
	return &CrosstabAxisPartition{Keys: keys, Tuples: tuples}
}
