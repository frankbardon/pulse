package cli

import (
	"context"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/profilefile"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// docsErrorFallback is the envelope placeholder for an uncoded failure
// on a `pulse docs` leaf. Every library refusal is coded.
const docsErrorFallback = "DOCS_ERROR"

// DocsExportResult is the --json data payload of `pulse docs export`.
type DocsExportResult struct {
	// Out is the --out directory, as given.
	Out string `json:"out"`
	// FeatureProfile is the --feature-profile path, empty for the full
	// default instance.
	FeatureProfile string `json:"feature_profile,omitempty"`
	// Skills reports whether the skill pages were exported.
	Skills bool `json:"skills"`
}

// DocsCommand returns the `pulse docs` group. Its single leaf, export,
// is a thin adapter over (*pulse.Pulse).ExportReference: it builds an
// instance over an in-memory filesystem (no data directory is read, so
// PULSE_DATA_DIR is not needed), optionally under a feature profile
// read from a host OS path, and writes the reference to --out on the OS
// filesystem.
func DocsCommand() *cli.Command {
	return &cli.Command{
		Name:  "docs",
		Usage: "Export the analysis reference (operator catalog, reading pages, glossary, skills) as Markdown",
		Commands: []*cli.Command{
			docsExportCommand(),
		},
	}
}

func docsExportCommand() *cli.Command {
	return &cli.Command{
		Name:  "export",
		Usage: "Write the instance's analysis reference as a deterministic Markdown tree with an mdBook SUMMARY.md fragment",
		Description: "Needs no data directory. --feature-profile scopes the reference to a feature profile " +
			"(a host OS path, read as pulse mcp --feature-profile reads it); PULSE_FEATURE_PROFILE is not consulted. " +
			"--out must be missing, empty, or a directory a previous export marked with .pulse-docs-export; " +
			"any other non-empty directory is refused with PULSE_DOCS_EXPORT_DIR_NOT_EMPTY.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "out", Usage: "Directory to write the reference into (host OS path)", Required: true},
			&cli.StringFlag{Name: "feature-profile", Usage: "Scope the reference to the feature profile at this host OS path"},
			&cli.BoolFlag{Name: "no-skills", Usage: "Leave out skills.md and the skills/ tree"},
			&cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			res := DocsExportResult{
				Out:            cmd.String("out"),
				FeatureProfile: cmd.String("feature-profile"),
				Skills:         !cmd.Bool("no-skills"),
			}
			if err := docsExport(ctx, res); err != nil {
				return docsFail(cmd, jsonOut, err)
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, res)
			}
			writeText(cmd.Writer, "exported the analysis reference to %s\n", res.Out)
			return nil
		},
	}
}

// docsExport builds the instance and calls ExportReference.
func docsExport(ctx context.Context, res DocsExportResult) error {
	if res.Out == "" {
		return perrors.NewCodedError(perrors.CLI_INPUT, "usage: pulse docs export --out DIR (a non-empty directory path)")
	}
	opts := pulse.Options{FS: afero.NewMemMapFs()}
	if res.FeatureProfile != "" {
		fp, err := profilefile.ReadOS(res.FeatureProfile)
		if err != nil {
			return err
		}
		opts.FeatureProfile = fp
	}
	p, err := pulse.New(withLogger(ctx, opts))
	if err != nil {
		return err
	}
	return p.ExportReference(afero.NewOsFs(), res.Out, pulse.ExportReferenceOptions{OmitSkills: !res.Skills})
}

// docsFail reports a fatal error and returns it, so the process exits
// non-zero. Under --json the envelope carries the error's own code.
func docsFail(cmd *cli.Command, jsonOut bool, err error) error {
	if jsonOut {
		if werr := writeCodedErrorEnvelope(cmd.Writer, docsErrorFallback, err); werr != nil {
			return werr
		}
	}
	return err
}
