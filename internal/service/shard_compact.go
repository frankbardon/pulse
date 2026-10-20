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
// v1 note: AddShard / RemoveShard already use temp+rename for the
// whole-archive rewrite, so v1 archives never accumulate orphan bytes
// from Pulse-issued mutations. Compact still has a job in v1: it
// refreshes the canonical _schema.pulse metadata that may have drifted
// (for example, if the archive was edited by a third-party zip tool
// outside Pulse) and is the explicit reclaim path for archives
// containing orphan bytes from any source. Future PRs may introduce
// true in-place AddShard atop lower-level zip primitives; Compact's
// contract remains identical — produce a clean archive byte-for-byte.
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
	return rewriteKeptShards(fsys, a, archivePath, canonicalDoc.Schema, kept)
}
