//go:build ignore

// build_fixtures.go regenerates the canonical Pulse shard archives
// committed under testdata/sharding/. Run it from the repo root:
//
//	go run ./testdata/sharding/build_fixtures.go
//
// The archive contents live in internal/shardfixtures — the same
// generator TestShardFixtures_MatchGenerator compares the committed
// files against, so a codec change that moves the bytes fails the suite
// until the fixtures are regenerated (with this command, or
// `go test ./internal/shardfixtures/ -update`). The committed .pulse
// files need `git add -f` (*.pulse is gitignored).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/frankbardon/pulse/internal/shardfixtures"
)

func main() {
	files, err := shardfixtures.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, "build_fixtures:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(shardfixtures.Dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "build_fixtures:", err)
		os.Exit(1)
	}
	for _, f := range files {
		p := filepath.Join(shardfixtures.Dir, f.Name)
		if err := os.WriteFile(p, f.Bytes, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "build_fixtures:", err)
			os.Exit(1)
		}
		fmt.Printf("  %s (%d bytes)\n", p, len(f.Bytes))
	}
}
