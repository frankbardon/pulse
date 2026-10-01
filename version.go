package pulse

import "github.com/frankbardon/pulse/internal/buildinfo"

// Version returns the Pulse build version. It resolves, in order, to the
// value injected at link time (`make build` sets it from `git describe`
// via -ldflags -X github.com/frankbardon/pulse/internal/buildinfo.version),
// then the module version recorded by `go install module@vX.Y.Z`, and
// finally "devel" — suffixed "+<short vcs revision>" when the binary
// embeds VCS metadata. It never returns an empty string.
func Version() string {
	return buildinfo.Version()
}
