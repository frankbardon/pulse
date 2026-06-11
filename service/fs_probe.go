package service

import (
	"github.com/spf13/afero"
)

// RealPather is the capability interface a filesystem implements to
// declare that the virtual path it serves maps to a regular on-disk
// file the kernel can mmap. The returned path MUST satisfy:
//
//   - It is an absolute or process-CWD-relative path resolvable by
//     os.Open at the moment of the call.
//   - The file is a regular file (not a device, FIFO, or socket).
//   - The returned bytes, when read, are byte-equal to what the
//     filesystem's Open(name).Read would yield.
//
// External afero.Fs implementations (e.g. a CopyOnRead overlay that
// caches GCS objects to a local tempdir) should satisfy this interface
// to opt their files into the iterator's mmap fast path. The signature
// intentionally matches afero.BasePathFs.RealPath so that wrapper is
// detected without further glue.
//
// Returning a non-nil error declines the fast path for that call and
// the iterator falls back to afero.ReadFile.
type RealPather interface {
	RealPath(name string) (string, error)
}

// resolveRealPath walks fs and its wrappers looking for a real on-disk
// path for name. Returns the resolved absolute/relative path string and
// true when probing succeeds; returns "" and false otherwise.
//
// Probe order:
//
//  1. fs itself satisfies RealPather (covers both *afero.BasePathFs and
//     any external opt-in fs).
//  2. fs is *afero.OsFs — name is already the on-disk path.
//  3. (Future) further unwrap layers can be added here; today
//     *afero.BasePathFs.RealPath is method-callable so step 1 catches it
//     without a separate type-switch.
//
// MemMapFs and unknown wrappers return ("", false) so the caller falls
// back to afero.ReadFile and hermetic tests stay clean.
//
// The probe MUST stay cheap. No filesystem walks, no Stat. The optional
// "open-and-inspect" fallback the story Notes mention is intentionally
// NOT taken here: every wrapper Pulse cares about (BasePathFs in-tree;
// the BERA CoR overlay downstream) can advertise RealPath at near-zero
// cost. Adding an Open-based probe would charge an extra syscall on the
// hot path for every Process() call. If a future fs needs it we can add
// the probe as an opt-in third layer; today the two layers above cover
// every fs in the dependency graph.
func resolveRealPath(fs afero.Fs, name string) (string, bool) {
	if fs == nil {
		return "", false
	}
	if rp, ok := fs.(RealPather); ok {
		resolved, err := rp.RealPath(name)
		if err != nil {
			return "", false
		}
		return resolved, true
	}
	if _, ok := fs.(*afero.OsFs); ok {
		return name, true
	}
	return "", false
}
