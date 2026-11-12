package main

import (
	"bytes"
	"context"
	stderrors "errors"
	"io"
	"math"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	pcli "github.com/frankbardon/pulse/internal/cli"
)

// TestMemoryLimitFlag_AcceptedOnEveryLeaf (U74 E1-S3, #503):
// --memory-limit is a root persistent flag, so every actionable leaf
// parses it after its own name and lists it in help.
func TestMemoryLimitFlag_AcceptedOnEveryLeaf(t *testing.T) {
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
			// --help stops before GlobalBefore, so the limit is not applied.
			args := append(append([]string{"pulse"}, strings.Fields(leaf)[1:]...), "--help")
			if err := app.Run(context.Background(), args); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			if !strings.Contains(buf.String(), "--memory-limit") {
				t.Errorf("%s --help does not list --memory-limit:\n%s", leaf, buf.String())
			}
		})
	}
}

// TestMemoryLimitFlag_AppliedBeforeTheLeaf: given after a leaf's name
// the flag still sets the process soft memory limit before the leaf
// runs, and a bad value refuses `pulse mcp` before it serves — a usage
// error (exit 2) carrying CLI_INPUT {flag, value}.
//
// Falsified by dropping MemoryLimitFlags from buildApp's root flags.
//
// serial: changes the process-global soft memory limit.
func TestMemoryLimitFlag_AppliedBeforeTheLeaf(t *testing.T) {
	orig := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(orig) })
	debug.SetMemoryLimit(math.MaxInt64)

	if _, err := runApp(t, "version", "--memory-limit", "768MiB"); err != nil {
		t.Fatal(err)
	}
	if got := debug.SetMemoryLimit(-1); got != 768<<20 {
		t.Fatalf("version --memory-limit 768MiB: soft limit %d, want %d", got, int64(768<<20))
	}

	debug.SetMemoryLimit(math.MaxInt64)
	err := runMCPBriefly(t, "--data-dir", t.TempDir(), "--memory-limit", "150%")
	if status, _ := pcli.ExitStatus(err); status != pcli.ExitUsage {
		t.Fatalf("mcp --memory-limit 150%%: exit status %d (err %v), want %d", status, err, pcli.ExitUsage)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.CLI_INPUT ||
		ce.Details["flag"] != "memory-limit" || ce.Details["value"] != "150%" {
		t.Fatalf("err = %#v, want CLI_INPUT {flag: memory-limit, value: 150%%}", err)
	}
	if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
		t.Errorf("a refused mcp run set the soft limit to %d", got)
	}
}
