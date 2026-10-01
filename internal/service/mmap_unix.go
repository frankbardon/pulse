//go:build darwin || linux || freebsd

package service

import (
	"os"

	"golang.org/x/sys/unix"
)

// mmapFileBytes maps the regular file at path read-only and returns the
// underlying byte slice plus a cleanup closure. The returned slice is
// valid until cleanup is invoked; reading past cleanup is undefined and
// may SIGBUS.
//
// Empty files return a nil slice and a no-op cleanup (mmap rejects
// zero-length mappings on darwin/linux).
//
// Returned data is read-only; callers MUST NOT write to the slice.
// PROT_READ + MAP_SHARED prevents accidental write-through but does not
// stop someone re-slicing the buffer into a writable view.
func mmapFileBytes(path string) ([]byte, func() error, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	size := info.Size()
	if size == 0 {
		_ = f.Close()
		return nil, func() error { return nil }, nil
	}
	data, err := unix.Mmap(int(f.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	// Hint sequential access — kernel can prefetch ahead and evict
	// behind. Pulse scans linearly so this is the right access pattern.
	_ = unix.Madvise(data, unix.MADV_SEQUENTIAL)
	cleanup := func() error {
		merr := unix.Munmap(data)
		ferr := f.Close()
		if merr != nil {
			return merr
		}
		return ferr
	}
	return data, cleanup, nil
}
