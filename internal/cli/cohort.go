package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/frankbardon/pulse/descriptor"
	cli "github.com/urfave/cli/v3"
)

// CohortCommand returns the cohort command group.
func CohortCommand() *cli.Command {
	return &cli.Command{
		Name:  "cohort",
		Usage: "Inspect and filter .pulse cohort files",
		Commands: []*cli.Command{
			cohortInspectCmd(),
			cohortFilterCmd(),
		},
	}
}

func cohortInspectCmd() *cli.Command {
	return &cli.Command{
		Name:      "inspect",
		Usage:     "Inspect a .pulse file header and schema",
		ArgsUsage: "PATH",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
			&cli.BoolFlag{Name: "full-dict", Usage: "Show full categorical dictionaries without truncation"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 1 {
				return fmt.Errorf("usage: pulse cohort inspect PATH")
			}
			path := args.First()
			jsonOut := cmd.Bool("json")
			fullDict := cmd.Bool("full-dict")

			if fullDict || jsonOut {
				// Use descriptor directly for full-dict / envelope support.
				data, err := os.ReadFile(path)
				if err != nil {
					if jsonOut {
						return writeErrorEnvelope(cmd.Writer, "INSPECT_ERROR", err.Error())
					}
					return err
				}
				opts := &descriptor.InspectOptions{FullDict: fullDict}
				env := descriptor.InspectFromBytes(data, opts)
				if len(env.Errors) > 0 && !jsonOut {
					return fmt.Errorf("%s", env.Errors[0].Message)
				}
				if jsonOut {
					return writeJSON(cmd.Writer, env)
				}
				result, ok := env.Data.(*descriptor.InspectResult)
				if !ok {
					return fmt.Errorf("unexpected inspect result type")
				}
				printInspectResult(cmd, result)
				return nil
			}

			p, err := newPulse()
			if err != nil {
				return err
			}

			result, err := p.Inspect(ctx, path)
			if err != nil {
				return err
			}

			printInspectResult(cmd, result)
			return nil
		},
	}
}

func printInspectResult(cmd *cli.Command, result *descriptor.InspectResult) {
	writeText(cmd.Writer, "Fields: %d\n", result.FieldCount)
	for _, f := range result.Fields {
		writeText(cmd.Writer, "  %-30s %-20s %s\n", f.Name, f.Type, f.Description)
		if f.Dictionary != nil {
			writeText(cmd.Writer, "    dictionary: %d entries", f.Dictionary.TotalEntries)
			if f.Dictionary.Truncated {
				writeText(cmd.Writer, " (truncated)")
			}
			writeText(cmd.Writer, "\n")
		}
	}
}

func cohortFilterCmd() *cli.Command {
	return &cli.Command{
		Name:  "filter",
		Usage: "Filter a .pulse file to a new .pulse file",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse file path", Required: true},
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output .pulse file path", Required: true},
			&cli.StringFlag{Name: "filter", Usage: "Filter expression", Required: true},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			output := cmd.String("output")
			filterExpr := cmd.String("filter")
			jsonOut := cmd.Bool("json")

			p, err := newPulse()
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			written, err := p.FilterToFile(ctx, input, output, filterExpr)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "FILTER_ERROR", err.Error())
				}
				return err
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, map[string]any{
					"input":           input,
					"output":          output,
					"written_records": written,
				})
			}

			writeText(cmd.Writer, "Filtered %d rows from %s to %s\n", written, input, output)
			return nil
		},
	}
}
