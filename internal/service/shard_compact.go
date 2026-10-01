package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// CompactShardArchive rewrites the archive at archivePath to eliminate
// orphaned bytes from prior in-place mutations and refresh the canonical
// schema-doc metadata (aggregate_record_count + shard_count). The
// rewrite reads every shard payload (excluding the reserved
// `_schema.pulse` entry) in central-directory order, re-emits the
// canonical schema with the recomputed metadata, and writes the result
// via the standard atomic temp+rename pattern (§7.1).
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

	archiveBytes, err := afero.ReadFile(fsys, archivePath)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("CompactShardArchive: reading archive %s", archivePath))
	}
	arch, err := encoding.OpenArchive(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return err
	}

	// Read canonical schema. Compact never grows the schema — it only
	// updates the metadata extension (aggregate + count) from the live
	// shards.
	canonicalDoc, err := readSchemaDocEntry(arch)
	if err != nil {
		return err
	}

	// Enumerate every real shard in central-directory order, draining
	// its bytes for re-emission. Reserved schema entry is excluded.
	type entry struct {
		name    string
		payload []byte
	}
	kept := make([]entry, 0, len(arch.Entries()))
	for _, e := range arch.Entries() {
		if e.Name == encoding.ReservedSchemaName {
			continue
		}
		payload, perr := readEntryBytes(arch, e.Name)
		if perr != nil {
			return perr
		}
		kept = append(kept, entry{name: e.Name, payload: payload})
	}

	// Recompute aggregate record count from the live shards' payloads
	// against the canonical schema. This is the refresh-canonical-
	// metadata step that justifies Compact's existence in v1.
	var aggregate uint64
	for _, e := range kept {
		n, perr := recordCountFromBytes(e.payload, canonicalDoc.Schema)
		if perr != nil {
			return perr
		}
		aggregate += uint64(n)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	schemaPayload, err := buildSchemaDocPayload(canonicalDoc.Schema, aggregate, uint16(len(kept)))
	if err != nil {
		return err
	}
	if err := writeArchiveEntry(zw, encoding.ReservedSchemaName, schemaPayload); err != nil {
		return err
	}
	for _, e := range kept {
		if err := writeArchiveEntry(zw, e.name, e.payload); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			"CompactShardArchive: finalising zip writer")
	}

	return atomicWriteCompact(fsys, archivePath, buf.Bytes())
}

// atomicWriteCompact is a thin pass-through to the shared atomicWrite
// helper. Kept as a named wrapper so future Compact-specific telemetry
// (rename-fsync, durability hooks) lands cleanly without surgery on
// the broader admin path.
func atomicWriteCompact(fsys afero.Fs, dst string, data []byte) error {
	return atomicWrite(fsys, dst, data)
}
