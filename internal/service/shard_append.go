package service

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// shard_append.go is AddShard's in-place arm (U73 #492): an ordinary add
// — no archive-level set widen, no constant-group promotion, so every
// stored shard keeps its bytes — appends to the archive's TAIL instead
// of rewriting it:
//
//	[old entries][old directory + EOCD]           untouched
//	[new shard][new _schema.pulse]                 appended
//	[central directory][zip64 records][EOCD]       appended
//
// The new directory lists the refreshed `_schema.pulse` first, every
// stored shard's record VERBATIM (their offsets never move) in archive
// order, then the new shard — the entry order a rewrite writes — and
// drops the superseded `_schema.pulse`, so no reader ever sees two. The
// old directory, EOCD and `_schema.pulse` stay behind as dead bytes no
// directory references; CompactShardArchive reclaims them. Bytes
// written are the new shard + `_schema.pulse` + the directory (O(shards)),
// whatever the archive's size.
//
// Concurrency (owner decision): an append assumes no concurrent WRITER,
// as every archive mutation does, and takes no lock. A reader that
// opened the archive before the append keeps its old view — its parsed
// directory points only at bytes the append never touches — and reads
// every pre-append shard. A write-open the platform refuses (a
// read-only file or filesystem; on Windows a reader that opened the
// archive without write sharing) fails the add with
// PULSE_SHARD_APPEND_REFUSED, nothing written, the fixup naming the
// staged rewrite (`--rewrite`).
//
// Failure: any write, flush or fsync error truncates the archive back
// to its old length (its old EOCD is then the file's last record again)
// and returns the error. A CRASH mid-append is the append journal's
// (U73 E2-S2): before the first archive byte, `<archive>.append`
// records the old length and a CRC-32 of the old tail, durably; it is
// removed only once the appended bytes are synced. A surviving marker
// makes every path-based open refuse the archive
// (PULSE_SHARD_APPEND_INCOMPLETE) until RepairShardArchive truncates it
// back — the old EOCD may otherwise sit outside the tail a zip reader
// searches (unopenable), or inside it with a torn tail after it (read
// silently as the pre-append view).
//
// Index upkeep is still BUILT BEFORE PUBLISH: the new shard's sidecars
// and the updated manifest are staged from an in-memory twin archive
// holding the new shard exactly as the append will store it (same name,
// bytes and CRC32 — the shard's identity) before the archive is opened
// for writing, and renamed only after the append is durable.

// appendShard appends m.incoming as base to the archive a (whose tail
// dir describes) and refreshes `_schema.pulse`. a is closed before the
// archive is opened for writing.
func (s *Service) appendShard(fsys afero.Fs, archivePath string, a *archiveFile, dir *encx.ArchiveDirectory,
	m *shardMerge, base string, aggregate uint64, shardCount int,
	manifest *encx.IndexManifest, indexEntries []int, result *AddShardResult) (*AddShardResult, error) {
	schemaDoc, err := buildSchemaDocPayload(m.canonical, aggregate, shardCount, archivePath)
	if err != nil {
		return nil, err
	}

	var upkeep *indexUpkeep
	if manifest != nil {
		src, twin, err := appendUpkeepSource(schemaDoc, base, m.incoming)
		if err != nil {
			return nil, err
		}
		upkeep, err = stageAddUpkeep(fsys, src, archivePath, twin, m.canonical, base, manifest, indexEntries)
		if err != nil {
			return nil, err
		}
		defer upkeep.abort()
	}

	// The journal's tail checksum is read while the read handle is
	// open; nothing of the archive is read past this point.
	journal, err := encx.NewAppendJournal(a.ra, dir.Size, base)
	if err != nil {
		return nil, err
	}
	// The read handle goes before the write handle opens.
	if err := a.Close(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: closing archive %s", archivePath))
	}
	// An earlier crashed rewrite's orphan stage goes with this mutation,
	// as it would with a rewrite's.
	sweepStagedArchives(fsys, archivePath)

	if err := s.appendArchiveTail(fsys, archivePath, dir, journal, base, m.incoming, schemaDoc); err != nil {
		return nil, err
	}
	if upkeep != nil {
		result.Warnings = append(result.Warnings, upkeep.publish())
	}
	result.ShardCount = shardCount
	return result, nil
}

// appendUpkeepSource is the in-memory twin the upkeep reads: a
// two-entry archive (`_schema.pulse`, then the new shard) written by the
// same appender, so the shard's CRC32 and size are the ones the real
// append records. Memory is O(the new shard).
func appendUpkeepSource(schemaDoc []byte, base string, payload []byte) (afero.Fs, string, error) {
	const twin = "append-upkeep.pulse"
	mem := afero.NewMemMapFs()
	f, err := mem.Create(twin)
	if err != nil {
		return nil, "", errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "staging the index upkeep source")
	}
	defer f.Close()
	ap := encx.NewArchiveAppender(f, 0)
	rs, err := ap.WriteEntry(encx.ReservedSchemaName, schemaDoc)
	if err != nil {
		return nil, "", err
	}
	rn, err := ap.WriteEntry(base, payload)
	if err != nil {
		return nil, "", err
	}
	if _, err := ap.Finish([]encx.DirectoryRecord{rs, rn}); err != nil {
		return nil, "", err
	}
	return mem, twin, nil
}

// appendRefused is PULSE_SHARD_APPEND_REFUSED: nothing was written.
func appendRefused(archivePath, shard, reason string, cause error) error {
	details := map[string]any{"archive": archivePath, "shard": shard, "reason": reason}
	msg := fmt.Sprintf("shard add could not append %s to %s in place (%s)", shard, archivePath, reason)
	if cause != nil {
		details["cause"] = cause.Error()
		msg += ": " + cause.Error()
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_APPEND_REFUSED, msg, details)
}

// appendSeam observes a tail append (tests only): wrap wraps the
// archive writer BELOW the buffer, so every byte it passes on has
// reached the file; at runs at the named phases ("journalled": the
// marker is durable and no archive byte written; "synced": every
// appended byte is durable and the marker not yet removed). A crash
// test snapshots the files at either.
type appendSeam struct {
	wrap func(io.Writer) io.Writer
	at   func(phase string)
}

func (s *Service) appendPhase(phase string) {
	if s.appendSeam != nil && s.appendSeam.at != nil {
		s.appendSeam.at(phase)
	}
}

// appendArchiveTail opens the archive for writing, journals the append
// (`<archive>.append`, synced), appends the new shard, the refreshed
// `_schema.pulse` and the new directory at dir.Size, fsyncs, then
// removes the journal. Any failure after the write-open truncates the
// archive back to dir.Size before returning; the journal is removed
// only once that rollback is durable, so an archive the rollback could
// not restore stays refused until a repair.
func (s *Service) appendArchiveTail(fsys afero.Fs, archivePath string, dir *encx.ArchiveDirectory, journal *encx.AppendJournal,
	base string, payload, schemaDoc []byte) (err error) {
	f, err := fsys.OpenFile(archivePath, os.O_WRONLY, 0)
	if err != nil {
		return appendRefused(archivePath, base, "open_for_write", err)
	}
	closed, journalled := false, false
	defer func() {
		if closed {
			return
		}
		if err != nil && journalled {
			// Roll back: the old EOCD is the last record again. The
			// journal goes only when the rollback is durable.
			if f.Truncate(dir.Size) == nil && f.Sync() == nil {
				_ = f.Close()
				removeAppendJournal(fsys, archivePath)
				return
			}
		}
		_ = f.Close()
	}()

	st, err := f.Stat()
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: sizing archive %s", archivePath))
	}
	if st.Size() != dir.Size {
		// Not ours to truncate: someone else wrote it. Close untouched.
		closed = true
		_ = f.Close()
		return appendRefused(archivePath, base, "archive_changed",
			fmt.Errorf("archive is %d bytes, was %d when read", st.Size(), dir.Size))
	}
	// The journal is durable before the first archive byte: from here a
	// crash leaves a marker every open refuses.
	if err = encx.WriteAppendJournal(fsys, archivePath, journal); err != nil {
		return err
	}
	syncDir(fsys, filepath.Dir(archivePath))
	journalled = true
	s.appendPhase("journalled")
	if _, err = f.Seek(dir.Size, io.SeekStart); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: seeking to the tail of %s", archivePath))
	}

	var w io.Writer = f
	if s.appendSeam != nil && s.appendSeam.wrap != nil {
		w = s.appendSeam.wrap(w)
	}
	bw := bufio.NewWriterSize(w, shardCopyBuffer)
	ap := encx.NewArchiveAppender(bw, dir.Size)
	shardRec, err := ap.WriteEntry(base, payload)
	if err != nil {
		return err
	}
	schemaRec, err := ap.WriteEntry(encx.ReservedSchemaName, schemaDoc)
	if err != nil {
		return err
	}
	records := make([]encx.DirectoryRecord, 0, len(dir.Records)+1)
	records = append(records, schemaRec)
	for _, r := range dir.Records {
		if r.Name != encx.ReservedSchemaName {
			records = append(records, r)
		}
	}
	records = append(records, shardRec)
	if _, err = ap.Finish(records); err != nil {
		return err
	}
	if err = bw.Flush(); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: appending to %s", archivePath))
	}
	if err = f.Sync(); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: syncing %s", archivePath))
	}
	closed = true
	if err = f.Close(); err != nil {
		// The bytes are durable (fsync succeeded); report the close. The
		// journal stays: the next open refuses and a repair rolls back.
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: closing %s", archivePath))
	}
	s.appendPhase("synced")
	removeAppendJournal(fsys, archivePath)
	return nil
}

// removeAppendJournal removes the marker (and any stage of it) and
// syncs the directory, so the removal is as durable as the append.
func removeAppendJournal(fsys afero.Fs, archivePath string) {
	_ = fsys.Remove(encx.AppendJournalPath(archivePath))
	_ = fsys.Remove(encx.AppendJournalStagePath(archivePath))
	syncDir(fsys, filepath.Dir(archivePath))
}
