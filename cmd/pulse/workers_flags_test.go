package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
)

// TestWorkerFlags_AcceptedOnEveryLeaf (U51 E2-S2, FR-38):
// --shard-workers / --decode-workers are root persistent flags, so
// every actionable leaf parses them after its own name and lists them
// in help.
func TestWorkerFlags_AcceptedOnEveryLeaf(t *testing.T) {
	t.Parallel()

	for _, leaf := range cliLeaves() {
		if isCompletionLeafPath(strings.Fields(leaf)) {
			continue // urfave's script printers: no instance, persistent flags unlisted
		}
		t.Run(leaf, func(t *testing.T) {
			app := buildApp()
			var buf bytes.Buffer
			setWriterRecursive(app, &buf)
			app.ErrWriter = io.Discard
			args := append(append([]string{"pulse"}, strings.Fields(leaf)[1:]...),
				"--shard-workers", "2", "--decode-workers", "0", "--help")
			if err := app.Run(context.Background(), args); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			if !strings.Contains(buf.String(), "--shard-workers") || !strings.Contains(buf.String(), "--decode-workers") {
				t.Errorf("%s --help does not list the worker flags:\n%s", leaf, buf.String())
			}
		})
	}
}

// TestWorkerFlags_ReachTheRun: the flag reaches the instance the leaf
// builds — over a three-shard archive `api process --shard-workers 3`
// runs the per-shard parallel arm on 3 workers and `--shard-workers 1`
// the serial arm, as the Debug plan record on stderr reports.
//
// Falsified by dropping withWorkers from withGlobals.
func TestWorkerFlags_ReachTheRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var shards []string
	for i := range 3 {
		sub := filepath.Join(dir, string(rune('a'+i)))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		// Shard basenames must be distinct inside one archive.
		named := filepath.Join(dir, "s"+string(rune('a'+i))+".pulse")
		if err := os.Rename(createTestPulseFile(t, sub), named); err != nil {
			t.Fatal(err)
		}
		shards = append(shards, named)
	}
	arch := filepath.Join(dir, "archive.pulse")
	p, err := pulse.New(pulse.Options{FS: afero.NewOsFs()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(context.Background(), arch, shards); err != nil {
		t.Fatal(err)
	}
	reqPath := filepath.Join(dir, "request.json")
	req := `{"cohort": {"filename": "` + arch + `"}, "aggregations": [{"type": "AGG_COUNT", "field": "age"}]}`
	if err := os.WriteFile(reqPath, []byte(req), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		workers, want string
	}{
		{"3", `"arm":"shard_parallel","workers":3`},
		{"1", `"workers":1`},
	} {
		_, stderr := runCLIProcess(t, "api", "process", "--request", reqPath, "--json",
			"--log-level", "debug", "--log-format", "json", "--shard-workers", c.workers)
		if !strings.Contains(stderr, c.want) || (c.workers == "1" && strings.Contains(stderr, "shard_parallel")) {
			t.Errorf("--shard-workers %s: plan record does not show %s:\n%s", c.workers, c.want, stderr)
		}
	}
}
