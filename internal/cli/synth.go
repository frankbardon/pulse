package cli

import (
	"context"
	"encoding/json"
	"fmt"

	pulse "github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// SynthCommand returns the `pulse synth` command group.
func SynthCommand() *cli.Command {
	return &cli.Command{
		Name:  "synth",
		Usage: "Generate synthetic .pulse cohorts from a schema or profile",
		Commands: []*cli.Command{
			synthFromSchemaCmd(),
			synthFromProfileCmd(),
		},
	}
}

// ProfileCommand returns the `pulse profile` command group.
func ProfileCommand() *cli.Command {
	return &cli.Command{
		Name:  "profile",
		Usage: "Inspect cohort statistics for use with synth",
		Commands: []*cli.Command{
			profileCreateCmd(),
		},
	}
}

func synthFromSchemaCmd() *cli.Command {
	return &cli.Command{
		Name:  "from-schema",
		Usage: "Generate a .pulse file from a JSON schema/spec",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "spec", Aliases: []string{"s"}, Usage: "Synth spec JSON path", Required: true},
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output .pulse file path", Required: true},
			&cli.IntFlag{Name: "rows", Usage: "Override row_count from spec"},
			&cli.IntFlag{Name: "seed", Usage: "Deterministic RNG seed", Value: 0},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			specPath := cmd.String("spec")
			output := cmd.String("output")
			rows := int(cmd.Int("rows"))
			seed := cmd.Int("seed")
			jsonOut := cmd.Bool("json")

			fs := afero.NewOsFs()
			raw, err := afero.ReadFile(fs, specPath)
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			spec, err := synth.ParseSpec(raw)
			if err != nil {
				return cliError(cmd, jsonOut, "SYNTH_SPEC_ERROR", err.Error())
			}
			if rows > 0 {
				spec.RowCount = rows
			}

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}

			res, err := p.Synth(ctx, spec, output, pulse.SynthOptions{Seed: int64(seed)})
			if err != nil {
				return cliError(cmd, jsonOut, "SYNTH_ERROR", err.Error())
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, res)
			}
			writeText(cmd.Writer, "Generated %d rows -> %s (rejected %d)\n",
				res.RowsGenerated, res.OutputPath, res.RowsRejected)
			return nil
		},
	}
}

func synthFromProfileCmd() *cli.Command {
	return &cli.Command{
		Name:  "from-profile",
		Usage: "Top up a source cohort with rows generated from a previously-captured profile",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "profile", Aliases: []string{"p"}, Usage: "Profile JSON path", Required: true},
			&cli.StringFlag{Name: "source", Usage: "Source .pulse cohort the profile was captured from — copied into the output tagged _synthetic=false; never opened for write", Required: true},
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output .pulse file path — must differ from --source", Required: true},
			&cli.IntFlag{Name: "rows", Usage: "Number of NEW rows to generate (not a top-up-to-total target)", Required: true},
			&cli.IntFlag{Name: "seed", Usage: "Deterministic RNG seed", Value: 0},
			&cli.StringFlag{Name: "fidelity-report", Usage: "Write a JSON fidelity report (per-field TEST_KS/TEST_CHISQ comparison against the source) to this path after generation"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			profPath := cmd.String("profile")
			source := cmd.String("source")
			output := cmd.String("output")
			rows := int(cmd.Int("rows"))
			seed := cmd.Int("seed")
			fidelityReport := cmd.String("fidelity-report")
			jsonOut := cmd.Bool("json")

			fs := afero.NewOsFs()
			raw, err := afero.ReadFile(fs, profPath)
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			var prof synth.Profile
			if err := json.Unmarshal(raw, &prof); err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", fmt.Sprintf("parsing profile: %v", err))
			}

			spec, conflictWarnings := synth.SpecFromProfile(&prof, rows)
			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			// Capture-time thin-cell warnings (prof.Warnings, written to
			// the profile document at `profile create` time) and
			// synth-time conditional-relationship conflict warnings
			// (conflictWarnings, computed just now against the composed
			// Spec — see SpecFromProfile) share one channel into the
			// fidelity report: FidelityWarnings. Capture-time first,
			// synth-time second, matching the order each was produced.
			fidelityWarnings := append(append([]string{}, prof.Warnings...), conflictWarnings...)
			res, err := p.Synth(ctx, spec, output, pulse.SynthOptions{
				Seed:               int64(seed),
				SourceCohort:       source,
				FidelityReportPath: fidelityReport,
				FidelityWarnings:   fidelityWarnings,
			})
			if err != nil {
				return cliError(cmd, jsonOut, "SYNTH_ERROR", err.Error())
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, res)
			}
			writeText(cmd.Writer, "Generated %d rows -> %s (rejected %d)\n",
				res.RowsGenerated, res.OutputPath, res.RowsRejected)
			if res.FidelityReportPath != "" {
				writeText(cmd.Writer, "Fidelity report -> %s\n", res.FidelityReportPath)
			}
			return nil
		},
	}
}

func profileCreateCmd() *cli.Command {
	return &cli.Command{
		Name:  "create",
		Usage: "Create a profile JSON for an existing .pulse cohort",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse file path", Required: true},
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output profile JSON path", Required: true},
			&cli.IntFlag{Name: "top-k", Usage: "Top-K categorical values to retain per field", Value: 32},
			&cli.BoolFlag{Name: "include-stats", Usage: "Include percentile / std stats", Value: true},
			&cli.BoolFlag{Name: "include-correlations", Usage: "Capture pairwise numeric correlations"},
			&cli.IntFlag{Name: "correlation-top-k", Usage: "Cap on retained correlation pairs", Value: 16},
			&cli.BoolFlag{Name: "conditional", Usage: "Capture row-aligned numeric-numeric pair structure (rho + true co-occurrence N) for exact correlation reconstruction; pairs below 30 supporting observations warn rather than refuse"},
			&cli.BoolFlag{Name: "fit-shape", Usage: "Fit a 2-component Gaussian mixture per numeric field and keep it only when it's a genuine BIC improvement over the plain normal (see skills/synthetic-data.md); a near-normal field is left as normal"},
			&cli.IntFlag{Name: "sample-limit", Usage: "Cap rows ingested for the profile (0 = unlimited)"},
			&cli.BoolFlag{Name: "json", Usage: "Print envelope to stdout as well"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			output := cmd.String("output")
			topK := int(cmd.Int("top-k"))
			includeStats := cmd.Bool("include-stats")
			includeCorrelations := cmd.Bool("include-correlations")
			corrTopK := int(cmd.Int("correlation-top-k"))
			conditional := cmd.Bool("conditional")
			fitShape := cmd.Bool("fit-shape")
			sampleLimit := int(cmd.Int("sample-limit"))
			jsonOut := cmd.Bool("json")

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			prof, err := p.Profile(ctx, input, pulse.ProfileOptions{
				TopK:                topK,
				IncludeStats:        includeStats,
				IncludeCorrelations: includeCorrelations,
				CorrelationTopK:     corrTopK,
				IncludeConditional:  conditional,
				FitShape:            fitShape,
				SampleLimit:         sampleLimit,
			})
			if err != nil {
				return cliError(cmd, jsonOut, "PROFILE_ERROR", err.Error())
			}
			fs := afero.NewOsFs()
			out, err := json.MarshalIndent(prof, "", "  ")
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			if err := afero.WriteFile(fs, output, out, 0644); err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, prof)
			}
			writeText(cmd.Writer, "Profiled %d rows from %s -> %s\n", prof.RowCount, input, output)
			return nil
		},
	}
}

// cliError funnels error returns through either the JSON envelope or the
// stderr text path, depending on --json.
func cliError(cmd *cli.Command, jsonOut bool, code, msg string) error {
	if jsonOut {
		return writeErrorEnvelope(cmd.Writer, code, msg)
	}
	return fmt.Errorf("%s: %s", code, msg)
}
