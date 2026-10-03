package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/profilefile"
	cli "github.com/urfave/cli/v3"
)

// featuresErrorFallback is the envelope placeholder for an uncoded
// failure on a `pulse features` leaf. Every library refusal is coded.
const featuresErrorFallback = "FEATURES_ERROR"

// FeaturesCommand returns the `pulse features` group: thin adapters over
// the root feature-profile tooling (pulse.InitFeatureProfile,
// CheckFeatureProfile, DiffFeatureProfile, DescribeFeatureProfile). They
// describe the binary, not an instance, and hold no logic of their own.
//
// A fatal error exits non-zero on both paths; under --json the envelope
// is written first, with the error's own code in errors[0].
func FeaturesCommand() *cli.Command {
	return &cli.Command{
		Name:  "features",
		Usage: "Write, check, diff and describe feature profiles (an instance's allowlist of operators and capabilities)",
		Description: "Feature profiles are JSON files naming the features a Pulse instance offers; " +
			"pulse mcp --feature-profile serves one. These leaves need no data directory: " +
			"they check a feature profile against this binary. Profile paths are host OS paths.",
		Commands: []*cli.Command{
			featuresInitCommand(),
			featuresCheckCommand(),
			featuresDiffCommand(),
			featuresShowCommand(),
		},
	}
}

func featuresJSONFlag() cli.Flag {
	return &cli.BoolFlag{Name: "json", Usage: "Output the result as a JSON envelope"}
}

func featuresInitCommand() *cli.Command {
	return &cli.Command{
		Name:  "init",
		Usage: "Print a feature profile listing every feature this build offers, or seeded from an example feature profile",
		Description: "Writes strict feature-profile JSON to stdout, stamped with written_with. " +
			"--from NAME starts from a published example feature profile instead (" +
			strings.Join(pulse.ExampleFeatureProfiles(), ", ") + ").",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "from", Usage: "Seed from the named example feature profile"},
			featuresJSONFlag(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			fp, err := pulse.InitFeatureProfile(cmd.String("from"))
			if err != nil {
				return featuresFail(cmd, jsonOut, nil, nil, err)
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, fp)
			}
			return writeJSON(cmd.Writer, fp)
		},
	}
}

func featuresCheckCommand() *cli.Command {
	return &cli.Command{
		Name:      "check",
		Usage:     "Validate a feature profile file against this build, exactly as pulse.New would",
		ArgsUsage: "FILE",
		Description: "Runs the structural, name and dependency checks in order and exits non-zero on the first failing class. " +
			"The check is offline: a name that follows the extension naming policy but is not registered here is " +
			"reported as a warning (unverified extension name) rather than an error; check it in-process.",
		Flags: []cli.Flag{featuresJSONFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			fp, err := featuresReadProfile(cmd)
			if err != nil {
				return featuresFail(cmd, jsonOut, nil, nil, err)
			}
			report, err := pulse.CheckFeatureProfile(fp, pulse.FeatureProfileCheckOptions{Offline: true})
			if err != nil {
				return featuresFail(cmd, jsonOut, report, report.Warnings, err)
			}
			if jsonOut {
				env := descriptor.NewEnvelope(report)
				env.Warnings = append(env.Warnings, report.Warnings...)
				return writeJSON(cmd.Writer, env)
			}
			writeEnvelopeEntries(cmd.Writer, report.Warnings)
			writeText(cmd.Writer, "feature profile %s: valid (%d features, checked against pulse %s)\n",
				featuresLabel(cmd.Args().First(), report.Profile), report.Features, report.Version)
			return nil
		},
	}
}

func featuresDiffCommand() *cli.Command {
	return &cli.Command{
		Name:      "diff",
		Usage:     "List the features this build offers that a feature profile does not list, and the names it does not resolve",
		ArgsUsage: "FILE",
		Description: "A missing feature is flagged new when it was introduced after the profile's written_with. " +
			"Unknown names are reported, not fatal.",
		Flags: []cli.Flag{featuresJSONFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			fp, err := featuresReadProfile(cmd)
			if err != nil {
				return featuresFail(cmd, jsonOut, nil, nil, err)
			}
			diff, err := pulse.DiffFeatureProfile(fp)
			if err != nil {
				return featuresFail(cmd, jsonOut, nil, nil, err)
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, diff)
			}
			w := cmd.Writer
			writeText(w, "feature profile %s (written with %s) against pulse %s\n",
				featuresLabel(cmd.Args().First(), diff.Profile), orNone(diff.WrittenWith), diff.Version)
			writeText(w, "missing (%d):\n", len(diff.Missing))
			for _, m := range diff.Missing {
				line := "  " + m.Name
				if m.Since != "" {
					line += "  since " + m.Since
				}
				if m.New {
					line += "  [new]"
				}
				writeText(w, "%s\n", line)
			}
			writeText(w, "unknown (%d):\n", len(diff.Unknown))
			for _, u := range diff.Unknown {
				writeText(w, "  %s\n", unknownLine(u))
			}
			return nil
		},
	}
}

func featuresShowCommand() *cli.Command {
	return &cli.Command{
		Name:      "show",
		Usage:     "Describe every feature a feature profile lists: kind, category, source, since and dependencies",
		ArgsUsage: "FILE",
		Flags:     []cli.Flag{featuresJSONFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			fp, err := featuresReadProfile(cmd)
			if err != nil {
				return featuresFail(cmd, jsonOut, nil, nil, err)
			}
			desc, err := pulse.DescribeFeatureProfile(fp)
			if err != nil {
				return featuresFail(cmd, jsonOut, nil, nil, err)
			}
			if jsonOut {
				return writeEnvelope(cmd.Writer, desc)
			}
			w := cmd.Writer
			writeText(w, "feature profile %s (written with %s) against pulse %s: %d features\n",
				featuresLabel(cmd.Args().First(), desc.Profile), orNone(desc.WrittenWith), desc.Version, len(desc.Features))
			for _, f := range desc.Features {
				kind := f.Kind
				if f.Category != "" {
					kind += "/" + f.Category
				}
				line := fmt.Sprintf("  %s  %s  %s", f.Name, kind, f.Source)
				if f.Since != "" {
					line += "  since " + f.Since
				}
				if len(f.DependsOn) > 0 {
					groups := make([]string, len(f.DependsOn))
					for i, g := range f.DependsOn {
						groups[i] = strings.Join(g, "|")
					}
					line += "  depends on " + strings.Join(groups, ", ")
				}
				if f.Unknown != nil {
					line += "  (" + unknownLine(*f.Unknown) + ")"
				}
				writeText(w, "%s\n", line)
			}
			return nil
		},
	}
}

// featuresReadProfile reads the FILE argument as a host OS path through
// the same reader as `pulse mcp --feature-profile` (profilefile.ReadOS).
func featuresReadProfile(cmd *cli.Command) (*pulse.FeatureProfile, error) {
	path := cmd.Args().First()
	if path == "" {
		return nil, perrors.NewCodedError(perrors.CLI_INPUT,
			fmt.Sprintf("usage: pulse features %s FILE (a feature profile JSON file)", cmd.Name))
	}
	return profilefile.ReadOS(path)
}

// featuresFail reports a fatal error and returns it, so the process
// exits non-zero. Under --json the envelope (data, warnings, and the
// error with its own code) is written first.
func featuresFail(cmd *cli.Command, jsonOut bool, data any, warnings []*descriptor.EnvelopeEntry, err error) error {
	if jsonOut {
		env := codedErrorEnvelope(data, featuresErrorFallback, err)
		env.Warnings = append(env.Warnings, warnings...)
		if werr := writeJSON(cmd.Writer, env); werr != nil {
			return werr
		}
		return err
	}
	writeEnvelopeEntries(cmd.Writer, warnings)
	return err
}

// writeEnvelopeEntries prints one "Warning [CODE]: message" line per
// entry on the text path.
func writeEnvelopeEntries(w io.Writer, entries []*descriptor.EnvelopeEntry) {
	for _, e := range entries {
		if e != nil {
			writeText(w, "Warning [%s]: %s\n", e.Code, e.Message)
		}
	}
}

func featuresLabel(path, name string) string {
	if name == "" {
		return path
	}
	return fmt.Sprintf("%s (%s)", path, name)
}

func orNone(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func unknownLine(u pulse.FeatureProfileUnknownName) string {
	line := u.Name + ": " + u.Reason
	if u.DidYouMean != "" {
		line += ", did you mean " + u.DidYouMean
	}
	if u.Since != "" {
		line += ", since " + u.Since
	}
	return line
}
