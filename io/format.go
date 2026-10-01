package io

import (
	"path/filepath"
	"strings"
)

// Format identifies a tabular file format the io factory can build a
// Reader or Writer for, plus the native "pulse" cohort format, which
// FormatFromPath recognises and every factory refuses.
//
// The values are the stable lower-case identifiers the CLI's --format
// flags, internal/imports.Spec.Format and the manifest Import block use, so a
// Format converts to and from those strings losslessly.
type Format string

// The tabular formats the factory builds, and the native format it refuses.
const (
	FormatCSV       Format = "csv"
	FormatTSV       Format = "tsv"
	FormatNDJSON    Format = "ndjson"
	FormatJSONArray Format = "jsonarray"
	FormatParquet   Format = "parquet"
	FormatArrow     Format = "arrow"
	FormatExcel     Format = "excel"

	// FormatSPSS is the SPSS system file, covering both `.sav` and
	// `.zsav`: the same dictionary and data section, zsav adding zlib
	// block compression. All three data-section encodings read (flat,
	// bytecode, zlib); the writer emits flat or bytecode.
	FormatSPSS Format = "spss"

	// FormatPulse is the engine's native cohort format. FormatFromPath
	// recognises it so a caller can branch on it, but no tabular Reader
	// or Writer exists for it: every factory refuses it with
	// PULSE_IO_FORMAT_UNSUPPORTED. Open a cohort directly instead.
	FormatPulse Format = "pulse"
)

// formats is the stable order Formats reports: the order documentation
// and CLI help enumerate.
var formats = []Format{
	FormatCSV,
	FormatTSV,
	FormatNDJSON,
	FormatJSONArray,
	FormatParquet,
	FormatArrow,
	FormatExcel,
	FormatSPSS,
}

// Formats returns every tabular format the factory constructs, in a
// stable order. It excludes FormatPulse. The slice is a fresh copy.
func Formats() []Format {
	return append([]Format(nil), formats...)
}

// String returns the format identifier.
func (f Format) String() string { return string(f) }

// CanRead reports whether NewReader / NewReaderFromBytes build a Reader
// for f. False for FormatPulse, the empty Format and unknown identifiers.
func (f Format) CanRead() bool { return f.tabular() }

// CanWrite reports whether NewWriter / NewWriterToBuffer build a Writer
// for f. False for FormatPulse, the empty Format and unknown identifiers.
func (f Format) CanWrite() bool { return f.tabular() }

func (f Format) tabular() bool {
	for _, k := range formats {
		if f == k {
			return true
		}
	}
	return false
}

// FormatFromPath returns the Format a file path's extension names, case
// folded: `.csv`, `.tsv`, `.ndjson` / `.jsonl`, `.json` (a JSON array),
// `.parquet` / `.pq`, `.arrow` / `.feather`, `.xlsx` / `.xls`, `.sav` /
// `.zsav` and `.pulse`. An unrecognised or missing extension returns the
// empty Format; callers that need an error wrap it with their own
// diagnostic.
//
// `.xls` maps to FormatExcel although the Excel reader decodes only the
// xlsx container: the mapping is the historical dispatch, and a legacy
// `.xls` fails at read time with the reader's own error.
func FormatFromPath(path string) Format {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		return FormatCSV
	case ".tsv":
		return FormatTSV
	case ".ndjson", ".jsonl":
		return FormatNDJSON
	case ".json":
		return FormatJSONArray
	case ".parquet", ".pq":
		return FormatParquet
	case ".arrow", ".feather":
		return FormatArrow
	case ".xlsx", ".xls":
		return FormatExcel
	case ".sav", ".zsav":
		return FormatSPSS
	case ".pulse":
		return FormatPulse
	default:
		return ""
	}
}
