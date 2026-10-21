package service

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// CreateShardArchive builds a fresh Pulse shard archive at archivePath
// from the constituent shardPaths. The first shard seeds the canonical
// schema; every other shard is folded in through mergeShard — the ONE
// merge rule AddShard uses, so creating from N files and adding them one
// by one produce byte-identical archives. Dictionary growth across
// shards accumulates into the canonical schema, whose stored offsets are
// the seed's, copied verbatim.
//
// Set-width AUTO-WIDEN applies here exactly as it does on AddShard: a
// set column whose merged dictionary outgrows its declared bitmask, or
// whose rung differs between two of the shards being seeded, is promoted
// to the narrowest rung that holds every member, across the canonical
// schema and every shard folded in so far. Widening emits a MANDATORY
// PULSE_SHARD_SET_WIDENED warning on the result, which is why this
// returns a *CreateShardArchiveResult rather than a bare error: the
// shards stored in the archive are no longer byte-identical to the
// `.pulse` files the caller named, and nothing else would say so. A
// union above the widest rung stays fatal (PULSE_SHARD_DICT_WIDTH_OVERFLOW).
//
// Streaming (U51 E1-S4): no source is held beside another. Pass 1 —
// the PLAN — folds every source in order: an ungrouped merge is decided
// by schemas alone, so it reads each source's header and schema only
// and folds a zero-row stand-in, the record count coming from the file
// length. A merge involving parent groups is decided by the rows (group
// dictionaries are built from them), so that source is read whole, one
// at a time. Each source ends pass 1 with the transforms its bytes take
// — its own merge's arrival transforms, then every later archive-moving
// rewrite (a set widen, a constant-group promotion) — and the final
// canonical schema and header-only aggregate are known before a byte is
// written, as `_schema.pulse` (written first) requires. Pass 2 streams
// each source once into the staged archive (archiveWriter): verbatim
// when it takes no transform, otherwise loaded, transformed and stored
// — one source resident at a time. A grouped rebuild in pass 1 replays
// earlier sources the same way, one at a time.
//
// Errors:
//   - PULSE_SHARD_RESERVED_NAME — any shardPath has basename `_schema.pulse`.
//   - PULSE_SHARD_NAME_COLLISION — two shardPaths share a basename.
//   - PULSE_SHARD_COUNT_LIMIT — more than 65535 shardPaths.
//   - PULSE_SHARD_SCHEMA_MISMATCH / PULSE_SHARD_DICT_DIVERGENCE — propagated from cohesion validators.
//   - PULSE_SHARD_DICT_WIDTH_OVERFLOW — a set union past the widest rung.
//   - SERVICE_VALIDATION — empty shardPaths.
//   - SERVICE_RESOURCE — I/O failure on any input/output.
func (s *Service) CreateShardArchive(ctx context.Context, archivePath string, shardPaths []string) (*CreateShardArchiveResult, error) {
	if len(shardPaths) == 0 {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			"CreateShardArchive requires at least one shard path")
	}
	// The SHRD trailer's shard_count is a u16: refuse before any I/O
	// rather than store a truncated count.
	if err := checkArchiveShardCount(len(shardPaths), archivePath); err != nil {
		return nil, err
	}

	fsys := s.fs.Fs()

	// Reserved-name + basename-collision precheck. Both errors are
	// caller-fixable; surface them before any I/O.
	if err := validateBasenames(shardPaths); err != nil {
		return nil, err
	}

	result := &CreateShardArchiveResult{Archive: archivePath,
		Warnings: []encx.CohesionWarning{}}

	// Pass 1: the plan.
	sources := &createShards{fsys: fsys, src: make([]*createSource, 0, len(shardPaths))}
	var canonical *encoding.Schema
	var aggregate uint64
	for i, p := range shardPaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		schema, records, err := peekSourceShard(fsys, p)
		if err != nil {
			return nil, err
		}
		base := filepath.Base(p)
		src := &createSource{path: p, name: base, records: records}
		aggregate += uint64(records)
		if i == 0 {
			canonical = cloneSchemaForArchive(schema)
			sources.src = append(sources.src, src)
			continue
		}

		// An ungrouped merge reads no row: fold a zero-row stand-in with
		// the source's schema. A grouped one is decided by the rows.
		var incoming []byte
		inRecords := records
		if canonical.HasGroups() || schema.HasGroups() {
			if incoming, err = readSourceShard(fsys, p); err != nil {
				return nil, err
			}
			inRecords = -1
		} else {
			var buf bytes.Buffer
			if err := encx.WritePreamble(&buf, schema); err != nil {
				return nil, err
			}
			incoming = buf.Bytes()
		}

		m, merr := mergeShard(canonical, sources, incoming, inRecords, base, archivePath)
		if merr != nil {
			return nil, merr
		}
		// The archive moved: every source folded so far takes the
		// rewrite after its own transforms, in fold order.
		if m.rewrite != nil {
			for _, prev := range sources.src {
				prev.ops = append(prev.ops, m.rewrite)
			}
		}
		src.ops = m.arrival
		canonical = m.canonical
		result.Widened = append(result.Widened, m.widened...)
		result.Regrouped = append(result.Regrouped, m.regrouped...)
		result.Warnings = append(result.Warnings, m.warnings...)
		sources.src = append(sources.src, src)
	}

	// Pass 2: stream every source once into the stage.
	w, err := newArchiveWriter(fsys, archivePath)
	if err != nil {
		return nil, err
	}
	defer w.abort()
	if err := w.writeSchemaDoc(canonical, aggregate, len(sources.src)); err != nil {
		return nil, err
	}
	for i, src := range sources.src {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(src.ops) == 0 {
			if err := streamSourceEntry(w, fsys, src); err != nil {
				return nil, err
			}
			continue
		}
		payload, err := sources.loadShard(i)
		if err != nil {
			return nil, err
		}
		if err := w.writeEntry(src.name, payload); err != nil {
			return nil, err
		}
	}
	if err := w.commit(); err != nil {
		return nil, err
	}
	result.ShardCount = len(sources.src)
	return result, nil
}

// createSource is one source of a streaming create: where it is, its
// header-only record count, and the transforms its bytes take on the
// way into the archive — never its bytes.
type createSource struct {
	path    string
	name    string
	records int64
	ops     []storedRewrite
}

// createShards is storedShards over the sources folded so far: a
// payload is the source re-read and replayed through its transforms,
// one at a time (a grouped rebuild's discovery pass, pass 2).
type createShards struct {
	fsys afero.Fs
	src  []*createSource
}

func (c *createShards) shardCount() int                   { return len(c.src) }
func (c *createShards) shardName(i int) string            { return c.src[i].name }
func (c *createShards) shardRecords(i int) (int64, error) { return c.src[i].records, nil }
func (c *createShards) loadShard(i int) ([]byte, error) {
	src := c.src[i]
	payload, err := readSourceShard(c.fsys, src.path)
	if err != nil {
		return nil, err
	}
	for _, op := range src.ops {
		if payload, err = op(src.name, payload); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

// readSourceShard reads one source file whole.
func readSourceShard(fsys afero.Fs, path string) ([]byte, error) {
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("CreateShardArchive: reading shard source %s", path))
	}
	return data, nil
}

// peekSourceShard reads a source's header and schema block only, and
// derives its record count from the file length (a truncated tail
// floors, as PeekShardRecordCount does). No record byte is read.
func peekSourceShard(fsys afero.Fs, path string) (*encoding.Schema, int64, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("CreateShardArchive: reading shard source %s", path))
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("CreateShardArchive: reading shard source %s", path))
	}
	// Counted ABOVE the buffer: what the schema parse consumed, not what
	// the buffer pre-fetched.
	cr := &countingReader{r: bufio.NewReaderSize(f, 4<<10)}
	version, err := encoding.ReadHeader(cr)
	if err != nil {
		return nil, 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"reading shard header")
	}
	schema, err := encoding.ReadSchema(cr, version)
	if err != nil {
		return nil, 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"reading shard schema")
	}
	n, _, ok := schema.RecordCountForPayload(st.Size() - cr.n)
	if !ok {
		return schema, 0, nil
	}
	return schema, n, nil
}

// streamSourceEntry copies a source that takes no transform into the
// stage verbatim, through the writer's copy buffer — never held.
func streamSourceEntry(w *archiveWriter, fsys afero.Fs, src *createSource) error {
	f, err := fsys.Open(src.path)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("CreateShardArchive: reading shard source %s", src.path))
	}
	defer f.Close()
	ew, err := w.createEntry(src.name)
	if err != nil {
		return err
	}
	// Hide the file's WriteTo: *os.File's falls back to a fresh 32 KiB
	// buffer per call, an allocation per source instead of the writer's
	// one shared copy buffer.
	if _, err := io.CopyBuffer(ew, struct{ io.Reader }{f}, w.buf); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("CreateShardArchive: copying shard source %s", src.path))
	}
	return nil
}

// checkArchiveShardCount refuses an archive of more shards than the
// SHRD trailer's u16 shard_count holds (encx.MaxArchiveShards). Every
// writer calls it before writing anything: a stored count that silently
// wrapped would misreport the archive to every header-only reader.
func checkArchiveShardCount(n int, archivePath string) error {
	if n <= encx.MaxArchiveShards {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_COUNT_LIMIT,
		fmt.Sprintf("archive %s would hold %d shards; an archive holds at most %d (the `_schema.pulse` shard_count is a u16)",
			archivePath, n, encx.MaxArchiveShards),
		map[string]any{"archive": archivePath, "shard_count": n, "limit": encx.MaxArchiveShards})
}
