package processing

import (
	stderrors "errors"
	"fmt"
	"sort"
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
// stream) the E4-S1 gate accepts — same MatrixPayload / long-shape Data
// row order, same normalization and divide-by-zero policy, same margin
// recomputation rule. The orchestrator (E4-S4, on the parent epic) is
// responsible for dispatching to the fused path only when CanFuseCrosstab
// holds; the defensive guards in this file echo that gate so a
// mis-routed request fails fast with a typed CodedError instead of
// producing a diverging result.
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

	// Cell aggregator wiring. cellFactory is the constructor used to
	// lazily build a per-cell OnlineAggregator the first time a record
	// lands in that cell. cellField + cellLabel + cellAgg are the spec
	// fields shared by every cell / margin so Finalize can dispatch the
	// rich-aggregator path uniformly.
	cellAgg     *types.Aggregation
	cellLabel   string
	cellField   string
	cellFactory AggregatorFactory

	// Axis grouper instances. Each entry is a field-bound StreamableGrouper
	// constructed once at NewFusedCrosstabState time. KeyFor is called
	// per record per axis in Update — the only per-record allocation is
	// the composite-key string.
	rowGroupers []StreamableGrouper
	colGroupers []StreamableGrouper

	// Per-cell accumulator and the symmetric margin maps. Keys are the
	// composite axis keys produced by joining per-grouper KeyFor outputs
	// with crosstabAxisKeySep (byte-equal to PartitionByAxis composite
	// keys). Construction is lazy: a key first appears when a record
	// lands in its bucket.
	cells       map[crosstabCellKey]OnlineAggregator
	rowMargins  map[string]OnlineAggregator
	colMargins  map[string]OnlineAggregator
	grandMargin OnlineAggregator

	// Cross-axis margin map. Populated when spec.NormalizeWithin != nil
	// and spec.NormalizeOrDefault() is row or column. The key is the
	// (truncated rowPrefix, truncated colPrefix) pair per the depth rules
	// in processing/crosstab.go::crossActive (lines 410–448). Always
	// scalar-aggregated — the map cell gate above rejects map-valued
	// aggregators paired with normalization.
	crossMargins  map[crosstabCellKey]OnlineAggregator
	crossActive   bool
	crossRowDepth int
	crossColDepth int

	// Same-axis partial-depth (normalize_level) bookkeeping. When
	// rowNormLevel != leaf the row denominator switches from the leaf
	// row margin to a margin keyed by the depth-truncated row prefix; the
	// fused path materialises that partial-margin accumulator alongside
	// the leaf one. Mirror story for column.
	partialRowMargins map[string]OnlineAggregator
	partialColMargins map[string]OnlineAggregator
	rowNormLevel      int
	colNormLevel      int

	// Row-key tracking. We retain insertion-time the tuple form alongside
	// each composite key so Finalize can emit MatrixPayload.RowKeys /
	// ColumnKeys without re-parsing the composite-key strings — the
	// buffered path does this via PartitionByAxis.Tuples; the fused path
	// rebuilds the same shape by remembering the per-grouper keys at
	// first sight.
	rowTuples map[string]types.AxisKey
	colTuples map[string]types.AxisKey

	// Row counters.
	totalRows    int64
	filteredRows int64
}

// NewFusedCrosstabState constructs a FusedCrosstabState. Validates the
// spec against the gate's structural assumptions (every axis grouper
// must implement StreamableGrouper, cell aggregator must be online) and
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
		cells:        make(map[crosstabCellKey]OnlineAggregator),
		rowTuples:    make(map[string]types.AxisKey),
		colTuples:    make(map[string]types.AxisKey),
		rowNormLevel: -1,
		colNormLevel: -1,
	}

	if spec.NeedsRowMargin() {
		st.rowMargins = make(map[string]OnlineAggregator)
	}
	if spec.NeedsColumnMargin() {
		st.colMargins = make(map[string]OnlineAggregator)
	}
	if spec.NeedsGrandMargin() {
		st.grandMargin, err = newOnlineCell(cellFactory, spec.Cell, schema)
		if err != nil {
			return nil, err
		}
	}

	// Partial-depth (normalize_level) bookkeeping. When the configured
	// level is the leaf the partial map aliases the leaf margin map
	// (no extra accumulators needed); only the truncate-to-prefix case
	// allocates a parallel map.
	mode := spec.NormalizeOrDefault()
	if mode == types.CrosstabNormalizeRow {
		st.rowNormLevel = spec.NormalizeLevelOrLeaf(len(spec.Rows))
		if st.rowNormLevel < len(spec.Rows)-1 {
			st.partialRowMargins = make(map[string]OnlineAggregator)
		}
	}
	if mode == types.CrosstabNormalizeColumn {
		st.colNormLevel = spec.NormalizeLevelOrLeaf(len(spec.Columns))
		if st.colNormLevel < len(spec.Columns)-1 {
			st.partialColMargins = make(map[string]OnlineAggregator)
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

// buildStreamableAxis constructs the per-axis grouper chain. Every
// instance is type-asserted to StreamableGrouper; a non-streamable
// grouper (e.g. GROUP_QUANTILE) surfaces a typed CodedError so the
// caller can dispatch back to the buffered RunCrosstab path. axisName
// is the human-readable axis label ("rows" / "columns") used in the
// error details.
func buildStreamableAxis(axis []*types.Group, schema *encoding.Schema, ext *ExtensionRegistry, axisName string) ([]StreamableGrouper, error) {
	if len(axis) == 0 {
		return nil, nil
	}
	out := make([]StreamableGrouper, 0, len(axis))
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
		streamable, ok := instance.(StreamableGrouper)
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("fused crosstab %s axis grouper %s does not implement StreamableGrouper", axisName, grp.Type),
				map[string]any{"axis": axisName, "position": i, "group_type": string(grp.Type)})
		}
		out = append(out, streamable)
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
// (CanFuseCrosstab, landing in E4-S1) is the load-bearing check; this
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

// Update folds a single filter-passing record into the cell, margin,
// and cross-margin accumulators it touches. The caller is responsible
// for running filters / row-local attributes / features / etc. before
// passing the record in — by the time Update sees a record it is
// already a filter-passing observation in the same sense the buffered
// RunCrosstab path treats its filtered slice. Records skipped by the
// caller's filter contribute to totalRows only via the totalRows
// counter the caller separately advances.
//
// Records whose row-axis or column-axis key is null on any grouper
// (ErrGrouperKeyNull from KeyFor) are silently skipped — they have no
// placement in the matrix, matching the buffered behavior where the
// recursive PartitionByAxis drops them from every bucket.
func (s *FusedCrosstabState) Update(rec *Record) error {
	if rec == nil {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"FusedCrosstabState.Update called with nil record")
	}
	s.filteredRows++

	rowKey, rowTuple, ok, err := axisKeyAndTuple(s.rowGroupers, rec)
	if err != nil {
		return err
	}
	if !ok {
		// Null on a row-axis grouper → unplaceable. Mirror buffered.
		return nil
	}
	colKey, colTuple, ok, err := axisKeyAndTuple(s.colGroupers, rec)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	if _, present := s.rowTuples[rowKey]; !present {
		s.rowTuples[rowKey] = rowTuple
	}
	if _, present := s.colTuples[colKey]; !present {
		s.colTuples[colKey] = colTuple
	}

	cellKey := crosstabCellKey{row: rowKey, col: colKey}
	cell, present := s.cells[cellKey]
	if !present {
		cell, err = newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
		if err != nil {
			return err
		}
		s.cells[cellKey] = cell
	}
	if err := cell.UpdateRow(rec, s.cellField); err != nil {
		return err
	}

	if s.rowMargins != nil {
		mar, present := s.rowMargins[rowKey]
		if !present {
			mar, err = newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
			if err != nil {
				return err
			}
			s.rowMargins[rowKey] = mar
		}
		if err := mar.UpdateRow(rec, s.cellField); err != nil {
			return err
		}
	}
	if s.colMargins != nil {
		mar, present := s.colMargins[colKey]
		if !present {
			mar, err = newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
			if err != nil {
				return err
			}
			s.colMargins[colKey] = mar
		}
		if err := mar.UpdateRow(rec, s.cellField); err != nil {
			return err
		}
	}
	if s.grandMargin != nil {
		if err := s.grandMargin.UpdateRow(rec, s.cellField); err != nil {
			return err
		}
	}

	if s.partialRowMargins != nil {
		pkey := truncateCompositeKey(rowKey, s.rowNormLevel)
		mar, present := s.partialRowMargins[pkey]
		if !present {
			mar, err = newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
			if err != nil {
				return err
			}
			s.partialRowMargins[pkey] = mar
		}
		if err := mar.UpdateRow(rec, s.cellField); err != nil {
			return err
		}
	}
	if s.partialColMargins != nil {
		pkey := truncateCompositeKey(colKey, s.colNormLevel)
		mar, present := s.partialColMargins[pkey]
		if !present {
			mar, err = newOnlineCell(s.cellFactory, s.cellAgg, s.schema)
			if err != nil {
				return err
			}
			s.partialColMargins[pkey] = mar
		}
		if err := mar.UpdateRow(rec, s.cellField); err != nil {
			return err
		}
	}

	if s.crossActive {
		rPart := truncateCompositeKey(rowKey, s.crossRowDepth)
		cPart := truncateCompositeKey(colKey, s.crossColDepth)
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

// axisKeyAndTuple computes the composite key + axis tuple for one
// record across an ordered grouper chain. Returns (key, tuple, true,
// nil) on success; (..., false, nil) when any grouper in the chain
// signals ErrGrouperKeyNull (the record is unplaceable on this axis
// and the buffered path drops it). A non-null non-nil err is a
// genuine grouper failure (e.g. a custom StreamableGrouper extension's
// KeyFor panicking) and is bubbled through unchanged.
func axisKeyAndTuple(groupers []StreamableGrouper, rec *Record) (string, types.AxisKey, bool, error) {
	if len(groupers) == 0 {
		// Empty axis is a programming bug — validateCrosstabSpec rejects
		// zero-grouper axes up-front so reaching here means the gate
		// drifted. Surface a typed error rather than a silent placement
		// into the "" bucket.
		return "", nil, false, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"fused crosstab axis has zero groupers; spec validation should have rejected")
	}
	parts := make([]string, 0, len(groupers))
	tuple := make(types.AxisKey, 0, len(groupers))
	for _, g := range groupers {
		key, err := g.KeyFor(rec)
		if err != nil {
			if stderrors.Is(err, ErrGrouperKeyNull) {
				return "", nil, false, nil
			}
			return "", nil, false, err
		}
		parts = append(parts, key)
		tuple = append(tuple, key)
	}
	return strings.Join(parts, crosstabAxisKeySep), tuple, true, nil
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

	// Deterministic key emission order: sort by composite axis key. The
	// buffered RunCrosstab does the same via PartitionByAxis (sort.Strings
	// on the bucket keys), so the fused output matches byte-for-byte.
	rowKeys := sortedKeys(s.rowTuples)
	colKeys := sortedKeys(s.colTuples)

	// Finalize every cell and margin to scalar/rich values. We materialise
	// the cell values once into cellValues; the normalize pass below
	// rewrites them when mode != none.
	cellValues, cellPresent, err := finalizeCells(s.cells)
	if err != nil {
		return nil, err
	}
	rowMargins, rowMarginPresent, err := finalizeMarginMap(s.rowMargins)
	if err != nil {
		return nil, err
	}
	colMargins, colMarginPresent, err := finalizeMarginMap(s.colMargins)
	if err != nil {
		return nil, err
	}
	var grandValue any
	var grandPresent bool
	if s.grandMargin != nil {
		scalar, err := s.grandMargin.Finalize()
		if err != nil {
			return nil, err
		}
		v, err := dispatchAggregatorResult(s.grandMargin, scalar)
		if err != nil {
			return nil, err
		}
		grandValue = v
		grandPresent = true
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
		if s.partialRowMargins != nil {
			partialRowDenom, partialRowDenomPresent, err = finalizeFloatMargins(s.partialRowMargins)
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
		if s.partialColMargins != nil {
			partialColDenom, partialColDenomPresent, err = finalizeFloatMargins(s.partialColMargins)
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
			v, err := dispatchAggregatorResult(agg, scalar)
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
	rowPart := &CrosstabAxisPartition{Keys: rowKeys, Tuples: tuplesForKeys(rowKeys, s.rowTuples)}
	colPart := &CrosstabAxisPartition{Keys: colKeys, Tuples: tuplesForKeys(colKeys, s.colTuples)}

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

	return resp, nil
}

// finalizeCells drives Finalize on every cell aggregator and dispatches
// through dispatchAggregatorResult so map-valued (AGG_SET_FREQUENCY) and
// slice-valued (AGG_SET_UNION) cells surface their rich payload. The
// returned maps own no references to the aggregator instances; safe to
// drop the state after this call.
func finalizeCells(in map[crosstabCellKey]OnlineAggregator) (map[crosstabCellKey]any, map[crosstabCellKey]bool, error) {
	values := make(map[crosstabCellKey]any, len(in))
	present := make(map[crosstabCellKey]bool, len(in))
	for k, agg := range in {
		scalar, err := agg.Finalize()
		if err != nil {
			return nil, nil, err
		}
		v, err := dispatchAggregatorResult(agg, scalar)
		if err != nil {
			return nil, nil, err
		}
		values[k] = v
		present[k] = true
	}
	return values, present, nil
}

// finalizeMarginMap is the sibling of finalizeCells for the per-row /
// per-column / per-partial margin maps. Same dispatch rules.
func finalizeMarginMap(in map[string]OnlineAggregator) (map[string]any, map[string]bool, error) {
	if in == nil {
		return nil, nil, nil
	}
	values := make(map[string]any, len(in))
	present := make(map[string]bool, len(in))
	for k, agg := range in {
		scalar, err := agg.Finalize()
		if err != nil {
			return nil, nil, err
		}
		v, err := dispatchAggregatorResult(agg, scalar)
		if err != nil {
			return nil, nil, err
		}
		values[k] = v
		present[k] = true
	}
	return values, present, nil
}

// finalizeFloatMargins is the scalar-only variant of finalizeMarginMap
// used for partial-depth (normalize_level) denominators. Map-valued
// aggregators are rejected upstream when normalize != none, so every
// margin here is guaranteed scalar; we still funnel through
// dispatchAggregatorResult + coerceFloat64 so a future numeric-cell
// rich payload (e.g. dictionary-of-floats) continues to coerce
// predictably.
func finalizeFloatMargins(in map[string]OnlineAggregator) (map[string]float64, map[string]bool, error) {
	values := make(map[string]float64, len(in))
	present := make(map[string]bool, len(in))
	for k, agg := range in {
		scalar, err := agg.Finalize()
		if err != nil {
			return nil, nil, err
		}
		v, err := dispatchAggregatorResult(agg, scalar)
		if err != nil {
			return nil, nil, err
		}
		values[k] = coerceFloat64(v)
		present[k] = true
	}
	return values, present, nil
}

// sortedKeys returns the deterministic sorted key list for a string-keyed
// map. Mirrors the per-axis key sort in PartitionByAxis so the fused
// output's row / column order matches the buffered output byte-for-byte.
func sortedKeys(m map[string]types.AxisKey) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
	partialRowPart := buildPartialAxisFromKeys(partialRowMargins, rowNormLevel)
	partialColPart := buildPartialAxisFromKeys(partialColMargins, colNormLevel)
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
func buildPartialAxisFromKeys(margins map[string]float64, level int) *CrosstabAxisPartition {
	if len(margins) == 0 || level < 0 {
		return nil
	}
	keys := make([]string, 0, len(margins))
	for k := range margins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tuples := make([]types.AxisKey, 0, len(keys))
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
