package cli

import (
	"context"
	"strconv"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	cli "github.com/urfave/cli/v3"
)

// Worker flag names. Like the log flags they are root persistent flags,
// so every leaf accepts them in any position; GlobalBefore validates
// them once and newPulseOpts (withGlobals) hands them to every instance
// a leaf builds — `pulse mcp` included. No environment-variable form.
const (
	flagShardWorkers  = "shard-workers"
	flagDecodeWorkers = "decode-workers"
)

// WorkerFlags returns the root persistent --shard-workers /
// --decode-workers flags: Options.ShardWorkers / Options.DecodeWorkers
// for every instance-building leaf (0 = runtime.NumCPU, 1 = serial).
func WorkerFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:  flagShardWorkers,
			Usage: "Per-shard parallel workers over a shard archive (Options.ShardWorkers): 0 = one per CPU, 1 = serial",
		},
		&cli.IntFlag{
			Name:  flagDecodeWorkers,
			Usage: "Parallel decode workers over a large single .pulse file (Options.DecodeWorkers): 0 = one per CPU, 1 = serial",
		},
	}
}

// workersKey carries the validated worker caps to the leaves.
type workersKey struct{}

type workerCaps struct{ shard, decode int }

// GlobalBefore is the root command's Before: the log flags (LogBefore)
// then the worker flags. A negative worker count is refused before any
// leaf runs — a usage error (exit 2) carrying CLI_INPUT, the rule
// pulse.New applies to the Options fields.
//
// A shell-completion request skips both: completion must stay silent
// and exit 0 whatever the persistent flags typed so far say, and no
// leaf runs to read them.
func GlobalBefore(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	if completionRequested(ctx) {
		return ctx, nil
	}
	ctx, err := LogBefore(ctx, cmd)
	if err != nil {
		return ctx, err
	}
	return workersBefore(ctx, cmd)
}

func workersBefore(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	var caps workerCaps
	for _, f := range []struct {
		name string
		dst  *int
	}{{flagShardWorkers, &caps.shard}, {flagDecodeWorkers, &caps.decode}} {
		n := int(cmd.Int(f.name))
		if n < 0 {
			return ctx, usageExit(perrors.NewCodedErrorWithDetails(perrors.CLI_INPUT,
				"--"+f.name+" "+strconv.Itoa(n)+": want 0 (one per CPU), 1 (serial) or more",
				map[string]any{"flag": f.name, "value": n}))
		}
		*f.dst = n
	}
	if caps == (workerCaps{}) {
		return ctx, nil
	}
	return context.WithValue(ctx, workersKey{}, caps), nil
}

// withWorkers sets o.ShardWorkers / o.DecodeWorkers from ctx unless the
// caller already set them.
func withWorkers(ctx context.Context, o pulse.Options) pulse.Options {
	if ctx == nil {
		return o
	}
	caps, _ := ctx.Value(workersKey{}).(workerCaps)
	if o.ShardWorkers == 0 {
		o.ShardWorkers = caps.shard
	}
	if o.DecodeWorkers == 0 {
		o.DecodeWorkers = caps.decode
	}
	return o
}

// withGlobals applies every root persistent flag to o: the logger and
// the worker caps. Every leaf that builds a Pulse instance routes its
// Options through it.
func withGlobals(ctx context.Context, o pulse.Options) pulse.Options {
	return withWorkers(ctx, withLogger(ctx, o))
}
