// Package excel provides Excel import and export for the pulse I/O pipeline.
package excel

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
	"github.com/xuri/excelize/v2"
)

// Option configures Excel reader or writer behavior.
type Option func(*config)

type config struct {
	sheet string // empty means first sheet
}

// WithSheet selects a named sheet to read from or write to.
func WithSheet(name string) Option {
	return func(c *config) {
		c.sheet = name
	}
}

// Reader reads tabular data from an Excel (.xlsx) file.
type Reader struct {
	fs      afero.Fs
	path    string
	cfg     config
	data    []byte
	file    *excelize.File
	sheet   string // resolved sheet name
	header  []string
	rows    [][]string // cached rows (excelize doesn't support true streaming for afero)
	rowIdx  int
	started bool
}

// NewReader creates an Excel reader from a filesystem path.
func NewReader(fs afero.Fs, path string, opts ...Option) *Reader {
	r := &Reader{
		fs:   fs,
		path: path,
	}
	for _, o := range opts {
		o(&r.cfg)
	}
	return r
}

// NewReaderFromBytes creates an Excel reader from raw bytes.
func NewReaderFromBytes(data []byte, opts ...Option) *Reader {
	r := &Reader{
		data: data,
	}
	for _, o := range opts {
		o(&r.cfg)
	}
	return r
}

func (r *Reader) init() error {
	if r.file != nil {
		return nil
	}

	// Load the data.
	if r.data == nil {
		if r.fs == nil {
			return fmt.Errorf("excel.Reader: no data source")
		}
		data, err := afero.ReadFile(r.fs, r.path)
		if err != nil {
			return fmt.Errorf("excel.Reader: reading %s: %w", r.path, err)
		}
		r.data = data
	}

	f, err := excelize.OpenReader(bytes.NewReader(r.data))
	if err != nil {
		return fmt.Errorf("excel.Reader: opening workbook: %w", err)
	}
	r.file = f

	// Resolve sheet name.
	if r.cfg.sheet != "" {
		r.sheet = r.cfg.sheet
	} else {
		r.sheet = f.GetSheetName(0)
	}

	// Read all rows from the sheet into memory.
	// Excelize's GetRows is the simplest reliable way to read data,
	// and works correctly with afero-sourced bytes.
	allRows, err := f.GetRows(r.sheet)
	if err != nil {
		return fmt.Errorf("excel.Reader: reading sheet %q: %w", r.sheet, err)
	}

	if len(allRows) > 0 {
		r.header = allRows[0]
		r.rows = allRows[1:]
	} else {
		r.header = nil
		r.rows = nil
	}
	r.rowIdx = 0
	r.started = false

	return nil
}

// ReadHeader returns the column names from the first row.
func (r *Reader) ReadHeader() ([]string, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	r.started = true
	if r.header == nil {
		return nil, nil
	}
	return r.header, nil
}

// ReadRows streams rows to fn. ReadHeader must be called first.
func (r *Reader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	if r.file == nil {
		if err := r.init(); err != nil {
			return err
		}
	}
	if !r.started {
		if _, err := r.ReadHeader(); err != nil {
			return err
		}
	}

	numCols := len(r.header)

	for r.rowIdx < len(r.rows) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		row := r.rows[r.rowIdx]
		r.rowIdx++

		// Pad or truncate to match header width.
		normalized := make([]string, numCols)
		for i := 0; i < numCols; i++ {
			if i < len(row) {
				normalized[i] = formatCellValue(row[i])
			}
		}

		if err := fn(normalized); err != nil {
			if err == pio.ErrStopIteration() {
				return nil
			}
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_ROW_ERROR,
				fmt.Sprintf("row %d: %v", r.rowIdx, err),
				map[string]any{"row": r.rowIdx},
			)
		}
	}

	return nil
}

// Close releases resources.
func (r *Reader) Close() error {
	if r.file != nil {
		r.file.Close()
		r.file = nil
	}
	r.rows = nil
	r.header = nil
	return nil
}

// Reset rewinds the reader to the beginning.
func (r *Reader) Reset() error {
	// Keep data, re-parse on next init.
	if r.file != nil {
		r.file.Close()
		r.file = nil
	}
	r.header = nil
	r.rows = nil
	r.rowIdx = 0
	r.started = false
	return nil
}

// formatCellValue normalizes a cell value string for the I/O pipeline.
// Excelize returns cell values as strings; we clean up trailing zeros
// on floats and handle booleans.
func formatCellValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}

	// Boolean normalization: Excel stores as TRUE/FALSE.
	upper := strings.ToUpper(v)
	if upper == "TRUE" {
		return "true"
	}
	if upper == "FALSE" {
		return "false"
	}

	// Try to clean up float representations.
	// If the value looks like "95.5000000001" due to float precision, try to normalize.
	if strings.Contains(v, ".") {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			// Format with full precision but trim trailing zeros.
			clean := strconv.FormatFloat(f, 'f', -1, 64)
			return clean
		}
	}

	return v
}

// Writer writes tabular data to an Excel (.xlsx) file.
type Writer struct {
	fs      afero.Fs
	path    string
	cfg     config
	file    *excelize.File
	sw      *excelize.StreamWriter
	sheet   string
	rowNum  int
	numCols int

	// pulseSchema is set by ExportJob via SetPulseSchema before
	// WriteHeader so the writer can apply scale-driven number formats
	// to decimal columns and use text encoding for point/h3 columns.
	pulseSchema *encoding.Schema
	// decimalStyleIDs caches per-column number-format style ids,
	// computed lazily on first write of a decimal column. The slice is
	// indexed by column position; -1 means no cached style yet.
	decimalStyleIDs []int
}

// SetPulseSchema records the source .pulse schema. Implements
// pio.SchemaAwareWriter so ExportJob can hand the writer typed values
// for decimal128 / point_f64 / h3_cell columns.
func (w *Writer) SetPulseSchema(s *encoding.Schema) {
	w.pulseSchema = s
}

// NewWriter creates an Excel writer targeting a filesystem path.
func NewWriter(fs afero.Fs, path string, opts ...Option) *Writer {
	w := &Writer{
		fs:   fs,
		path: path,
	}
	for _, o := range opts {
		o(&w.cfg)
	}
	return w
}

func (w *Writer) init() error {
	if w.file != nil {
		return nil
	}

	w.file = excelize.NewFile()

	// Resolve sheet name.
	if w.cfg.sheet != "" {
		w.sheet = w.cfg.sheet
		idx, err := w.file.NewSheet(w.sheet)
		if err != nil {
			return fmt.Errorf("excel.Writer: creating sheet %q: %w", w.sheet, err)
		}
		w.file.SetActiveSheet(idx)
		// Delete default sheet.
		defaultSheet := w.file.GetSheetName(0)
		if defaultSheet != w.sheet {
			w.file.DeleteSheet(defaultSheet)
		}
	} else {
		w.sheet = w.file.GetSheetName(0)
	}

	sw, err := w.file.NewStreamWriter(w.sheet)
	if err != nil {
		return fmt.Errorf("excel.Writer: creating stream writer: %w", err)
	}
	w.sw = sw
	w.rowNum = 0

	return nil
}

// WriteHeader writes column names as the first row.
func (w *Writer) WriteHeader(columns []string) error {
	if err := w.init(); err != nil {
		return err
	}
	w.numCols = len(columns)
	w.rowNum++

	row := make([]interface{}, len(columns))
	for i, c := range columns {
		row[i] = excelize.Cell{Value: c}
	}

	cell, _ := excelize.CoordinatesToCellName(1, w.rowNum)
	return w.sw.SetRow(cell, row)
}

// WriteRow writes a single data row. With a Pulse schema set the
// writer renders decimal128 cells as Excel numbers with a scale-driven
// "0.000…" format, point_f64 cells as "lon, lat" text, and h3_cell
// cells as 15-char hex text. Without a schema, values pass through
// excelize's default cell-value handling.
func (w *Writer) WriteRow(values []any) error {
	if w.sw == nil {
		if err := w.init(); err != nil {
			return err
		}
	}
	w.rowNum++

	row := make([]interface{}, len(values))
	for i, v := range values {
		row[i] = w.formatCell(i, v)
	}

	cell, _ := excelize.CoordinatesToCellName(1, w.rowNum)
	return w.sw.SetRow(cell, row)
}

// formatCell applies type-aware Excel cell rendering when a Pulse
// schema is present.
func (w *Writer) formatCell(col int, v any) excelize.Cell {
	if w.pulseSchema != nil && col < len(w.pulseSchema.Fields) {
		f := w.pulseSchema.Fields[col]
		switch f.Type {
		case encoding.FieldTypeDecimal128, encoding.FieldTypeNullableDecimal128:
			return w.decimalCell(col, f, v)
		case encoding.FieldTypePointF64:
			return excelize.Cell{Value: pointToText(v)}
		case encoding.FieldTypeH3Cell:
			return excelize.Cell{Value: h3ToText(v)}
		}
	}
	return excelize.Cell{Value: v}
}

// decimalCell renders a Decimal128 as a number cell with the field's
// scale-driven format string. Falls back to text for values that exceed
// float64 representable range or for nil / empty inputs.
func (w *Writer) decimalCell(col int, f encoding.Field, v any) excelize.Cell {
	var d encoding.Decimal128
	switch x := v.(type) {
	case nil:
		return excelize.Cell{Value: ""}
	case string:
		if x == "" {
			return excelize.Cell{Value: ""}
		}
		parsed, parsedScale, err := encoding.ParseDecimal128(x)
		if err != nil {
			return excelize.Cell{Value: x}
		}
		if parsedScale != f.Scale {
			parsed, err = parsed.Rescale(parsedScale, f.Scale)
			if err != nil {
				return excelize.Cell{Value: x}
			}
		}
		d = parsed
	case encoding.Decimal128:
		d = x
	default:
		return excelize.Cell{Value: fmt.Sprintf("%v", v)}
	}
	styleID := w.ensureDecimalStyle(col, f.Scale)
	return excelize.Cell{StyleID: styleID, Value: d.Float64(f.Scale)}
}

// ensureDecimalStyle materializes (and caches) a workbook style for
// decimal column `col` with a scale-driven number format.
func (w *Writer) ensureDecimalStyle(col int, scale uint8) int {
	if w.decimalStyleIDs == nil {
		w.decimalStyleIDs = make([]int, w.numCols)
		for i := range w.decimalStyleIDs {
			w.decimalStyleIDs[i] = -1
		}
	}
	if col < len(w.decimalStyleIDs) && w.decimalStyleIDs[col] != -1 {
		return w.decimalStyleIDs[col]
	}
	format := decimalNumberFormat(scale)
	id, err := w.file.NewStyle(&excelize.Style{NumFmt: 0, CustomNumFmt: &format})
	if err != nil {
		return 0
	}
	if col < len(w.decimalStyleIDs) {
		w.decimalStyleIDs[col] = id
	}
	return id
}

// decimalNumberFormat builds Excel's "0.000…" custom format for the
// given scale. Scale 0 returns "0".
func decimalNumberFormat(scale uint8) string {
	if scale == 0 {
		return "0"
	}
	return "0." + strings.Repeat("0", int(scale))
}

// pointToText renders a PointF64 as "lon, lat" (the WKT-natural order)
// or stringifies a passed-through string. nil yields empty.
func pointToText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case encoding.PointF64:
		return strconv.FormatFloat(x.Lon, 'g', -1, 64) + ", " + strconv.FormatFloat(x.Lat, 'g', -1, 64)
	case string:
		return x
	default:
		return fmt.Sprintf("%v", v)
	}
}

// h3ToText renders an H3Cell as the 15-char canonical hex string or
// passes through a hex string. nil yields empty.
func h3ToText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case encoding.H3Cell:
		return encoding.FormatH3CellHex(x)
	case uint64:
		return encoding.FormatH3CellHex(encoding.H3Cell(x))
	case string:
		return x
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Close flushes and writes the workbook to the target path.
func (w *Writer) Close() error {
	if w.sw != nil {
		if err := w.sw.Flush(); err != nil {
			return fmt.Errorf("excel.Writer: flushing stream: %w", err)
		}
	}
	if w.file == nil {
		return nil
	}

	if w.fs != nil && w.path != "" {
		buf, err := w.file.WriteToBuffer()
		if err != nil {
			return fmt.Errorf("excel.Writer: writing buffer: %w", err)
		}
		if err := afero.WriteFile(w.fs, w.path, buf.Bytes(), 0644); err != nil {
			return fmt.Errorf("excel.Writer: writing file: %w", err)
		}
	}

	return w.file.Close()
}
