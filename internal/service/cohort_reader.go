package service

import (
	"io"
	"math"
	"os"
	"sync"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// CohortRecordReader reads records of an opened single-file cohort by
// index, returning each as a freshly allocated []any in schema LOGICAL
// field order holding the EXACT stored value (see exactValue for the
// per-type Go mapping). A grouped (0x02) cohort decodes identically to
// its ungrouped twin.
//
// RecordAt is safe for concurrent use: every call drives its own
// io.SectionReader (private cursor) over one shared io.ReaderAt, so no
// seek cursor is shared. Close waits for in-flight reads and makes
// every later RecordAt fail with SERVICE_RESOURCE.
type CohortRecordReader struct {
	schema *encoding.Schema
	loc    *encoding.RecordLocator
	size   int64

	mu     sync.RWMutex
	closed bool
	file   afero.File
	ra     io.ReaderAt
}

// OpenRecordReader opens an index-addressed record reader over the
// cohort. Single-file cohorts (ungrouped 0x01 and grouped 0x02) are
// supported; a shard archive opened whole is refused with
// SERVICE_VALIDATION.
func (c *Cohort) OpenRecordReader() (*CohortRecordReader, error) {
	if len(c.shards) > 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"record-by-record reads of a whole shard archive are not supported; open one shard with the archive#shard anchor",
			map[string]any{"path": c.path, "shards": len(c.shards)})
	}
	f, err := c.fs.Open(c.path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"opening cohort file for record reads: "+c.path)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"sizing cohort file for record reads: "+c.path)
	}
	size := st.Size()
	ra := concurrentReaderAt(f)

	// Measure the header + schema prefix: the record region begins
	// where ReadSchema stops. The already-parsed c.schema supplies the
	// geometry (the physical stride for a grouped cohort).
	sr := io.NewSectionReader(ra, 0, size)
	version, err := encoding.ReadHeader(sr)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := encoding.ReadSchema(sr, version); err != nil {
		_ = f.Close()
		return nil, err
	}
	start, err := sr.Seek(0, io.SeekCurrent)
	if err != nil {
		_ = f.Close()
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			"locating the record region")
	}
	loc := &encoding.RecordLocator{
		Schema:            c.schema,
		RecordRegionStart: start,
		Stride:            int64(c.schema.RecordByteSize()),
	}
	if n, _, ok := c.schema.RecordCountForPayload(size - start); ok {
		loc.TotalRecords = uint64(n)
	}
	return &CohortRecordReader{
		schema: c.schema,
		loc:    loc,
		size:   size,
		file:   f,
		ra:     ra,
	}, nil
}

// Schema returns the cohort schema the reader decodes against; row
// slots are indexed by Schema().Fields.
func (r *CohortRecordReader) Schema() *encoding.Schema { return r.schema }

// Len returns the number of records the reader addresses.
func (r *CohortRecordReader) Len() int64 { return int64(r.loc.TotalRecords) }

// RecordAt decodes record i. An index outside [0, Len()) is
// SERVICE_VALIDATION; a call after Close is SERVICE_RESOURCE.
func (r *CohortRecordReader) RecordAt(i int64) ([]any, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, errors.NewCodedError(errors.SERVICE_RESOURCE,
			"cohort reader is closed")
	}
	if i < 0 || uint64(i) >= r.loc.TotalRecords {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"record index out of range",
			map[string]any{"index": i, "len": r.loc.TotalRecords})
	}
	values := make(map[string]float64, len(r.schema.Fields))
	nulls := map[string]bool{}
	wide := map[string]any{}
	raw := make(map[string]uint64, len(r.schema.Fields))
	sr := io.NewSectionReader(r.ra, 0, r.size)
	if err := encx.ReadRecordAtRaw(r.loc, sr, uint64(i), values, nulls, wide, raw); err != nil {
		if err == io.EOF {
			return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"truncated record", map[string]any{"index": i})
		}
		return nil, err
	}
	row := make([]any, len(r.schema.Fields))
	for fi := range r.schema.Fields {
		f := &r.schema.Fields[fi]
		if nulls[f.Name] {
			continue // nil
		}
		v, err := exactValue(f, values, wide, raw)
		if err != nil {
			return nil, err
		}
		row[fi] = v
	}
	return row, nil
}

// Close releases the file handle. It is idempotent.
func (r *CohortRecordReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if err := r.file.Close(); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"closing cohort reader")
	}
	return nil
}

// exactValue converts one decoded non-null field to its canonical Go
// value: integers uint64; f32/f64 float32/float64; date int32 epoch
// days; datetime int64 epoch seconds; decimal128 encoding.Decimal128;
// categorical the label string; set_* the labels in dictionary order
// ([]string, empty non-nil for no selection); packed_bool bool. Every
// value comes from the raw word or the wide map, never the float echo.
func exactValue(f *encoding.Field, values map[string]float64, wide map[string]any, raw map[string]uint64) (any, error) {
	switch f.Type {
	case encoding.FieldTypePackedBool:
		return values[f.Name] != 0, nil
	case encoding.FieldTypeU4:
		return uint64(values[f.Name]), nil
	case encoding.FieldTypeDecimal128:
		d, ok := wide[f.Name].(encoding.Decimal128)
		if !ok {
			return nil, missingExact(f)
		}
		return d, nil
	case encoding.FieldTypeSetU128, encoding.FieldTypeSetU256:
		m, ok := wide[f.Name].(encoding.SetMask)
		if !ok {
			return nil, missingExact(f)
		}
		return m.Labels(f.Dictionary), nil
	}
	w, ok := raw[f.Name]
	if !ok {
		return nil, missingExact(f)
	}
	switch {
	case f.Type == encoding.FieldTypeF32:
		return math.Float32frombits(uint32(w)), nil
	case f.Type == encoding.FieldTypeF64:
		return math.Float64frombits(w), nil
	case f.Type == encoding.FieldTypeDate:
		return encoding.DateDays(w), nil
	case f.Type == encoding.FieldTypeDateTime:
		return encoding.DateTimeSeconds(w), nil
	case f.Type.IsSet():
		return encoding.SetMaskFromUint64(w).Labels(f.Dictionary), nil
	case f.Type.HasDictionary():
		if f.Dictionary == nil || w >= uint64(f.Dictionary.Count()) {
			return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"categorical dictionary ID out of range",
				map[string]any{"field": f.Name, "id": w})
		}
		return f.Dictionary.Resolve(uint32(w)), nil
	default:
		return w, nil
	}
}

func missingExact(f *encoding.Field) error {
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INTERNAL,
		"decoder produced no exact value for field",
		map[string]any{"field": f.Name, "type": f.Type.String()})
}

// concurrentReaderAt returns an io.ReaderAt safe for concurrent use
// over f. An *os.File's ReadAt is a positional pread and already safe
// (a BasePathFs file — the DataDir configuration — is unwrapped to it);
// other afero files (the in-memory MemMapFs file implements ReadAt by
// swapping its shared cursor) are serialised behind a mutex.
func concurrentReaderAt(f afero.File) io.ReaderAt {
	if bp, ok := f.(*afero.BasePathFile); ok {
		f = bp.File
	}
	if osf, ok := f.(*os.File); ok {
		return osf
	}
	return &lockedReaderAt{f: f}
}

type lockedReaderAt struct {
	mu sync.Mutex
	f  io.ReaderAt
}

func (l *lockedReaderAt) ReadAt(p []byte, off int64) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.ReadAt(p, off)
}
