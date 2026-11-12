package cli

import (
	"context"
	"math"
	"runtime/debug"
	"strconv"
	"strings"

	perrors "github.com/frankbardon/pulse/errors"
	cli "github.com/urfave/cli/v3"
)

// flagMemoryLimit is the root persistent --memory-limit flag: the Go
// runtime's soft memory limit (debug.SetMemoryLimit) for the whole
// process. It is CLI-only — the limit is process-global, so no
// pulse.Options field carries it and the library never sets it — and
// it has no PULSE_* form: GOMEMLIMIT already is the environment
// variable, and the flag wins over it because it is applied after the
// runtime read GOMEMLIMIT at start-up. Unset leaves the runtime alone.
const flagMemoryLimit = "memory-limit"

// MemoryLimitFlags returns the root persistent --memory-limit flag.
func MemoryLimitFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: flagMemoryLimit,
			Usage: "Go soft memory limit for the process (debug.SetMemoryLimit): a byte size as GOMEMLIMIT spells it " +
				"(14GiB, 14000MiB; units B, KiB, MiB, GiB, TiB) or N% of physical RAM (85%). " +
				"Wins over GOMEMLIMIT; unset leaves the runtime untouched; a bad value is a usage error (CLI_INPUT).",
		},
	}
}

// setMemoryLimit and physicalMemory are the runtime seams
// memoryLimitBefore uses.
var (
	setMemoryLimit = debug.SetMemoryLimit
	physicalMemory = hostPhysicalMemory
)

// memoryLimitBefore applies --memory-limit. GlobalBefore runs it last,
// so a refused run (this flag or any other global flag) never moves the
// process limit. A bad value is a usage error (exit 2) carrying
// CLI_INPUT {flag, value}.
func memoryLimitBefore(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	raw := cmd.String(flagMemoryLimit)
	if raw == "" && !cmd.IsSet(flagMemoryLimit) {
		return ctx, nil
	}
	n, err := parseMemoryLimit(raw, physicalMemory)
	if err != nil {
		return ctx, usageExit(err)
	}
	setMemoryLimit(n)
	return ctx, nil
}

// memoryUnits is GOMEMLIMIT's suffix set (runtime parseByteCount).
var memoryUnits = []struct {
	suffix string
	shift  uint
}{{"TiB", 40}, {"GiB", 30}, {"MiB", 20}, {"KiB", 10}, {"B", 0}}

// parseMemoryLimit turns a --memory-limit value into a byte count: a
// positive integer with an optional GOMEMLIMIT unit suffix, or N% (0 <
// N <= 100) of the physical RAM ram reports. Anything else — zero, a
// negative, over 100%, an unknown unit, an overflow, an unreadable RAM
// size — is CLI_INPUT {flag, value}.
func parseMemoryLimit(s string, ram func() (uint64, error)) (int64, error) {
	v := strings.TrimSpace(s)
	if pct, ok := strings.CutSuffix(v, "%"); ok {
		p, err := strconv.ParseFloat(pct, 64)
		if err != nil || math.IsNaN(p) || p <= 0 || p > 100 {
			return 0, memoryLimitError(s, "want a percentage above 0 and at most 100 (e.g. 85%)")
		}
		total, err := ram()
		if err != nil {
			return 0, memoryLimitError(s, "cannot read physical RAM on this host ("+err.Error()+"); give a byte size such as 14GiB")
		}
		b := float64(total) * p / 100
		if b >= math.MaxInt64 {
			return math.MaxInt64, nil
		}
		if b < 1 {
			return 0, memoryLimitError(s, "the percentage of physical RAM rounds to zero bytes")
		}
		return int64(b), nil
	}
	num, shift := v, uint(0)
	for _, u := range memoryUnits {
		if rest, ok := strings.CutSuffix(v, u.suffix); ok {
			num, shift = rest, u.shift
			break
		}
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n <= 0 || num[0] == '+' {
		return 0, memoryLimitError(s, "want a positive byte size with an optional unit B, KiB, MiB, GiB or TiB (e.g. 14GiB), or N% of physical RAM")
	}
	if n > math.MaxInt64>>shift {
		return 0, memoryLimitError(s, "byte size overflows int64")
	}
	return n << shift, nil
}

func memoryLimitError(value, why string) error {
	return perrors.NewCodedErrorWithDetails(perrors.CLI_INPUT,
		"--"+flagMemoryLimit+" "+value+": "+why,
		map[string]any{"flag": flagMemoryLimit, "value": value})
}
