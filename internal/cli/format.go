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
// Thin shim over pformat.NewReader preserving the CLI's
// (format, fs, path, sheet) call signature.
func newReaderForFormat(format string, fs afero.Fs, path string, sheet string) (pio.Reader, error) {
	return pformat.NewReader(format, fs, path, pformat.ReaderOptions{Sheet: sheet})
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
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}
