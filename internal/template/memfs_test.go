package template_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/spf13/afero"
)

// tfs is the in-memory filesystem every store test in this package builds
// its template directories on. The store reads through the afero.Fs it is
// handed, so these tests never touch the host; each test takes its own
// directory from memDir, which keeps parallel tests apart on the one Fs.
var tfs afero.Fs = afero.NewMemMapFs()

// memDirSeq numbers memDir directories so every call is a fresh root.
var memDirSeq atomic.Uint64

// memDir creates and returns a fresh, empty directory on tfs — the
// in-memory counterpart of t.TempDir.
func memDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(string(filepath.Separator)+"templates", strconv.FormatUint(memDirSeq.Add(1), 10))
	if err := tfs.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	return dir
}

// denyReadFs is tfs with reads of chosen files refused as a permission
// fault, while Stat still succeeds — the shape of a file whose mode bits
// forbid reading. It makes the unreadable-file paths testable without
// depending on the host user honouring chmod.
type denyReadFs struct {
	afero.Fs
	mu     sync.Mutex
	denied map[string]bool
}

func newDenyReadFs() *denyReadFs {
	return &denyReadFs{Fs: tfs, denied: map[string]bool{}}
}

// deny makes every later open of path fail with fs.ErrPermission.
func (d *denyReadFs) deny(path string) {
	d.mu.Lock()
	d.denied[path] = true
	d.mu.Unlock()
}

func (d *denyReadFs) refused(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.denied[name]
}

func (d *denyReadFs) Open(name string) (afero.File, error) {
	if d.refused(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return d.Fs.Open(name)
}

func (d *denyReadFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if d.refused(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return d.Fs.OpenFile(name, flag, perm)
}
