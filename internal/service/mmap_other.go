//go:build !darwin && !linux && !freebsd && !windows

package service

import "errors"

var errMmapUnsupported = errors.New("mmap not supported on this platform")

// mmapSupported reports that this build has no mmapFileBytes fast path
// (the test predicate the mmap-engagement suites key on).
const mmapSupported = false

// mmapFileBytes returns errMmapUnsupported so callers fall back to the
// afero.ReadFile path. The unix builds (mmap_unix.go) and Windows
// (mmap_windows.go) map the file; every other GOOS reads it whole.
func mmapFileBytes(_ string) ([]byte, func() error, error) {
	return nil, nil, errMmapUnsupported
}
