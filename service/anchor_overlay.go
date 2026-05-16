package service

import (
	"bytes"
	"os"
	"time"

	"github.com/spf13/afero"
)

// anchorOverlay is an afero.Fs that intercepts reads of one specific
// virtual path (the anchor form `archive.pulse#shard.pulse`) and
// serves the in-memory bytes of the corresponding shard payload.
// Every other path falls through to the wrapped base filesystem so
// path normalisation (DataDir, etc.) keeps working unchanged.
//
// The streamingIterator only needs Open/ReadFile semantics for the
// virtual path; mutating operations (Create, Mkdir, Remove, Chmod,
// Chtimes) fall through unchanged.
type anchorOverlay struct {
	afero.Fs
	virtualPath string
	payload     []byte
}

// newAnchorOverlay returns an overlay that serves payload for reads
// of virtualPath, delegating every other operation to base.
func newAnchorOverlay(base afero.Fs, virtualPath string, payload []byte) afero.Fs {
	return &anchorOverlay{Fs: base, virtualPath: virtualPath, payload: payload}
}

// Open returns the shard payload for the virtual path; otherwise
// falls through. The returned afero.File is read-only.
func (a *anchorOverlay) Open(name string) (afero.File, error) {
	if name == a.virtualPath {
		return &anchorFile{name: name, r: bytes.NewReader(a.payload), size: int64(len(a.payload))}, nil
	}
	return a.Fs.Open(name)
}

// OpenFile honours the virtual-path intercept for read-only flags
// (the only mode the streaming iterator and ReadFile use).
func (a *anchorOverlay) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if name == a.virtualPath && (flag&os.O_WRONLY == 0) && (flag&os.O_RDWR == 0) {
		return &anchorFile{name: name, r: bytes.NewReader(a.payload), size: int64(len(a.payload))}, nil
	}
	return a.Fs.OpenFile(name, flag, perm)
}

// Stat surfaces a synthetic FileInfo for the virtual path so
// afero.ReadFile and friends can size the payload before reading.
func (a *anchorOverlay) Stat(name string) (os.FileInfo, error) {
	if name == a.virtualPath {
		return &anchorFileInfo{name: name, size: int64(len(a.payload))}, nil
	}
	return a.Fs.Stat(name)
}

// Name reports the overlay's identity for diagnostic output.
func (a *anchorOverlay) Name() string { return "anchorOverlay(" + a.Fs.Name() + ")" }

// --- afero.File backing an in-memory shard payload ---

type anchorFile struct {
	name string
	r    *bytes.Reader
	size int64
}

func (f *anchorFile) Close() error                                 { return nil }
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
