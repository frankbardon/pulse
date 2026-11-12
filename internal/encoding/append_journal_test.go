package encoding

import (
	"bytes"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// TestAppendJournal_RoundTripAndChecks: a journal written for an
// archive reads back equal, leaves no stage, matches the archive's tail
// and stops matching when a tail byte changes; a missing marker reads as
// nil and opens are refused only while a marker exists.
func TestAppendJournal_RoundTripAndChecks(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	archive := bytes.Repeat([]byte("0123456789"), 10000) // past the 64 KiB tail window
	if j, err := ReadAppendJournal(fsys, "a.pulse"); j != nil || err != nil {
		t.Fatalf("missing marker = %+v, %v; want nil, nil", j, err)
	}
	if err := RefuseIncompleteAppend(fsys, "a.pulse"); err != nil {
		t.Fatalf("no marker refused: %v", err)
	}
	j, err := NewAppendJournal(bytes.NewReader(archive), int64(len(archive)), "s.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if j.TailLength != int64(zipEOCDSearch) || j.ArchiveSize != int64(len(archive)) {
		t.Errorf("journal = %+v", j)
	}
	if err := WriteAppendJournal(fsys, "a.pulse", j); err != nil {
		t.Fatal(err)
	}
	if ok, _ := afero.Exists(fsys, AppendJournalStagePath("a.pulse")); ok {
		t.Error("the marker stage was left behind")
	}
	got, err := ReadAppendJournal(fsys, "a.pulse")
	if err != nil || *got != *j {
		t.Fatalf("read back %+v, %v; want %+v", got, err, j)
	}
	grown := append(append([]byte{}, archive...), 'x', 'y')
	if ok, err := got.TailMatches(bytes.NewReader(grown)); err != nil || !ok {
		t.Errorf("TailMatches over the grown archive = %v, %v", ok, err)
	}
	grown[len(archive)-1] ^= 1
	if ok, _ := got.TailMatches(bytes.NewReader(grown)); ok {
		t.Error("TailMatches ignored a changed tail byte")
	}
	var ce *errors.CodedError
	if err := RefuseIncompleteAppend(fsys, "a.pulse"); !stderrors.As(err, &ce) || ce.Code != errors.PULSE_SHARD_APPEND_INCOMPLETE {
		t.Fatalf("marker present: %v, want PULSE_SHARD_APPEND_INCOMPLETE", err)
	}

	for _, body := range []string{`{`, `{"journal_format_version":2,"archive_size":1,"tail_length":1}`, `{"journal_format_version":1,"archive_size":1,"tail_length":2}`} {
		if err := afero.WriteFile(fsys, AppendJournalPath("b.pulse"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := ReadAppendJournal(fsys, "b.pulse")
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_SHARD_REPAIR_REFUSED || ce.Details["reason"] != "journal_invalid" {
			t.Errorf("journal %s: %v, want PULSE_SHARD_REPAIR_REFUSED journal_invalid", body, err)
		}
	}
}
