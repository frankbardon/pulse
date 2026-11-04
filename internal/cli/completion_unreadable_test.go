package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// deniedFs refuses to open anything under dir — as a mode-000
// directory does on Unix: Stat still answers, every Open / OpenFile
// (and so every ReadDir) fails with a permission error. Injected, so the
// scenario runs identically on every OS (Windows ignores Unix mode bits,
// so os.Chmod(dir, 0) cannot produce it there).
type deniedFs struct {
	afero.Fs
	dir string
}

func (d deniedFs) denied(name string) bool {
	name, dir := filepath.Clean(name), filepath.Clean(d.dir)
	return name == dir || strings.HasPrefix(name, dir+string(filepath.Separator))
}

func (d deniedFs) Open(name string) (afero.File, error) {
	if d.denied(name) {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrPermission}
	}
	return d.Fs.Open(name)
}

func (d deniedFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if d.denied(name) {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrPermission}
	}
	return d.Fs.OpenFile(name, flag, perm)
}

// TestCompletion_UnreadableDataDirIsQuiet: a PULSE_DATA_DIR that exists
// but cannot be read completes nothing and writes nothing to stderr —
// the shell falls back to its own file completion. (The cmd/pulse
// failure-quiet table covers the other data-dir faults end to end.)
func TestCompletion_UnreadableDataDirIsQuiet(t *testing.T) {
	// serial: swaps the package-level cli.completionBaseFs test hook; t.Setenv mutates the process environment

	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "/data/locked/hidden.pulse", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := completionBaseFs
	t.Cleanup(func() { completionBaseFs = saved })
	t.Setenv("PULSE_DATA_DIR", "/data/locked")

	// Premise: readable, the same tree lists the cohort.
	completionBaseFs = func() afero.Fs { return mem }
	if got := completeLeaf(t, "cohort", "inspect"); len(got) != 1 || !strings.HasSuffix(got[0], "hidden.pulse") {
		t.Fatalf("readable data dir completes %v, want hidden.pulse (test premise)", got)
	}

	completionBaseFs = func() afero.Fs { return deniedFs{Fs: mem, dir: "/data/locked"} }
	if got := completeLeaf(t, "cohort", "inspect"); len(got) != 0 {
		t.Errorf("unreadable data dir completes %v, want nothing", got)
	}
}
