package service

import (
	"bytes"
	"io"
	"os"
	"time"

	"github.com/spf13/afero"
)

// anchorOverlay is an afero.Fs that intercepts reads of one specific
// virtual path (the anchor form `archive.pulse#shard.pulse`) and
// serves the corresponding shard payload: for a stored entry
// (newAnchorSectionOverlay) a SectionReader at the entry's data offset
// over a fresh handle on the archive, so no shard byte is resident; for
// a deflated third-party entry (newAnchorOverlay) its inflated bytes.
// Every other path falls through to the wrapped base filesystem so
// path normalisation (DataDir, etc.) keeps working unchanged.
//
// The streamingIterator only needs Open/ReadFile semantics for the
// virtual path; mutating operations (Create, Mkdir, Remove, Chmod,
// Chtimes) fall through unchanged.
type anchorOverlay struct {
	afero.Fs
	virtualPath string
	// payload is the inflated shard (newAnchorOverlay); nil for a
	// section overlay.
	payload []byte
	// archivePath / offset / size locate a stored shard inside its
	// archive on Fs (newAnchorSectionOverlay).
	archivePath  string
	offset, size int64
}

// newAnchorOverlay returns an overlay that serves payload for reads
// of virtualPath, delegating every other operation to base.
func newAnchorOverlay(base afero.Fs, virtualPath string, payload []byte) afero.Fs {
	return &anchorOverlay{Fs: base, virtualPath: virtualPath, payload: payload, size: int64(len(payload))}
}

// newAnchorSectionOverlay returns an overlay that serves the size
// bytes at offset of archivePath (on base) for reads of virtualPath:
// each open is a fresh archive handle and a SectionReader over it.
func newAnchorSectionOverlay(base afero.Fs, virtualPath, archivePath string, offset, size int64) afero.Fs {
	return &anchorOverlay{Fs: base, virtualPath: virtualPath, archivePath: archivePath, offset: offset, size: size}
}

// anchorSectionSize reports the shard size when path is the virtual
// path of an anchor overlay — seen through the observability byte
// counter (execCountingFs) too, so counting never changes the read —
// and false for any other fs or path.
func anchorSectionSize(fsys afero.Fs, path string) (int64, bool) {
	if c, ok := fsys.(*execCountingFs); ok {
		fsys = c.Fs
	}
	a, ok := fsys.(*anchorOverlay)
	if !ok || path != a.virtualPath {
		return 0, false
	}
	return a.size, true
}

// openVirtual opens the shard payload behind the virtual path.
func (a *anchorOverlay) openVirtual(name string) (afero.File, error) {
	if a.payload != nil || a.archivePath == "" {
		return &anchorFile{name: name, r: bytes.NewReader(a.payload), size: a.size}, nil
	}
	f, err := a.Fs.Open(a.archivePath)
	if err != nil {
		return nil, err
	}
	sr := io.NewSectionReader(concurrentReaderAt(f), a.offset, a.size)
	return &anchorFile{name: name, r: sr, size: a.size, closer: f}, nil
}

// Open returns the shard payload for the virtual path; otherwise
// falls through. The returned afero.File is read-only.
func (a *anchorOverlay) Open(name string) (afero.File, error) {
	if name == a.virtualPath {
		return a.openVirtual(name)
	}
	return a.Fs.Open(name)
}

// OpenFile honours the virtual-path intercept for read-only flags
// (the only mode the streaming iterator and ReadFile use).
func (a *anchorOverlay) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if name == a.virtualPath && (flag&os.O_WRONLY == 0) && (flag&os.O_RDWR == 0) {
		return a.openVirtual(name)
	}
	return a.Fs.OpenFile(name, flag, perm)
}

// Stat surfaces a synthetic FileInfo for the virtual path so
// afero.ReadFile and friends can size the payload before reading.
func (a *anchorOverlay) Stat(name string) (os.FileInfo, error) {
	if name == a.virtualPath {
		return &anchorFileInfo{name: name, size: a.size}, nil
	}
	return a.Fs.Stat(name)
}

// Name reports the overlay's identity for diagnostic output.
func (a *anchorOverlay) Name() string { return "anchorOverlay(" + a.Fs.Name() + ")" }

type anchorFile struct {
	name string
	r    interface {
		io.Reader
		io.ReaderAt
		io.Seeker
	}
	size int64
	// closer releases the archive handle behind a section-backed file.
	closer io.Closer
}

func (f *anchorFile) Close() error {
	if f.closer == nil {
		return nil
	}
	err := f.closer.Close()
	f.closer = nil
	return err
}
func (f *anchorFile) Name() string                                 { return f.name }
func (f *anchorFile) Read(p []byte) (int, error)                   { return f.r.Read(p) }
func (f *anchorFile) ReadAt(p []byte, off int64) (int, error)      { return f.r.ReadAt(p, off) }
func (f *anchorFile) Seek(offset int64, whence int) (int64, error) { return f.r.Seek(offset, whence) }
func (f *anchorFile) Write(_ []byte) (int, error)                  { return 0, os.ErrPermission }
func (f *anchorFile) WriteAt(_ []byte, _ int64) (int, error)       { return 0, os.ErrPermission }
func (f *anchorFile) WriteString(_ string) (int, error)            { return 0, os.ErrPermission }
func (f *anchorFile) Sync() error                                  { return nil }
func (f *anchorFile) Truncate(_ int64) error                       { return os.ErrPermission }
func (f *anchorFile) Stat() (os.FileInfo, error) {
	return &anchorFileInfo{name: f.name, size: f.size}, nil
}
func (f *anchorFile) Readdir(_ int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }
func (f *anchorFile) Readdirnames(_ int) ([]string, error) { return nil, os.ErrInvalid }

type anchorFileInfo struct {
	name string
	size int64
}

func (i *anchorFileInfo) Name() string       { return i.name }
func (i *anchorFileInfo) Size() int64        { return i.size }
func (i *anchorFileInfo) Mode() os.FileMode  { return 0o444 }
func (i *anchorFileInfo) ModTime() time.Time { return time.Time{} }
func (i *anchorFileInfo) IsDir() bool        { return false }
func (i *anchorFileInfo) Sys() any           { return nil }
