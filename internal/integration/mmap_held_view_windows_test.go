package integration_test

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	perr "github.com/frankbardon/pulse/errors"
)

// mmap_held_view_windows_test.go pins U77 E2-S1's external-reader
// contract on Windows: while ANOTHER reader (a second process, or an
// embedder's own mapping) still maps a cohort, every Pulse atomic
// rewrite of that cohort returns either success or a coded error —
// never a raw syscall error — and a refused rewrite leaves the cohort
// byte-identical with no stage, spool or temp file behind.

// winHoldView maps path read-only the way a foreign reader would —
// CreateFileW with share, CreateFileMappingW, MapViewOfFile — and
// returns the view's bytes plus its release.
func winHoldView(t *testing.T, path string, share uint32) ([]byte, func()) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	size := info.Size()
	section, err := windows.CreateFileMapping(h, nil, windows.PAGE_READONLY, 0, uint32(size), nil)
	if err != nil {
		_ = windows.CloseHandle(h)
		t.Fatalf("CreateFileMapping: %v", err)
	}
	addr, err := windows.MapViewOfFile(section, windows.FILE_MAP_READ, 0, 0, uintptr(size))
	if err != nil {
		_ = windows.CloseHandle(section)
		_ = windows.CloseHandle(h)
		t.Fatalf("MapViewOfFile: %v", err)
	}
	data := unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&addr))), int(size))
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		_ = windows.UnmapViewOfFile(addr)
		_ = windows.CloseHandle(section)
		_ = windows.CloseHandle(h)
	}
	t.Cleanup(release)
	return data, release
}

// TestMmapHeldView_RewriteIsSuccessOrCoded: for every single-file
// rewrite, with the target mapped by a foreign reader (read-only share,
// and the share mode Pulse's own views use), the rewrite either succeeds
// or fails with a *errors.CodedError; on failure the cohort's bytes are
// unchanged and the directory holds exactly what it held before.
func TestMmapHeldView_RewriteIsSuccessOrCoded(t *testing.T) {
	t.Parallel()

	shares := []struct {
		name  string
		share uint32
	}{
		{"foreign_read_only", windows.FILE_SHARE_READ},
		{"pulse_share_mode", windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE},
	}
	for _, sh := range shares {
		for _, c := range mmapRewriteCases() {
			if c.target == "" {
				continue // archives and sidecars are never mapped
			}
			t.Run(sh.name+"/"+c.name, func(t *testing.T) {
				p, dir := mmapNewDataDir(t)
				c.setup(t, p)
				target := filepath.Join(dir, c.target)
				before, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				listing := mmapDirListing(t, dir)
				view, release := winHoldView(t, target, sh.share)

				err = c.run(context.Background(), p)
				viewIntact := bytes.Equal(view, before)
				release()

				if err == nil {
					t.Logf("held view (%s) %s: rewrite succeeded; the view kept the old bytes = %v", sh.name, c.name, viewIntact)
					return
				}
				var ce *perr.CodedError
				if !stderrors.As(err, &ce) {
					t.Fatalf("rewrite under a held view returned an uncoded error: %v", err)
				}
				t.Logf("held view (%s) %s: refused with %s: %v", sh.name, c.name, ce.Code, err)
				after, rerr := os.ReadFile(target)
				if rerr != nil {
					t.Fatal(rerr)
				}
				if !bytes.Equal(after, before) {
					t.Errorf("a refused rewrite changed the cohort (%d -> %d bytes)", len(before), len(after))
				}
				if got := mmapDirListing(t, dir); !slices.Equal(got, listing) {
					t.Errorf("a refused rewrite left the directory %v, want %v", got, listing)
				}
			})
		}
	}
}
