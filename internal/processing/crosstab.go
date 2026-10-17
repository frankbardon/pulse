package processing

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/vectors"
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
	return p.partitionByAxis(axis, records, nil, nil)
}

// partitionByAxis is PartitionByAxis that, when warns is non-nil,
// collects the warnings of every params.fit "within" refit it runs
// (refitWarnings), each stamped with its outer segment: outer (the
// tuple of the composite positions before axis[0] — empty for the row
// axis, the row tuple for a per-row-bucket column partition) plus this
// axis's own prefix. Only RunCrosstab's two cell partitions collect, so
// each outer bucket's refit is reported once; the margin, normalisation
// and component passes refit too but stay silent.
func (p *Processor) partitionByAxis(axis []*types.Group, records []*Record, outer types.AxisKey, warns *[]*types.ResponseWarning) (*CrosstabAxisPartition, error) {
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
	ApplyGrouperExtensions(grouper, p.exts)
	var buckets map[string][]*Record
	order := buildIncludeFilter(grp.Include)
	if ws := p.wholeSetAxes[grp]; ws != nil && ws.route {
		// A whole-set axis (GROUP_QUANTILE / GROUP_KMEANS under
		// params.fit "global") was fitted once over every filtered
		// record (fitWholeSetAxes); this level only routes its records
		// by that assignment, so a nested position — and the
		// per-row-bucket column partition — never refits a subset.
		buckets = ws.partition(records)
		if ws.order != nil {
			order = ws.order
		}
	} else {
		if buckets, err = grouper.Group(records, grp.Field); err != nil {
			return nil, err
		}
		if eo, ok := grouper.(emissionOrderer); ok {
			order = eo.emissionOrder()
		}
		if warns != nil {
			*warns = append(*warns, refitWarnings(grouper, outer)...)
		}
	}

	headKeys := make([]string, 0, len(buckets))
	for k := range buckets {
		headKeys = append(headKeys, k)
	}
	// Per-axis-position ordering: this recursion level owns exactly one
	// axis position (grp == axis[0]). Order its present head keys by that
	// position's own include list when supplied, else alphabetically —
	// the no-include path funnels through sort.Strings inside
	// orderKeysByInclude, preserving byte-identity. Each deeper recursion
	// level orders its own position independently, so per-axis
	// independence falls out of the recursion for free.
	headKeys = orderKeysByInclude(order, headKeys)

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
		var childOuter types.AxisKey
		if warns != nil {
			childOuter = append(append(make(types.AxisKey, 0, len(outer)+1), outer...), k)
		}
		child, err := p.partitionByAxis(axis[1:], buckets[k], childOuter, warns)
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

// orderCompositeKeysByAxisInclude orders a flat slice of composite axis
// keys by tuple position, honoring each axis position's include list.
//
// Decomposition contract (so the buffered PartitionByAxis recursion and
// the fused Finalize path agree byte-for-byte):
//
//   - keys[i] is the composite key CompositeAxisKey(component-strings of
//     tuples[i]) — i.e. keys and tuples are PARALLEL, one entry per axis
//     tuple, in any input order.
//   - tuples[i] is the per-position component vector for keys[i]; each
//     element is the string a grouper emitted at that axis position
//     (types.AxisKey elements are `any` but always string per the
//     Grouper.Group contract). len(tuples[i]) == len(axis).
//   - axis[p] is the *types.Group at position p; axis[p].Include drives
//     the position-p ordering. When Include is empty/nil that position
//     sorts alphabetically (the byte-identity no-include path, funneled
//     through orderKeysByInclude / sort.Strings).
//
// Ordering is lexicographic across positions: position 0 is the primary
// sort key, position 1 breaks ties within a position-0 bucket, etc.
// Within each position, member keys sort by their include index (first
// occurrence) and non-members / include-less positions sort
// alphabetically. This is exactly the ordering PartitionByAxis's
// per-level orderKeysByInclude produces, so a fused caller that decomposes
// its rowKeys/colKeys into (keys, tuples) reuses this helper to match the
// buffered output without re-deriving the recursion.
//
// keys is not mutated — a fresh ordered slice is returned. A nil / empty
// axis (or fewer axis positions than tuple components) falls back to
// sort.Strings on whatever positions remain unconstrained. Entries whose
// tuple is shorter than the axis are ordered by the positions they do
// carry, then alphabetically by composite key as a final defensive tie-
// break.
func orderCompositeKeysByAxisInclude(keys []string, tuples []types.AxisKey, axis []*types.Group) []string {
	out := make([]string, len(keys))
	copy(out, keys)
	if len(keys) != len(tuples) {
		// Parallel-slice contract violated — fall back to deterministic
		// alphabetical ordering rather than index a mismatched tuple.
		sort.Strings(out)
		return out
	}

	// Pre-build one include filter per axis position. A nil filter
	// (no Include) makes that position alphabetical via the same
	// orderKeysByInclude comparator the single-axis path uses.
	filters := make([]*includeFilter, len(axis))
	for p, grp := range axis {
		filters[p] = buildIncludeFilter(grp.Include)
	}

	// Track each key's parallel tuple so the comparator can read
	// per-position components after the stable sort permutes out.
	tupleOf := make(map[string]types.AxisKey, len(keys))
	for i, k := range keys {
		tupleOf[k] = tuples[i]
	}
	return sortCompositeKeys(out, tupleOf, filters)
}

// orderCompositeKeys is orderCompositeKeysByAxisInclude over explicit
// per-position filters (filters[p] orders position p; nil is
// alphabetical): the multi-grouper Request.Groups arms pass each
// grouper's native order (includeFilterOf). keys is not mutated.
func orderCompositeKeys(keys []string, tupleOf map[string]types.AxisKey, filters []*includeFilter) []string {
	out := make([]string, len(keys))
	copy(out, keys)
	return sortCompositeKeys(out, tupleOf, filters)
}

// sortCompositeKeys sorts out in place lexicographically by tuple
// position (see orderCompositeKeysByAxisInclude) and returns it.
func sortCompositeKeys(out []string, tupleOf map[string]types.AxisKey, filters []*includeFilter) []string {

	component := func(tup types.AxisKey, p int) (string, bool) {
		if p >= len(tup) {
			return "", false
		}
		s, ok := tup[p].(string)
		return s, ok
	}

	sort.SliceStable(out, func(a, b int) bool {
		ta, tb := tupleOf[out[a]], tupleOf[out[b]]
		for p := 0; p < len(filters); p++ {
			ca, aok := component(ta, p)
			cb, bok := component(tb, p)
			switch {
			case !aok && !bok:
				continue
			case aok != bok:
				// The tuple that carries this position sorts first.
				return aok
			}
			if ca == cb {
				continue
			}
			f := filters[p]
			ia, iamem := f.index(ca)
			ib, ibmem := f.index(cb)
			switch {
			case iamem && ibmem:
				return ia < ib
			case iamem != ibmem:
				// members sort before non-member stragglers
				return iamem
			default:
				// include-less position or both non-members: alphabetical
				return ca < cb
			}
		}
		// All axis positions tie — defensive alphabetical composite-key
		// tie-break for stability.
		return out[a] < out[b]
	})
	return out
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
		ApplyGrouperExtensions(grouper, p.exts)
		if ws := p.wholeSetAxes[grp]; ws != nil {
			// Whole-set fit (a "within" position's too: its Components
			// describe the whole-set fit, as on plain groups).
			out[i] = ws.components
			continue
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
// here (handled in internal/service/crosstab.go).
func (p *Processor) RunCrosstab(_ context.Context, req *types.Request, records []*Record) (*types.Response, error) {
	if req == nil || req.Crosstab == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"RunCrosstab requires a Crosstab spec")
	}
	// The cell and margin aggregators take their applied weight off
	// the stamped spec, so cells, every margin, the normalization
	// denominators and the auxiliaries are all weighted from raw rows;
	// counts stay raw (.claude/reference/weighting.md "Crosstab").
	req = p.prepare(req)
	spec := req.Crosstab
	if err := validateCrosstabSpec(spec, req, p.exts); err != nil {
		return nil, err
	}
	// Map-valued aggregators (AGG_SET_FREQUENCY today) cannot normalize —
	// dividing one map by another is undefined. Reject up-front so the
	// dispatch loop below can treat every scalar/rich routing as safe.
	for _, cell := range spec.CellAggregations() {
		if spec.NormalizeOrDefault() != types.CrosstabNormalizeNone && cell.Type.MapValued() {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PULSE_CROSSTAB_NORMALIZE_MAP_VALUED,
				"crosstab normalize="+string(spec.NormalizeOrDefault())+
					" is incompatible with map-valued cell aggregator "+
					string(cell.Type),
				map[string]any{
					"aggregation": string(cell.Type),
					"normalize":   string(spec.NormalizeOrDefault()),
				})
		}
	}

	if err := p.applyFeatures(req.Features, records); err != nil {
		return nil, err
	}
	filtered, err := p.applyFilters(req.Filterers, records)
	if err != nil {
		return nil, err
	}
	attrWarnings, attrComps, err := p.applyAttributes(req.Attributes, filtered)
	if err != nil {
		return nil, err
	}
	// Whole-set axis groupers (GROUP_QUANTILE / GROUP_KMEANS under
	// params.fit "global") fit once over the filtered set before any
	// partition; their warnings join the attribute pass's.
	axisWarnings, err := p.fitWholeSetAxes(spec, filtered)
	if err != nil {
		return nil, err
	}
	defer func() { p.wholeSetAxes = nil }()
	attrWarnings = append(attrWarnings, axisWarnings...)

	totalRows := int64(len(records))
	filteredRows := int64(len(filtered))

	// refitWarns collects the params.fit "within" refits of the two
	// cell partitions (rows over the filtered set, columns per row
	// bucket), each naming its outer segment — the plain-groups set.
	var refitWarns []*types.ResponseWarning
	rowPart, err := p.partitionByAxis(spec.Rows, filtered, types.AxisKey{}, &refitWarns)
	if err != nil {
		return nil, err
	}
	colPart, err := p.PartitionByAxis(spec.Columns, filtered)
	if err != nil {
		return nil, err
	}
	// The implicit Total column (no column grouper): the empty-axis
	// partition is one bucket under composite key "" holding every
	// filtered record, which is exactly the Total column. Only its tuple
	// is relabelled — the composite key stays "" so the per-row column
	// partition below, the margins and the normalize lookups keep
	// addressing it unchanged. The fused keyer emits the same pair.
	if spec.HasImplicitTotalColumn() {
		colPart.Tuples = []types.AxisKey{{types.CrosstabTotalColumnKey}}
	}
	// MaxCrosstabCells: the observed grid is rows x cols, refused before
	// the per-cell maps below preallocate it. The fused arm checks the
	// same product at its axis-key interners.
	if err := limits.Check(p.limits, limits.MaxCrosstabCells, int64(len(rowPart.Keys))*int64(len(colPart.Keys))); err != nil {
		return nil, err
	}

	// The per-row column partitions are measure-independent: computed
	// once, before any measure, so a params.fit "within" refit warns
	// once and every measure reads the same buckets.
	innerCols := make([]*CrosstabAxisPartition, len(rowPart.Keys))
	for ri, rkey := range rowPart.Keys {
		rowRecs := rowPart.Records[rkey]
		if len(rowRecs) == 0 {
			continue
		}
		innerCol, err := p.partitionByAxis(spec.Columns, rowRecs, rowPart.Tuples[ri], &refitWarns)
		if err != nil {
			return nil, err
		}
		innerCols[ri] = innerCol
	}
	// The own-margin admission sets (see runCrosstabMeasure) are axis
	// facts, shared by every measure.
	rowMember, colMember := ownMarginMembership(rowPart, colPart, len(filtered))

	// Per-axis grouper components, measure-independent too: computed
	// once when the plan builds components.crosstab.
	var rowKeyComponents, colKeyComponents []map[string]any
	if p.compute.Crosstab {
		// Per-axis grouper components for the row + column axes. Each
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
		rowKeyComponents = projectAxisKeyComponents(spec.Rows, rowPart.Tuples, rowAxisComponents)
		colKeyComponents = projectAxisKeyComponents(spec.Columns, colPart.Tuples, colAxisComponents)
	}

	// One run per measure: the single Cell, or each Cells entry as the
	// single-measure spec it runs as (types.CrosstabSpec.MeasureSpec).
	// Every run shares the partitions above, so the measures' axis keys
	// are identical by construction — the property foldCrosstabMeasures
	// stitches on.
	in := crosstabMeasureInput{
		req: req, filtered: filtered, filteredRows: filteredRows,
		rowPart: rowPart, colPart: colPart, innerCols: innerCols,
		rowMember: rowMember, colMember: colMember,
		rowKeyComponents: rowKeyComponents, colKeyComponents: colKeyComponents,
	}
	runs := make([]*crosstabMeasureRun, 0, max(1, len(spec.Cells)))
	for _, ms := range crosstabMeasureSpecs(spec) {
		run, err := p.runCrosstabMeasure(ms, in)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}

	// Tier-1 row tests fold over the filter-passing record set, mirroring
	// the buffered grouped path in processRecords. Streamable tests run
	// online; buffered tests (TEST_KS, TEST_MANN_WHITNEY_U,
	// TEST_FISHER_EXACT, etc.) collect rows internally during UpdateRow
	// and decide at Finalize. Same semantics as a hand-written grouped
	// request — see skills/crosstab-guide.md "Statistical testing".
	rowTests, err := p.buildRowTests(req)
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
	testResults, err := finalizeRowTests(rowTests, len(req.Tests))
	if err != nil {
		return nil, err
	}

	resp := &types.Response{
		Metadata: &types.ResponseMetadata{
			TotalRows:    totalRows,
			FilteredRows: filteredRows,
		},
		Tests: testResults,
		// Fit warnings from the attribute pass (declared order).
		Warnings: append(attrWarnings, refitWarns...),
	}

	// The measures' payloads, components and post-test rows: the one
	// run itself on the single-measure form (byte-identical to the
	// pre-Cells wire), else the MEASURE fold.
	folded, err := foldCrosstabMeasures(spec, runs)
	if err != nil {
		return nil, err
	}
	resp.Crosstab = folded.result
	resp.Data = folded.long
	if folded.comps != nil {
		if resp.Components == nil {
			resp.Components = &types.ResponseComponents{}
		}
		resp.Components.Crosstab = folded.comps
	}

	// Tier-2 post-tests run over the materialized cell rows. Margin rows
	// are excluded — they are emission-time annotations, not statistical
	// observations. For shape=matrix the orchestrator synthesises the
	// cell-row view; for shape=long the cell rows are already
	// addressable as the non-margin subset of resp.Data.
	postRows := folded.postRows
	postResults, _, err := p.runPostTests(req.PostTests, nil, postRows)
	if err != nil {
		return nil, err
	}
	resp.PostTests = postResults

	// Overlay fold. When Request.Overlays is non-empty and the
	// buffered exit produced a MATRIX-shaped CrosstabResult, wrap the
	// finalised MatrixPayload in a CrosstabHostView and let
	// applyOverlays dispatch each spec through the per-kind runtime
	// handler. Result: Response.Overlays carries one
	// OverlayLayer per spec in matching order, and every
	// types.OverlayWarning is promoted to types.ResponseWarning so envelope
	// consumers see the same diagnostics surface label warnings already
	// use (see internal/service/process_labels.go for the canonical promotion
	// shape).
	//
	// Scope:
	//
	//   - shape=long is out of scope today — the validator
	//     (internal/descriptor.ValidateOverlays) requires a MATRIX host for
	//     INDEX_VS_MARGIN; a misconfigured spec that survived predict
	//     gets a defensive PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE-style
	//     bail without touching resp.Overlays.
	//   - Empty / nil req.Overlays leaves resp.Overlays nil — additive
	//     byte-identity preserved against the pre-overlay baseline.
	//   - The fused crosstab path (crosstab_fused_run.go
	//     RunCrosstabFused) calls this same hook on the response
	//     FusedCrosstabState.Finalize returns, so an overlay-carrying
	//     crosstab no longer forces the buffered path and both paths
	//     emit identical Response.Overlays / Response.Warnings.
	if err := applyOverlaysToResponse(req, resp, p.exts, p.compute, folded.bases, folded.measureHosts); err != nil {
		return nil, err
	}
	// Attribute slots' Components entries (cohort-wide; the fused arm
	// attaches them at the same point).
	attachAttributeComponents(resp, attrComps)

	// One PULSE_WEIGHT_INVALID_ROWS warning per weight field the cell or
	// an auxiliary margin reads, over the filter-passing rows (after
	// attributes) — the same rows the fused arm observes.
	weights := NewWeightRowTally(req)
	weights.ObserveAll(filtered)
	if err := weights.Apply(resp, p.strictWeights); err != nil {
		return nil, err
	}
	// One PULSE_WEIGHT_LOW_NEFF warning per weighted row-test group
	// whose Kish n_eff fell below the test's floor.
	if err := applyLowNEff(resp, rowTests, p.strictWeights); err != nil {
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
// Shared by both crosstab exits: the buffered RunCrosstab above and the
// fused RunCrosstabFused (crosstab_fused_run.go). It reads only
// resp.Crosstab.Matrix and resp.Components.Crosstab and consumes no
// records, which is why an overlay-carrying request is fusable.
//
// On success, resp.Overlays carries one OverlayLayer per spec in
// matching order and resp.Warnings is extended with one
// ResponseWarning per types.OverlayWarning the handlers emitted (mirrors the
// label-resolver promotion in internal/service/process_labels.go).
//
// On unknown overlay kind, applyOverlays returns a CodedError whose own
// Code is the canonical errors.PULSE_OVERLAY_KIND_UNKNOWN — every overlay
// fault carries its real code so `pulse errors lookup` resolves it.
//
// compute is the run's ComputePlan: a layer it skips (ComputesOverlay)
// runs no handler and raises no refusal — the slab-partition gate
// included — and no layer computed is the overlay-free exit.
//
// bases carries each host cell's normalise-denominator floor (nil when
// the cell slot is unweighted or normalize is none) — the n leg of the
// weighted pairwise proportion kinds (crosstab_base_floors.go).
//
// measures carries each measure's single-measure host on a multi-measure
// crosstab (types.CrosstabSpec.Cells; nil otherwise): every overlay then
// runs per measure against that host and folds into one layer over the
// MEASURE-folded payload (overlay_measures.go), so a pair never crosses
// measures and each measure reads its own margins and bases.
func applyOverlaysToResponse(req *types.Request, resp *types.Response, exts *ExtensionRegistry, compute ComputePlan, bases *crosstabBaseFloors, measures []crosstabMeasureHost) error {
	if req == nil || len(req.Overlays) == 0 || !compute.Overlays {
		return nil
	}
	// A Compose, whole-chain or FACET kind on Request.Overlays, or a
	// SERIES-only kind over this MATRIX host — the shared wrong-slot /
	// wrong-shape refusals predict raises first (ValidateOverlays).
	if err := refuseWrongHostOverlays(req, exts, compute.overlayKeep()); err != nil {
		return err
	}
	// Distinct-key slab partition gate — the RUNTIME twin of the predict
	// arm in descriptor.validateOverlayPairwise. It lives here rather
	// than in runPairwiseOverlay because the offending fact is the
	// pair-axis GROUPER TYPE, which the materialised CrosstabHostView
	// does not carry; and it lives at this hook rather than at either
	// crosstab exit because both the buffered arm and the fused arm
	// funnel through here. It runs BEFORE the MATRIX-payload guard below
	// so a shape=long host refuses identically to the way predict does —
	// the refusal is a property of the request, not of the payload.
	if err := checkPairwiseSlabPartition(req, exts, compute.overlayKeep()); err != nil {
		return err
	}
	if resp == nil || resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		// shape=long or no crosstab result — validator should have
		// already rejected this at predict time. Defense in depth:
		// quietly skip so we never panic on a nil host view.
		return nil
	}
	// Thread the per-cell components block (universal-floor counters,
	// Welford triples, margin counts) into the host view so component-
	// reading overlays (the OVERLAY_PAIRWISE_* family) can resolve sample
	// sizes and Welford moments. Nil when components were disabled — those
	// handlers surface PULSE_OVERLAY_COMPONENTS_REQUIRED rather than
	// silently dividing by a zero n. Payload-only overlays ignore it.
	var crosstabComps *types.CrosstabComponents
	if resp.Components != nil {
		crosstabComps = resp.Components.Crosstab
	}
	host := newCrosstabHostViewWithComponents(resp.Crosstab.Matrix, crosstabComps)
	if req.Crosstab != nil && req.Crosstab.HasMeasures() {
		host.measures = make([]*CrosstabHostView, len(measures))
		for m, mh := range measures {
			sub := newCrosstabHostViewWithComponents(mh.matrix, mh.comps)
			sub.withNormalize(req.Crosstab.NormalizeOrDefault(), mh.bases)
			sub.withCellWeight(mh.cell)
			if mh.cell != nil {
				sub.cellType = mh.cell.Type
			}
			host.measures[m] = sub
		}
	}
	// Both crosstab arms stamp req before they run (StampWeightsWith),
	// so the cell's own `weight` IS its resolved weight: the host's
	// weight basis comes from it, not from floor-key inference (an
	// AGG_WEIGHTED_MEAN cell emits n_eff under either kind).
	if req.Crosstab != nil {
		host.withNormalize(req.Crosstab.NormalizeOrDefault(), bases)
		host.withCellWeight(req.Crosstab.Cell)
		if req.Crosstab.Cell != nil {
			host.cellType = req.Crosstab.Cell.Type
		}
		host.cell = req.Crosstab.Cell
		host.rowGroups = req.Crosstab.Rows
	}
	// When the Processor carries a live ExtensionRegistry the
	// FORMULA dispatch arm of ApplyOverlaysWithExtensions threads the
	// registry's ExprFunctions into the compile-time `[]expr.Option`
	// slice so embedder-registered helpers (`pct_change`, lookup
	// builtins, etc.) are reachable from OVERLAY_FORMULA expressions.
	// Non-FORMULA kinds ignore the registry — their handlers continue
	// to use the (spec, host) signature unchanged.
	layers, warnings, err := applyOverlaysPlanned(req.Overlays, host, exts, compute.overlayKeep())
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

// checkPairwiseSlabPartition refuses an OVERLAY_PAIRWISE_* spec whose
// distinct-key slab sums across a FAN-OUT pair-axis dim. Summing
// per-cell distinct cardinalities equals the slab's true distinct count
// only when those cells partition the key set; under a fan-out grouper
// one key lands in two summed cells and n comes out too large —
// silently, and liberally, which is the failure n_within_distinct
// exists to remove.
//
// The predicate, the message and the Details map all live on
// types.CheckPairwiseSlabPartition so this arm and the predict arm
// cannot drift apart. A malformed Params blob is NOT diagnosed here:
// runPairwiseOverlay already surfaces PULSE_OVERLAY_PARAM_MISSING for
// it, and raising a second, worse-targeted error first would bury it.
//
// exts carries the embedder's grouper registrations, so a custom
// fan-out grouper is gated exactly like GROUP_SET_PER_ELEMENT. The
// predict arm reaches the same fact through
// internal/descriptor.ExtensionsSnapshot instead — different route, one
// resolution order, because both call
// types.CheckPairwiseSlabPartitionWith.
//
// keep (nil: every spec) limits the gate to the layers the run folds.
func checkPairwiseSlabPartition(req *types.Request, exts *ExtensionRegistry, keep func(int) bool) error {
	if req == nil || req.Crosstab == nil {
		return nil
	}
	for i := range req.Overlays {
		if keep != nil && !keep(i) {
			continue
		}
		spec := &req.Overlays[i]
		if !types.IsPairwiseOverlayKind(exts.overlayRoute(spec.Kind)) {
			continue
		}
		paramread.Declare(paramread.Overlay, string(spec.Kind), (*types.PairwiseOverlayParams)(nil))
		params, err := types.DecodePairwiseParams(spec.Params)
		if err != nil {
			continue
		}
		v, bad := types.CheckPairwiseSlabPartitionWith(req.Crosstab, spec.Scope, params,
			exts.ExtensionGroupFanOut())
		if !bad {
			continue
		}
		// Like the rest of the overlay family this raises the canonical
		// PULSE_OVERLAY_* code directly as the CodedError's own Code, so
		// the runtime refusal carries the same errors[0].code the predict
		// envelope does and `pulse errors lookup` resolves it.
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED,
			v.Message(spec.Kind, params),
			v.Details(spec.Kind, params, i))
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
		ColumnHeader:     spec.ColumnHeader(),
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
// left unpopulated (older callers skip per-cell components emission).
// When non-nil, every entry corresponds to a cell that received at
// least one record
// (no empty cells in the map); a missing (r, c) yields a nil entry in
// the emitted matrix so consumers can distinguish empty cells from
// populated cells with an empty component map.
//
// rowMarginCounts / rowMarginComponents (and column / grand
// analogues) are emitted only when the caller passes non-nil
// maps — gated by the display flag on the buffered / fused path. The
// emission rule mirrors MatrixPayload.RowMargins / ColumnMargins /
// GrandTotal: margins computed for normalization but not displayed
// stay off the wire. grandHasMargin distinguishes "grand margin
// requested with zero count" from "grand margin not requested"; the
// other margins use the nil-map sentinel because per-axis maps are
// allocated only when display is on.
//
// aux carries the AUXILIARY margin-only aggregation figures
// (CrosstabSpec.MarginAggregations), already normalised out of whichever
// path produced them. Nil means the request declared no auxiliary, which
// is what keeps such a request's response byte-identical to the
// pre-auxiliary wire form — no slice header, no map, no JSON key. A nil
// map INSIDE it means that particular margin slot is not emitted.
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
	aux *crosstabAuxComponents,
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

		// Per-cell components matrix indexed identically to CellCounts.
		// Empty cells (no entry in cellComponents) emit nil at [r][c] —
		// consumers can dereference both matrices with the same (r, c)
		// pair, distinguishing "no data" (nil) from "data with empty
		// operator payload" (non-nil map with floor only). The outer
		// matrix is allocated only when cellComponents is non-nil so
		// callsites that omit CellComponents emit a CellCounts-only
		// payload.
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

	// Row-margin counts + components, indexed in rowKeys order.
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
	// Auxiliary margin-only aggregation figures, projected onto the same
	// sorted axis-key order every other margin vector uses so a consumer
	// dereferences RowMarginAggregations[r] with the r it already used
	// for RowMarginCounts[r] and MatrixPayload.RowKeys[r]. An axis key
	// with no entry (the slot admitted no record, so computeAuxMargins /
	// finalizeAuxMargins wrote nothing for it) leaves nil in its
	// position, matching RowMarginComponents' own missing-entry rule.
	//
	// The whole block is skipped when aux is nil, which is the
	// undeclared-request case and the reason a response for a request
	// with no margin_aggregations is byte-identical to one built before
	// the slot existed.
	if aux != nil {
		if aux.Rows != nil && len(rowKeys) > 0 {
			figs := make([]map[string]types.MarginAggregationFigure, len(rowKeys))
			for i, rk := range rowKeys {
				if m, ok := aux.Rows[rk]; ok {
					figs[i] = m
				}
			}
			ct.RowMarginAggregations = figs
		}
		if aux.Cols != nil && len(colKeys) > 0 {
			figs := make([]map[string]types.MarginAggregationFigure, len(colKeys))
			for i, ck := range colKeys {
				if m, ok := aux.Cols[ck]; ok {
					figs[i] = m
				}
			}
			ct.ColumnMarginAggregations = figs
		}
		if len(aux.Grand) > 0 {
			ct.GrandTotalAggregations = aux.Grand
		}
	}

	// Per-axis grouper components projected onto sorted axis-key
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
// "aggregator returned an empty row" (the buffered path treats the
// latter as a skipped cell — presence surfaces via the flag).
type cellAggregationResult struct {
	value   any
	present bool
	// weight is the slot's weighted-floor tally over the same bucket
	// (sum_weights / n_eff / n_weight_invalid); inert on an unweighted
	// slot, so an unweighted component map is unchanged.
	weight WeightFloor
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
	wf := NewWeightFloor(slot)
	for _, r := range bucket {
		// The slot's presence rule (a vector element slot: its one
		// member answered) — the fused arm and admission ask the same.
		if AggregationPresent(r, slot) {
			n++
		} else {
			nNull++
		}
		wf.ObserveSlot(r, slot)
	}

	// Decimal128 dispatch — same gate as aggregateWithComponents: a
	// registered extension aggregator bypasses the built-in decimal
	// table and runs its own factory.
	if p.schema != nil && !p.exts.isExtensionAggregator(slot.Type) {
		if f := p.schema.Field(slot.Field); f != nil && f.Type.IsDecimal() && !p.exts.decimalAggregationSupported(slot.Type) {
			if cerr := p.exts.decimalAggregationRefusal(slot.Type, slot.Field, f.Type); cerr != nil {
				return cellAggregationResult{}, nil, 0, 0, cerr
			}
		} else if f != nil && f.Type.IsDecimal() {
			out, err := AggregateDecimalField(slot.Type, bucket, slot.Field, f.Scale)
			if err != nil {
				return cellAggregationResult{}, nil, 0, 0, err
			}
			// Decimal aggregators do not implement MetaAggregator — the
			// orchestrator's floor-only entry is the full components
			// payload (mirrors the ungrouped buffered path).
			return cellAggregationResult{value: out.cellValue(), present: true, weight: wf}, nil, n, nNull, nil
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
	return cellAggregationResult{value: v, present: true, weight: wf}, aggregator, n, nNull, nil
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
	workCrosstabComponentMaps.Add(1)
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
// compatibility) live in internal/descriptor (validateCrosstab, crosstab.go) and run earlier in
// the predict path.
func validateCrosstabSpec(spec *types.CrosstabSpec, req *types.Request, ext *ExtensionRegistry) error {
	if spec == nil {
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"validateCrosstabSpec called with nil spec")
	}
	if len(spec.Rows) == 0 {
		return errors.NewCodedError(errors.PULSE_CROSSTAB_EMPTY_ROWS,
			"crosstab requires at least one row-axis grouper")
	}
	// No column grouper is the implicit Total column
	// (CrosstabSpec.HasImplicitTotalColumn), not a refusal.
	if spec.Cell == nil && !spec.HasMeasures() {
		return errors.NewCodedError(errors.PULSE_CROSSTAB_MISSING_CELL,
			"crosstab requires a Cell aggregation")
	}
	// The multi-measure Cells slot. Detection is shared with predict via
	// types.CrosstabSpec.CellsFaults; first fault wins here.
	for _, fault := range spec.CellsFaults() {
		return errors.NewCodedErrorWithDetails(cellsFaultCode(fault.Kind), fault.Message, fault.Details)
	}
	if req != nil && (len(req.Groups) > 0 || len(req.Aggregations) > 0) {
		return errors.NewCodedError(errors.PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS,
			"crosstab cannot coexist with top-level groups or aggregations on the same request")
	}
	// An array-valued cell or margin aggregation (AGG_VEC_*, or an
	// extension declared ArrayValued) has no one number for a cell:
	// refused whatever normalize says. Shared with predict
	// (vectors.ArrayValuedCrosstabRefusal).
	if cerr := vectors.ArrayValuedCrosstabRefusal(spec, ext.AggregationKinds()); cerr != nil {
		return cerr
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
	// Auxiliary margin-only aggregations. Detection is shared with
	// predict via types.CrosstabSpec.MarginAggregationFaults so the two
	// validators cannot drift on WHICH specs they refuse; only the
	// coded rendering differs. First fault wins — execution returns one
	// error, where predict accumulates them all onto the envelope.
	for _, fault := range spec.MarginAggregationFaults() {
		return errors.NewCodedErrorWithDetails(
			marginAggregationFaultCode(fault.Kind), fault.Message, fault.Details)
	}
	// Internal guard: every aggregator must carry a reducibility
	// classification. The default branch returns MarginRecompute, so
	// reaching unclassified means a new aggregator was added without
	// updating AggregationType.MarginReducibility — fail fast so the
	// missing classification is fixed. Applied to the cell aggregator
	// and to every auxiliary margin aggregation alike: an auxiliary
	// lands in the same margin accumulators, so an unclassified one is
	// the same defect in the same place.
	cells := spec.CellAggregations()
	for i, agg := range append(append([]*types.Aggregation(nil), cells...), spec.MarginAggregations...) {
		switch agg.Type.MarginReducibility() {
		case types.MarginSummable, types.MarginMeanReducible,
			types.MarginIndependent, types.MarginRecompute:
			// classified
		default:
			slot := "cell"
			if i >= len(cells) {
				slot = "margin_aggregations"
			}
			return errors.NewCodedErrorWithDetails(errors.PULSE_CROSSTAB_AGG_UNCLASSIFIED,
				"crosstab "+slot+" aggregator has no MarginReducibility classification",
				map[string]any{"aggregation": string(agg.Type), "slot": slot})
		}
	}
	return nil
}

// cellsFaultCode maps a shared Cells fault kind onto this package's coded
// surface; internal/descriptor/crosstab.go holds the predict twin.
func cellsFaultCode(kind types.CellsFaultKind) errors.Code {
	if kind == types.CellsFaultDuplicateLabel {
		return errors.PULSE_CROSSTAB_CELLS_DUPLICATE_LABEL
	}
	return errors.PULSE_CROSSTAB_CELLS_INVALID
}

// marginAggregationFaultCode maps a shared structural fault kind onto
// this package's coded surface. internal/descriptor/crosstab.go holds the
// predict-side twin; keeping the MAPPING separate from the DETECTION is
// what lets one report an envelope entry and the other a CodedError
// without either owning the rules.
func marginAggregationFaultCode(kind types.MarginAggregationFaultKind) errors.Code {
	switch kind {
	case types.MarginAggregationFaultDuplicateLabel:
		return errors.PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL
	default:
		// Nil entry, missing type, and any kind added later without a
		// dedicated code: all are "this entry is not a usable
		// aggregation", which is exactly what _INVALID says.
		return errors.PULSE_CROSSTAB_MARGIN_AGG_INVALID
	}
}
