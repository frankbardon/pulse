package arrow

import (
	"bytes"
	"context"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
)

// ---------- Reader ----------

// Reader reads Arrow IPC (Feather V2) data and implements pio.Reader and
// pio.ResetReader. The full file is materialized in memory at init time —
// see the package doc for the rationale.
type Reader struct {
	data    []byte
	fs      afero.Fs
	path    string
	header  []string
	started bool

	// Cached arrow state from init.
	fileReader *ipc.FileReader
	arrowSc    *arrow.Schema
	batches    []arrow.RecordBatch
}

// NewReader creates an Arrow IPC reader that loads from the given filesystem
// path on first read. The file is not opened until ReadHeader, ReadRows, or
// InferPulseSchema is called.
func NewReader(fs afero.Fs, path string) *Reader {
	return &Reader{fs: fs, path: path}
}

// NewReaderFromBytes creates an Arrow IPC reader over an in-memory byte
// slice. The slice is not copied; callers must not mutate it after handing
// it to the reader.
func NewReaderFromBytes(data []byte) *Reader {
	return &Reader{data: data}
}

func (r *Reader) init() error {
	if r.arrowSc != nil {
		return nil
	}
	if r.data == nil {
		if r.fs == nil {
			return fmt.Errorf("arrow.Reader: no data source")
		}
		data, err := afero.ReadFile(r.fs, r.path)
		if err != nil {
			return fmt.Errorf("arrow.Reader: reading %s: %w", r.path, err)
		}
		r.data = data
	}

	// bytes.Reader implements ReadAtSeeker, which is what ipc.NewFileReader
	// requires (the file format is footer-indexed and needs random access).
	br := bytes.NewReader(r.data)
	fr, err := ipc.NewFileReader(br, ipc.WithAllocator(memory.NewGoAllocator()))
	if err != nil {
		return fmt.Errorf("arrow.Reader: opening arrow file: %w", err)
	}
	r.fileReader = fr
	r.arrowSc = fr.Schema()

	// Materialize all record batches into a slice. We use RecordBatchAt rather
	// than RecordBatch because the latter releases the previously returned
	// batch on the next call, which would invalidate every batch we cached
	// before the last one. RecordBatchAt transfers ownership to the caller.
	n := fr.NumRecords()
	r.batches = make([]arrow.RecordBatch, 0, n)
	for i := 0; i < n; i++ {
		rec, err := fr.RecordBatchAt(i)
		if err != nil {
			return fmt.Errorf("arrow.Reader: reading batch %d: %w", i, err)
		}
		r.batches = append(r.batches, rec)
	}

	r.header = nil
	r.started = false
	return nil
}

// ReadHeader returns column names from the Arrow schema.
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

// ReadRows iterates rows across all materialized record batches, calling fn
// for each row. Null cells produce empty strings.
func (r *Reader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	if err := r.init(); err != nil {
		return err
	}
	if !r.started {
		if _, err := r.ReadHeader(); err != nil {
			return err
		}
	}

	numCols := r.arrowSc.NumFields()
	row := make([]string, numCols)

	for _, batch := range r.batches {
		nRows := int(batch.NumRows())
		// Snapshot the column arrays for this batch up front to avoid
		// re-fetching on every row.
		cols := make([]arrow.Array, numCols)
		for c := 0; c < numCols; c++ {
			cols[c] = batch.Column(c)
		}

		for i := 0; i < nRows; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			for c := 0; c < numCols; c++ {
				if cols[c].IsNull(i) {
					row[c] = ""
					continue
				}
				row[c] = FormatValue(cols[c], i)
			}

			if err := fn(row); err != nil {
				if err == pio.ErrStopIteration() {
					return nil
				}
				return err
			}
		}
	}

	return nil
}

// Close releases all materialized record batches and the underlying file
// reader. Safe to call multiple times.
func (r *Reader) Close() error {
	for _, b := range r.batches {
		b.Release()
	}
	r.batches = nil
	if r.fileReader != nil {
		_ = r.fileReader.Close()
		r.fileReader = nil
	}
	r.arrowSc = nil
	return nil
}

// Reset rewinds the reader to the beginning. The next Read call will re-open
// the underlying file and re-materialize batches.
func (r *Reader) Reset() error {
	if err := r.Close(); err != nil {
		return err
	}
	r.header = nil
	r.started = false
	return nil
}

// InferPulseSchema builds a Pulse schema from the Arrow file's schema using
// the shared TypeToPulse mapping.
func (r *Reader) InferPulseSchema() (*encoding.Schema, error) {
	if err := r.init(); err != nil {
		return nil, err
	}

	numFields := r.arrowSc.NumFields()
	fields := make([]encoding.Field, numFields)
	byteOffset := 0

	for i := 0; i < numFields; i++ {
		af := r.arrowSc.Field(i)
		// Honor pulse:type / pulse:h3_resolution extension metadata first
		// so point_f64 and h3_cell columns recover their original type.
		// Decimal128 columns also resolve through this path so precision
		// and scale ride along.
		if pf, ok := PulseFieldFromArrow(af); ok {
			pf.ByteOffset = byteOffset
			pf.CsvColumnIdx = i
			fields[i] = pf
			byteOffset += pf.Type.ByteSize()
			continue
		}
		ft := TypeToPulse(af.Type, af.Nullable)
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

// defaultRecordBatchSize is the target number of rows per Arrow record batch.
// Each batch of this many rows is flushed via FileWriter.Write.
const defaultRecordBatchSize = 64 * 1024

// Writer writes tabular data as Arrow IPC (Feather V2), accumulating rows
// into an Arrow RecordBuilder and flushing in batch-sized chunks to bound
// peak memory.
type Writer struct {
	fs      afero.Fs
	path    string
	buf     bytes.Buffer
	columns []string

	// pulseSchema is set by the export pipeline via SetPulseSchema before
	// WriteHeader so the writer can build native typed columns for
	// decimal128 / point_f64 / h3_cell. nil falls back to the legacy
	// all-string column layout.
	pulseSchema *encoding.Schema

	// Lazily-initialized arrow writer state. Built on first WriteRow once
	// the schema (column names) is known. Reused across all batches.
	alloc   memory.Allocator
	sc      *arrow.Schema
	fw      *ipc.FileWriter
	bldr    *array.RecordBuilder
	strBs   []*array.StringBuilder
	pending int
	closed  bool
}

// SetPulseSchema records the source .pulse schema so subsequent
// initWriter can build native typed Arrow columns. Implements
// pio.SchemaAwareWriter.
func (w *Writer) SetPulseSchema(s *encoding.Schema) {
	w.pulseSchema = s
}

// NewWriter creates an Arrow IPC writer targeting a filesystem path. The
// file is materialized in an internal buffer and flushed to fs on Close.
func NewWriter(fs afero.Fs, path string) *Writer {
	return &Writer{fs: fs, path: path}
}

// NewWriterToBuffer creates an Arrow IPC writer that writes to an internal
// buffer accessible via Bytes after Close.
func NewWriterToBuffer() *Writer {
	return &Writer{}
}

// WriteHeader records the column names. The Arrow schema and writer are
// constructed lazily on the first WriteRow so that callers can WriteHeader
// and immediately Close to produce a schema-only file.
func (w *Writer) WriteHeader(columns []string) error {
	w.columns = make([]string, len(columns))
	copy(w.columns, columns)
	return nil
}

// initWriter builds the Arrow schema, RecordBuilder, and ipc.FileWriter on
// demand. When a Pulse schema was provided via SetPulseSchema, columns
// use native Arrow types per FieldFromPulse (Decimal128(p,s),
// FixedSizeBinary(16), UInt64). Otherwise every column falls back to
// arrow.String, the legacy all-string layout.
func (w *Writer) initWriter() error {
	if w.fw != nil {
		return nil
	}

	w.alloc = memory.NewGoAllocator()

	arrowFields := make([]arrow.Field, len(w.columns))
	if w.pulseSchema != nil && len(w.pulseSchema.Fields) == len(w.columns) {
		for i, pf := range w.pulseSchema.Fields {
			arrowFields[i] = FieldFromPulse(pf)
			arrowFields[i].Name = w.columns[i]
		}
	} else {
		for i, name := range w.columns {
			arrowFields[i] = arrow.Field{Name: name, Type: arrow.BinaryTypes.String}
		}
	}
	w.sc = arrow.NewSchema(arrowFields, nil)

	w.bldr = array.NewRecordBuilder(w.alloc, w.sc)
	w.strBs = make([]*array.StringBuilder, len(w.columns))
	for i := range w.columns {
		if sb, ok := w.bldr.Field(i).(*array.StringBuilder); ok {
			w.strBs[i] = sb
		}
	}

	w.buf.Reset()
	fw, err := ipc.NewFileWriter(&w.buf,
		ipc.WithSchema(w.sc),
		ipc.WithAllocator(w.alloc),
	)
	if err != nil {
		return fmt.Errorf("arrow.Writer: creating file writer: %w", err)
	}
	w.fw = fw
	return nil
}

// flushBatch builds an arrow.Record from the buffered rows and writes it as
// a single batch via ipc.FileWriter.Write. Resets the builder afterwards.
// No-op if no rows are pending.
func (w *Writer) flushBatch() error {
	if w.pending == 0 {
		return nil
	}
	rec := w.bldr.NewRecordBatch()
	if err := w.fw.Write(rec); err != nil {
		rec.Release()
		return fmt.Errorf("arrow.Writer: writing record: %w", err)
	}
	rec.Release()
	w.pending = 0
	return nil
}

// WriteRow writes a single row of values. With a Pulse schema set the
// row dispatches per-column to the native typed builder for that
// column's Pulse type. Without a schema every value is stringified via
// fmt.Sprintf and appended to a StringBuilder. Buffered rows flush in
// defaultRecordBatchSize chunks; batch boundaries fall between rows.
func (w *Writer) WriteRow(values []any) error {
	if w.columns == nil {
		return fmt.Errorf("arrow.Writer: WriteRow called before WriteHeader")
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
	w.pending++

	if w.pending >= defaultRecordBatchSize {
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
		case encoding.FieldTypeDecimal128, encoding.FieldTypeNullableDecimal128:
			return w.appendDecimal(c, f, v)
		case encoding.FieldTypePointF64:
			return w.appendPoint(c, v)
		case encoding.FieldTypeH3Cell:
			return w.appendH3(c, v)
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
	// No string builder and no recognized typed column — append the
	// generic value through the builder's reflective AppendValueFromString
	// path.
	if v == nil {
		w.bldr.Field(c).AppendNull()
		return nil
	}
	return w.bldr.Field(c).AppendValueFromString(fmt.Sprintf("%v", v))
}

func (w *Writer) appendDecimal(c int, f encoding.Field, v any) error {
	return AppendDecimal128(w.bldr.Field(c), f, v)
}

func (w *Writer) appendPoint(c int, v any) error {
	return AppendPointF64(w.bldr.Field(c), v)
}

func (w *Writer) appendH3(c int, v any) error {
	return AppendH3Cell(w.bldr.Field(c), v)
}

// Close flushes any pending batch, closes the underlying Arrow IPC writer,
// and writes the buffered file to fs if a path was configured. If no header
// was written, Close is a no-op and Bytes returns an empty slice. If a
// header was written but no rows, an empty record batch is emitted so the
// file is structurally valid.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	if w.columns == nil {
		// No header was ever written; preserve the empty-output behavior.
		return nil
	}

	// If no rows were ever written, emit a schema-only file with one empty
	// record batch so the file is valid Arrow IPC.
	if w.fw == nil {
		if err := w.initWriter(); err != nil {
			return err
		}
		rec := w.bldr.NewRecordBatch()
		if err := w.fw.Write(rec); err != nil {
			rec.Release()
			return fmt.Errorf("arrow.Writer: writing empty record: %w", err)
		}
		rec.Release()
	} else {
		if err := w.flushBatch(); err != nil {
			return err
		}
	}

	if err := w.fw.Close(); err != nil {
		return fmt.Errorf("arrow.Writer: closing: %w", err)
	}
	if w.bldr != nil {
		w.bldr.Release()
		w.bldr = nil
	}
	w.fw = nil

	if w.fs != nil && w.path != "" {
		return afero.WriteFile(w.fs, w.path, w.buf.Bytes(), 0644)
	}
	return nil
}

// Bytes returns the buffered Arrow IPC output. Only meaningful after Close.
func (w *Writer) Bytes() []byte {
	return w.buf.Bytes()
}

// Ensure interfaces are satisfied at compile time.
var _ pio.Reader = (*Reader)(nil)
var _ pio.ResetReader = (*Reader)(nil)
var _ pio.Writer = (*Writer)(nil)
