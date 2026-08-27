package cli

import (
	"fmt"

	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	parrow "github.com/frankbardon/pulse/io/arrow"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/frankbardon/pulse/io/excel"
	pformat "github.com/frankbardon/pulse/io/format"
	"github.com/frankbardon/pulse/io/jsonarray"
	"github.com/frankbardon/pulse/io/ndjson"
	"github.com/frankbardon/pulse/io/parquet"
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

// newWriterForFormat creates a writer for the given format. Writers
// stay CLI-local for now — managed imports only need readers.
func newWriterForFormat(format string, fs afero.Fs, path string) (pio.Writer, error) {
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
		// SPSS is import-only today: io/format registers a reader and
		// there is no writer. Answer with the specific code rather than
		// falling through to the generic "unsupported format" below —
		// the extension IS recognised (that is why we are in this case
		// arm at all), so "Pulse does not know what .sav is" would be
		// actively misleading, and the fixups point at the two things a
		// caller might have meant: a writable target format, or the
		// .sav on the input side.
		return nil, perrors.NewCodedErrorWithDetails(
			perrors.PULSE_SPSS_EXPORT_UNSUPPORTED,
			"SPSS (.sav / .zsav) is an import-only format; Pulse cannot write it yet",
			map[string]any{"format": format, "output_path": path})
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}
