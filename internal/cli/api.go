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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			reqPath := cmd.String("request")
			jsonOut := cmd.Bool("json")
			stream := cmd.Bool("stream")
			noDefaults := cmd.Bool("no-defaults")

			req, err := loadRequest(reqPath)
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
				return writeEnvelope(cmd.Writer, resp)
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
		Usage: "Return distinct values for a field in a cohort",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse file path", Required: true},
			&cli.StringFlag{Name: "field", Aliases: []string{"f"}, Usage: "Field name to facet", Required: true},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			field := cmd.String("field")
			jsonOut := cmd.Bool("json")

			p, err := newPulse()
			if err != nil {
				if jsonOut {
					return writeErrorEnvelope(cmd.Writer, "CLI_ERROR", err.Error())
				}
				return err
			}

			values, err := p.Facet(ctx, input, field)
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
		},
	}
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
