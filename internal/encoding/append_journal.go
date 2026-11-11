package encoding

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// append_journal.go is the crash story of an in-place `shard add` (U73
// #492): before the first byte is appended, a journal marker beside the
// archive records where the archive ended and a checksum of its old
// tail; the marker is removed once the append is durable. A marker that
// survives is an INCOMPLETE append — the archive may hold a torn tail
// whose old end-of-central-directory record a zip reader no longer
// finds, or finds while silently reading the pre-append view — so every
// path-based archive open refuses it (PULSE_SHARD_APPEND_INCOMPLETE)
// and only an explicit repair (`pulse shard verify --repair`) truncates
// the archive back to the journalled length.

// AppendJournalSuffix is appended to an archive's path to name its
// append journal marker: "cohort.pulse" → "cohort.pulse.append". The
// marker is not a `.pulse` file and not a `*.json` document, so cohort
// enumerations (`*.pulse`) and the table-directory loaders (`*.json`)
// never pick it up.
const AppendJournalSuffix = ".append"

// appendJournalTempSuffix names the marker's stage: the marker is
// written there, synced, then renamed into place, so a marker is
// either whole or absent.
const appendJournalTempSuffix = ".append.tmp"

// AppendJournalFormatVersion is the marker document's own version.
const AppendJournalFormatVersion = 1

// AppendJournalPath is the journal marker path for archivePath.
func AppendJournalPath(archivePath string) string { return archivePath + AppendJournalSuffix }

// AppendJournalStagePath is the marker's stage path for archivePath.
func AppendJournalStagePath(archivePath string) string {
	return archivePath + appendJournalTempSuffix
}

// AppendJournal is the marker's content: the archive's length before
// the append, and a CRC-32 of its last TailLength bytes — the window a
// zip reader searches for the end-of-central-directory record — so a
// repair truncates only an archive whose old tail is still the one the
// append left behind.
type AppendJournal struct {
	FormatVersion int    `json:"journal_format_version"`
	ArchiveSize   int64  `json:"archive_size"`
	TailLength    int64  `json:"tail_length"`
	TailCRC32     uint32 `json:"tail_crc32"`
	Shard         string `json:"shard,omitempty"`
}

// NewAppendJournal journals the archive r of size bytes before shard is
// appended to it.
func NewAppendJournal(r io.ReaderAt, size int64, shard string) (*AppendJournal, error) {
	n := min(size, int64(zipEOCDSearch))
	sum, err := tailCRC32(r, size, n)
	if err != nil {
		return nil, err
	}
	return &AppendJournal{
		FormatVersion: AppendJournalFormatVersion,
		ArchiveSize:   size,
		TailLength:    n,
		TailCRC32:     sum,
		Shard:         shard,
	}, nil
}

// tailCRC32 is the CRC-32 of the n bytes ending at size.
func tailCRC32(r io.ReaderAt, size, n int64) (uint32, error) {
	buf := make([]byte, n)
	if got, err := r.ReadAt(buf, size-n); int64(got) != n {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, errors.WrapCodedError(err, errors.ENCODING_IO, "reading the archive tail for the append journal")
	}
	return crc32.ChecksumIEEE(buf), nil
}

// TailMatches reports whether the n = j.TailLength bytes ending at
// j.ArchiveSize in r carry the journalled checksum.
func (j *AppendJournal) TailMatches(r io.ReaderAt) (bool, error) {
	sum, err := tailCRC32(r, j.ArchiveSize, j.TailLength)
	if err != nil {
		return false, err
	}
	return sum == j.TailCRC32, nil
}

// WriteAppendJournal writes j as archivePath's marker durably: stage,
// fsync, rename. The caller syncs the directory.
func WriteAppendJournal(fsys afero.Fs, archivePath string, j *AppendJournal) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_INTERNAL, "encoding the append journal")
	}
	stage := AppendJournalStagePath(archivePath)
	f, err := fsys.OpenFile(stage, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("writing the append journal of %s", archivePath))
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = fsys.Rename(stage, AppendJournalPath(archivePath))
	}
	if err != nil {
		_ = fsys.Remove(stage)
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("writing the append journal of %s", archivePath))
	}
	return nil
}

// ReadAppendJournal reads archivePath's marker. A missing marker is
// (nil, nil); one that does not decode is PULSE_SHARD_REPAIR_REFUSED
// (reason journal_invalid).
func ReadAppendJournal(fsys afero.Fs, archivePath string) (*AppendJournal, error) {
	raw, err := afero.ReadFile(fsys, AppendJournalPath(archivePath))
	if err != nil {
		if stderrors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("reading the append journal of %s", archivePath))
	}
	var j AppendJournal
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, RepairRefused(archivePath, "journal_invalid", err.Error(), nil)
	}
	if j.FormatVersion != AppendJournalFormatVersion || j.ArchiveSize < 0 || j.TailLength < 0 ||
		j.TailLength > j.ArchiveSize || j.TailLength > int64(zipEOCDSearch) {
		return nil, RepairRefused(archivePath, "journal_invalid",
			fmt.Sprintf("journal fields out of range (version %d, size %d, tail %d)", j.FormatVersion, j.ArchiveSize, j.TailLength), nil)
	}
	return &j, nil
}

// RefuseIncompleteAppend is the read-side check every path-based archive
// open makes: a journal marker beside archivePath means an append did
// not finish, and the archive is refused with
// PULSE_SHARD_APPEND_INCOMPLETE. Open never repairs.
func RefuseIncompleteAppend(fsys afero.Fs, archivePath string) error {
	marker := AppendJournalPath(archivePath)
	if _, err := fsys.Stat(marker); err != nil {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_APPEND_INCOMPLETE,
		fmt.Sprintf("shard archive %s has an interrupted `shard add` (journal %s); run `pulse shard verify --repair` to roll it back", archivePath, marker),
		map[string]any{"archive": archivePath, "journal": marker})
}

// RepairRefused is PULSE_SHARD_REPAIR_REFUSED: the journal does not
// describe the archive on disk, so nothing was truncated.
func RepairRefused(archivePath, reason, cause string, extra map[string]any) error {
	details := map[string]any{"archive": archivePath, "journal": AppendJournalPath(archivePath), "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	msg := fmt.Sprintf("refusing to repair %s (%s)", archivePath, reason)
	if cause != "" {
		details["cause"] = cause
		msg += ": " + cause
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_REPAIR_REFUSED, msg, details)
}
