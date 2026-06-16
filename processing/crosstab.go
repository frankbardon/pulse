package processing

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// crosstabAxisKeySep separates per-grouper key components in the
// composite key used to address a row or column axis tuple. \x00 is not
// a legal byte in any grouper key (groupers emit either stringified
// numerics, dictionary strings, or formatted date strings), so it serves
// as an unambiguous tuple separator.
const crosstabAxisKeySep = "\x00"

// crosstabCellKey addresses one cell by its (rowComposite, colComposite)
// composite keys.
type crosstabCellKey struct{ row, col string }

// CrosstabAxisPartition is the result of recursively partitioning a
// record set by an ordered list of groupers (rows or columns).
type CrosstabAxisPartition struct {
	// Keys is the sorted list of composite-key strings (one per axis
	// tuple). compositeAxisKey(Tuples[i]) == Keys[i].
	Keys []string
	// Tuples is the parallel list of axis tuples, one per Key. Each
	// tuple has len(axis) entries — one per axis grouper, in axis
	// order. Categorical / numeric / date keys are kept as the grouper
	// emitted them (always strings via Grouper.Group's contract).
	Tuples []types.AxisKey
	// Records maps composite key → record bucket for that axis tuple.
	Records map[string][]*Record
}

// PartitionByAxis recursively partitions records by an ordered list of
// groupers, producing one bucket per axis tuple. The returned partition
// is sorted by composite key for deterministic output ordering across
// runs. Empty axis returns a single global bucket keyed by the empty
// string, so callers can write a single uniform reshape loop.
//
// Exported for crosstab and any embedder that needs deterministic
// multi-grouper partitioning without going through the full Process
// pipeline.
func (p *Processor) PartitionByAxis(axis []*types.Group, records []*Record) (*CrosstabAxisPartition, error) {
	if len(axis) == 0 {
		recs := records
		if recs == nil {
			recs = []*Record{}
		}
		return &CrosstabAxisPartition{
			Keys:    []string{""},
			Tuples:  []types.AxisKey{{}},
			Records: map[string][]*Record{"": recs},
		}, nil
	}

	grp := axis[0]
	factory, ok := p.exts.LookupGrouper(grp.Type)
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown group type: %s", grp.Type))
	}
	grouper, err := factory(grp, p.schema)
	if err != nil {
		return nil, err
	}
	buckets, err := grouper.Group(records, grp.Field)
	if err != nil {
		return nil, err
	}

	headKeys := make([]string, 0, len(buckets))
	for k := range buckets {
		headKeys = append(headKeys, k)
	}
	sort.Strings(headKeys)

	out := &CrosstabAxisPartition{
		Records: make(map[string][]*Record, len(buckets)),
	}
	if len(axis) == 1 {
		for _, k := range headKeys {
			out.Keys = append(out.Keys, k)
			out.Tuples = append(out.Tuples, types.AxisKey{k})
			out.Records[k] = buckets[k]
		}
		return out, nil
	}

	for _, k := range headKeys {
		child, err := p.PartitionByAxis(axis[1:], buckets[k])
		if err != nil {
			return nil, err
		}
		for i, ckey := range child.Keys {
			composite := k + crosstabAxisKeySep + ckey
			tuple := make(types.AxisKey, 0, 1+len(child.Tuples[i]))
			tuple = append(tuple, k)
			tuple = append(tuple, child.Tuples[i]...)
			out.Keys = append(out.Keys, composite)
			out.Tuples = append(out.Tuples, tuple)
			out.Records[composite] = child.Records[ckey]
		}
	}
	return out, nil
}

// CompositeAxisKey joins a tuple of per-grouper key components into a
// single composite key string. Mirrors the composite-key shape used by
// PartitionByAxis so callers can index its Records map directly. The
// separator is internal — callers should not parse the output.
func CompositeAxisKey(parts []string) string {
	return strings.Join(parts, crosstabAxisKeySep)
}

// axisComponentsFor builds the per-axis-position grouper components for
// a crosstab axis. One *types.Group per axis position is independently
// instantiated and exercised against the full filtered record set; the
// resulting MetaGrouper.Components() emission is captured per position
// so RunCrosstab can index axis-key buckets by (position, key) when
// projecting axis components onto the sorted RowKeys / ColumnKeys.
//
// Per-position groupers are constructed fresh — independent of the
// instances PartitionByAxis used for the cell partition — so the
// recursive sub-partition pass doesn't have to expose its private
// grouper sequence. The cost is one extra Group() call per axis
// position, which is bounded by len(axis) ≤ a handful in practice.
//
// Returned slice has len(axis) entries. Entry i is the Components map
// emitted by the grouper at axis position i; entries are nil when the
// grouper does not implement MetaGrouper (extension groupers without a
// Components() implementation). Empty axes return a nil slice.
//
// Used by RunCrosstab to feed populateCrosstabComponents with the
// axis-grouper bucket maps. The fused path captures the same per-axis
// emission off its long-lived rowGroupers / colGroupers instances.
func (p *Processor) axisComponentsFor(axis []*types.Group, filtered []*Record) ([]map[string]any, error) {
	if len(axis) == 0 {
		return nil, nil
	}
	out := make([]map[string]any, len(axis))
	for i, grp := range axis {
		factory, ok := p.exts.LookupGrouper(grp.Type)
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown group type: %s", grp.Type))
		}
		grouper, err := factory(grp, p.schema)
		if err != nil {
			return nil, err
		}
		// Run the grouper against the full filtered set so liveBuckets
		// reflects every key the axis observed across the cohort. The
		// returned partition map is discarded — the cell-partition pass
		// in RunCrosstab handles routing; this call is purely to populate
		// MetaGrouper.Components() state.
		if _, err := grouper.Group(filtered, grp.Field); err != nil {
			return nil, err
		}
		if meta, ok := grouper.(MetaGrouper); ok {
			op, err := meta.Components()
			if err != nil {
				return nil, err
			}
			out[i] = op
		}
	}
	return out, nil
}

// projectAxisKeyComponents builds the per-axis-key component vector
// indexed in axisKeys order from (a) the sorted axis-tuple slice (one
// tuple per key) and (b) the per-axis-position components emissions.
// For single-axis (len(axis)==1) the per-key entry is the bucket map
// emitted by the sole axis grouper for that key; for multi-axis the
// entry carries every position's bucket merged under an "axes" slice
// keyed by the axis field name.
//
// Lookup strategy: each per-position components map is expected to
// carry a "buckets" entry of type []map[string]any whose elements have
// a "key" string field. The per-key bucket vector is built by indexing
// these buckets once per position so the per-axis-key loop is a flat
// O(len(axisKeys) * len(axis)) cost without nested probes against the
// underlying buckets slice.
//
// Returns nil when axis is empty (no axis to project). Returns a slice
// of len(axisTuples) entries, parallel to axisTuples / axisKeys, when
// axis is non-empty. Entries are nil when no bucket matches the tuple's
// per-position key in the axis components emission — defensive: every
// observed axis key should have a matching bucket because the grouper
// instances ran against the same filtered set the tuples came from.
func projectAxisKeyComponents(axis []*types.Group, axisTuples []types.AxisKey, axisComponents []map[string]any) []map[string]any {
	if len(axis) == 0 {
		return nil
	}
	out := make([]map[string]any, len(axisTuples))
	if len(axis) == 1 {
		// Single-axis common case: emit the bucket map directly so
		// consumers can read `RowKeyComponents[r]["count"]` without
		// drilling through an axes-array wrapper.
		buckets := axisBuckets(axisComponents[0])
		for i, tuple := range axisTuples {
			if len(tuple) == 0 {
				continue
			}
			key, ok := tuple[0].(string)
			if !ok {
				continue
			}
			out[i] = bucketByKey(buckets, key)
		}
		return out
	}
	// Multi-axis: emit composite layout
	//   {"axes": [{"field": <field>, "bucket": <bucket map>}, ...]}
	// so consumers can identify which axis position contributed which
	// bucket. Build per-position buckets slices once; the per-key loop
	// then runs O(len(axis)) lookups per row.
	perPosBuckets := make([][]map[string]any, len(axis))
	for p := range axis {
		perPosBuckets[p] = axisBuckets(axisComponents[p])
	}
	for i, tuple := range axisTuples {
		axes := make([]map[string]any, 0, len(axis))
		for p, grp := range axis {
			if p >= len(tuple) {
				break
			}
			key, ok := tuple[p].(string)
			if !ok {
				continue
			}
			entry := map[string]any{
				"field":  grp.Field,
				"bucket": bucketByKey(perPosBuckets[p], key),
			}
			axes = append(axes, entry)
		}
		out[i] = map[string]any{"axes": axes}
	}
	return out
}

// axisBuckets extracts the "buckets" slice from a per-axis-position
// Components emission. Every built-in grouper that implements
// MetaGrouper emits a "buckets" []map[string]any payload by contract
// (categoryGrouper / dateGrouper / rangeGrouper / roundedGrouper /
// quantileGrouper / setValueGrouper / setPerElementGrouper). Returns
// nil when the emission is absent (nil grouper components) or
// structurally divergent (extension grouper that omits the buckets
// key).
func axisBuckets(op map[string]any) []map[string]any {
	if op == nil {
		return nil
	}
	b, ok := op["buckets"].([]map[string]any)
	if !ok {
		return nil
	}
	return b
}

// bucketByKey returns the first bucket map whose "key" field equals
// target, or nil when none match. Linear scan is fine in practice —
// axis cardinalities cap out in the hundreds for the categorical
// groupers (dict-bounded) and the dozens for numeric / date binning.
func bucketByKey(buckets []map[string]any, target string) map[string]any {
	for _, b := range buckets {
		if k, ok := b["key"].(string); ok && k == target {
			return b
		}
	}
	return nil
}

// truncateCompositeKey returns the prefix of a composite axis key
// containing the first level+1 segments. Used to translate a leaf
// composite key (one segment per grouper in the axis) to the partial
// composite key that addresses the level-th depth bucket.
func truncateCompositeKey(composite string, level int) string {
	if level < 0 {
		return composite
	}
	parts := strings.Split(composite, crosstabAxisKeySep)
	if level+1 >= len(parts) {
		return composite
	}
	return strings.Join(parts[:level+1], crosstabAxisKeySep)
}

// buildLeafToPartial constructs the leaf→partial composite-key map for
// a sorted slice of leaf axis keys. Each entry maps a leaf composite
// key to its truncated prefix at the chosen depth.
func buildLeafToPartial(leafKeys []string, level int) map[string]string {
	out := make(map[string]string, len(leafKeys))
	for _, k := range leafKeys {
		out[k] = truncateCompositeKey(k, level)
	}
	return out
}

// CrosstabValidationError represents a structural failure surfaced by
// pre-execution validation in RunCrosstab. The error wraps a CodedError
// so callers see the same PULSE_CROSSTAB_* code regardless of entry
// point.
type CrosstabValidationError = errors.CodedError

// RunCrosstab executes a Request.Crosstab against a pre-drained record
// set. It runs the standard pre-aggregate pipeline (features → filters
// → attributes), partitions filtered records by the configured row and
// column axes, computes per-cell aggregates, recomputes the requested
// margins, applies the configured normalization, and emits either a
// MatrixPayload (shape=matrix) or long-form rows (shape=long).
//
// Margin computation is a recompute-only path in v1: every margin is
// derived from the raw filter-passing rows (not the cell values), so
// AGG_MEDIAN / AGG_STDDEV / AGG_PERCENTILE produce statistically
// correct margins. See AggregationType.MarginReducibility for the
// classification surfaced in the manifest.
//
// Streamability: this method always materializes the full filter-
// passing record set. shape=long without margins and without
// normalization is the only streamable case, and the orchestrator
// short-circuits to the standard grouped process path before reaching
// here (handled in service/crosstab.go).
func (p *Processor) RunCrosstab(_ context.Context, req *types.Request, records []*Record) (*types.Response, error) {
	if req == nil || req.Crosstab == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"RunCrosstab requires a Crosstab spec")
	}
	spec := req.Crosstab
	if err := validateCrosstabSpec(spec, req); err != nil {
		return nil, err
	}
	// Map-valued aggregators (AGG_SET_FREQUENCY today) cannot normalize —
	// dividing one map by another is undefined. Reject up-front so the
	// dispatch loop below can treat every scalar/rich routing as safe.
	if spec.Cell != nil &&
		spec.NormalizeOrDefault() != types.CrosstabNormalizeNone &&
		spec.Cell.Type.MapValued() {
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

	if err := p.applyFeatures(req.Features, records); err != nil {
		return nil, err
	}
	filtered, err := p.applyFilters(req.Filterers, records)
	if err != nil {
		return nil, err
	}
	if err := p.applyAttributes(req.Attributes, filtered); err != nil {
		return nil, err
	}

	totalRows := int64(len(records))
	filteredRows := int64(len(filtered))

	rowPart, err := p.PartitionByAxis(spec.Rows, filtered)
	if err != nil {
		return nil, err
	}
	colPart, err := p.PartitionByAxis(spec.Columns, filtered)
	if err != nil {
		return nil, err
	}

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

	// E3-S2: per-cell record count tracked alongside the aggregator update
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

	// E3-S3: per-cell components map indexed by the same composite-key
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
	cellComponents := make(map[crosstabCellKey]map[string]any, len(rowPart.Keys)*len(colPart.Keys))

	for _, rkey := range rowPart.Keys {
		rowRecs := rowPart.Records[rkey]
		if len(rowRecs) == 0 {
			continue
		}
		innerCol, err := p.PartitionByAxis(spec.Columns, rowRecs)
		if err != nil {
			return nil, err
		}
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
			compMap, err := buildCellComponentMap(instance, n, nNull)
			if err != nil {
				return nil, err
			}
			cellComponents[ck] = compMap
			if !val.present {
				continue
			}
			cellValues[ck] = val.value
			cellPresent[ck] = true
		}
	}

	// E3-S4: margin recompute now routes through runCellAggregation so
	// the orchestrator captures the post-Finalize aggregator instance
	// alongside the scalar/rich margin value. The instance feeds
	// buildCellComponentMap which merges the universal floor {n, n_null}
	// with the cell aggregator's MetaAggregator.Components() output for
	// the row-margin / column-margin / grand-total components emission.
	// Component maps are tracked unconditionally when the margin slot is
	// computed (NeedsRowMargin / NeedsColumnMargin / NeedsGrandMargin),
	// but only flow to Response.Components when the corresponding
	// display flag is set — mirroring the MatrixPayload.RowMargins /
	// ColumnMargins / GrandTotal emission rule (computed for
	// normalization, surfaced only on explicit request).
	var rowMargins map[string]any
	var rowMarginPresent map[string]bool
	var rowMarginCounts map[string]int
	var rowMarginComponents map[string]map[string]any
	if spec.NeedsRowMargin() {
		rowMargins = make(map[string]any, len(rowPart.Keys))
		rowMarginPresent = make(map[string]bool, len(rowPart.Keys))
		rowMarginCounts = make(map[string]int, len(rowPart.Keys))
		rowMarginComponents = make(map[string]map[string]any, len(rowPart.Keys))
		for _, rkey := range rowPart.Keys {
			bucket := rowPart.Records[rkey]
			if len(bucket) == 0 {
				continue
			}
			val, instance, n, nNull, err := p.runCellAggregation(cellAgg, bucket)
			if err != nil {
				return nil, err
			}
			rowMarginCounts[rkey] = n + nNull
			compMap, err := buildCellComponentMap(instance, n, nNull)
			if err != nil {
				return nil, err
			}
			rowMarginComponents[rkey] = compMap
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
		colMarginComponents = make(map[string]map[string]any, len(colPart.Keys))
		for _, ckey := range colPart.Keys {
			bucket := colPart.Records[ckey]
			if len(bucket) == 0 {
				continue
			}
			val, instance, n, nNull, err := p.runCellAggregation(cellAgg, bucket)
			if err != nil {
				return nil, err
			}
			colMarginCounts[ckey] = n + nNull
			compMap, err := buildCellComponentMap(instance, n, nNull)
			if err != nil {
				return nil, err
			}
			colMarginComponents[ckey] = compMap
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
		if len(filtered) > 0 {
			val, instance, n, nNull, err := p.runCellAggregation(cellAgg, filtered)
			if err != nil {
				return nil, err
			}
			grandMarginCount = n + nNull
			compMap, err := buildCellComponentMap(instance, n, nNull)
			if err != nil {
				return nil, err
			}
			grandMarginComponents = compMap
			if val.present {
				grandMargin = val.value
				grandPresent = true
			}
		}
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
			for _, pkey := range partialRowPart.Keys {
				bucket := partialRowPart.Records[pkey]
				if len(bucket) == 0 {
					continue
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
		}
	}
	if mode == types.CrosstabNormalizeColumn {
		axisLen := len(spec.Columns)
		level := spec.NormalizeLevelOrLeaf(axisLen)
		colNormLevel = level
		if level == axisLen-1 {
			partialColMargins = coerceAnyMarginMap(colMargins)
			partialColPresent = colMarginPresent
		} else {
			partialColPart, err = p.PartitionByAxis(spec.Columns[:level+1], filtered)
			if err != nil {
				return nil, err
			}
			partialColMargins = make(map[string]float64, len(partialColPart.Keys))
			partialColPresent = make(map[string]bool, len(partialColPart.Keys))
			for _, pkey := range partialColPart.Keys {
				bucket := partialColPart.Records[pkey]
				if len(bucket) == 0 {
					continue
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
		partitionAxis = append(partitionAxis, spec.Columns[:colDepth+1]...)
		joined, err := p.PartitionByAxis(partitionAxis, filtered)
		if err != nil {
			return nil, err
		}
		crossMargins = make(map[crosstabCellKey]float64, len(joined.Keys))
		for _, jkey := range joined.Keys {
			bucket := joined.Records[jkey]
			if len(bucket) == 0 {
				continue
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

	// Tier-1 row tests fold over the filter-passing record set, mirroring
	// the buffered grouped path in processRecords. Streamable tests run
	// online; buffered tests (TEST_KS, TEST_MANN_WHITNEY_U,
	// TEST_FISHER_EXACT, etc.) collect rows internally during UpdateRow
	// and decide at Finalize. Same semantics as a hand-written grouped
	// request — see skills/crosstab-guide.md "Statistical testing".
	rowTests, err := p.buildRowTests(req.Tests)
	if err != nil {
		return nil, err
	}
	for _, rt := range rowTests {
		for _, rec := range filtered {
			if err := rt.test.UpdateRow(rec); err != nil {
				return nil, err
			}
		}
	}
	testResults, err := finalizeRowTests(rowTests)
	if err != nil {
		return nil, err
	}

	resp := &types.Response{
		Metadata: &types.ResponseMetadata{
			TotalRows:    totalRows,
			FilteredRows: filteredRows,
		},
		Tests: testResults,
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

	// E3-S2: emit per-cell record-count matrix on Response.Components.
	// Layout indexed identically to MatrixPayload.Cells (rowPart.Keys
	// outer, colPart.Keys inner) so consumers can use the same (r, c) to
	// dereference cell value and cell count. Even shape=long callers see
	// the matrix because the components slot is shape-agnostic; the
	// indexing tracks rowPart / colPart key order which is deterministic
	// regardless of shape.
	excludedRecords := int(filteredRows) - includedRecords
	if excludedRecords < 0 {
		// Defensive: should never happen — includedRecords is bounded by
		// the count of records that landed in at least one cell, which is
		// at most filteredRows. Surface the divergence as zero rather
		// than emit a negative count to the wire.
		excludedRecords = 0
	}
	// E3-S4: row / column / grand-total margin counts + components emit
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
	// E3-S5: per-axis grouper components for the row + column axes. Each
	// axis grouper is instantiated fresh and exercised against the full
	// filtered set so MetaGrouper.Components() reflects the cohort-wide
	// bucket emission for that axis position. Buckets are then projected
	// onto the sorted RowKeys / ColumnKeys via projectAxisKeyComponents
	// so RowKeyComponents[r] / ColumnKeyComponents[c] are indexed in
	// lockstep with MatrixPayload.RowKeys / ColumnKeys.
	rowAxisComponents, err := p.axisComponentsFor(spec.Rows, filtered)
	if err != nil {
		return nil, err
	}
	colAxisComponents, err := p.axisComponentsFor(spec.Columns, filtered)
	if err != nil {
		return nil, err
	}
	rowKeyComponents := projectAxisKeyComponents(spec.Rows, rowPart.Tuples, rowAxisComponents)
	colKeyComponents := projectAxisKeyComponents(spec.Columns, colPart.Tuples, colAxisComponents)
	populateCrosstabComponents(resp, rowPart.Keys, colPart.Keys,
		cellCounts, cellComponents,
		rowMarginCountsSlot, rowMarginComponentsSlot,
		colMarginCountsSlot, colMarginComponentsSlot,
		grandMarginCountSlot, grandMarginComponentsSlot, spec.Margins.Grand,
		includedRecords, excludedRecords,
		rowKeyComponents, colKeyComponents)

	// Tier-2 post-tests run over the materialized cell rows. Margin rows
	// are excluded — they are emission-time annotations, not statistical
	// observations. For shape=matrix the orchestrator synthesises the
	// cell-row view; for shape=long the cell rows are already
	// addressable as the non-margin subset of resp.Data.
	postRows := buildCellRowsForPostTest(spec, rowPart, colPart, cellValues, cellPresent, cellLabel)
	postResults, err := p.runPostTests(req.PostTests, postRows)
	if err != nil {
		return nil, err
	}
	resp.PostTests = postResults

	// Overlay fold (E1-S6). When Request.Overlays is non-empty and the
	// buffered exit produced a MATRIX-shaped CrosstabResult, wrap the
	// finalised MatrixPayload in a CrosstabHostView and let
	// ApplyOverlays dispatch each spec through the per-kind runtime
	// handler (E1-S5). Result: Response.Overlays carries one
	// OverlayLayer per spec in matching order, and every
	// types.OverlayWarning is promoted to types.ResponseWarning so envelope
	// consumers see the same diagnostics surface label warnings already
	// use (see service/process_labels.go for the canonical promotion
	// shape).
	//
	// Scope:
	//
	//   - shape=long is out of scope today — the validator
	//     (descriptor.ValidateOverlays) requires a MATRIX host for
	//     INDEX_VS_MARGIN; a misconfigured spec that survived predict
	//     gets a defensive PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE-style
	//     bail without touching resp.Overlays.
	//   - Empty / nil req.Overlays leaves resp.Overlays nil — additive
	//     byte-identity preserved against the pre-overlay baseline.
	//   - The fused crosstab path (crosstab_fused.go) is intentionally
	//     deferred per the E1 scope notes.
	if err := applyOverlaysToResponse(req, resp, p.exts); err != nil {
		return nil, err
	}

	return resp, nil
}

// applyOverlaysToResponse runs the overlay fold against a finalised
// crosstab response. No-op when req.Overlays is empty (additive byte-
// identity contract); no-op when the response did not produce a MATRIX
// payload (long-shape host today — the validator already rejected this
// at predict time, so reaching here is the defense-in-depth branch).
//
// On success, resp.Overlays carries one OverlayLayer per spec in
// matching order and resp.Warnings is extended with one
// ResponseWarning per types.OverlayWarning the handlers emitted (mirrors the
// label-resolver promotion in service/process_labels.go).
//
// On unknown overlay kind, ApplyOverlays returns a PROCESSING_INTERNAL
// CodedError whose details carry the canonical
// errors.PULSE_OVERLAY_KIND_UNKNOWN code.
func applyOverlaysToResponse(req *types.Request, resp *types.Response, exts *ExtensionRegistry) error {
	if req == nil || len(req.Overlays) == 0 {
		return nil
	}
	if resp == nil || resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		// shape=long or no crosstab result — validator should have
		// already rejected this at predict time. Defense in depth:
		// quietly skip so we never panic on a nil host view.
		return nil
	}
	host := NewCrosstabHostView(resp.Crosstab.Matrix)
	// E8-S5: when the Processor carries a live ExtensionRegistry the
	// FORMULA dispatch arm of ApplyOverlaysWithExtensions threads the
	// registry's ExprFunctions into the compile-time `[]expr.Option`
	// slice so embedder-registered helpers (`pct_change`, lookup
	// builtins, etc.) are reachable from OVERLAY_FORMULA expressions.
	// Non-FORMULA kinds ignore the registry — their handlers continue
	// to use the (spec, host) signature unchanged.
	layers, warnings, err := ApplyOverlaysWithExtensions(req.Overlays, host, exts)
	if err != nil {
		return err
	}
	if len(layers) > 0 {
		resp.Overlays = layers
	}
	for _, w := range warnings {
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
			Code:    w.Code,
			Message: w.Message,
			Details: w.Details,
		})
	}
	return nil
}

// buildCellRowsForPostTest synthesises one row per present cell with
// every axis field populated and the cell label set to the (possibly
// normalized) cell value. Excludes margin annotations. Matches the
// cell-only subset of buildLongRows so a tier-2 post test sees the
// same observations regardless of the configured shape.
func buildCellRowsForPostTest(spec *types.CrosstabSpec,
	rowPart, colPart *CrosstabAxisPartition,
	cellValues map[crosstabCellKey]any,
	cellPresent map[crosstabCellKey]bool,
	cellLabel string,
) []map[string]any {
	rowFields := types.AxisFieldNames(spec.Rows)
	colFields := types.AxisFieldNames(spec.Columns)
	var out []map[string]any
	for i, rcomp := range rowPart.Keys {
		for j, ccomp := range colPart.Keys {
			key := crosstabCellKey{rcomp, ccomp}
			if !cellPresent[key] {
				continue
			}
			row := make(map[string]any, len(rowFields)+len(colFields)+1)
			for k, f := range rowFields {
				if k < len(rowPart.Tuples[i]) {
					row[f] = rowPart.Tuples[i][k]
				}
			}
			for k, f := range colFields {
				if k < len(colPart.Tuples[j]) {
					row[f] = colPart.Tuples[j][k]
				}
			}
			row[cellLabel] = cellValues[key]
			out = append(out, row)
		}
	}
	return out
}

// normalizeDenominator returns the denominator to divide a cell by under
// the configured normalization mode, plus a boolean that is false when
// the required margin is missing. The caller treats a missing margin or
// a zero denominator as null-cell (divide-by-zero policy).
func normalizeDenominator(mode types.CrosstabNormalize, rkey, ckey string,
	rowMargins map[string]float64, rowPresent map[string]bool,
	colMargins map[string]float64, colPresent map[string]bool,
	grand float64, grandPresent bool,
) (float64, bool) {
	switch mode {
	case types.CrosstabNormalizeRow:
		if !rowPresent[rkey] {
			return 0, false
		}
		return rowMargins[rkey], true
	case types.CrosstabNormalizeColumn:
		if !colPresent[ckey] {
			return 0, false
		}
		return colMargins[ckey], true
	case types.CrosstabNormalizeTotal:
		if !grandPresent {
			return 0, false
		}
		return grand, true
	}
	return 0, false
}

func buildMatrixPayload(spec *types.CrosstabSpec,
	rowPart, colPart *CrosstabAxisPartition,
	cellValues map[crosstabCellKey]any,
	cellPresent map[crosstabCellKey]bool,
	rowMargins map[string]any, rowPresent map[string]bool,
	colMargins map[string]any, colPresent map[string]bool,
	grand any, grandPresent bool,
	cellLabel string, mode types.CrosstabNormalize,
) *types.MatrixPayload {
	payload := &types.MatrixPayload{
		RowHeader: types.AxisHeader{
			Fields: types.AxisFieldNames(spec.Rows),
			Types:  types.AxisTypes(spec.Rows),
		},
		ColumnHeader: types.AxisHeader{
			Fields: types.AxisFieldNames(spec.Columns),
			Types:  types.AxisTypes(spec.Columns),
		},
		RowKeys:          append([]types.AxisKey(nil), rowPart.Tuples...),
		ColumnKeys:       append([]types.AxisKey(nil), colPart.Tuples...),
		Cells:            make([][]types.MatrixCell, len(rowPart.Keys)),
		CellLabel:        cellLabel,
		NormalizeApplied: mode,
	}
	for i, rcomp := range rowPart.Keys {
		row := make([]types.MatrixCell, len(colPart.Keys))
		for j, ccomp := range colPart.Keys {
			key := crosstabCellKey{rcomp, ccomp}
			if cellPresent[key] {
				row[j] = types.MatrixCell{Value: cellValues[key], Present: true}
			}
		}
		payload.Cells[i] = row
	}

	if spec.Margins.Rows {
		payload.RowMargins = make([]types.MatrixCell, len(rowPart.Keys))
		for i, rcomp := range rowPart.Keys {
			if rowPresent[rcomp] {
				payload.RowMargins[i] = types.MatrixCell{Value: rowMargins[rcomp], Present: true}
			}
		}
	}
	if spec.Margins.Columns {
		payload.ColumnMargins = make([]types.MatrixCell, len(colPart.Keys))
		for i, ccomp := range colPart.Keys {
			if colPresent[ccomp] {
				payload.ColumnMargins[i] = types.MatrixCell{Value: colMargins[ccomp], Present: true}
			}
		}
	}
	if spec.Margins.Grand && grandPresent {
		payload.GrandTotal = types.MatrixCell{Value: grand, Present: true}
	}
	return payload
}

func buildLongRows(spec *types.CrosstabSpec,
	rowPart, colPart *CrosstabAxisPartition,
	cellValues map[crosstabCellKey]any,
	cellPresent map[crosstabCellKey]bool,
	rowMargins map[string]any, rowPresent map[string]bool,
	colMargins map[string]any, colPresent map[string]bool,
	grand any, grandPresent bool,
	cellLabel string,
	partialRowPart *CrosstabAxisPartition,
	partialRowMargins map[string]float64, partialRowPresent map[string]bool,
	rowNormLevel int,
	partialColPart *CrosstabAxisPartition,
	partialColMargins map[string]float64, partialColPresent map[string]bool,
	colNormLevel int,
) []map[string]any {
	rowFields := types.AxisFieldNames(spec.Rows)
	colFields := types.AxisFieldNames(spec.Columns)
	var data []map[string]any
	for i, rcomp := range rowPart.Keys {
		for j, ccomp := range colPart.Keys {
			key := crosstabCellKey{rcomp, ccomp}
			if !cellPresent[key] {
				continue
			}
			row := make(map[string]any, len(rowFields)+len(colFields)+1)
			for k, f := range rowFields {
				if k < len(rowPart.Tuples[i]) {
					row[f] = rowPart.Tuples[i][k]
				}
			}
			for k, f := range colFields {
				if k < len(colPart.Tuples[j]) {
					row[f] = colPart.Tuples[j][k]
				}
			}
			row[cellLabel] = cellValues[key]
			data = append(data, row)
		}
	}
	if spec.Margins.Rows {
		for i, rcomp := range rowPart.Keys {
			if !rowPresent[rcomp] {
				continue
			}
			row := make(map[string]any, len(rowFields)+2)
			for k, f := range rowFields {
				if k < len(rowPart.Tuples[i]) {
					row[f] = rowPart.Tuples[i][k]
				}
			}
			row[cellLabel] = rowMargins[rcomp]
			row["_margin"] = "row"
			data = append(data, row)
		}
	}
	if spec.Margins.Columns {
		for j, ccomp := range colPart.Keys {
			if !colPresent[ccomp] {
				continue
			}
			row := make(map[string]any, len(colFields)+2)
			for k, f := range colFields {
				if k < len(colPart.Tuples[j]) {
					row[f] = colPart.Tuples[j][k]
				}
			}
			row[cellLabel] = colMargins[ccomp]
			row["_margin"] = "column"
			data = append(data, row)
		}
	}
	if spec.Margins.Grand && grandPresent {
		data = append(data, map[string]any{
			cellLabel: grand,
			"_margin": "grand",
		})
	}
	// Partial-depth margin rows. Emitted only when the corresponding
	// display flag is set, the normalization axis matches the margin
	// surface, and the selected level is shallower than the leaf. The
	// leaf-level rows above are unchanged; partial rows ride alongside
	// them so consumers see both denominators in one pass.
	if spec.Margins.Rows && partialRowPart != nil && rowNormLevel >= 0 && rowNormLevel < len(spec.Rows)-1 {
		tag := fmt.Sprintf("row_at_%d", rowNormLevel)
		for i, pcomp := range partialRowPart.Keys {
			if !partialRowPresent[pcomp] {
				continue
			}
			row := make(map[string]any, len(rowFields)+2)
			tuple := partialRowPart.Tuples[i]
			for k := 0; k < len(rowFields) && k < len(tuple); k++ {
				row[rowFields[k]] = tuple[k]
			}
			row[cellLabel] = partialRowMargins[pcomp]
			row["_margin"] = tag
			data = append(data, row)
		}
	}
	if spec.Margins.Columns && partialColPart != nil && colNormLevel >= 0 && colNormLevel < len(spec.Columns)-1 {
		tag := fmt.Sprintf("column_at_%d", colNormLevel)
		for j, pcomp := range partialColPart.Keys {
			if !partialColPresent[pcomp] {
				continue
			}
			row := make(map[string]any, len(colFields)+2)
			tuple := partialColPart.Tuples[j]
			for k := 0; k < len(colFields) && k < len(tuple); k++ {
				row[colFields[k]] = tuple[k]
			}
			row[cellLabel] = partialColMargins[pcomp]
			row["_margin"] = tag
			data = append(data, row)
		}
	}
	return data
}

// populateCrosstabComponents writes the per-cell record-count matrix
// and the per-cell components-map matrix onto
// Response.Components.Crosstab in the same (r, c) layout as
// MatrixPayload.Cells. Allocates Response.Components and
// Response.Components.Crosstab when nil so the buffered and fused
// crosstab paths can call this helper unconditionally without
// duplicating slot-init plumbing.
//
// Shared with the fused path so byte-equal parity is structural — both
// paths build the matrices from the same {(rowKey, colKey) → count}
// and {(rowKey, colKey) → component map} maps against the same sorted
// axis-key slices.
//
// cellComponents may be nil — in that case the CellComponents matrix is
// left unpopulated (no E3-S3 emission for older callers). When non-nil,
// every entry corresponds to a cell that received at least one record
// (no empty cells in the map); a missing (r, c) yields a nil entry in
// the emitted matrix so consumers can distinguish empty cells from
// populated cells with an empty component map.
//
// E3-S4: rowMarginCounts / rowMarginComponents (and column / grand
// analogues) are emitted only when the caller passes non-nil
// maps — gated by the display flag on the buffered / fused path. The
// emission rule mirrors MatrixPayload.RowMargins / ColumnMargins /
// GrandTotal: margins computed for normalization but not displayed
// stay off the wire. grandHasMargin distinguishes "grand margin
// requested with zero count" from "grand margin not requested"; the
// other margins use the nil-map sentinel because per-axis maps are
// allocated only when display is on.
func populateCrosstabComponents(resp *types.Response,
	rowKeys, colKeys []string,
	cellCounts map[crosstabCellKey]int,
	cellComponents map[crosstabCellKey]map[string]any,
	rowMarginCounts map[string]int,
	rowMarginComponents map[string]map[string]any,
	colMarginCounts map[string]int,
	colMarginComponents map[string]map[string]any,
	grandMarginCount int,
	grandMarginComponents map[string]any,
	grandHasMargin bool,
	includedRecords, excludedRecords int,
	rowKeyComponents []map[string]any,
	columnKeyComponents []map[string]any,
) {
	if resp == nil {
		return
	}
	if resp.Components == nil {
		resp.Components = &types.ResponseComponents{}
	}
	if resp.Components.Crosstab == nil {
		resp.Components.Crosstab = &types.CrosstabComponents{}
	}
	ct := resp.Components.Crosstab

	// Always allocate the cell-count matrix so consumers can dereference
	// CellCounts[r][c] by axis index without worrying about ragged rows
	// (omitempty drops the slice when both dims are zero, but a 2x3
	// crosstab with no surviving cells still emits an all-zero matrix —
	// matches the MatrixPayload.Cells contract that the row outer slice
	// is always sized to len(rowKeys)).
	if len(rowKeys) > 0 && len(colKeys) > 0 {
		matrix := make([][]int, len(rowKeys))
		for i, rk := range rowKeys {
			row := make([]int, len(colKeys))
			for j, ck := range colKeys {
				if n, ok := cellCounts[crosstabCellKey{rk, ck}]; ok {
					row[j] = n
				}
			}
			matrix[i] = row
		}
		ct.CellCounts = matrix

		// E3-S3: per-cell components matrix indexed identically to
		// CellCounts. Empty cells (no entry in cellComponents) emit nil
		// at [r][c] — consumers can dereference both matrices with the
		// same (r, c) pair, distinguishing "no data" (nil) from "data
		// with empty operator payload" (non-nil map with floor only).
		// The outer matrix is allocated only when cellComponents is
		// non-nil so the legacy E3-S2 callsites that pre-date
		// CellComponents emit a CellCounts-only payload.
		if cellComponents != nil {
			compMatrix := make([][]map[string]any, len(rowKeys))
			for i, rk := range rowKeys {
				row := make([]map[string]any, len(colKeys))
				for j, ck := range colKeys {
					if m, ok := cellComponents[crosstabCellKey{rk, ck}]; ok {
						row[j] = m
					}
				}
				compMatrix[i] = row
			}
			ct.CellComponents = compMatrix
		}
	}

	// E3-S4: row-margin counts + components, indexed in rowKeys order.
	// Emitted only when the caller passes a non-nil map (display flag
	// gate). Per-axis vectors are sized to len(rowKeys) so consumers can
	// dereference RowMarginCounts[r] / RowMarginComponents[r] by the
	// same r index they use against MatrixPayload.RowKeys[r] and
	// MatrixPayload.RowMargins[r]; missing entries (no records for the
	// row key) emit 0 / nil in the parallel slot.
	if rowMarginCounts != nil && len(rowKeys) > 0 {
		counts := make([]int, len(rowKeys))
		for i, rk := range rowKeys {
			if n, ok := rowMarginCounts[rk]; ok {
				counts[i] = n
			}
		}
		ct.RowMarginCounts = counts
	}
	if rowMarginComponents != nil && len(rowKeys) > 0 {
		comps := make([]map[string]any, len(rowKeys))
		for i, rk := range rowKeys {
			if m, ok := rowMarginComponents[rk]; ok {
				comps[i] = m
			}
		}
		ct.RowMarginComponents = comps
	}
	if colMarginCounts != nil && len(colKeys) > 0 {
		counts := make([]int, len(colKeys))
		for i, ck := range colKeys {
			if n, ok := colMarginCounts[ck]; ok {
				counts[i] = n
			}
		}
		ct.ColumnMarginCounts = counts
	}
	if colMarginComponents != nil && len(colKeys) > 0 {
		comps := make([]map[string]any, len(colKeys))
		for i, ck := range colKeys {
			if m, ok := colMarginComponents[ck]; ok {
				comps[i] = m
			}
		}
		ct.ColumnMarginComponents = comps
	}
	if grandHasMargin {
		ct.GrandTotalCount = grandMarginCount
		ct.GrandTotalComponents = grandMarginComponents
	}
	// E3-S5: per-axis grouper components projected onto sorted axis-key
	// order. Single-axis crosstabs surface the bucket map directly; multi-
	// axis crosstabs wrap each axis position's bucket in an "axes" slice
	// keyed by the axis field name (see projectAxisKeyComponents). Vector
	// length matches MatrixPayload.RowKeys / ColumnKeys so consumers can
	// dereference RowKeyComponents[r] / ColumnKeyComponents[c] in lockstep
	// with the matrix coordinates.
	if len(rowKeyComponents) > 0 {
		ct.RowKeyComponents = rowKeyComponents
	}
	if len(columnKeyComponents) > 0 {
		ct.ColumnKeyComponents = columnKeyComponents
	}
	ct.IncludedRecords = includedRecords
	ct.ExcludedRecords = excludedRecords
}

// cellAggregationResult carries the per-cell scalar/rich value and a
// present flag distinguishing "aggregator finalised a value" from
// "aggregator returned an empty row" (the buffered path treated the
// latter as a skipped cell pre-E3-S3, which still holds here — we
// surface presence via the flag).
type cellAggregationResult struct {
	value   any
	present bool
}

// runCellAggregation runs a single cell aggregator against a bucket and
// returns the rich/scalar value, the post-Finalize aggregator instance
// (for MetaAggregator emission), and the per-cell universal-floor
// counters (n = non-null contributing rows, nNull = null inputs the
// aggregator skipped). The aggregator instance is exposed so the
// caller can interrogate MetaAggregator without re-running the
// pipeline.
//
// Mirrors the slot-handling subset of aggregateWithComponents — same
// decimal dispatch, same factory lookup, same value/rich dispatch —
// scoped to one slot. Distinct entry point so the crosstab cell
// builder can capture the aggregator instance after Aggregate
// returns, which the ungrouped aggregateWithComponents wraps inside
// its closure.
//
// The empty-bucket case is rejected by the caller — bucket is always
// non-empty when this is invoked from the cell loop, so we never
// fall through to the "no rows seen" floor.
func (p *Processor) runCellAggregation(slot *types.Aggregation, bucket []*Record) (cellAggregationResult, any, int, int, error) {
	// Per-cell {n, n_null} for the cell field. Walked over the bucket
	// once before / alongside Aggregate (the floorFor helper used by
	// aggregateWithComponents tilts the same way, but per-field rather
	// than per-bucket). Cheap: one NumericValue probe per record.
	var n, nNull int
	for _, r := range bucket {
		if _, ok := r.NumericValue(slot.Field); ok {
			n++
		} else {
			nNull++
		}
	}

	// Decimal128 dispatch — same gate as aggregateWithComponents.
	if p.schema != nil {
		if f := p.schema.Field(slot.Field); f != nil && f.Type.IsDecimal() {
			if !IsDecimalAggregationSupported(slot.Type) {
				return cellAggregationResult{}, nil, 0, 0, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					"aggregation has no decimal128 implementation",
					map[string]any{"aggregation": string(slot.Type), "field": slot.Field})
			}
			out, err := AggregateDecimalField(slot.Type, bucket, slot.Field, f.Scale)
			if err != nil {
				return cellAggregationResult{}, nil, 0, 0, err
			}
			// Decimal aggregators do not implement MetaAggregator — the
			// orchestrator's floor-only entry is the full components
			// payload (mirrors the ungrouped buffered path).
			return cellAggregationResult{value: out, present: true}, nil, n, nNull, nil
		}
	}

	factory, ok := p.exts.LookupAggregator(slot.Type)
	if !ok {
		return cellAggregationResult{}, nil, 0, 0, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown aggregation type: %s", slot.Type))
	}
	aggregator, err := factory(slot, p.schema)
	if err != nil {
		return cellAggregationResult{}, nil, 0, 0, err
	}
	scalar, err := aggregator.Aggregate(bucket, slot.Field)
	if err != nil {
		return cellAggregationResult{}, nil, 0, 0, err
	}
	v, err := dispatchAggregatorCellResult(aggregator, scalar)
	if err != nil {
		return cellAggregationResult{}, nil, 0, 0, err
	}
	return cellAggregationResult{value: v, present: true}, aggregator, n, nNull, nil
}

// buildCellComponentMap merges the per-cell universal-floor counters
// {n, n_null} with the operator-specific component keys emitted by the
// cell aggregator's MetaAggregator.Components() implementation. The
// returned map is pre-sized by `2 + len(operator-keys)` so the in-place
// merge never rehashes; an aggregator without MetaAggregator (or one
// returning a nil operator map) yields the floor alone.
//
// The orchestrator owns the floor: cell aggregators MUST NOT re-emit
// `n` or `n_null` from their Components() implementation. This helper
// is the single point of merge so the contract is structural — any
// operator key shadowing the floor would land here and be quietly
// overwritten by the floor pass, surfacing as a test divergence.
//
// Returns a non-nil error only when the underlying MetaAggregator
// emission returns one — same dispatch contract as
// buildAggregationComponents in the ungrouped path.
func buildCellComponentMap(instance any, n, nNull int) (map[string]any, error) {
	var operator map[string]any
	if meta, ok := instance.(MetaAggregator); ok {
		op, err := meta.Components()
		if err != nil {
			return nil, err
		}
		operator = op
	}
	out := make(map[string]any, 2+len(operator))
	out["n"] = n
	out["n_null"] = nNull
	for k, v := range operator {
		out[k] = v
	}
	return out, nil
}

// coerceAnyMarginMap converts a map[string]any margin map (the post-
// widening shape) to a map[string]float64 for the partial-normalization
// paths. Only invoked after the MapValued() gate has rejected map cells,
// so every entry is guaranteed scalar — non-numeric entries coerce to
// zero via coerceFloat64.
func coerceAnyMarginMap(in map[string]any) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = coerceFloat64(v)
	}
	return out
}

func coerceFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case uint64:
		return float64(x)
	case uint32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

// validateCrosstabSpec checks the structural invariants of a CrosstabSpec
// at orchestrator entry. Schema-level checks (field existence, type
// compatibility) live in descriptor.ValidateCrosstab and run earlier in
// the predict path.
func validateCrosstabSpec(spec *types.CrosstabSpec, req *types.Request) error {
	if spec == nil {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"validateCrosstabSpec called with nil spec")
	}
	if len(spec.Rows) == 0 {
		return errors.NewCodedError(errors.PULSE_CROSSTAB_EMPTY_ROWS,
			"crosstab requires at least one row-axis grouper")
	}
	if len(spec.Columns) == 0 {
		return errors.NewCodedError(errors.PULSE_CROSSTAB_EMPTY_COLUMNS,
			"crosstab requires at least one column-axis grouper")
	}
	if spec.Cell == nil {
		return errors.NewCodedError(errors.PULSE_CROSSTAB_MISSING_CELL,
			"crosstab requires a Cell aggregation")
	}
	if req != nil && (len(req.Groups) > 0 || len(req.Aggregations) > 0) {
		return errors.NewCodedError(errors.PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS,
			"crosstab cannot coexist with top-level groups or aggregations on the same request")
	}
	if !types.IsValidNormalize(spec.Normalize) {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown crosstab normalize mode: %q", spec.Normalize),
			map[string]any{"normalize": string(spec.Normalize)})
	}
	if !types.IsValidShape(spec.Shape) {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown crosstab shape: %q", spec.Shape),
			map[string]any{"shape": string(spec.Shape)})
	}
	if spec.NormalizeLevel != nil {
		switch spec.NormalizeOrDefault() {
		case types.CrosstabNormalizeNone:
			return errors.NewCodedErrorWithDetails(errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS,
				"crosstab normalize_level requires Normalize to be row or column",
				map[string]any{"normalize_level": *spec.NormalizeLevel})
		case types.CrosstabNormalizeTotal:
			return errors.NewCodedErrorWithDetails(errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE,
				"crosstab normalize_level cannot be combined with Normalize=total",
				map[string]any{"normalize_level": *spec.NormalizeLevel})
		case types.CrosstabNormalizeRow:
			if lvl := *spec.NormalizeLevel; lvl < 0 || lvl >= len(spec.Rows) {
				return errors.NewCodedErrorWithDetails(errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE,
					"crosstab normalize_level is out of range for the row axis",
					map[string]any{"normalize_level": lvl, "axis": "rows", "axis_len": len(spec.Rows)})
			}
		case types.CrosstabNormalizeColumn:
			if lvl := *spec.NormalizeLevel; lvl < 0 || lvl >= len(spec.Columns) {
				return errors.NewCodedErrorWithDetails(errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE,
					"crosstab normalize_level is out of range for the column axis",
					map[string]any{"normalize_level": lvl, "axis": "columns", "axis_len": len(spec.Columns)})
			}
		}
	}
	if spec.NormalizeWithin != nil {
		switch spec.NormalizeOrDefault() {
		case types.CrosstabNormalizeNone:
			return errors.NewCodedErrorWithDetails(errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS,
				"crosstab normalize_within requires Normalize to be row or column",
				map[string]any{"normalize_within": *spec.NormalizeWithin})
		case types.CrosstabNormalizeTotal:
			return errors.NewCodedErrorWithDetails(errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE,
				"crosstab normalize_within cannot be combined with Normalize=total",
				map[string]any{"normalize_within": *spec.NormalizeWithin})
		case types.CrosstabNormalizeRow:
			if w := *spec.NormalizeWithin; w < 0 || w >= len(spec.Columns) {
				return errors.NewCodedErrorWithDetails(errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE,
					"crosstab normalize_within is out of range for the column axis",
					map[string]any{"normalize_within": w, "axis": "columns", "axis_len": len(spec.Columns)})
			}
		case types.CrosstabNormalizeColumn:
			if w := *spec.NormalizeWithin; w < 0 || w >= len(spec.Rows) {
				return errors.NewCodedErrorWithDetails(errors.PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE,
					"crosstab normalize_within is out of range for the row axis",
					map[string]any{"normalize_within": w, "axis": "rows", "axis_len": len(spec.Rows)})
			}
		}
	}
	// Internal guard: every aggregator must carry a reducibility
	// classification. The default branch returns MarginRecompute, so
	// reaching unclassified means a new aggregator was added without
	// updating AggregationType.MarginReducibility — fail fast so the
	// missing classification is fixed.
	switch spec.Cell.Type.MarginReducibility() {
	case types.MarginSummable, types.MarginMeanReducible, types.MarginRecompute:
		// classified
	default:
		return errors.NewCodedErrorWithDetails(errors.PULSE_CROSSTAB_AGG_UNCLASSIFIED,
			"crosstab cell aggregator has no MarginReducibility classification",
			map[string]any{"aggregation": string(spec.Cell.Type)})
	}
	return nil
}
