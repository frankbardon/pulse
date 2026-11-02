package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/jsonfinite"
	"github.com/frankbardon/pulse/internal/strictjson"
	cli "github.com/urfave/cli/v3"
)

// explainRootPairs maps each --root value — descriptor.ExplainRequestRoots,
// which --root is validated against — to its ExplainRequest request slot
// and, when one exists, the result slot a --response file fills.
var explainRootPairs = map[descriptor.ExplainRoot]struct {
	request  func(*descriptor.ExplainRequest) any
	response func(*descriptor.ExplainRequest) any
}{
	descriptor.ExplainRootRequest: {
		func(r *descriptor.ExplainRequest) any { return &r.Request },
		func(r *descriptor.ExplainRequest) any { return &r.Response },
	},
	descriptor.ExplainRootComposed: {
		func(r *descriptor.ExplainRequest) any { return &r.Composed },
		func(r *descriptor.ExplainRequest) any { return &r.ComposedResponse },
	},
	descriptor.ExplainRootChain: {
		func(r *descriptor.ExplainRequest) any { return &r.Chain },
		func(r *descriptor.ExplainRequest) any { return &r.ChainResponse },
	},
	descriptor.ExplainRootFacet: {
		func(r *descriptor.ExplainRequest) any { return &r.Facet },
		func(r *descriptor.ExplainRequest) any { return &r.FacetResult },
	},
	descriptor.ExplainRootSample: {
		func(r *descriptor.ExplainRequest) any { return &r.Sample },
		nil,
	},
}

// ExplainCommand returns the `pulse explain` leaf: a request file is
// described before it runs (predict-checked when it names a cohort), a
// response file is read into findings — with the request file beside it
// as its companion — and an operator name given as the argument
// (`pulse explain TEST_T`) returns that operator's validation evidence.
//
// All behaviour is pulse.Explain; this leaf reads the files, calls it
// once and formats the result. A response file is decoded with the
// undefined-figure rule (jsonfinite): a null figure stays undefined
// instead of reading as 0. A `--json` envelope (format_version + data)
// is unwrapped to its data.
func ExplainCommand() *cli.Command {
	return &cli.Command{
		Name:      "explain",
		Usage:     "Say in plain words what a request will do (never run; predict-checked when it names a cohort), what a response found, or how an operator is validated",
		ArgsUsage: "[OPERATOR]",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Request JSON file. Alone: described before it runs. With --response: the request that produced it"},
			&cli.StringFlag{Name: "response", Usage: "Result JSON file to read into findings (a --json envelope is unwrapped to its data)"},
			&cli.StringFlag{Name: "root", Value: string(descriptor.ExplainRootRequest), Usage: "Which root the files hold, one of " + orList(descriptor.ExplainRequestRoots()) +
				" (" + string(descriptor.ExplainRootRequest) + " is a process request; " + string(descriptor.ExplainRootSample) + " takes --request only)"},
			&cli.StringFlag{Name: "detail", Usage: "One of " + orList(descriptor.ExplainDetails()) + ": " + string(descriptor.ExplainTerse) + " (default) is the summary and structured parts; " +
				string(descriptor.ExplainFull) + " adds narrative sentences, glossary refs, follow-ups and assumptions"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			reqPath, respPath, root := cmd.String("request"), cmd.String("response"), descriptor.ExplainRoot(cmd.String("root"))
			roots := descriptor.ExplainRequestRoots()
			pair, ok := explainRootPairs[root]
			op := cmd.Args().First()
			switch {
			case op != "" && (reqPath != "" || respPath != "" || cmd.Args().Len() > 1):
				return cliError(cmd, jsonOut, "CLI_INPUT", "pulse explain OPERATOR takes one operator name and no --request or --response")
			case op != "":
			case reqPath == "" && respPath == "":
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"usage: pulse explain OPERATOR | --request FILE | --response FILE [--request FILE] [--root "+pipeList(roots)+"] [--detail "+pipeList(descriptor.ExplainDetails())+"]")
			case !ok || !slices.Contains(roots, root):
				return cliError(cmd, jsonOut, "CLI_INPUT", "--root "+string(root)+" is not one of "+commaList(roots))
			case respPath != "" && pair.response == nil:
				return cliError(cmd, jsonOut, "CLI_INPUT", "--root "+string(root)+" has no response to read; a sample result is rows, not findings")
			}

			req := descriptor.ExplainRequest{Detail: descriptor.ExplainDetail(cmd.String("detail")), Operator: op}
			if reqPath != "" {
				data, err := os.ReadFile(reqPath)
				if err == nil {
					// The request side decodes strictly, as every request
					// root does: an unknown key keeps its own code.
					err = strictjson.DecodeInto(data, pair.request(&req), strictjson.Options{AllowMeta: true})
				}
				var ce *perr.CodedError
				if errors.As(err, &ce) {
					return cliCodedError(cmd, jsonOut, "CLI_INPUT", err)
				}
				if err != nil {
					return cliError(cmd, jsonOut, "CLI_INPUT", "--request "+reqPath+": "+err.Error())
				}
			}
			if respPath != "" {
				if err := loadExplainResult(respPath, pair.response(&req)); err != nil {
					return cliError(cmd, jsonOut, "CLI_INPUT", "--response "+respPath+": "+err.Error())
				}
			}

			p, err := newPulse(ctx)
			if err != nil {
				return cliErrorFrom(cmd, jsonOut, "CLI_ERROR", err)
			}
			res, err := p.Explain(ctx, req)
			if err != nil {
				// Every refusal carries its own code (SERVICE_VALIDATION,
				// DATA_FILE, ENCODING_INVALID); EXPLAIN_ERROR is reached
				// only by an uncoded error.
				return cliCodedError(cmd, jsonOut, "EXPLAIN_ERROR", err)
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, res)
			}
			writeExplainText(cmd, res)
			return nil
		},
	}
}

// loadExplainResult decodes a result file into dst with the
// undefined-figure rule, unwrapping a --json envelope's data first.
func loadExplainResult(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var env struct {
		FormatVersion *string         `json:"format_version"`
		Data          json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &env) == nil && env.FormatVersion != nil && len(env.Data) > 0 {
		data = env.Data
	}
	return jsonfinite.Unmarshal(data, dst)
}

// writeExplainText is the human-readable form of an explain result.
func writeExplainText(cmd *cli.Command, res *descriptor.ExplainResult) {
	w := cmd.Writer
	writeText(w, "%s\n", res.Summary)
	if res.Valid != nil {
		writeText(w, "valid: %t\n", *res.Valid)
	}
	for _, s := range res.Steps {
		writeText(w, "\n- %s: %s", s.Slot, s.Text)
	}
	if len(res.Steps) > 0 {
		writeText(w, "\n")
	}
	for _, f := range res.Findings {
		line := fmt.Sprintf("\n- %s: %s [%s]", f.Slot, f.Subject, f.Verdict)
		if f.StrengthBand != "" {
			line += fmt.Sprintf(" %s (%s)", f.StrengthBand, f.Convention)
		}
		writeText(w, "%s", line)
	}
	if len(res.Findings) > 0 {
		writeText(w, "\n")
	}
	for _, r := range res.Refusals {
		writeText(w, "refusal %s: %s\n", r.Code, r.Message)
	}
	for _, wn := range res.Warnings {
		writeText(w, "warning %s: %s\n", wn.Code, wn.Message)
	}
	for _, a := range res.Advisories {
		writeText(w, "advisory %s: %s\n", a.Code, a.Message)
	}
	if len(res.Sentences) > 0 {
		writeText(w, "\n%s\n", strings.Join(res.Sentences, " "))
	}
	for _, c := range res.Caveats {
		writeText(w, "caveat: %s\n", c)
	}
	if v := res.Validation; v != nil {
		for _, r := range v.References {
			writeText(w, "\n- %s — tolerance %s; %s", r.Against, r.Tolerance, strings.Join(r.Tests, ", "))
		}
		if v.WeightRefusal != nil {
			writeText(w, "\n- weighted analysis refused (%s): %s; %s", v.WeightRefusal.Code, v.WeightRefusal.Reason, strings.Join(v.WeightRefusal.Tests, ", "))
		}
		if len(v.References) > 0 || v.WeightRefusal != nil {
			writeText(w, "\n")
		}
		if v.Gap != "" {
			writeText(w, "gap: %s\n", v.Gap)
		}
		for _, c := range v.Certification {
			writeText(w, "NIST %s %s (%s): %.1f digits at least\n", c.Family, c.Dataset, nonEmptyOr(c.Difficulty, "difficulty not stated"), c.MinDigits)
		}
	}
}

// nonEmptyOr returns s, or alt when s is empty.
func nonEmptyOr(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}
