package descriptor

// ExportFormatCapability describes the per-format export envelope
// surfaced on the manifest. One entry per format the engine's export
// dispatch supports, carrying the format identifier plus the per-format
// overlay-embedding strategy LLM planners need to decide whether a
// Response.Overlays slice will round-trip through an ExportJob to the
// chosen target.
//
// OverlaySupport is the canonical embedding-shape label declared by
// research/export-embedding-shape.md and wired by E9-S2..S6:
//
//   - "sidecar"        — the format carries a top-level
//                        LIST<STRUCT> column-family appended to the
//                        host stream (Arrow / Parquet). The host
//                        record stream is byte-identical to the
//                        overlay-free shape; readers that ignore the
//                        column family see the unchanged host.
//   - "sheets"         — one workbook sheet per overlay layer named
//                        "__overlay_<layer_name>" (Excel). The host
//                        workbook sheet is unchanged from the overlay-
//                        free shape.
//   - "trailing_block" — a single trailing line `{"_overlays": [...]}`
//                        after the last host record (NDJSON). The
//                        host stream is byte-identical to the overlay-
//                        free shape until the trailer.
//   - "warn_and_skip"  — the format cannot embed overlays at all (CSV /
//                        TSV). The dispatcher emits one
//                        PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED warning
//                        and writes the host body verbatim; no overlay
//                        output lands. Setting IncludeOverlays=false
//                        suppresses the warning while keeping the same
//                        body.
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
// landed by E9-S2..S6 (Arrow + Parquet sidecar, Excel sheets, NDJSON
// trailing block, CSV / TSV warn-and-skip). jsonarray and tsv share
// the warn-and-skip shape with CSV — neither format carries an
// extension surface beyond the host stream so embedded overlays would
// not round-trip through a vanilla reader.
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
			{Name: "tsv", OverlaySupport: "warn_and_skip"},
		},
	}
}
