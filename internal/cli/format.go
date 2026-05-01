package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	pio "github.com/frankbardon/pulse/io"
	parrow "github.com/frankbardon/pulse/io/arrow"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/frankbardon/pulse/io/excel"
	"github.com/frankbardon/pulse/io/jsonarray"
	"github.com/frankbardon/pulse/io/ndjson"
	"github.com/frankbardon/pulse/io/parquet"
	"github.com/frankbardon/pulse/io/tsv"
	"github.com/spf13/afero"
)

// formatFromExt detects the format from a file extension.
func formatFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".csv":
		return "csv"
	case ".tsv":
		return "tsv"
	case ".ndjson", ".jsonl":
		return "ndjson"
	case ".json":
		return "jsonarray"
	case ".parquet", ".pq":
		return "parquet"
	case ".arrow", ".feather":
		return "arrow"
	case ".xlsx", ".xls":
		return "excel"
	case ".pulse":
		return "pulse"
	default:
		return ""
	}
}

// newReaderForFormat creates a reader for the given format.
func newReaderForFormat(format string, fs afero.Fs, path string, sheet string) (pio.Reader, error) {
	switch format {
	case "csv":
		return csv.NewReader(fs, path), nil
	case "tsv":
		return tsv.NewReader(fs, path), nil
	case "ndjson":
		return ndjson.NewReader(fs, path), nil
	case "jsonarray":
		return jsonarray.NewReader(fs, path), nil
	case "parquet":
		return parquet.NewReader(fs, path), nil
	case "arrow":
		return parrow.NewReader(fs, path), nil
	case "excel":
		opts := []excel.Option{}
		if sheet != "" {
			opts = append(opts, excel.WithSheet(sheet))
		}
		return excel.NewReader(fs, path, opts...), nil
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

// newWriterForFormat creates a writer for the given format.
func newWriterForFormat(format string, fs afero.Fs, path string) (pio.Writer, error) {
	switch format {
	case "csv":
		return csv.NewWriter(fs, path), nil
	case "tsv":
		return tsv.NewWriter(fs, path), nil
	case "ndjson":
		return ndjson.NewWriter(fs, path), nil
	case "jsonarray":
		return jsonarray.NewWriter(fs, path), nil
	case "parquet":
		return parquet.NewWriter(fs, path), nil
	case "arrow":
		return parrow.NewWriter(fs, path), nil
	case "excel":
		return excel.NewWriter(fs, path), nil
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}
