// Command doccoverage regenerates the documentation coverage matrix,
// docs/src/contract/coverage.md, from the repository it runs in (the module
// root). `make docs-generate` runs it with the table / template / profile
// environment variables cleared; TestDocsCoverageCurrent fails when the
// committed page is stale.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/doccoverage"
)

func main() {
	out := flag.String("out", doccoverage.PagePath, "page to write, relative to the module root")
	flag.Parse()
	if err := run(afero.NewOsFs(), *out); err != nil {
		fmt.Fprintln(os.Stderr, "doccoverage:", err)
		os.Exit(1)
	}
}

func run(fsys afero.Fs, out string) error {
	page, err := doccoverage.Generate(fsys)
	if err != nil {
		return err
	}
	return afero.WriteFile(fsys, out, page, 0o644)
}
