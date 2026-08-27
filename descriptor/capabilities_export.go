package descriptor

// ExportFormatCapability describes the per-format export envelope
// surfaced on the manifest. One entry per format the engine's export
// dispatch supports, carrying the format identifier plus the per-format
// overlay-embedding strategy LLM planners need to decide whether a
// Response.Overlays slice will round-trip through an ExportJob to the
// chosen target.
//
// OverlaySupport is the canonical embedding-shape label declared by
// research/export-embedding-shape.md:
//
//   - "sidecar"        — the format carries a top-level
//     LIST<STRUCT> column-family appended to the
//     host stream (Arrow / Parquet). The host
//     record stream is byte-identical to the
//     overlay-free shape; readers that ignore the
//     column family see the unchanged host.
//   - "sheets"         — one workbook sheet per overlay layer named
//     "__overlay_<layer_name>" (Excel). The host
//     workbook sheet is unchanged from the overlay-
//     free shape.
//   - "trailing_block" — a single trailing line `{"_overlays": [...]}`
//     after the last host record (NDJSON). The
//     host stream is byte-identical to the overlay-
//     free shape until the trailer.
//   - "warn_and_skip"  — the format cannot embed overlays at all (CSV /
//     TSV / SPSS). The dispatcher emits one
//     PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED warning
//     and writes the host body verbatim; no overlay
//     output lands. Setting IncludeOverlays=false
//     suppresses the warning while keeping the same
//     body.
type ExportFormatCapability struct {
	// Name is the canonical format identifier matching the
	// io/format package constants (csv / tsv / ndjson / jsonarray /
	// parquet / arrow / excel). Stable across format-version 1.0;
	// new formats land additively.
	Name string `json:"name"`

	// OverlaySupport names the per-format overlay-embedding strategy
	// the export dispatcher follows when ExportJob.IncludeOverlays
	// resolves to true. See ExportFormatCapability godoc for the
	// canonical label vocabulary.
	OverlaySupport string `json:"overlay_support"`
}

// ExportCapability is the cross-format export envelope. Carries the
// alphabetised per-format slice so LLM planners can pick a target
// format aware of its overlay-embedding shape without inspecting the
// io/ packages.
type ExportCapability struct {
	// Formats enumerates every format the export dispatcher supports,
	// sorted alphabetically by Name for deterministic golden output.
	// One entry per format. Empty slice never appears — Pulse always
	// ships at least the canonical seven adapters.
	Formats []ExportFormatCapability `json:"formats"`
}

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
// Sorted alphabetically by Name so the golden manifest stays stable.
func exportCapability() ExportCapability {
	return ExportCapability{
		Formats: []ExportFormatCapability{
			{Name: "arrow", OverlaySupport: "sidecar"},
			{Name: "csv", OverlaySupport: "warn_and_skip"},
			{Name: "excel", OverlaySupport: "sheets"},
			{Name: "jsonarray", OverlaySupport: "warn_and_skip"},
			{Name: "ndjson", OverlaySupport: "trailing_block"},
			{Name: "parquet", OverlaySupport: "sidecar"},
			{Name: "spss", OverlaySupport: "warn_and_skip"},
			{Name: "tsv", OverlaySupport: "warn_and_skip"},
		},
	}
}

// ImportFormatCapability describes one tabular source format the import
// dispatch (io/format.NewReader) accepts, surfaced on the manifest so an
// LLM planner can answer "can Pulse read this file, and will the
// resulting cohort's types be the source's or a guess?" without
// crawling io/.
//
// SchemaSource is the load-bearing slot and carries exactly two values:
//
//   - "inferred"      — the adapter yields rows of text and the shared
//     inference pass (io/infer.go) samples them and
//     votes on a type per column. Correct in the
//     common case, but it is a guess: a categorical
//     column's dictionary is built in first-seen
//     order, and a type can change with the sample
//     window.
//   - "authoritative" — the adapter implements io.SchemaAwareReader and
//     hands over a schema its own source dictionary
//     DECLARES. Inference is skipped entirely, so
//     types, nullability and categorical dictionary
//     ORDER come from the file rather than from its
//     cell text.
//
// Export reports whether the SAME format can also be written today. It
// is deliberately not inferable from this block's presence: a format
// being readable says nothing about whether a writer exists. Every
// format Pulse reads it can also write as of E5-S6, which mounted the
// `.sav` writer and flipped the last false here to true — the slot stays
// because the two halves are independent and a future read-only format
// would need it again. Cross-reference ExportCapability.Formats for the
// per-format overlay-embedding shape.
type ImportFormatCapability struct {
	// Name is the canonical format identifier matching the io/format
	// package constants (csv / tsv / ndjson / jsonarray / parquet /
	// arrow / excel / spss).
	Name string `json:"name"`

	// Extensions lists the lowercase file extensions (leading dot
	// included) that io/format.FromExt resolves to this format, in the
	// dispatch's own order. Present so a planner can answer "what will
	// this path be detected as" without a round trip.
	Extensions []string `json:"extensions"`

	// SchemaSource is "authoritative" when the adapter implements
	// io.SchemaAwareReader and the source's own dictionary becomes the
	// .pulse schema, "inferred" when the shared sample-and-vote pass
	// decides the types. See ImportFormatCapability godoc.
	SchemaSource string `json:"schema_source"`

	// Export reports whether Pulse can also WRITE this format today.
	// False for import-only formats; cross-reference
	// ExportCapability.Formats for the writable set's overlay shapes.
	Export bool `json:"export"`
}

// ImportCapability is the cross-format import envelope — the read-side
// peer of ExportCapability. Carries the alphabetised per-format slice so
// LLM planners can route a source file to `pulse import` (or
// pulse_import) knowing both that the format is readable and whether the
// cohort's types will be the source's own.
type ImportCapability struct {
	// Formats enumerates every format the import dispatch supports,
	// sorted alphabetically by Name for deterministic golden output.
	// The native "pulse" format is excluded: it needs no tabular
	// reader and passes through untouched.
	Formats []ImportFormatCapability `json:"formats"`
}

// importCapability returns the canonical ImportCapability entry.
//
// The table is hand-declared rather than derived from
// io/format.SupportedImport because descriptor/ is the no-execute layer:
// importing io/format would drag the arrow, parquet and excel adapters
// into every manifest build for the sake of a list of seven strings.
// TestManifestImportCapability_MatchesFormatRegistry pins the two
// against each other so the hand-declaration cannot drift.
//
// Sorted alphabetically by Name so the golden manifest stays stable.
func importCapability() ImportCapability {
	return ImportCapability{
		Formats: []ImportFormatCapability{
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
