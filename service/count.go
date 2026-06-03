package service

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// CountRecords returns the total record count for the cohort at path
// without decoding any payload bytes. Three resolution paths:
//
//   - Single-file `.pulse`: stat the file, stream the header + schema,
//     and derive count = (file_size − header − schema) / record_stride.
//     Bytes read is bounded by header + schema size — O(1) in the
//     record count.
//   - Shard archive (zip magic): read the archive's central directory
//   - the reserved `_schema.pulse` entry. The SHRD trailer's
//     AggregateRecordCount is the cached sum of every shard's record
//     count; cohesive archives keep it in lockstep with the
//     per-shard headers. Cost is O(N shards) for the directory walk,
//     not O(total records).
//   - Anchor path `archive#shard`: opens the archive's central
//     directory and uses the named shard's header to derive count.
//     Cost is O(shard payload size) today; the v1 contract is
//     "no full-archive decode," not "header-only" for anchors.
//
// Embedders that want strict O(1) for anchors can call
// pulse.Inspect(archive) and read the per-shard counts from the
// returned Shards slice.
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
			return s.countArchive(fsys, path)
		}
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("rewinding cohort file %s", path))
	}

	cr := &countingReader{r: f}
	if err := encoding.ReadHeader(cr); err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("invalid pulse file: %s", path))
	}
	schema, err := encoding.ReadSchema(cr)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			fmt.Sprintf("reading schema from: %s", path))
	}
	stride := schema.RecordByteSize()
	if stride <= 0 {
		return 0, nil
	}
	remaining := size - cr.n
	if remaining < 0 {
		return 0, errors.NewCodedError(errors.ENCODING_INVALID,
			"cohort file size smaller than header + schema")
	}
	return uint64(remaining) / uint64(stride), nil
}

// countArchive reads the zip central directory + the reserved
// `_schema.pulse` entry's SHRD trailer to derive the aggregate
// record count. Falls back to per-shard PeekShardRecordCount when
// the trailer is absent (older archives written without sharding
// metadata).
func (s *Service) countArchive(fsys afero.Fs, path string) (uint64, error) {
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("reading archive %s", path))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, err
	}
	// SHRD trailer in _schema.pulse is the cheap path. When absent we
	// sum per-shard counts (still bounded by central-directory walk).
	rc, err := arch.Open(encoding.ReservedSchemaName)
	if err == nil {
		doc, derr := encoding.ReadSchemaDoc(rc)
		rc.Close()
		if derr == nil && doc.AggregateRecordCount > 0 {
			return doc.AggregateRecordCount, nil
		}
	}
	var total int64
	for _, e := range arch.Entries() {
		if e.Name == encoding.ReservedSchemaName {
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
// and returns just that shard's record count.
func (s *Service) countShardAnchor(archivePath, anchor string) (uint64, error) {
	if anchor == encoding.ReservedSchemaName {
		return 0, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot count records of the reserved canonical schema entry",
			map[string]any{"entry": anchor})
	}
	fsys := s.fs.Fs()
	data, err := afero.ReadFile(fsys, archivePath)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening archive: %s", archivePath))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, err
	}
	count, err := arch.PeekShardRecordCount(anchor)
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
