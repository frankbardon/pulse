package jsonarray

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/jsonshared"
	"github.com/spf13/afero"
)

// Reader reads a top-level JSON array of objects, one object per row.
type Reader struct {
	data    []byte
	fs      afero.Fs
	path    string
	dec     *json.Decoder
	header  []string
	pending map[string]any // first object, buffered for the first ReadRows call.
	rowNum  int
	closed  bool
}

// NewReader creates a Reader from a filesystem path.
func NewReader(fs afero.Fs, path string) *Reader {
	return &Reader{fs: fs, path: path}
}

// NewReaderFromBytes creates a Reader from raw bytes.
func NewReaderFromBytes(data []byte) *Reader {
	return &Reader{data: data}
}

func (r *Reader) init() error {
	if r.dec != nil {
		return nil
	}
	if r.data == nil {
		if r.fs == nil {
			return fmt.Errorf("jsonarray.Reader: no data source")
		}
		data, err := afero.ReadFile(r.fs, r.path)
		if err != nil {
			return fmt.Errorf("jsonarray.Reader: reading %s: %w", r.path, err)
		}
		r.data = data
	}
	r.dec = json.NewDecoder(bufio.NewReader(bytes.NewReader(r.data)))
	r.dec.UseNumber()

	// Consume opening '['.
	tok, err := r.dec.Token()
	if err == io.EOF {
		// Empty input: leave dec in a state where More() returns false.
		return nil
	}
	if err != nil {
		return fmt.Errorf("jsonarray.Reader: %w", err)
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '[' {
		return fmt.Errorf("jsonarray.Reader: expected '[' at start, got %v", tok)
	}
	return nil
}

// decodeObject decodes one object from the decoder, returning its fields and key order.
func decodeObject(dec *json.Decoder) (map[string]any, []string, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '{' {
		return nil, nil, fmt.Errorf("expected object, got %v", tok)
	}

	obj := make(map[string]any)
	var keys []string
	for dec.More() {
		ktok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := ktok.(string)
		if !ok {
			return nil, nil, fmt.Errorf("expected string key, got %T", ktok)
		}
		keys = append(keys, key)

		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, nil, err
		}
		switch v := val.(type) {
		case map[string]any:
			return nil, nil, fmt.Errorf("nested object at key %q is not supported", key)
		case []any:
			for _, el := range v {
				switch el.(type) {
				case map[string]any, []any:
					return nil, nil, fmt.Errorf("non-scalar element in array at key %q is not supported", key)
				}
			}
		}
		obj[key] = val
	}

	// Consume closing '}'.
	if _, err := dec.Token(); err != nil {
		return nil, nil, err
	}
	return obj, keys, nil
}

// ReadHeader returns column names from the first JSON object.
func (r *Reader) ReadHeader() ([]string, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	if r.header != nil {
		return r.header, nil
	}
	if r.dec == nil || !r.dec.More() {
		return nil, nil
	}

	obj, keys, err := decodeObject(r.dec)
	if err != nil {
		return nil, fmt.Errorf("jsonarray.Reader: parsing first element: %w", err)
	}
	r.header = keys
	r.pending = obj
	return r.header, nil
}

// ReadRows streams rows to fn. ReadHeader is auto-called if not yet invoked.
func (r *Reader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	if r.dec == nil {
		if err := r.init(); err != nil {
			return err
		}
	}
	if r.header == nil {
		if _, err := r.ReadHeader(); err != nil {
			return err
		}
	}
	if r.header == nil {
		return nil // empty array
	}

	emit := func(obj map[string]any) error {
		row := make([]string, len(r.header))
		for i, key := range r.header {
			if v, ok := obj[key]; ok {
				row[i] = jsonshared.ValueToString(v)
			}
		}
		if err := fn(row); err != nil {
			if err == pio.ErrStopIteration() {
				return errStop
			}
			return err
		}
		return nil
	}

	if r.pending != nil {
		obj := r.pending
		r.pending = nil
		r.rowNum++
		if err := emit(obj); err != nil {
			if err == errStop {
				return nil
			}
			return err
		}
	}

	for r.dec.More() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		r.rowNum++
		obj, _, err := decodeObject(r.dec)
		if err != nil {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_ROW_ERROR,
				fmt.Sprintf("row %d: %v", r.rowNum, err),
				map[string]any{"row": r.rowNum},
			)
		}
		if err := emit(obj); err != nil {
			if err == errStop {
				return nil
			}
			return err
		}
	}
	return nil
}

// errStop is an internal sentinel used to unwind ReadRows on pio.ErrStopIteration.
var errStop = fmt.Errorf("jsonarray: stop")

// Close releases resources.
func (r *Reader) Close() error {
	r.closed = true
	r.dec = nil
	r.pending = nil
	return nil
}

// Reset rewinds the reader to the beginning.
func (r *Reader) Reset() error {
	r.dec = nil
	r.header = nil
	r.pending = nil
	r.rowNum = 0
	r.closed = false
	return nil
}

// Writer writes a JSON array, one object per row.
type Writer struct {
	fs       afero.Fs
	path     string
	buf      bytes.Buffer
	columns  []string
	started  bool
	firstRow bool
	closed   bool
}

// NewWriter creates a Writer targeting a filesystem path.
func NewWriter(fs afero.Fs, path string) *Writer {
	return &Writer{fs: fs, path: path}
}

// NewWriterToBuffer creates a Writer that accumulates output in an internal buffer.
func NewWriterToBuffer() *Writer {
	return &Writer{}
}

// WriteHeader stores the column names and emits the opening '['.
func (w *Writer) WriteHeader(columns []string) error {
	w.columns = make([]string, len(columns))
	copy(w.columns, columns)
	w.buf.WriteByte('[')
	w.started = true
	w.firstRow = true
	return nil
}

// WriteRow writes a single object element.
func (w *Writer) WriteRow(values []any) error {
	if !w.started {
		return fmt.Errorf("jsonarray.Writer: WriteHeader must be called before WriteRow")
	}
	if !w.firstRow {
		w.buf.WriteByte(',')
	}
	w.firstRow = false

	w.buf.WriteByte('{')
	for i, col := range w.columns {
		if i > 0 {
			w.buf.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(col)
		w.buf.Write(keyBytes)
		w.buf.WriteByte(':')

		var v any
		if i < len(values) {
			v = jsonshared.CoerceValue(values[i])
		}
		valBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("jsonarray.Writer: marshaling value for %q: %w", col, err)
		}
		w.buf.Write(valBytes)
	}
	w.buf.WriteByte('}')
	return nil
}

// Close emits the closing ']' and flushes to the target path if configured.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if !w.started {
		// No header was written; emit empty array for consumer parsability.
		w.buf.WriteByte('[')
	}
	w.buf.WriteByte(']')
	if w.fs != nil && w.path != "" {
		return afero.WriteFile(w.fs, w.path, w.buf.Bytes(), 0644)
	}
	return nil
}

// Bytes returns the buffered JSON output. Callers should call Close first.
func (w *Writer) Bytes() []byte {
	return w.buf.Bytes()
}
