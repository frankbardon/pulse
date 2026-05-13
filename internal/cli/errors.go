package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/errors"
	cli "github.com/urfave/cli/v3"
)

// ErrorsCommand returns the errors command group. Two leaves:
//   - `pulse errors lookup CODE [--json]`
//   - `pulse errors list [--domain D] [--query Q] [--json]`
//
// Both wrap the errors-package lookup surface. CODE detail (Message +
// Fixup hints) lives behind the lookup tool rather than the bootstrap
// manifest, so per-session context stays lean.
func ErrorsCommand() *cli.Command {
	return &cli.Command{
		Name:  "errors",
		Usage: "Look up Pulse error code metadata",
		Commands: []*cli.Command{
			errorsLookupCmd(),
			errorsListCmd(),
		},
	}
}

func errorsLookupCmd() *cli.Command {
	return &cli.Command{
		Name:      "lookup",
		Usage:     "Print metadata (Message + Fixups) for one error code",
		ArgsUsage: "CODE",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 1 {
				return fmt.Errorf("usage: pulse errors lookup CODE")
			}
			code := args.First()
			r, ok := errors.Lookup(code)
			if !ok {
				return fmt.Errorf("error code %q not found", code)
			}
			if cmd.Bool("json") {
				return writeEnvelope(cmd.Writer, r)
			}
			renderErrorDetail(cmd, r)
			return nil
		},
	}
}

func errorsListCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List error codes filtered by domain and/or substring query",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "domain", Usage: "Restrict to a single domain (CLI, DATA, ENCODING, PROCESSING, PULSE, SERVICE); case-insensitive"},
			&cli.StringFlag{Name: "query", Usage: "Case-insensitive substring search across descriptions and fixup hints"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			domain := cmd.String("domain")
			query := cmd.String("query")
			jsonOut := cmd.Bool("json")

			var results []errors.LookupResult
			switch {
			case domain != "" && query != "":
				// Intersect: every result of one filter that also matches the
				// other. Use the smaller axis as the candidate set.
				dom := errors.ByDomain(domain)
				idx := make(map[string]struct{}, len(dom))
				for _, r := range dom {
					idx[r.Code] = struct{}{}
				}
				for _, r := range errors.Search(query) {
					if _, ok := idx[r.Code]; ok {
						results = append(results, r)
					}
				}
			case domain != "":
				results = errors.ByDomain(domain)
			case query != "":
				results = errors.Search(query)
			default:
				// No filters: enumerate every code, alphabetical.
				for _, name := range errors.SortedCodeNames() {
					if r, ok := errors.Lookup(name); ok {
						results = append(results, r)
					}
				}
			}

			if results == nil {
				results = []errors.LookupResult{}
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, results)
			}
			if len(results) == 0 {
				writeText(cmd.Writer, "(no matches)\n")
				return nil
			}
			for _, r := range results {
				writeText(cmd.Writer, "%-45s  %-10s  %s\n", r.Code, r.Domain, oneLine(r.Message))
			}
			return nil
		},
	}
}

// renderErrorDetail prints the human-readable form of one code.
func renderErrorDetail(cmd *cli.Command, r errors.LookupResult) {
	writeText(cmd.Writer, "%s\n", r.Code)
	writeText(cmd.Writer, "  domain: %s\n", r.Domain)
	if r.Message != "" {
		writeText(cmd.Writer, "  message: %s\n", r.Message)
	}
	if r.FixupNotApplicable {
		writeText(cmd.Writer, "  fixup: not applicable (Pulse bug, not a user-fixable request)\n")
		return
	}
	if len(r.Fixups) == 0 {
		writeText(cmd.Writer, "  fixup: (none registered)\n")
		return
	}
	for i, fx := range r.Fixups {
		writeText(cmd.Writer, "  fixup[%d]:\n", i)
		writeText(cmd.Writer, "    action: %s\n", fx.Action)
		if len(fx.Path) > 0 {
			writeText(cmd.Writer, "    path:   %s\n", strings.Join(fx.Path, "."))
		}
		if fx.Hint != "" {
			writeText(cmd.Writer, "    hint:   %s\n", fx.Hint)
		}
		if len(fx.Examples) > 0 {
			parts := make([]string, 0, len(fx.Examples))
			for _, e := range fx.Examples {
				parts = append(parts, fmt.Sprintf("%v", e))
			}
			writeText(cmd.Writer, "    examples: %s\n", strings.Join(parts, ", "))
		}
	}
}

// oneLine collapses Message to a single line for the list view.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 100 {
		return s[:97] + "..."
	}
	return s
}
