package cli

import (
	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// newPulse creates a Pulse instance suitable for CLI use.
// It uses an OS filesystem directly so absolute paths work.
func newPulse() (*pulse.Pulse, error) {
	return pulse.New(pulse.Options{FS: afero.NewOsFs()})
}

// newPulseOpts creates a Pulse instance with the given options merged
// over the CLI defaults (OS filesystem). Used by leaves that need to
// toggle behaviour per invocation (e.g. --no-defaults on process / compose).
func newPulseOpts(o pulse.Options) (*pulse.Pulse, error) {
	if o.FS == nil {
		o.FS = afero.NewOsFs()
	}
	return pulse.New(o)
}
