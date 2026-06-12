// Package parquet provides Parquet import and export for the pulse I/O pipeline.
package parquet

import (
	"bytes"
	"context"
	"fmt"
	"math"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/file"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	parrow "github.com/frankbardon/pulse/io/arrow"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"

	pq "github.com/apache/arrow-go/v18/parquet"
)

// Option configures a Reader or Writer.
type Option func(*options)

type options struct {
	workers int
}

// WithWorkers sets the number of parallel workers for row-group decoding.
func WithWorkers(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.workers = n
		}
	}
}

func defaultOptions() options {
	return options{workers: 1}
}

// ---------- Reader ----------

// Reader reads Parquet data and implements the io.Reader and io.ResetReader interfaces.
type Reader struct {
	data    []byte
	fs      afero.Fs
	path    string
	opts    options
	header  []string
	started bool

	// Cached arrow table from reading.
	tbl     arrow.Table
	pqFile  *file.Reader
	arrowSc *arrow.Schema
}

// NewReader creates a Parquet reader from a filesystem path.
func NewReader(fs afero.Fs, path string, opts ...Option) *Reader {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}
	return &Reader{fs: fs, path: path, opts: o}
}

// NewReaderFromBytes creates a Parquet reader from raw bytes.
func NewReaderFromBytes(data []byte, opts ...Option) *Reader {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}
	return &Reader{data: data, opts: o}
}

func (r *Reader) init() error {
	if r.arrowSc != nil {
		return nil
	}
	if r.data == nil {
		if r.fs == nil {
			return fmt.Errorf("parquet.Reader: no data source")
		}
		data, err := afero.ReadFile(r.fs, r.path)
		if err != nil {
			return fmt.Errorf("parquet.Reader: reading %s: %w", r.path, err)
		}
		r.data = data
	}

	br := bytes.NewReader(r.data)
	pqFile, err := file.NewParquetReader(br)
	if err != nil {
		return fmt.Errorf("parquet.Reader: opening parquet: %w", err)
	}
	r.pqFile = pqFile

	arrowReader, err := pqarrow.NewFileReader(pqFile, pqarrow.ArrowReadProperties{
		Parallel: r.opts.workers > 1,
	}, memory.NewGoAllocator())
	if err != nil {
		return fmt.Errorf("parquet.Reader: creating arrow reader: %w", err)
	}

	sc, err := arrowReader.Schema()
	if err != nil {
		return fmt.Errorf("parquet.Reader: reading schema: %w", err)
	}
	r.arrowSc = sc

	// Read the full table. Arrow-Go handles internal parallelism via the
	// Parallel flag in ArrowReadProperties — no manual row-group dispatch needed.
	tbl, err := arrowReader.ReadTable(context.Background())
	if err != nil {
		return fmt.Errorf("parquet.Reader: reading table: %w", err)
	}
	r.tbl = tbl

	r.header = nil
	r.started = false
	return nil
}

// ReadHeader returns column names from the Parquet schema.
//
// A top-level "overlays" field (the overlay column-family sidecar
// emitted by SetOverlays per research/export-embedding-shape.md § 4.1)
// is filtered out of the returned header — host-data consumers that
// were written before overlay embedding existed still see the original
// column list. Callers that want the embedded overlays read them via
// ReadOverlays.
func (r *Reader) ReadHeader() ([]string, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	if r.header != nil {
		return r.header, nil
	}

	numFields := r.arrowSc.NumFields()
	r.header = make([]string, 0, numFields)
	for i := 0; i < numFields; i++ {
		name := r.arrowSc.Field(i).Name
		if name == parrow.OverlaysFieldName {
			continue
		}
		r.header = append(r.header, name)
	}
	r.started = true
	return r.header, nil
}

// ReadRows streams rows; calls fn for each row.
//
// The top-level "overlays" sidecar column (when present) is filtered
// from the row tuple — host-data row consumers see only the host
// columns. Use ReadOverlays to extract the embedded overlay layers.
func (r *Reader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	if err := r.init(); err != nil {
		return err
	}
	if !r.started {
		if _, err := r.ReadHeader(); err != nil {
			return err
		}
	}

	tbl := r.tbl
	numTotal := int(tbl.NumCols())
	numRows := int(tbl.NumRows())

	// Map output column index → source table column index, skipping
	// the overlays sidecar field. Keeps the per-row hot path free of
	// per-row name comparisons.
	hostColIdx := make([]int, 0, numTotal)
	for c := 0; c < numTotal; c++ {
		if r.arrowSc.Field(c).Name == parrow.OverlaysFieldName {
			continue
		}
		hostColIdx = append(hostColIdx, c)
	}
	numCols := len(hostColIdx)

	// Build column readers - each column may have multiple chunks.
	type colChunks struct {
		chunks []arrow.Array
	}
	cols := make([]colChunks, numCols)
	for out, src := range hostColIdx {
		col := tbl.Column(src)
		chunks := col.Data().Chunks()
		cols[out] = colChunks{chunks: chunks}
	}

	// Iterate over rows.
	row := make([]string, numCols)
	rowIdx := 0
	for rowIdx < numRows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		for c := 0; c < numCols; c++ {
			row[c] = getValueAsString(cols[c].chunks, rowIdx)
		}

		if err := fn(row); err != nil {
			if err == pio.ErrStopIteration() {
				return nil
			}
			return err
		}
		rowIdx++
	}

	return nil
}

// getValueAsString extracts a value from chunked arrays at the given row index.
func getValueAsString(chunks []arrow.Array, rowIdx int) string {
	// Find which chunk and local index.
	offset := rowIdx
	for _, chunk := range chunks {
		if offset < chunk.Len() {
			if chunk.IsNull(offset) {
				return ""
			}
			return parrow.FormatValue(chunk, offset)
		}
		offset -= chunk.Len()
	}
	return ""
}

// Close releases underlying resources.
func (r *Reader) Close() error {
	if r.tbl != nil {
		r.tbl.Release()
		r.tbl = nil
	}
	if r.pqFile != nil {
		r.pqFile.Close()
		r.pqFile = nil
	}
	r.arrowSc = nil
	return nil
}

// Reset rewinds the reader to the beginning.
func (r *Reader) Reset() error {
	if r.tbl != nil {
		r.tbl.Release()
		r.tbl = nil
	}
	if r.pqFile != nil {
		r.pqFile.Close()
		r.pqFile = nil
	}
	r.arrowSc = nil
	r.header = nil
	r.started = false
	return nil
}

// InferPulseSchema builds a Pulse schema from the Parquet file's Arrow schema.
func (r *Reader) InferPulseSchema() (*encoding.Schema, error) {
	if err := r.init(); err != nil {
		return nil, err
	}

	numFields := r.arrowSc.NumFields()
	fields := make([]encoding.Field, numFields)
	byteOffset := 0

	for i := 0; i < numFields; i++ {
		af := r.arrowSc.Field(i)
		if pf, ok := parrow.PulseFieldFromArrow(af); ok {
			pf.ByteOffset = byteOffset
			pf.CsvColumnIdx = i
			fields[i] = pf
			byteOffset += pf.Type.ByteSize()
			continue
		}
		ft := parrow.TypeToPulse(af.Type)
		fields[i] = encoding.Field{
			Name:         af.Name,
			Type:         ft,
			Nullable:     af.Nullable,
			ByteOffset:   byteOffset,
			CsvColumnIdx: i,
		}
		if ft.IsBitPacked() {
			byteOffset++
		} else {
			byteOffset += ft.ByteSize()
		}
	}

	return &encoding.Schema{Fields: fields}, nil
}

// ---------- Writer ----------

// defaultRowGroupSize is the target number of rows per Parquet row group.
// Each batch of this many rows is flushed as its own row group via the
// pqarrow FileWriter.Write API. Hardcoded for now; not user-configurable.
const defaultRowGroupSize = 64 * 1024

// Writer writes tabular data as Parquet, flushing rows in row-group sized
// batches to bound peak memory usage on large exports.
type Writer struct {
	fs      afero.Fs
	path    string
	buf     bytes.Buffer
	columns []string

	// pulseSchema is set by ExportJob through SetPulseSchema before
	// WriteHeader so the writer can build native typed Arrow columns
	// for decimal128 / point_f64 / h3_cell. nil falls back to the
	// legacy all-string layout.
	pulseSchema *encoding.Schema

	// overlays carries the Response.Overlays layers the export
	// pipeline hands to the writer via SetOverlays before WriteHeader.
	// When non-nil + non-empty, the writer adds a top-level "overlays"
	// field (LIST<STRUCT>, the shared Arrow schema per
	// research/export-embedding-shape.md § 4) to the Arrow schema and
	// emits the layers ONCE in the first record-batch / row-group
	// per § 4.2. nil OR empty produces byte-identical Parquet output
	// to a pre-overlay export (the field is OMITTED from the schema,
	// not present-with-empty-list).
	overlays []*types.OverlayLayer

	// overlaysEmitted tracks whether the writer has flushed the
	// populated overlays list in the first record-batch. Subsequent
	// rows / batches emit an empty list per § 4.2.
	overlaysEmitted bool

	// Lazily-initialized parquet writer state. Built on first WriteRow once
	// the schema (column names) is known. Reused across all batches.
	alloc   memory.Allocator
	sc      *arrow.Schema
	pw      *pqarrow.FileWriter
	bldr    *array.RecordBuilder
	strBs   []*array.StringBuilder // cached per-column builders
	pending int                    // rows currently buffered in bldr
	closed  bool
}

// SetPulseSchema records the source .pulse schema so subsequent
// initWriter can build native typed Parquet columns. Implements
// pio.SchemaAwareWriter.
func (w *Writer) SetPulseSchema(s *encoding.Schema) {
	w.pulseSchema = s
}

// NewWriter creates a Parquet writer targeting a filesystem path.
func NewWriter(fs afero.Fs, path string, opts ...Option) *Writer {
	return &Writer{fs: fs, path: path}
}

// NewWriterToBuffer creates a Parquet writer that writes to an internal buffer.
func NewWriterToBuffer(opts ...Option) *Writer {
	return &Writer{}
}

// WriteHeader writes column names.
func (w *Writer) WriteHeader(columns []string) error {
	w.columns = make([]string, len(columns))
	copy(w.columns, columns)
	return nil
}

// initWriter builds the Arrow schema, RecordBuilder, and pqarrow FileWriter.
// Called once, lazily, before the first batch is buffered. When a Pulse
// schema was supplied via SetPulseSchema, native Arrow types are used
// per FieldFromPulse — Decimal128(p,s), FixedSizeBinary(16) with
// pulse:type metadata, UInt64 with pulse:type metadata. Otherwise every
// column is Arrow String.
//
// When SetOverlays handed the writer a non-empty []*OverlayLayer, the
// schema gains one trailing field "overlays" of type LIST<STRUCT> per
// research/export-embedding-shape.md § 4 (shared verbatim with the
// Arrow IPC adapter via the io/arrow.OverlaysArrowField helper). The
// "overlays" field is OMITTED entirely when overlays is nil / empty so
// the byte layout of an overlay-free export stays identical to the
// pre-overlay path.
func (w *Writer) initWriter() error {
	if w.pw != nil {
		return nil
	}

	w.alloc = memory.NewGoAllocator()

	hostFieldCount := len(w.columns)
	hasOverlays := len(w.overlays) > 0

	totalFields := hostFieldCount
	if hasOverlays {
		totalFields++
	}
	arrowFields := make([]arrow.Field, totalFields)
	if w.pulseSchema != nil && len(w.pulseSchema.Fields) == len(w.columns) {
		for i, pf := range w.pulseSchema.Fields {
			arrowFields[i] = parrow.FieldFromPulse(pf)
			arrowFields[i].Name = w.columns[i]
		}
	} else {
		for i, name := range w.columns {
			arrowFields[i] = arrow.Field{Name: name, Type: arrow.BinaryTypes.String}
		}
	}
	if hasOverlays {
		arrowFields[hostFieldCount] = parrow.OverlaysArrowField()
	}
	w.sc = arrow.NewSchema(arrowFields, nil)

	w.bldr = array.NewRecordBuilder(w.alloc, w.sc)
	w.strBs = make([]*array.StringBuilder, hostFieldCount)
	for i := 0; i < hostFieldCount; i++ {
		if sb, ok := w.bldr.Field(i).(*array.StringBuilder); ok {
			w.strBs[i] = sb
		}
	}

	w.buf.Reset()
	props := pq.NewWriterProperties(pq.WithDictionaryDefault(true))
	// Persist the Arrow schema as a base64-encoded metadata blob so that
	// Field-level metadata (pulse:type, pulse:h3_resolution) survives the
	// Parquet round trip. Without this option, pqarrow lifts only the
	// physical Parquet types on read and the extension hints disappear.
	arrowProps := pqarrow.NewArrowWriterProperties(pqarrow.WithStoreSchema())
	pw, err := pqarrow.NewFileWriter(w.sc, &w.buf, props, arrowProps)
	if err != nil {
		return fmt.Errorf("parquet.Writer: creating file writer: %w", err)
	}
	w.pw = pw
	return nil
}

// flushBatch builds an arrow.Record from the buffered rows and writes it as a
// single row group via pqarrow.FileWriter.Write (which always emits at least
// one row group per call). Resets the builder afterwards. No-op if no rows
// are pending.
func (w *Writer) flushBatch() error {
	if w.pending == 0 {
		return nil
	}
	rec := w.bldr.NewRecordBatch()
	if err := w.pw.Write(rec); err != nil {
		rec.Release()
		return fmt.Errorf("parquet.Writer: writing record: %w", err)
	}
	rec.Release()
	w.pending = 0
	return nil
}

// WriteRow writes a single row of values. With a Pulse schema set the
// row dispatches per-column to the native typed builder (Decimal128,
// FixedSizeBinary, UInt64). Without a schema every value is stringified
// and appended to a StringBuilder. Rows accumulate in an Arrow
// RecordBuilder; once the batch reaches defaultRowGroupSize it is
// flushed as a row group and the builder is reset.
//
// When SetOverlays handed the writer non-empty layers, the row also
// appends to the trailing overlays LIST<STRUCT> column. Row 0 of the
// first record-batch / row-group carries the populated layer list;
// every subsequent row (including subsequent batches / row-groups)
// carries an empty list per research/export-embedding-shape.md § 4.2
// "emit once" optimisation.
func (w *Writer) WriteRow(values []any) error {
	if w.columns == nil {
		return fmt.Errorf("parquet.Writer: WriteRow called before WriteHeader")
	}
	if err := w.initWriter(); err != nil {
		return err
	}

	for c := 0; c < len(w.columns); c++ {
		var v any
		if c < len(values) {
			v = values[c]
		}
		if err := w.appendCell(c, v); err != nil {
			return err
		}
	}
	if err := w.appendOverlayCell(); err != nil {
		return err
	}
	w.pending++

	if w.pending >= defaultRowGroupSize {
		return w.flushBatch()
	}
	return nil
}

// appendCell appends a single cell value to column c's builder, picking
// the typed builder when the source schema declared a non-string type.
func (w *Writer) appendCell(c int, v any) error {
	if w.pulseSchema != nil && c < len(w.pulseSchema.Fields) {
		f := w.pulseSchema.Fields[c]
		switch f.Type {
		case encoding.FieldTypeDecimal128:
			return parrow.AppendDecimal128(w.bldr.Field(c), f, v)
		}
	}
	if w.strBs[c] != nil {
		var s string
		if v != nil {
			s = fmt.Sprintf("%v", v)
		}
		w.strBs[c].Append(s)
		return nil
	}
	if v == nil {
		w.bldr.Field(c).AppendNull()
		return nil
	}
	return w.bldr.Field(c).AppendValueFromString(fmt.Sprintf("%v", v))
}

// Close flushes any remaining buffered rows as a final row group, closes the
// underlying parquet writer, and writes the file to the filesystem if a path
// was configured.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	if w.columns == nil {
		// No header was ever written; preserve the empty-output behavior.
		return nil
	}

	// If no rows were ever written, we still emit a schema-only file to
	// preserve the prior behavior of Close after WriteHeader.
	if w.pw == nil {
		if err := w.initWriter(); err != nil {
			return err
		}
		// Emit one empty record so the file has a valid (empty) row group.
		rec := w.bldr.NewRecordBatch()
		if err := w.pw.Write(rec); err != nil {
			rec.Release()
			return fmt.Errorf("parquet.Writer: writing empty record: %w", err)
		}
		rec.Release()
	} else {
		if err := w.flushBatch(); err != nil {
			return err
		}
	}

	if err := w.pw.Close(); err != nil {
		return fmt.Errorf("parquet.Writer: closing: %w", err)
	}
	if w.bldr != nil {
		w.bldr.Release()
		w.bldr = nil
	}
	w.pw = nil

	if w.fs != nil && w.path != "" {
		return afero.WriteFile(w.fs, w.path, w.buf.Bytes(), 0644)
	}
	return nil
}

// Bytes returns the buffered Parquet output.
func (w *Writer) Bytes() []byte {
	return w.buf.Bytes()
}

// ---------- Type Mapping ----------
//
// Type-mapping helpers live in io/arrow as parrow.TypeToPulse and
// parrow.TypeFromPulse so the Parquet and Arrow IPC paths share a single
// source of truth. The thin wrappers below preserve the historical local
// names (used by parquet's tests) without re-implementing the logic.

// arrowTypeToPulse delegates to the shared io/arrow type map. Nullability
// is carried by encoding.Field.Nullable, not by the type itself; this
// helper exists for backward source-level symmetry with the older
// signature and ignores the nullable argument.
func arrowTypeToPulse(dt arrow.DataType, _ bool) encoding.FieldType {
	return parrow.TypeToPulse(dt)
}

// pulseTypeToArrow delegates to the shared io/arrow type map.
func pulseTypeToArrow(ft encoding.FieldType) arrow.DataType {
	return parrow.TypeFromPulse(ft)
}

// Ensure interfaces are satisfied at compile time.
var _ pio.Reader = (*Reader)(nil)
var _ pio.ResetReader = (*Reader)(nil)
var _ pio.Writer = (*Writer)(nil)
var _ pio.SchemaAwareWriter = (*Writer)(nil)
var _ pio.OverlayAwareWriter = (*Writer)(nil)

// Suppress unused import warnings.
var _ = math.MaxFloat32
