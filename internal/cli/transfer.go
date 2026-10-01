package cli

import (
	"context"

	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// exportTransferCmd is `pulse export transfer`: compress a cohort's exact
// bytes into a zstd transfer artifact. It is a leaf of `export` because
// it is the outbound half of moving a cohort, not a tabular target, so
// it takes none of the tabular flags (--include, --labels).
func exportTransferCmd() *cli.Command {
	return &cli.Command{
		Name:  "transfer",
		Usage: "Compress a .pulse cohort or shard archive into a zstd transfer artifact (.pulse.zst); transport only — decompress with `pulse import transfer` before opening",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse cohort or shard archive", Required: true},
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output transfer artifact path (conventionally <input>.zst); overwritten"},
			&cli.IntFlag{Name: "level", Usage: "zstd level 1..22 (1-2 fastest, 3-5 default, 6-9 better, 10-22 best)", Value: pio.DefaultTransferLevel},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			input := cmd.String("input")
			output := cmd.String("output")
			if output == "" {
				output = input + pio.TransferExtension
			}
			job := &pio.TransferExportJob{FS: afero.NewOsFs(), Source: input, Output: output, Level: int(cmd.Int("level"))}
			report, err := job.Run(ctx)
			return finishTransfer(cmd, "EXPORT_ERROR", "Compressed", report, err)
		},
	}
}

// importTransferCmd is `pulse import transfer`: decompress a transfer
// artifact back into a byte-identical .pulse at rest.
func importTransferCmd() *cli.Command {
	return importLeaf(&cli.Command{
		Name:  "transfer",
		Usage: "Decompress a zstd transfer artifact (.pulse.zst) into a byte-identical .pulse at rest",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "input", Aliases: []string{"i"}, Usage: "Input .pulse.zst transfer artifact", Required: true},
			&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Output .pulse path (must not exist unless --overwrite)", Required: true},
			&cli.BoolFlag{Name: "overwrite", Usage: "Replace an existing output cohort"},
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			job := &pio.TransferImportJob{FS: afero.NewOsFs(), Source: cmd.String("input"), Output: cmd.String("output"), Overwrite: cmd.Bool("overwrite")}
			report, err := job.Run(ctx)
			return finishTransfer(cmd, "IMPORT_ERROR", "Decompressed", report, err)
		},
	})
}

// finishTransfer emits a transfer leaf's result. A fatal coded error keeps
// its own code on the envelope (writeCodedErrorEnvelope); fallback names
// the leaf placeholder used only for an uncoded error.
func finishTransfer(cmd *cli.Command, fallback, verb string, r *pio.TransferReport, err error) error {
	jsonOut := cmd.Bool("json")
	if err != nil {
		if jsonOut {
			return writeCodedErrorEnvelope(cmd.Writer, fallback, err)
		}
		return err
	}
	if jsonOut {
		return writeEnvelope(cmd.Writer, r)
	}
	writeText(cmd.Writer, "%s %s -> %s (%s, cohort %d B, artifact %d B, %.2fx)\nsha256 %s\n",
		verb, r.Source, r.Output, r.Layout, r.CohortBytes, r.CompressedBytes, r.Ratio, r.SHA256)
	return nil
}
