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
	"github.com/frankbardon/pulse/internal/iocore"
	"github.com/frankbardon/pulse/types"
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

	// open replaces the workbook open (openWorkbook) — a test seam for
	// a parser that panics; nil is the production open.
	open func(data []byte) (*excelize.File, error)
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

func (r *Reader) init() (err error) {
	if r.file != nil {
		return nil
	}
	// The workbook parser is third-party code handed untrusted bytes: a
	// panic inside it is a malformed source, never a crash.
	defer iocore.RecoverDecode("excel", &err)
	defer func() {
		if err != nil && r.file != nil {
			_ = r.file.Close()
			r.file = nil
		}
	}()

	// Load the data.
	if r.data == nil {
		if r.fs == nil {
			return errors.NewCodedError(errors.DATA_FILE, "excel.Reader: no data source")
		}
		data, err := afero.ReadFile(r.fs, r.path)
		if err != nil {
			ce := iocore.SourceFileError("excel", fmt.Sprintf("excel.Reader: reading %s", r.path), err)
			ce.Details["path"] = r.path
			return ce
		}
		r.data = data
	}

	open := r.open
	if open == nil {
		open = openWorkbook
	}
	f, err := open(r.data)
	if err != nil {
		return iocore.SourceFileError("excel", "excel.Reader: opening workbook", err)
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
		return iocore.SourceFileError("excel", fmt.Sprintf("excel.Reader: reading sheet %q", r.sheet), err)
	}

	if err := checkGrid(allRows); err != nil {
		return err
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

// Grid bounds for checkGrid. An import materialises every data row at
// the header's width, so its cost is rows x header width whatever the
// sheet stores. Up to gridFloorCells of that grid is always accepted;
// past it, the grid may be at most gridSparsity times the cells the
// sheet actually holds.
const (
	gridFloorCells = 1 << 21
	gridSparsity   = 64
)

// checkGrid refuses a sheet whose import grid no stored cell backs. A
// cell reference alone (XFD1 in the header, A1048576 in the data)
// widens or lengthens the grid every row is padded to: a workbook of a
// few kilobytes could otherwise demand billions of cells — minutes of
// work and gigabytes of allocation for a sheet that is all blanks.
func checkGrid(rows [][]string) error {
	if len(rows) < 2 {
		return nil
	}
	var present int64
	for _, row := range rows {
		present += int64(len(row))
	}
	width, data := int64(len(rows[0])), int64(len(rows)-1)
	grid := width * data
	if grid <= gridFloorCells || grid <= gridSparsity*present {
		return nil
	}
	return errors.NewCodedErrorWithDetails(
		errors.DATA_FILE,
		fmt.Sprintf("excel.Reader: the sheet's %d data rows at its %d-column header width make %d cells, but it stores only %d: more than %dx sparser than its stored cells and over %d cells",
			data, width, grid, present, gridSparsity, gridFloorCells),
		map[string]any{"format": "excel", "rows": data, "columns": width, "cells": grid, "stored_cells": present},
	)
}

// maxDeflateRatio is DEFLATE's maximum expansion: one compressed byte
// decodes to at most 1032 bytes. archive/zip reads only STORE (1:1) and
// DEFLATE, and refuses an entry whose decoded size differs from the size
// its header declares — so no honest workbook declares more than
// maxDeflateRatio times its own length in total.
const maxDeflateRatio = 1032

// openWorkbook is the production workbook open. Its unzip limit is the
// most the source could honestly decode to: the parser sizes a buffer
// from each entry's DECLARED length, so a crafted header claiming
// gigabytes would otherwise be allocated before a byte is read.
func openWorkbook(data []byte) (*excelize.File, error) {
	limit := min(int64(len(data))*maxDeflateRatio, int64(excelize.UnzipSizeLimit))
	limit = max(limit, 1)
	return excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		UnzipSizeLimit:    limit,
		UnzipXMLSizeLimit: min(limit, int64(excelize.StreamChunkSize)),
	})
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

// SheetNames returns the list of sheet names on the workbook. Overlay
// sheets ride alongside the host data sheet with the OverlaySheetPrefix
// reserved namespace ("__overlay_*"); callers introspecting the overlay
// layout filter by HasPrefix on the prefix constant.
func (r *Reader) SheetNames() ([]string, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	return r.file.GetSheetList(), nil
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
			if err == iocore.ErrStopIteration() {
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
	fs   afero.Fs
	path string
	// toBuffer marks a NewWriterToBuffer writer: Close serialises the
	// workbook into out instead of onto a filesystem path.
	toBuffer bool
	out      []byte
	cfg      config
	file     *excelize.File
	sw       *excelize.StreamWriter
	sheet    string
	rowNum   int
	numCols  int

	// pulseSchema is set by ExportJob via SetPulseSchema before
	// WriteHeader so the writer can apply scale-driven number formats
	// to decimal columns and use text encoding for point/h3 columns.
	pulseSchema *encoding.Schema
	// exportCols is the export's explicit column plan
	// (iocore.ColumnAwareWriter); colFields is the field typing each
	// header column, resolved at WriteHeader from it — or, without a
	// plan, from the pulseSchema field of the same name. Never the
	// schema's i-th field: projection and label augment move columns
	// off their field's index.
	exportCols []iocore.ExportColumn
	colFields  []*encoding.Field
	// decimalStyleIDs caches per-column number-format style ids,
	// computed lazily on first write of a decimal column. The slice is
	// indexed by column position; -1 means no cached style yet.
	decimalStyleIDs []int

	// overlays carries the Response.Overlays layers the export pipeline
	// hands to the writer via SetOverlays before WriteHeader. When
	// non-nil + non-empty, the writer adds one sheet per layer named
	// "__overlay_<layer_name>" (truncated + disambiguated per
	// research/export-embedding-shape.md § 5) alongside the host data
	// sheet at Close time. nil OR empty produces byte-identical Excel
	// output to a pre-overlay export — no overlay sheets land on the
	// workbook (the host sheet is unchanged).
	overlays []*types.OverlayLayer
}

// SetPulseSchema records the source .pulse schema. Implements
// iocore.SchemaAwareWriter so ExportJob can hand the writer typed values
// for decimal128 columns.
func (w *Writer) SetPulseSchema(s *encoding.Schema) {
	w.pulseSchema = s
}

// SetExportColumns implements iocore.ColumnAwareWriter: column i is typed
// from cols[i].Field, whatever source field index it came from.
func (w *Writer) SetExportColumns(cols []iocore.ExportColumn) {
	w.exportCols = cols
}

// SetOverlays records the Response.Overlays layers the export pipeline
// wants the writer to embed in the workbook. Each layer lands on a
// dedicated sheet named "__overlay_<layer_name>" alongside the host
// data sheet per research/export-embedding-shape.md § 5 (one sheet per
// layer; the double-underscore prefix reserves the namespace). nil or
// empty layers leave the workbook byte-identical to a pre-overlay
// export (no overlay sheets land). Implements iocore.OverlayAwareWriter.
//
// Sheet emission happens at Close() time so the host sheet's stream
// writer can finish writing before the overlay sheets are appended.
// Must be called BEFORE Close.
func (w *Writer) SetOverlays(layers []*types.OverlayLayer) {
	w.overlays = layers
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

// NewWriterToBuffer creates an Excel writer with no filesystem target; the
// workbook bytes are read back with [Writer.Bytes] after Close. It is the
// fs-free path every other writable adapter offers, so the io factory's
// NewWriterToBuffer covers every writable format.
func NewWriterToBuffer(opts ...Option) *Writer {
	w := &Writer{toBuffer: true}
	for _, o := range opts {
		o(&w.cfg)
	}
	return w
}

// Bytes returns the serialised workbook of a NewWriterToBuffer writer
// after Close. It is nil before Close, for a writer that wrote nothing,
// and for a writer targeting a filesystem path.
func (w *Writer) Bytes() []byte { return w.out }

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
	w.colFields = iocore.ColumnFields(w.exportCols, w.pulseSchema, columns)
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
// "0.000…" format. Without a schema, values pass through excelize's
// default cell-value handling.
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
	if col < len(w.colFields) && w.colFields[col] != nil {
		f := w.colFields[col]
		switch f.Type {
		case encoding.FieldTypeDecimal128:
			return w.decimalCell(col, *f, v)
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

// Close flushes and writes the workbook to the target path. When
// SetOverlays handed the writer a non-empty layer slice the writer
// appends one "__overlay_<layer_name>" sheet per layer after the host
// sheet's stream writer is flushed and before WriteToBuffer; see
// writeOverlaySheets in overlay.go for the layout rules.
func (w *Writer) Close() error {
	if w.sw != nil {
		if err := w.sw.Flush(); err != nil {
			return fmt.Errorf("excel.Writer: flushing stream: %w", err)
		}
	}
	if w.file == nil {
		return nil
	}

	// Emit overlay sheets AFTER the host stream writer has flushed but
	// BEFORE the workbook is serialised to disk. nil / empty overlays
	// short-circuits inside writeOverlaySheets so the workbook stays
	// byte-identical to a pre-overlay export.
	if err := w.writeOverlaySheets(); err != nil {
		return err
	}

	if w.toBuffer {
		buf, err := w.file.WriteToBuffer()
		if err != nil {
			return fmt.Errorf("excel.Writer: writing buffer: %w", err)
		}
		w.out = buf.Bytes()
	} else if w.fs != nil && w.path != "" {
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

// Discard releases the workbook and any excelize temp files WITHOUT
// writing the target. iocore.DiscardableWriter.
//
// The excelize StreamWriter spills its buffer to an os.CreateTemp file
// once it passes excelize.StreamChunkSize, and excelize.File.Close is
// the only thing that removes those files — so an export that errors
// after a large number of rows, on a path that correctly refuses to
// Close (closing would emit a zero-row workbook next to the error),
// used to leave one behind. Discard is that path's release.
//
// It leaves the writer inert: the subsequent Close the happy path would
// have run becomes a no-op, so a discarded export can never resurrect a
// target file.
func (w *Writer) Discard() error {
	if w.file == nil {
		w.sw = nil
		return nil
	}
	f := w.file
	w.file = nil
	w.sw = nil
	return f.Close()
}

// Ensure interfaces are satisfied at compile time.
var _ iocore.Reader = (*Reader)(nil)
var _ iocore.ResetReader = (*Reader)(nil)
var _ iocore.Writer = (*Writer)(nil)
var _ iocore.SchemaAwareWriter = (*Writer)(nil)
var _ iocore.DiscardableWriter = (*Writer)(nil)
var _ iocore.OverlayAwareWriter = (*Writer)(nil)
