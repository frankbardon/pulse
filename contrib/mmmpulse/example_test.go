package mmmpulse_test

import (
	"context"
	"fmt"
	"slices"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/contrib/mmmpulse"
)

// The pack registers nothing: both media-mix models are built in, listed
// among the manifest's regressions on any instance.
func ExampleExtensions() {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: mmmpulse.Extensions()})
	if err != nil {
		fmt.Println(err)
		return
	}
	m := p.Manifest(context.Background())
	fmt.Println(len(m.Extensions.Regressions))
	for _, r := range m.Regressions {
		if slices.Contains([]string{"REG_MMM_BAYES", "REG_MMM_HILL"}, r.Name) {
			fmt.Println(r.Name)
		}
	}
	// Output:
	// 0
	// REG_MMM_BAYES
	// REG_MMM_HILL
}
