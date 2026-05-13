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
	DescInspect    = "Read header and schema of a .pulse file. Never reads record data."
	DescPredict    = "Validate a processing request against a cohort schema without executing."
	DescProcess    = "Execute a processing request against a cohort."
	DescCompose    = "Execute a batch of processing requests."
	DescSample     = "Return up to N rows from a cohort."
	DescFacet      = "Return distinct values for a field in a cohort."
	DescSkillsList = "List available embedded skills with their descriptions."
	DescSkillsGet  = "Fetch the markdown body of a named skill."
	DescManifest   = "Return the root Pulse manifest — the LLM-authored-request bootstrap blob. Carries per-operator params + accepted field types + streamability, tier-1 + tier-2 test catalogs as peer slices, synth distributions, error codes, MCP tool list, and cohort field types with operator cross-references. Call once at session start, cache the result, reference it for every subsequent request authoring decision. MCP always returns the slim payload (no prose descriptions); fetch operator prose via pulse_skills_get when needed."
	DescAsk        = "Pulse one-shot. Inspect the given .pulse file, validate the request against its schema, and execute if valid. Accepts either a structured `request` or a `query` natural-language string (parsed server-side using the file's schema). On validation failure with on_invalid=\"suggest\", return structured fixup suggestions instead of erroring. Replaces the inspect→predict→process round-trip for the common case. Set predict=true to validate without executing."
	DescExamplesSearch = "Search the embedded request-example library. Filters: `query` (case-insensitive substring across name, description, and operators), `tags` (ANDed list of canonical taxonomy tags such as `time-series`, `experiment-analysis`, `tier-1-test`), and `category` (exact directory: `aggregations`, `attributes`, `features`, `filterers`, `groupers`, `tests`, `windows`). Returns lightweight summaries (name, category, tags, operators, description); fetch the runnable JSON body via pulse_examples_get."
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
