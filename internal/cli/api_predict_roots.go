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

// The three non-Request predict leaves: `pulse api predict-compose`,
// `predict-facet` and `predict-chain`. Each reads one request file,
// calls its facade predict method and writes the envelope it returns
// as it is. They take only --request and --json: the facade validators
// honour neither Strict nor EchoRequest, so the leaves offer no flag
// that would silently do nothing.

func apiPredictComposeCmd() *cli.Command {
	return predictRootCmd("predict-compose",
		"Validate a Compose batch (each slot over its own cohort, then the batch overlays) without executing",
		"Composed request JSON file path",
		func(ctx context.Context, p *pulse.Pulse, data []byte) (*descriptor.Envelope, error) {
			var req types.ComposedRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, fmt.Errorf("parsing composed request JSON: %w", err)
			}
			return p.PredictCompose(ctx, &req)
		})
}

func apiPredictFacetCmd() *cli.Command {
	return predictRootCmd("predict-facet",
		"Validate a facet request against its cohort without executing",
		"Facet request JSON file path",
		func(ctx context.Context, p *pulse.Pulse, data []byte) (*descriptor.Envelope, error) {
			var req types.FacetRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, fmt.Errorf("parsing facet request JSON: %w", err)
			}
			return p.PredictFacet(ctx, &req)
		})
}

func apiPredictChainCmd() *cli.Command {
	return predictRootCmd("predict-chain",
		"Validate a process-chain request (each stage over the schema the one before produces) without executing",
		"Chain request JSON file path",
		func(ctx context.Context, p *pulse.Pulse, data []byte) (*descriptor.Envelope, error) {
			var req types.ChainRequest
			if err := json.Unmarshal(data, &req); err != nil {
				return nil, fmt.Errorf("parsing chain request JSON: %w", err)
			}
			return p.PredictChain(ctx, &req)
		})
}

// predictRootCmd builds one non-Request predict leaf around predict,
// which decodes the request file's bytes and calls the facade. A
// returned error (a bad file, a nil cohort, an unreadable cohort) is
// written with its own code; a refused request is the envelope's coded
// errors with data.valid false, and exits non-zero like `api predict`.
func predictRootCmd(name, usage, requestUsage string,
	predict func(context.Context, *pulse.Pulse, []byte) (*descriptor.Envelope, error)) *cli.Command {
	return &cli.Command{
		Name:  name,
		Usage: usage,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "request", Aliases: []string{"r"}, Usage: requestUsage, Required: true},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			fail := func(fallback string, err error) error {
				if jsonOut {
					return writeCodedErrorEnvelope(cmd.Writer, fallback, err)
				}
				return err
			}

			data, err := os.ReadFile(cmd.String("request"))
			if err != nil {
				return fail("CLI_ERROR", fmt.Errorf("reading request file: %w", err))
			}
			p, err := newPulse(ctx)
			if err != nil {
				return fail("PREDICT_ERROR", err)
			}
			env, err := predict(ctx, p, data)
			if err != nil {
				return fail("PREDICT_ERROR", err)
			}

			if jsonOut {
				return writeJSON(cmd.Writer, env)
			}
			valid := len(env.Errors) == 0
			writeText(cmd.Writer, "Valid: %t\n", valid)
			for _, e := range env.Errors {
				writeText(cmd.Writer, "Error [%s]: %s\n", e.Code, e.Message)
			}
			for _, w := range env.Warnings {
				writeText(cmd.Writer, "Warning [%s]: %s\n", w.Code, w.Message)
			}
			if !valid {
				return fmt.Errorf("validation failed")
			}
			return nil
		},
	}
}
