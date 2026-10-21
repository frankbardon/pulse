package service

import (
	"bufio"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// archiveFile is a shard archive opened for RANDOM ACCESS: one file
// handle, its byte length, a concurrency-safe io.ReaderAt over it
// (concurrentReaderAt) and the zip central directory parsed once
// (encx.OpenArchive). Opening reads only the 4-byte magic and the
// directory at the file's tail — never the shard payloads — so its cost
// is O(shards), not O(archive bytes) (TestArchiveOpen_AllocsIndependentOfBytes).
//
// Every whole-archive read path in this package goes through it: Open,
// OpenAnchor, CountRecords, the serial shard iterator, the per-shard
// parallel reducer, shard verify / precheck / extract and the
// filter-to-file archive arm. A shard's records are then read through
// storedShard / shardReader: a SectionReader at the entry's data
// offset, which any number of goroutines may drive at once.
//
// The caller owns the handle: Close releases it, and no SectionReader
// carved from it may be read afterwards.
type archiveFile struct {
	path string
	f    afero.File
	size int64
	ra   io.ReaderAt
	arch *encx.Archive
}

// openArchiveFile opens path on fsys and parses its central directory.
// Errors keep the codes the slurping callers raised: SERVICE_RESOURCE
// for an unopenable file, the encx.OpenArchive codes
// (PULSE_ARCHIVE_MAGIC_INVALID, PULSE_ARCHIVE_CORRUPT,
// PULSE_COHORT_COMPRESSED) for a file that is not a readable archive.
func openArchiveFile(fsys afero.Fs, path string) (*archiveFile, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("opening shard archive: %s", path))
	}
	a, err := archiveFromFile(f, path)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return a, nil
}

// archiveFromFile parses the central directory of an already-open
// archive handle f. On success the returned archiveFile owns f.
func archiveFromFile(f afero.File, path string) (*archiveFile, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("sizing shard archive: %s", path))
	}
	ra := concurrentReaderAt(f)
	arch, err := encx.OpenArchive(ra, st.Size())
	if err != nil {
		return nil, err
	}
	return &archiveFile{path: path, f: f, size: st.Size(), ra: ra, arch: arch}, nil
}

// Close releases the archive's file handle.
func (a *archiveFile) Close() error {
	if a == nil || a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return err
}

// storedShard returns a SectionReader over the named entry's stored
// payload. A deflated (non-store) entry is REFUSED with
// PULSE_ARCHIVE_CORRUPT — its section holds compressed bytes, which
// would decode as garbage records — the same refusal the index-addressed
// record reader raises (openArchiveRecordReader). Pulse writes only
// store-only entries, so only a third-party tool reaches the refusal.
// An unknown name is PULSE_SHARD_MISSING.
func (a *archiveFile) storedShard(name string) (*io.SectionReader, error) {
	sect, err := a.arch.OpenAt(name)
	if err != nil {
		return nil, err
	}
	if !a.arch.IsStored(name) {
		return nil, deflatedShardError(a.path, name)
	}
	return &sect, nil
}

// deflatedShardError is the refusal for a shard entry that is not
// store-only.
func deflatedShardError(path, name string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_ARCHIVE_CORRUPT,
		"shard entry is not store-only, so its records cannot be read by offset",
		map[string]any{"archive": path, "entry": name})
}

// shardReadBuffer bounds the read-ahead of one shard scan: each refill
// is one positional read of the archive, so a scan costs payload /
// shardReadBuffer reads instead of one per record, and its resident
// footprint is this buffer, never the shard.
const shardReadBuffer = 256 << 10

// shardReader returns a buffered sequential reader over the named
// stored shard (storedShard's refusals apply). The reader also
// implements a forward-only io.Seeker, so a projected decode
// (encx.DecodePlan SkipBytes) discards unprojected bytes from the
// buffer instead of copying them through io.Discard.
func (a *archiveFile) shardReader(name string) (io.Reader, error) {
	sect, err := a.storedShard(name)
	if err != nil {
		return nil, err
	}
	return newSectionScanner(sect), nil
}

// sectionScanner is a bufio.Reader over a SectionReader that also
// answers Seek(n, io.SeekCurrent) for n ≥ 0 by discarding n bytes.
type sectionScanner struct {
	br  *bufio.Reader
	pos int64
}

func newSectionScanner(sect *io.SectionReader) *sectionScanner {
	size := min(int64(shardReadBuffer), max(sect.Size(), 16))
	return &sectionScanner{br: bufio.NewReaderSize(sect, int(size))}
}

func (s *sectionScanner) Read(p []byte) (int, error) {
	n, err := s.br.Read(p)
	s.pos += int64(n)
	return n, err
}

// Seek supports only a forward relative seek — the one advanceReader
// issues — and reports the new position from the section start. A seek
// past the end is not an error, exactly as on the *bytes.Reader the
// scan used to read: the next Read reports io.EOF.
func (s *sectionScanner) Seek(offset int64, whence int) (int64, error) {
	if whence != io.SeekCurrent || offset < 0 {
		return s.pos, errors.NewCodedError(errors.ENCODING_INTERNAL,
			"shard scanner seeks forward only")
	}
	for offset > 0 {
		step := int(min(offset, int64(shardReadBuffer)))
		n, err := s.br.Discard(step)
		s.pos += int64(n)
		offset -= int64(n)
		if err == io.EOF {
			return s.pos, nil
		}
		if err != nil {
			return s.pos, err
		}
	}
	return s.pos, nil
}
