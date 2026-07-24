package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	cli "github.com/urfave/cli/v3"
)

// IndexCommand returns the `pulse index` command group covering
// point-lookup sidecar index management: `build`, `list`, `verify`,
// `drop` — mirroring the `pulse shard` subcommand tree's leaf shape.
func IndexCommand() *cli.Command {
	return &cli.Command{
		Name:  "index",
		Usage: "Manage point-lookup sidecar indexes",
		Commands: []*cli.Command{
			indexBuildCmd(),
			indexListCmd(),
			indexVerifyCmd(),
			indexDropCmd(),
		},
	}
}

// indexBuildOutput is the JSON-envelope shape for a successful
// `pulse index build`. Keys is the ordered list of key columns (order
// is significant — see encoding.SidecarIndexPath); DistinctKeys and
// IndexedRecords summarize the built encoding.Index without requiring
// the caller to re-read the sidecar off disk.
type indexBuildOutput struct {
	Cohort         string   `json:"cohort"`
	IndexPath      string   `json:"index_path"`
	Keys           []string `json:"keys"`
	DistinctKeys   int      `json:"distinct_keys"`
	IndexedRecords int      `json:"indexed_records"`
}

func indexBuildCmd() *cli.Command {
	return &cli.Command{
		Name:      "build",
		Usage:     "Build a point-lookup sidecar index for one or more key columns",
		ArgsUsage: "COHORT",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path (or pass as the positional argument)"},
			&cli.StringFlag{Name: "key", Aliases: []string{"k"}, Usage: "Key column(s) to build the index over; comma-separated for a composite key (e.g. region,date)", Required: true},
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
					"usage: pulse index build COHORT --key col[,col...] (or --input COHORT --key ...)")
			}

			keyFields := splitIndexKeyFields(cmd.String("key"))
			if len(keyFields) == 0 {
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"pulse index build requires --key with at least one column")
			}

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}

			res, err := p.BuildIndex(ctx, input, keyFields)
			if err != nil {
				return indexCliError(cmd, jsonOut, err)
			}

			out := indexBuildOutput{
				Cohort:         input,
				IndexPath:      res.IndexPath,
				Keys:           keyFields,
				DistinctKeys:   countDistinctIndexKeys(res),
				IndexedRecords: countIndexedRecords(res),
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, out)
			}
			writeText(cmd.Writer, "Built index for %s (keys: %s) -> %s (%d distinct key(s), %d indexed record(s))\n",
				out.Cohort, strings.Join(out.Keys, ","), out.IndexPath, out.DistinctKeys, out.IndexedRecords)
			return nil
		},
	}
}

// indexListEntry is one sidecar's summary within indexListOutput.
// Mirrors pulse.IndexInfo's fields with explicit snake_case JSON tags
// (pulse.IndexInfo itself carries no tags — it is a service-layer
// value type, not part of the wire contract).
type indexListEntry struct {
	IndexPath      string   `json:"index_path"`
	Keys           []string `json:"keys"`
	DistinctKeys   int      `json:"distinct_keys"`
	IndexedRecords int      `json:"indexed_records"`
}

// indexListOutput is the JSON-envelope shape for a successful
// `pulse index list`. Indexes is always a non-nil (possibly empty)
// slice — a cohort with no sidecar indexes built yet is not an error.
type indexListOutput struct {
	Cohort  string           `json:"cohort"`
	Indexes []indexListEntry `json:"indexes"`
}

func indexListCmd() *cli.Command {
	return &cli.Command{
		Name:      "list",
		Usage:     "List every sidecar point-lookup index built for a cohort",
		ArgsUsage: "COHORT",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path (or pass as the positional argument)"},
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
					"usage: pulse index list COHORT (or --input COHORT)")
			}

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}

			indexes, err := p.ListIndexes(ctx, input)
			if err != nil {
				return indexCliError(cmd, jsonOut, err)
			}

			entries := make([]indexListEntry, len(indexes))
			for i, info := range indexes {
				entries[i] = indexListEntry{
					IndexPath:      info.IndexPath,
					Keys:           info.Keys,
					DistinctKeys:   info.DistinctKeys,
					IndexedRecords: info.IndexedRecords,
				}
			}
			out := indexListOutput{Cohort: input, Indexes: entries}

			if jsonOut {
				return writeEnvelope(cmd.Writer, out)
			}
			if len(entries) == 0 {
				writeText(cmd.Writer, "No sidecar indexes found for %s\n", input)
				return nil
			}
			writeText(cmd.Writer, "Sidecar indexes for %s:\n", input)
			for _, e := range entries {
				writeText(cmd.Writer, "  %s (keys: %s) -> %d distinct key(s), %d indexed record(s))\n",
					e.IndexPath, strings.Join(e.Keys, ","), e.DistinctKeys, e.IndexedRecords)
			}
			return nil
		},
	}
}

// indexVerifyOutput is the JSON-envelope shape for a successful
// `pulse index verify`. Reason is the string form of
// service.IndexFreshnessReason ("stat_mismatch" / "fingerprint_match" /
// "fingerprint_mismatch"); FastPath mirrors
// service.VerifyIndexResult.FastPath — true only when Reason ==
// "stat_mismatch" (the size+mtime comparison alone was conclusive).
type indexVerifyOutput struct {
	Cohort    string   `json:"cohort"`
	IndexPath string   `json:"index_path"`
	Keys      []string `json:"keys"`
	Fresh     bool     `json:"fresh"`
	Reason    string   `json:"reason"`
	FastPath  bool     `json:"fast_path"`
}

func indexVerifyCmd() *cli.Command {
	return &cli.Command{
		Name:      "verify",
		Usage:     "Report whether a cohort's sidecar point-lookup index is still fresh",
		ArgsUsage: "COHORT",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path (or pass as the positional argument)"},
			&cli.StringFlag{Name: "key", Aliases: []string{"k"}, Usage: "Key column(s) the index was built over; comma-separated for a composite key (e.g. region,date)", Required: true},
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
					"usage: pulse index verify COHORT --key col[,col...] (or --input COHORT --key ...)")
			}

			keyFields := splitIndexKeyFields(cmd.String("key"))
			if len(keyFields) == 0 {
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"pulse index verify requires --key with at least one column")
			}

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}

			res, err := p.VerifyIndex(ctx, input, keyFields)
			if err != nil {
				return indexCliError(cmd, jsonOut, err)
			}

			out := indexVerifyOutput{
				Cohort:    input,
				IndexPath: res.IndexPath,
				Keys:      keyFields,
				Fresh:     res.Fresh,
				Reason:    string(res.Reason),
				FastPath:  res.FastPath,
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, out)
			}
			freshness := "STALE"
			if out.Fresh {
				freshness = "FRESH"
			}
			writeText(cmd.Writer, "%s: %s (keys: %s) -> %s (reason: %s, fast_path: %v)\n",
				freshness, out.Cohort, strings.Join(out.Keys, ","), out.IndexPath, out.Reason, out.FastPath)
			return nil
		},
	}
}

// indexDropOutput is the JSON-envelope shape for a successful
// `pulse index drop`. Dropped is always true in the success case — a
// missing sidecar is a coded PULSE_INDEX_MISSING error, not a
// Dropped=false success, so a caller never has to distinguish "dropped"
// from "was already gone" from the JSON shape alone.
type indexDropOutput struct {
	Cohort    string   `json:"cohort"`
	IndexPath string   `json:"index_path"`
	Keys      []string `json:"keys"`
	Dropped   bool     `json:"dropped"`
}

func indexDropCmd() *cli.Command {
	return &cli.Command{
		Name:      "drop",
		Usage:     "Remove a cohort's sidecar point-lookup index (DESTRUCTIVE, non-interactive — no confirmation prompt; the sidecar is a cheap rebuild artifact, so `pulse index build` regenerates it)",
		ArgsUsage: "COHORT",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path (or pass as the positional argument)"},
			&cli.StringFlag{Name: "key", Aliases: []string{"k"}, Usage: "Key column(s) the index was built over; comma-separated for a composite key (e.g. region,date). Removes the sidecar immediately — no confirmation prompt.", Required: true},
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
					"usage: pulse index drop COHORT --key col[,col...] (or --input COHORT --key ...)")
			}

			keyFields := splitIndexKeyFields(cmd.String("key"))
			if len(keyFields) == 0 {
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"pulse index drop requires --key with at least one column")
			}

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}

			indexPath := encoding.SidecarIndexPath(input, keyFields)
			if err := p.DropIndex(ctx, input, keyFields); err != nil {
				return indexCliError(cmd, jsonOut, err)
			}

			out := indexDropOutput{
				Cohort:    input,
				IndexPath: indexPath,
				Keys:      keyFields,
				Dropped:   true,
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, out)
			}
			writeText(cmd.Writer, "Dropped index for %s (keys: %s) -> %s\n",
				out.Cohort, strings.Join(out.Keys, ","), out.IndexPath)
			return nil
		},
	}
}

// splitIndexKeyFields splits a --key flag value on commas, trims
// surrounding whitespace from each column name, and drops any empty
// entries (a trailing comma or repeated commas never silently produce
// a bogus empty-string key column).
func splitIndexKeyFields(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// countDistinctIndexKeys sums the number of key entries across every
// hash bucket in the built index — the count of distinct key values
// observed during the build scan (not the bucket-table size, which is
// sized to the same number but is an implementation detail).
func countDistinctIndexKeys(res *pulse.BuildIndexResult) int {
	if res == nil || res.Index == nil {
		return 0
	}
	n := 0
	for _, b := range res.Index.Buckets {
		n += len(b.Entries)
	}
	return n
}

// countIndexedRecords sums every entry's RowIDs across every bucket —
// the total number of source records the index actually covers (rows
// skipped for a null key component are not counted; see
// service.Service.BuildIndex's null-key-skip contract).
func countIndexedRecords(res *pulse.BuildIndexResult) int {
	if res == nil || res.Index == nil {
		return 0
	}
	n := 0
	for _, b := range res.Index.Buckets {
		for _, e := range b.Entries {
			n += len(e.RowIDs)
		}
	}
	return n
}

// indexCliError surfaces a facade/service error from any `pulse index`
// leaf (build, list, verify, drop). In non-JSON mode the error is
// returned as a plain "CODE: message" error (matching cliError's
// non-JSON shape) so the coded error's real code is still visible on
// stderr. In JSON mode the error is unwrapped to its underlying
// *errors.CodedError (if any) so the envelope's error code round-trips
// the real coded error (e.g. PULSE_INDEX_UNSUPPORTED_SHARDED,
// PULSE_INDEX_MISSING, PROCESSING_CONFIG) rather than a generic wrapper
// string — the CLI never masks a coded error behind a made-up code.
func indexCliError(cmd *cli.Command, jsonOut bool, err error) error {
	code, message, details := indexErrorParts(err)
	if !jsonOut {
		return fmt.Errorf("%s: %s", code, message)
	}
	env := descriptor.NewEnvelope(nil)
	env.AddError(code, message, details)
	return writeJSON(cmd.Writer, env)
}

// indexErrorParts extracts the {code, message, details} triple from
// err's *errors.CodedError, if the error chain carries one, or falls
// back to a generic CLI_ERROR code wrapping err's plain message.
func indexErrorParts(err error) (code, message string, details map[string]any) {
	if ce := asIndexCodedError(err); ce != nil {
		return string(ce.Code), ce.Message, ce.Details
	}
	return "CLI_ERROR", err.Error(), nil
}

// asIndexCodedError walks err's Unwrap chain looking for an
// *errors.CodedError. A local pointer-cast walk (mirroring
// service.asCodedError) avoids importing the stdlib "errors" package
// just for this one errors.As call, which would collide with this
// file's github.com/frankbardon/pulse/errors import name.
func asIndexCodedError(err error) *errors.CodedError {
	for cur := err; cur != nil; {
		if ce, ok := cur.(*errors.CodedError); ok {
			return ce
		}
		u, ok := cur.(interface{ Unwrap() error })
		if !ok {
			return nil
		}
		cur = u.Unwrap()
	}
	return nil
}
