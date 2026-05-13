package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/examples"
	cli "github.com/urfave/cli/v3"
)

// ExamplesCommand returns the examples command group. Two leaves:
//   - `pulse examples search [--query Q] [--tag T] [--category C] [--json]`
//   - `pulse examples show NAME [--json]`
//
// Both wrap the embedded request-example library (examples package).
func ExamplesCommand() *cli.Command {
	return &cli.Command{
		Name:  "examples",
		Usage: "Search and fetch runnable Pulse request examples",
		Commands: []*cli.Command{
			examplesSearchCmd(),
			examplesShowCmd(),
		},
	}
}

func examplesSearchCmd() *cli.Command {
	return &cli.Command{
		Name:  "search",
		Usage: "Search the embedded request-example library",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "query", Usage: "Case-insensitive substring match across name, description, and operators"},
			&cli.StringSliceFlag{Name: "tag", Usage: "Filter by canonical taxonomy tag (repeatable; results must carry every tag)"},
			&cli.StringFlag{Name: "category", Usage: "Exact directory match: aggregations, attributes, features, filterers, groupers, tests, windows"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			query := cmd.String("query")
			tags := cmd.StringSlice("tag")
			category := cmd.String("category")
			hits := examples.Search(query, tags, category)

			if cmd.Bool("json") {
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
				return fmt.Errorf("example %q not found", name)
			}
			if cmd.Bool("json") {
				return writeEnvelope(cmd.Writer, ex)
			}
			writeText(cmd.Writer, "%s\n", string(ex.Body))
			return nil
		},
	}
}
