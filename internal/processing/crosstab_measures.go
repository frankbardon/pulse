package processing

import (
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// crosstab_measures.go holds the buffered crosstab's per-measure run and
// the multi-measure fold (types.CrosstabSpec.Cells, U47 #326).
//
// A multi-measure crosstab runs each measure EXACTLY as the
// single-measure crosstab with Cell = Cells[i] would — its own cells,
// margins, normalize denominators, auxiliary margins, components and
// base floors — over the SAME axis partitions, then folds the measures
// into one payload whose row axis gains an innermost MEASURE position.
// Nothing is pooled across measures, so every per-measure figure is the
// single-measure figure; the fused arm (crosstab_fused_run.go) drives one
// FusedCrosstabState per measure in lockstep over one scan and folds its
// finalised responses through the same foldCrosstabMeasures, which is
// what makes fused == buffered for a multi-measure request structural.

// crosstabMeasureInput is the measure-independent state every measure's
// run shares: the filtered records, both axis partitions, the per-row
// column partitions, the own-margin admission sets and the axis-key
// components.
type crosstabMeasureInput struct {
	req          *types.Request
	filtered     []*Record
	filteredRows int64

	rowPart, colPart *CrosstabAxisPartition
	innerCols        []*CrosstabAxisPartition

	rowMember, colMember map[*Record]struct{}

	rowKeyComponents, colKeyComponents []map[string]any
}

// crosstabMeasureRun is one measure's finished output: a response
// carrying its Crosstab (matrix or long Data) and Components.Crosstab,
// its post-test cell rows, its normalise-denominator base floors (nil
// unless an overlay fold asked) and its included-record count.
type crosstabMeasureRun struct {
	resp            *types.Response
	postRows        []map[string]any
	bases           *crosstabBaseFloors
	includedRecords int
}

// crosstabMeasureSpecs returns the single-measure specs a crosstab runs:
// the spec itself on the single-measure form, else one MeasureSpec per
// Cells entry, in declaration order.
func crosstabMeasureSpecs(spec *types.CrosstabSpec) []*types.CrosstabSpec {
	if !spec.HasMeasures() {
		return []*types.CrosstabSpec{spec}
	}
	out := make([]*types.CrosstabSpec, len(spec.Cells))
	for i := range spec.Cells {
		out[i] = spec.MeasureSpec(i)
	}
	return out
}

// runCrosstabMeasure computes one measure of the buffered crosstab — the
// cells, the three own margins (U47 admission rule), the auxiliary
// margins, the partial-depth and cross-axis normalize denominators and
// the normalization — and emits it as a single-measure response. spec is
// a single-measure spec (Cell set).
func (p *Processor) runCrosstabMeasure(spec *types.CrosstabSpec, in crosstabMeasureInput) (*crosstabMeasureRun, error) {
	filtered := in.filtered
	rowPart, colPart := in.rowPart, in.colPart
	resp := &types.Response{}
	var err error

	cellAgg := spec.Cell
	cellLabel := spec.CellLabel()

	// cellValues admits any aggregator payload — scalar float64 for the
	// usual aggregators, plus rich shapes (map[string]int, []string) when
	// the cell aggregator implements RichAggregator. The normalize gate
	// below rejects spec.Normalize != none paired with a MapValued()
	// aggregator (see types.AggregationType.MapValued), so partial /
	// margin paths that need a float64 denominator stay scalar.
	cellValues := make(map[crosstabCellKey]any, len(rowPart.Keys)*len(colPart.Keys))
	cellPresent := make(map[crosstabCellKey]bool, len(rowPart.Keys)*len(colPart.Keys))

	// Per-cell record count tracked alongside the aggregator update
	// so Response.Components.Crosstab.CellCounts can be populated without
	// a second pass. A cell entry is created whenever a non-empty
	// (row, col) bucket exists — independent of whether the cell
	// aggregator finalises a value (an aggregator returning a nil row is
	// still routed N records). includedRecords is the count of records
	// that landed in at least one cell (both axis keys non-null);
	// ExcludedRecords on Components.Crosstab is then
	// filteredRows - includedRecords.
	cellCounts := make(map[crosstabCellKey]int, len(rowPart.Keys)*len(colPart.Keys))
	var includedRecords int

	// Per-cell components map indexed by the same composite-key
	// pair as cellValues. The buffered cell builder runs the cell
	// aggregator through runCellAggregation which returns the post-
	// Finalize aggregator instance, scalar value, and per-cell n_null
	// (counted by walking the bucket once for the cell field). When the
	// aggregator implements MetaAggregator the operator-specific keys are
	// merged into a flat map alongside the universal floor (n, n_null);
	// for floor-only operators (AGG_COUNT / AGG_NULL_COUNT) the operator
	// half is empty and the cell's payload is the floor alone. Empty
	// cells (no bucket) emit nil — preserved by the nil sentinel
	// populateCrosstabComponents writes when no entry exists for (r, c).
	//
	// Built only when the ComputePlan computes components.crosstab (the
	// `return` selection and the DisableComponents gate): a nil map
	// skips every per-cell and per-margin MetaAggregator.Components()
	// call below, not merely their emission.
	buildComps := p.compute.Crosstab
	var cellComponents map[crosstabCellKey]map[string]any
	if buildComps {
		cellComponents = make(map[crosstabCellKey]map[string]any, len(rowPart.Keys)*len(colPart.Keys))
	}

	for ri, rkey := range rowPart.Keys {
		if len(rowPart.Records[rkey]) == 0 {
			continue
		}
		innerCol := in.innerCols[ri]
		for _, ckey := range innerCol.Keys {
			bucket := innerCol.Records[ckey]
			if len(bucket) == 0 {
				continue
			}
			ck := crosstabCellKey{rkey, ckey}
			cellCounts[ck] = len(bucket)
			includedRecords += len(bucket)
			val, instance, n, nNull, err := p.runCellAggregation(cellAgg, bucket)
			if err != nil {
				return nil, err
			}
			if buildComps {
				compMap, err := buildCellComponentMap(instance, n, nNull)
				if err != nil {
					return nil, err
				}
				val.weight.stampMap(compMap)
				cellComponents[ck] = compMap
			}
			if !val.present {
				continue
			}
			cellValues[ck] = val.value
			cellPresent[ck] = true
		}
	}

	// Margin recompute routes through runCellAggregation so
	// the orchestrator captures the post-Finalize aggregator instance
	// alongside the scalar/rich margin value. The instance feeds
	// buildCellComponentMap which merges the universal floor {n, n_null}
	// with the cell aggregator's MetaAggregator.Components() output for
	// the row-margin / column-margin / grand-total components emission.
	// Component maps are tracked whenever the plan computes
	// components.crosstab and the margin slot is computed
	// (NeedsRowMargin / NeedsColumnMargin / NeedsGrandMargin),
	// but only flow to Response.Components when the corresponding
	// display flag is set — mirroring the MatrixPayload.RowMargins /
	// ColumnMargins / GrandTotal emission rule (computed for
	// normalization, surfaced only on explicit request).
	//
	// THE ADMISSION RULE (U47 #325). A record reaches a margin only if it
	// reached a CELL: both its row key and its column key resolved. A row
	// margin therefore walks its row bucket narrowed to the records that
	// are also column-axis members, a column margin the reverse, and the
	// grand margin the records that are members of BOTH axes. A record
	// whose GROUP_SET_PER_ELEMENT set is null or empty, or whose axis key
	// a grouper Include excluded, reached no cell and so moves no margin
	// and no normalize denominator — column % is "% of those asked", not
	// "% of everyone". The cell field's own nullity is NOT a gate here
	// (admitRecords with a nil cellAgg): a null cell field still reached
	// its cell, as that cell's n_null, and the margin floor counts it the
	// same way. The fused walk applies the identical gate
	// (FusedCrosstabState.Update); the auxiliary margins add the cell-field
	// probe on top (computeAuxMargins).
	//
	// A nil membership set means "every filter-passing record resolved
	// this axis", which admitRecords reads as "do not cross-check" — the
	// common no-null, no-Include crosstab pays no per-record filtering.
	rowMember, colMember := in.rowMember, in.colMember

	// Normalise-denominator floors for the pairwise proportion overlays
	// (crosstab_base_floors.go, U47 #324): collected only under a
	// normalize mode on a run that folds overlays, and never written to
	// the wire.
	var baseSrc *baseFloorSource
	if spec.NormalizeOrDefault() != types.CrosstabNormalizeNone && len(in.req.Overlays) > 0 && p.compute.Overlays {
		baseSrc = &baseFloorSource{mode: spec.NormalizeOrDefault()}
	}

	var rowMargins map[string]any
	var rowMarginPresent map[string]bool
	var rowMarginCounts map[string]int
	var rowMarginComponents map[string]map[string]any
	if spec.NeedsRowMargin() {
		rowMargins = make(map[string]any, len(rowPart.Keys))
		rowMarginPresent = make(map[string]bool, len(rowPart.Keys))
		rowMarginCounts = make(map[string]int, len(rowPart.Keys))
		if buildComps {
			rowMarginComponents = make(map[string]map[string]any, len(rowPart.Keys))
		}
		for _, rkey := range rowPart.Keys {
			bucket := admitRecords(rowPart.Records[rkey], colMember, nil, nil)
			if len(bucket) == 0 {
				continue
			}
			val, instance, n, nNull, err := p.runCellAggregation(cellAgg, bucket)
			if err != nil {
				return nil, err
			}
			rowMarginCounts[rkey] = n + nNull
			if baseSrc != nil && baseSrc.mode == types.CrosstabNormalizeRow {
				if baseSrc.rows == nil {
					baseSrc.rows = make(map[string]baseTally, len(rowPart.Keys))
				}
				baseSrc.rows[rkey] = baseTally{n: n, w: val.weight}
			}
			if buildComps {
				compMap, err := buildCellComponentMap(instance, n, nNull)
				if err != nil {
					return nil, err
				}
				val.weight.stampMap(compMap)
				rowMarginComponents[rkey] = compMap
			}
			if !val.present {
				continue
			}
			rowMargins[rkey] = val.value
			rowMarginPresent[rkey] = true
		}
	}

	var colMargins map[string]any
	var colMarginPresent map[string]bool
	var colMarginCounts map[string]int
	var colMarginComponents map[string]map[string]any
	if spec.NeedsColumnMargin() {
		colMargins = make(map[string]any, len(colPart.Keys))
		colMarginPresent = make(map[string]bool, len(colPart.Keys))
		colMarginCounts = make(map[string]int, len(colPart.Keys))
		if buildComps {
			colMarginComponents = make(map[string]map[string]any, len(colPart.Keys))
		}
		for _, ckey := range colPart.Keys {
			bucket := admitRecords(colPart.Records[ckey], rowMember, nil, nil)
			if len(bucket) == 0 {
				continue
			}
			val, instance, n, nNull, err := p.runCellAggregation(cellAgg, bucket)
			if err != nil {
				return nil, err
			}
			colMarginCounts[ckey] = n + nNull
			if baseSrc != nil && baseSrc.mode == types.CrosstabNormalizeColumn {
				if baseSrc.cols == nil {
					baseSrc.cols = make(map[string]baseTally, len(colPart.Keys))
				}
				baseSrc.cols[ckey] = baseTally{n: n, w: val.weight}
			}
			if buildComps {
				compMap, err := buildCellComponentMap(instance, n, nNull)
				if err != nil {
					return nil, err
				}
				val.weight.stampMap(compMap)
				colMarginComponents[ckey] = compMap
			}
			if !val.present {
				continue
			}
			colMargins[ckey] = val.value
			colMarginPresent[ckey] = true
		}
	}

	var grandMargin any
	grandPresent := false
	var grandMarginCount int
	var grandMarginComponents map[string]any
	if spec.NeedsGrandMargin() {
		if admitted := admitRecords(filtered, rowMember, colMember, nil); len(admitted) > 0 {
			val, instance, n, nNull, err := p.runCellAggregation(cellAgg, admitted)
			if err != nil {
				return nil, err
			}
			grandMarginCount = n + nNull
			if baseSrc != nil {
				baseSrc.grand, baseSrc.grandOK = baseTally{n: n, w: val.weight}, true
			}
			if buildComps {
				compMap, err := buildCellComponentMap(instance, n, nNull)
				if err != nil {
					return nil, err
				}
				val.weight.stampMap(compMap)
				grandMarginComponents = compMap
			}
			if val.present {
				grandMargin = val.value
				grandPresent = true
			}
		}
	}

	// Auxiliary margin-only aggregations (spec.MarginAggregations).
	// Evaluated here, immediately after the cell aggregator's own three
	// margin blocks, because these are margins too — the difference is
	// WHICH records reach them, not where they sit. See
	// crosstab_margin_agg.go for the admission rule: the blocks above
	// share its two axis exclusions but not its cell-field probe.
	//
	// Placed AHEAD of the partial-depth and cross-axis margin work below
	// on purpose: those exist solely as normalization denominators for
	// the cell, and an auxiliary is never a denominator, so neither gets
	// one — on either path.
	//
	// THE FIGURES REACH THE WIRE through the components block below:
	// auxMargins.components() normalises this path's carrier into the one
	// emission shape populateCrosstabComponents projects, and the fused
	// path normalises its live accumulators into the same shape.
	//
	// Computed HERE rather than inside that block, i.e. OUTSIDE the
	// components gate, because this call is also what refuses an
	// auxiliary naming an operator no registry knows — which the fused
	// arm refuses at construction, unconditionally. Moving it inside the
	// gate would make that refusal depend on whether components happened
	// to be enabled
	// (TestCrosstab_BufferedAuxMarginUnresolvableTypeRefused). The
	// ACCUMULATION past that refusal does ride the plan:
	// computeAuxMargins returns nil without evaluating a figure when
	// ComputePlan.AuxMargins is off.
	auxMargins, err := p.computeAuxMargins(spec, filtered, rowPart, colPart)
	if err != nil {
		return nil, err
	}

	mode := spec.NormalizeOrDefault()

	// Partial-depth normalization. When spec.NormalizeLevel selects a
	// non-leaf depth on the normalization axis, build partial margins
	// keyed by the truncated composite key and a leaf→partial lookup so
	// the per-cell loop dispatches through the partial denominator.
	// When level == leaf (default), partial structures alias the leaf
	// maps; byte-equal output is preserved.
	var partialRowPart, partialColPart *CrosstabAxisPartition
	var partialRowMargins map[string]float64
	var partialRowPresent map[string]bool
	var partialColMargins map[string]float64
	var partialColPresent map[string]bool
	var leafRowToPartial map[string]string
	var leafColToPartial map[string]string
	rowNormLevel := -1
	colNormLevel := -1

	if mode == types.CrosstabNormalizeRow {
		axisLen := len(spec.Rows)
		level := spec.NormalizeLevelOrLeaf(axisLen)
		rowNormLevel = level
		if level == axisLen-1 {
			// Normalize rejected for map cells; coerce-cast the leaf
			// margin map (every value is float64 here).
			partialRowMargins = coerceAnyMarginMap(rowMargins)
			partialRowPresent = rowMarginPresent
		} else {
			partialRowPart, err = p.PartitionByAxis(spec.Rows[:level+1], filtered)
			if err != nil {
				return nil, err
			}
			partialRowMargins = make(map[string]float64, len(partialRowPart.Keys))
			partialRowPresent = make(map[string]bool, len(partialRowPart.Keys))
			if baseSrc != nil {
				baseSrc.rows = make(map[string]baseTally, len(partialRowPart.Keys))
			}
			for _, pkey := range partialRowPart.Keys {
				bucket := admitRecords(partialRowPart.Records[pkey], rowMember, colMember, nil)
				if len(bucket) == 0 {
					continue
				}
				if baseSrc != nil {
					baseSrc.rows[pkey] = bucketBaseTally(cellAgg, bucket)
				}
				row, err := p.aggregate([]*types.Aggregation{cellAgg}, bucket)
				if err != nil {
					return nil, err
				}
				if row == nil {
					continue
				}
				partialRowMargins[pkey] = coerceFloat64(row[cellLabel])
				partialRowPresent[pkey] = true
			}
			leafRowToPartial = buildLeafToPartial(rowPart.Keys, level)
			if baseSrc != nil {
				baseSrc.leafRowToPartial = leafRowToPartial
			}
		}
	}
	if mode == types.CrosstabNormalizeColumn {
		axisLen := len(spec.Columns)
		level := spec.NormalizeLevelOrLeaf(axisLen)
		colNormLevel = level
		// The implicit Total column has no depth to truncate: its one
		// leaf margin is the denominator.
		if axisLen == 0 || level == axisLen-1 {
			partialColMargins = coerceAnyMarginMap(colMargins)
			partialColPresent = colMarginPresent
		} else {
			partialColPart, err = p.PartitionByAxis(spec.Columns[:level+1], filtered)
			if err != nil {
				return nil, err
			}
			partialColMargins = make(map[string]float64, len(partialColPart.Keys))
			partialColPresent = make(map[string]bool, len(partialColPart.Keys))
			if baseSrc != nil {
				baseSrc.cols = make(map[string]baseTally, len(partialColPart.Keys))
			}
			for _, pkey := range partialColPart.Keys {
				bucket := admitRecords(partialColPart.Records[pkey], rowMember, colMember, nil)
				if len(bucket) == 0 {
					continue
				}
				if baseSrc != nil {
					baseSrc.cols[pkey] = bucketBaseTally(cellAgg, bucket)
				}
				row, err := p.aggregate([]*types.Aggregation{cellAgg}, bucket)
				if err != nil {
					return nil, err
				}
				if row == nil {
					continue
				}
				partialColMargins[pkey] = coerceFloat64(row[cellLabel])
				partialColPresent[pkey] = true
			}
			leafColToPartial = buildLeafToPartial(colPart.Keys, level)
			if baseSrc != nil {
				baseSrc.leafColToPartial = leafColToPartial
			}
		}
	}

	// Cross-axis partition normalization. When spec.NormalizeWithin is
	// set, the denominator fixes the full normalize-axis key (truncated
	// to NormalizeLevel if that is also set) AND a prefix of the OTHER
	// axis at the configured depth. Distinct from same-axis truncation
	// (NormalizeLevel alone) — both can coexist on the same request.
	var crossMargins map[crosstabCellKey]float64
	var crossLeafRowToPartial map[string]string
	var crossLeafColToPartial map[string]string
	crossActive := false
	if mode != types.CrosstabNormalizeNone && spec.NormalizeWithin != nil {
		var rowDepth, colDepth int
		var partitionAxis []*types.Group
		switch mode {
		case types.CrosstabNormalizeRow:
			rowDepth = spec.NormalizeLevelOrLeaf(len(spec.Rows))
			colDepth = *spec.NormalizeWithin
		case types.CrosstabNormalizeColumn:
			colDepth = spec.NormalizeLevelOrLeaf(len(spec.Columns))
			rowDepth = *spec.NormalizeWithin
		}
		partitionAxis = append(partitionAxis, spec.Rows[:rowDepth+1]...)
		// Under the implicit Total column (normalize=column only — a
		// row-mode normalize_within has no column depth and is refused)
		// there is no column prefix to fix: the joined key is the row
		// prefix alone and its column part is "", the Total column's
		// composite key.
		partitionAxis = append(partitionAxis, spec.Columns[:min(colDepth+1, len(spec.Columns))]...)
		joined, err := p.PartitionByAxis(partitionAxis, filtered)
		if err != nil {
			return nil, err
		}
		crossMargins = make(map[crosstabCellKey]float64, len(joined.Keys))
		if baseSrc != nil {
			baseSrc.cross = make(map[crosstabCellKey]baseTally, len(joined.Keys))
		}
		for _, jkey := range joined.Keys {
			bucket := admitRecords(joined.Records[jkey], rowMember, colMember, nil)
			if len(bucket) == 0 {
				continue
			}
			if baseSrc != nil {
				parts := strings.Split(jkey, crosstabAxisKeySep)
				baseSrc.cross[crosstabCellKey{
					row: strings.Join(parts[:rowDepth+1], crosstabAxisKeySep),
					col: strings.Join(parts[rowDepth+1:], crosstabAxisKeySep),
				}] = bucketBaseTally(cellAgg, bucket)
			}
			row, err := p.aggregate([]*types.Aggregation{cellAgg}, bucket)
			if err != nil {
				return nil, err
			}
			if row == nil {
				continue
			}
			parts := strings.Split(jkey, crosstabAxisKeySep)
			rPartKey := strings.Join(parts[:rowDepth+1], crosstabAxisKeySep)
			cPartKey := strings.Join(parts[rowDepth+1:], crosstabAxisKeySep)
			crossMargins[crosstabCellKey{rPartKey, cPartKey}] = coerceFloat64(row[cellLabel])
		}
		crossLeafRowToPartial = buildLeafToPartial(rowPart.Keys, rowDepth)
		crossLeafColToPartial = buildLeafToPartial(colPart.Keys, colDepth)
		crossActive = true
		if baseSrc != nil {
			baseSrc.crossActive = true
			baseSrc.crossRow, baseSrc.crossCol = crossLeafRowToPartial, crossLeafColToPartial
		}
	}

	if mode != types.CrosstabNormalizeNone {
		// Map-valued aggregators rejected upstream by the
		// MapValued() gate; every cell here is scalar.
		normalized := make(map[crosstabCellKey]any, len(cellValues))
		for ck, v := range cellValues {
			var denom float64
			var ok bool
			if crossActive {
				key := crosstabCellKey{
					row: crossLeafRowToPartial[ck.row],
					col: crossLeafColToPartial[ck.col],
				}
				if d, present := crossMargins[key]; present {
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
					partialRowMargins, partialRowPresent,
					partialColMargins, partialColPresent,
					coerceFloat64(grandMargin), grandPresent)
			}
			if !ok || denom == 0 {
				// Divide-by-zero policy: drop the cell. Downstream
				// rendering sees Present=false.
				cellPresent[ck] = false
				continue
			}
			normalized[ck] = coerceFloat64(v) / denom
		}
		cellValues = normalized
	}

	shape := spec.ShapeOrDefault()
	if shape == types.CrosstabShapeMatrix {
		resp.Crosstab = &types.CrosstabResult{
			Shape:  shape,
			Matrix: buildMatrixPayload(spec, rowPart, colPart, cellValues, cellPresent, rowMargins, rowMarginPresent, colMargins, colMarginPresent, grandMargin, grandPresent, cellLabel, mode),
		}
	} else {
		resp.Crosstab = &types.CrosstabResult{Shape: shape}
		resp.Data = buildLongRows(spec, rowPart, colPart, cellValues, cellPresent,
			rowMargins, rowMarginPresent, colMargins, colMarginPresent,
			grandMargin, grandPresent, cellLabel,
			partialRowPart, partialRowMargins, partialRowPresent, rowNormLevel,
			partialColPart, partialColMargins, partialColPresent, colNormLevel)
	}

	// Emit per-cell record-count matrix on Response.Components.
	// Layout indexed identically to MatrixPayload.Cells (rowPart.Keys
	// outer, colPart.Keys inner) so consumers can use the same (r, c) to
	// dereference cell value and cell count. Even shape=long callers see
	// the matrix because the components slot is shape-agnostic; the
	// indexing tracks rowPart / colPart key order which is deterministic
	// regardless of shape.
	excludedRecords := int(in.filteredRows) - includedRecords
	if excludedRecords < 0 {
		// Defensive: should never happen — includedRecords is bounded by
		// the count of records that landed in at least one cell, which is
		// at most filteredRows. Surface the divergence as zero rather
		// than emit a negative count to the wire.
		excludedRecords = 0
	}
	// Row / column / grand-total margin counts + components emit
	// alongside the cell matrices. Display flags drive the emission gate —
	// counts/components ride only when the corresponding MatrixPayload
	// margin slot is populated (spec.Margins.Rows / Columns / Grand). The
	// normalization-only path computes the margins internally but does
	// not surface counts/components (mirrors the value emission rule).
	var rowMarginCountsSlot map[string]int
	var rowMarginComponentsSlot map[string]map[string]any
	if spec.Margins.Rows {
		rowMarginCountsSlot = rowMarginCounts
		rowMarginComponentsSlot = rowMarginComponents
	}
	var colMarginCountsSlot map[string]int
	var colMarginComponentsSlot map[string]map[string]any
	if spec.Margins.Columns {
		colMarginCountsSlot = colMarginCounts
		colMarginComponentsSlot = colMarginComponents
	}
	var grandMarginCountSlot int
	var grandMarginComponentsSlot map[string]any
	if spec.Margins.Grand {
		grandMarginCountSlot = grandMarginCount
		grandMarginComponentsSlot = grandMarginComponents
	}
	// Components emission block — gated by the processor's
	// ComputePlan (components.crosstab). The axis grouper instantiation +
	// MetaGrouper.Components walk is expensive on wide cohorts, so the
	// gate skips that work entirely rather than building then discarding.
	if p.compute.Crosstab {
		populateCrosstabComponents(resp, rowPart.Keys, colPart.Keys,
			cellCounts, cellComponents,
			rowMarginCountsSlot, rowMarginComponentsSlot,
			colMarginCountsSlot, colMarginComponentsSlot,
			grandMarginCountSlot, grandMarginComponentsSlot, spec.Margins.Grand,
			includedRecords, excludedRecords,
			in.rowKeyComponents, in.colKeyComponents,
			auxMargins.components(spec.Margins))
	}

	run := &crosstabMeasureRun{
		resp:            resp,
		postRows:        buildCellRowsForPostTest(spec, rowPart, colPart, cellValues, cellPresent, cellLabel),
		bases:           baseSrc.build(rowPart.Keys, colPart.Keys),
		includedRecords: includedRecords,
	}
	return run, nil
}

// crosstabMeasureHost is one measure's single-measure host for the
// overlay fold: the overlays of a multi-measure crosstab run per measure
// against exactly the payload, components and base floors the
// single-measure crosstab would have produced (overlay_measures.go).
type crosstabMeasureHost struct {
	cell   *types.Aggregation
	matrix *types.MatrixPayload
	comps  *types.CrosstabComponents
	bases  *crosstabBaseFloors
}

// crosstabFold is the folded output of every measure run.
type crosstabFold struct {
	result   *types.CrosstabResult
	long     []map[string]any
	comps    *types.CrosstabComponents
	postRows []map[string]any
	// bases is the single-measure base floors; nil on the multi-measure
	// form, whose overlays read measureHosts instead.
	bases        *crosstabBaseFloors
	measureHosts []crosstabMeasureHost
}

// foldCrosstabMeasures stitches the measure runs into one result. The
// single-measure form returns its one run unchanged, so a request
// without Cells is byte-identical to the pre-Cells wire. The
// multi-measure form folds a MEASURE position into the row axis:
//
//   - the matrix row header gains {_measure, MEASURE}; spec row r becomes
//     payload rows r*M .. r*M+M-1, row key + measure label, so a stub
//     row's measures sit together in declaration order;
//   - Cells, RowMargins and every row-indexed Components slot follow the
//     folded rows; CellLabels names each row's measure;
//   - column margins and the grand total — per-measure figures — move to
//     MatrixPayload.Measures / CrosstabComponents.Measures, leaving the
//     single-measure slots empty;
//   - long-shape rows (and the post-test cell rows) are each measure's
//     rows in declaration order, every row stamped `_measure`.
//
// Every measure ran over the same partitions, so the axis keys are equal
// by construction; a mismatch is an invariant violation
// (PROCESSING_INTERNAL), never silently realigned.
func foldCrosstabMeasures(spec *types.CrosstabSpec, runs []*crosstabMeasureRun) (*crosstabFold, error) {
	if len(runs) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL, "crosstab fold received no measure run")
	}
	if !spec.HasMeasures() {
		r := runs[0]
		f := &crosstabFold{result: r.resp.Crosstab, long: r.resp.Data, postRows: r.postRows, bases: r.bases}
		if r.resp.Components != nil {
			f.comps = r.resp.Components.Crosstab
		}
		return f, nil
	}
	labels := spec.MeasureLabels()
	f := &crosstabFold{result: &types.CrosstabResult{Shape: spec.ShapeOrDefault()}}
	f.measureHosts = make([]crosstabMeasureHost, len(runs))
	for m, r := range runs {
		host := crosstabMeasureHost{cell: spec.Cells[m], bases: r.bases}
		if r.resp.Crosstab != nil {
			host.matrix = r.resp.Crosstab.Matrix
		}
		if r.resp.Components != nil {
			host.comps = r.resp.Components.Crosstab
		}
		f.measureHosts[m] = host
		f.long = append(f.long, stampMeasureRows(r.resp.Data, labels[m])...)
		f.postRows = append(f.postRows, stampMeasureRows(r.postRows, labels[m])...)
	}
	if f.result.Shape == types.CrosstabShapeMatrix {
		mx, err := foldMeasureMatrices(spec, labels, f.measureHosts)
		if err != nil {
			return nil, err
		}
		f.result.Matrix = mx
	}
	comps, err := foldMeasureComponents(labels, f.measureHosts)
	if err != nil {
		return nil, err
	}
	f.comps = comps
	return f, nil
}

// stampMeasureRows copies rows, stamping each with its measure label
// under types.CrosstabMeasureField.
func stampMeasureRows(rows []map[string]any, label string) []map[string]any {
	if len(rows) == 0 {
		return nil
	}
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		cp := make(map[string]any, len(row)+1)
		for k, v := range row {
			cp[k] = v
		}
		cp[types.CrosstabMeasureField] = label
		out[i] = cp
	}
	return out
}

// sameAxisKeys reports whether two axis-key lists are equal tuple for
// tuple (the fold's invariant check).
func sameAxisKeys(a, b []types.AxisKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if stringifyOverlayAxisKey(a[i]) != stringifyOverlayAxisKey(b[i]) {
			return false
		}
	}
	return true
}

// foldMeasureMatrices interleaves the measures' matrices into the
// MEASURE-folded payload.
func foldMeasureMatrices(spec *types.CrosstabSpec, labels []string, hosts []crosstabMeasureHost) (*types.MatrixPayload, error) {
	base := hosts[0].matrix
	if base == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL, "crosstab fold: measure 0 produced no matrix")
	}
	for m, h := range hosts {
		if h.matrix == nil || !sameAxisKeys(h.matrix.RowKeys, base.RowKeys) || !sameAxisKeys(h.matrix.ColumnKeys, base.ColumnKeys) {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"crosstab fold: measure axis keys diverged", map[string]any{"measure": m})
		}
	}
	M := len(hosts)
	rows := len(base.RowKeys)
	out := &types.MatrixPayload{
		RowHeader:        spec.MeasureRowHeader(),
		ColumnHeader:     base.ColumnHeader,
		RowKeys:          make([]types.AxisKey, 0, rows*M),
		ColumnKeys:       append([]types.AxisKey(nil), base.ColumnKeys...),
		Cells:            make([][]types.MatrixCell, 0, rows*M),
		CellLabels:       make([]string, 0, rows*M),
		NormalizeApplied: base.NormalizeApplied,
		Measures:         make([]types.CrosstabMeasureMargins, M),
	}
	if spec.Margins.Rows {
		out.RowMargins = make([]types.MatrixCell, 0, rows*M)
	}
	for r := 0; r < rows; r++ {
		for m, h := range hosts {
			key := append(append(types.AxisKey(nil), base.RowKeys[r]...), labels[m])
			out.RowKeys = append(out.RowKeys, key)
			out.Cells = append(out.Cells, h.matrix.Cells[r])
			out.CellLabels = append(out.CellLabels, labels[m])
			if spec.Margins.Rows {
				var cell types.MatrixCell
				if r < len(h.matrix.RowMargins) {
					cell = h.matrix.RowMargins[r]
				}
				out.RowMargins = append(out.RowMargins, cell)
			}
		}
	}
	for m, h := range hosts {
		out.Measures[m] = types.CrosstabMeasureMargins{
			Label:         labels[m],
			ColumnMargins: h.matrix.ColumnMargins,
			GrandTotal:    h.matrix.GrandTotal,
		}
	}
	return out, nil
}

// foldMeasureComponents folds the measures' Components.Crosstab blocks:
// the row-indexed slots over the folded rows, the column-margin and
// grand-total slots into Measures. nil when no measure produced
// components (components disabled).
func foldMeasureComponents(labels []string, hosts []crosstabMeasureHost) (*types.CrosstabComponents, error) {
	if hosts[0].comps == nil {
		return nil, nil
	}
	base := hosts[0].comps
	M := len(hosts)
	rows := len(base.CellCounts)
	out := &types.CrosstabComponents{
		ColumnKeyComponents: base.ColumnKeyComponents,
		IncludedRecords:     base.IncludedRecords,
		ExcludedRecords:     base.ExcludedRecords,
		Measures:            make([]types.CrosstabMeasureComponents, M),
	}
	for m, h := range hosts {
		if h.comps == nil || len(h.comps.CellCounts) != rows {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"crosstab fold: measure components diverged", map[string]any{"measure": m})
		}
	}
	foldRows := func(get func(c *types.CrosstabComponents, r int)) {
		for r := 0; r < rows; r++ {
			for _, h := range hosts {
				get(h.comps, r)
			}
		}
	}
	if base.CellCounts != nil {
		out.CellCounts = make([][]int, 0, rows*M)
		foldRows(func(c *types.CrosstabComponents, r int) {
			out.CellCounts = append(out.CellCounts, c.CellCounts[r])
		})
	}
	if base.CellComponents != nil {
		out.CellComponents = make([][]map[string]any, 0, rows*M)
		foldRows(func(c *types.CrosstabComponents, r int) {
			var row []map[string]any
			if r < len(c.CellComponents) {
				row = c.CellComponents[r]
			}
			out.CellComponents = append(out.CellComponents, row)
		})
	}
	if base.RowMarginCounts != nil {
		out.RowMarginCounts = make([]int, 0, rows*M)
		foldRows(func(c *types.CrosstabComponents, r int) {
			v := 0
			if r < len(c.RowMarginCounts) {
				v = c.RowMarginCounts[r]
			}
			out.RowMarginCounts = append(out.RowMarginCounts, v)
		})
	}
	if base.RowMarginComponents != nil {
		out.RowMarginComponents = make([]map[string]any, 0, rows*M)
		foldRows(func(c *types.CrosstabComponents, r int) {
			var v map[string]any
			if r < len(c.RowMarginComponents) {
				v = c.RowMarginComponents[r]
			}
			out.RowMarginComponents = append(out.RowMarginComponents, v)
		})
	}
	if base.RowMarginAggregations != nil {
		out.RowMarginAggregations = make([]map[string]types.MarginAggregationFigure, 0, rows*M)
		foldRows(func(c *types.CrosstabComponents, r int) {
			var v map[string]types.MarginAggregationFigure
			if r < len(c.RowMarginAggregations) {
				v = c.RowMarginAggregations[r]
			}
			out.RowMarginAggregations = append(out.RowMarginAggregations, v)
		})
	}
	if base.RowKeyComponents != nil {
		out.RowKeyComponents = make([]map[string]any, 0, rows*M)
		foldRows(func(c *types.CrosstabComponents, r int) {
			var v map[string]any
			if r < len(c.RowKeyComponents) {
				v = c.RowKeyComponents[r]
			}
			out.RowKeyComponents = append(out.RowKeyComponents, v)
		})
	}
	for m, h := range hosts {
		c := h.comps
		out.Measures[m] = types.CrosstabMeasureComponents{
			Label:                    labels[m],
			ColumnMarginCounts:       c.ColumnMarginCounts,
			ColumnMarginComponents:   c.ColumnMarginComponents,
			ColumnMarginAggregations: c.ColumnMarginAggregations,
			GrandTotalCount:          c.GrandTotalCount,
			GrandTotalComponents:     c.GrandTotalComponents,
			GrandTotalAggregations:   c.GrandTotalAggregations,
		}
	}
	return out, nil
}
