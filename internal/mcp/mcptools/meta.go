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
