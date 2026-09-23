package cli

import (
	"context"

	cli "github.com/urfave/cli/v3"
)

// WidenCommand returns the `pulse widen` leaf: widen one set column of a
// single-file cohort to a wider set rung, rewriting the cohort in place.
//
// It is its own top-level leaf rather than a mode of `pulse convert`
// because `.pulse` is reserved on the import side — a legal convert
// TARGET, never a convert SOURCE (io/format.SupportedImport) — so there
// is no `.pulse` → `.pulse` conversion for a widen to ride on. It is
// also not a `pulse cohort` subcommand for the same reason `pulse index`
// is not: this mutates the cohort file itself, and a destructive
// in-place rewrite earns a name a reader cannot mistake for a read.
//
// All of the behaviour is pulse.WidenSetField: layout dispatch, the
// refusal policy and the atomic temp/fsync/rename rewrite. This leaf
// parses three flags, calls it once, and formats the report.
func WidenCommand() *cli.Command {
	return &cli.Command{
		Name:      "widen",
		Usage:     "Widen a set column to a wider set rung, rewriting the cohort in place (DESTRUCTIVE, non-interactive — no confirmation prompt; the rewrite is atomic, so a failure leaves the cohort byte-identical)",
		ArgsUsage: "COHORT",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path (or pass as the positional argument)"},
			&cli.StringFlag{Name: "field", Aliases: []string{"f"}, Usage: "Name of the set column to widen", Required: true},
			&cli.StringFlag{Name: "to", Usage: "Target set type: set_u16, set_u32, set_u64, set_u128 or set_u256. Must be WIDER than the column's current rung", Required: true},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")

			input := cmd.String("input")
			if input == "" {
				input = cmd.Args().First()
			}
			if input == "" {
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"usage: pulse widen COHORT --field NAME --to TYPE (or --input COHORT --field ... --to ...)")
			}
			field := cmd.String("field")
			if field == "" {
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"pulse widen requires --field naming the set column to widen")
			}
			target := cmd.String("to")
			if target == "" {
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"pulse widen requires --to naming the target set type (e.g. set_u128)")
			}

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}

			rep, err := p.WidenSetField(ctx, input, field, target)
			if err != nil {
				// The refusals a user actually hits here — a narrower
				// target, a non-set column, a shard archive, a missing
				// field — all carry their own code. WIDEN_ERROR is only
				// ever reached by an UNCODED failure, so `pulse errors
				// lookup` works on everything this leaf can print.
				return cliCodedError(cmd, jsonOut, "WIDEN_ERROR", err)
			}

			out := widenOutput{
				Cohort:       input,
				Field:        rep.Field,
				From:         rep.From.String(),
				To:           rep.To.String(),
				Records:      rep.Records,
				StrideBefore: rep.StrideBefore,
				StrideAfter:  rep.StrideAfter,
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, out)
			}
			writeText(cmd.Writer, "Widened %s field %q: %s -> %s (%d record(s) rewritten, stride %d -> %d bytes)\n",
				out.Cohort, out.Field, out.From, out.To, out.Records, out.StrideBefore, out.StrideAfter)
			return nil
		},
	}
}

// widenOutput is the JSON-envelope shape for a successful `pulse widen`.
// It mirrors encoding.WidenReport with explicit snake_case tags (the
// report is an encoding-layer value type carrying no tags of its own).
//
// Both strides are reported, not just the delta: the stride pair is the
// only observable proof that every record was re-laid-out rather than
// the schema block alone being rewritten, which is the failure mode that
// produces a cohort that opens and decodes to garbage.
type widenOutput struct {
	Cohort       string `json:"cohort"`
	Field        string `json:"field"`
	From         string `json:"from"`
	To           string `json:"to"`
	Records      int64  `json:"records"`
	StrideBefore int    `json:"stride_before"`
	StrideAfter  int    `json:"stride_after"`
}
