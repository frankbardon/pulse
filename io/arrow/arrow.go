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
	"github.com/frankbardon/pulse/types"
)

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
//
// A top-level "overlays" field (the overlay column-family sidecar
// emitted by SetOverlays per research/export-embedding-shape.md § 3.1)
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
		if name == OverlaysFieldName {
			continue
		}
		r.header = append(r.header, name)
	}
	r.started = true
	return r.header, nil
}

// ReadRows iterates rows across all materialized record batches, calling fn
// for each row. Null cells produce empty strings.
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

	numFields := r.arrowSc.NumFields()
	// Map output column index → batch column index, skipping the
	// overlays sidecar field. The mapped slice keeps the per-row write
	// loop free of per-row name comparisons.
	hostColIdx := make([]int, 0, numFields)
	for c := 0; c < numFields; c++ {
		if r.arrowSc.Field(c).Name == OverlaysFieldName {
			continue
		}
		hostColIdx = append(hostColIdx, c)
	}
	numCols := len(hostColIdx)
	row := make([]string, numCols)

	for _, batch := range r.batches {
		nRows := int(batch.NumRows())
		// Snapshot the column arrays for this batch up front to avoid
		// re-fetching on every row.
		cols := make([]arrow.Array, numCols)
		for out, src := range hostColIdx {
			cols[out] = batch.Column(src)
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

// ReadOverlays extracts the embedded overlay layers from the top-level
// "overlays" sidecar column emitted by a SetOverlays-enabled Writer.
// Returns nil when the Arrow file carries no overlays sidecar (the
// "overlays" field is absent from the schema) or when every list
// element is empty.
//
// Per research/export-embedding-shape.md § 3.3 the reader walks the
// first non-empty list element across the first record-batch; the
// canonical writer emits the populated list in row 0 of batch 0 (§ 3.2
// "emit once" optimisation), so this scan terminates on the first row.
func (r *Reader) ReadOverlays() ([]*types.OverlayLayer, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	overlayFieldIdx := -1
	for c := 0; c < r.arrowSc.NumFields(); c++ {
		if r.arrowSc.Field(c).Name == OverlaysFieldName {
			overlayFieldIdx = c
			break
		}
	}
	if overlayFieldIdx < 0 {
		return nil, nil
	}
	for _, batch := range r.batches {
		col := batch.Column(overlayFieldIdx)
		layers, err := ReadOverlaysFromArray(col)
		if err != nil {
			return nil, err
		}
		if layers != nil {
			return layers, nil
		}
	}
	return nil, nil
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
		// Decimal128 columns resolve through this path so precision and
		// scale ride along.
		if pf, ok := PulseFieldFromArrow(af); ok {
			pf.ByteOffset = byteOffset
			pf.CsvColumnIdx = i
			fields[i] = pf
			byteOffset += pf.Type.ByteSize()
			continue
		}
		ft := TypeToPulse(af.Type)
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
	// decimal128. nil falls back to the legacy all-string column layout.
	pulseSchema *encoding.Schema

	// overlays carries the Response.Overlays layers the export pipeline
	// hands to the writer via SetOverlays before WriteHeader. When
	// non-nil + non-empty, the writer adds a top-level "overlays" field
	// (LIST<STRUCT>) to the Arrow schema and emits the layers ONCE in
	// the first record-batch per research/export-embedding-shape.md
	// § 3.1 / § 3.2. nil OR empty produces byte-identical Arrow output
	// to a pre-overlay export (the field is OMITTED from the schema, not
	// present-with-empty-list).
	overlays []*types.OverlayLayer

	// overlaysEmitted tracks whether the writer has flushed the
	// populated overlays list in the first record-batch. Subsequent
	// batches emit an empty list per the "emit once, empty thereafter"
	// rule (§ 3.2).
	overlaysEmitted bool

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

// SetOverlays records the Response.Overlays layers the export pipeline
// wants the writer to embed in the Arrow file. The layers ride as a
// top-level "overlays" LIST<STRUCT> column whose populated value lands
// in the first record-batch and whose subsequent record-batches carry
// an empty list — per research/export-embedding-shape.md § 3.1 / § 3.2.
// nil or empty layers leaves the Arrow output byte-identical to a
// pre-overlay export (the "overlays" field is OMITTED from the schema
// rather than emitted with empty lists). Implements
// pio.OverlayAwareWriter.
//
// Must be called BEFORE WriteHeader so the lazily-built Arrow schema
// includes the overlay field. Calling after the writer has been
// initialised has no effect on the schema.
func (w *Writer) SetOverlays(layers []*types.OverlayLayer) {
	w.overlays = layers
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
// use native Arrow types per FieldFromPulse (Decimal128(p,s)). Otherwise
// every column falls back to arrow.String, the legacy all-string layout.
//
// When SetOverlays handed the writer a non-empty []*OverlayLayer, the
// schema gains one trailing field "overlays" of type LIST<STRUCT> per
// research/export-embedding-shape.md § 3.1. The "overlays" field is
// OMITTED entirely when overlays is nil / empty so the byte layout of
// an overlay-free export stays identical to the pre-overlay path.
func (w *Writer) initWriter() error {
	if w.fw != nil {
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
			arrowFields[i] = FieldFromPulse(pf)
			arrowFields[i].Name = w.columns[i]
		}
	} else {
		for i, name := range w.columns {
			arrowFields[i] = arrow.Field{Name: name, Type: arrow.BinaryTypes.String}
		}
	}
	if hasOverlays {
		arrowFields[hostFieldCount] = OverlaysArrowField()
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
//
// When SetOverlays handed the writer non-empty layers, the row also
// appends to the trailing overlays LIST<STRUCT> column. Row 0 of the
// first record-batch carries the populated layer list; every
// subsequent row (including subsequent record-batches) carries an
// empty list per research/export-embedding-shape.md § 3.2 "emit once"
// optimisation.
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
	if err := w.appendOverlayCell(); err != nil {
		return err
	}
	w.pending++

	if w.pending >= defaultRecordBatchSize {
		return w.flushBatch()
	}
	return nil
}

// appendOverlayCell appends one cell to the trailing overlays
// LIST<STRUCT> column. No-op when the writer has no overlays
// configured (the schema does not include the overlays field).
//
// The first appended cell (row 0 of the first record-batch) carries
// the populated layer list; every subsequent cell carries an empty
// list per § 3.2. The flag flips on the first emission so subsequent
// record-batches see overlaysEmitted=true and append empty lists.
func (w *Writer) appendOverlayCell() error {
	if len(w.overlays) == 0 {
		return nil
	}
	overlayFieldIdx := len(w.columns)
	listBldr, ok := w.bldr.Field(overlayFieldIdx).(*array.ListBuilder)
	if !ok {
		return fmt.Errorf("arrow.Writer: overlays field is not a list builder (got %T)", w.bldr.Field(overlayFieldIdx))
	}
	listBldr.Append(true)
	structBldr := listBldr.ValueBuilder().(*array.StructBuilder)

	if w.overlaysEmitted {
		// Empty list — no struct elements appended.
		return nil
	}

	for _, layer := range w.overlays {
		if err := AppendOverlayLayerStruct(structBldr, layer); err != nil {
			return err
		}
	}
	w.overlaysEmitted = true
	return nil
}

// AppendOverlayLayerStruct appends one struct entry to the overlay
// list's value builder. Field order matches OverlaysFieldType.
//
// Exported so io/parquet can populate the same struct schema through
// its own pqarrow-fed RecordBuilder (research/export-embedding-shape.md
// § 4.4).
func AppendOverlayLayerStruct(structBldr *array.StructBuilder, layer *types.OverlayLayer) error {
	if layer == nil {
		structBldr.AppendNull()
		return nil
	}
	structBldr.Append(true)

	refJSON, err := JSONMarshalOverlay(layer.Ref)
	if err != nil {
		return fmt.Errorf("arrow.overlay: marshal overlay ref: %w", err)
	}
	payloadJSON, err := JSONMarshalOverlay(layer.Payload)
	if err != nil {
		return fmt.Errorf("arrow.overlay: marshal overlay payload: %w", err)
	}

	structBldr.FieldBuilder(0).(*array.StringBuilder).Append(layer.Name)
	structBldr.FieldBuilder(1).(*array.StringBuilder).Append(string(layer.Kind))
	structBldr.FieldBuilder(2).(*array.StringBuilder).Append(string(layer.Scope))
	structBldr.FieldBuilder(3).(*array.StringBuilder).Append(string(refJSON))
	structBldr.FieldBuilder(4).(*array.StringBuilder).Append(string(layer.Payload.Shape))
	structBldr.FieldBuilder(5).(*array.StringBuilder).Append(string(payloadJSON))
	if layer.Summary == nil {
		structBldr.FieldBuilder(6).(*array.StringBuilder).AppendNull()
	} else {
		summaryJSON, err := JSONMarshalOverlay(layer.Summary)
		if err != nil {
			return fmt.Errorf("arrow.overlay: marshal overlay summary: %w", err)
		}
		structBldr.FieldBuilder(6).(*array.StringBuilder).Append(string(summaryJSON))
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
			return w.appendDecimal(c, f, v)
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
var _ pio.SchemaAwareWriter = (*Writer)(nil)
var _ pio.OverlayAwareWriter = (*Writer)(nil)
