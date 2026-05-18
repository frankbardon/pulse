package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/frankbardon/pulse"
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
		Usage: "Filter a .pulse file (single-file or shard archive) to a new .pulse file",
		Description: "Filters records by either a filter expression (--filter), a file-backed " +
			"include-set (--include-from + --include-field), or both. When both are " +
			"supplied the predicates are AND-combined and the include-set is tested " +
			"first so per-row work short-circuits on misses without paying the " +
			"expression evaluation cost.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse file path", Required: true},
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output .pulse file path", Required: true},
			&cli.StringFlag{Name: "filter", Usage: "Filter expression (FILTER_EXPRESSION semantics)"},
			&cli.StringFlag{Name: "include-from", Usage: "Path to newline-delimited file of values to include"},
			&cli.StringFlag{Name: "include-field", Usage: "Field name whose value is tested against --include-from"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			output := cmd.String("output")
			filterExpr := cmd.String("filter")
			includeFrom := cmd.String("include-from")
			includeField := cmd.String("include-field")
			jsonOut := cmd.Bool("json")

			if filterExpr == "" && includeFrom == "" {
				err := fmt.Errorf("pulse cohort filter requires at least one of --filter or --include-from")
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_INPUT", err.Error())
				}
				return err
			}
			if (includeFrom == "") != (includeField == "") {
				err := fmt.Errorf("--include-from and --include-field must be supplied together")
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_INPUT", err.Error())
				}
				return err
			}

			p, err := newPulse()
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			if includeFrom == "" {
				return runFilterByExpr(ctx, cmd, p, input, output, filterExpr, jsonOut)
			}
			return runFilterByIncludeSet(ctx, cmd, p, input, output, includeFrom, includeField, filterExpr, jsonOut)
		},
	}
}

func runFilterByExpr(ctx context.Context, cmd *cli.Command, p *pulse.Pulse, input, output, filterExpr string, jsonOut bool) error {
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
}

func runFilterByIncludeSet(ctx context.Context, cmd *cli.Command, p *pulse.Pulse, input, output, includeFrom, includeField, filterExpr string, jsonOut bool) error {
	schema, err := p.ResolveCanonicalSchema(ctx, input)
	if err != nil {
		if jsonOut {
			return writeErrorEnvelope(cmd.Writer, "FILTER_ERROR", err.Error())
		}
		return err
	}

	f, err := os.Open(includeFrom)
	if err != nil {
		if jsonOut {
			return writeErrorEnvelope(cmd.Writer, "CLI_INPUT", err.Error())
		}
		return fmt.Errorf("opening --include-from %s: %w", includeFrom, err)
	}
	defer f.Close()

	load, err := pulse.LoadMemberSetFromReader(f, schema, includeField)
	if err != nil {
		if jsonOut {
			return writeErrorEnvelope(cmd.Writer, "FILTER_ERROR", err.Error())
		}
		return err
	}

	written, err := p.FilterToFileBySetAndExpr(ctx, input, output, includeField, load.Set, filterExpr)
	if err != nil {
		if jsonOut {
			return writeErrorEnvelope(cmd.Writer, "FILTER_ERROR", err.Error())
		}
		return err
	}

	if jsonOut {
		return writeEnvelope(cmd.Writer, map[string]any{
			"input":               input,
			"output":              output,
			"written_records":     written,
			"include_field":       includeField,
			"include_kind":        load.Set.Kind(),
			"include_members":     load.Set.Len(),
			"include_lines":       load.Lines,
			"include_not_in_dict": load.NotInDictionary,
			"include_invalid":     load.Invalid,
		})
	}

	writeText(cmd.Writer, "Loaded %d include values (%s) from %s; field=%q",
		load.Set.Len(), load.Set.Kind(), includeFrom, includeField)
	if load.NotInDictionary > 0 {
		writeText(cmd.Writer, "; %d not in dictionary (dropped)", load.NotInDictionary)
	}
	if load.Invalid > 0 {
		writeText(cmd.Writer, "; %d invalid (dropped)", load.Invalid)
	}
	writeText(cmd.Writer, "\nFiltered %d rows from %s to %s\n", written, input, output)
	return nil
}
