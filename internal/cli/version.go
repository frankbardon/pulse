package cli

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/buildinfo"
	cli "github.com/urfave/cli/v3"
)

// VersionInfo is the `data` payload of `pulse version --json`. Every field
// is read from pulse.Version() or the internal/buildinfo helpers; the CLI
// only assembles them. Commit and CommitTime are omitted when the binary
// carries no VCS metadata.
type VersionInfo struct {
	PulseVersion  string `json:"pulse_version"`
	GoVersion     string `json:"go_version"`
	Commit        string `json:"commit,omitempty"`
	CommitTime    string `json:"commit_time,omitempty"`
	FormatVersion string `json:"format_version"`
}

// currentVersionInfo gathers the build facts reported by `pulse version`.
// FormatVersion is the envelope contract version, taken from
// descriptor.NewEnvelope so it can never drift from the real envelope.
func currentVersionInfo() VersionInfo {
	return VersionInfo{
		PulseVersion:  pulse.Version(),
		GoVersion:     buildinfo.GoVersion(),
		Commit:        buildinfo.Commit(),
		CommitTime:    buildinfo.CommitTime(),
		FormatVersion: descriptor.NewEnvelope(nil).FormatVersion,
	}
}

// VersionCommand returns the `pulse version` leaf. Plain output is
// `pulse <version>`; --json wraps a VersionInfo in the standard envelope.
func VersionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print the Pulse build version (--json adds Go version, commit and envelope format_version)",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output build facts as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			info := currentVersionInfo()
			if cmd.Bool("json") {
				return WriteJSONPublic(cmd.Writer, descriptor.NewEnvelope(info))
			}
			_, err := fmt.Fprintf(cmd.Writer, "pulse %s\n", info.PulseVersion)
			return err
		},
	}
}
