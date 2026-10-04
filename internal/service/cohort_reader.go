package service

import (
	"io"
	"math"
	"os"
	"sort"
	"sync"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// CohortRecordReader reads records of an opened cohort by index,
// returning each as a freshly allocated []any in schema LOGICAL field
// order holding the EXACT stored value (see exactValue for the per-type
// Go mapping). A grouped (0x02) cohort decodes identically to its
// ungrouped twin.
//
// A single-file cohort is one segment. A shard archive is one segment
// per shard in archive (central-directory) order, addressed through a
// global index: prefix sums over the shard record counts locate record
// i's shard by binary search, and the shard's records decode against
// the archive's CANONICAL schema, exactly as the shard iterator does.
// An anchored shard (`archive.pulse#shard.pulse`) is a one-segment
// archive read and also decodes against the canonical schema.
//
// RecordAt is safe for concurrent use: every call drives its own
// io.SectionReader (private cursor) over one shared io.ReaderAt, so no
// seek cursor is shared. Close waits for in-flight reads and makes
// every later RecordAt fail with SERVICE_RESOURCE.
type CohortRecordReader struct {
	schema *encoding.Schema
	segs   []recordSegment
	// starts[k] is the global index of segment k's first record;
	// starts[len(segs)] is the total.
	starts []int64

	mu     sync.RWMutex
	closed bool
	file   afero.File
}

// recordSegment is one independently addressable record region: the
// whole file of a single-file cohort, or one stored shard entry.
type recordSegment struct {
	loc  *encoding.RecordLocator
	ra   io.ReaderAt // offsets relative to the segment start
	size int64
}

// OpenRecordReader opens an index-addressed record reader over the
// cohort: a single-file cohort (ungrouped 0x01 and grouped 0x02), a
// whole shard archive, or one anchored shard.
func (c *Cohort) OpenRecordReader() (*CohortRecordReader, error) {
	switch {
	case len(c.shards) > 0:
		return openArchiveRecordReader(c.fs, c.path, c.schema, c.shards, "")
	case c.anchorArchive != "":
		return openArchiveRecordReader(c.fs, c.anchorArchive, nil, nil, c.anchorEntry)
	}
	f, size, ra, err := openReaderFile(c.fs, c.path)
	if err != nil {
		return nil, err
	}
	// The already-parsed c.schema supplies the geometry (the physical
	// stride for a grouped cohort).
	seg, _, err := measureSegment(ra, size, c.schema)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &CohortRecordReader{
		schema: c.schema,
		segs:   []recordSegment{seg},
		starts: []int64{0, int64(seg.loc.TotalRecords)},
		file:   f,
	}, nil
}

// openReaderFile opens path for positional reads held until Close.
func openReaderFile(fsys afero.Fs, path string) (afero.File, int64, io.ReaderAt, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, 0, nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"opening cohort file for record reads: "+path)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"sizing cohort file for record reads: "+path)
	}
	return f, st.Size(), concurrentReaderAt(f), nil
}

// measureSegment parses the header + schema prefix of the single-file
// payload in ra (the record region begins where ReadSchema stops) and
// returns a segment whose locator decodes against geometry. The
// payload's own header schema is returned for cohesion checks.
func measureSegment(ra io.ReaderAt, size int64, geometry *encoding.Schema) (recordSegment, *encoding.Schema, error) {
	sr := io.NewSectionReader(ra, 0, size)
	version, err := encoding.ReadHeader(sr)
	if err != nil {
		return recordSegment{}, nil, err
	}
	own, err := encoding.ReadSchema(sr, version)
	if err != nil {
		return recordSegment{}, nil, err
	}
	start, err := sr.Seek(0, io.SeekCurrent)
	if err != nil {
		return recordSegment{}, nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			"locating the record region")
	}
	loc := &encoding.RecordLocator{
		Schema:            geometry,
		RecordRegionStart: start,
		Stride:            int64(geometry.RecordByteSize()),
	}
	if n, _, ok := geometry.RecordCountForPayload(size - start); ok {
		loc.TotalRecords = uint64(n)
	}
	return recordSegment{loc: loc, ra: ra, size: size}, own, nil
}

// openArchiveRecordReader opens a shard archive for index-addressed
// reads. canonical / shards come from the opened cohort when it is the
// whole archive; for an anchored shard (only != "") both are read here
// and the reader addresses that one shard. Every shard's own header
// schema must be decode-compatible with the canonical schema — the
// structural cohesion rule plus the dictionary prefix rule `shard
// verify` applies — so decoding with canonical yields the shard's own
// values; a shard that breaks either is refused with the verify code.
func openArchiveRecordReader(fsys afero.Fs, path string, canonical *encoding.Schema, shards []ShardEntry, only string) (*CohortRecordReader, error) {
	f, size, ra, err := openReaderFile(fsys, path)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*CohortRecordReader, error) {
		_ = f.Close()
		return nil, err
	}
	arch, err := encx.OpenArchive(ra, size)
	if err != nil {
		return fail(err)
	}
	if canonical == nil {
		rc, err := arch.Open(encx.ReservedSchemaName)
		if err != nil {
			return fail(err)
		}
		doc, err := encx.ReadSchemaDoc(rc)
		_ = rc.Close()
		if err != nil {
			return fail(errors.WrapCodedError(err, errors.ENCODING_INVALID,
				"reading the canonical schema of archive: "+path))
		}
		canonical = doc.Schema
	}
	names := []string{only}
	if only == "" {
		names = make([]string, len(shards))
		for k, sh := range shards {
			names[k] = sh.Filename
		}
	}
	r := &CohortRecordReader{
		schema: canonical,
		segs:   make([]recordSegment, 0, len(names)),
		starts: make([]int64, 1, len(names)+1),
		file:   f,
	}
	for _, name := range names {
		if !arch.IsStored(name) {
			if _, err := arch.OpenAt(name); err != nil {
				return fail(err) // PULSE_SHARD_MISSING
			}
			return fail(errors.NewCodedErrorWithDetails(errors.PULSE_ARCHIVE_CORRUPT,
				"shard entry is not store-only, so its records cannot be addressed by offset",
				map[string]any{"archive": path, "entry": name}))
		}
		sect, err := arch.OpenAt(name)
		if err != nil {
			return fail(err)
		}
		seg, own, err := measureSegment(&sect, sect.Size(), canonical)
		if err != nil {
			return fail(errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
				"reading shard header for record reads: "+path+"#"+name))
		}
		if err := shardDecodeCompatible(canonical, own); err != nil {
			return fail(attachShardName(coerceCodedError(err), name))
		}
		r.segs = append(r.segs, seg)
		r.starts = append(r.starts, r.starts[len(r.starts)-1]+int64(seg.loc.TotalRecords))
	}
	return r, nil
}

// shardDecodeCompatible reports whether a shard written with header
// schema own decodes to its own values under canonical: same fields,
// types (set rungs included), offsets, nullability and group layout,
// with every dictionary (categorical, set, group entries) a PREFIX of
// canonical's. A dictionary LONGER than canonical's could hold IDs the
// canonical dictionary cannot resolve and is PULSE_SHARD_DICT_DIVERGENCE.
func shardDecodeCompatible(canonical, own *encoding.Schema) error {
	if _, err := encx.ValidateStructuralCohesion(canonical, own); err != nil {
		return err
	}
	if own.RecordByteSize() != canonical.RecordByteSize() {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH,
			"shard record stride differs from the canonical schema",
			map[string]any{"canonical_stride": canonical.RecordByteSize(), "shard_stride": own.RecordByteSize()})
	}
	ext, err := encx.ValidateDictPrefixRule(canonical, own)
	if err != nil {
		return err
	}
	if ext != canonical {
		return errors.NewCodedError(errors.PULSE_SHARD_DICT_DIVERGENCE,
			"shard dictionary extends past the canonical dictionary")
	}
	return nil
}

// Schema returns the cohort schema the reader decodes against; row
// slots are indexed by Schema().Fields. For an archive or an anchored
// shard it is the archive's canonical schema.
func (r *CohortRecordReader) Schema() *encoding.Schema { return r.schema }

// Len returns the number of records the reader addresses (for an
// archive, the sum over its shards).
func (r *CohortRecordReader) Len() int64 { return r.starts[len(r.starts)-1] }

// RecordAt decodes record i. An index outside [0, Len()) is
// SERVICE_VALIDATION; a call after Close is SERVICE_RESOURCE.
func (r *CohortRecordReader) RecordAt(i int64) ([]any, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, errors.NewCodedError(errors.SERVICE_RESOURCE,
			"cohort reader is closed")
	}
	if i < 0 || i >= r.Len() {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"record index out of range",
			map[string]any{"index": i, "len": r.Len()})
	}
	// The segment holding i: the last k with starts[k] <= i.
	k := sort.Search(len(r.segs), func(k int) bool { return r.starts[k+1] > i })
	seg := &r.segs[k]
	local := uint64(i - r.starts[k])

	values := make(map[string]float64, len(r.schema.Fields))
	nulls := map[string]bool{}
	wide := map[string]any{}
	raw := make(map[string]uint64, len(r.schema.Fields))
	sr := io.NewSectionReader(seg.ra, 0, seg.size)
	if err := encx.ReadRecordAtRaw(seg.loc, sr, local, values, nulls, wide, raw); err != nil {
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
