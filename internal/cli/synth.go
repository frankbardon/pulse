package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

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
			// Generation warnings (conflict arbitration, correlation
			// completion, model compilation) reach --json inside
			// data.warnings and reached the text path nowhere at all.
			writeWarningSummary(errWriter(cmd), res.Warnings,
				"re-run with --json for every line")
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
			&cli.StringFlag{Name: "rules", Usage: "Load structural rules from a standalone JSON file (a bare array of rule objects — the same shape as a spec's \"rules\" key) and apply them to the profile-derived spec; REPLACES any rules the spec carries"},
			&cli.StringFlag{Name: "emit-spec", Usage: "Write the profile-derived spec (after any --rules merge) to this path as indented JSON — the spec that actually generates, so `synth from-schema` reproduces this run at the same seed"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			profPath := cmd.String("profile")
			source := cmd.String("source")
			output := cmd.String("output")
			rows := int(cmd.Int("rows"))
			seed := cmd.Int("seed")
			fidelityReport := cmd.String("fidelity-report")
			rulesPath := cmd.String("rules")
			emitSpecPath := cmd.String("emit-spec")
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

			// --rules merges BEFORE --emit-spec writes and before
			// generation runs, so the emitted document is the spec that
			// actually generated. The load, the merge and the eager
			// validation all live in synth.ApplyRulesFile; this leaf
			// only supplies the path and routes the refusal, which
			// carries E1-S2's own PULSE_SYNTH_RULE_* code plus the file
			// path in details — writeCodedErrorEnvelope surfaces both
			// rather than flattening them into a placeholder.
			if rulesPath != "" {
				if err := synth.ApplyRulesFile(fs, spec, rulesPath); err != nil {
					return cliCodedError(cmd, jsonOut, "SYNTH_RULES_ERROR", err)
				}
			}
			// Emitted BEFORE generation on purpose: the document's
			// diagnostic value (which models survived translation,
			// which distribution each field reconstructed to, which
			// conditional pairs were retired) is at its highest
			// precisely when the run that follows fails.
			if emitSpecPath != "" {
				if err := synth.WriteSpec(fs, spec, emitSpecPath); err != nil {
					return cliCodedError(cmd, jsonOut, "SYNTH_EMIT_SPEC_ERROR", err)
				}
			}

			p, err := newPulse()
			if err != nil {
				return cliError(cmd, jsonOut, "CLI_ERROR", err.Error())
			}
			// Capture-time thin-cell warnings (prof.Warnings, written to
			// the profile document at `profile create` time) and
			// translation-time conditional-relationship conflict
			// warnings (conflictWarnings, computed just now against the
			// composed Spec — see SpecFromProfile) share one channel
			// into the fidelity report: FidelityWarnings. Capture-time
			// first, translation-time second, matching the order each
			// was produced.
			//
			// These are the two channels that exist BEFORE generation,
			// and they are deliberately still the only two passed here:
			// conflictWarnings was computed against a spec that had not
			// yet been merged with --rules, so it cannot name a
			// relationship a rule retired. The third channel —
			// everything generate() raised, the post-merge arbitration
			// included — is folded onto the report by the facade, which
			// is the only place holding both it and the report path.
			// See mergeFidelityWarnings (synth_fidelity.go) for why the
			// fold lives there rather than being re-derived here.
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
			if emitSpecPath != "" {
				writeText(cmd.Writer, "Derived spec -> %s\n", emitSpecPath)
			}
			if res.FidelityReportPath != "" {
				writeText(cmd.Writer, "Fidelity report -> %s\n", res.FidelityReportPath)
			}
			// The terminal summary spans all THREE warning channels this
			// leaf touches, because each carries findings the others do
			// not: prof.Warnings is capture-time, conflictWarnings is
			// SpecFromProfile translating the document (the channel the
			// v0.32.x model-drop defect hid in), and res.Warnings is
			// generate() compiling the spec. The middle two both derive
			// their conflict lines from resolveConflicts over the same
			// *Spec, so dedupeWarnings collapses that verbatim overlap —
			// see its own doc. Nothing written to a file changes.
			writeModelRecoverySummary(errWriter(cmd), fs, res.FidelityReportPath)
			writeWarningSummary(errWriter(cmd),
				append(append([]string{}, fidelityWarnings...), res.Warnings...),
				fidelityWarningsLocation(res.FidelityReportPath))
			return nil
		},
	}
}

// fidelityWarningsLocation names where a reader finds the unbounded
// warning list for `synth from-profile`.
//
// The fidelity report is the ONLY document that carries all three
// channels (capture, translation, generation — the last folded in by
// the facade, see mergeFidelityWarnings), so without --fidelity-report
// there is no file to point at and the honest answer is the flag that
// would make one — not a path that does not exist. Until E2-S3 this
// pointer was not true even WITH the flag: the report held the first
// two channels only.
func fidelityWarningsLocation(reportPath string) string {
	if reportPath == "" {
		return "re-run with --fidelity-report to capture every line"
	}
	return reportPath + " (.warnings)"
}

// writeModelRecoverySummary prints the one-line headline of the fidelity
// report's model-recovery sections.
//
// The sections themselves are the E5 instrument: per applied model, the
// captured coefficients beside the ones a refit on the generated rows
// recovers. On the motivating cohort they flag 30 of 55 models and 147
// of 1,485 residual pairs — inside a 1.65 MB JSON document, which is a
// finding nobody reads. The line exists so a reader learns that flags
// are there without opening the file.
//
// It re-reads the report rather than having Synth return the counts,
// because synth.Result is the --json payload and a new slot on it would
// move that output. A report that cannot be read or carries no `models`
// section prints nothing: this is a courtesy line and must never be able
// to fail a generation that already succeeded.
func writeModelRecoverySummary(w io.Writer, fsys afero.Fs, reportPath string) {
	if reportPath == "" {
		return
	}
	raw, err := afero.ReadFile(fsys, reportPath)
	if err != nil {
		return
	}
	var rep synth.FidelityReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return
	}
	if len(rep.Models) > 0 {
		flagged := 0
		for _, m := range rep.Models {
			if m != nil && m.Flagged {
				flagged++
			}
		}
		writeText(w, "Model recovery: %d model(s) checked, %d flagged — see %s (.models)\n",
			len(rep.Models), flagged, reportPath)
	}
	if rc := rep.ModelResidualCorrelations; rc != nil && rc.Compared > 0 {
		writeText(w, "Residual recovery: %d pair(s) compared, %d flagged — see %s (.model_residual_correlations)\n",
			rc.Compared, rc.Flagged, reportPath)
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
			&cli.BoolFlag{Name: "fit-models", Usage: "Fit one linear model per numeric field on the same scan — the field regressed on the cohort's categorical levels and set options — keeping the coefficients, residual scale and fitted residuals; a field with no usable predictors or a rank-deficient design is skipped with a warning, never a refusal"},
			&cli.BoolFlag{Name: "residual-correlations", Usage: "Capture the full correlation submatrix among --fit-models' fitted residuals (every pair, not a top-K sample); a pair with too few co-present rows is recorded as unmeasured with a reason, never as rho=0. Requires --fit-models"},
			&cli.IntFlag{Name: "sample-limit", Usage: "Cap rows ingested for the profile (0 = unlimited)"},
			&cli.IntFlag{Name: "seed", Usage: "Deterministic RNG seed for --conditional's categorical-categorical reservoir sampling and --fit-models' residual reservoir (each draws from its own stream)", Value: 0},
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
			fitModels := cmd.Bool("fit-models")
			residualCorrelations := cmd.Bool("residual-correlations")
			sampleLimit := int(cmd.Int("sample-limit"))
			seed := cmd.Int("seed")
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
				FitModels:           fitModels,

				FitResidualCorrelations: residualCorrelations,

				SampleLimit: sampleLimit,
				Seed:        int64(seed),
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
			// Capture-time warnings — thin pairs, shrunk levels,
			// zero-predictor models, skipped models, unmeasured residual
			// pairs — used to reach the terminal nowhere at all, so a
			// capture where something was dropped looked identical to one
			// where nothing was. They stay in the document verbatim; this
			// is a bounded pointer at them.
			writeWarningSummary(errWriter(cmd), prof.Warnings, output+" (.warnings)")
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

// cliCodedError is cliError for a failure that already carries its own
// coded error. It preserves the code and the details map instead of
// stringifying both into a placeholder — a rules-file refusal's value
// is precisely its PULSE_SYNTH_RULE_* code (usable with
// `pulse errors lookup`) and its details, which name the rule index,
// the slot, the field and the file.
//
// On the text path the error is returned unwrapped, so errors.As still
// finds the code, and its own Error() already renders as "CODE: message".
func cliCodedError(cmd *cli.Command, jsonOut bool, fallback string, err error) error {
	if jsonOut {
		return writeCodedErrorEnvelope(cmd.Writer, fallback, err)
	}
	return err
}
