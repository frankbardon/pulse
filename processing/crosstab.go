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

	cellValues := make(map[crosstabCellKey]float64, len(rowPart.Keys)*len(colPart.Keys))
	cellPresent := make(map[crosstabCellKey]bool, len(rowPart.Keys)*len(colPart.Keys))

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
			row, err := p.aggregate([]*types.Aggregation{cellAgg}, bucket)
			if err != nil {
				return nil, err
			}
			if row == nil {
				continue
			}
			cellValues[crosstabCellKey{rkey, ckey}] = coerceFloat64(row[cellLabel])
			cellPresent[crosstabCellKey{rkey, ckey}] = true
		}
	}

	var rowMargins map[string]float64
	var rowMarginPresent map[string]bool
	if spec.NeedsRowMargin() {
		rowMargins = make(map[string]float64, len(rowPart.Keys))
		rowMarginPresent = make(map[string]bool, len(rowPart.Keys))
		for _, rkey := range rowPart.Keys {
			bucket := rowPart.Records[rkey]
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
			rowMargins[rkey] = coerceFloat64(row[cellLabel])
			rowMarginPresent[rkey] = true
		}
	}

	var colMargins map[string]float64
	var colMarginPresent map[string]bool
	if spec.NeedsColumnMargin() {
		colMargins = make(map[string]float64, len(colPart.Keys))
		colMarginPresent = make(map[string]bool, len(colPart.Keys))
		for _, ckey := range colPart.Keys {
			bucket := colPart.Records[ckey]
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
			colMargins[ckey] = coerceFloat64(row[cellLabel])
			colMarginPresent[ckey] = true
		}
	}

	var grandMargin float64
	grandPresent := false
	if spec.NeedsGrandMargin() {
		if len(filtered) > 0 {
			row, err := p.aggregate([]*types.Aggregation{cellAgg}, filtered)
			if err != nil {
				return nil, err
			}
			if row != nil {
				grandMargin = coerceFloat64(row[cellLabel])
				grandPresent = true
			}
		}
	}

	mode := spec.NormalizeOrDefault()
	if mode != types.CrosstabNormalizeNone {
		normalized := make(map[crosstabCellKey]float64, len(cellValues))
		for ck, v := range cellValues {
			denom, ok := normalizeDenominator(mode, ck.row, ck.col,
				rowMargins, rowMarginPresent,
				colMargins, colMarginPresent,
				grandMargin, grandPresent)
			if !ok || denom == 0 {
				// Divide-by-zero policy: drop the cell. Downstream
				// rendering sees Present=false.
				cellPresent[ck] = false
				continue
			}
			normalized[ck] = v / denom
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
		resp.Data = buildLongRows(spec, rowPart, colPart, cellValues, cellPresent, rowMargins, rowMarginPresent, colMargins, colMarginPresent, grandMargin, grandPresent, cellLabel)
	}

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

	return resp, nil
}

// buildCellRowsForPostTest synthesises one row per present cell with
// every axis field populated and the cell label set to the (possibly
// normalized) cell value. Excludes margin annotations. Matches the
// cell-only subset of buildLongRows so a tier-2 post test sees the
// same observations regardless of the configured shape.
func buildCellRowsForPostTest(spec *types.CrosstabSpec,
	rowPart, colPart *CrosstabAxisPartition,
	cellValues map[crosstabCellKey]float64,
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
	cellValues map[crosstabCellKey]float64,
	cellPresent map[crosstabCellKey]bool,
	rowMargins map[string]float64, rowPresent map[string]bool,
	colMargins map[string]float64, colPresent map[string]bool,
	grand float64, grandPresent bool,
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
	cellValues map[crosstabCellKey]float64,
	cellPresent map[crosstabCellKey]bool,
	rowMargins map[string]float64, rowPresent map[string]bool,
	colMargins map[string]float64, colPresent map[string]bool,
	grand float64, grandPresent bool,
	cellLabel string,
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
	return data
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
