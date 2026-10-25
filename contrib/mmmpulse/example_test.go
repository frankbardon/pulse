package mmmpulse_test

import (
	"context"
	"fmt"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/contrib/mmmpulse"
)

// Register the pack on an instance: REG_MMM_HILL then runs, predicts and
// self-describes like a built-in regression.
func ExampleExtensions() {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: mmmpulse.Extensions()})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, m := range p.Manifest(context.Background()).Extensions.Regressions {
		fmt.Println(m.Name)
	}
	// Output: REG_MMM_HILL
}
