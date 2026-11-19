// Command genfixtures rewrites the generated example fixture CSVs
// under internal/examples/fixtures/. Run it from the repo root:
//
//	go run ./internal/examples/fixtures/cmd/genfixtures
//
// It writes exactly what fixtures.Generate returns, so the committed
// files match TestFixtures_RegenerateByteIdentical on every
// architecture.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/frankbardon/pulse/internal/examples/fixtures"
)

const outputDir = "internal/examples/fixtures"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genfixtures:", err)
		os.Exit(1)
	}
}

func run() error {
	spec, err := os.ReadFile(filepath.FromSlash(fixtures.BrandSpecPath))
	if err != nil {
		return fmt.Errorf("run from the repo root: %w", err)
	}
	files, err := fixtures.Generate(spec)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(outputDir, f.Name), f.Data, 0o644); err != nil {
			return err
		}
	}
	fmt.Println("wrote", len(files), "fixture CSVs to", outputDir)
	return nil
}
