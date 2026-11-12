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
// Tail append (shard_append.go, U73 #492): an ORDINARY add — no
// archive-level widen, no constant-group promotion, so every stored
// shard keeps its bytes — appends the new shard, a refreshed
// `_schema.pulse` and a new zip64 central directory to the archive's
// tail in place: bytes written are O(the new shard + the directory),
// whatever the archive's size, and the superseded `_schema.pulse` and
// directory stay behind as dead bytes (CompactShardArchive reclaims
// them). A write-open the platform refuses is PULSE_SHARD_APPEND_REFUSED,
// nothing written; a crash mid-append is journalled (shard_append.go)
// and rolled back by RepairShardArchive.
//
// Streaming rewrite: when the archive itself moves (an archive-level set
// widen, a constant-group promotion) — or its tail is a layout the
// appender does not extend — the archive is opened by random access
// (central directory + `_schema.pulse`), the aggregate is summed
// header-only (PeekShardRecordCount), and a new archive is streamed into
// a staged file beside the original (archiveWriter) — the refreshed
// `_schema.pulse`, every stored shard (re-encoded one at a time on its
// way into the stage when the archive moves, otherwise copied RAW with
// zip CreateRaw from a SectionReader), then the new shard — and renamed
// over it. Memory is O(the new shard), I/O O(the archive). The rename is
// what makes the widen safe without locking — concurrency is
// caller-owned by contract, and a failure (or a crash) at any point
// before it leaves the original archive byte-identical and openable
// rather than half-widened (which would open, and decode every
// post-widen field at the wrong offset). That is why a widen or a
// promotion never appends.
//
// Point-lookup indexes (index_upkeep.go): an ordinary add builds the new
// shard's sidecar for every archive index and the updated index
// manifest BEFORE the archive is published — any failure aborts the
// whole add with the archive, every sidecar and the manifest untouched
// — then publishes the archive (the tail append, or the rename), then
// renames the sidecars and the manifest, in that order, with the
// mandatory PULSE_SHARD_INDEXES_UPDATED warning. An add
// that re-encodes the stored shards keeps nothing and reports the
// sidecars it invalidated (AddShardResult.InvalidatedSidecars).
//
// Dead bytes (U73 E2-S3): every append leaves the superseded
// `_schema.pulse` and directory behind, so an ordinary add whose append
// would leave dead bytes past HALF the archive (appendDeadByteLimit)
// takes the rewrite instead — reclaiming them all — with ONE mandatory
// PULSE_SHARD_DEAD_BYTES_RECLAIMED warning. AddShardWithOptions'
// Rewrite forces the rewrite outright.
func (s *Service) AddShard(ctx context.Context, archivePath, shardPath string) (*AddShardResult, error) {
	return s.AddShardWithOptions(ctx, archivePath, shardPath, AddShardOptions{})
}

// AddShardOptions tunes AddShardWithOptions. The zero value is AddShard.
type AddShardOptions struct {
	// Rewrite forces the whole-archive streaming rewrite (staged file +
	// rename) for an add that would otherwise append in place: the
	// archive is never opened for writing (the answer to
	// PULSE_SHARD_APPEND_REFUSED) and its dead bytes are reclaimed. No
	// warning: the caller asked for it.
	Rewrite bool
}

// AddShardWithOptions is AddShard with options (`pulse shard add
// --rewrite`).
func (s *Service) AddShardWithOptions(ctx context.Context, archivePath, shardPath string, opts AddShardOptions) (*AddShardResult, error) {
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

	// The archive's point-lookup indexes, read before anything is
	// written: an unreadable manifest aborts the add (build before
	// publish — an index the add cannot read, it cannot keep).
	manifest, indexEntries, err := readArchiveIndexes(fsys, archivePath)
	if err != nil {
		return nil, err
	}

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

	// Per-shard distinct counts (U73 #500): the arrival is counted from
	// the bytes the archive will store; every stored shard CARRIES the
	// prior trailer's entry (its words did not change — a widen or
	// promotion re-lays-out records, never an eligible field's words),
	// and a stored shard the prior trailer does not cover (an archive
	// written before it) is recorded absent, never recounted here.
	// A carry is identity-checked (U73 #720): a stored shard whose entry
	// its central-directory identity no longer vouches for — replaced
	// outside Pulse under the same name, or recorded without an identity
	// — is recounted, and every entry is stamped with the identity of the
	// bytes the new archive stores (addDistinct).
	dist, err := s.addDistinct(ctx, a, stored, m, base)
	if err != nil {
		return nil, err
	}

	// An ordinary add keeps every stored shard's bytes, so it appends to
	// the archive's tail in place (shard_append.go); a tail the appender
	// does not extend (prepended data, a multi-disk or trailing-bytes
	// layout) takes the rewrite below, as does an archive holding a
	// deflated entry (a third-party zip tool's), which the rewrite
	// re-stores.
	// An append that would leave the archive more than half dead bytes,
	// or a caller's Rewrite, takes the rewrite too.
	if m.rewrite == nil && allEntriesStored(a) && !opts.Rewrite {
		dir, err := encx.ReadArchiveDirectory(a.ra, a.size)
		switch {
		case err == nil:
			schemaDoc, err := buildSchemaDocPayload(m.canonical, aggregate, shardCount, dist, archivePath)
			if err != nil {
				return nil, err
			}
			reclaim, err := appendDeadBytesFallback(a, dir, archivePath, base, len(m.incoming), len(schemaDoc))
			if err != nil {
				return nil, err
			}
			if reclaim == nil {
				return s.appendShard(fsys, archivePath, a, dir, m, base, schemaDoc, shardCount, manifest, indexEntries, result)
			}
			result.Warnings = append(result.Warnings, *reclaim)
		case !encx.IsNotAppendable(err):
			return nil, err
		}
	}

	w, err := newArchiveWriter(fsys, archivePath)
	if err != nil {
		return nil, err
	}
	defer w.abort()
	if err := w.writeSchemaDoc(m.canonical, aggregate, shardCount, dist); err != nil {
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
	if err := w.finish(); err != nil {
		return nil, err
	}
	// Index upkeep, BUILT BEFORE PUBLISH: the new shard's sidecars and
	// the updated manifest are staged from the finished stage archive;
	// any failure here returns with the archive, every sidecar and the
	// manifest untouched (w.abort and the upkeep's own abort remove the
	// stages). A re-encoding add keeps nothing — every stored shard's
	// CRC32 moves — and reports the sidecars it invalidated instead.
	var upkeep *indexUpkeep
	if manifest != nil {
		if m.rewrite == nil {
			upkeep, err = stageAddUpkeep(fsys, fsys, archivePath, w.stagedPath(), m.canonical, base, manifest, indexEntries)
			if err != nil {
				return nil, err
			}
			defer upkeep.abort()
		} else {
			result.InvalidatedSidecars = staleArchiveSidecars(archivePath, manifest, indexEntries)
		}
	}
	if err := w.publish(); err != nil {
		return nil, err
	}
	if upkeep != nil {
		result.Warnings = append(result.Warnings, upkeep.publish())
	}
	result.ShardCount = shardCount
	return result, nil
}

// addDistinct is the DIST trailer an add writes. Every entry records
// the identity of the bytes the new archive stores for its shard, and
// `_schema.pulse` is written FIRST, so each identity is known before any
// shard is: a kept shard's off the central directory (append, raw copy
// and re-store all keep its uncompressed bytes), the arrival's off its
// held bytes — and, when the archive moves (m.rewrite: a widen or a
// group promotion re-encodes every stored shard), a PRE-PASS re-encodes
// each stored shard once to learn the identity of the bytes the write
// then re-encodes again (the owner-accepted double read, U73 #720). A
// stale stored shard is recounted from the bytes the archive will store.
func (s *Service) addDistinct(ctx context.Context, a *archiveFile, stored *archiveShards, m *shardMerge, base string) (*encx.ArchiveDistinct, error) {
	prior := readPriorDistinct(a.arch)
	pass := s.newDistinctPass(m.canonical)
	refs := keptShardRefs(a, stored.names)
	var fresh map[string][]encx.DistinctCount
	if m.rewrite == nil {
		var err error
		if fresh, err = pass.recountKept(ctx, a, refs, prior, false); err != nil {
			return nil, err
		}
	} else {
		fresh = map[string][]encx.DistinctCount{}
		for i := range refs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			payload, err := stored.loadShard(i)
			if err != nil {
				return nil, err
			}
			if payload, err = m.rewrite(refs[i].Name, payload); err != nil {
				return nil, err
			}
			refs[i].Stored = encx.PayloadIdentity(payload)
			if prior.Stale(refs[i].Name, refs[i].Current) {
				counts, err := pass.count(bytes.NewReader(payload), refs[i].Name)
				if err != nil {
					return nil, err
				}
				fresh[refs[i].Name] = counts
			}
		}
	}
	counts, err := pass.count(bytes.NewReader(m.incoming), base)
	if err != nil {
		return nil, err
	}
	fresh[base] = counts
	refs = append(refs, encx.KeptShard(base, encx.PayloadIdentity(m.incoming)))
	return encx.NextArchiveDistinct(m.canonical, refs, prior, fresh), nil
}

// appendDeadByteLimit is the share of the archive the dead bytes an
// append leaves may reach: past dead*appendDeadByteLimit > size (more
// than half the file dead), AddShard rewrites instead.
const appendDeadByteLimit = 2

// appendDeadBytesFallback measures the dead bytes the tail append of a
// shard of shardSize bytes (and a `_schema.pulse` of schemaSize) would
// leave — the dead bytes already there plus the live `_schema.pulse` and
// directory tail the append supersedes — against the archive's size
// after that append. Over half: the mandatory
// PULSE_SHARD_DEAD_BYTES_RECLAIMED warning the rewrite reports; at or
// under: nil, append.
func appendDeadBytesFallback(a *archiveFile, dir *encx.ArchiveDirectory, archivePath, base string, shardSize, schemaSize int) (*encx.CohesionWarning, error) {
	sp, err := encx.MeasureArchiveSpace(a.ra, dir)
	if err != nil {
		return nil, err
	}
	dead := sp.DeadAfterAppend()
	size := dir.Size + encx.AppendLength(dir, encx.ReservedSchemaName,
		[]string{base, encx.ReservedSchemaName}, []uint64{uint64(shardSize), uint64(schemaSize)})
	if dead*appendDeadByteLimit <= size {
		return nil, nil
	}
	return &encx.CohesionWarning{
		Code: string(errors.PULSE_SHARD_DEAD_BYTES_RECLAIMED),
		Message: fmt.Sprintf("shard add rewrote %s instead of appending %s in place: the append would have left %d of %d bytes dead (over half); the rewrite reclaimed them",
			archivePath, base, dead, size),
		Details: map[string]any{
			"archive":            archivePath,
			"shard":              base,
			"dead_bytes":         dead,
			"archive_size":       size,
			"dead_schema_copies": sp.DeadSchemaCopies + 1,
			"threshold":          0.5,
		},
	}, nil
}

// allEntriesStored reports whether every entry of the archive is
// store-only (Method 0).
func allEntriesStored(a *archiveFile) bool {
	for _, e := range a.arch.Entries() {
		if !a.arch.IsStored(e.Name) {
			return false
		}
	}
	return true
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
// Every archive point-lookup index covering the shard is kept in step:
// the updated index manifest is staged before the archive is touched,
// then — after the archive rename — published and the removed shard's
// sidecars deleted, with the mandatory PULSE_SHARD_INDEXES_UPDATED
// warning. No other sidecar is touched (the kept shards' bytes, sizes
// and CRC32s are copied raw, so their sidecars stay valid).
//
// Streams like AddShard: the kept shards are copied raw into a staged
// archive, the aggregate is summed header-only, and one rename
// publishes it — O(one copy buffer) memory, the original byte-identical
// until the rename.
func (s *Service) RemoveShard(ctx context.Context, archivePath, shardBasename string) (*RemoveShardResult, error) {
	fsys := s.fs.Fs()
	if shardBasename == encx.ReservedSchemaName {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_RESERVED_NAME,
			"cannot remove the reserved canonical schema entry",
			map[string]any{"basename": shardBasename})
	}

	a, err := openArchiveFile(fsys, archivePath)
	if err != nil {
		return nil, err
	}
	defer a.Close()

	canonicalDoc, err := readSchemaDocEntry(a.arch)
	if err != nil {
		return nil, err
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
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_MISSING,
			fmt.Sprintf("shard %q not present in archive", shardBasename),
			map[string]any{"entry": shardBasename})
	}

	// Index upkeep, staged before the archive is touched: the removed
	// shard's record leaves every index manifest entry that covers it.
	// An unreadable manifest aborts the remove.
	result := &RemoveShardResult{Archive: archivePath, Removed: shardBasename,
		ShardCount: kept.shardCount(), Warnings: []encx.CohesionWarning{}}
	manifest, indexEntries, err := readArchiveIndexes(fsys, archivePath)
	if err != nil {
		return nil, err
	}
	var upkeep *indexUpkeep
	if manifest != nil {
		if upkeep, err = stageRemoveUpkeep(fsys, archivePath, shardBasename, manifest, indexEntries); err != nil {
			return nil, err
		}
		defer upkeep.abort()
	}
	// The kept shards' bytes are copied raw: their distinct counts carry,
	// the removed shard's entry drops with it — except a kept shard whose
	// entry its central-directory identity no longer vouches for (U73
	// #720), which is recounted off its stored bytes.
	prior := readPriorDistinct(a.arch)
	refs := keptShardRefs(a, kept.names)
	pass := s.newDistinctPass(canonicalDoc.Schema)
	fresh, err := pass.recountKept(ctx, a, refs, prior, false)
	if err != nil {
		return nil, err
	}
	dist := encx.NextArchiveDistinct(canonicalDoc.Schema, refs, prior, fresh)
	if err := rewriteKeptShards(fsys, a, archivePath, canonicalDoc.Schema, kept, dist); err != nil {
		return nil, err
	}
	if upkeep != nil {
		result.Warnings = append(result.Warnings, upkeep.publish())
	}
	return result, nil
}

// RemoveShardResult is what RemoveShard reports. Like AddShardResult it
// is a result rather than a bare error so the mandatory
// PULSE_SHARD_INDEXES_UPDATED warning (the removed shard's point-lookup
// sidecars deleted and the index manifest pruned) has a channel.
type RemoveShardResult struct {
	// Archive is the archive path that was rewritten.
	Archive string `json:"archive"`
	// Removed is the basename of the shard that was removed.
	Removed string `json:"removed"`
	// ShardCount is the archive's shard count after the remove.
	ShardCount int `json:"shard_count"`
	// Warnings carry every non-fatal diagnostic the remove produced:
	// PULSE_SHARD_INDEXES_UPDATED when an archive index covered the
	// shard (or PULSE_INDEX_STALE when publishing that upkeep failed
	// after the archive was rewritten). Shaped like the descriptor
	// envelope's warning entries.
	Warnings []encx.CohesionWarning `json:"warnings"`
}

// rewriteKeptShards publishes an archive holding canonical and the kept
// stored shards, copied raw, under a refreshed `_schema.pulse` whose
// aggregate is summed header-only. RemoveShard and CompactShardArchive
// share it. It closes a before the rename. Raw copies keep every stored
// shard's bytes, size and CRC32, so the archive's point-lookup sidecars
// stay valid for every kept shard.
func rewriteKeptShards(fsys afero.Fs, a *archiveFile, archivePath string, canonical *encoding.Schema, kept *archiveShards, dist *encx.ArchiveDistinct) error {
	aggregate, err := sumStoredRecords(kept)
	if err != nil {
		return err
	}
	w, err := newArchiveWriter(fsys, archivePath)
	if err != nil {
		return err
	}
	defer w.abort()
	if err := w.writeSchemaDoc(canonical, aggregate, kept.shardCount(), dist); err != nil {
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
//
// dist is the DIST trailer (per-shard distinct counts, U73 #500) every
// Pulse archive writer records; this is the one place a `_schema.pulse`
// is serialised, so every writer path carries one.
func buildSchemaDocPayload(schema *encoding.Schema, agg uint64, shardCount int, dist *encx.ArchiveDistinct, archivePath string) ([]byte, error) {
	if err := checkArchiveShardCount(shardCount, archivePath); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := encx.WriteSchemaDocDistinct(&buf, schema, agg, uint16(shardCount), dist); err != nil {
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
