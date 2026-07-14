package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
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
		},
	}
}

func apiProcessCmd() *cli.Command {
	return &cli.Command{
		Name:  "process",
		Usage: "Execute a processing request against a cohort",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Request JSON file path", Required: true},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
			&cli.BoolFlag{Name: "stream", Usage: "Stream rows as NDJSON (one row per line) instead of buffering"},
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
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			p, err := newPulseOpts(pulse.Options{DisableDefaults: noDefaults, DisableComponents: noComponents, DisableProjection: noProject, Strict: strict, EchoRequest: echoRequest})
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			if stream {
				iter, err := p.ProcessStream(ctx, req)
				if err != nil {
					if jsonOut {
						return writeErrorEnvelope(cmd.Writer, "PROCESS_ERROR", err.Error())
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
						return nil
					}
					if err := enc.Encode(row); err != nil {
						return err
					}
				}
			}

			resp, err := p.Process(ctx, req)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "PROCESS_ERROR", err.Error())
				}
				return err
			}

			if jsonOut {
				env := descriptor.NewEnvelope(resp)
				// Surface the categorical-aggregation warning on the
				// envelope. Strict mode would have already errored in
				// Process, so reaching this point in strict mode means
				// no issues. In non-strict the warnings are advisory
				// and the run continues.
				if !strict && req != nil && req.Cohort != nil {
					cohortPath := req.Cohort.Filename
					if req.Cohort.DataDir != "" {
						cohortPath = req.Cohort.DataDir + "/" + req.Cohort.Filename
					}
					if cohort, openErr := p.Open(ctx, cohortPath); openErr == nil {
						for _, entry := range descriptor.CategoricalAggregationIssues(req, cohort.Schema()) {
							env.AddWarning(entry.Code, entry.Message, entry.Details)
						}
					}
				}
				// Process mutates req in place via service.applyDefaults,
				// so by the time we reach here req is the normalized
				// (post-defaults) form. Echo it verbatim — what the
				// caller sees is exactly what the engine ran.
				if echoRequest {
					env.WithRequest(req)
				}
				return writeJSON(cmd.Writer, env)
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
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
			&cli.BoolFlag{Name: "no-defaults", Usage: "Disable smart operator defaults"},
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
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			p, err := newPulseOpts(pulse.Options{DisableDefaults: noDefaults, DisableComponents: noComponents, EchoRequest: echoRequest})
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			resp, err := p.ProcessChain(ctx, chain)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "PROCESS_CHAIN_ERROR", err.Error())
				}
				return err
			}

			if jsonOut {
				var echoed any
				if echoRequest && resp != nil && resp.NormalizedRequest != nil {
					echoed = resp.NormalizedRequest
				}
				return writeEnvelopeWithRequest(cmd.Writer, resp, echoed)
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
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
			&cli.BoolFlag{Name: "stream", Usage: "Stream rows as NDJSON; each line is {\"index\":N,\"row\":{...}}"},
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

			composed, err := loadComposedRequest(reqPath)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			p, err := newPulseOpts(pulse.Options{DisableDefaults: noDefaults, DisableComponents: noComponents, EchoRequest: echoRequest})
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
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
					return writeErrorEnvelope(cmd.Writer, "COMPOSE_ERROR", err.Error())
				}
				return err
			}

			if stream {
				// overlays surface only at terminal flush, not in per-row events
				enc := json.NewEncoder(cmd.Writer)
				if resp != nil {
					for i, sub := range resp.Responses {
						if sub == nil {
							continue
						}
						for _, row := range sub.Data {
							if err := enc.Encode(map[string]any{"index": i, "row": row}); err != nil {
								return err
							}
						}
					}
				}
				return nil
			}

			if jsonOut {
				var echoed any
				if echoRequest {
					// Each sub-request was mutated in place by service
					// during execution. Echo the composed request as a
					// whole — each slot is the post-defaults form.
					echoed = composed
				}
				return writeEnvelopeWithRequest(cmd.Writer, resp, echoed)
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
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse file path", Required: true},
			&cli.IntFlag{Name: "count", Aliases: []string{"n"}, Value: 10, Usage: "Number of rows to sample"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
			&cli.StringSliceFlag{Name: "labels", Usage: "Categorical label binding: field=table[:replace|augment]. Repeatable. Requires PULSE_LABEL_TABLES_DIR or programmatic table registration."},
			&cli.BoolFlag{Name: "echo-request", Usage: "Include the resolved SampleRequest on the envelope under \"request\"."},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			count := int(cmd.Int("count"))
			jsonOut := cmd.Bool("json")
			labelArgs := cmd.StringSlice("labels")
			echoRequest := cmd.Bool("echo-request")

			p, err := newPulse()
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			if len(labelArgs) == 0 {
				rows, err := p.Sample(ctx, input, count)
				if err != nil {
					if jsonOut {
						return writeErrorEnvelope(cmd.Writer, "SAMPLE_ERROR", err.Error())
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
					return writeErrorEnvelope(cmd.Writer, "CLI_INPUT", perr.Error())
				}
				return perr
			}
			sampleReq := &pulse.SampleRequest{
				Cohort: &types.Cohort{Filename: input}, N: count, Labels: bindings,
			}
			result, err := p.SampleWithRequest(ctx, sampleReq)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "SAMPLE_ERROR", err.Error())
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

func apiFacetCmd() *cli.Command {
	return &cli.Command{
		Name:  "facet",
		Usage: "Return distinct values for a field in a cohort, or a rich multi-field summary",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse file path"},
			&cli.StringSliceFlag{Name: "field", Aliases: []string{"f"}, Usage: "Field name to facet (repeat for multiple)"},
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Full FacetRequest JSON file (overrides flags)"},
			&cli.IntFlag{Name: "top-k", Usage: "Cap discrete values per field"},
			&cli.Float64SliceFlag{Name: "percentile", Usage: "Numeric percentile (repeatable, in (0,1))"},
			&cli.BoolFlag{Name: "histogram", Usage: "Include numeric histograms"},
			&cli.IntFlag{Name: "histogram-bins", Usage: "Histogram bin count (default 20)"},
			&cli.Float64Flag{Name: "histogram-min", Usage: "Histogram lower bound (required with --histogram)"},
			&cli.Float64Flag{Name: "histogram-max", Usage: "Histogram upper bound (required with --histogram)"},
			&cli.StringSliceFlag{Name: "additive", Usage: "Compute additive contribution counts for this field (repeatable)"},
			&cli.StringSliceFlag{Name: "labels", Usage: "Categorical label binding: field=table[:replace|augment]. Repeatable."},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
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

			p, err := newPulse()
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
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
						return writeErrorEnvelope(cmd.Writer, "FACET_ERROR", err.Error())
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
					return writeErrorEnvelope(cmd.Writer, "CLI_INPUT", err.Error())
				}
				return err
			}
			if len(labelArgs) > 0 {
				bindings, perr := parseLabelBindings(labelArgs)
				if perr != nil {
					if jsonOut {
						return writeErrorEnvelope(cmd.Writer, "CLI_INPUT", perr.Error())
					}
					return perr
				}
				req.Labels = bindings
			}
			result, err := p.FacetSchema(ctx, req)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "FACET_ERROR", err.Error())
				}
				return err
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
		if err := json.Unmarshal(data, req); err != nil {
			return nil, fmt.Errorf("parsing facet request JSON: %w", err)
		}
	}
	if input != "" {
		if req.Cohort == nil {
			req.Cohort = &types.Cohort{Filename: input}
		} else if req.Cohort.Filename == "" {
			req.Cohort.Filename = input
		}
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
	if req.Cohort == nil {
		return nil, fmt.Errorf("facet request requires a cohort (--input or request.cohort)")
	}
	return req, nil
}

func apiPredictCmd() *cli.Command {
	return &cli.Command{
		Name:  "predict",
		Usage: "Validate a request against a cohort without executing",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Request JSON file path", Required: true},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
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
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			if req.Cohort == nil {
				msg := "request must have a cohort"
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", msg)
				}
				return fmt.Errorf("%s", msg)
			}

			// Use descriptor.Predict directly for strict/envelope support.
			cohortPath := req.Cohort.Filename
			if req.Cohort.DataDir != "" {
				cohortPath = req.Cohort.DataDir + "/" + req.Cohort.Filename
			}

			data, err := os.ReadFile(cohortPath)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "PREDICT_ERROR", err.Error())
				}
				return err
			}

			opts := &descriptor.PredictOptions{Strict: strict, EchoRequest: echoRequest}
			env := descriptor.PredictFromBytes(data, req, opts)

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

			if !result.Valid {
				return fmt.Errorf("validation failed")
			}
			return nil
		},
	}
}

func loadRequest(path string) (*types.Request, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading request file: %w", err)
	}
	var req types.Request
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("parsing request JSON: %w", err)
	}
	return &req, nil
}

func loadComposedRequest(path string) (*types.ComposedRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading request file: %w", err)
	}
	var req types.ComposedRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("parsing composed request JSON: %w", err)
	}
	return &req, nil
}

func loadChainRequest(path string) (*types.ChainRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading request file: %w", err)
	}
	var req types.ChainRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("parsing chain request JSON: %w", err)
	}
	return &req, nil
}
