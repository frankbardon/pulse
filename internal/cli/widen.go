package cli

import (
	"context"
	stderrors "errors"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
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

			// Sidecar hint. A widen changes the cohort's byte LENGTH,
			// so the point-lookup index and the SPSS metadata sidecar
			// beside it both self-invalidate through their own size
			// fingerprints — neither can serve stale data. What was
			// missing is that nothing SAID so, and a corpus that quietly
			// stops answering lookups is a bad way to find out.
			//
			// Reporting only, never rebuilding: an automatic rebuild
			// would attach unbounded work to a bounded operation, with
			// no way for the caller to decline it.
			//
			// A discovery failure is reported as an envelope WARNING
			// rather than an error, because the widen has already
			// succeeded and its exit code must say so. Swallowing it
			// would be the silence this whole hint exists to end.
			sidecars, sidecarErr := p.InvalidatedSidecars(ctx, input)
			out.InvalidatedSidecars = sidecars

			if jsonOut {
				env := descriptor.NewEnvelope(out)
				if sidecarErr != nil {
					addSidecarScanWarning(env, input, sidecarErr)
				}
				return writeJSON(cmd.Writer, env)
			}
			writeText(cmd.Writer, "Widened %s field %q: %s -> %s (%d record(s) rewritten, stride %d -> %d bytes)\n",
				out.Cohort, out.Field, out.From, out.To, out.Records, out.StrideBefore, out.StrideAfter)
			if sidecarErr != nil {
				writeText(cmd.Writer, "  WARN   could not enumerate sidecars beside %s: %v\n", input, sidecarErr)
			}
			if len(sidecars) > 0 {
				writeText(cmd.Writer, "Invalidated sidecars (not rebuilt — rebuilding is yours to schedule):\n")
				for _, sc := range sidecars {
					writeText(cmd.Writer, "  %s  [%s]\n", sc.Path, sc.Kind)
					writeText(cmd.Writer, "    %s\n", sc.Rebuild)
				}
			}
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
	// InvalidatedSidecars names the sidecar documents beside the cohort
	// that the rewrite invalidated, each with the command that rebuilds
	// it. Structured rather than prose because a consumer must not have
	// to parse a sentence to learn which file to rebuild.
	//
	// omitempty, and the text arm prints nothing in the same case: a
	// cohort with no sidecars is the overwhelmingly common one, and an
	// empty section there is pure noise.
	InvalidatedSidecars []pulse.StaleSidecar `json:"invalidated_sidecars,omitempty"`
}

// addSidecarScanWarning records a failed sidecar enumeration on the
// envelope's warnings array, preserving the coded error's own code when
// it carries one so `pulse errors lookup` still works on it.
func addSidecarScanWarning(env *descriptor.Envelope, cohort string, err error) {
	code := "WIDEN_SIDECAR_SCAN"
	var coded *errors.CodedError
	if stderrors.As(err, &coded) {
		code = string(coded.Code)
	}
	env.AddWarning(code,
		"the widen succeeded but the sidecars beside the cohort could not be enumerated; "+
			"any point-lookup index or SPSS metadata sidecar here is now stale and must be rebuilt manually",
		map[string]any{"cohort": cohort, "error": err.Error()})
}
