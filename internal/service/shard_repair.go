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
type ShardRepairResult struct {
	Archive     string `json:"archive"`
	Repaired    bool   `json:"repaired"`
	Shard       string `json:"shard,omitempty"`
	SizeBefore  int64  `json:"size_before,omitempty"`
	ArchiveSize int64  `json:"archive_size,omitempty"`
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
// journal: every open refuses a journalled archive.
func (s *Service) RepairShardArchive(ctx context.Context, archivePath string) (*ShardRepairResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fsys := s.fs.Fs()
	result := &ShardRepairResult{Archive: archivePath}
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
	return result, nil
}
