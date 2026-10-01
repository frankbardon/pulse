package cli

import (
	"context"
	"strings"

	"github.com/frankbardon/pulse"
	encx "github.com/frankbardon/pulse/internal/encoding"
	pio "github.com/frankbardon/pulse/io"
	cli "github.com/urfave/cli/v3"
)

// DedupCommand returns the `pulse dedup` leaf: retro-dedup an EXISTING
// single-file cohort into a grouped (format 0x02) cohort — the
// existing-cohort twin of `pulse import <fmt> --group`.
//
// It is its own top-level leaf, beside `pulse widen`, for the reasons
// widen is: `.pulse` is never a convert SOURCE, so there is no
// `.pulse` → `.pulse` conversion for it to be a mode of, and an in-place
// rewrite earns a name a reader cannot mistake for a read. It is not an
// import mode either — import reads a tabular SOURCE, and the point of
// retro-dedup is that the source may be gone.
//
// All behaviour is pulse.Dedup; this leaf parses flags, calls it once
// and formats the report.
func DedupCommand() *cli.Command {
	return &cli.Command{
		Name:      "dedup",
		Usage:     "Deduplicate an existing cohort's repeated parent blocks into parent groups (format 0x02), in place or to --out (DESTRUCTIVE in place, non-interactive; the rewrite is atomic, so a failure or refusal leaves the cohort byte-identical)",
		ArgsUsage: "COHORT",
		// A --group value carries its own commas (KEY,KEY:MEMBER,MEMBER):
		// one flag is one group, never split on ','.
		DisableSliceFlagSeparator: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path (or pass as the positional argument)"},
			&cli.StringSliceFlag{Name: "group", Usage: "Declare a parent group: KEY[,KEY...]:MEMBER[,MEMBER...] stores each distinct tuple once and refuses a member that varies within its key; MEMBER[,MEMBER...] is a plain tuple group. Repeatable, one group per flag. An already-grouped cohort is regrouped from scratch"},
			&cli.StringFlag{Name: "out", Aliases: []string{"o"}, Usage: "Write the deduplicated cohort to this NEW path (must not exist) and leave the input untouched; default rewrites the input in place"},
			&cli.BoolFlag{Name: "elide-constants", Usage: "Also store fields holding one value on every record once in the schema block"},
			&cli.FloatFlag{Name: "dedup-ratio-floor", Value: encx.DefaultDedupRatioFloor, Usage: "Rows per distinct tuple below which a --group draws a PULSE_DEDUP_LOW_RATIO warning (the group is still written); 1 leaves only the grows-the-file check"},
			&cli.BoolFlag{Name: "strict", Usage: "Treat parent-group viability warnings (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO) as errors: nothing is written"},
			&cli.BoolFlag{Name: "suggest-groups", Usage: "Detect candidate parent groups over the cohort's records and print each measured candidate with a ready-to-paste --group value. Alone it writes nothing"},
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
					"usage: pulse dedup COHORT --group KEY:MEMBER[,MEMBER...] [--out PATH] (or --suggest-groups to find groups)")
			}
			opts := pulse.DedupOptions{
				Out:            cmd.String("out"),
				ElideConstants: cmd.Bool("elide-constants"),
				RatioFloor:     cmd.Float("dedup-ratio-floor"),
				Strict:         cmd.Bool("strict"),
				SuggestGroups:  cmd.Bool("suggest-groups"),
			}
			for _, decl := range cmd.StringSlice("group") {
				g, err := pio.ParseGroupDecl(decl)
				if err != nil {
					return cliCodedError(cmd, jsonOut, "CLI_INPUT", err)
				}
				opts.Groups = append(opts.Groups, g)
			}
			if len(opts.Groups) == 0 && !opts.ElideConstants && !opts.SuggestGroups {
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"pulse dedup needs at least one --group, --elide-constants or --suggest-groups")
			}

			p, err := newPulse()
			if err != nil {
				return cliErrorFrom(cmd, jsonOut, "CLI_ERROR", err)
			}
			res, err := p.Dedup(ctx, input, opts)
			if err != nil {
				// Every refusal a user hits carries its own code (the
				// PULSE_GROUP_* family, the gate's codes under --strict,
				// SERVICE_VALIDATION for a shard archive or an existing
				// --out). DEDUP_ERROR is reached only by an uncoded error.
				return cliCodedError(cmd, jsonOut, "DEDUP_ERROR", err)
			}
			if jsonOut {
				return writeEnvelopeWithWarnings(cmd.Writer, res, res.Warnings())
			}
			writeDedupText(cmd, res)
			return nil
		},
	}
}

// writeDedupText is the human-readable report of a dedup run.
func writeDedupText(cmd *cli.Command, res *pulse.DedupResult) {
	w := cmd.Writer
	switch {
	case res.Rewritten:
		where := "in place"
		if !res.InPlace {
			where = "to " + res.Target
		}
		writeText(w, "Deduplicated %s %s: %d record(s), format 0x%02x -> 0x%02x, %d -> %d bytes, stride %d -> %d bytes\n",
			res.Source, where, res.Records, res.FormatVersionBefore, res.FormatVersionAfter,
			res.BytesBefore, res.BytesAfter, res.StrideBefore, res.StrideAfter)
	case len(res.Groups) > 0 || len(res.ElidedConstants) > 0:
		writeText(w, "Nothing written: no declared group survived the viability gate and nothing was elided; %s is unchanged\n", res.Source)
	}
	writeGroupReports(w, res.Groups)
	if len(res.ElidedConstants) > 0 {
		writeText(w, "Elided constant fields: %s\n", strings.Join(res.ElidedConstants, ", "))
	}
	writeSourceWarnings(w, res.Warnings())
	writeGroupCandidates(w, res.GroupCandidates)
	if len(res.InvalidatedSidecars) > 0 {
		writeText(w, "Invalidated sidecars (not rebuilt — rebuilding is yours to schedule):\n")
		for _, sc := range res.InvalidatedSidecars {
			writeText(w, "  %s  [%s]\n", sc.Path, sc.Kind)
			writeText(w, "    %s\n", sc.Rebuild)
		}
	}
}
