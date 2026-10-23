package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/frankbardon/pulse"
	cli "github.com/urfave/cli/v3"
)

// RakeCommand returns the `pulse rake` leaf: compute survey weights
// (rim weighting, or cell weighting for a joint margin) for a cohort and
// write them, beside every source column, into a NEW cohort.
//
// It is its own top-level leaf, beside `pulse dedup` and `pulse widen`,
// because it writes a cohort rather than answering a request: there is
// no Request slot for it to be a mode of (a weight computed inside a
// request could never weight that request — a weight names a SCHEMA
// field).
//
// All behaviour is pulse.Rake; this leaf parses flags into one
// pulse.RakeRequest, calls it once and formats the report. The targets
// are inline JSON — `--margins` (or a whole `--request`) takes a JSON
// literal or the path of a file holding it.
func RakeCommand() *cli.Command {
	return &cli.Command{
		Name:      "rake",
		Usage:     "Compute survey weights — rim weighting (raking) to one or more target margins, cell weighting when a margin joins several fields — and write them as a nullable f64 column of a NEW cohort beside every source column (atomic; the output weights are probability weights)",
		ArgsUsage: "COHORT",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Source .pulse cohort (single file, shard archive or archive#shard; or pass as the positional argument)"},
			&cli.StringFlag{Name: "out", Aliases: []string{"o"}, Usage: "Path of the NEW single-file .pulse cohort to write"},
			&cli.StringFlag{Name: "margins", Aliases: []string{"m"}, Usage: `Target margins: a JSON array literal or the path of a file holding one, e.g. [{"field":"gender","targets":{"F":0.51,"M":0.49}},{"fields":["age","region"],"cells":[{"values":["18-34","NE"],"target":0.2}, ...]}]; each margin may set "targets_as": "shares" (default; sum 1 or 100) or "counts"`},
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "A whole rake request (cohort, out, margins, weight_field, by, base_weight, missing, min_weight, max_weight, max_iter, tol, total, overwrite): a JSON object literal or the path of a file holding one. Every flag set on the command line overrides its key"},
			&cli.StringFlag{Name: "weight-field", Usage: "Name of the output weight column (default rake_weight); a name the source already carries needs --overwrite, which replaces that column in place"},
			&cli.StringFlag{Name: "by", Usage: "Rake every group of this field (e.g. a wave) separately to the same targets"},
			&cli.StringFlag{Name: "base-weight", Usage: "Start from this design-weight field: the output is it times the raking adjustment"},
			&cli.StringFlag{Name: "missing", Usage: "Null margin value policy, one of " + orList(pulse.RakeMissingPolicies()) + ": " + pulse.RakeMissingRefuse +
				" (default) refuses the record with PULSE_RAKE_MISSING_VALUE; " + pulse.RakeMissingBase + " keeps the record's factor for that margin"},
			&cli.FloatFlag{Name: "min-weight", Usage: "Lower cap on every weight, relative to its group's mean weight (0 = none), applied after every sweep"},
			&cli.FloatFlag{Name: "max-weight", Usage: "Upper cap on every weight, relative to its group's mean weight (0 = none), applied after every sweep"},
			&cli.IntFlag{Name: "max-iter", Usage: "Sweep ceiling (default 100); a run that stops there still writes, with a PULSE_RAKE_NOT_CONVERGED warning"},
			&cli.FloatFlag{Name: "tol", Usage: "Convergence tolerance on the largest achieved-vs-target share gap (default 1e-6)"},
			&cli.FloatFlag{Name: "total", Usage: "Normalise every group's weights to sum to this value (default: the group's record count, mean weight 1)"},
			&cli.BoolFlag{Name: "overwrite", Usage: "Replace an existing cohort at --out (atomic; its now-stale sidecars are listed, never rebuilt) and an existing source column named --weight-field"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			req, err := rakeRequestFromFlags(cmd)
			if err != nil {
				return cliCodedError(cmd, jsonOut, "CLI_INPUT", err)
			}
			p, err := newPulse(ctx)
			if err != nil {
				return cliCodedError(cmd, jsonOut, "CLI_ERROR", err)
			}
			res, err := p.Rake(ctx, req)
			if err != nil {
				// Every refusal carries its own code (the PULSE_RAKE_*
				// family, SERVICE_VALIDATION for an existing or anchored
				// --out). RAKE_ERROR is reached only by an uncoded error.
				return cliCodedError(cmd, jsonOut, "RAKE_ERROR", err)
			}
			if jsonOut {
				return writeEnvelopeWithWarnings(cmd.Writer, res, res.Warnings)
			}
			writeRakeText(cmd, res)
			return nil
		},
	}
}

// rakeRequestFromFlags assembles the one pulse.RakeRequest the leaf runs:
// --request (when given) first, then every flag set on the command line
// over it. It parses only; every judgement on the request is pulse.Rake's.
func rakeRequestFromFlags(cmd *cli.Command) (pulse.RakeRequest, error) {
	var req pulse.RakeRequest
	if v := cmd.String("request"); v != "" {
		data, err := jsonLiteralOrFile(v, '{')
		if err != nil {
			return req, err
		}
		if err := decodeRequestJSON(data, &req, "rake request"); err != nil {
			return req, err
		}
	}
	if v := cmd.String("margins"); v != "" {
		data, err := jsonLiteralOrFile(v, '[')
		if err != nil {
			return req, err
		}
		var margins []pulse.RakeMargin
		if err := decodeRequestJSON(data, &margins, "rake margins"); err != nil {
			return req, err
		}
		req.Margins = margins
	}
	input := cmd.String("input")
	if input == "" {
		input = cmd.Args().First()
	}
	if input != "" {
		req.Cohort = input
	}
	if cmd.IsSet("out") {
		req.Out = cmd.String("out")
	}
	if cmd.IsSet("weight-field") {
		req.WeightField = cmd.String("weight-field")
	}
	if cmd.IsSet("by") {
		req.By = cmd.String("by")
	}
	if cmd.IsSet("base-weight") {
		req.BaseWeight = cmd.String("base-weight")
	}
	if cmd.IsSet("missing") {
		req.Missing = cmd.String("missing")
	}
	if cmd.IsSet("min-weight") {
		req.MinWeight = cmd.Float("min-weight")
	}
	if cmd.IsSet("max-weight") {
		req.MaxWeight = cmd.Float("max-weight")
	}
	if cmd.IsSet("max-iter") {
		req.MaxIter = cmd.Int("max-iter")
	}
	if cmd.IsSet("tol") {
		req.Tol = cmd.Float("tol")
	}
	if cmd.IsSet("total") {
		req.Total = cmd.Float("total")
	}
	if cmd.IsSet("overwrite") {
		req.Overwrite = cmd.Bool("overwrite")
	}
	return req, nil
}

// jsonLiteralOrFile returns v's bytes when v is a JSON literal (its first
// non-space byte is open — '{' for an object, '[' for an array), else the
// contents of the file v names.
func jsonLiteralOrFile(v string, open byte) ([]byte, error) {
	if t := strings.TrimSpace(v); t != "" && t[0] == open {
		return []byte(t), nil
	}
	data, err := os.ReadFile(v)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", v, err)
	}
	return data, nil
}

// writeRakeText is the human-readable report of a rake run.
func writeRakeText(cmd *cli.Command, res *pulse.RakeResult) {
	w := cmd.Writer
	verdict := "converged"
	if !res.Converged {
		verdict = "NOT converged"
	}
	writeText(w, "Raked %d record(s) to %s; weight column %s; %s after %d sweep(s), residual %.3g\n",
		res.Records, res.Target, res.WeightField, verdict, res.Iterations, res.Residual)
	e := res.Efficiency
	writeText(w, "Efficiency: n_eff %.4g of %d (%.4g%%), deff_kish %.4g (unequal weighting only), weights %.4g..%.4g, %d capped\n",
		e.NEff, e.N, e.EfficiencyPct, e.DeffKish, e.WeightMin, e.WeightMax, e.NCapped)
	for _, m := range res.Margins {
		writeText(w, "Margin %s:\n", strings.Join(m.Fields, " x "))
		for _, c := range m.Categories {
			writeText(w, "  %-24s observed %.4f  target %.4f  achieved %.4f\n",
				strings.Join(c.Values, " / "), c.Observed, c.Target, c.Achieved)
		}
	}
	writeSourceWarnings(w, res.Warnings)
	if len(res.InvalidatedSidecars) > 0 {
		writeText(w, "Invalidated sidecars (not rebuilt — rebuilding is yours to schedule):\n")
		for _, sc := range res.InvalidatedSidecars {
			writeText(w, "  %s  [%s]\n", sc.Path, sc.Kind)
			writeText(w, "    %s\n", sc.Rebuild)
		}
	}
}
