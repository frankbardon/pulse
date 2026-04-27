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
