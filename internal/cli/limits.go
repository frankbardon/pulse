package cli

import (
	"context"
	"strings"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	cli "github.com/urfave/cli/v3"
)

// flagLimit is the root persistent --limit flag. Like the worker flags
// every leaf accepts it in any position; GlobalBefore parses it once and
// withGlobals hands the Options.Limits layer to every instance a leaf
// builds — `pulse mcp` included. No environment-variable form.
const flagLimit = "limit"

// LimitFlags returns the root persistent, repeatable --limit name=value
// flag: the Options.Limits input layer for every instance-building leaf.
func LimitFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{
			Name: flagLimit,
			Usage: "Instance resource limit as name=value (repeatable; names: " + commaList(pulse.LimitNames()) + "). " +
				"Values: a positive integer, a Go duration for request_timeout (30s), unlimited (or -1), or 0 for the default. " +
				"Under pulse mcp it overrides the feature profile's limits section per key; an unknown name or bad value is a usage error (CLI_INPUT).",
		},
	}
}

// limitsKey carries the parsed --limit layer to the leaves.
type limitsKey struct{}

// limitsBefore parses the root --limit flags. A malformed pair, an
// unknown name or a bad value is refused before any leaf runs — a usage
// error (exit 2) carrying CLI_INPUT, like a negative worker count.
func limitsBefore(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	lims, err := parseLimitFlags(cmd.StringSlice(flagLimit))
	if err != nil {
		return ctx, usageExit(err)
	}
	if lims == (pulse.Limits{}) {
		return ctx, nil
	}
	return context.WithValue(ctx, limitsKey{}, lims), nil
}

// withLimits fills every o.Limits key the caller left at 0 (default)
// from the --limit layer in ctx; a caller's own explicit key wins.
func withLimits(ctx context.Context, o pulse.Options) pulse.Options {
	if ctx == nil {
		return o
	}
	lims, ok := ctx.Value(limitsKey{}).(pulse.Limits)
	if !ok {
		return o
	}
	for _, n := range limits.Names() {
		if limits.Value(o.Limits, n) == 0 {
			limits.Set(&o.Limits, n, limits.Value(lims, n))
		}
	}
	return o
}

// parseLimitFlags turns repeated `--limit name=value` flags into the
// Options.Limits input layer (0 = default, so an unset key defers to the
// feature profile and then the built-in default). A later flag for the
// same name wins. A malformed pair, an unknown name or a bad value is
// CLI_INPUT with details {flag, value} (+ limit when the name parsed).
func parseLimitFlags(specs []string) (pulse.Limits, error) {
	var out pulse.Limits
	for _, spec := range specs {
		key, raw, ok := strings.Cut(spec, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return pulse.Limits{}, limitFlagError(spec, "",
				"--limit "+spec+": want name=value (e.g. max_groups=1000000, request_timeout=30s)")
		}
		n, err := limits.ParseName(key)
		if err != nil {
			return pulse.Limits{}, limitFlagError(spec, "",
				"--limit "+spec+": unknown limit "+key+"; known: "+limitNameList())
		}
		v, err := limits.ParseValue(n, raw)
		if err != nil {
			return pulse.Limits{}, limitFlagError(spec, n,
				"--limit "+spec+": bad value for "+key+": "+err.Error())
		}
		limits.Set(&out, n, v)
	}
	return out, nil
}

func limitNameList() string {
	return commaList(pulse.LimitNames())
}

func limitFlagError(spec string, n limits.Name, msg string) error {
	details := map[string]any{"flag": flagLimit, "value": spec}
	if n != "" {
		details["limit"] = string(n)
	}
	return perrors.NewCodedErrorWithDetails(perrors.CLI_INPUT, msg, details)
}
