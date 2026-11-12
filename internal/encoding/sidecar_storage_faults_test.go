package encoding

import (
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"hash/crc32"
	"io"
	"os"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// sidecar_storage_faults_test.go pins the storage-fault answers of the
// U73 sidecars — the append journal, the per-shard Bloom and the
// archive Bloom set: an unreadable or unwritable file is a coded I/O
// error (never "absent", never a half-written marker), and a Bloom
// whose CRC is intact but whose lengths lie is ENCODING_INVALID.

// faultFs fails Open / OpenFile with openErr and Rename with renameErr
// when set; Stat (and so afero.Exists) still sees the real files.
type faultFs struct {
	afero.Fs
	openErr, renameErr error
}

func (f faultFs) Open(name string) (afero.File, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.Fs.Open(name)
}

func (f faultFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return f.Fs.OpenFile(name, flag, perm)
}

func (f faultFs) Rename(oldname, newname string) error {
	if f.renameErr != nil {
		return f.renameErr
	}
	return f.Fs.Rename(oldname, newname)
}

// shortNilReaderAt returns fewer bytes than asked with a nil error — a
// reader the io.ReaderAt contract forbids but storage adapters produce.
type shortNilReaderAt struct{}

func (shortNilReaderAt) ReadAt(p []byte, _ int64) (int, error) { return len(p) / 2, nil }

// TestAppendJournal_StorageFaults: an unreadable archive tail cannot be
// journalled or checked (ENCODING_IO, even when the reader under-reads
// without an error); an unwritable stage or a failed rename is
// SERVICE_RESOURCE and leaves neither the stage nor a marker; an
// unreadable marker is SERVICE_RESOURCE, never "no marker".
func TestAppendJournal_StorageFaults(t *testing.T) {
	t.Parallel()

	archive := bytes.Repeat([]byte("abc"), 100)
	size := int64(len(archive))
	for name, r := range map[string]io.ReaderAt{
		"read error":            offsetFailReaderAt{bytes.NewReader(archive), 0},
		"short read, nil error": shortNilReaderAt{},
	} {
		if _, err := NewAppendJournal(r, size, "s.pulse"); !errors.HasCode(err, errors.ENCODING_IO) {
			t.Errorf("NewAppendJournal, %s: %v, want ENCODING_IO", name, err)
		}
	}
	j, err := NewAppendJournal(bytes.NewReader(archive), size, "s.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := j.TailMatches(bytes.NewReader(archive[:10])); ok || !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("TailMatches over a truncated archive = %v, %v; want false, ENCODING_IO", ok, err)
	}

	mem := afero.NewMemMapFs()
	for name, fsys := range map[string]afero.Fs{
		"stage unwritable": faultFs{Fs: mem, openErr: errInjected},
		"rename fails":     faultFs{Fs: mem, renameErr: errInjected},
	} {
		err := WriteAppendJournal(fsys, "a.pulse", j)
		if !errors.HasCode(err, errors.SERVICE_RESOURCE) || !stderrors.Is(err, errInjected) {
			t.Errorf("%s: %v, want SERVICE_RESOURCE wrapping the fault", name, err)
		}
		for _, p := range []string{AppendJournalStagePath("a.pulse"), AppendJournalPath("a.pulse")} {
			if ok, _ := afero.Exists(mem, p); ok {
				t.Errorf("%s: %s left behind", name, p)
			}
		}
	}

	if err := WriteAppendJournal(mem, "a.pulse", j); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadAppendJournal(faultFs{Fs: mem, openErr: errInjected}, "a.pulse"); got != nil || !errors.HasCode(err, errors.SERVICE_RESOURCE) {
		t.Errorf("unreadable marker = %+v, %v; want nil, SERVICE_RESOURCE", got, err)
	}
}

// reCRC recomputes a Bloom sidecar's trailing CRC-32 after an edit, so
// the decoder reaches the length checks behind it.
func reCRC(b []byte) []byte {
	binary.LittleEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[:len(b)-4]))
	return b
}

// TestShardBloom_LengthLiesBehindAValidCRC: a sidecar whose CRC matches
// but whose name length, hash count or word count disagrees with its
// bytes is ENCODING_INVALID (reason "length"), never a filter that
// reads past its words or answers with zero hash functions.
func TestShardBloom_LengthLiesBehindAValidCRC(t *testing.T) {
	t.Parallel()

	f := NewShardBloom("a.pulse", 1, 2, 8, 4)
	f.Add(bloomKey(9))
	good := MarshalShardBloom(f)
	nameAt := 26
	wordsAt := 28 + len("a.pulse")
	mut := func(edit func(b []byte)) []byte {
		b := append([]byte(nil), good...)
		edit(b)
		return reCRC(b)
	}
	for name, raw := range map[string][]byte{
		"name overruns":   mut(func(b []byte) { binary.LittleEndian.PutUint16(b[nameAt:], 0xffff) }),
		"zero hash count": mut(func(b []byte) { b[9] = 0 }),
		"word count":      mut(func(b []byte) { binary.LittleEndian.PutUint32(b[wordsAt:], binary.LittleEndian.Uint32(b[wordsAt:])+1) }),
		// No words at all, and the length says so: still no filter.
		"zero words": func() []byte {
			b := append([]byte(nil), good[:wordsAt]...)
			b = binary.LittleEndian.AppendUint32(b, 0)
			return reCRC(append(b, 0, 0, 0, 0))
		}(),
	} {
		_, err := UnmarshalShardBloom(raw)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID || ce.Details["reason"] != "length" {
			t.Errorf("%s: err = %v, want ENCODING_INVALID reason length", name, err)
		}
	}
	if _, err := UnmarshalShardBloom(reCRC(append([]byte(nil), good...))); err != nil {
		t.Fatalf("re-CRC of an unedited sidecar refused: %v", err)
	}
}

// TestBloomSidecars_StorageFaults: an unwritable or unreadable Bloom
// sidecar or Bloom set is ENCODING_IO — an unreadable file that exists
// is never reported as absent.
func TestBloomSidecars_StorageFaults(t *testing.T) {
	t.Parallel()

	mem := afero.NewMemMapFs()
	filters := bloomSetFixture()
	if err := WriteShardBloomFile(mem, "a.bloom", filters[0]); err != nil {
		t.Fatal(err)
	}
	if err := WriteArchiveBloomSetFile(mem, "x.blooms", filters); err != nil {
		t.Fatal(err)
	}
	broken := faultFs{Fs: mem, openErr: errInjected}

	if err := WriteShardBloomFile(broken, "b.bloom", filters[0]); !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("WriteShardBloomFile on an unwritable fs: %v, want ENCODING_IO", err)
	}
	if err := WriteArchiveBloomSetFile(broken, "y.blooms", filters); !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("WriteArchiveBloomSetFile on an unwritable fs: %v, want ENCODING_IO", err)
	}
	if f, present, err := ReadShardBloomFile(broken, "a.bloom"); f != nil || present || !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("unreadable Bloom = %v, %v, %v; want nil, false, ENCODING_IO", f, present, err)
	}
	if s, present, err := ReadArchiveBloomSetFile(broken, "x.blooms"); s != nil || present || !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("unreadable Bloom set = %v, %v, %v; want nil, false, ENCODING_IO", s, present, err)
	}
	// The same files read back through the healthy fs.
	if f, present, err := ReadShardBloomFile(mem, "a.bloom"); err != nil || !present || f.Shard != "a.pulse" {
		t.Errorf("healthy Bloom read = %v, %v, %v", f, present, err)
	}
}

// TestArchiveBloomSet_DuplicateAndTruncatedRecords: a second record for
// the same shard marks the set Damaged and keeps the first; a record
// count that promises more records than the bytes hold (no room for the
// next length prefix) marks it Damaged and keeps what was read.
func TestArchiveBloomSet_DuplicateAndTruncatedRecords(t *testing.T) {
	t.Parallel()

	filters := bloomSetFixture()
	dup := NewShardBloom("a.pulse", 999, 0x1, 8, 2)
	s, err := UnmarshalArchiveBloomSet(MarshalArchiveBloomSet([]*ShardBloom{filters[0], dup}))
	if err != nil || !s.Damaged {
		t.Fatalf("duplicate = %+v, %v; want damaged", s, err)
	}
	if got := s.Filters["a.pulse"]; got == nil || got.Size != 100 {
		t.Errorf("duplicate shard kept %+v, want the first record (size 100)", got)
	}

	raw := MarshalArchiveBloomSet(filters)
	binary.LittleEndian.PutUint32(raw[9:], 3) // three records promised, two present
	s, err = UnmarshalArchiveBloomSet(raw)
	if err != nil || !s.Damaged || len(s.Filters) != 2 {
		t.Errorf("over-promised count = %+v, %v; want damaged with both records", s, err)
	}
}
