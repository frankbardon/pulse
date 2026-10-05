package descriptor

import "github.com/frankbardon/pulse/descriptor"

// exportCapability returns the canonical ExportCapability entry. The
// per-format overlay-embedding labels mirror the dispatcher wiring
// (Arrow + Parquet sidecar, Excel sheets, NDJSON trailing block, CSV /
// TSV / SPSS warn-and-skip). jsonarray and tsv share the warn-and-skip
// shape with CSV — neither format carries an extension surface beyond
// the host stream so embedded overlays would not round-trip through a
// vanilla reader.
//
// SPSS is warn-and-skip for a different reason: a `.sav` DOES have an
// extension surface (record type 7 subtypes), but every subtype in it is
// specified, and a reader meeting an unknown one is entitled to ignore
// it. An overlay layer smuggled into a private subtype would be dropped
// by every tool that opens the file, which is warn-and-skip with extra
// steps. The `.sav` writer also encodes from the cohort's raw storage
// rather than from the rendered row stream, so it is the one target for
// which overlays never reach the adapter at all.
//
// TimeZone mirrors the --tz contract: every row-stream format renders
// local offset literals, and the cohort-path `.sav` writer refuses a
// non-UTC zone (TestManifestExportCapability_TimeZone pins it against
// the real writers' behaviour).
//
// Sorted alphabetically by Name so the golden manifest stays stable.
func exportCapability() descriptor.ExportCapability {
	return descriptor.ExportCapability{
		Formats: []descriptor.ExportFormatCapability{
			{Name: "arrow", OverlaySupport: "sidecar", TimeZone: "local_offset"},
			{Name: "csv", OverlaySupport: "warn_and_skip", TimeZone: "local_offset"},
			{Name: "excel", OverlaySupport: "sheets", TimeZone: "local_offset"},
			{Name: "jsonarray", OverlaySupport: "warn_and_skip", TimeZone: "local_offset"},
			{Name: "ndjson", OverlaySupport: "trailing_block", TimeZone: "local_offset"},
			{Name: "parquet", OverlaySupport: "sidecar", TimeZone: "local_offset"},
			{Name: "spss", OverlaySupport: "warn_and_skip", TimeZone: "refused"},
			{Name: "tsv", OverlaySupport: "warn_and_skip", TimeZone: "local_offset"},
		},
	}
}

// importCapability returns the canonical ImportCapability entry.
//
// The table is hand-declared rather than derived from
// io.Formats() because descriptor/ is the no-execute layer:
// importing the io factory would drag the arrow, parquet and excel adapters
// into every manifest build for the sake of a list of seven strings.
// TestManifestImportCapability_MatchesFormatRegistry pins the two
// against each other so the hand-declaration cannot drift.
//
// Sorted alphabetically by Name so the golden manifest stays stable.
func importCapability() descriptor.ImportCapability {
	return descriptor.ImportCapability{
		Formats: []descriptor.ImportFormatCapability{
			{Name: "arrow", Extensions: []string{".arrow", ".feather"}, SchemaSource: "inferred", Export: true},
			{Name: "csv", Extensions: []string{".csv"}, SchemaSource: "inferred", Export: true},
			{Name: "excel", Extensions: []string{".xlsx", ".xls"}, SchemaSource: "inferred", Export: true},
			{Name: "jsonarray", Extensions: []string{".json"}, SchemaSource: "inferred", Export: true},
			{Name: "ndjson", Extensions: []string{".ndjson", ".jsonl"}, SchemaSource: "inferred", Export: true},
			{Name: "parquet", Extensions: []string{".parquet", ".pq"}, SchemaSource: "inferred", Export: true},
			{Name: "spss", Extensions: []string{".sav", ".zsav"}, SchemaSource: "authoritative", Export: true},
			{Name: "tsv", Extensions: []string{".tsv"}, SchemaSource: "inferred", Export: true},
		},
	}
}
