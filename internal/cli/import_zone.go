package cli

import (
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"

	perr "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	pio "github.com/frankbardon/pulse/io"
)

// sourceTZFlagUsage / dstPolicyFlagUsage are the shared help lines for the
// source-zone import flags.
const (
	sourceTZFlagUsage  = "Zone naive datetime values were recorded in (IANA name such as America/New_York, UTC, or a fixed +HH:MM / -HH:MM offset); stored values stay UTC instants. Bare Zone applies to every datetime column (date columns are skipped); repeatable col=Zone sets one column and wins over the bare form. A value with its own Z or offset ignores the zone"
	dstPolicyFlagUsage = "How a naive datetime that a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under --source-tz: error (default — the import fails naming the first such row), earlier (pre-transition offset) or later (post-transition offset); a resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning"
)

// sourceZoneFlags is the parsed --source-tz / --dst-policy triple, in
// the shape every job that reads a source takes (io.ImportJob,
// io.ConvertJob, imports.Spec).
type sourceZoneFlags struct {
	tz     string
	cols   map[string]string
	policy pio.DSTPolicy
}

// sourceZoneCLIFlags returns fresh --source-tz / --dst-policy flags for
// `import auto`, which builds its flag list outside the shared
// importFlags slice. The leaf must not split slice values on ','
// (importLeaf).
func sourceZoneCLIFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{Name: "source-tz", Usage: sourceTZFlagUsage},
		&cli.StringFlag{Name: "dst-policy", Value: "error", Usage: dstPolicyFlagUsage},
	}
}

// applySourceZoneFlags parses --source-tz / --dst-policy onto job.
func applySourceZoneFlags(cmd *cli.Command, job *pio.ImportJob) error {
	f, err := parseSourceZoneFlags(cmd)
	if err != nil {
		return err
	}
	job.SourceTZ, job.ColumnSourceTZ, job.DSTPolicy = f.tz, f.cols, f.policy
	return nil
}

// applyConvertZoneFlags parses --source-tz / --dst-policy onto a convert
// job.
func applyConvertZoneFlags(cmd *cli.Command, job *pio.ConvertJob) error {
	f, err := parseSourceZoneFlags(cmd)
	if err != nil {
		return err
	}
	job.SourceTZ, job.ColumnSourceTZ, job.DSTPolicy = f.tz, f.cols, f.policy
	return nil
}

// parseSourceZoneFlags parses --source-tz / --dst-policy. Only the flag
// SYNTAX is checked here (CLI_INPUT): at most one bare zone, one
// col=Zone per column, a known policy. Zone names and column existence /
// type are the library's verdict, taken against the resolved schema
// (see cliSourceZoneError).
func parseSourceZoneFlags(cmd *cli.Command) (sourceZoneFlags, error) {
	var out sourceZoneFlags
	for _, v := range cmd.StringSlice("source-tz") {
		// Zone names never contain '=', so the LAST '=' separates the
		// column (which may) from the zone.
		k := strings.LastIndexByte(v, '=')
		if k < 0 {
			if out.tz != "" {
				return sourceZoneFlags{}, perr.NewCodedErrorWithDetails(perr.CLI_INPUT,
					fmt.Sprintf("--source-tz: two bare zones (%q and %q); give one zone for every datetime column and col=Zone for the exceptions", out.tz, v),
					map[string]any{"flag": "source-tz", "value": v})
			}
			if v == "" {
				return sourceZoneFlags{}, perr.NewCodedErrorWithDetails(perr.CLI_INPUT, "--source-tz: empty zone", map[string]any{"flag": "source-tz"})
			}
			out.tz = v
			continue
		}
		col, zone := v[:k], v[k+1:]
		if col == "" || zone == "" {
			return sourceZoneFlags{}, perr.NewCodedErrorWithDetails(perr.CLI_INPUT,
				fmt.Sprintf("--source-tz %q: want Zone or col=Zone", v),
				map[string]any{"flag": "source-tz", "value": v})
		}
		if out.cols == nil {
			out.cols = map[string]string{}
		}
		if prev, dup := out.cols[col]; dup {
			return sourceZoneFlags{}, perr.NewCodedErrorWithDetails(perr.CLI_INPUT,
				fmt.Sprintf("--source-tz: column %q given twice (%q and %q)", col, prev, zone),
				map[string]any{"flag": "source-tz", "column": col})
		}
		out.cols[col] = zone
	}
	switch p := cmd.String("dst-policy"); p {
	case "":
		// A leaf without the flag: the library default (error).
	case "error", "earlier", "later":
		out.policy = pio.DSTPolicy(p)
	default:
		return sourceZoneFlags{}, perr.NewCodedErrorWithDetails(perr.CLI_INPUT,
			fmt.Sprintf("--dst-policy %q: want error, earlier or later", p),
			map[string]any{"flag": "dst-policy", "value": p})
	}
	return out, nil
}

// cliSourceZoneError re-codes the library's source-zone OPTION refusal (a
// --source-tz column the source does not carry, or one that is not
// datetime) as CLI_INPUT: from the command line it is a flag mistake.
// Every other error passes through unchanged.
func cliSourceZoneError(err error) error {
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.SERVICE_VALIDATION || ce.Details[iio.DetailOption] == nil {
		return err
	}
	details := map[string]any{}
	for k, v := range ce.Details {
		details[k] = v
	}
	return perr.NewCodedErrorWithDetails(perr.CLI_INPUT, "--"+strings.ReplaceAll(fmt.Sprint(ce.Details[iio.DetailOption]), "_", "-")+": "+ce.Message, details)
}
