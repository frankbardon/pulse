package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/examples"
	cli "github.com/urfave/cli/v3"
)

// ExamplesCommand returns the examples command group. Two leaves:
//   - `pulse examples search [--query Q | WORDS...] [--intent ID] [--tag T] [--category C] [--json]`
//   - `pulse examples show NAME [--json]`
//
// Both wrap the embedded request-example library; search routes through
// the facade's Pulse.ExamplesSearchWith, so its matching tiers and the
// intent check are the library's, never the CLI's.
func ExamplesCommand() *cli.Command {
	return &cli.Command{
		Name:  "examples",
		Usage: "Search and fetch the built-in runnable request examples (embedder examples: pulse_examples_* / Pulse.ExampleGet)",
		Commands: []*cli.Command{
			examplesSearchCmd(),
			examplesShowCmd(),
		},
	}
}

func examplesSearchCmd() *cli.Command {
	return &cli.Command{
		Name:      "search",
		Usage:     "Search the embedded request-example library",
		ArgsUsage: "[WORDS...]",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "query", Usage: "Plain words (or pass them as arguments): one word is a case-insensitive substring of name, description or operators; several must each match a whole word of name, description, operators, intents or tags; operator aliases and intent phrasings, then near-miss spellings, match when nothing matches literally"},
			&cli.StringFlag{Name: "intent", Usage: "Intent-taxonomy ID the examples must declare (pulse skills show intents lists them); an unknown one is PULSE_RECOMMEND_INTENT_UNKNOWN"},
			&cli.StringSliceFlag{Name: "tag", Usage: "Filter by canonical taxonomy tag (repeatable; results must carry every tag)"},
			&cli.StringFlag{Name: "category", Usage: "Exact directory match: " + strings.Join(examples.AllCategories(), ", ")},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			query := cmd.String("query")
			if words := cmd.Args().Slice(); len(words) > 0 {
				if query != "" {
					return cliError(cmd, jsonOut, "CLI_INPUT", "usage: pulse examples search [--query Q | WORDS...]: pass the query once, as --query or as arguments")
				}
				query = strings.Join(words, " ")
			}
			p, err := newPulse(ctx)
			if err != nil {
				return cliErrorFrom(cmd, jsonOut, "CLI_ERROR", err)
			}
			hits, err := p.ExamplesSearchWith(pulse.ExamplesQuery{
				Query:    query,
				Tags:     cmd.StringSlice("tag"),
				Category: cmd.String("category"),
				Intent:   cmd.String("intent"),
			})
			if err != nil {
				// PULSE_RECOMMEND_INTENT_UNKNOWN keeps its own code.
				return cliCodedError(cmd, jsonOut, "EXAMPLES_ERROR", err)
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, hits)
			}
			if len(hits) == 0 {
				writeText(cmd.Writer, "(no matches)\n")
				return nil
			}
			for _, h := range hits {
				writeText(cmd.Writer, "%-40s  %-14s  %s\n", h.Name, h.Category, h.Description)
				if len(h.Tags) > 0 {
					writeText(cmd.Writer, "    tags: %s\n", strings.Join(h.Tags, ", "))
				}
				if len(h.Operators) > 0 {
					writeText(cmd.Writer, "    ops:  %s\n", strings.Join(h.Operators, ", "))
				}
			}
			return nil
		},
	}
}

func examplesShowCmd() *cli.Command {
	return &cli.Command{
		Name:      "show",
		Usage:     "Print the runnable request JSON for a named example",
		ArgsUsage: "NAME",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Wrap the example record in a JSON envelope (default prints just the runnable body)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 1 {
				return fmt.Errorf("usage: pulse examples show NAME")
			}
			name := args.First()
			ex, ok := examples.Get(name)
			if !ok {
				// Under --json the refusal is an envelope like every
				// other leaf's, so stdout always parses (exit 1).
				return cliError(cmd, cmd.Bool("json"), "CLI_INPUT", fmt.Sprintf("example %q not found", name))
			}
			if cmd.Bool("json") {
				return writeEnvelope(cmd.Writer, ex)
			}
			writeText(cmd.Writer, "%s\n", string(ex.Body))
			return nil
		},
	}
}
