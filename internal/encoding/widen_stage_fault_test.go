package encoding_test

import (
	"bytes"
	stderrors "errors"
	"os"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	pfs "github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

var errStage = stderrors.New("injected filesystem failure")

// stageFs fails exactly one filesystem step of an atomic rewrite.
type stageFs struct {
	afero.Fs
	fail string // create | flush | sync | close | srcclose | rename
}

type stageFile struct {
	afero.File
	fail string
}

func (f *stageFile) Write(p []byte) (int, error) {
	if f.fail == "flush" {
		return 0, errStage
	}
	return f.File.Write(p)
}

func (f *stageFile) Sync() error {
	if f.fail == "sync" {
		return errStage
	}
	return f.File.Sync()
}

func (f *stageFile) Close() error {
	err := f.File.Close()
	if f.fail == "close" || f.fail == "srcclose" {
		return errStage
	}
	return err
}

func (fs *stageFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if flag&os.O_CREATE != 0 && fs.fail == "create" {
		return nil, errStage
	}
	f, err := fs.Fs.OpenFile(name, flag, perm)
	if err != nil || flag&os.O_CREATE == 0 {
		return f, err
	}
	fail := fs.fail
	if fail == "srcclose" {
		fail = ""
	}
	return &stageFile{File: f, fail: fail}, nil
}

func (fs *stageFs) Open(name string) (afero.File, error) {
	f, err := fs.Fs.Open(name)
	if err != nil || fs.fail != "srcclose" {
		return f, err
	}
	return &stageFile{File: f, fail: "srcclose"}, nil
}

func (fs *stageFs) Rename(oldname, newname string) error {
	if fs.fail == "rename" {
		return errStage
	}
	return fs.Fs.Rename(oldname, newname)
}

// TestWidenSetFieldFile_EveryStageFailureIsAtomic: whichever step of the
// temp-write / flush / fsync / close / rename sequence fails, the widen
// reports ENCODING_IO carrying the cause, the original cohort is byte
// identical, and no temp file is left beside it.
func TestWidenSetFieldFile_EveryStageFailureIsAtomic(t *testing.T) {
	t.Parallel()

	rows := []widenRow{{id: 1, mask: widenMaskOf(0, 63), score: 1.5}, {id: 2, mask: widenMaskOf(7), score: 2, nullScor: true}}
	src := buildWidenCohort(t, widenSchema(t, encoding.FieldTypeSetU64, true, 64), rows)
	const path = "/data/cohort.pulse"
	for _, stage := range []string{"create", "flush", "sync", "close", "srcclose", "rename"} {
		base := pfs.NewMemMap().Fs()
		if err := afero.WriteFile(base, path, src, 0o644); err != nil {
			t.Fatal(err)
		}
		fsys := &stageFs{Fs: base, fail: stage}
		_, err := encx.WidenSetFieldFile(fsys, path, "picks", encoding.FieldTypeSetU128)
		if !errors.HasCode(err, errors.ENCODING_IO) || !stderrors.Is(err, errStage) {
			t.Errorf("%s: err = %v, want ENCODING_IO wrapping the fault", stage, err)
			continue
		}
		got, _ := afero.ReadFile(base, path)
		if !bytes.Equal(got, src) {
			t.Errorf("%s: the failed widen modified the cohort", stage)
		}
		if names := widenDirEntries(t, base, "/data"); len(names) != 1 || names[0] != "cohort.pulse" {
			t.Errorf("%s: failed widen left %v behind", stage, names)
		}
	}

	// A control run through the same wrapper with no failure succeeds.
	base := pfs.NewMemMap().Fs()
	if err := afero.WriteFile(base, path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if rep, err := encx.WidenSetFieldFile(&stageFs{Fs: base}, path, "picks", encoding.FieldTypeSetU128); err != nil || rep.Records != 2 {
		t.Fatalf("control widen = (%+v, %v)", rep, err)
	}
}

// TestWidenSetFieldBytes_SourceRefusals: a source that is not a cohort,
// names no such field, or ends inside its schema is refused with
// ENCODING_INVALID before any output exists.
func TestWidenSetFieldBytes_SourceRefusals(t *testing.T) {
	t.Parallel()

	src := buildWidenCohort(t, widenSchema(t, encoding.FieldTypeSetU8, false, 8), []widenRow{{id: 1, mask: widenMaskOf(3), score: 1}})
	cases := map[string]struct {
		in    []byte
		field string
	}{
		"not a cohort":       {[]byte("definitely not a pulse cohort"), "picks"},
		"schema cut short":   {src[:encoding.HeaderSize+3], "picks"},
		"field not declared": {src, "nope"},
	}
	for name, tc := range cases {
		out, rep, err := encx.WidenSetFieldBytes(tc.in, tc.field, encoding.FieldTypeSetU16)
		if err == nil || out != nil || rep != nil {
			t.Errorf("%s: = (%d bytes, %+v, %v), want a refusal", name, len(out), rep, err)
			continue
		}
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) {
			t.Errorf("%s: err = %v, want a coded error", name, err)
		}
	}
}
