package service

import (
	"archive/zip"
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

// AddShard appends shardPath to the archive at archivePath. The
// incoming shard is validated against the archive's canonical schema
// via ValidateStructuralCohesion + MergeDictUnion. If the union-merge
// extends the canonical dictionary, the rewritten `_schema.pulse`
// payload reflects the extension.
//
// When a merged dictionary outgrows a set_* field's bitmask, the field
// is WIDENED to the narrowest rung that holds it — across the canonical
// schema and every shard payload in the archive — rather than the add
// being refused. Refusing forces a full re-import (re-read the source,
// re-infer, rebuild), which is strictly more expensive than the
// mechanical re-stride it would be avoiding. Widening emits a MANDATORY
// PULSE_SHARD_SET_WIDENED warning on the result: an expensive
// whole-archive rewrite that happens silently is indistinguishable from
// a cheap append. A union above the widest rung has nowhere to go and
// stays fatal (PULSE_SHARD_DICT_WIDTH_OVERFLOW).
//
// Streaming rewrite: the archive is opened by random access (central
// directory + `_schema.pulse`), the aggregate is summed header-only
// (PeekShardRecordCount), and a new archive is streamed into a staged
// file beside the original (archiveWriter) — the refreshed
// `_schema.pulse`, every stored shard copied RAW with zip CreateRaw
// from a SectionReader (no decode), then the new shard — and renamed
// over it. Memory is O(the new shard), I/O O(the archive). Only when the
// archive itself moves (an archive-level set widen, a constant-group
// promotion) is each stored shard read, one at a time, and re-encoded
// on its way into the stage. The rename is what makes the widen safe
// without locking — concurrency is caller-owned by contract, and a
// failure (or a crash) at any point before it leaves the original
// archive byte-identical and openable rather than half-widened (which
// would open, and decode every post-widen field at the wrong offset).
// True in-place tail append is not done: it would give up exactly that
// guarantee.
func (s *Service) AddShard(ctx context.Context, archivePath, shardPath string) (*AddShardResult, error) {
	fsys := s.fs.Fs()

	base := filepath.Base(shardPath)
	if base == encx.ReservedSchemaName {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			fmt.Sprintf("cannot add a shard with the reserved basename %q", encx.ReservedSchemaName),
			map[string]any{"basename": base})
	}

	a, err := openArchiveFile(fsys, archivePath)
	if err != nil {
		return nil, err
	}
	defer a.Close()

	// Read incoming shard bytes: the one payload an ordinary add holds.
	incomingBytes, err := afero.ReadFile(fsys, shardPath)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: reading shard source %s", shardPath))
	}
	// Parse the incoming header + schema up front: a malformed shard is
	// PULSE_SHARD_HEADER_INVALID before the archive is enumerated.
	if _, err := readSinglePulseSchema(incomingBytes); err != nil {
		return nil, err
	}

	canonicalDoc, err := readSchemaDocEntry(a.arch)
	if err != nil {
		return nil, err
	}

	result := &AddShardResult{Archive: archivePath, Added: shardPath,
		Warnings: []encx.CohesionWarning{}}

	// Enumerate the stored shards, detecting a name collision before any
	// payload is read.
	stored := &archiveShards{a: a}
	for _, e := range a.arch.Entries() {
		if e.Name == encx.ReservedSchemaName {
			continue
		}
		if e.Name == base {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_NAME_COLLISION,
				fmt.Sprintf("archive already contains a shard named %q", base),
				map[string]any{"basename": base})
		}
		stored.names = append(stored.names, e.Name)
	}
	// The SHRD trailer's shard_count is a u16: refuse an add past it
	// before any shard is merged or written.
	if err := checkArchiveShardCount(stored.shardCount()+1, archivePath); err != nil {
		return nil, err
	}

	// The one merge rule create shares (mergeShard): set-rung
	// reconciliation PLANNED before strict cohesion — that order is the
	// whole of the relaxation, because a divergent rung fails cohesion on
	// the type byte and on every offset it shifts — then strict cohesion
	// over the RECONCILED schemas, the dictionary union, and for grouped
	// shards the parent-group reconciliation. m.rewrite is non-nil only
	// when the archive itself moves.
	m, err := mergeShard(canonicalDoc.Schema, stored, incomingBytes, -1, base, archivePath)
	if err != nil {
		return nil, err
	}
	result.Widened = m.widened
	result.Regrouped = m.regrouped
	result.Warnings = append(result.Warnings, m.warnings...)

	// Aggregate record count, header-only: each stored shard's own
	// header (never the cached `_schema.pulse` aggregate, which may lag an
	// archive edited outside Pulse) plus the arrival. A widen or regroup
	// re-lays-out records but never changes how many there are.
	aggregate, err := sumStoredRecords(stored)
	if err != nil {
		return nil, err
	}
	n, err := recordCountFromBytes(m.incoming, m.canonical)
	if err != nil {
		return nil, err
	}
	aggregate += uint64(n)
	shardCount := stored.shardCount() + 1

	w, err := newArchiveWriter(fsys, archivePath)
	if err != nil {
		return nil, err
	}
	defer w.abort()
	if err := w.writeSchemaDoc(m.canonical, aggregate, shardCount); err != nil {
		return nil, err
	}
	if err := writeStoredShards(w, a, stored, m.rewrite); err != nil {
		return nil, err
	}
	if err := w.writeEntry(base, m.incoming); err != nil {
		return nil, err
	}
	// The source handle is released before the rename replaces its file.
	if err := a.Close(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: closing archive %s", archivePath))
	}
	if err := w.commit(); err != nil {
		return nil, err
	}
	result.ShardCount = shardCount
	return result, nil
}

// writeStoredShards streams every stored shard into w in archive order:
// copied raw when rewrite is nil, otherwise read one at a time, passed
// through rewrite and stored — so a rewrite holds one shard, never the
// archive.
func writeStoredShards(w *archiveWriter, a *archiveFile, stored storedShards, rewrite storedRewrite) error {
	for i := range stored.shardCount() {
		name := stored.shardName(i)
		if rewrite == nil {
			if err := w.copyEntry(a, name); err != nil {
				return err
			}
			continue
		}
		payload, err := stored.loadShard(i)
		if err != nil {
			return err
		}
		if payload, err = rewrite(name, payload); err != nil {
			return err
		}
		if err := w.writeEntry(name, payload); err != nil {
			return err
		}
	}
	return nil
}

// RemoveShard rewrites the archive at archivePath omitting the named
// shard. The canonical schema is preserved (orphaned dictionary
// entries are retained — canonical only grows). Returns
// PULSE_SHARD_MISSING when the named shard is not present.
//
// Streams like AddShard: the kept shards are copied raw into a staged
// archive, the aggregate is summed header-only, and one rename
// publishes it — O(one copy buffer) memory, the original byte-identical
// until the rename.
func (s *Service) RemoveShard(ctx context.Context, archivePath, shardBasename string) error {
	fsys := s.fs.Fs()
	if shardBasename == encx.ReservedSchemaName {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot remove the reserved canonical schema entry",
			map[string]any{"basename": shardBasename})
	}

	a, err := openArchiveFile(fsys, archivePath)
	if err != nil {
		return err
	}
	defer a.Close()

	canonicalDoc, err := readSchemaDocEntry(a.arch)
	if err != nil {
		return err
	}

	kept := &archiveShards{a: a}
	found := false
	for _, e := range a.arch.Entries() {
		if e.Name == encx.ReservedSchemaName {
			continue
		}
		if e.Name == shardBasename {
			found = true
			continue
		}
		kept.names = append(kept.names, e.Name)
	}
	if !found {
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_MISSING,
			fmt.Sprintf("shard %q not present in archive", shardBasename),
			map[string]any{"entry": shardBasename})
	}
	return rewriteKeptShards(fsys, a, archivePath, canonicalDoc.Schema, kept)
}

// rewriteKeptShards publishes an archive holding canonical and the kept
// stored shards, copied raw, under a refreshed `_schema.pulse` whose
// aggregate is summed header-only. RemoveShard and CompactShardArchive
// share it. It closes a before the rename.
func rewriteKeptShards(fsys afero.Fs, a *archiveFile, archivePath string, canonical *encoding.Schema, kept *archiveShards) error {
	aggregate, err := sumStoredRecords(kept)
	if err != nil {
		return err
	}
	w, err := newArchiveWriter(fsys, archivePath)
	if err != nil {
		return err
	}
	defer w.abort()
	if err := w.writeSchemaDoc(canonical, aggregate, kept.shardCount()); err != nil {
		return err
	}
	if err := writeStoredShards(w, a, kept, nil); err != nil {
		return err
	}
	if err := a.Close(); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("closing archive %s", archivePath))
	}
	return w.commit()
}

// ListShards opens the archive and returns its ShardEntry slice in
// central-directory order (reuses Service.Open which peeks per-shard
// headers to populate RecordCount). Single-file cohorts return an
// empty slice.
func (s *Service) ListShards(ctx context.Context, archivePath string) ([]ShardEntry, error) {
	cohort, err := s.Open(ctx, archivePath)
	if err != nil {
		return nil, err
	}
	return cohort.Shards(), nil
}

// ExtractShard returns an io.ReadCloser over the named shard's
// standalone single-file `.pulse` bytes. The closer releases the
// archive resources; callers must invoke Close after draining.
//
// The archive is read by random access: the reader streams the one
// entry (CRC-checked at EOF) off an open archive handle, which Close
// releases — no other entry, and no whole-archive copy, is read.
func (s *Service) ExtractShard(ctx context.Context, archivePath, shardBasename string) (io.ReadCloser, error) {
	a, err := openArchiveFile(s.fs.Fs(), archivePath)
	if err != nil {
		return nil, err
	}
	if shardBasename == encx.ReservedSchemaName {
		_ = a.Close()
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot extract the reserved canonical schema entry as a shard",
			map[string]any{"basename": shardBasename})
	}
	rc, err := a.arch.Open(shardBasename)
	if err != nil {
		_ = a.Close()
		return nil, err
	}
	return &archiveEntryCloser{ReadCloser: rc, archive: a}, nil
}

// archiveEntryCloser is an entry reader that also releases its
// archive handle on Close.
type archiveEntryCloser struct {
	io.ReadCloser
	archive *archiveFile
}

func (c *archiveEntryCloser) Close() error {
	err := c.ReadCloser.Close()
	if aerr := c.archive.Close(); err == nil {
		err = aerr
	}
	return err
}

// validateBasenames runs the reserved-name and uniqueness checks over
// the caller-supplied shard paths. Returns the first failure
// encountered. Order-stable so test output is deterministic.
func validateBasenames(paths []string) error {
	seen := make(map[string]struct{}, len(paths))
	// Process in input order so error messages reference the first
	// offending basename.
	for _, p := range paths {
		base := filepath.Base(p)
		if base == encx.ReservedSchemaName {
			return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
				fmt.Sprintf("shard path %q has the reserved basename %q",
					p, encx.ReservedSchemaName),
				map[string]any{"path": p, "basename": base})
		}
		if _, dup := seen[base]; dup {
			return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_NAME_COLLISION,
				fmt.Sprintf("shard basename %q appears more than once in the create list", base),
				map[string]any{"basename": base})
		}
		seen[base] = struct{}{}
	}
	return nil
}

// readSinglePulseSchema parses a single-file `.pulse` byte slice and
// returns the embedded schema. Used by Create/Add to seed cohesion
// validation before forwarding the payload into the archive.
func readSinglePulseSchema(data []byte) (*encoding.Schema, error) {
	r := bytes.NewReader(data)
	pulseVersion, err := encoding.ReadHeader(r)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"reading shard header")
	}
	schema, err := encoding.ReadSchema(r, pulseVersion)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"reading shard schema")
	}
	return schema, nil
}

// cloneSchemaForArchive returns a deep enough copy of s suitable as the
// canonical schema seed. Reuses encoding's clone via re-marshal — keeps
// the unexported encoding.cloneSchema unused here. Field descriptors
// are copied by value; dictionaries are reconstructed.
func cloneSchemaForArchive(s *encoding.Schema) *encoding.Schema {
	if s == nil {
		return nil
	}
	out := &encoding.Schema{Fields: make([]encoding.Field, len(s.Fields))}
	for i, f := range s.Fields {
		out.Fields[i] = f
		if f.Dictionary != nil {
			d := encoding.NewDictionary()
			for _, v := range f.Dictionary.Values() {
				_, _ = d.Add(v)
			}
			out.Fields[i].Dictionary = d
		}
	}
	// A grouped seed keeps its group layout and dictionaries: the
	// canonical schema decides every shard's physical stride.
	out.Groups = encx.CloneGroups(s.Groups)
	out.Vectors = encx.CloneVectors(s.Vectors)
	return out
}

// recordCountFromBytes derives the shard's record count from its
// payload bytes against the canonical schema's per-record stride. It
// reads the shard's own header and schema block only to position the
// reader at the first record and to reject a payload that is not a
// valid single-file cohort; the stride itself comes from the canonical
// schema, which per-shard structural cohesion guarantees agrees with
// the shard's own.
//
// The arithmetic is deliberately NOT written here. It is
// encoding.Schema.RecordCountForPayload — the one derivation behind
// every "how many records does this hold" answer — because a local
// sum of FieldType.ByteSize() drops the trailing per-record null
// bitmap and scores bit-packed fields (u4, packed_bool, ByteSize()==0)
// as zero bytes wide, which is exactly how this call site used to bake
// an inflated aggregate_record_count into `_schema.pulse`.
//
// A truncated tail FLOORS silently, matching
// Archive.PeekShardRecordCount on the read side. Every caller here
// (CreateShardArchive, AddShard, RemoveShard, CompactShardArchive)
// returns a bare error with no warning channel, and refreshing cached
// metadata must not be the operation that fails on an archive that
// still opens and still processes. `pulse shard verify` is the
// diagnostic arm that owns reporting a short tail.
func recordCountFromBytes(payload []byte, schema *encoding.Schema) (int64, error) {
	r := bytes.NewReader(payload)
	pulseVersion, err := encoding.ReadHeader(r)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"recordCountFromBytes: reading shard header")
	}
	// Layout-only: the stride comes from the canonical schema, so the
	// shard's dictionaries and group entry tables are read past, never
	// allocated (TestRecordCountFromBytes_SkipsDictionaries).
	if _, err := encx.ReadSchemaLayout(r, pulseVersion); err != nil {
		return 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"recordCountFromBytes: reading shard schema")
	}
	count, _, ok := schema.RecordCountForPayload(int64(r.Len()))
	if !ok {
		// Field-less schema (non-positive stride): no records to
		// count, matching PeekShardRecordCount's zero fallback.
		return 0, nil
	}
	return count, nil
}

// readSchemaDocEntry parses the archive's reserved `_schema.pulse`
// entry.
func readSchemaDocEntry(arch *encx.Archive) (*encx.SchemaDoc, error) {
	rc, err := arch.Open(encx.ReservedSchemaName)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	doc, err := encx.ReadSchemaDoc(rc)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_INVALID,
			"reading archive canonical schema")
	}
	return doc, nil
}

// readEntrySchemaVerified parses the named entry's header + schema off
// its entry stream, then drains the rest into io.Discard so the zip
// reader verifies the entry's CRC — what reading it whole did, without
// holding the payload. A short or malformed prefix is
// PULSE_SHARD_HEADER_INVALID; a failed drain (a CRC mismatch, a
// truncated entry) is ENCODING_IO.
func readEntrySchemaVerified(arch *encx.Archive, name string) (*encoding.Schema, error) {
	rc, err := arch.Open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	r := bufio.NewReader(rc)
	pulseVersion, err := encoding.ReadHeader(r)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"reading shard header")
	}
	schema, err := encoding.ReadSchema(r, pulseVersion)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"reading shard schema")
	}
	if _, err := io.Copy(io.Discard, r); err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("reading archive entry %q", name))
	}
	return schema, nil
}

// readEntryBytes drains the named entry into a byte slice.
func readEntryBytes(arch *encx.Archive, name string) ([]byte, error) {
	rc, err := arch.Open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("reading archive entry %q", name))
	}
	return data, nil
}

// buildSchemaDocPayload serialises a canonical schema + sharding
// metadata extension into a byte slice suitable for storage under
// encoding.ReservedSchemaName. A shard count past the u16 trailer field
// is refused (PULSE_SHARD_COUNT_LIMIT), never truncated — the last line
// of defence behind each writer's own up-front check.
func buildSchemaDocPayload(schema *encoding.Schema, agg uint64, shardCount int, archivePath string) ([]byte, error) {
	if err := checkArchiveShardCount(shardCount, archivePath); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := encx.WriteSchemaDoc(&buf, schema, agg, uint16(shardCount)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeArchiveEntry adds one store-only zip entry to zw.
func writeArchiveEntry(zw *zip.Writer, name string, payload []byte) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("creating archive entry %q", name))
	}
	if _, err := w.Write(payload); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("writing archive entry %q", name))
	}
	return nil
}
