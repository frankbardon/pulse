package cli

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	cli "github.com/urfave/cli/v3"
)

// runWorkersBefore parses args on a root carrying the worker flags and
// returns the ctx GlobalBefore built (and its error).
func runWorkersBefore(t *testing.T, args ...string) (context.Context, error) {
	t.Helper()
	var got context.Context
	root := &cli.Command{
		Name:   "pulse",
		Flags:  append(append(LogFlags(), WorkerFlags()...), LimitFlags()...),
		Before: GlobalBefore,
		Action: func(ctx context.Context, _ *cli.Command) error { got = ctx; return nil },
	}
	err := root.Run(context.Background(), append([]string{"pulse"}, args...))
	return got, err
}

// TestWorkerFlags_ThreadIntoOptions (U51 E2-S2, FR-38): the root
// --shard-workers / --decode-workers reach pulse.Options through
// withGlobals — the one path newPulseOpts, the docs and import leaves
// and `pulse mcp` (mcpPulseOptions) all take — unset leaves both 0
// (NumCPU), and a caller's own explicit value is never replaced.
//
// Falsified by dropping withWorkers from withGlobals.
func TestWorkerFlags_ThreadIntoOptions(t *testing.T) {
	t.Parallel()

	ctx, err := runWorkersBefore(t, "--shard-workers", "3", "--decode-workers", "1")
	if err != nil {
		t.Fatal(err)
	}
	o := withGlobals(ctx, pulse.Options{})
	if o.ShardWorkers != 3 || o.DecodeWorkers != 1 {
		t.Errorf("withGlobals: ShardWorkers/DecodeWorkers = %d/%d, want 3/1", o.ShardWorkers, o.DecodeWorkers)
	}
	mo := mcpPulseOptions(ctx, t.TempDir())
	if mo.ShardWorkers != 3 || mo.DecodeWorkers != 1 {
		t.Errorf("pulse mcp options: ShardWorkers/DecodeWorkers = %d/%d, want 3/1", mo.ShardWorkers, mo.DecodeWorkers)
	}
	if own := withGlobals(ctx, pulse.Options{ShardWorkers: 5}); own.ShardWorkers != 5 {
		t.Errorf("withGlobals replaced the caller's ShardWorkers 5 with %d", own.ShardWorkers)
	}
	unset, err := runWorkersBefore(t)
	if err != nil {
		t.Fatal(err)
	}
	if o := withGlobals(unset, pulse.Options{}); o.ShardWorkers != 0 || o.DecodeWorkers != 0 {
		t.Errorf("unset flags: ShardWorkers/DecodeWorkers = %d/%d, want 0/0", o.ShardWorkers, o.DecodeWorkers)
	}
}

// TestWorkerFlags_NegativeIsUsageError: a negative worker count is
// refused before any leaf runs — ExitUsage, carrying CLI_INPUT with the
// flag and value — the rule pulse.New applies to the Options fields.
//
// Falsified by deleting the n < 0 refusal in workersBefore.
func TestWorkerFlags_NegativeIsUsageError(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--shard-workers", "--decode-workers"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			_, err := runWorkersBefore(t, flag, "-2")
			if status, _ := ExitStatus(err); status != ExitUsage {
				t.Fatalf("%s -2: exit status %d (err %v), want %d", flag, status, err, ExitUsage)
			}
			var ce *perrors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perrors.CLI_INPUT || ce.Details["flag"] != flag[2:] || ce.Details["value"] != -2 {
				t.Fatalf("%s -2: err = %#v, want CLI_INPUT {flag: %s, value: -2}", flag, err, flag[2:])
			}
		})
	}
}
