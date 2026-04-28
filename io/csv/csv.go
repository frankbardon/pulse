// Package csv provides CSV format adapters for the Pulse I/O pipeline.
package csv

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// Reader reads CSV data from a byte source.
type Reader struct {
	data    []byte
	fs      afero.Fs
	path    string
	cr      *csv.Reader
	buf     *bytes.Reader
	header  []string
	started bool
}

// NewReader creates a CSV reader from a filesystem path.
func NewReader(fs afero.Fs, path string) *Reader {
	return &Reader{
		fs:   fs,
		path: path,
	}
}

// NewReaderFromBytes creates a CSV reader from raw bytes.
func NewReaderFromBytes(data []byte) *Reader {
	return &Reader{
		data: data,
	}
}

func (r *Reader) init() error {
	if r.cr != nil {
		return nil
	}
	if r.data == nil {
		if r.fs == nil {
			return fmt.Errorf("csv.Reader: no data source")
		}
		data, err := afero.ReadFile(r.fs, r.path)
		if err != nil {
			return fmt.Errorf("csv.Reader: reading %s: %w", r.path, err)
		}
		r.data = data
	}
	r.buf = bytes.NewReader(r.data)
	r.cr = csv.NewReader(r.buf)
	r.cr.FieldsPerRecord = -1 // allow variable field counts
	r.cr.LazyQuotes = true
	r.started = false
	r.header = nil
	return nil
}

// ReadHeader returns the column names from the first row.
func (r *Reader) ReadHeader() ([]string, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	if r.header != nil {
		return r.header, nil
	}
	record, err := r.cr.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("csv.Reader: reading header: %w", err)
	}
	r.header = record
	r.started = true
	return r.header, nil
}

// ReadRows streams rows to fn. ReadHeader must be called first.
func (r *Reader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	if r.cr == nil {
		if err := r.init(); err != nil {
			return err
		}
	}
	if !r.started {
		if _, err := r.ReadHeader(); err != nil {
			return err
		}
	}

	rowNum := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		record, err := r.cr.Read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			rowNum++
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_ROW_ERROR,
				fmt.Sprintf("row %d: %v", rowNum, err),
				map[string]any{"row": rowNum},
			)
		}
		rowNum++

		if err := fn(record); err != nil {
			if err == pio.ErrStopIteration() {
				return nil
			}
			return err
		}
	}
}

// Close releases resources.
func (r *Reader) Close() error {
	r.cr = nil
	r.buf = nil
	return nil
}

// Reset rewinds the reader to the beginning.
func (r *Reader) Reset() error {
	r.cr = nil
	r.buf = nil
	r.header = nil
	r.started = false
	return nil
}

// Writer writes CSV data to a buffer.
type Writer struct {
	fs   afero.Fs
	path string
	buf  bytes.Buffer
	cw   *csv.Writer
}

// NewWriter creates a CSV writer targeting a filesystem path.
func NewWriter(fs afero.Fs, path string) *Writer {
	w := &Writer{
		fs:   fs,
		path: path,
	}
	w.cw = csv.NewWriter(&w.buf)
	return w
}

// NewWriterToBuffer creates a CSV writer that writes to an internal buffer.
func NewWriterToBuffer() *Writer {
	w := &Writer{}
	w.cw = csv.NewWriter(&w.buf)
	return w
}

// WriteHeader writes the column names as the first CSV row.
func (w *Writer) WriteHeader(columns []string) error {
	return w.cw.Write(columns)
}

// WriteRow writes a single data row.
func (w *Writer) WriteRow(values []any) error {
	record := make([]string, len(values))
	for i, v := range values {
		record[i] = formatCell(v)
	}
	return w.cw.Write(record)
}

// formatCell converts a cell value to its string representation without the
// reflection cost of fmt.Sprintf("%v", v). Float formatting uses 'g' to match
// the previous fmt verb output. Unknown types fall back to fmt.Sprint to
// preserve correctness for callers passing custom types.
func formatCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

// Close flushes and writes to the target path if configured.
func (w *Writer) Close() error {
	w.cw.Flush()
	if err := w.cw.Error(); err != nil {
		return err
	}
	if w.fs != nil && w.path != "" {
		return afero.WriteFile(w.fs, w.path, w.buf.Bytes(), 0644)
	}
	return nil
}

// Bytes returns the buffered CSV output.
func (w *Writer) Bytes() []byte {
	w.cw.Flush()
	return w.buf.Bytes()
}
