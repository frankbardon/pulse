package service

import (
	"context"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// CountRecords returns the total record count for the cohort at path
// without decoding any payload bytes. Three resolution paths:
//
//   - Single-file `.pulse`: stat the file, stream the header + schema,
//     and derive count = (file_size − header − schema) / record_stride.
//     Bytes read is bounded by header + schema size — O(1) in the
//     record count.
//   - Shard archive (zip magic): read the archive's central directory
//     by random access plus the reserved `_schema.pulse` entry. The
//     SHRD trailer's AggregateRecordCount is the cached sum of every
//     shard's record count; cohesive archives keep it in lockstep with
//     the per-shard headers. Cost is O(N shards) for the directory
//     walk, not O(total records) and not O(archive bytes).
//   - Anchor path `archive#shard`: opens the archive's central
//     directory and derives the named shard's count from its header
//     and its central-directory size — no payload byte is read.
//
// Every arm floors a truncated tail SILENTLY (never an error);
// Inspect is the surface that warns.
func (s *Service) CountRecords(ctx context.Context, path string) (uint64, error) {
	if archivePath, anchor, ok := splitAnchorPath(path); ok {
		return s.countShardAnchor(archivePath, anchor)
	}

	fsys := s.fs.Fs()
	f, err := fsys.Open(path)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening cohort file: %s", path))
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("stat cohort file: %s", path))
	}
	size := info.Size()

	var magic [4]byte
	if size >= 4 {
		if _, err := io.ReadFull(f, magic[:]); err != nil {
			return 0, errors.WrapCodedError(err, errors.ENCODING_IO,
				fmt.Sprintf("reading magic bytes from %s", path))
		}
		if magic[0] == 'P' && magic[1] == 'K' && magic[2] == 0x03 && magic[3] == 0x04 {
			a, aerr := archiveFromFile(f, path)
			if aerr != nil {
				return 0, aerr
			}
			return countArchive(a)
		}
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("rewinding cohort file %s", path))
	}

	cr := &countingReader{r: f}
	pulseVersion, err := encoding.ReadHeader(cr)
	if err != nil {
		if errors.HasCode(err, errors.PULSE_COHORT_COMPRESSED) {
			return 0, err
		}
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("invalid pulse file: %s", path))
	}
	schema, err := encoding.ReadSchema(cr, pulseVersion)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading schema from: %s", path))
	}
	remaining := size - cr.n
	if remaining < 0 {
		return 0, errors.NewCodedError(errors.ENCODING_INVALID,
			"cohort file size smaller than header + schema")
	}
	// Floor division over the record stride, through the one shared
	// derivation descriptor.Inspect also calls — the count must be the
	// same number on both arms, and that is only guaranteed while the
	// arithmetic exists once. A non-whole payload (truncated tail)
	// floors SILENTLY here: this path has no warning channel, it feeds
	// the parallel-decode eligibility gate as well as the facade, and a
	// half-written trailing record must not stop a cohort that still
	// processes from reporting how many whole records it holds. The
	// same bytes through `pulse cohort inspect` carry the
	// ENCODING_INVALID warning naming the leftover bytes.
	count, _, ok := schema.RecordCountForPayload(remaining)
	if !ok {
		return 0, nil
	}
	return uint64(count), nil
}

// openedRecordCount is CountRecords for a single-file cohort Open
// already read: the same floor division (RecordCountForPayload) over
// the file length less the header + schema bytes Open consumed, with
// one Stat and no second schema decode. Re-reading the schema cost two
// unbuffered reads per inline dictionary entry — ~0.45 s per Process at
// 600k entries on the parallel-decode dispatch (U51 E3-S4,
// BenchmarkGroupedSumScale "cat"). Anything else (an archive, an
// anchor, an unknown offset, a failed Stat, a file shorter than its
// schema) falls back to CountRecords, which also owns the error.
func (s *Service) openedRecordCount(ctx context.Context, cohort *Cohort, path string) (uint64, error) {
	if cohort == nil || cohort.payloadOffset <= 0 || cohort.path != path || cohort.fs == nil ||
		len(cohort.shards) > 0 || cohort.anchorArchive != "" {
		return s.CountRecords(ctx, path)
	}
	info, err := cohort.fs.Stat(path)
	if err != nil {
		return s.CountRecords(ctx, path)
	}
	remaining := info.Size() - cohort.payloadOffset
	if remaining < 0 {
		return s.CountRecords(ctx, path)
	}
	count, _, ok := cohort.schema.RecordCountForPayload(remaining)
	if !ok {
		return 0, nil
	}
	return uint64(count), nil
}

// countArchive reads the reserved `_schema.pulse` entry's SHRD trailer
// of an archive opened for random access (central directory parsed,
// no shard payload read) to derive the aggregate record count. Falls
// back to per-shard PeekShardRecordCount — header plus central-directory
// size, flooring a truncated tail — when the trailer is absent (older
// archives written without sharding metadata).
func countArchive(a *archiveFile) (uint64, error) {
	arch := a.arch
	// SHRD trailer in _schema.pulse is the cheap path. When absent we
	// sum per-shard counts (still bounded by central-directory walk).
	rc, err := arch.Open(encx.ReservedSchemaName)
	if err == nil {
		doc, derr := encx.ReadSchemaDoc(rc)
		rc.Close()
		if derr == nil && doc.AggregateRecordCount > 0 {
			return doc.AggregateRecordCount, nil
		}
	}
	var total int64
	for _, e := range arch.Entries() {
		if e.Name == encx.ReservedSchemaName {
			continue
		}
		count, perr := arch.PeekShardRecordCount(e.Name)
		if perr != nil {
			return 0, perr
		}
		total += count
	}
	return uint64(total), nil
}

// countShardAnchor resolves a single shard inside an archive by name
// and returns just that shard's record count: its header plus its
// central-directory size (PeekShardRecordCount), never its payload.
func (s *Service) countShardAnchor(archivePath, anchor string) (uint64, error) {
	if anchor == encx.ReservedSchemaName {
		return 0, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot count records of the reserved canonical schema entry",
			map[string]any{"entry": anchor})
	}
	a, err := openArchiveFile(s.fs.Fs(), archivePath)
	if err != nil {
		return 0, err
	}
	defer a.Close()
	count, err := a.arch.PeekShardRecordCount(anchor)
	if err != nil {
		return 0, err
	}
	return uint64(count), nil
}

// countingReader counts bytes pulled through a wrapped io.Reader. Used
// by CountRecords to measure header + schema length without making
// assumptions about either's serialised size.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
