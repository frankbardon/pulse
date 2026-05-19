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
			apiComposeCmd(),
			apiSampleCmd(),
			apiFacetCmd(),
			apiPredictCmd(),
			apiAskCmd(),
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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			reqPath := cmd.String("request")
			jsonOut := cmd.Bool("json")
			stream := cmd.Bool("stream")
			noDefaults := cmd.Bool("no-defaults")
			strict := cmd.Bool("strict")

			req, err := loadRequest(reqPath)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			p, err := newPulseOpts(pulse.Options{DisableDefaults: noDefaults, Strict: strict})
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
				return writeJSON(cmd.Writer, env)
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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			reqPath := cmd.String("request")
			jsonOut := cmd.Bool("json")
			stream := cmd.Bool("stream")
			workers := int(cmd.Int("parallel"))
			noFailFast := cmd.Bool("no-fail-fast")
			noDefaults := cmd.Bool("no-defaults")

			composed, err := loadComposedRequest(reqPath)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			p, err := newPulseOpts(pulse.Options{DisableDefaults: noDefaults})
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			var responses []*types.Response
			if workers != 1 {
				responses, err = p.ComposeParallel(ctx, composed, pulse.ComposeOptions{
					MaxWorkers: workers,
					FailFast:   !noFailFast,
				})
			} else {
				responses, err = p.Compose(ctx, composed)
			}
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "COMPOSE_ERROR", err.Error())
				}
				return err
			}

			if stream {
				enc := json.NewEncoder(cmd.Writer)
				for i, resp := range responses {
					if resp == nil {
						continue
					}
					for _, row := range resp.Data {
						if err := enc.Encode(map[string]any{"index": i, "row": row}); err != nil {
							return err
						}
					}
				}
				return nil
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, responses)
			}

			return writeJSON(cmd.Writer, responses)
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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			count := int(cmd.Int("count"))
			jsonOut := cmd.Bool("json")

			p, err := newPulse()
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			rows, err := p.Sample(ctx, input, count)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "SAMPLE_ERROR", err.Error())
				}
				return err
			}

			if jsonOut {
				return writeEnvelope(cmd.Writer, rows)
			}

			return writeJSON(cmd.Writer, rows)
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
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
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
			jsonOut := cmd.Bool("json")

			rich := reqPath != "" || len(fields) > 1 || topK > 0 || len(pcts) > 0 || includeHist || len(additive) > 0

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
					return writeEnvelope(cmd.Writer, values)
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
			result, err := p.FacetSchema(ctx, req)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "FACET_ERROR", err.Error())
				}
				return err
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, result)
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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			reqPath := cmd.String("request")
			jsonOut := cmd.Bool("json")
			strict := cmd.Bool("strict")

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

			opts := &descriptor.PredictOptions{Strict: strict}
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

// apiAskCmd is the natural-query one-shot leaf. It accepts either a
// structured request JSON file (--request), a natural-language query
// (--query) against a cohort file (--file), or both — query parsing
// fills slots the structured request left empty.
func apiAskCmd() *cli.Command {
	return &cli.Command{
		Name:  "ask",
		Usage: "Ask a question of a cohort: parse a natural-language query into a request and execute",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Usage: "Cohort .pulse file path"},
			&cli.StringFlag{Name: "query", Aliases: []string{"q"}, Usage: "Natural-language query string (e.g. \"average revenue by region\")"},
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: "Optional structured request JSON file path"},
			&cli.StringFlag{Name: "on-invalid", Value: "abort", Usage: "Predict-invalid behavior: \"abort\" (default) or \"suggest\""},
			&cli.BoolFlag{Name: "predict", Usage: "Validate without executing"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
			&cli.BoolFlag{Name: "no-defaults", Usage: "Disable smart operator defaults"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			file := cmd.String("file")
			query := cmd.String("query")
			reqPath := cmd.String("request")
			onInvalid := cmd.String("on-invalid")
			predictOnly := cmd.Bool("predict")
			jsonOut := cmd.Bool("json")
			noDefaults := cmd.Bool("no-defaults")

			if query == "" && reqPath == "" {
				msg := "ask requires either --query or --request"
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_INPUT", msg)
				}
				return fmt.Errorf("%s", msg)
			}

			ask := &pulse.AskRequest{
				File:      file,
				Query:     query,
				OnInvalid: onInvalid,
				Predict:   predictOnly,
			}
			if reqPath != "" {
				req, err := loadRequest(reqPath)
				if err != nil {
					if jsonOut {
						return writeErrorEnvelope(cmd.Writer, "CLI_INPUT", err.Error())
					}
					return err
				}
				ask.Request = req
			}

			p, err := newPulseOpts(pulse.Options{DisableDefaults: noDefaults})
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			resp, err := p.Ask(ctx, ask)
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "ASK_ERROR", err.Error())
				}
				return err
			}

			if jsonOut {
				return writeJSON(cmd.Writer, resp)
			}

			// Human-friendly output. Mirror `pulse api process` for the
			// row data, but lead with a parsed-request echo so the
			// caller can see how their natural-language query was
			// translated.
			if resp.QueryResolution != nil {
				writeText(cmd.Writer, "Query: %s\n", resp.QueryResolution.Query)
				writeText(cmd.Writer, "Matched fields: %v\n", resp.QueryResolution.MatchedFields)
				writeText(cmd.Writer, "Confidence: %.2f\n\n", resp.QueryResolution.Confidence)
			}
			if resp.Predict != nil && resp.Predict.Request != nil {
				writeText(cmd.Writer, "Resolved request:\n")
				_ = writeJSON(cmd.Writer, resp.Predict.Request)
				writeText(cmd.Writer, "\n")
			}
			if resp.Process != nil {
				return writeJSON(cmd.Writer, resp.Process)
			}
			for _, e := range resp.Errors {
				writeText(cmd.Writer, "Error [%s]: %s\n", e.Code, e.Message)
			}
			for _, w := range resp.Warnings {
				writeText(cmd.Writer, "Warning [%s]: %s\n", w.Code, w.Message)
			}
			return nil
		},
	}
}

// loadRequest reads and parses a JSON request file.
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

// loadComposedRequest reads and parses a JSON composed request file.
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
