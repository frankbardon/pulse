//go:build windows

package service

import (
	"errors"
	"math"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// mmapSupported reports that this build carries a real mmapFileBytes
// (the test predicate the mmap-engagement suites key on).
const mmapSupported = true

// errMmapNotRegular refuses a path that is not a regular file (a
// directory, a named pipe, a device): the caller falls back to
// afero.ReadFile, as the unix build does when mmap(2) rejects one.
var errMmapNotRegular = errors.New("mmap: not a regular file")

// errMmapTooLarge refuses a file whose size does not fit the address
// space (a 32-bit build): the caller falls back to afero.ReadFile.
var errMmapTooLarge = errors.New("mmap: file larger than the address space")

// mmapFileBytes maps the regular file at path read-only and returns the
// underlying byte slice plus a cleanup closure — the Windows twin of the
// unix build: CreateFileMappingW (PAGE_READONLY) over the whole file,
// then MapViewOfFile (FILE_MAP_READ). The returned slice is valid until
// cleanup is invoked; reading past cleanup is undefined (an access
// violation).
//
// Empty files return a nil slice and a no-op cleanup (CreateFileMapping
// rejects a zero-length mapping of an empty file, like mmap(2)); a
// special file is refused with an error so the caller falls back.
//
// The file is opened with FILE_SHARE_READ | FILE_SHARE_WRITE |
// FILE_SHARE_DELETE (mmapShareMode), so the handle Pulse holds never
// refuses another opener its access mode. Every Pulse rewrite releases
// its own views before its rename; what a view another reader still
// holds does to a rewrite is .claude/reference/byte-layout/mmap-and-rewrites.md.
//
// cleanup releases the view, the section handle and the file handle, in
// that order, and returns the first error.
func mmapFileBytes(path string) ([]byte, func() error, error) {
	return mmapFileShared(path, mmapShareMode)
}

// mmapShareMode is the share mode mmapFileBytes opens the file with.
const mmapShareMode = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

// mmapFileShared is mmapFileBytes with the open's share mode explicit.
func mmapFileShared(path string, share uint32) ([]byte, func() error, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, errMmapNotRegular
	}
	size := info.Size()
	if size == 0 {
		_ = f.Close()
		return nil, func() error { return nil }, nil
	}
	if uint64(size) > math.MaxInt {
		_ = f.Close()
		return nil, nil, errMmapTooLarge
	}
	section, err := windows.CreateFileMapping(h, nil, windows.PAGE_READONLY,
		uint32(uint64(size)>>32), uint32(uint64(size)), nil)
	if err != nil {
		_ = f.Close()
		return nil, nil, &os.PathError{Op: "CreateFileMapping", Path: path, Err: err}
	}
	addr, err := windows.MapViewOfFile(section, windows.FILE_MAP_READ, 0, 0, uintptr(size))
	if err != nil {
		_ = windows.CloseHandle(section)
		_ = f.Close()
		return nil, nil, &os.PathError{Op: "MapViewOfFile", Path: path, Err: err}
	}
	// addr is an OS-owned address outside the Go heap; the view stays
	// mapped until cleanup, so the slice never outlives it while in use.
	data := unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&addr))), int(size))
	cleanup := func() error {
		uerr := windows.UnmapViewOfFile(addr)
		herr := windows.CloseHandle(section)
		ferr := f.Close()
		for _, e := range []error{uerr, herr, ferr} {
			if e != nil {
				return e
			}
		}
		return nil
	}
	return data, cleanup, nil
}
