// Package parquet provides Parquet import and export for the pulse I/O pipeline.
package parquet

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/file"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
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
func (r *Reader) ReadHeader() ([]string, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	if r.header != nil {
		return r.header, nil
	}

	numFields := r.arrowSc.NumFields()
	r.header = make([]string, numFields)
	for i := 0; i < numFields; i++ {
		r.header[i] = r.arrowSc.Field(i).Name
	}
	r.started = true
	return r.header, nil
}

// ReadRows streams rows; calls fn for each row.
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
	numCols := int(tbl.NumCols())
	numRows := int(tbl.NumRows())

	// Build column readers - each column may have multiple chunks.
	type colChunks struct {
		chunks []arrow.Array
	}
	cols := make([]colChunks, numCols)
	for c := 0; c < numCols; c++ {
		col := tbl.Column(c)
		chunks := col.Data().Chunks()
		cols[c] = colChunks{chunks: chunks}
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
			return formatArrowValue(chunk, offset)
		}
		offset -= chunk.Len()
	}
	return ""
}

// formatArrowValue converts an Arrow array element to a string.
func formatArrowValue(arr arrow.Array, idx int) string {
	switch a := arr.(type) {
	case *array.Uint8:
		return strconv.FormatUint(uint64(a.Value(idx)), 10)
	case *array.Uint16:
		return strconv.FormatUint(uint64(a.Value(idx)), 10)
	case *array.Uint32:
		return strconv.FormatUint(uint64(a.Value(idx)), 10)
	case *array.Uint64:
		return strconv.FormatUint(a.Value(idx), 10)
	case *array.Int8:
		return strconv.FormatInt(int64(a.Value(idx)), 10)
	case *array.Int16:
		return strconv.FormatInt(int64(a.Value(idx)), 10)
	case *array.Int32:
		return strconv.FormatInt(int64(a.Value(idx)), 10)
	case *array.Int64:
		return strconv.FormatInt(a.Value(idx), 10)
	case *array.Float32:
		return strconv.FormatFloat(float64(a.Value(idx)), 'f', -1, 32)
	case *array.Float64:
		return strconv.FormatFloat(a.Value(idx), 'f', -1, 64)
	case *array.Boolean:
		if a.Value(idx) {
			return "true"
		}
		return "false"
	case *array.String:
		return a.Value(idx)
	case *array.Date32:
		days := int64(a.Value(idx))
		t := time.Unix(days*86400, 0).UTC()
		return t.Format("2006-01-02")
	case *array.Date64:
		ms := int64(a.Value(idx))
		t := time.Unix(ms/1000, (ms%1000)*1e6).UTC()
		return t.Format("2006-01-02")
	case *array.Dictionary:
		// Dictionary-encoded: resolve to the string value.
		dict := a.Dictionary()
		index := a.GetValueIndex(idx)
		if strDict, ok := dict.(*array.String); ok {
			return strDict.Value(index)
		}
		return fmt.Sprintf("%v", index)
	default:
		// Fallback: use the String representation from the array.
		return fmt.Sprintf("%v", a.GetOneForMarshal(idx))
	}
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
		ft := arrowTypeToPulse(af.Type, af.Nullable)
		fields[i] = encoding.Field{
			Name:         af.Name,
			Type:         ft,
			ByteOffset:   byteOffset,
			CsvColumnIdx: i,
		}
		byteOffset += ft.ByteSize()
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
// Called once, lazily, before the first batch is buffered.
func (w *Writer) initWriter() error {
	if w.pw != nil {
		return nil
	}

	w.alloc = memory.NewGoAllocator()

	// Arrow schema: all columns as string. Built once and reused for every
	// row group to avoid repeated schema construction.
	arrowFields := make([]arrow.Field, len(w.columns))
	for i, name := range w.columns {
		arrowFields[i] = arrow.Field{Name: name, Type: arrow.BinaryTypes.String}
	}
	w.sc = arrow.NewSchema(arrowFields, nil)

	w.bldr = array.NewRecordBuilder(w.alloc, w.sc)
	w.strBs = make([]*array.StringBuilder, len(w.columns))
	for i := range w.columns {
		w.strBs[i] = w.bldr.Field(i).(*array.StringBuilder)
	}

	w.buf.Reset()
	props := pq.NewWriterProperties(pq.WithDictionaryDefault(true))
	arrowProps := pqarrow.DefaultWriterProps()
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

// WriteRow writes a single row of values. Values are stringified. Rows are
// accumulated in an Arrow RecordBuilder; once the batch reaches
// defaultRowGroupSize, it is flushed as a row group and the builder is
// reset. Row-group boundaries fall between rows — never inside a row.
func (w *Writer) WriteRow(values []any) error {
	if w.columns == nil {
		return fmt.Errorf("parquet.Writer: WriteRow called before WriteHeader")
	}
	if err := w.initWriter(); err != nil {
		return err
	}

	// Append the row to the current batch. Each column's StringBuilder gets
	// exactly one value, so all columns stay aligned and the row is atomic.
	for c := 0; c < len(w.columns); c++ {
		var s string
		if c < len(values) {
			s = fmt.Sprintf("%v", values[c])
		}
		w.strBs[c].Append(s)
	}
	w.pending++

	if w.pending >= defaultRowGroupSize {
		return w.flushBatch()
	}
	return nil
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

// arrowTypeToPulse maps an Arrow data type to a Pulse FieldType.
func arrowTypeToPulse(dt arrow.DataType, nullable bool) encoding.FieldType {
	switch dt.ID() {
	case arrow.UINT8:
		if nullable {
			return encoding.FieldTypeNullableU8
		}
		return encoding.FieldTypeU8
	case arrow.UINT16:
		if nullable {
			return encoding.FieldTypeNullableU16
		}
		return encoding.FieldTypeU16
	case arrow.UINT32:
		return encoding.FieldTypeU32
	case arrow.UINT64:
		return encoding.FieldTypeU64
	case arrow.INT8:
		if nullable {
			return encoding.FieldTypeNullableU8
		}
		return encoding.FieldTypeU8
	case arrow.INT16:
		if nullable {
			return encoding.FieldTypeNullableU16
		}
		return encoding.FieldTypeU16
	case arrow.INT32:
		return encoding.FieldTypeU32
	case arrow.INT64:
		return encoding.FieldTypeU64
	case arrow.FLOAT32:
		return encoding.FieldTypeF32
	case arrow.FLOAT64:
		return encoding.FieldTypeF64
	case arrow.BOOL:
		if nullable {
			return encoding.FieldTypeNullableBool
		}
		return encoding.FieldTypePackedBool
	case arrow.DATE32:
		return encoding.FieldTypeDate
	case arrow.DATE64:
		return encoding.FieldTypeDate
	case arrow.STRING, arrow.LARGE_STRING, arrow.BINARY, arrow.LARGE_BINARY:
		return encoding.FieldTypeCategoricalU8
	case arrow.DICTIONARY:
		// Dictionary-encoded -> categorical.
		return encoding.FieldTypeCategoricalU8
	default:
		// Default to f64 for unknown types.
		return encoding.FieldTypeF64
	}
}

// pulseTypeToArrow maps a Pulse FieldType to an Arrow DataType.
func pulseTypeToArrow(ft encoding.FieldType) arrow.DataType {
	switch ft {
	case encoding.FieldTypeU8:
		return arrow.PrimitiveTypes.Uint8
	case encoding.FieldTypeU16:
		return arrow.PrimitiveTypes.Uint16
	case encoding.FieldTypeU32:
		return arrow.PrimitiveTypes.Uint32
	case encoding.FieldTypeU64:
		return arrow.PrimitiveTypes.Uint64
	case encoding.FieldTypeF32:
		return arrow.PrimitiveTypes.Float32
	case encoding.FieldTypeF64:
		return arrow.PrimitiveTypes.Float64
	case encoding.FieldTypeDate:
		return arrow.FixedWidthTypes.Date32
	case encoding.FieldTypePackedBool:
		return arrow.FixedWidthTypes.Boolean
	case encoding.FieldTypeNullableBool:
		return arrow.FixedWidthTypes.Boolean
	case encoding.FieldTypeNullableU4:
		return arrow.PrimitiveTypes.Uint8
	case encoding.FieldTypeNullableU8:
		return arrow.PrimitiveTypes.Uint8
	case encoding.FieldTypeNullableU16:
		return arrow.PrimitiveTypes.Uint16
	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
		return arrow.BinaryTypes.String
	default:
		return arrow.PrimitiveTypes.Float64
	}
}

// Ensure interfaces are satisfied at compile time.
var _ pio.Reader = (*Reader)(nil)
var _ pio.ResetReader = (*Reader)(nil)
var _ pio.Writer = (*Writer)(nil)

// Suppress unused import warnings.
var _ = math.MaxFloat32
