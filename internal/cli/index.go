package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	cli "github.com/urfave/cli/v3"
)

// IndexCommand returns the `pulse index` command group covering
// point-lookup sidecar index management. First (and currently only)
// leaf is `build`; `list` / `verify` / `drop` land in a follow-up
// story (E4-S2) mirroring the `pulse shard` subcommand tree.
func IndexCommand() *cli.Command {
	return &cli.Command{
		Name:  "index",
		Usage: "Manage point-lookup sidecar indexes",
		Commands: []*cli.Command{
			indexBuildCmd(),
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
				return indexBuildCliError(cmd, jsonOut, err)
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

// indexBuildCliError surfaces a facade/service error from BuildIndex.
// In non-JSON mode the error is returned as a plain "CODE: message"
// error (matching cliError's non-JSON shape) so the coded error's real
// code is still visible on stderr. In JSON mode the error is unwrapped
// to its underlying *errors.CodedError (if any) so the envelope's
// error code round-trips the real coded error (e.g.
// PULSE_INDEX_UNSUPPORTED_SHARDED, PROCESSING_CONFIG) rather than a
// generic wrapper string — the CLI never masks a coded error behind a
// made-up code.
func indexBuildCliError(cmd *cli.Command, jsonOut bool, err error) error {
	code, message, details := indexBuildErrorParts(err)
	if !jsonOut {
		return fmt.Errorf("%s: %s", code, message)
	}
	env := descriptor.NewEnvelope(nil)
	env.AddError(code, message, details)
	return writeJSON(cmd.Writer, env)
}

// indexBuildErrorParts extracts the {code, message, details} triple
// from err's *errors.CodedError, if the error chain carries one, or
// falls back to a generic CLI_ERROR code wrapping err's plain message.
func indexBuildErrorParts(err error) (code, message string, details map[string]any) {
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
