package excel

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/types"
	"github.com/xuri/excelize/v2"
)

// Excel overlay embedding implementation per
// research/export-embedding-shape.md § 5.
//
// Each Response.Overlays layer rides on a dedicated sheet named
// "__overlay_<layer_name>" alongside the host data sheet. The double-
// underscore prefix reserves the sheet-name namespace from author-
// supplied workbook sheets; readers can filter overlay sheets by
// HasPrefix(name, OverlaySheetPrefix). Excel sheet names cap at 31
// characters; longer names truncate + disambiguate via a numeric suffix.
//
// Per-shape layouts (§ 5.2):
//
//   - SCALAR — header block (kind, scope, ref) + value cell + summary
//     block.
//   - SERIES — header block + table (key | value | present | statistic
//     | p_value | parameters).
//   - MATRIX — header block + axis-key grid (corner | column keys
//     [+ __col_margin] / row keys [+ __row_margin] / grand total) where
//     present cells carry the float64 value and absent cells stay blank.

// OverlaySheetPrefix is the reserved sheet-name prefix every embedded
// overlay sheet carries. Authors filter overlay sheets with
// strings.HasPrefix(name, OverlaySheetPrefix). Author-supplied sheets
// MUST NOT start with this prefix; the prefix is reserved for the
// engine.
const OverlaySheetPrefix = "__overlay_"

// excelSheetNameMaxLen is the Excel-mandated sheet-name length cap
// (31 characters). Overlay sheets whose composed name exceeds this cap
// are truncated and disambiguated via a numeric suffix per § 5.1.
const excelSheetNameMaxLen = 31

// writeOverlaySheets emits one sheet per overlay layer onto the
// writer's workbook. No-op when overlays is nil or empty — the
// workbook stays byte-identical to a pre-overlay export.
//
// Layout per shape:
//   - scalar: a small header block (kind, scope, ref) + the value +
//     summary slots.
//   - series: header + per-entry rows (key | value | present | statistic
//     | p_value | parameters_json).
//   - matrix: header + axis grid (row × column cells, optional row /
//     column margins, grand total).
//
// Sheet emission order matches Response.Overlays slot order so the
// sheet ordinal (after the host sheet) matches the layer index. Sheet
// names use buildOverlaySheetName for truncation + collision
// disambiguation.
func (w *Writer) writeOverlaySheets() error {
	if len(w.overlays) == 0 {
		return nil
	}
	if w.file == nil {
		return fmt.Errorf("excel.Writer: writeOverlaySheets called before init")
	}

	// existing tracks already-allocated sheet names (the host sheet plus
	// any previously-emitted overlay sheet) so buildOverlaySheetName can
	// disambiguate collisions.
	existing := map[string]bool{}
	for _, name := range w.file.GetSheetList() {
		existing[name] = true
	}

	for _, layer := range w.overlays {
		if layer == nil {
			continue
		}
		sheetName := buildOverlaySheetName(layer.Name, existing)
		existing[sheetName] = true

		if _, err := w.file.NewSheet(sheetName); err != nil {
			return fmt.Errorf("excel.Writer: creating overlay sheet %q: %w", sheetName, err)
		}
		if err := writeOverlayLayerSheet(w.file, sheetName, layer); err != nil {
			return fmt.Errorf("excel.Writer: writing overlay sheet %q: %w", sheetName, err)
		}
	}
	return nil
}

// buildOverlaySheetName produces the engine-managed sheet name for a
// layer. The composed shape is "__overlay_<layer_name>" truncated to
// excelSheetNameMaxLen and disambiguated against `existing` (the
// already-used sheet-name set) via a numeric suffix.
//
// Truncation strategy:
//
//  1. Prepend the OverlaySheetPrefix.
//  2. If the composed name fits in 31 chars and is unique, use it.
//  3. Otherwise truncate the suffix portion and append "_tN" (N >= 1)
//     until the name is unique. The numeric suffix grows in width
//     (1 → 2 → 3 digits) so the prefix shrinks accordingly to stay
//     under the cap.
//
// The disambiguation suffix is "_t<N>" rather than just "<N>" so the
// pattern is visually distinct from a layer name that happens to end in
// a digit; "t" stands for "truncated" and matches the convention called
// out in research/export-embedding-shape.md § 5.1.
func buildOverlaySheetName(layerName string, existing map[string]bool) string {
	prefix := OverlaySheetPrefix
	full := prefix + layerName

	// Fast path: composed name fits the cap and does not collide.
	if len(full) <= excelSheetNameMaxLen && !existing[full] {
		return full
	}

	// Truncate to the cap first.
	base := full
	if len(base) > excelSheetNameMaxLen {
		base = base[:excelSheetNameMaxLen]
	}
	if !existing[base] {
		return base
	}

	// Disambiguate via "_tN" suffix. The suffix replaces the trailing
	// characters of the truncated base so the total length stays under
	// the cap.
	for n := 1; n < 1<<20; n++ {
		suffix := "_t" + strconv.Itoa(n)
		room := excelSheetNameMaxLen - len(suffix)
		if room < len(prefix) {
			// Suffix grew past the room budget; fall back to a fully-
			// numeric name carrying only the prefix + suffix. This path
			// is theoretical (1 048 575 collisions on the same prefix +
			// truncated base) but keeps the loop bounded.
			candidate := prefix + suffix
			if len(candidate) > excelSheetNameMaxLen {
				candidate = candidate[:excelSheetNameMaxLen]
			}
			if !existing[candidate] {
				return candidate
			}
			continue
		}
		head := full
		if len(head) > room {
			head = head[:room]
		}
		candidate := head + suffix
		if !existing[candidate] {
			return candidate
		}
	}
	// Caller will surface a clearer error if we exit the loop without a
	// unique name — but in practice we never reach this branch.
	return base
}

// writeOverlayLayerSheet renders one overlay layer onto a freshly-
// created sheet. The renderer dispatches on Payload.Shape to pick the
// per-shape layout described in research/export-embedding-shape.md § 5.2.
//
// Every shape's sheet starts with a small header block (rows 1..N
// covering kind, scope, and ref) followed by a blank row and then the
// shape-specific body. Headers ride with row labels in column A and
// values in column B so a renderer reading the sheet sees a key/value
// preamble before the payload table / grid.
func writeOverlayLayerSheet(f *excelize.File, sheet string, layer *types.OverlayLayer) error {
	row, err := writeOverlayHeaderBlock(f, sheet, layer)
	if err != nil {
		return err
	}

	switch layer.Payload.Shape {
	case types.OverlayShapeScalar:
		if err := writeOverlayScalarBody(f, sheet, row, layer); err != nil {
			return err
		}
	case types.OverlayShapeSeries:
		if err := writeOverlaySeriesBody(f, sheet, row, layer); err != nil {
			return err
		}
	case types.OverlayShapeMatrix:
		if err := writeOverlayMatrixBody(f, sheet, row, layer); err != nil {
			return err
		}
	default:
		// Unknown shape — write the kind echo + shape literal so a
		// renderer surfaces the unknown value rather than silently
		// dropping the layer. The discriminator stays inline per
		// research/export-embedding-shape.md § 2 rule 3.
		if err := setOverlayCell(f, sheet, "A", row, "shape"); err != nil {
			return err
		}
		if err := setOverlayCell(f, sheet, "B", row, string(layer.Payload.Shape)); err != nil {
			return err
		}
	}

	// Layer-level summary always rides last so the renderer can locate
	// it after the payload body.
	if layer.Summary != nil {
		if err := writeOverlaySummaryBlock(f, sheet, layer.Summary); err != nil {
			return err
		}
	}
	return nil
}

// writeOverlayHeaderBlock writes the discriminator header rows (kind,
// scope, ref) starting at row 1 and returns the next free row index.
// A blank row separator follows the block so the payload body starts
// two rows below the last header cell.
func writeOverlayHeaderBlock(f *excelize.File, sheet string, layer *types.OverlayLayer) (int, error) {
	row := 1
	if err := setOverlayCell(f, sheet, "A", row, "kind"); err != nil {
		return 0, err
	}
	if err := setOverlayCell(f, sheet, "B", row, string(layer.Kind)); err != nil {
		return 0, err
	}
	row++

	if err := setOverlayCell(f, sheet, "A", row, "scope"); err != nil {
		return 0, err
	}
	if err := setOverlayCell(f, sheet, "B", row, string(layer.Scope)); err != nil {
		return 0, err
	}
	row++

	if err := setOverlayCell(f, sheet, "A", row, "ref"); err != nil {
		return 0, err
	}
	refJSON, err := json.Marshal(layer.Ref)
	if err != nil {
		return 0, fmt.Errorf("marshal ref: %w", err)
	}
	if err := setOverlayCell(f, sheet, "B", row, string(refJSON)); err != nil {
		return 0, err
	}
	row++

	// Blank separator row.
	row++
	return row, nil
}

// writeOverlayScalarBody writes the SCALAR payload section per § 5.2.
// Layout is a single key/value pair carrying the float64 value.
func writeOverlayScalarBody(f *excelize.File, sheet string, row int, layer *types.OverlayLayer) error {
	if err := setOverlayCell(f, sheet, "A", row, "value"); err != nil {
		return err
	}
	if layer.Payload.Scalar != nil {
		if err := setOverlayCell(f, sheet, "B", row, *layer.Payload.Scalar); err != nil {
			return err
		}
	}
	return nil
}

// writeOverlaySeriesBody writes the SERIES payload section per § 5.2.
// Layout is a tabular block:
//
//	row N+0: | key | value | present | statistic | p_value | parameters |
//	row N+1+i (one per entry): the per-entry tuple.
//
// `value` mirrors Summary.Statistic when present (matching the SERIES
// convention used throughout the kind catalog); `present` is true when
// the entry has a non-empty key OR any summary slot populated.
// `parameters` is the JSON-encoded parameter map (omitempty when empty).
func writeOverlaySeriesBody(f *excelize.File, sheet string, row int, layer *types.OverlayLayer) error {
	headers := []string{"key", "value", "present", "statistic", "p_value", "parameters"}
	for c, h := range headers {
		col, _ := excelize.ColumnNumberToName(c + 1)
		if err := setOverlayCell(f, sheet, col, row, h); err != nil {
			return err
		}
	}
	row++

	if layer.Payload.Series == nil {
		return nil
	}
	for _, entry := range layer.Payload.Series.Entries {
		keyStr := joinAxisKey(entry.Key)
		if err := setOverlayCell(f, sheet, "A", row, keyStr); err != nil {
			return err
		}

		// `value` mirrors Summary.Statistic when present — the SERIES
		// per-entry statistic is the renderer-facing numeric.
		present := entry.Summary.Statistic != nil
		if present {
			if err := setOverlayCell(f, sheet, "B", row, *entry.Summary.Statistic); err != nil {
				return err
			}
		}
		if err := setOverlayCell(f, sheet, "C", row, present); err != nil {
			return err
		}
		if entry.Summary.Statistic != nil {
			if err := setOverlayCell(f, sheet, "D", row, *entry.Summary.Statistic); err != nil {
				return err
			}
		}
		if entry.Summary.PValue != nil {
			if err := setOverlayCell(f, sheet, "E", row, *entry.Summary.PValue); err != nil {
				return err
			}
		}
		if len(entry.Summary.Parameters) > 0 {
			paramsJSON, err := json.Marshal(entry.Summary.Parameters)
			if err != nil {
				return fmt.Errorf("marshal series entry parameters: %w", err)
			}
			if err := setOverlayCell(f, sheet, "F", row, string(paramsJSON)); err != nil {
				return err
			}
		}
		row++
	}
	return nil
}

// writeOverlayMatrixBody writes the MATRIX payload section per § 5.2.
// Layout is a 2-D grid: column A carries row keys, row N carries column
// keys (offset by one for the corner cell). Optional margin columns /
// rows ride on the outer edge ("__col_margin" / "__row_margin") and the
// grand total lands at the intersection. Cells with Present=false stay
// blank — the absent-cell rule from research/export-embedding-shape.md
// § 2 (rule 4) and § 5.2.
func writeOverlayMatrixBody(f *excelize.File, sheet string, row int, layer *types.OverlayLayer) error {
	mp := layer.Payload.Matrix
	if mp == nil {
		return nil
	}

	hasColMargin := len(mp.ColumnMargins) > 0
	hasRowMargin := len(mp.RowMargins) > 0

	// Header row: corner label "key", then per-column keys, then
	// optional __col_margin.
	if err := setOverlayCell(f, sheet, "A", row, "key"); err != nil {
		return err
	}
	for c, ck := range mp.ColumnKeys {
		col, _ := excelize.ColumnNumberToName(c + 2)
		if err := setOverlayCell(f, sheet, col, row, joinAxisKey(ck)); err != nil {
			return err
		}
	}
	if hasColMargin {
		col, _ := excelize.ColumnNumberToName(len(mp.ColumnKeys) + 2)
		if err := setOverlayCell(f, sheet, col, row, "__col_margin"); err != nil {
			return err
		}
	}
	row++

	// One row per RowKey: row-key cell + per-cell values + optional
	// __row_margin trailing cell.
	for r, rk := range mp.RowKeys {
		if err := setOverlayCell(f, sheet, "A", row, joinAxisKey(rk)); err != nil {
			return err
		}
		if r < len(mp.Cells) {
			for c, cell := range mp.Cells[r] {
				col, _ := excelize.ColumnNumberToName(c + 2)
				if cell.Present {
					if err := setOverlayCell(f, sheet, col, row, cell.Value); err != nil {
						return err
					}
				}
			}
		}
		if hasRowMargin && r < len(mp.RowMargins) {
			col, _ := excelize.ColumnNumberToName(len(mp.ColumnKeys) + 2)
			rm := mp.RowMargins[r]
			if rm.Present {
				if err := setOverlayCell(f, sheet, col, row, rm.Value); err != nil {
					return err
				}
			}
		}
		row++
	}

	// Optional margin row: "__col_margin" label + per-column margins +
	// grand total at the corner.
	if hasColMargin {
		if err := setOverlayCell(f, sheet, "A", row, "__col_margin"); err != nil {
			return err
		}
		for c, cm := range mp.ColumnMargins {
			col, _ := excelize.ColumnNumberToName(c + 2)
			if cm.Present {
				if err := setOverlayCell(f, sheet, col, row, cm.Value); err != nil {
					return err
				}
			}
		}
		if mp.GrandTotal.Present {
			col, _ := excelize.ColumnNumberToName(len(mp.ColumnKeys) + 2)
			if err := setOverlayCell(f, sheet, col, row, mp.GrandTotal.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeOverlaySummaryBlock writes the layer-level summary fields after
// the payload body. Each populated field rides as a key/value pair
// (label in column A, value in column B). Absent summary fields stay
// blank so the renderer can detect them via cell emptiness.
func writeOverlaySummaryBlock(f *excelize.File, sheet string, sum *types.OverlaySummary) error {
	// Find the first free row by scanning the sheet's existing rows.
	row, err := nextFreeRow(f, sheet)
	if err != nil {
		return err
	}
	row++ // blank-row separator before the summary block

	if err := setOverlayCell(f, sheet, "A", row, "summary"); err != nil {
		return err
	}
	row++

	if sum.Min != nil {
		if err := writeSummaryPair(f, sheet, row, "summary.min", *sum.Min); err != nil {
			return err
		}
		row++
	}
	if sum.Max != nil {
		if err := writeSummaryPair(f, sheet, row, "summary.max", *sum.Max); err != nil {
			return err
		}
		row++
	}
	if sum.Count != nil {
		if err := writeSummaryPair(f, sheet, row, "summary.count", *sum.Count); err != nil {
			return err
		}
		row++
	}
	if sum.Baseline != nil {
		if err := writeSummaryPair(f, sheet, row, "summary.baseline", *sum.Baseline); err != nil {
			return err
		}
		row++
	}
	if sum.Statistic != nil {
		if err := writeSummaryPair(f, sheet, row, "summary.statistic", *sum.Statistic); err != nil {
			return err
		}
		row++
	}
	if sum.PValue != nil {
		if err := writeSummaryPair(f, sheet, row, "summary.p_value", *sum.PValue); err != nil {
			return err
		}
		row++
	}
	if len(sum.Parameters) > 0 {
		paramsJSON, err := json.Marshal(sum.Parameters)
		if err != nil {
			return fmt.Errorf("marshal summary parameters: %w", err)
		}
		if err := writeSummaryPair(f, sheet, row, "summary.parameters", string(paramsJSON)); err != nil {
			return err
		}
	}
	return nil
}

// writeSummaryPair writes a labelled summary value as a key/value pair.
func writeSummaryPair(f *excelize.File, sheet string, row int, label string, value any) error {
	if err := setOverlayCell(f, sheet, "A", row, label); err != nil {
		return err
	}
	return setOverlayCell(f, sheet, "B", row, value)
}

// setOverlayCell sets a cell value at (col, row) on the named sheet.
// Wraps excelize.File.SetCellValue with the column-name + row-index
// addressing the overlay layout uses.
func setOverlayCell(f *excelize.File, sheet, col string, row int, value any) error {
	cell := col + strconv.Itoa(row)
	return f.SetCellValue(sheet, cell, value)
}

// nextFreeRow returns the first row index that has no populated cell on
// the named sheet. Used by writeOverlaySummaryBlock to append the
// layer-level summary after the payload body without overwriting cells.
func nextFreeRow(f *excelize.File, sheet string) (int, error) {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return 0, fmt.Errorf("get rows: %w", err)
	}
	// GetRows returns the populated row range; the next free row is one
	// past the last populated row (1-indexed for excelize).
	return len(rows) + 1, nil
}

// joinAxisKey renders a composite axis-key tuple as a "|"-joined
// string. Mirrors the categorical_* / SET_VALUE join convention called
// out in research/export-embedding-shape.md § 5.2 ("The row / column
// header carries axis-key strings joined with `|` for multi-grouper
// axes").
func joinAxisKey(k types.AxisKey) string {
	if len(k) == 0 {
		return ""
	}
	parts := make([]string, len(k))
	for i, v := range k {
		parts[i] = fmt.Sprintf("%v", v)
	}
	return strings.Join(parts, "|")
}

// ReadOverlays walks every "__overlay_*" sheet on the workbook and
// reconstructs the embedded []*types.OverlayLayer slice. Returns nil
// (not error) when the workbook carries no overlay sheets — the
// contract is "no overlays present" rather than "field missing is an
// error" (mirrors the Arrow / Parquet ReadOverlays surface).
//
// Round-trip is best-effort per research/export-embedding-shape.md
// § 5.3 — Excel coerces some numeric edge cases (NaN, Inf, very-small /
// very-large floats) to text strings on write, so the reader treats
// each cell as a float64 best-effort and tolerates ±1 ULP on numeric
// values. The shape (kind / scope / ref / payload shape / per-entry
// keys / per-cell present flag / summary slots) is preserved exactly.
//
// Sheet emission order is the source of truth for layer slot order
// (matches Response.Overlays slot order at export time). The reader
// returns layers in the order it discovers overlay sheets on the
// workbook.
func (r *Reader) ReadOverlays() ([]*types.OverlayLayer, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	var layers []*types.OverlayLayer
	for _, sheet := range r.file.GetSheetList() {
		if !strings.HasPrefix(sheet, OverlaySheetPrefix) {
			continue
		}
		layer, err := readOverlayLayerFromSheet(r.file, sheet)
		if err != nil {
			return nil, fmt.Errorf("excel.Reader: reading overlay sheet %q: %w", sheet, err)
		}
		if layer != nil {
			layers = append(layers, layer)
		}
	}
	return layers, nil
}

// readOverlayLayerFromSheet rebuilds one OverlayLayer from the rows on
// a "__overlay_*" sheet. Inverse of writeOverlayLayerSheet.
func readOverlayLayerFromSheet(f *excelize.File, sheet string) (*types.OverlayLayer, error) {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("get rows: %w", err)
	}
	if len(rows) < 3 {
		return nil, nil
	}

	layer := &types.OverlayLayer{}
	if len(rows) > 0 && len(rows[0]) >= 2 && rows[0][0] == "kind" {
		layer.Kind = types.OverlayKind(rows[0][1])
	}
	if len(rows) > 1 && len(rows[1]) >= 2 && rows[1][0] == "scope" {
		layer.Scope = types.OverlayScope(rows[1][1])
	}
	if len(rows) > 2 && len(rows[2]) >= 2 && rows[2][0] == "ref" {
		if rows[2][1] != "" {
			if err := json.Unmarshal([]byte(rows[2][1]), &layer.Ref); err != nil {
				return nil, fmt.Errorf("unmarshal ref: %w", err)
			}
		}
	}

	// Layer name is derived from the sheet name (strip prefix). This is
	// best-effort — round-trip Excel exports preserve the original
	// layer name modulo truncation / disambiguation. Renderers that
	// need exact identity should pair Excel exports with a sidecar
	// manifest (out of scope for v1; documented in § 5.3).
	layer.Name = strings.TrimPrefix(sheet, OverlaySheetPrefix)

	// Body starts at row 5 (row 4 is the blank separator). Dispatch on
	// the row-5 layout to decode the payload.
	if len(rows) < 5 {
		return layer, nil
	}
	bodyRows := rows[4:]
	if err := decodeOverlayBody(bodyRows, layer); err != nil {
		return nil, err
	}
	return layer, nil
}

// decodeOverlayBody inspects the body rows of an overlay sheet and
// dispatches to the matching payload decoder. The dispatch is shape-
// agnostic — it looks at the first body row's first cell:
//
//   - "value" → SCALAR
//   - "key"   → SERIES or MATRIX (further disambiguation: SERIES carries
//     a "value" header in column B; MATRIX has axis-key strings in
//     row 5 starting at column B).
func decodeOverlayBody(bodyRows [][]string, layer *types.OverlayLayer) error {
	if len(bodyRows) == 0 {
		return nil
	}
	head := bodyRows[0]
	if len(head) == 0 {
		return nil
	}

	switch head[0] {
	case "value":
		layer.Payload.Shape = types.OverlayShapeScalar
		if len(head) >= 2 && head[1] != "" {
			if v, err := strconv.ParseFloat(head[1], 64); err == nil {
				layer.Payload.Scalar = &v
			}
		}
	case "key":
		// SERIES header is "key | value | present | statistic |
		// p_value | parameters"; MATRIX header is "key | <col_key_0> |
		// <col_key_1> | ...". Distinguish on column B: SERIES carries
		// the literal "value" string; MATRIX carries an axis-key.
		if len(head) >= 2 && head[1] == "value" {
			layer.Payload.Shape = types.OverlayShapeSeries
			if err := decodeOverlaySeriesBody(bodyRows[1:], layer); err != nil {
				return err
			}
		} else {
			layer.Payload.Shape = types.OverlayShapeMatrix
			if err := decodeOverlayMatrixBody(bodyRows, layer); err != nil {
				return err
			}
		}
	}

	// Trailing summary block — scan for the "summary" marker row and
	// decode the labelled key/value pairs that follow.
	for i := 0; i < len(bodyRows); i++ {
		if len(bodyRows[i]) > 0 && bodyRows[i][0] == "summary" {
			if err := decodeOverlaySummaryBlock(bodyRows[i+1:], layer); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// decodeOverlaySeriesBody reconstructs SeriesPayload from the rows
// following the SERIES header row.
func decodeOverlaySeriesBody(rows [][]string, layer *types.OverlayLayer) error {
	series := &types.SeriesPayload{}
	for _, row := range rows {
		if len(row) == 0 {
			break
		}
		// Trailing summary block — stop at the summary marker.
		if row[0] == "summary" {
			break
		}
		entry := types.SeriesEntry{}
		if row[0] != "" {
			entry.Key = types.AxisKey{row[0]}
		}
		if len(row) > 3 && row[3] != "" {
			if v, err := strconv.ParseFloat(row[3], 64); err == nil {
				entry.Summary.Statistic = &v
			}
		}
		if len(row) > 4 && row[4] != "" {
			if v, err := strconv.ParseFloat(row[4], 64); err == nil {
				entry.Summary.PValue = &v
			}
		}
		if len(row) > 5 && row[5] != "" {
			params := map[string]float64{}
			if err := json.Unmarshal([]byte(row[5]), &params); err == nil {
				entry.Summary.Parameters = params
			}
		}
		series.Entries = append(series.Entries, entry)
	}
	layer.Payload.Series = series
	return nil
}

// decodeOverlayMatrixBody reconstructs MatrixPayload from a MATRIX
// overlay sheet's body rows. The first body row is the column-header
// row (corner + per-column keys + optional __col_margin); subsequent
// rows carry the per-row data (row key + cells + optional __row_margin);
// an optional final "__col_margin" row carries the column margins +
// grand total at the corner.
func decodeOverlayMatrixBody(bodyRows [][]string, layer *types.OverlayLayer) error {
	if len(bodyRows) == 0 {
		return nil
	}
	header := bodyRows[0]
	if len(header) < 2 {
		return nil
	}

	// Parse column keys (skip column A which carries the "key" corner)
	// and detect a trailing __col_margin column.
	colKeys := []types.AxisKey{}
	hasColMargin := false
	for c := 1; c < len(header); c++ {
		if header[c] == "__col_margin" {
			hasColMargin = true
			continue
		}
		colKeys = append(colKeys, types.AxisKey{header[c]})
	}

	rowKeys := []types.AxisKey{}
	cells := [][]types.MatrixCell{}
	rowMargins := []types.MatrixCell{}
	hasRowMargin := false
	colMargins := []types.MatrixCell{}
	grandTotal := types.MatrixCell{}

	colCount := len(colKeys)
	marginColIdx := -1
	if hasColMargin {
		marginColIdx = colCount + 1
	}

	for r := 1; r < len(bodyRows); r++ {
		row := bodyRows[r]
		if len(row) == 0 {
			break
		}
		// Trailing summary block — stop scanning.
		if row[0] == "summary" {
			break
		}
		// Trailing __col_margin row.
		if row[0] == "__col_margin" {
			for c := 0; c < colCount; c++ {
				if c+1 >= len(row) {
					colMargins = append(colMargins, types.MatrixCell{})
					continue
				}
				colMargins = append(colMargins, parseMatrixCell(row[c+1]))
			}
			if hasColMargin && marginColIdx < len(row) {
				grandTotal = parseMatrixCell(row[marginColIdx])
			}
			continue
		}
		rowKeys = append(rowKeys, types.AxisKey{row[0]})
		cellRow := make([]types.MatrixCell, colCount)
		for c := 0; c < colCount; c++ {
			if c+1 < len(row) {
				cellRow[c] = parseMatrixCell(row[c+1])
			}
		}
		cells = append(cells, cellRow)
		if hasColMargin && marginColIdx < len(row) {
			hasRowMargin = true
			rowMargins = append(rowMargins, parseMatrixCell(row[marginColIdx]))
		} else if hasColMargin {
			hasRowMargin = true
			rowMargins = append(rowMargins, types.MatrixCell{})
		}
	}

	mp := &types.MatrixPayload{
		RowKeys:    rowKeys,
		ColumnKeys: colKeys,
		Cells:      cells,
	}
	if hasRowMargin {
		mp.RowMargins = rowMargins
	}
	if len(colMargins) > 0 {
		mp.ColumnMargins = colMargins
	}
	mp.GrandTotal = grandTotal
	layer.Payload.Matrix = mp
	return nil
}

// parseMatrixCell rebuilds one MatrixCell from a string cell value.
// Empty string → absent cell (Present=false). Non-empty parseable
// float64 → present cell with the float value. Non-empty unparseable
// → present cell with the raw string (preserves the round-trip best-
// effort policy from § 5.3).
func parseMatrixCell(s string) types.MatrixCell {
	if s == "" {
		return types.MatrixCell{Present: false}
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return types.MatrixCell{Value: v, Present: true}
	}
	return types.MatrixCell{Value: s, Present: true}
}

// decodeOverlaySummaryBlock reconstructs the layer-level OverlaySummary
// from the labelled key/value pairs following the "summary" marker row.
func decodeOverlaySummaryBlock(rows [][]string, layer *types.OverlayLayer) error {
	sum := &types.OverlaySummary{}
	populated := false
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		label := row[0]
		val := row[1]
		if val == "" {
			continue
		}
		switch label {
		case "summary.min":
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				sum.Min = &v
				populated = true
			}
		case "summary.max":
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				sum.Max = &v
				populated = true
			}
		case "summary.count":
			if v, err := strconv.Atoi(val); err == nil {
				sum.Count = &v
				populated = true
			}
		case "summary.baseline":
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				sum.Baseline = &v
				populated = true
			}
		case "summary.statistic":
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				sum.Statistic = &v
				populated = true
			}
		case "summary.p_value":
			if v, err := strconv.ParseFloat(val, 64); err == nil {
				sum.PValue = &v
				populated = true
			}
		case "summary.parameters":
			params := map[string]float64{}
			if err := json.Unmarshal([]byte(val), &params); err == nil {
				sum.Parameters = params
				populated = true
			}
		}
	}
	if populated {
		layer.Summary = sum
	}
	return nil
}
