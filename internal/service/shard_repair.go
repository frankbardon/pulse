package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// ShardRepairResult is RepairShardArchive's outcome. Repaired is false
// when the archive carried no append journal (nothing to roll back);
// otherwise the archive was truncated from SizeBefore to ArchiveSize —
// its bytes before the interrupted append — and the journal removed.
// Shard names the shard whose append was rolled back, when journalled.
// Warnings carries ONE mandatory PULSE_SHARD_APPEND_ROLLED_BACK when the
// repair truncated bytes (it rewrote the archive) and is empty, never
// nil, otherwise.
type ShardRepairResult struct {
	Archive     string                 `json:"archive"`
	Repaired    bool                   `json:"repaired"`
	Shard       string                 `json:"shard,omitempty"`
	SizeBefore  int64                  `json:"size_before,omitempty"`
	ArchiveSize int64                  `json:"archive_size,omitempty"`
	Warnings    []encx.CohesionWarning `json:"warnings"`
}

// RepairShardArchive rolls back an interrupted in-place `shard add`
// (U73 E2-S2): with an append journal (`<archive>.append`) beside the
// archive it checks that the archive is at least the journalled length,
// that the journalled tail still carries its CRC-32 and that the
// journalled prefix parses as an archive, then truncates the archive to
// that length, syncs it and removes the journal. Any disagreement is
// PULSE_SHARD_REPAIR_REFUSED with nothing truncated and the journal
// kept — it never truncates blindly. Without a journal it changes
// nothing and reports Repaired false. It is the ONLY path that clears a
// journal: every open refuses a journalled archive. A repair that
// truncates reports the mandatory PULSE_SHARD_APPEND_ROLLED_BACK
// warning (U73 E5-S1): like every unasked archive rewrite it is never
// silent.
func (s *Service) RepairShardArchive(ctx context.Context, archivePath string) (*ShardRepairResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fsys := s.fs.Fs()
	result := &ShardRepairResult{Archive: archivePath, Warnings: []encx.CohesionWarning{}}
	j, err := encx.ReadAppendJournal(fsys, archivePath)
	if err != nil {
		return nil, err
	}
	if j == nil {
		// A stage left by a crash while the marker was being written
		// means no archive byte was appended.
		_ = fsys.Remove(encx.AppendJournalStagePath(archivePath))
		return result, nil
	}

	f, err := fsys.OpenFile(archivePath, os.O_RDWR, 0)
	if err != nil {
		if stderrors.Is(err, fs.ErrNotExist) {
			return nil, encx.RepairRefused(archivePath, "archive_shorter_than_journal", "archive does not exist", nil)
		}
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("repairing shard archive %s", archivePath))
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()
	st, err := f.Stat()
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("sizing shard archive %s", archivePath))
	}
	size := st.Size()
	extra := map[string]any{"archive_size": size, "journal_size": j.ArchiveSize}
	if size < j.ArchiveSize {
		return nil, encx.RepairRefused(archivePath, "archive_shorter_than_journal",
			fmt.Sprintf("archive is %d bytes, the journal recorded %d", size, j.ArchiveSize), extra)
	}
	ok, err := j.TailMatches(f)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, encx.RepairRefused(archivePath, "tail_checksum_mismatch",
			"the bytes at the journalled tail are not the ones the append left behind", extra)
	}
	if _, err := encx.ReadArchiveDirectory(f, j.ArchiveSize); err != nil && !encx.IsNotAppendable(err) {
		return nil, encx.RepairRefused(archivePath, "tail_not_an_archive", err.Error(), extra)
	}

	if size != j.ArchiveSize {
		if err := f.Truncate(j.ArchiveSize); err != nil {
			return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
				fmt.Sprintf("truncating shard archive %s", archivePath))
		}
	}
	if err := f.Sync(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("syncing shard archive %s", archivePath))
	}
	closed = true
	if err := f.Close(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("closing shard archive %s", archivePath))
	}
	if err := fsys.Remove(encx.AppendJournalPath(archivePath)); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("removing the append journal of %s", archivePath))
	}
	removeAppendJournal(fsys, archivePath)
	result.Repaired = true
	result.Shard = j.Shard
	result.SizeBefore = size
	result.ArchiveSize = j.ArchiveSize
	if size != j.ArchiveSize {
		result.Warnings = append(result.Warnings, appendRolledBackWarning(archivePath, j.Shard, size, j.ArchiveSize))
	}
	return result, nil
}

// appendRolledBackWarning is the mandatory warning of a repair that
// truncated an interrupted append's tail: the archive was rewritten (cut
// from sizeBefore back to archiveSize) and the shard the append carried
// is gone from it.
func appendRolledBackWarning(archivePath, shard string, sizeBefore, archiveSize int64) encx.CohesionWarning {
	return encx.CohesionWarning{
		Code: string(errors.PULSE_SHARD_APPEND_ROLLED_BACK),
		Message: fmt.Sprintf("shard verify --repair truncated %s from %d to %d bytes, rolling back the interrupted append of %s; re-run that shard add",
			archivePath, sizeBefore, archiveSize, shard),
		Details: map[string]any{
			"archive":         archivePath,
			"journal":         encx.AppendJournalPath(archivePath),
			"shard":           shard,
			"size_before":     sizeBefore,
			"archive_size":    archiveSize,
			"truncated_bytes": sizeBefore - archiveSize,
		},
	}
}
