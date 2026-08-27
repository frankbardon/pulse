package cli

import (
	"fmt"

	pio "github.com/frankbardon/pulse/io"
	parrow "github.com/frankbardon/pulse/io/arrow"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/frankbardon/pulse/io/excel"
	pformat "github.com/frankbardon/pulse/io/format"
	"github.com/frankbardon/pulse/io/jsonarray"
	"github.com/frankbardon/pulse/io/ndjson"
	"github.com/frankbardon/pulse/io/parquet"
	"github.com/frankbardon/pulse/io/spss"
	"github.com/frankbardon/pulse/io/tsv"
	"github.com/spf13/afero"
)

// formatFromExt detects the format from a file extension.
// Thin shim over pformat.FromExt for back-compat with existing CLI
// call sites; new code should call pformat.FromExt directly.
func formatFromExt(path string) string {
	return pformat.FromExt(path)
}

// newReaderForFormat creates a reader for the given format.
// Thin shim over pformat.NewReader; the options struct is passed through
// rather than flattened, so a format gaining a knob does not move this
// signature.
func newReaderForFormat(format string, fs afero.Fs, path string, opts pformat.ReaderOptions) (pio.Reader, error) {
	return pformat.NewReader(format, fs, path, opts)
}

// writerOptions is the per-format knob bag the CLI's writer dispatch
// passes through, mirroring pformat.ReaderOptions on the read side.
//
// It exists because writer dispatch does NOT route through io/format —
// readers do, writers live in this file's own switch — so there is no
// shared options struct to hang a flag on. A leaf that does not declare
// a flag reads it as the zero value, which is the same as not setting
// the option, so one struct serves every leaf.
type writerOptions struct {
	// SPSS carries the `.sav` writer's knobs verbatim. The four CLI
	// flags map onto spss.WriterOptions one for one; see the struct's
	// own documentation for what each means.
	SPSS spss.WriterOptions
}

// newWriterForFormat creates a writer for the given format. Writers
// stay CLI-local for now — managed imports only need readers.
func newWriterForFormat(format string, fs afero.Fs, path string, opts writerOptions) (pio.Writer, error) {
	switch format {
	case pformat.CSV:
		return csv.NewWriter(fs, path), nil
	case pformat.TSV:
		return tsv.NewWriter(fs, path), nil
	case pformat.NDJSON:
		return ndjson.NewWriter(fs, path), nil
	case pformat.JSONArray:
		return jsonarray.NewWriter(fs, path), nil
	case pformat.Parquet:
		return parquet.NewWriter(fs, path), nil
	case pformat.Arrow:
		return parrow.NewWriter(fs, path), nil
	case pformat.Excel:
		return excel.NewWriter(fs, path), nil
	case pformat.SPSS:
		// SPSS was import-only until E5-S6 mounted the writer. The arm
		// that used to answer PULSE_SPSS_EXPORT_UNSUPPORTED here is gone;
		// the code survives, repurposed, for the things inside a cohort
		// that genuinely have no `.sav` form. See errors/codes.go.
		return spss.NewWriter(fs, path, opts.SPSS), nil
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}
