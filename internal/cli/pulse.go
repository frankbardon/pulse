package cli

import (
	"context"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// newPulse creates a Pulse instance suitable for CLI use.
// It uses an OS filesystem directly so absolute paths work, and the
// stderr logger the root --log-level flag installed in ctx (nil = silent)
// and the root --shard-workers / --decode-workers caps.
func newPulse(ctx context.Context) (*pulse.Pulse, error) {
	return newPulseOpts(ctx, pulse.Options{})
}

// newPulseOpts creates a Pulse instance with the given options merged
// over the CLI defaults (OS filesystem, the ctx logger and worker caps —
// withGlobals). Used by leaves
// that need to toggle behaviour per invocation (e.g. --no-defaults on
// process / compose).
func newPulseOpts(ctx context.Context, o pulse.Options) (*pulse.Pulse, error) {
	if o.FS == nil {
		o.FS = afero.NewOsFs()
	}
	return pulse.New(withGlobals(ctx, o))
}
