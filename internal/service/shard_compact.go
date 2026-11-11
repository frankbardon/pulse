package service

import (
	"context"

	encx "github.com/frankbardon/pulse/internal/encoding"
)

// CompactShardArchive rewrites the archive at archivePath to eliminate
// orphaned bytes from prior in-place mutations and refresh the canonical
// schema-doc metadata (aggregate_record_count + shard_count). The
// rewrite copies every shard payload (excluding the reserved
// `_schema.pulse` entry) in central-directory order, re-emits the
// canonical schema with the recomputed metadata, and streams the result
// into a staged archive published by one rename (archiveWriter): each
// shard is copied raw (zip CreateRaw from a SectionReader), so memory
// is one copy buffer and a Pulse-written archive whose metadata is
// already current re-emits byte-identically.
//
// Dead bytes: an in-place `shard add` (shard_append.go) leaves every
// superseded `_schema.pulse` copy and old central directory behind;
// Compact is the explicit reclaim path (the add's own dead-byte fallback
// is the automatic one), so its result has zero dead bytes
// (VerifyResult.DeadBytes). It also refreshes canonical metadata that
// drifted when the archive was edited outside Pulse.
//
// Errors:
//   - PULSE_ARCHIVE_MAGIC_INVALID / PULSE_ARCHIVE_CORRUPT — surfaced
//     when the source archive cannot be opened.
//   - PULSE_SHARD_HEADER_INVALID — surfaced when a shard payload's
//     header cannot be parsed during record-count recomputation.
//   - SERVICE_RESOURCE — I/O failure on read, temp-write, or rename.
func (s *Service) CompactShardArchive(ctx context.Context, archivePath string) error {
	fsys := s.fs.Fs()

	a, err := openArchiveFile(fsys, archivePath)
	if err != nil {
		return err
	}
	defer a.Close()

	// Read canonical schema. Compact never grows the schema — it only
	// updates the metadata extension (aggregate + count) from the live
	// shards.
	canonicalDoc, err := readSchemaDocEntry(a.arch)
	if err != nil {
		return err
	}

	// Every real shard in central-directory order; the reserved schema
	// entry is excluded and re-emitted refreshed. The aggregate is the
	// shards' own headers summed (PeekShardRecordCount) — the
	// refresh-canonical-metadata step that justifies Compact's
	// existence — and each shard is copied raw, never decoded.
	kept := &archiveShards{a: a}
	for _, e := range a.arch.Entries() {
		if e.Name == encx.ReservedSchemaName {
			continue
		}
		kept.names = append(kept.names, e.Name)
	}
	// Distinct counts (U73 #500): every shard the prior trailer knows
	// CARRIES its entry (its bytes are copied raw, unchanged), and every
	// shard with an absent entry — an archive written before the DIST
	// trailer, a shard an append carried from one, a field that became
	// eligible — is counted now, off its raw stored bytes: compact is the
	// BACKFILL.
	// Opted out, an uncovered shard is recorded opted out instead.
	prior := readPriorDistinct(a.arch)
	pass := s.newDistinctPass(canonicalDoc.Schema)
	fresh := map[string][]encx.DistinctCount{}
	for _, name := range kept.names {
		if prior.Known(name, pass.fields) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		counts, err := pass.entry(a, name)
		if err != nil {
			return err
		}
		fresh[name] = counts
	}
	dist := encx.NextArchiveDistinct(canonicalDoc.Schema, kept.names, prior, fresh)
	return rewriteKeptShards(fsys, a, archivePath, canonicalDoc.Schema, kept, dist)
}
