package service

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/fs"
)

// heldFs emulates Windows' sharing rule over any afero.Fs, on every OS:
// a Rename onto or of a path, or a Remove of it, fails while a handle
// to that path is still open (Go opens files without FILE_SHARE_DELETE,
// and a mapped view pins a file the same way). It also reports which
// handles are still open, so a test can prove an operation leaked none.
type heldFs struct {
	afero.Fs
	mu   sync.Mutex
	open map[string]int
}

var errSharingViolation = stderrors.New("the process cannot access the file because it is being used by another process")

func newHeldFs(base afero.Fs) *heldFs { return &heldFs{Fs: base, open: map[string]int{}} }

func (h *heldFs) track(name string, f afero.File, err error) (afero.File, error) {
	if err != nil {
		return f, err
	}
	key := filepath.Clean(name)
	h.mu.Lock()
	h.open[key]++
	h.mu.Unlock()
	return &heldFile{File: f, h: h, key: key}, nil
}

func (h *heldFs) Create(name string) (afero.File, error) {
	f, err := h.Fs.Create(name)
	return h.track(name, f, err)
}

func (h *heldFs) Open(name string) (afero.File, error) {
	f, err := h.Fs.Open(name)
	return h.track(name, f, err)
}

func (h *heldFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := h.Fs.OpenFile(name, flag, perm)
	return h.track(name, f, err)
}

func (h *heldFs) held(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.open[filepath.Clean(name)] > 0
}

func (h *heldFs) Rename(oldname, newname string) error {
	if h.held(oldname) || h.held(newname) {
		return &os.LinkError{Op: "rename", Old: oldname, New: newname, Err: errSharingViolation}
	}
	return h.Fs.Rename(oldname, newname)
}

func (h *heldFs) Remove(name string) error {
	if h.held(name) {
		return &os.PathError{Op: "remove", Path: name, Err: errSharingViolation}
	}
	return h.Fs.Remove(name)
}

// leaked lists the paths that still have an open handle.
func (h *heldFs) leaked() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for k, n := range h.open {
		if n > 0 {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

type heldFile struct {
	afero.File
	h    *heldFs
	key  string
	once sync.Once
}

func (f *heldFile) Close() error {
	f.once.Do(func() {
		f.h.mu.Lock()
		f.h.open[f.key]--
		f.h.mu.Unlock()
	})
	return f.File.Close()
}

// TestHeldHandle_InPlaceRewritesReleaseTheirSource: every rewrite that
// may replace the file it reads — an in-place filter-to-file over a
// single file, an archive and an anchored shard, and the shard admin
// rewrites — closes its own source before the rename, so it succeeds
// under Windows' sharing rule (heldFs), and returns with no handle left
// open. Pulse's mmap views are released the same way (the iterator's
// Close and the parallel decode cleanup run before any publish).
//
// Falsified by deleting `plan.release = release` in filterToFileRun
// (the single-file arm then renames over its own open source), or the
// archive arm's `plan.release = a.Close`.
func TestHeldHandle_InPlaceRewritesReleaseTheirSource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	setup := func(t *testing.T) (*Service, *heldFs) {
		t.Helper()
		h := newHeldFs(afero.NewMemMapFs())
		for _, name := range []string{"c.pulse", "d.pulse", "e.pulse"} {
			if _, err := writeBenchPulseFile(h, name, shape1Num, 32); err != nil {
				t.Fatal(err)
			}
		}
		cfg, err := fs.New(fs.WithFs(h))
		if err != nil {
			t.Fatal(err)
		}
		svc := New(cfg)
		return svc, h
	}
	archive := func(t *testing.T, svc *Service) {
		t.Helper()
		if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"c.pulse", "d.pulse"}); err != nil {
			t.Fatalf("CreateShardArchive: %v", err)
		}
	}
	cases := []struct {
		name string
		run  func(t *testing.T, svc *Service) error
	}{
		{"filter_single_in_place", func(t *testing.T, svc *Service) error {
			_, err := svc.FilterToFile(ctx, "c.pulse", "c.pulse", "v >= 0")
			return err
		}},
		{"filter_archive_in_place", func(t *testing.T, svc *Service) error {
			archive(t, svc)
			_, err := svc.FilterToFile(ctx, "arch.pulse", "arch.pulse", "v >= 0")
			return err
		}},
		{"filter_anchored_over_its_archive", func(t *testing.T, svc *Service) error {
			archive(t, svc)
			_, err := svc.FilterToFile(ctx, "arch.pulse#c.pulse", "arch.pulse", "v >= 0")
			return err
		}},
		{"shard_add_remove_compact", func(t *testing.T, svc *Service) error {
			archive(t, svc)
			if _, err := svc.AddShard(ctx, "arch.pulse", "e.pulse"); err != nil {
				return err
			}
			if _, err := svc.RemoveShard(ctx, "arch.pulse", "c.pulse"); err != nil {
				return err
			}
			return svc.CompactShardArchive(ctx, "arch.pulse")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, h := setup(t)
			if err := c.run(t, svc); err != nil {
				t.Fatalf("rewrite under the sharing rule: %v", err)
			}
			if l := h.leaked(); len(l) > 0 {
				t.Errorf("handles still open after the rewrite: %v", l)
			}
		})
	}
}

// TestHeldHandle_FsRefusesAHeldRename pins the emulation itself: without
// it the suite above would pass vacuously.
func TestHeldHandle_FsRefusesAHeldRename(t *testing.T) {
	t.Parallel()

	h := newHeldFs(afero.NewMemMapFs())
	if err := afero.WriteFile(h, "a", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(h, "b", []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := h.Open("a")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Rename("b", "a"); !stderrors.Is(err, errSharingViolation) {
		t.Errorf("rename over a held file = %v, want the sharing violation", err)
	}
	if err := h.Remove("a"); !stderrors.Is(err, errSharingViolation) {
		t.Errorf("remove of a held file = %v, want the sharing violation", err)
	}
	_ = f.Close()
	if err := h.Rename("b", "a"); err != nil {
		t.Errorf("rename after close: %v", err)
	}
	if l := h.leaked(); len(l) > 0 {
		t.Errorf("leaked %v", l)
	}
}
