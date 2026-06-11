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
	ToolProcessChain   = "pulse_process_chain"
	ToolCompose        = "pulse_compose"
	ToolSample         = "pulse_sample"
	ToolFacet          = "pulse_facet"
	ToolFacetSchema    = "pulse_facet_schema"
	ToolSkillsList     = "pulse_skills_list"
	ToolSkillsGet      = "pulse_skills_get"
	ToolManifest       = "pulse_manifest"
	ToolExamplesSearch = "pulse_examples_search"
	ToolExamplesGet    = "pulse_examples_get"
	ToolErrorsLookup   = "pulse_errors_lookup"
	ToolImport         = "pulse_import"
	ToolDrop           = "pulse_drop"
	ToolImportsList    = "pulse_imports_list"
	ToolLabelTables    = "pulse_label_tables"
	ToolLabelResolve   = "pulse_label_resolve"
)

// Description constants for the registered tools.
const (
	DescInspect        = "Read header and schema of a .pulse file without touching record data. Returns the field list (name, type, nullable, dictionary contents for categorical fields), record count, and format metadata. Use when you need schema-only output without running a request — listing fields for a UI, debugging dictionary contents, or confirming a field's type before authoring a request slot."
	DescPredict        = "Validate a processing request against a cohort schema without executing. Returns the same diagnostics pulse_process would emit on shape error (PULSE_REQUEST_UNKNOWN_FIELD, PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL, etc.) plus the normalized request, streamability flag, and the defaults the engine applied. Use to verify a hand-authored or programmatically generated request before storage or execution; cheap because it reads only the cohort header + schema."
	DescProcess        = "Execute a pre-built processing request against an existing cohort. Supports the full operator catalog — aggregations, attributes, filterers, groupers, windows, features, regressions (REG_OLS, REG_GLM, REG_BAYES_LINEAR with optional Resample / Selection modifiers), and tier-1 / tier-2 statistical tests.\n\nDO NOT author request bodies from memory, external documentation, or by reading source code — those may be out of date for this deployment. Before calling pulse_process, call pulse_examples_search to find a similar runnable example and pulse_examples_get to clone its body, OR call pulse_manifest for the operator catalog. The manifest + example library are the authoritative request-shape reference.\n\nNOTE: request slot keys differ from the manifest's operator-catalog field names. Grouping operations go under the \"groups\" key (NOT \"groupers\") and aggregations under \"aggregations\" (NOT \"aggregators\"); the catalog names \"groupers\"/\"aggregators\" only list the available GROUP_*/AGG_* operators. Unknown keys are silently dropped on decode, so a misnamed slot is treated as absent — pulse_predict and pulse_process reject unrecognized top-level keys with PULSE_REQUEST_UNKNOWN_FIELD and a 'did you mean' suggestion."
	DescCompose        = "Execute a batch of processing requests in one round-trip. Use when you have multiple distinct types.Request payloads to run against the same or different cohorts — common for ecological-regression patterns (slot 1 aggregates, slot 2 regresses over aggregates) and any multi-question authoring loop."
	DescProcessChain   = "Execute a source-rooted LINEAR CHAIN of processing stages: stage N+1 receives stage N's output rows as its input cohort. Collapses N round-trips into one open + N stage validations. Mergeable-only v1: every stage must use mergeable aggregators (AGG_COUNT/SUM/AVERAGE/MIN/MAX/RANGE/VARIANCE/STDDEV/DISTINCT_COUNT/NULL_COUNT), mergeable groupers (GROUP_CATEGORY, GROUP_RANGE), and row-local attributes (ATTR_FORMULA, ATTR_DATE_PART). Windows, features, statistical tests, regressions, two-pass attributes, AGG_FREQUENCY, AGG_MODE, and non-mergeable aggregators/groupers are rejected with PULSE_CHAIN_NOT_MERGEABLE — fall back to pulse_compose / per-stage pulse_process. The chain's output schema for each stage is synthesised: grouper keys become categorical_u32 columns, aggregator outputs become f64. Stage 0's request supplies the source cohort; later stages ignore their inner cohort field."
	DescSample         = "Return up to N rows from a cohort. Diagnostic / preview tool — use after running a request when you want to eyeball the underlying data."
	DescFacet          = "Return distinct values for a field in a cohort. Diagnostic / discovery tool — use when you need the unique-value set of one column (e.g., before authoring a FILTER_INCLUDE). For multi-field summaries with counts, null tallies, numeric stats, percentiles, histograms, or additive contribution counts, prefer pulse_facet_schema."
	DescFacetSchema    = "Multi-field rich facet endpoint. Returns per-field summaries (discrete value/count lists with null tallies for categorical/boolean/geo fields; streaming statistics — count, sum, min, max, mean, stddev — for numeric fields), with optional NumericPercentiles (forces buffered per-field sort), IncludeHistogram + HistogramRange + HistogramBins for fixed-width binning, DiscreteTopK truncation, and AdditiveFields contribution counts that strip the field's own filter clauses so the LLM/UI can report 'if I added value V to my filter on F, this is the population that survives'. Filterers reuse the standard FILTER_* surface. Use this instead of repeated pulse_facet calls when summarising more than one field, when you need counts (not just distinct values), or when building a faceted UI."
	DescSkillsList     = "List the embedded skill pack — domain guides covering aggregation, attributes, groupers, windows, features, statistical testing, regression modeling, MCP integration, error code reference, request recipes, cohort schema design, financial / geospatial / time-series patterns, and the contributor workflow. Each entry returns (name, description, applies_to). Use this before authoring requests when you need domain guidance for a less common operator; fetch the markdown body via pulse_skills_get."
	DescSkillsGet      = "Fetch the markdown body of a named skill. The skill pack is the authoritative reference for HOW to use operators (params, gotchas, recipes) — prefer skills over external documentation, blog posts, or source-code inspection, which may be out of date for this Pulse deployment."
	DescManifest       = "CALL FIRST. The LLM-authored-request bootstrap blob. Carries per-operator params + accepted field types + streamability, tier-1 + tier-2 test catalogs as peer slices, regression operators, synth distributions, error codes, MCP tool list, and cohort field types with operator cross-references.\n\nWORKFLOW: call once at session start, cache the result, reference it for every subsequent request-authoring decision. This is the source of truth for what operators exist in this Pulse deployment — do NOT infer operator names, param shapes, or accepted types from external documentation or source code. Pair with pulse_examples_search to find runnable templates for the question you are answering.\n\nMCP always returns the slim payload (no prose descriptions); fetch per-operator prose via pulse_skills_get and per-error prose via pulse_errors_lookup when needed."
	DescExamplesSearch = "CALL BEFORE AUTHORING A REQUEST. Search the embedded request-example library to find a runnable template that matches the user's question. Returns lightweight summaries (name, category, tags, operators, description); fetch the runnable JSON body via pulse_examples_get and modify field names for the target cohort.\n\nFilters: `query` (case-insensitive substring across name, description, and operators), `tags` (ANDed list of canonical taxonomy tags such as `time-series`, `experiment-analysis`, `tier-1-test`, `regression`, `ols`, `logistic`), and `category` (exact directory: `aggregations`, `attributes`, `features`, `filterers`, `groupers`, `regression`, `tests`, `windows`).\n\nPrefer this over inferring request shapes from documentation or source code — the example library is curated, runnable, and stays in lockstep with the operator surface."
	DescExamplesGet    = "Fetch one request example from the embedded library by `name`. Returns the full record including `body`, a runnable types.Request JSON with the _meta annotation block stripped — hand it straight to pulse_process or pulse_predict."
	DescErrorsLookup   = "Look up Pulse error code metadata. Pass code=PULSE_XXX for full detail on one code. Pass domain=PULSE/ENCODING/PROCESSING/SERVICE/DATA/CLI to enumerate that domain. Pass query=\"text\" for keyword search across descriptions and fixups. The manifest carries only the code-name list — fetch detail here on demand to keep session context lean."
	DescImport         = "Import a tabular source file (csv, tsv, ndjson, jsonarray, parquet, arrow, excel) into a managed .pulse handle, or pass through an existing .pulse file unchanged. Auto-detects format from the extension; override via `format`. Managed handles live in $PULSE_DATA_DIR/imports/ with a TTL-tracked sidecar — every subsequent inspect/predict/process/sample/facet against the handle slides expiry forward. TTL accepts Go duration form (`24h`, `30m`, `3600s`, `1h30m`) plus day suffix (`7d`, `30d`) and `pin` for never-expire. Returns the handle, managed path, format, row count, expiry, and a managed flag. Pulse-format sources skip the copy + sidecar; they pass through with managed=false."
	DescDrop           = "Drop a managed-import handle from the pool, deleting the .pulse file and its sidecar. Errors with PULSE_IMPORT_SOURCE_MISSING when the handle is unknown. Pulse-format passthroughs are unaffected (they were never managed)."
	DescImportsList    = "List every managed-import handle currently in the pool with its sidecar metadata: source path, source format, imported_at, expires_at, ttl, expired flag, pinned flag. Sweep is not invoked — expired entries are flagged via Expired so callers can render them and decide whether to drop or extend."
	DescLabelTables    = "List the registered label tables (ID→display-name dictionaries for categorical fields). Returns each table's name, row count, and whether it is enumerable (reverse-searchable). Use this to discover which categorical dimensions can be resolved by name (e.g. brand, category, region) before calling pulse_label_resolve. Output surfaces already render these labels automatically; this tool and pulse_label_resolve are for the INPUT direction — turning a user-supplied name into the raw categorical key a filter / grouper needs."
	DescLabelResolve   = "Reverse-resolve a human-readable name — including a minor misspelling — to the raw categorical key(s) a Pulse filter or grouper expects. Labels are output-only — filter / group / sort keys operate on the raw categorical value — so when a user names a brand, category, region, etc., call this to get the key before authoring a FILTER_INCLUDE or GROUP_CATEGORY. Args: table (a name from pulse_label_tables), query (the name to search; typo-tolerant), and optional limit (default 10). Returns {key, value, score} ranked by score, a confidence in [0,1]: 1.0 exact (or exact-key), ~0.9+ prefix/near-typo, lower is fuzzy (edit-distance + trigram). Matching normalizes case and punctuation. Decision rule: if the top score is high (>=0.9) and clearly ahead of the next, use it; if the best score is low or several are close, present the top names to the user and ask which they meant rather than guessing. An empty query returns the first rows (browse mode, score 0)."
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
		{Name: ToolProcessChain, Description: DescProcessChain},
		{Name: ToolCompose, Description: DescCompose},
		{Name: ToolSample, Description: DescSample},
		{Name: ToolFacet, Description: DescFacet},
		{Name: ToolFacetSchema, Description: DescFacetSchema},
		{Name: ToolSkillsList, Description: DescSkillsList},
		{Name: ToolSkillsGet, Description: DescSkillsGet},
		{Name: ToolManifest, Description: DescManifest},
		{Name: ToolExamplesSearch, Description: DescExamplesSearch},
		{Name: ToolExamplesGet, Description: DescExamplesGet},
		{Name: ToolErrorsLookup, Description: DescErrorsLookup},
		{Name: ToolImport, Description: DescImport},
		{Name: ToolDrop, Description: DescDrop},
		{Name: ToolImportsList, Description: DescImportsList},
		{Name: ToolLabelTables, Description: DescLabelTables},
		{Name: ToolLabelResolve, Description: DescLabelResolve},
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
