package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/returnshape"
	"github.com/frankbardon/pulse/internal/strictjson"
	"github.com/frankbardon/pulse/types"
	cli "github.com/urfave/cli/v3"
)

// APICommand returns the api command group.
func APICommand() *cli.Command {
	return &cli.Command{
		Name:  "api",
		Usage: "Execute processing operations against cohorts",
		Commands: []*cli.Command{
			apiProcessCmd(),
			apiProcessChainCmd(),
			apiComposeCmd(),
			apiSampleCmd(),
			apiFacetCmd(),
			apiPredictCmd(),
			apiPredictComposeCmd(),
			apiPredictFacetCmd(),
			apiPredictChainCmd(),
			apiLookupCmd(),
			apiMaterializeCmd(),
		},
	}
}

func apiProcessCmd() *cli.Command {
	return &cli.Command{
		Name:  "process",
		Usage: "Execute a processing request against a cohort",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Request JSON file path", Required: true},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
			&cli.BoolFlag{Name: "stream", Usage: "Stream rows as NDJSON (one row per line) instead of buffering; a run that warned ends with one {\"warnings\":[...]} line"},
			&cli.BoolFlag{Name: "no-defaults", Usage: "Disable smart operator defaults; require an explicit Type on every aggregation and grouper"},
			&cli.BoolFlag{Name: "strict", Usage: "Promote request-validation warnings into hard errors (e.g. numeric aggregation on a categorical field)"},
			&cli.BoolFlag{Name: "echo-request", Usage: "Include the normalized (post-defaults) request on the envelope under \"request\". Streaming output skips this — NDJSON has no envelope."},
			&cli.BoolFlag{Name: "no-components", Usage: "Suppress Response.Components (per-aggregator / per-grouper / per-filterer / crosstab / run constituent-parts metadata). Wire form is byte-identical to the pre-Components baseline."},
			&cli.BoolFlag{Name: "no-project", Usage: "Disable buffered-decode field projection; force full-record decode. Projection is on by default and output-transparent (same result, only faster), so this is an opt-out for debugging or full-map decode."},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			reqPath := cmd.String("request")
			jsonOut := cmd.Bool("json")
			stream := cmd.Bool("stream")
			noDefaults := cmd.Bool("no-defaults")
			strict := cmd.Bool("strict")
			echoRequest := cmd.Bool("echo-request")
			noComponents := cmd.Bool("no-components")
			noProject := cmd.Bool("no-project")

			req, err := loadRequest(reqPath)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}

			p, err := newPulseOpts(ctx, pulse.Options{DisableDefaults: noDefaults, DisableComponents: noComponents, DisableProjection: noProject, Strict: strict, EchoRequest: echoRequest})
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}

			if stream {
				iter, err := p.ProcessStream(ctx, req)
				if err != nil {
					if jsonOut {
						return writeCodedErrorEnvelope(cmd.Writer, "PROCESS_ERROR", err)
					}
					return err
				}
				defer iter.Close()
				enc := json.NewEncoder(cmd.Writer)
				for {
					row, ok, err := iter.Next(ctx)
					if err != nil {
						return err
					}
					if !ok {
						// A run that warned ends the stream with one
						// {"warnings":[...]} record.
						var ws []*types.ResponseWarning
						if w, ok := iter.(interface {
							Warnings() []*types.ResponseWarning
						}); ok {
							ws = w.Warnings()
						}
						return writeStreamWarnings(enc, runtimeWarnings(&types.Response{Warnings: ws}))
					}
					// A `return`-shaped stream writes each row at its
					// precision; an unshaped one exactly as before.
					body, err := returnshape.MarshalStreamRow(iter, row)
					if err != nil {
						return err
					}
					if err := enc.Encode(json.RawMessage(body)); err != nil {
						return err
					}
				}
			}

			resp, err := p.Process(ctx, req)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "PROCESS_ERROR", err)
				}
				return err
			}

			if jsonOut {
				// Run-time warnings (Response.Warnings) are promoted
				// onto the envelope's top-level warnings; data keeps
				// them too.
				env := withRuntimeWarnings(descriptor.NewEnvelope(resp), resp)
				// Process mutates req in place via service.applyDefaults,
				// so by the time we reach here req is the normalized
				// (post-defaults) form. Echo it verbatim — what the
				// caller sees is exactly what the engine ran.
				if echoRequest {
					env.WithRequest(req)
				}
				return writeEnvelopeStreamed(cmd.Writer, env)
			}

			return writeJSON(cmd.Writer, resp)
		},
	}
}

func apiProcessChainCmd() *cli.Command {
	return &cli.Command{
		Name:  "process-chain",
		Usage: "Execute a source-rooted linear chain of mergeable processing stages",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Chain request JSON file path", Required: true},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
			&cli.BoolFlag{Name: "no-defaults", Usage: "Disable smart operator defaults; require an explicit Type on every aggregation and grouper"},
			&cli.BoolFlag{Name: "echo-request", Usage: "Include the normalized ChainRequest on the envelope under \"request\". Each stage's Request reflects the per-stage post-defaults form captured during execution."},
			&cli.BoolFlag{Name: "no-components", Usage: "Suppress Response.Components on every per-stage Response. Wire form is byte-identical to the pre-Components baseline."},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			reqPath := cmd.String("request")
			jsonOut := cmd.Bool("json")
			noDefaults := cmd.Bool("no-defaults")
			echoRequest := cmd.Bool("echo-request")
			noComponents := cmd.Bool("no-components")

			chain, err := loadChainRequest(reqPath)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}

			p, err := newPulseOpts(ctx, pulse.Options{DisableDefaults: noDefaults, DisableComponents: noComponents, EchoRequest: echoRequest})
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}

			resp, err := p.ProcessChain(ctx, chain)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "PROCESS_CHAIN_ERROR", err)
				}
				return err
			}

			if jsonOut {
				var echoed any
				if echoRequest && resp != nil && resp.NormalizedRequest != nil {
					echoed = resp.NormalizedRequest
				}
				return writeResultEnvelope(cmd.Writer, resp, echoed)
			}
			return writeJSON(cmd.Writer, resp)
		},
	}
}

func apiComposeCmd() *cli.Command {
	return &cli.Command{
		Name:  "compose",
		Usage: "Execute multiple processing requests in batch",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Composed request JSON file path", Required: true},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
			&cli.BoolFlag{Name: "stream", Usage: "Stream rows as NDJSON; each line is {\"index\":N,\"row\":{...}}; a batch that warned adds one {\"warnings\":[...]} line after the rows; a sampling sweep (random / lhs) adds one {\"sweep_seed\":N} line; then, in this order, a ranked sweep adds one {\"ranking\":[...]} line (plus \"ranking_profile\" under rank.profile, \"sweep_trace\" under an adaptive mode), a banner one {\"banner\":{...}} line, optimize blocks one {\"optimizations\":[...]} line and nested-model comparisons one {\"comparisons\":[...]} line"},
			&cli.BoolFlag{Name: "stream-fits", Usage: "Under --stream, end the stream with one {\"fits\":{...}} line (the buffered fits object, after every other line) when the request declares fits; ignored without --stream"},
			&cli.IntFlag{Name: "parallel", Usage: "Run requests concurrently with up to N workers (0 = GOMAXPROCS); 1 forces sequential", Value: 1},
			&cli.BoolFlag{Name: "no-fail-fast", Usage: "Aggregate errors instead of cancelling on first failure (parallel only)"},
			&cli.BoolFlag{Name: "no-defaults", Usage: "Disable smart operator defaults; require an explicit Type on every aggregation and grouper"},
			&cli.BoolFlag{Name: "echo-request", Usage: "Include the normalized ComposedRequest on the envelope under \"request\". Each slot reflects its post-defaults form. Streaming output skips this."},
			&cli.BoolFlag{Name: "no-components", Usage: "Suppress Response.Components on every per-slot Response in ComposedResponse.Responses. Wire form is byte-identical to the pre-Components baseline."},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			reqPath := cmd.String("request")
			jsonOut := cmd.Bool("json")
			stream := cmd.Bool("stream")
			workers := int(cmd.Int("parallel"))
			noFailFast := cmd.Bool("no-fail-fast")
			noDefaults := cmd.Bool("no-defaults")
			echoRequest := cmd.Bool("echo-request")
			noComponents := cmd.Bool("no-components")
			streamFits := cmd.Bool("stream-fits")

			composed, err := loadComposedRequest(reqPath)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}

			p, err := newPulseOpts(ctx, pulse.Options{DisableDefaults: noDefaults, DisableComponents: noComponents, EchoRequest: echoRequest})
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}

			var resp *types.ComposedResponse
			if workers != 1 {
				resp, err = p.ComposeParallel(ctx, composed, pulse.ComposeOptions{
					MaxWorkers: workers,
					FailFast:   !noFailFast,
				})
			} else {
				resp, err = p.Compose(ctx, composed)
			}
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "COMPOSE_ERROR", err)
				}
				return err
			}

			if stream {
				// The library writes the stream (rows, then the
				// trailing records); --stream-fits adds the terminal
				// fits line.
				return pulse.WriteComposeStream(cmd.Writer, composed, resp, pulse.ComposeStreamOptions{TerminalFits: streamFits})
			}

			if jsonOut {
				// The library hands back the effective request it ran
				// (a sweep expanded into its slots) when EchoRequest
				// is on; the CLI writes it as is.
				var echoed any
				if echoRequest && resp != nil && resp.NormalizedRequest != nil {
					echoed = resp.NormalizedRequest
				}
				return writeResultEnvelope(cmd.Writer, resp, echoed)
			}

			return writeJSON(cmd.Writer, resp)
		},
	}
}

func apiSampleCmd() *cli.Command {
	return &cli.Command{
		Name:  "sample",
		Usage: "Return sample rows from a cohort",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path (required unless --request)"},
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Full SampleRequest JSON file, or - for stdin (cohort or union, union_source_field, n, labels); in place of --input. --count and --labels layer on top of the file"},
			&cli.IntFlag{Name: "count", Aliases: []string{"n"}, Value: 10, Usage: "Number of rows to sample (with --request: overrides the file's n, the default when it has none)"},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
			&cli.StringSliceFlag{Name: "labels", Usage: "Label binding on a categorical field, or an integer-coded u4/u8/u16/u32 field in augment mode: " + labelBindingGrammar + ". Repeatable. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration."},
			&cli.BoolFlag{Name: "echo-request", Usage: "Include the resolved SampleRequest on the envelope under \"request\"."},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			reqPath := cmd.String("request")
			count := int(cmd.Int("count"))
			jsonOut := cmd.Bool("json")
			labelArgs := cmd.StringSlice("labels")
			echoRequest := cmd.Bool("echo-request")

			// The parser's own refusals (exit 2, no envelope): --input
			// and --request name the cohort two ways, and one is needed.
			switch {
			case input != "" && reqPath != "":
				return usageExit(perr.NewCodedError(perr.CLI_INPUT,
					"--input and --request are mutually exclusive (name the cohort in the request file, or pass --input alone)"))
			case input == "" && reqPath == "":
				return usageExit(perr.NewCodedError(perr.CLI_INPUT,
					`Required flag "input" not set (or pass --request with a SampleRequest JSON file)`))
			}

			p, err := newPulse(ctx)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}

			if reqPath != "" {
				return runSampleRequest(ctx, cmd, p, reqPath, labelArgs, jsonOut, echoRequest)
			}

			if len(labelArgs) == 0 {
				rows, err := p.Sample(ctx, input, count)
				if err != nil {
					if jsonOut {
						return writeCodedErrorEnvelope(cmd.Writer, "SAMPLE_ERROR", err)
					}
					return err
				}
				if jsonOut {
					var echoed any
					if echoRequest {
						echoed = &pulse.SampleRequest{
							Cohort: &types.Cohort{Filename: input},
							N:      count,
						}
					}
					return writeEnvelopeWithRequest(cmd.Writer, rows, echoed)
				}
				return writeJSON(cmd.Writer, rows)
			}

			bindings, perr := parseLabelBindings(labelArgs)
			if perr != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_INPUT", perr)
				}
				return perr
			}
			sampleReq := &pulse.SampleRequest{
				Cohort: &types.Cohort{Filename: input}, N: count, Labels: bindings,
			}
			result, err := p.SampleWithRequest(ctx, sampleReq)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "SAMPLE_ERROR", err)
				}
				return err
			}
			if jsonOut {
				env := descriptor.NewEnvelope(result.Rows)
				if echoRequest {
					env.WithRequest(sampleReq)
				}
				for _, w := range result.Warnings {
					env.AddWarning(w.Code, w.Message, w.Details)
				}
				return writeJSON(cmd.Writer, env)
			}
			return writeJSON(cmd.Writer, result.Rows)
		},
	}
}

// runSampleRequest is `api sample --request`: the SampleRequest the
// file (or stdin, for "-") holds, through the strict request decoder,
// with --count / --labels layered on top, handed to SampleWithRequest
// as MCP pulse_sample hands its union form. A union slot the instance
// hides is refused there, by the library's slot gate.
func runSampleRequest(ctx context.Context, cmd *cli.Command, p *pulse.Pulse, path string, labelArgs []string, jsonOut, echoRequest bool) error {
	var count *int
	if cmd.IsSet("count") {
		n := int(cmd.Int("count"))
		count = &n
	}
	req, err := loadSampleRequest(path, cmdReader(cmd), count)
	if err != nil {
		if jsonOut {
			return writeCodedErrorEnvelope(cmd.Writer, "CLI_INPUT", err)
		}
		return err
	}
	if len(labelArgs) > 0 {
		bindings, lerr := parseLabelBindings(labelArgs)
		if lerr != nil {
			if jsonOut {
				return writeCodedErrorEnvelope(cmd.Writer, "CLI_INPUT", lerr)
			}
			return lerr
		}
		req.Labels = bindings
	}
	result, err := p.SampleWithRequest(ctx, req)
	if err != nil {
		if jsonOut {
			return writeCodedErrorEnvelope(cmd.Writer, "SAMPLE_ERROR", err)
		}
		return err
	}
	if !jsonOut {
		return writeJSON(cmd.Writer, result.Rows)
	}
	env := descriptor.NewEnvelope(result.Rows)
	if echoRequest {
		env.WithRequest(req)
	}
	for _, w := range result.Warnings {
		env.AddWarning(w.Code, w.Message, w.Details)
	}
	return writeJSON(cmd.Writer, env)
}

// sampleDefaultCount is the row count a sample request gets when
// neither its file (`n`) nor --count names one — --count's default, and
// MCP pulse_sample's.
const sampleDefaultCount = 10

// loadSampleRequest decodes the SampleRequest at path ("-" reads stdin)
// through the shared strict request decoder. A file without `n` takes
// sampleDefaultCount; count (an explicitly set --count) overrides
// either. The cohort / union rules are the library's.
func loadSampleRequest(path string, stdin io.Reader, count *int) (*pulse.SampleRequest, error) {
	var data []byte
	var err error
	if path == "-" {
		if stdin == nil {
			stdin = os.Stdin
		}
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading sample request file: %w", err)
	}
	req := &pulse.SampleRequest{N: sampleDefaultCount}
	if err := decodeRequestJSON(data, req, "sample request"); err != nil {
		return nil, err
	}
	if count != nil {
		req.N = *count
	}
	return req, nil
}

// cmdReader is the stdin a leaf reads: the root command's Reader (a
// test's buffer), else the process's.
func cmdReader(cmd *cli.Command) io.Reader {
	if r := cmd.Root().Reader; r != nil {
		return r
	}
	return os.Stdin
}

func apiFacetCmd() *cli.Command {
	return &cli.Command{
		Name:  "facet",
		Usage: "Return distinct values for a field in a cohort, or a rich multi-field summary",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path"},
			&cli.StringSliceFlag{Name: "field", Aliases: []string{"f"}, Usage: "Field name to facet (repeat for multiple)"},
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Full FacetRequest JSON file (flags layer on top of the file)"},
			&cli.IntFlag{Name: "top-k", Usage: "Cap discrete values per field"},
			&cli.Float64SliceFlag{Name: "percentile", Usage: "Numeric percentile (repeatable, in (0,1))"},
			&cli.BoolFlag{Name: "histogram", Usage: "Include numeric histograms"},
			&cli.IntFlag{Name: "histogram-bins", Usage: "Histogram bin count (default 20)"},
			&cli.Float64Flag{Name: "histogram-min", Usage: "Histogram lower bound (required with --histogram)"},
			&cli.Float64Flag{Name: "histogram-max", Usage: "Histogram upper bound (required with --histogram)"},
			&cli.StringSliceFlag{Name: "additive", Usage: "Compute additive contribution counts for this field (repeatable)"},
			&cli.StringSliceFlag{Name: "labels", Usage: "Label binding on a categorical field, or an integer-coded u4/u8/u16/u32 field in augment mode: " + labelBindingGrammar + ". Repeatable. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration."},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
			&cli.BoolFlag{Name: "echo-request", Usage: "Include the resolved FacetRequest on the envelope under \"request\"."},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			fields := cmd.StringSlice("field")
			reqPath := cmd.String("request")
			topK := int(cmd.Int("top-k"))
			pcts := cmd.Float64Slice("percentile")
			includeHist := cmd.Bool("histogram")
			histBins := int(cmd.Int("histogram-bins"))
			histMin := cmd.Float64("histogram-min")
			histMax := cmd.Float64("histogram-max")
			additive := cmd.StringSlice("additive")
			labelArgs := cmd.StringSlice("labels")
			jsonOut := cmd.Bool("json")
			echoRequest := cmd.Bool("echo-request")

			rich := reqPath != "" || len(fields) > 1 || topK > 0 || len(pcts) > 0 || includeHist || len(additive) > 0 || len(labelArgs) > 0

			p, err := newPulse(ctx)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}

			if !rich {
				if input == "" || len(fields) == 0 {
					msg := "facet requires --input and --field (or --request for rich mode)"
					if jsonOut {
						return writeErrorEnvelope(cmd.Writer, "CLI_INPUT", msg)
					}
					return fmt.Errorf("%s", msg)
				}
				values, err := p.Facet(ctx, input, fields[0])
				if err != nil {
					if jsonOut {
						return writeCodedErrorEnvelope(cmd.Writer, "FACET_ERROR", err)
					}
					return err
				}
				if jsonOut {
					var echoed any
					if echoRequest {
						echoed = &pulse.FacetRequest{
							Cohort: &types.Cohort{Filename: input},
							Fields: []string{fields[0]},
						}
					}
					return writeEnvelopeWithRequest(cmd.Writer, values, echoed)
				}
				for _, v := range values {
					writeText(cmd.Writer, "%s\n", v)
				}
				return nil
			}

			req, err := loadFacetRequest(reqPath, input, fields, topK, pcts, includeHist, histBins, histMin, histMax, additive)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_INPUT", err)
				}
				return err
			}
			if len(labelArgs) > 0 {
				bindings, perr := parseLabelBindings(labelArgs)
				if perr != nil {
					if jsonOut {
						return writeCodedErrorEnvelope(cmd.Writer, "CLI_INPUT", perr)
					}
					return perr
				}
				req.Labels = bindings
			}
			result, err := p.FacetSchema(ctx, req)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "FACET_ERROR", err)
				}
				return err
			}
			if jsonOut {
				var echoed any
				if echoRequest {
					echoed = req
				}
				return writeResultEnvelope(cmd.Writer, result, echoed)
			}
			return writeJSON(cmd.Writer, result)
		},
	}
}

// loadFacetRequest constructs a FacetRequest either by parsing the
// supplied --request JSON file or by composing one from the per-flag
// shortcuts. Flag overrides land on top of the file shape so
// "--request foo.json --top-k 50" works as expected.
func loadFacetRequest(path, input string, fields []string, topK int, pcts []float64,
	includeHist bool, histBins int, histMin, histMax float64,
	additive []string) (*pulse.FacetRequest, error) {
	req := &pulse.FacetRequest{}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading facet request file: %w", err)
		}
		if err := decodeRequestJSON(data, req, "facet request"); err != nil {
			return nil, err
		}
	}
	if input != "" {
		req.Cohort = inputCohort(input)
	}
	if len(fields) > 0 {
		req.Fields = fields
	}
	if topK > 0 {
		req.DiscreteTopK = topK
	}
	if len(pcts) > 0 {
		req.NumericPercentiles = pcts
	}
	if includeHist {
		req.IncludeHistogram = true
		if histBins > 0 {
			req.HistogramBins = histBins
		}
		if histMin != 0 || histMax != 0 {
			req.HistogramRange = [2]float64{histMin, histMax}
		}
	}
	if len(additive) > 0 {
		req.AdditiveFields = additive
	}
	if req.Cohort == nil && len(req.Union) == 0 {
		return nil, fmt.Errorf("facet request requires a cohort (--input, request.cohort or request.union)")
	}
	return req, nil
}

func apiPredictCmd() *cli.Command {
	return &cli.Command{
		Name:  "predict",
		Usage: "Validate a request against a cohort without executing",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Request JSON file path", Required: true},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
			&cli.BoolFlag{Name: "strict", Usage: "Treat warnings as errors"},
			&cli.BoolFlag{Name: "echo-request", Usage: "Include the normalized request on the envelope under \"request\" (distinct from PredictResult.Request, which echoes the raw input)."},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			reqPath := cmd.String("request")
			jsonOut := cmd.Bool("json")
			strict := cmd.Bool("strict")
			echoRequest := cmd.Bool("echo-request")

			req, err := loadRequest(reqPath)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "CLI_ERROR", err)
				}
				return err
			}

			if req.Cohort == nil && len(req.Union) == 0 {
				msg := "request must have a cohort or a union"
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", msg)
				}
				return fmt.Errorf("%s", msg)
			}

			// PredictEnvelope reads only the cohort's header and schema
			// (an archive's central directory and shard headers) through
			// the instance filesystem, returns the whole envelope and
			// takes Strict / EchoRequest (and the extension snapshot)
			// from the instance.
			p, err := newPulseOpts(ctx, pulse.Options{Strict: strict, EchoRequest: echoRequest})
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "PREDICT_ERROR", err)
				}
				return err
			}
			env, err := p.PredictEnvelope(ctx, req)
			if err != nil {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, "PREDICT_ERROR", err)
				}
				return err
			}

			if jsonOut {
				return writeJSON(cmd.Writer, env)
			}

			result, ok := env.Data.(*descriptor.PredictResult)
			if !ok {
				return fmt.Errorf("unexpected predict result type")
			}

			if result.Valid {
				writeText(cmd.Writer, "Valid: true\n")
			} else {
				writeText(cmd.Writer, "Valid: false\n")
			}

			if result.SchemaInfo != nil {
				writeText(cmd.Writer, "Schema: %d fields\n", result.SchemaInfo.FieldCount)
			}

			for _, e := range env.Errors {
				writeText(cmd.Writer, "Error [%s]: %s\n", e.Code, e.Message)
			}
			for _, w := range env.Warnings {
				writeText(cmd.Writer, "Warning [%s]: %s\n", w.Code, w.Message)
			}
			for _, a := range result.Advisories {
				writeText(cmd.Writer, "Advisory [%s]: %s\n", a.Code, a.Message)
			}

			if !result.Valid {
				return fmt.Errorf("validation failed")
			}
			return nil
		},
	}
}

// apiLookupCmd implements `pulse api lookup` — the CLI leaf for
// Pulse.Lookup. This settles the "dedicated `pulse lookup` vs `pulse
// api lookup`" question raised in E4-S3: lookup joins process / compose
// / facet / sample / process-chain under the `pulse api` group because,
// like facet, it is a request/response operation against a cohort
// (execute-and-return), not a corpus-management verb the way `pulse
// index {build,list,verify,drop}` is. `pulse api` stays the single home
// for "run this request shape against a cohort and get a result back".
//
// Two ways to supply the request, mirroring apiFacetCmd's dual-mode
// precedent: the ergonomic flag form (--input, repeatable --key
// field=value for single or composite keys, --return, --mode) for the
// common case, or a full LookupRequest JSON file via --request/-r
// (flags layer on top of the file, same precedence as loadFacetRequest).
func apiLookupCmd() *cli.Command {
	return &cli.Command{
		Name:  "lookup",
		Usage: "Look up row(s) by an indexed key against a cohort's prebuilt sidecar index (see `pulse index build`)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort file path"},
			&cli.StringSliceFlag{Name: "key", Aliases: []string{"k"}, Usage: "Key column=value pair to match; repeat for a composite key in the same column order the sidecar index was built with (e.g. --key region=EU --key date=2026-01-01)"},
			&cli.StringFlag{Name: "return", Usage: "Comma-separated field names to project into the result row(s); omit for every schema field"},
			&cli.StringFlag{Name: "mode", Usage: lookupModeFlagUsage},
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Full LookupRequest JSON file (flags layer on top of the file)"},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
			&cli.BoolFlag{Name: "echo-request", Usage: "Include the resolved LookupRequest on the envelope under \"request\"."},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			keyArgs := cmd.StringSlice("key")
			returnCols := cmd.String("return")
			mode := cmd.String("mode")
			reqPath := cmd.String("request")
			jsonOut := cmd.Bool("json")
			echoRequest := cmd.Bool("echo-request")

			req, err := loadLookupRequest(reqPath, input, keyArgs, returnCols, mode)
			if err != nil {
				return cliErrorFrom(cmd, jsonOut, "CLI_INPUT", err)
			}

			p, err := newPulse(ctx)
			if err != nil {
				return cliErrorFrom(cmd, jsonOut, "CLI_ERROR", err)
			}

			result, err := p.Lookup(ctx, req)
			if err != nil {
				return indexCliError(cmd, jsonOut, err)
			}

			if jsonOut {
				var echoed any
				if echoRequest {
					echoed = req
				}
				return writeEnvelopeWithRequest(cmd.Writer, result, echoed)
			}
			return writeJSON(cmd.Writer, result)
		},
	}
}

// apiMaterializeCmd is `pulse api materialize`: write a request's
// derived columns (features, attributes, windows) as ordinary fields of
// a new cohort. All behaviour is pulse.Materialize; the leaf parses
// flags, calls it once and formats the result. Every refusal carries
// its own code (PULSE_MATERIALIZE_*, SERVICE_VALIDATION for an existing
// target, the request's own pre-flight codes); MATERIALIZE_ERROR is
// reached only by an uncoded error. --predict calls
// pulse.PredictMaterialize instead: the predict envelope is written as
// it is, nothing is written at --out (which it does not need), and the
// exit status follows the envelope's errors; --strict there promotes
// every predict warning into an error (the instance's Options.Strict).
func apiMaterializeCmd() *cli.Command {
	return &cli.Command{
		Name:  "materialize",
		Usage: "Write a new cohort holding the request's filter-passing source records plus one f64 column per derived output (features, attributes, windows), so later requests read them as ordinary fields (atomic)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Request JSON file path (cohort, label, features, filterers, attributes, windows, sort, weight, time_zone, output, disable_components only)", Required: true},
			&cli.StringFlag{Name: "out", Aliases: []string{"o"}, Usage: "Path of the new .pulse cohort to write (required unless --predict)"},
			&cli.BoolFlag{Name: "overwrite", Usage: "Replace an existing cohort at --out (atomic); its now-stale sidecars are listed, never rebuilt"},
			&cli.BoolFlag{Name: "strict", Usage: "Fail with PULSE_FIELD_DESCRIPTION_LOW_QUALITY (nothing written) when an output field's description is weak, instead of warning; with --predict, promote every predict warning into an error"},
			&cli.BoolFlag{Name: "predict", Usage: "Validate without executing: print the output schema the run would write, every static refusal and the data-dependent risks, reading the source's header and schema only; nothing is written"},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			if !cmd.Bool("predict") && cmd.String("out") == "" {
				// The parser's own refusal for a missing required flag,
				// which --out is unless --predict.
				return usageExit(perr.NewCodedError(perr.CLI_INPUT,
					`Required flag "out" not set (pass --predict to validate without writing)`))
			}
			req, err := loadRequest(cmd.String("request"))
			if err != nil {
				return cliErrorFrom(cmd, jsonOut, "CLI_INPUT", err)
			}
			if cmd.Bool("predict") {
				return materializePredict(ctx, cmd, jsonOut, req)
			}
			p, err := newPulse(ctx)
			if err != nil {
				return cliCodedError(cmd, jsonOut, "CLI_ERROR", err)
			}
			res, err := p.Materialize(ctx, req, cmd.String("out"), pulse.MaterializeOptions{
				Overwrite: cmd.Bool("overwrite"),
				Strict:    cmd.Bool("strict"),
			})
			if err != nil {
				return cliCodedError(cmd, jsonOut, "MATERIALIZE_ERROR", err)
			}
			if jsonOut {
				return writeEnvelopeWithWarnings(cmd.Writer, res, res.Warnings)
			}
			writeMaterializeText(cmd, res)
			return nil
		},
	}
}

// materializePredict is `pulse api materialize --predict`: the facade's
// predict envelope written as it is (--json), or a text report of the
// output columns, refusals, warnings and risks. Exits non-zero when the
// envelope carries an error.
func materializePredict(ctx context.Context, cmd *cli.Command, jsonOut bool, req *pulse.Request) error {
	p, err := newPulseOpts(ctx, pulse.Options{Strict: cmd.Bool("strict")})
	if err != nil {
		return cliCodedError(cmd, jsonOut, "CLI_ERROR", err)
	}
	env, err := p.PredictMaterialize(ctx, req)
	if err != nil {
		return cliCodedError(cmd, jsonOut, "PREDICT_ERROR", err)
	}
	if jsonOut {
		return writeJSON(cmd.Writer, env)
	}
	w := cmd.Writer
	valid := len(env.Errors) == 0
	writeText(w, "Valid: %t\n", valid)
	if res, ok := env.Data.(*descriptor.MaterializePredictResult); ok {
		for _, c := range res.Columns {
			nullable := ""
			if c.Nullable {
				nullable = ", nullable"
			}
			writeText(w, "  %s  %s%s  (from %s)\n", c.Name, c.Type, nullable, c.From)
		}
		for _, r := range res.Risks {
			writeText(w, "Risk [%s] %s: %s\n", r.Code, r.Column, r.Check)
		}
		if c := res.Container; c != nil && c.Kind == "shard_archive" {
			writeText(w, "Container: shard archive, %d row(s) per shard\n", c.MaxRecords)
		}
		if c := res.Container; c != nil && c.ElideConstants {
			writeText(w, "Container: constants elided\n")
		}
		if c := res.Container; c != nil {
			for _, g := range c.Groups {
				writeText(w, "Group %s (%s): %s\n", g.Label, g.Origin, g.Verdict)
			}
		}
	}
	for _, e := range env.Errors {
		writeText(w, "Error [%s]: %s\n", e.Code, e.Message)
	}
	for _, wn := range env.Warnings {
		writeText(w, "Warning [%s]: %s\n", wn.Code, wn.Message)
	}
	if !valid {
		return fmt.Errorf("validation failed")
	}
	return nil
}

// writeMaterializeText is the human-readable report of a materialize run.
func writeMaterializeText(cmd *cli.Command, res *pulse.MaterializeResult) {
	w := cmd.Writer
	writeText(w, "Materialized %d record(s) to %s; derived columns: %s\n",
		res.Records, res.Target, strings.Join(res.DerivedColumns, ", "))
	if len(res.Shards) > 0 {
		writeText(w, "Shard archive of %d shard(s): %s\n", len(res.Shards), strings.Join(res.Shards, ", "))
	}
	if len(res.ElidedConstants) > 0 {
		writeText(w, "Elided constants: %s\n", strings.Join(res.ElidedConstants, ", "))
	}
	for _, g := range res.Groups {
		writeText(w, "Group %s: %s\n", g.Label, g.Verdict)
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

// inputCohort is the cohort an `-i/--input` flag names. The flag
// REPLACES a request file's cohort outright — filename and data_dir —
// so `-i x.pulse -r req.json` reads x.pulse whatever req.json names, as
// the flag's help text says (#308). A data_dir left over from the file
// would otherwise silently prefix the flag's path.
func inputCohort(input string) *types.Cohort {
	return &types.Cohort{Filename: input}
}

// loadLookupRequest constructs a LookupRequest either by parsing the
// supplied --request JSON file or by composing one from the per-flag
// shortcuts, mirroring loadFacetRequest's precedence: flag values layer
// on top of (override) the file shape, so "--request foo.json --mode
// first" works as expected.
func loadLookupRequest(path, input string, keyArgs []string, returnCols, mode string) (*pulse.LookupRequest, error) {
	req := &pulse.LookupRequest{}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading lookup request file: %w", err)
		}
		if err := decodeRequestJSON(data, req, "lookup request"); err != nil {
			return nil, err
		}
	}
	if input != "" {
		req.Cohort = inputCohort(input)
	}
	if len(keyArgs) > 0 {
		keys, err := parseLookupKeys(keyArgs)
		if err != nil {
			return nil, err
		}
		req.Keys = keys
	}
	if returnCols != "" {
		req.ReturnColumns = splitIndexKeyFields(returnCols)
	}
	if mode != "" {
		multiplicity, err := translateLookupMode(mode)
		if err != nil {
			return nil, err
		}
		req.Multiplicity = multiplicity
	}
	if req.Cohort == nil && len(req.Union) == 0 {
		return nil, fmt.Errorf("lookup request requires a cohort (--input, request.cohort or request.union)")
	}
	if len(req.KeyComponents()) == 0 {
		return nil, fmt.Errorf("lookup request requires at least one --key field=value (or request.keys / request.field+value)")
	}
	return req, nil
}

// parseLookupKeys turns a slice of repeated CLI --key arguments (each
// "field=value") into the ordered []types.LookupKey tuple LookupRequest
// probes with. Order is preserved verbatim — composite lookups are
// order-significant end to end, matching Service.BuildIndex's keyFields
// ordering (see types.LookupKey's doc comment).
func parseLookupKeys(args []string) ([]types.LookupKey, error) {
	keys := make([]types.LookupKey, 0, len(args))
	for _, arg := range args {
		field, value, ok := strings.Cut(arg, "=")
		if !ok || field == "" {
			return nil, fmt.Errorf("invalid --key %q, want field=value", arg)
		}
		keys = append(keys, types.LookupKey{Field: field, Value: value})
	}
	return keys, nil
}

// lookupModeFlagUsage lists types.AllLookupMultiplicities, the default
// first.
var lookupModeFlagUsage = "Multiplicity mode when the key matches more than one row, one of " +
	orList(types.AllLookupMultiplicities()) + ": " + string(types.LookupMultiplicityAssertUnique) +
	" (default; also spelled assert-unique) errors PULSE_LOOKUP_AMBIGUOUS"

// translateLookupMode resolves a --mode value through
// types.ParseLookupMultiplicity: the wire spellings ("assert_unique" |
// "first" | "all") and the hyphenated "assert-unique" alias. A refusal
// stays uncoded (the leaf's placeholder code), as it always was.
func translateLookupMode(mode string) (types.LookupMultiplicity, error) {
	m, ok := types.ParseLookupMultiplicity(mode)
	if !ok {
		return "", fmt.Errorf("invalid --mode %q, want %s (or assert-unique)", mode, pipeList(types.AllLookupMultiplicities()))
	}
	return m, nil
}

func loadRequest(path string) (*types.Request, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading request file: %w", err)
	}
	var req types.Request
	if err := decodeRequestJSON(data, &req, "request"); err != nil {
		return nil, err
	}
	return &req, nil
}

func loadComposedRequest(path string) (*types.ComposedRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading request file: %w", err)
	}
	var req types.ComposedRequest
	if err := decodeRequestJSON(data, &req, "composed request"); err != nil {
		return nil, err
	}
	return &req, nil
}

func loadChainRequest(path string) (*types.ChainRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading request file: %w", err)
	}
	var req types.ChainRequest
	if err := decodeRequestJSON(data, &req, "chain request"); err != nil {
		return nil, err
	}
	return &req, nil
}

// decodeRequestJSON decodes a request file's bytes into out through the
// shared strict request-entry decoder (strictjson.DecodeRequestInto), as
// MCP does: an unknown key at any depth is the coded
// PULSE_REQUEST_UNKNOWN_FIELD refusal (details.path, suggestions,
// valid_keys); any other fault is SERVICE_VALIDATION "parsing <what>
// JSON: ...". Either is returned unwrapped, so the envelope carries its
// own code; a root `_meta` example block is tolerated.
func decodeRequestJSON(data []byte, out any, what string) error {
	return strictjson.DecodeRequestInto(data, out, strictjson.Options{AllowMeta: true}, what)
}
