//go:build !darwin && !linux

package cli

import stderrors "errors"

// hostPhysicalMemory has no reader off darwin and linux: the
// --memory-limit N% form is refused there (a byte size still works).
func hostPhysicalMemory() (uint64, error) {
	return 0, stderrors.New("physical RAM is read only on darwin and linux")
}
