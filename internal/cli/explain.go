package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/jsonfinite"
	cli "github.com/urfave/cli/v3"
)

// explainRootPairs maps each --root value to its ExplainRequest request
// slot and, when one exists, the result slot a --response file fills.
var explainRootPairs = map[string]struct {
	request  func(*descriptor.ExplainRequest) any
	response func(*descriptor.ExplainRequest) any
}{
	"request": {
		func(r *descriptor.ExplainRequest) any { return &r.Request },
		func(r *descriptor.ExplainRequest) any { return &r.Response },
	},
	"composed": {
		func(r *descriptor.ExplainRequest) any { return &r.Composed },
		func(r *descriptor.ExplainRequest) any { return &r.ComposedResponse },
	},
	"chain": {
		func(r *descriptor.ExplainRequest) any { return &r.Chain },
		func(r *descriptor.ExplainRequest) any { return &r.ChainResponse },
	},
	"facet": {
		func(r *descriptor.ExplainRequest) any { return &r.Facet },
		func(r *descriptor.ExplainRequest) any { return &r.FacetResult },
	},
	"sample": {
		func(r *descriptor.ExplainRequest) any { return &r.Sample },
		nil,
	},
}

// ExplainCommand returns the `pulse explain` leaf: a request file is
// described before it runs (predict-checked when it names a cohort), a
// response file is read into findings — with the request file beside it
// as its companion.
//
// All behaviour is pulse.Explain; this leaf reads the files, calls it
// once and formats the result. A response file is decoded with the
// undefined-figure rule (jsonfinite): a null figure stays undefined
// instead of reading as 0. A `--json` envelope (format_version + data)
// is unwrapped to its data.
func ExplainCommand() *cli.Command {
	return &cli.Command{
		Name:  "explain",
		Usage: "Say in plain words what a request will do (never run; predict-checked when it names a cohort) or what a response found",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Request JSON file. Alone: described before it runs. With --response: the request that produced it"},
			&cli.StringFlag{Name: "response", Usage: "Result JSON file to read into findings (a --json envelope is unwrapped to its data)"},
			&cli.StringFlag{Name: "root", Value: "request", Usage: "Which root the files hold: request (process), composed, chain, facet or sample (request only)"},
			&cli.StringFlag{Name: "detail", Usage: "terse (default) or full (adds narrative sentences, glossary refs, follow-ups and assumptions)"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			reqPath, respPath, root := cmd.String("request"), cmd.String("response"), cmd.String("root")
			pair, ok := explainRootPairs[root]
			switch {
			case reqPath == "" && respPath == "":
				return cliError(cmd, jsonOut, "CLI_INPUT",
					"usage: pulse explain --request FILE | --response FILE [--request FILE] [--root request|composed|chain|facet|sample] [--detail terse|full]")
			case !ok:
				return cliError(cmd, jsonOut, "CLI_INPUT", "--root "+root+" is not one of request, composed, chain, facet, sample")
			case respPath != "" && pair.response == nil:
				return cliError(cmd, jsonOut, "CLI_INPUT", "--root sample has no response to read; a sample result is rows, not findings")
			}

			req := descriptor.ExplainRequest{Detail: descriptor.ExplainDetail(cmd.String("detail"))}
			if reqPath != "" {
				data, err := os.ReadFile(reqPath)
				if err == nil {
					err = json.Unmarshal(data, pair.request(&req))
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
	for _, a := range res.Advisories {
		writeText(w, "advisory %s: %s\n", a.Code, a.Message)
	}
	if len(res.Sentences) > 0 {
		writeText(w, "\n%s\n", strings.Join(res.Sentences, " "))
	}
	for _, c := range res.Caveats {
		writeText(w, "caveat: %s\n", c)
	}
}
