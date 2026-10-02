package pulse

import "github.com/frankbardon/pulse/internal/buildinfo"

// Version returns the Pulse build version. It resolves, in order, to the
// value injected at link time (`make build` sets it from `git describe`
// via -ldflags -X github.com/frankbardon/pulse/internal/buildinfo.version),
// then Pulse's own module version from the build info (Main when built via
// `go install module@vX.Y.Z`, the Pulse dependency entry — honouring
// replace — when linked into an embedder; never the embedder's version), and
// finally "devel" — suffixed "+<short vcs revision>" when the binary
// embeds VCS metadata. It never returns an empty string.
func Version() string {
	return buildinfo.Version()
}
