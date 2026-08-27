// Package format dispatches tabular Reader construction by format
// identifier, sitting between the io/ interface definitions and the
// per-format leaf packages (io/csv, io/tsv, io/ndjson, io/jsonarray,
// io/parquet, io/arrow, io/excel, io/spss). It exists as a sub-package of io/
// to avoid an import cycle: io/csv (and siblings) already import io/
// for the Reader / ResetReader interfaces, so the dispatch table
// cannot live in io/ itself.
package format

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
	"github.com/frankbardon/pulse/io/spss"
	"github.com/frankbardon/pulse/io/tsv"
	"github.com/spf13/afero"
)

// Format identifiers for tabular sources Pulse can ingest. The empty
// string represents an unrecognised extension. "pulse" is reserved for
// the engine's native binary format — readers are not constructed for
// it; callers detect it and use it directly.
const (
	CSV       = "csv"
	TSV       = "tsv"
	NDJSON    = "ndjson"
	JSONArray = "jsonarray"
	Parquet   = "parquet"
	Arrow     = "arrow"
	Excel     = "excel"
	Pulse     = "pulse"

	// SPSS is the SPSS system-file format, covering both the `.sav`
	// extension and `.zsav`. Import-only today: NewReader builds a
	// reader for it, and there is deliberately no writer — see
	// errors.PULSE_SPSS_EXPORT_UNSUPPORTED, which the CLI writer
	// dispatch returns instead of a generic unknown-format message so
	// "Pulse cannot write .sav yet" is not confused with "Pulse does
	// not recognise .sav".
	//
	// `.zsav` maps here rather than to its own identifier because it is
	// the same dictionary and the same data section under zlib block
	// compression, not a different format. It resolves to the same
	// reader, and it reads: all three data-section encodings the format
	// defines are decoded — uncompressed, bytecode (the SPSS save
	// default) and ZSAV zlib blocks — so the extension chosen at export
	// time does not decide whether a file can be imported.
	SPSS = "spss"
)

// SupportedImport lists every format NewReader accepts. Excludes "pulse"
// (native, no conversion needed) and the empty string. Order is stable
// for deterministic documentation and CLI help output.
var SupportedImport = []string{
	CSV,
	TSV,
	NDJSON,
	JSONArray,
	Parquet,
	Arrow,
	Excel,
	SPSS,
}

// FromExt returns the canonical format identifier for a file path based
// on its extension. Returns the empty string when the extension is not
// recognised — callers that need an error should wrap with their own
// diagnostic.
func FromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".csv":
		return CSV
	case ".tsv":
		return TSV
	case ".ndjson", ".jsonl":
		return NDJSON
	case ".json":
		return JSONArray
	case ".parquet", ".pq":
		return Parquet
	case ".arrow", ".feather":
		return Arrow
	case ".xlsx", ".xls":
		return Excel
	case ".sav", ".zsav":
		return SPSS
	case ".pulse":
		return Pulse
	default:
		return ""
	}
}

// ReaderOptions modulate reader construction. Every field is honoured by
// exactly one format and ignored silently by the rest: a single options
// struct keeps the dispatch signature stable as formats gain knobs, which is
// why Charset joins Sheet here rather than arriving as a parallel mechanism.
type ReaderOptions struct {
	// Sheet names the Excel worksheet to read. Empty selects the first.
	// Ignored by every other format.
	Sheet string

	// Charset overrides the character encoding an SPSS `.sav` declares
	// about itself, resolved by the same lookup the file's own record
	// 7/20 name goes through ("windows-1252", "cp1252" and "1252" are one
	// request). Empty leaves the file's declaration in force.
	//
	// It is the ONLY recourse for a file that is wrong about itself, and
	// there are two common shapes: a dictionary transcoded by one tool and
	// re-saved by another keeps a stale 7/20 name, and a pre-Unicode file
	// declares nothing at all — the latter reads as strict UTF-8 by
	// default and fails PULSE_SPSS_CHARSET_INVALID on its first 8-bit
	// byte. Both are decisions only the caller can make, because the file
	// has no further evidence to offer.
	//
	// Decoding only. The file's own declaration is still retained
	// verbatim. Ignored by every format other than SPSS.
	Charset string
}

// NewReader constructs a tabular Reader for the given format, reading
// from path on fs. The returned Reader implements pio.ResetReader for
// every supported format, so schema inference + import is always
// available. Returns an error for the native pulse format (no tabular
// reader needed) and for unknown / empty formats.
func NewReader(format string, fs afero.Fs, path string, opts ReaderOptions) (pio.Reader, error) {
	switch format {
	case CSV:
		return csv.NewReader(fs, path), nil
	case TSV:
		return tsv.NewReader(fs, path), nil
	case NDJSON:
		return ndjson.NewReader(fs, path), nil
	case JSONArray:
		return jsonarray.NewReader(fs, path), nil
	case Parquet:
		return parquet.NewReader(fs, path), nil
	case Arrow:
		return parrow.NewReader(fs, path), nil
	case Excel:
		var excelOpts []excel.Option
		if opts.Sheet != "" {
			excelOpts = append(excelOpts, excel.WithSheet(opts.Sheet))
		}
		return excel.NewReader(fs, path, excelOpts...), nil
	case SPSS:
		var spssOpts []spss.Option
		if opts.Charset != "" {
			spssOpts = append(spssOpts, spss.WithCharset(opts.Charset))
		}
		return spss.NewReader(fs, path, spssOpts...), nil
	case Pulse:
		return nil, fmt.Errorf("format.NewReader: %q is the native format, no tabular reader needed", format)
	case "":
		return nil, fmt.Errorf("format.NewReader: empty format identifier")
	default:
		return nil, fmt.Errorf("format.NewReader: unsupported format %q", format)
	}
}
