// Package buildinfo is the single source of the Pulse build version.
//
// It is a leaf package: it imports only the standard library, so any
// layer — including the no-execute descriptor/ package — may depend on
// it without widening its import graph.
//
// Version resolves through a fixed chain:
//
//  1. the link-time value of version, set by the build system with
//     -ldflags "-X github.com/frankbardon/pulse/internal/buildinfo.version=<v>"
//     (make build does this from `git describe`);
//  2. debug.ReadBuildInfo().Main.Version when it is a real module version
//     (a `go install module@vX.Y.Z` build), never "(devel)" or empty;
//  3. "devel", suffixed "+<short vcs.revision>" when the binary embeds
//     VCS metadata.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// version is set at link time via -ldflags -X. Empty means "not injected".
var version = ""

// readBuildInfo is the BuildInfo reader. It is a package-level variable so
// tests can substitute each arm of the fallback chain.
var readBuildInfo = debug.ReadBuildInfo

// Devel is the version reported when neither ldflags nor the module
// version supply one.
const Devel = "devel"

// shortRevisionLen is the length of the abbreviated VCS revision appended
// to Devel, matching the Go pseudo-version convention.
const shortRevisionLen = 12

// Version returns the Pulse build version via the ldflags → module
// version → "devel[+<short revision>]" chain described in the package doc.
func Version() string {
	if version != "" {
		return version
	}
	info, ok := readBuildInfo()
	if ok && info != nil {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		if rev := setting(info, "vcs.revision"); rev != "" {
			if len(rev) > shortRevisionLen {
				rev = rev[:shortRevisionLen]
			}
			return Devel + "+" + rev
		}
	}
	return Devel
}

// Commit returns the full VCS revision (BuildInfo setting vcs.revision),
// or "" when the binary carries no VCS metadata (e.g. under go test).
func Commit() string {
	return buildSetting("vcs.revision")
}

// CommitTime returns the VCS commit time (BuildInfo setting vcs.time, an
// RFC 3339 string), or "" when unavailable.
func CommitTime() string {
	return buildSetting("vcs.time")
}

// GoVersion returns the Go toolchain version that built the binary:
// BuildInfo.GoVersion when available, runtime.Version() otherwise.
func GoVersion() string {
	if info, ok := readBuildInfo(); ok && info != nil && info.GoVersion != "" {
		return info.GoVersion
	}
	return runtime.Version()
}

// SetForTest overrides the link-time version and returns a func that
// restores the previous value. FOR TESTS ONLY — production code must never
// call it; the version is a build fact, not runtime state.
func SetForTest(v string) (restore func()) {
	prev := version
	version = v
	return func() { version = prev }
}

func buildSetting(key string) string {
	info, ok := readBuildInfo()
	if !ok || info == nil {
		return ""
	}
	return setting(info, key)
}

func setting(info *debug.BuildInfo, key string) string {
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}
