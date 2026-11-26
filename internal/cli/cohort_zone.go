package cli

import (
	"context"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	cli "github.com/urfave/cli/v3"
)

// cohortZoneCmd returns the `pulse cohort zone` group: set or clear an
// existing cohort's DEFAULT per-row zone field (the format 0x02 ZONE
// section an import's --zone-field writes). Every behaviour —
// validation, the single-file / archive dispatch, the atomic
// schema-only rewrite, the sidecar carry and the archive warning — is
// pulse.SetCohortZoneField / ClearCohortZoneField; these leaves parse
// arguments, call it once and format the report.
func cohortZoneCmd() *cli.Command {
	return &cli.Command{
		Name:  "zone",
		Usage: "Set or clear a cohort's default per-row zone field",
		Commands: []*cli.Command{
			cohortZoneSetCmd(),
			cohortZoneClearCmd(),
		},
	}
}

func cohortZoneSetCmd() *cli.Command {
	return &cli.Command{
		Name:      "set",
		Usage:     "Declare a categorical field of IANA zone names as the cohort's default per-row zone field, rewriting the cohort's schema in place (non-interactive, atomic; a shard archive is rewritten whole, with a warning)",
		ArgsUsage: "COHORT FIELD",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path (or pass as the first positional argument)"},
			&cli.StringFlag{Name: "field", Aliases: []string{"f"}, Usage: "The categorical zone field to declare (or pass as the last positional argument)"},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			args := cmd.Args().Slice()
			input, field := cmd.String("input"), cmd.String("field")
			if input == "" && len(args) > 0 {
				input, args = args[0], args[1:]
			}
			if field == "" && len(args) > 0 {
				field = args[0]
			}
			if input == "" || field == "" {
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"usage: pulse cohort zone set COHORT FIELD (or --input COHORT --field FIELD)")
			}
			p, err := newPulse(ctx)
			if err != nil {
				return cliErrorFrom(cmd, jsonOut, "CLI_ERROR", err)
			}
			res, err := p.SetCohortZoneField(ctx, input, field)
			if err != nil {
				return cliCodedError(cmd, jsonOut, "CLI_ERROR", err)
			}
			return writeCohortZoneResult(cmd, jsonOut, res)
		},
	}
}

func cohortZoneClearCmd() *cli.Command {
	return &cli.Command{
		Name:      "clear",
		Usage:     "Remove the cohort's default per-row zone field, rewriting the cohort's schema in place (non-interactive, atomic; a cohort set then cleared is byte-identical to the original)",
		ArgsUsage: "COHORT",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path (or pass as the positional argument)"},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			input := cmd.String("input")
			if input == "" {
				input = cmd.Args().First()
			}
			if input == "" {
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"usage: pulse cohort zone clear COHORT (or --input COHORT)")
			}
			p, err := newPulse(ctx)
			if err != nil {
				return cliErrorFrom(cmd, jsonOut, "CLI_ERROR", err)
			}
			res, err := p.ClearCohortZoneField(ctx, input)
			if err != nil {
				return cliCodedError(cmd, jsonOut, "CLI_ERROR", err)
			}
			return writeCohortZoneResult(cmd, jsonOut, res)
		},
	}
}

// writeCohortZoneResult renders a set / clear report: under --json the
// result as `data`, its archive warning and any sidecar warning lifted
// onto the envelope's `warnings`; as text one summary line, the warnings
// and the invalidated sidecars with their rebuild commands.
func writeCohortZoneResult(cmd *cli.Command, jsonOut bool, res *pulse.CohortZoneResult) error {
	if jsonOut {
		env := descriptor.NewEnvelope(res)
		for _, w := range res.Warnings {
			env.AddWarning(w.Code, w.Message, w.Details)
		}
		for _, w := range res.SidecarWarnings {
			env.AddWarning(string(w.Code), w.Message, w.Details)
		}
		return writeJSON(cmd.Writer, env)
	}
	switch {
	case !res.Changed && res.ZoneField == "":
		writeText(cmd.Writer, "%s declares no default zone field; nothing written\n", res.Cohort)
	case !res.Changed:
		writeText(cmd.Writer, "%s already declares %q as its default zone field; nothing written\n", res.Cohort, res.ZoneField)
	case res.ZoneField == "":
		writeText(cmd.Writer, "Cleared the default zone field %q of %s\n", res.PreviousZoneField, res.Cohort)
	default:
		writeText(cmd.Writer, "Declared %q as the default zone field of %s\n", res.ZoneField, res.Cohort)
	}
	for _, w := range res.Warnings {
		writeText(cmd.Writer, "  WARN   [%s] %s\n", w.Code, w.Message)
	}
	for _, w := range res.SidecarWarnings {
		writeText(cmd.Writer, "  WARN   [%s] %s\n", w.Code, w.Message)
	}
	if len(res.InvalidatedSidecars) > 0 {
		writeText(cmd.Writer, "Invalidated sidecars (not rebuilt — rebuilding is yours to schedule):\n")
		for _, sc := range res.InvalidatedSidecars {
			writeText(cmd.Writer, "  %s  [%s]\n", sc.Path, sc.Kind)
			writeText(cmd.Writer, "    %s\n", sc.Rebuild)
		}
	}
	return nil
}
