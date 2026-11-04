package service

import (
	"sync/atomic"

	"github.com/spf13/afero"
)

// mmapAvailable is the build's mmap capability, initialised from the
// build-tagged mmapSupported constant. It is a variable (not the
// constant) only so a test can switch it off on a platform that maps and
// prove every mmap gate refuses together; production never writes it.
var mmapAvailable atomic.Bool

func init() { mmapAvailable.Store(mmapSupported) }

// mmapCapable reports whether this build can memory-map a cohort. Every
// gate that decides "this read is mapped" consults it, so predict's
// DecodeMappable, canParallelDecode and the run's mapping attempt give
// the same answer on a GOOS with no mapping (mmap_other.go).
func mmapCapable() bool { return mmapAvailable.Load() }

// Bail reasons mappableRealPath reports; canParallelDecode surfaces them.
const (
	reasonMmapUnsupported = "mmap unsupported on this platform"
	reasonNoRealPath      = "mmap unavailable (no RealPath)"
)

// mappableRealPath is the one mmap-eligibility probe for a single file:
// the build must be able to map (mmapCapable) and fsys must resolve name
// to a real on-disk path (resolveRealPath). It returns the real path, or
// "" with a terse, stable bail reason.
func mappableRealPath(fsys afero.Fs, name string) (string, string) {
	if !mmapCapable() {
		return "", reasonMmapUnsupported
	}
	p, ok := resolveRealPath(fsys, name)
	if !ok {
		return "", reasonNoRealPath
	}
	return p, ""
}
