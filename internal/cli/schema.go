package cli

import (
	"context"

	"github.com/frankbardon/pulse/descriptor"
	cli "github.com/urfave/cli/v3"
)

// SchemaCommand returns the schema command. It prints the bundled JSON
// Schema (draft 2020-12) describing every public Pulse payload — the
// request envelopes and the universal output Envelope. The artifact is a
// self-contained JSON Schema document (it carries its own $schema/$id),
// so it is emitted raw rather than wrapped in the descriptor Envelope.
//
// This is the offline equivalent of fetching the published file at the
// $id URL or the pulse://schema MCP resource. CLI is a thin adapter: the
// schema is built by descriptor.BuildPayloadSchema().
func SchemaCommand() *cli.Command {
	return &cli.Command{
		Name:  "schema",
		Usage: "Output the JSON Schema for Pulse request/response payloads",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			out := descriptor.BuildPayloadSchema()
			if _, err := cmd.Writer.Write(out); err != nil {
				return err
			}
			_, err := cmd.Writer.Write([]byte("\n"))
			return err
		},
	}
}
