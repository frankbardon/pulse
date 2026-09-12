package cli

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/spf13/afero"
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

			p, err := newPulse()
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "INSPECT_ERROR", err)
				}
				return err
			}

			// Every mode reads through the facade. The --json / --full-dict
			// branch used to slurp the path itself with os.ReadFile, which
			// bypassed the injected afero.Fs and — the visible symptom —
			// never resolved the archive.pulse#shard.pulse anchor, so an
			// anchor that inspected fine as text returned data:null under
			// --json.
			env, err := p.InspectEnvelope(ctx, path, &descriptor.InspectOptions{FullDict: fullDict})
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "INSPECT_ERROR", err)
				}
				return err
			}
			if jsonOut {
				return writeJSON(cmd.Writer, env)
			}
			if len(env.Errors) > 0 {
				return fmt.Errorf("%s", env.Errors[0].Message)
			}
			result, ok := env.Data.(*descriptor.InspectResult)
			if !ok {
				return fmt.Errorf("unexpected inspect result type")
			}
			printInspectResult(cmd, result)
			return nil
		},
	}
}

// printInspectResult renders the text mode of `pulse cohort inspect`.
//
// Records is printed FIRST because it is the figure a reader is usually
// after and it was invisible here until now — reachable only through
// --json, the library or the MCP tool. The number is whatever
// descriptor.Inspect derived, which is already the right one for the
// shape being inspected: the aggregate across shards for an archive,
// that shard's own count for an archive.pulse#shard.pulse anchor
// (the anchor resolves to the shard's standalone bytes before inspect
// ever sees them), and payload_bytes / record_stride for a single file.
// A per-shard breakdown follows for an archive so the aggregate is not
// the only thing on offer; single-file cohorts carry an empty Shards
// slice and print no breakdown.
func printInspectResult(cmd *cli.Command, result *descriptor.InspectResult) {
	writeText(cmd.Writer, "Records: %d\n", result.RecordCount)
	if len(result.Shards) > 0 {
		writeText(cmd.Writer, "Shards: %d\n", len(result.Shards))
		for _, sh := range result.Shards {
			writeText(cmd.Writer, "  %-30s %d records\n", sh.Filename, sh.RecordCount)
		}
	}
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

	// The include-set is a SIDE FILE, not a cohort: an arbitrary
	// newline-delimited path the caller names on the command line,
	// resolved against the process working directory. It goes through an
	// explicitly-constructed afero.NewOsFs() rather than `os` directly —
	// the convention internal/cli/synth.go already uses for its own side
	// files (--profile, --rules, --emit-spec) — so the repo rule "do not
	// bypass afero.Fs" holds literally and the site is greppable with
	// every other filesystem reach in the CLI.
	//
	// It is deliberately NOT the facade's injected fs. That one is rooted
	// at PULSE_DATA_DIR when the env var is set, so routing a
	// cwd-relative side-file path through it would silently resolve it
	// inside the data directory. The cohort at --input goes through the
	// facade; this does not, and the two are different kinds of path.
	f, err := afero.NewOsFs().Open(includeFrom)
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
