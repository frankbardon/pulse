package cli

import (
	"context"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
	cli "github.com/urfave/cli/v3"
)

// RecommendCommand returns the `pulse recommend` leaf: an intent ID
// becomes ranked draft requests — placeholder skeletons without
// --cohort, predict-validated drafts bound to the cohort's fields with
// it.
//
// All behaviour is pulse.Recommend; this leaf parses flags, calls it
// once and formats the result.
func RecommendCommand() *cli.Command {
	return &cli.Command{
		Name:  "recommend",
		Usage: "Turn a question kind (an intent ID) into ranked draft requests: placeholder skeletons, or predict-validated drafts bound to --cohort's fields",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "intent", Usage: "Intent ID naming the kind of question (pulse skills show intents lists them); required"},
			&cli.StringFlag{Name: "cohort", Usage: "A .pulse cohort to bind the drafts to; each bound draft has passed predict. Omit for unbound placeholder skeletons"},
			&cli.StringSliceFlag{Name: "field", Usage: "A field hint (needs --cohort): pins the field to the first role of the intent it fits. Repeatable"},
			&cli.StringFlag{Name: "level", Usage: "Rank operators at this level first: basic, intermediate or advanced"},
			&cli.IntFlag{Name: "limit", Usage: "Maximum recommendations returned (0 = the default, 10)"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			req := descriptor.RecommendRequest{
				Intent: cmd.String("intent"),
				Fields: cmd.StringSlice("field"),
				Level:  descriptor.Level(cmd.String("level")),
				Limit:  int(cmd.Int("limit")),
			}
			if req.Intent == "" {
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"usage: pulse recommend --intent ID [--cohort COHORT [--field F ...]] [--level LEVEL] [--limit N]")
			}
			if c := cmd.String("cohort"); c != "" {
				req.Cohort = &types.Cohort{Filename: c}
			}

			p, err := newPulse(ctx)
			if err != nil {
				return cliErrorFrom(cmd, jsonOut, "CLI_ERROR", err)
			}
			res, err := p.Recommend(ctx, req)
			if err != nil {
				// Every refusal carries its own code
				// (PULSE_RECOMMEND_INTENT_UNKNOWN, SERVICE_VALIDATION,
				// DATA_FILE, ENCODING_INVALID); RECOMMEND_ERROR is reached
				// only by an uncoded error.
				return cliCodedError(cmd, jsonOut, "RECOMMEND_ERROR", err)
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, res)
			}
			writeRecommendText(cmd, res)
			return nil
		},
	}
}

// writeRecommendText is the human-readable form of a recommend result.
func writeRecommendText(cmd *cli.Command, res *descriptor.RecommendResult) {
	w := cmd.Writer
	mode := "unbound"
	if res.Bound {
		mode = "bound"
	}
	writeText(w, "Intent %s (%s): %d recommendation(s)", res.Intent, mode, len(res.Recommendations))
	if res.Truncated {
		writeText(w, " of %d considered", res.CandidatesConsidered)
	}
	writeText(w, "\n")
	for i, r := range res.Recommendations {
		state := "draft"
		if r.Bound {
			state = "ready"
		}
		writeText(w, "\n%d. %s [%s, %s, %s]\n   %s\n   request: %s\n", i+1, r.Operator, r.Category, r.Level, state, r.Why, string(r.Request))
		if len(r.Placeholders) > 0 {
			writeText(w, "   placeholders: %s\n", strings.Join(r.Placeholders, ", "))
		}
		for _, n := range r.Needs {
			writeText(w, "   needs %s: %s\n", n.Param, n.Why)
		}
		for _, a := range r.Advisories {
			writeText(w, "   advisory %s: %s\n", a.Code, a.Message)
		}
	}
	if len(res.RoutesTo) > 0 {
		writeText(w, "\nNo operator draft answers this; use instead:\n")
		for _, a := range res.RoutesTo {
			writeText(w, "  %s — when %s\n", a.Use, a.When)
		}
	}
}
