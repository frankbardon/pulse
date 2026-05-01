// Package ndjson provides NDJSON (newline-delimited JSON) import and export for the pulse I/O pipeline.
package ndjson

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/jsonshared"
	"github.com/spf13/afero"
)

// Reader reads NDJSON data from a byte source. Each line is one JSON object.
type Reader struct {
	data    []byte
	fs      afero.Fs
	path    string
	scanner *bufio.Scanner
	buf     *bytes.Reader
	header  []string
	started bool
	lineNum int
}

// NewReader creates an NDJSON reader from a filesystem path.
func NewReader(fs afero.Fs, path string) *Reader {
	return &Reader{
		fs:   fs,
		path: path,
	}
}

// NewReaderFromBytes creates an NDJSON reader from raw bytes.
func NewReaderFromBytes(data []byte) *Reader {
	return &Reader{
		data: data,
	}
}

func (r *Reader) init() error {
	if r.scanner != nil {
		return nil
	}
	if r.data == nil {
		if r.fs == nil {
			return fmt.Errorf("ndjson.Reader: no data source")
		}
		data, err := afero.ReadFile(r.fs, r.path)
		if err != nil {
			return fmt.Errorf("ndjson.Reader: reading %s: %w", r.path, err)
		}
		r.data = data
	}
	r.buf = bytes.NewReader(r.data)
	r.scanner = bufio.NewScanner(r.buf)
	r.started = false
	r.header = nil
	r.lineNum = 0
	return nil
}

// decodeLine parses a single NDJSON line into an ordered map.
// Returns the map and the ordered keys. Rejects nested objects/arrays.
func decodeLine(line []byte) (map[string]any, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()

	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, nil, err
	}

	// Extract key order by re-parsing the JSON tokens.
	dec2 := json.NewDecoder(bytes.NewReader(line))
	dec2.UseNumber()

	// Read opening brace.
	tok, err := dec2.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, fmt.Errorf("expected object, got %v", tok)
	}

	var keys []string
	for dec2.More() {
		tok, err := dec2.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, nil, fmt.Errorf("expected string key, got %T", tok)
		}
		keys = append(keys, key)

		// Read the value.
		var val any
		if err := dec2.Decode(&val); err != nil {
			return nil, nil, err
		}
	}

	// Validate no nested objects or arrays.
	for k, v := range obj {
		switch v.(type) {
		case map[string]any:
			return nil, nil, fmt.Errorf("nested object at key %q is not supported", k)
		case []any:
			return nil, nil, fmt.Errorf("array at key %q is not supported", k)
		}
	}

	return obj, keys, nil
}

// ReadHeader returns column names derived from the keys of the first JSON object.
func (r *Reader) ReadHeader() ([]string, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	if r.header != nil {
		return r.header, nil
	}

	// Scan for the first non-empty line.
	for r.scanner.Scan() {
		r.lineNum++
		line := bytes.TrimSpace(r.scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		_, keys, err := decodeLine(line)
		if err != nil {
			return nil, fmt.Errorf("ndjson.Reader: parsing header (line %d): %w", r.lineNum, err)
		}

		r.header = keys
		r.started = true
		return r.header, nil
	}

	if err := r.scanner.Err(); err != nil {
		return nil, fmt.Errorf("ndjson.Reader: scanning: %w", err)
	}

	// Empty file.
	return nil, nil
}

// ReadRows streams rows to fn. ReadHeader must be called first.
func (r *Reader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	if r.scanner == nil {
		if err := r.init(); err != nil {
			return err
		}
	}
	if !r.started {
		if _, err := r.ReadHeader(); err != nil {
			return err
		}
	}

	for r.scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		r.lineNum++
		line := bytes.TrimSpace(r.scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		obj, _, err := decodeLine(line)
		if err != nil {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_ROW_ERROR,
				fmt.Sprintf("line %d: %v", r.lineNum, err),
				map[string]any{"line": r.lineNum},
			)
		}

		// Build row in header order.
		row := make([]string, len(r.header))
		for i, key := range r.header {
			if v, ok := obj[key]; ok {
				row[i] = jsonshared.ValueToString(v)
			} else {
				row[i] = ""
			}
		}

		if err := fn(row); err != nil {
			if err == pio.ErrStopIteration() {
				return nil
			}
			return err
		}
	}

	if err := r.scanner.Err(); err != nil {
		return fmt.Errorf("ndjson.Reader: scanning: %w", err)
	}

	return nil
}

// Close releases resources.
func (r *Reader) Close() error {
	r.scanner = nil
	r.buf = nil
	return nil
}

// Reset rewinds the reader to the beginning.
func (r *Reader) Reset() error {
	r.scanner = nil
	r.buf = nil
	r.header = nil
	r.started = false
	r.lineNum = 0
	return nil
}

// Writer writes NDJSON data to a buffer.
type Writer struct {
	fs      afero.Fs
	path    string
	buf     bytes.Buffer
	columns []string
}

// NewWriter creates an NDJSON writer targeting a filesystem path.
func NewWriter(fs afero.Fs, path string) *Writer {
	return &Writer{
		fs:   fs,
		path: path,
	}
}

// NewWriterToBuffer creates an NDJSON writer that writes to an internal buffer.
func NewWriterToBuffer() *Writer {
	return &Writer{}
}

// WriteHeader records the column names for use in subsequent WriteRow calls.
// NDJSON has no header line; the columns are used as keys in each JSON object.
func (w *Writer) WriteHeader(columns []string) error {
	w.columns = make([]string, len(columns))
	copy(w.columns, columns)
	return nil
}

// WriteRow writes a single JSON object as one line.
func (w *Writer) WriteRow(values []any) error {
	if w.columns == nil {
		return fmt.Errorf("ndjson.Writer: WriteHeader must be called before WriteRow")
	}

	obj := make(map[string]any, len(w.columns))
	for i, col := range w.columns {
		if i < len(values) {
			obj[col] = jsonshared.CoerceValue(values[i])
		} else {
			obj[col] = nil
		}
	}

	// Write JSON in column order to produce deterministic output.
	w.buf.WriteByte('{')
	for i, col := range w.columns {
		if i > 0 {
			w.buf.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(col)
		w.buf.Write(keyBytes)
		w.buf.WriteByte(':')
		valBytes, err := json.Marshal(obj[col])
		if err != nil {
			return fmt.Errorf("ndjson.Writer: marshaling value for %q: %w", col, err)
		}
		w.buf.Write(valBytes)
	}
	w.buf.WriteByte('}')
	w.buf.WriteByte('\n')

	return nil
}

// Close flushes and writes to the target path if configured.
func (w *Writer) Close() error {
	if w.fs != nil && w.path != "" {
		return afero.WriteFile(w.fs, w.path, w.buf.Bytes(), 0644)
	}
	return nil
}

// Bytes returns the buffered NDJSON output.
func (w *Writer) Bytes() []byte {
	return w.buf.Bytes()
}
