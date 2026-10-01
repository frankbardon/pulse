//go:build !darwin && !linux && !freebsd

package service

import "errors"

var errMmapUnsupported = errors.New("mmap not supported on this platform")

// mmapFileBytes returns errMmapUnsupported so callers fall back to the
// afero.ReadFile path. Windows would require CreateFileMappingW +
// MapViewOfFile via golang.org/x/sys/windows; not implemented until
// there's a Windows CI gate to validate it.
func mmapFileBytes(_ string) ([]byte, func() error, error) {
	return nil, nil, errMmapUnsupported
}
