package doccheck

// RemovedNames is the removed-name denylist: spellings that existed before
// v1 (at v0.39.0, or only in pre-1.0 plans) and are gone, so served text
// must not use them. Seeded by `go run ./internal/tools/removednames`
// (v0.39.0 → HEAD) and pruned by the owner: a name still exported by some
// package, still a method or still a live string is never listed (it
// would flag correct prose). Add an entry when you remove a public name
// whose spelling is distinctive. TestNoRemovedNamesInDocs (root) and its
// heavy sibling TestNoRemovedNamesInPromptsAndReference (mcp/gosdk) read
// it; a qualified io.<Name> entry also matches pio.<Name> (Spellings).
var RemovedNames = []DeniedName{
	// Public names deleted at v1 (sweep of v0.39.0).
	{Name: "ApplyOverlays", Reason: "test-only processing helper, removed in U02 with no replacement; drive overlays through Request.Overlays or (*Pulse).ApplySeriesOverlays"},
	{Name: "CompileDateRanges", Reason: "processing helper removed in U02; range tables are validated at pulse.New"},
	{Name: "DateRangeSet", Reason: "processing type removed in U02; use pulse.DateRangeSpec"},
	{Name: "CategoricalAggregationIssues", Reason: "descriptor helper removed in U02; predict reports the issue"},
	{Name: "InspectFromBytes", Reason: "descriptor function replaced by (*Pulse).InspectBytes in U02 (#182)"},
	{Name: "PredictFromBytes", Reason: "descriptor function replaced by (*Pulse).PredictBytes in U02 (#182)"},
	{Name: "NormalizeRequest", Reason: "descriptor function removed in U02; predict and process normalise the request"},
	{Name: "ValidateChain", Reason: "test-only descriptor wrapper deleted in U34 (#194); use (*Pulse).PredictChain"},
	{Name: "ValidateChainFromBytes", Reason: "test-only descriptor wrapper deleted in U34 (#194); use (*Pulse).PredictChain"},
	{Name: "DistributionRegistration", Reason: "pulse type retired in U34 (#197); synthetic-data distributions are not an extension category"},
	{Name: "FromExt", Reason: "io/format function replaced in U02 by io.FormatFromPath"},
	{Name: "SupportedImport", Reason: "io/format function replaced in U02 by io.Formats() and Format.CanRead()"},
	{Name: "PromoteAdd", Reason: "encoding decimal helper removed before v1"},
	{Name: "PromoteMul", Reason: "encoding decimal helper removed before v1"},
	{Name: "PromoteDiv", Reason: "encoding decimal helper removed before v1"},
	{Name: "ValidatePrecisionScale", Reason: "encoding decimal helper removed before v1"},
	{Name: "PROCESSING_REGRESSION_NOT_IMPLEMENTED", Reason: "error code removed before v1: every regression operator is implemented"},
	// Pre-1.0 spellings that never shipped or were replaced.
	{Name: "OVERLAY_CORR_PVALUE", Reason: "planned overlay dropped in favour of MatrixSpec.multiplicity"},
	{Name: "MCP version 1.0.0", Reason: "pre-1.0 MCP server version; the server reports pulse.Version()"},
	// Migration-guide removals (docs/roadmap/v1.0.0-api-and-release/03-embedder-migration.md).
	{Name: "service.Cohort", Reason: "removed with (*Pulse).Service() in U02; use the facade methods"},
	{Name: "io.NewConvertJob", Reason: "removed in U02; build &io.ConvertJob{} directly"},
	{Name: "io.InferSchema", Reason: "moved internal in U02; inference runs inside io.ImportJob"},
	{Name: "mmmpulse.Extensions", Reason: "the media-mix pack is registered by the embedder, not reached through a pack-level Extensions value"},
}
