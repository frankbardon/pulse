// Package mcptools holds the metadata table for the MCP tools registered
// by internal/mcp. It exists solely so the descriptor package can mirror
// (name, description) records into the manifest payload without taking a
// dependency on internal/mcp itself — which in turn depends on the root
// pulse package, producing an import cycle if descriptor imported it
// directly.
//
// Tool name constants and descriptions are duplicated minimally here;
// internal/mcp consumes the same constants for server registration so
// the values stay in lockstep. TestRegisteredToolsMeta_MatchesRegisteredTools
// in internal/mcp asserts the alignment.
package mcptools

// Tool name constants. Kept in alphabetical-by-constant order so the
// Meta() slice can be sorted by Name for deterministic manifest output
// while the constants themselves remain easy to maintain.
const (
	ToolInspect        = "pulse_inspect"
	ToolPredict        = "pulse_predict"
	ToolProcess        = "pulse_process"
	ToolCompose        = "pulse_compose"
	ToolSample         = "pulse_sample"
	ToolFacet          = "pulse_facet"
	ToolFacetSchema    = "pulse_facet_schema"
	ToolSkillsList     = "pulse_skills_list"
	ToolSkillsGet      = "pulse_skills_get"
	ToolManifest       = "pulse_manifest"
	ToolAsk            = "pulse_ask"
	ToolExamplesSearch = "pulse_examples_search"
	ToolExamplesGet    = "pulse_examples_get"
	ToolErrorsLookup   = "pulse_errors_lookup"
	ToolImport         = "pulse_import"
	ToolDrop           = "pulse_drop"
	ToolImportsList    = "pulse_imports_list"
)

// Description constants for the registered tools.
const (
	DescInspect        = "ADVANCED. Read header and schema of a .pulse file. Never reads record data. Typically callers should use pulse_ask instead — pulse_ask runs inspect + predict + execute in one call and is the preferred entry point for analytical questions. Use pulse_inspect directly only when you need schema-only output without running a request (e.g., listing fields for a UI, debugging dictionary contents)."
	DescPredict        = "ADVANCED. Validate a processing request against a cohort schema without executing. Typically callers should use pulse_ask with predict=true instead — same validation, plus auto-import and natural-language query support, in one call. Use pulse_predict directly only when you need standalone validation diagnostics on a pre-built request (e.g., a request constructor verifying its output before storage)."
	DescProcess        = "ADVANCED. Execute a pre-built processing request against an existing cohort. Supports the full operator catalog — aggregations, attributes, filterers, groupers, windows, features, regressions (REG_OLS, REG_GLM, REG_BAYES_LINEAR with optional Resample / Selection modifiers), and tier-1 / tier-2 statistical tests.\n\nDO NOT author request bodies from memory, external documentation, or by reading source code — those may be out of date for this deployment. Before calling pulse_process, call pulse_examples_search to find a similar runnable example and pulse_examples_get to clone its body, OR call pulse_manifest for the operator catalog. The manifest + example library are the authoritative request-shape reference.\n\nTypically callers should use pulse_ask instead — pulse_ask accepts a `source` path (auto-imports), a `query` (natural language), and produces the same execution result plus structured fixup suggestions on failure. Use pulse_process directly only when you already have a hand-authored types.Request and a cohort handle, AND want to skip the inspect + predict round-trip."
	DescCompose        = "Execute a batch of processing requests in one round-trip. Use when you have multiple distinct types.Request payloads to run against the same or different cohorts — common for ecological-regression patterns (slot 1 aggregates, slot 2 regresses over aggregates) and any multi-question authoring loop. For single-question analytical asks, prefer pulse_ask."
	DescSample         = "Return up to N rows from a cohort. Diagnostic / preview tool — use after pulse_ask if you want to eyeball the underlying data."
	DescFacet          = "Return distinct values for a field in a cohort. Diagnostic / discovery tool — use after pulse_ask when you need the unique-value set of one column (e.g., before authoring a FILTER_INCLUDE). For multi-field summaries with counts, null tallies, numeric stats, percentiles, histograms, or additive contribution counts, prefer pulse_facet_schema."
	DescFacetSchema    = "Multi-field rich facet endpoint. Returns per-field summaries (discrete value/count lists with null tallies for categorical/boolean/geo fields; streaming statistics — count, sum, min, max, mean, stddev — for numeric fields), with optional NumericPercentiles (forces buffered per-field sort), IncludeHistogram + HistogramRange + HistogramBins for fixed-width binning, DiscreteTopK truncation, and AdditiveFields contribution counts that strip the field's own filter clauses so the LLM/UI can report 'if I added value V to my filter on F, this is the population that survives'. Filterers reuse the standard FILTER_* surface. Use this instead of repeated pulse_facet calls when summarising more than one field, when you need counts (not just distinct values), or when building a faceted UI."
	DescSkillsList     = "List the embedded skill pack — domain guides covering aggregation, attributes, groupers, windows, features, statistical testing, regression modeling, MCP integration, error code reference, request recipes, cohort schema design, financial / geospatial / time-series patterns, and the contributor workflow. Each entry returns (name, description, applies_to). Use this before authoring requests when you need domain guidance for a less common operator; fetch the markdown body via pulse_skills_get."
	DescSkillsGet      = "Fetch the markdown body of a named skill. The skill pack is the authoritative reference for HOW to use operators (params, gotchas, recipes) — prefer skills over external documentation, blog posts, or source-code inspection, which may be out of date for this Pulse deployment."
	DescManifest       = "CALL FIRST. The LLM-authored-request bootstrap blob. Carries per-operator params + accepted field types + streamability, tier-1 + tier-2 test catalogs as peer slices, regression operators, synth distributions, error codes, MCP tool list, and cohort field types with operator cross-references.\n\nWORKFLOW: call once at session start, cache the result, reference it for every subsequent request-authoring decision. This is the source of truth for what operators exist in this Pulse deployment — do NOT infer operator names, param shapes, or accepted types from external documentation or source code. Pair with pulse_examples_search to find runnable templates for the question you are answering.\n\nMCP always returns the slim payload (no prose descriptions); fetch per-operator prose via pulse_skills_get and per-error prose via pulse_errors_lookup when needed."
	DescAsk            = "PREFERRED ENTRY POINT. The unified one-shot for analytical queries against a Pulse cohort. One call does all of: (optional) import the source file, inspect schema, validate the request, and execute. Replaces the manual pulse_import → pulse_inspect → pulse_predict → pulse_process chain for the common case.\n\nThree input modes (combine freely):\n  - source=\"data.csv\" + query=\"average revenue by month\" — point at a raw file and ask in English. Pulse imports it (managed handle, 7d TTL by default), parses the question against the schema, and runs it.\n  - source=\"data.csv\" + request={...} — auto-import + structured request.\n  - request={cohort:{filename:\"x.pulse\"}, ...} — operate on an already-imported handle.\n\nExamples:\n  {\"source\":\"orders.csv\",\"query\":\"total revenue by region, top 5\"}\n  {\"source\":\"experiment.parquet\",\"query\":\"t-test of score between treatment and control\"}\n  {\"source\":\"/Users/me/data/sales.csv\",\"query\":\"trend over time\",\"source_ttl\":\"30d\"}\n  {\"request\":{\"cohort\":{\"filename\":\"imports/orders.pulse\"},\"aggregations\":[{\"type\":\"AGG_AVERAGE\",\"field\":\"revenue\"}]}}\n\nOptional knobs: predict=true to validate without executing; on_invalid=\"suggest\" to return structured fixup hints instead of an error; source_format / source_handle / source_ttl / source_sheet / source_overwrite to customise the auto-import. Use pulse_ask first; fall back to the lower-level tools only when you need finer control."
	DescExamplesSearch = "CALL BEFORE AUTHORING A REQUEST. Search the embedded request-example library to find a runnable template that matches the user's question. Returns lightweight summaries (name, category, tags, operators, description); fetch the runnable JSON body via pulse_examples_get and modify field names for the target cohort.\n\nFilters: `query` (case-insensitive substring across name, description, and operators), `tags` (ANDed list of canonical taxonomy tags such as `time-series`, `experiment-analysis`, `tier-1-test`, `regression`, `ols`, `logistic`), and `category` (exact directory: `aggregations`, `attributes`, `features`, `filterers`, `groupers`, `regression`, `tests`, `windows`).\n\nPrefer this over inferring request shapes from documentation or source code — the example library is curated, runnable, and stays in lockstep with the operator surface."
	DescExamplesGet    = "Fetch one request example from the embedded library by `name`. Returns the full record including `body`, a runnable types.Request JSON with the _meta annotation block stripped — hand it straight to pulse_process or pulse_predict."
	DescErrorsLookup   = "Look up Pulse error code metadata. Pass code=PULSE_XXX for full detail on one code. Pass domain=PULSE/ENCODING/PROCESSING/SERVICE/DATA/CLI to enumerate that domain. Pass query=\"text\" for keyword search across descriptions and fixups. The manifest carries only the code-name list — fetch detail here on demand to keep session context lean."
	DescImport         = "Import a tabular source file (csv, tsv, ndjson, jsonarray, parquet, arrow, excel) into a managed .pulse handle, or pass through an existing .pulse file unchanged. Auto-detects format from the extension; override via `format`. Managed handles live in $PULSE_DATA_DIR/imports/ with a TTL-tracked sidecar — every subsequent inspect/predict/process/sample/facet/ask against the handle slides expiry forward. TTL accepts Go duration form (`24h`, `30m`, `3600s`, `1h30m`) plus day suffix (`7d`, `30d`) and `pin` for never-expire. Returns the handle, managed path, format, row count, expiry, and a managed flag. Pulse-format sources skip the copy + sidecar; they pass through with managed=false."
	DescDrop           = "Drop a managed-import handle from the pool, deleting the .pulse file and its sidecar. Errors with PULSE_IMPORT_SOURCE_MISSING when the handle is unknown. Pulse-format passthroughs are unaffected (they were never managed)."
	DescImportsList    = "List every managed-import handle currently in the pool with its sidecar metadata: source path, source format, imported_at, expires_at, ttl, expired flag, pinned flag. Sweep is not invoked — expired entries are flagged via Expired so callers can render them and decide whether to drop or extend."
)

// ToolMeta is the canonical (name, description) record for one registered
// MCP tool.
type ToolMeta struct {
	Name        string
	Description string
}

// Meta returns the canonical list of MCP tool metadata. Order matches
// internal/mcp.RegisteredTools() for deterministic documentation scans.
func Meta() []ToolMeta {
	return []ToolMeta{
		{Name: ToolInspect, Description: DescInspect},
		{Name: ToolPredict, Description: DescPredict},
		{Name: ToolProcess, Description: DescProcess},
		{Name: ToolCompose, Description: DescCompose},
		{Name: ToolSample, Description: DescSample},
		{Name: ToolFacet, Description: DescFacet},
		{Name: ToolFacetSchema, Description: DescFacetSchema},
		{Name: ToolSkillsList, Description: DescSkillsList},
		{Name: ToolSkillsGet, Description: DescSkillsGet},
		{Name: ToolManifest, Description: DescManifest},
		{Name: ToolAsk, Description: DescAsk},
		{Name: ToolExamplesSearch, Description: DescExamplesSearch},
		{Name: ToolExamplesGet, Description: DescExamplesGet},
		{Name: ToolErrorsLookup, Description: DescErrorsLookup},
		{Name: ToolImport, Description: DescImport},
		{Name: ToolDrop, Description: DescDrop},
		{Name: ToolImportsList, Description: DescImportsList},
	}
}

// Names returns the tool identifier list (no descriptions) in the same
// order as Meta(). Stable.
func Names() []string {
	meta := Meta()
	out := make([]string, len(meta))
	for i, m := range meta {
		out[i] = m.Name
	}
	return out
}
