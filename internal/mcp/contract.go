// Package mcp is the SDK-free core of the Pulse MCP surface. It defines the
// public typed contract for every registered tool — an exported In/Out struct
// pair per tool — and reflects an input + output JSON Schema for each at
// package-init time, carrying both as json.RawMessage.
//
// The package imports NO MCP SDK. It depends only on the Pulse domain
// packages (types, descriptor, errors, imports, examples, skills, the root
// facade) plus the schema reflector github.com/google/jsonschema-go. A thin
// adapter (mcp/gosdk, a later story) mounts these descriptors onto a
// caller-supplied go-sdk server via the low-level Server.AddTool path, which
// accepts any value that JSON-marshals to a valid 2020-12 schema — exactly the
// json.RawMessage this package emits. Keeping the schema carrier type-erased is
// what lets external programs import the contract without pulling an MCP SDK.
//
// Recursive-type note: jsonschema-go's reflector returns an error on a Go-level
// type cycle (and the go-sdk generic AddTool would turn that into a panic). The
// payload request/response types referenced below (types.Request, types.Response,
// types.ComposedResponse, the Crosstab matrix, overlay layers) are non-cyclic at
// the Go level, so direct reflection succeeds. Were a future field to introduce
// a cycle, type it as any / json.RawMessage in the In/Out struct so reflection
// stays error-free. The init-time reflection records any error per tool (see
// schema.go) so the unit test fails loudly rather than the package panicking.
package mcp

import (
	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/imports"
	"github.com/frankbardon/pulse/internal/skills"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// --- Thin-tool inputs ---------------------------------------------------------

// InspectIn is the input contract for pulse_inspect.
type InspectIn struct {
	Path string `json:"path" jsonschema:"Filesystem path to the .pulse file"`
}

// SampleIn is the input contract for pulse_sample.
type SampleIn struct {
	Path  string `json:"path" jsonschema:"Filesystem path to the .pulse file"`
	Count int    `json:"count,omitempty" jsonschema:"Maximum rows to return (default 10)"`
}

// FacetIn is the input contract for pulse_facet.
type FacetIn struct {
	Path  string `json:"path" jsonschema:"Filesystem path to the .pulse file"`
	Field string `json:"field" jsonschema:"Field name to facet"`
}

// SkillsListIn is the input contract for pulse_skills_list. With no
// intent it lists every skill, sorted by name.
type SkillsListIn struct {
	Intent string `json:"intent,omitempty" jsonschema:"Intent-taxonomy ID (e.g. compare_groups, relationship). Lists only that intent's skills, ranked: the intents skill, design skills covering its operators, then their atomic skills basic to advanced. Unknown: PULSE_RECOMMEND_INTENT_UNKNOWN"`
}

// SkillsGetIn is the input contract for pulse_skills_get.
type SkillsGetIn struct {
	Name string `json:"name" jsonschema:"Skill name (e.g. 'aggregation-design')"`
}

// ManifestIn is the input contract for pulse_manifest. With no intent
// it returns the full slim manifest.
type ManifestIn struct {
	Intent string `json:"intent,omitempty" jsonschema:"Intent-taxonomy ID (e.g. compare_groups, relationship). Returns the manifest scoped to that intent: only its operators and skills, the fixed sections dropped and listed in elided, the intent record in scope. Unknown: PULSE_RECOMMEND_INTENT_UNKNOWN"`
}

// ExamplesSearchIn is the input contract for pulse_examples_search. All four
// filters are optional and ANDed.
type ExamplesSearchIn struct {
	Query    string   `json:"query,omitempty" jsonschema:"Plain words or a name. One word is a case-insensitive substring of name, description or operators; several words must each match a whole word of name, description, operators, intents or tags. Known operator aliases (anova, chisq.test, pearson) and phrases from an intent's sounds (move together) also match"`
	Tags     []string `json:"tags,omitempty" jsonschema:"Canonical taxonomy tags; results must carry every tag (AND)"`
	Category string   `json:"category,omitempty" jsonschema:"Exact directory: aggregations, attributes, crosstab, facet, features, filterers, groupers, matrices, overlays, regression, tests, windows"`
	Intent   string   `json:"intent,omitempty" jsonschema:"Intent-taxonomy ID (e.g. compare_groups, relationship); results must declare it. Unknown: PULSE_RECOMMEND_INTENT_UNKNOWN"`
}

// ExamplesGetIn is the input contract for pulse_examples_get.
type ExamplesGetIn struct {
	Name string `json:"name" jsonschema:"Example name from the _meta.name field (e.g. 't_test_one_sample')"`
}

// ErrorsLookupIn is the input contract for pulse_errors_lookup. At least one
// axis must be set; supplied axes are intersected.
type ErrorsLookupIn struct {
	Code   string `json:"code,omitempty" jsonschema:"Exact error code identifier (e.g. 'PULSE_LOOKUP_MISS')"`
	Domain string `json:"domain,omitempty" jsonschema:"Domain prefix (PULSE, ENCODING, PROCESSING, SERVICE, DATA, CLI); case-insensitive"`
	Query  string `json:"query,omitempty" jsonschema:"Case-insensitive substring across descriptions and fixup hints"`
}

// ImportIn is the input contract for pulse_import.
type ImportIn struct {
	Source    string `json:"source" jsonschema:"Filesystem path to the source file (relative to PULSE_DATA_DIR)"`
	Format    string `json:"format,omitempty" jsonschema:"Format override: csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss, pulse"`
	Handle    string `json:"handle,omitempty" jsonschema:"Managed handle name; defaults to source basename without extension"`
	TTL       string `json:"ttl,omitempty" jsonschema:"TTL: Go duration ('24h', '30m') or day form ('7d', '30d'); 'pin' disables expiry. Default 7d."`
	Sheet     string `json:"sheet,omitempty" jsonschema:"Excel sheet name; ignored for non-Excel sources"`
	Charset   string `json:"charset,omitempty" jsonschema:"SPSS ONLY: character encoding override for a .sav / .zsav source (e.g. 'windows-1252', 'cp1252', 'latin1'; spelling is forgiving). Ignored for every other format. Empty leaves the file's own declaration in force. Reach for it when an import fails PULSE_SPSS_CHARSET_INVALID or PULSE_SPSS_CHARSET_UNSUPPORTED — typically a file that kept a stale record 7/20 name after being transcoded, or a pre-Unicode file that declares no encoding at all and so fails the strict UTF-8 default on its first 8-bit byte. Decoding only; the file's own declaration is still retained."`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"Replace an existing handle of the same name. Default false."`
	// SourceTZ / ColumnSourceTZ / DSTPolicy are io.ImportJob's
	// source-zone options, persisted onto the managed sidecar.
	SourceTZ       string            `json:"source_tz,omitempty" jsonschema:"Zone the source's naive datetime values (no Z, no offset) were recorded in: an IANA name such as 'America/New_York', 'UTC', or a fixed '+HH:MM' / '-HH:MM' offset. Applies to every datetime column (date columns are skipped); the stored value is always the UTC instant. A value with its own Z or offset ignores it. Empty (default) reads naive values as UTC. Unknown zone: PULSE_TIMEZONE_UNKNOWN."`
	ColumnSourceTZ map[string]string `json:"column_source_tz,omitempty" jsonschema:"Per-column source zone, {column: zone}, same spellings as source_tz; wins over source_tz for that column. A key naming no column, or a column that is not datetime, is SERVICE_VALIDATION."`
	DSTPolicy      string            `json:"dst_policy,omitempty" jsonschema:"How a naive value a DST transition makes ambiguous (repeated hour) or nonexistent (skipped hour) resolves under a source zone: 'error' (default; the import fails with PULSE_IMPORT_DST_AMBIGUOUS / PULSE_IMPORT_DST_NONEXISTENT naming the row), 'earlier' (pre-transition offset) or 'later' (post-transition offset). A resolved value is counted in a PULSE_IMPORT_DST_RESOLVED entry on zone_warnings."`
	// Groups and SuggestGroups carry the parent-group surface. See the
	// slot-policy note below for why there is no ratio-floor or strict
	// slot beside them.
	Groups        []pio.GroupDecl `json:"groups,omitempty" jsonschema:"Parent-group declarations, one object per group: {key: [fields...], members: [fields...]}. Each distinct member tuple is stored ONCE and every row carries a 4-byte index instead — use it when the source is a denormalized join, where a parent's attributes (members) repeat on every child row and are determined by the parent's ID (key). The import FAILS with PULSE_GROUP_MEMBER_NOT_CONSTANT if a member varies within its key; omit key for a plain tuple group with no such check. Take declarations from suggest_groups candidates (their key/members are this exact shape) rather than guessing. Weak groups are reported, not refused: group_warnings carries PULSE_GROUP_TOO_NARROW (dropped) or PULSE_DEDUP_LOW_RATIO (written, under 2 rows per distinct tuple or no smaller). Unknown keys inside an entry are rejected with PULSE_GROUP_DECLARATION_INVALID. Writes format 0x02, which older pulse binaries cannot read. Not allowed on a .pulse passthrough."`
	SuggestGroups bool            `json:"suggest_groups,omitempty" jsonschema:"Also detect candidate parent groups and return them as group_candidates, each measured over every row (ratio, resident dictionary bytes, projected file size, verdict) with key/members ready to pass back as a groups entry. Suggests only: the import still writes exactly what groups declares. Costs one extra full pass over the source. The loop is: import with suggest_groups, read group_candidates (the suggested list is the non-overlapping viable set), then re-import with groups and overwrite=true."`
}

// There is deliberately no spss_missing slot on ImportIn. The mode's default
// ("auto") is the fidelity-preserving one — every numeric user-missing value
// is null in its analytic column AND a generated `<var>_missing` sibling
// records why — and the only alternative, "null", drops those siblings. A knob
// whose sole effect is to discard information does not belong on a
// general-purpose import tool; `pulse import spss --spss-missing=null` is
// where asking for it is an explicit act. Charset is the opposite case: it is
// the only recourse for a file that is wrong about its own encoding, and
// without it such a file is unimportable over MCP by any means.
//
// The same policy decides the parent-group slots. groups earns one: it is
// the only way to write a deduplicated cohort through the managed pool,
// and suggest_groups is the only way an agent can SEE candidates at all
// (there is no import-predict tool). The ratio floor and strict mode do
// not: neither changes a byte written — the floor only decides whether
// PULSE_DEDUP_LOW_RATIO is raised (at the default 2.0) and strict only
// promotes that warning to a failure — so an agent reading group_warnings
// already holds both. Constant elision is absent for the same reason as
// spss_missing's opposite: its value is at-rest bytes, and a managed
// import is a transient TTL'd cache file.

// DedupIn is the input contract for pulse_dedup.
//
// The slot policy is pulse_import's (see the note above): groups and
// suggest_groups earn slots because they are the only way to write, or
// even see, a deduplicated cohort over MCP; the ratio floor and strict
// mode change no byte written, so an agent reading group_warnings holds
// both already; constant elision stays CLI + library only — it is an
// at-rest opt-in the user makes deliberately (`pulse dedup
// --elide-constants`). out is the one slot pulse_import has no need
// of: it is how an agent converts WITHOUT destroying the original.
type DedupIn struct {
	Path          string          `json:"path" jsonschema:"Path to the existing single-file .pulse cohort to deduplicate (relative to PULSE_DATA_DIR). Shard archives are refused with SERVICE_VALIDATION."`
	Groups        []pio.GroupDecl `json:"groups,omitempty" jsonschema:"Parent-group declarations, one object per group: {key: [fields...], members: [fields...]} — the same shape pulse_import takes. Each distinct member tuple is stored ONCE and every row carries a 4-byte index. FAILS with PULSE_GROUP_MEMBER_NOT_CONSTANT (cohort untouched) if a member varies within its key. Take declarations from suggest_groups candidates rather than guessing. An already-grouped cohort is regrouped from scratch. Unknown keys inside an entry are rejected with PULSE_GROUP_DECLARATION_INVALID."`
	SuggestGroups bool            `json:"suggest_groups,omitempty" jsonschema:"Detect candidate parent groups over the cohort's records and return them as group_candidates (ratio, resident dictionary bytes, projected size, verdict), each with key/members ready to pass back as a groups entry. With no groups this call is READ-ONLY. The loop is: call with suggest_groups only, read group_candidates.suggested, call again with those as groups."`
	Out           string          `json:"out,omitempty" jsonschema:"Write the deduplicated cohort to this NEW path (must not exist) and leave the original untouched. Omit to rewrite the cohort IN PLACE (destructive, atomic: a failure leaves it byte-identical). Prefer out unless the user asked for an in-place conversion."`
}

// DropIn is the input contract for pulse_drop.
type DropIn struct {
	Handle string `json:"handle" jsonschema:"Managed handle name to remove"`
}

// ImportsListIn is the (empty) input contract for pulse_imports_list.
type ImportsListIn struct{}

// LabelTablesIn is the (empty) input contract for pulse_label_tables.
type LabelTablesIn struct{}

// RangeTablesIn is the (empty) input contract for pulse_range_tables.
type RangeTablesIn struct{}

// LabelResolveIn is the input contract for pulse_label_resolve.
type LabelResolveIn struct {
	Table string `json:"table" jsonschema:"Label table name (from pulse_label_tables, e.g. 'brand')"`
	Query string `json:"query,omitempty" jsonschema:"Name to resolve, case-insensitive; empty returns the first rows (browse mode)"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum matches to return. Default 10."`
}

// --- Payload-tool inputs ------------------------------------------------------
//
// Payload tools carry the full structured request as their input. These alias
// the canonical types so a consumer codes against the same payload structs the
// engine validates. They reflect cleanly (non-cyclic at the Go level); the
// rich human-facing payload schema is also available via
// descriptor.BuildPayloadSchema and the pulse://schema resource.

// ProcessIn is the input contract for pulse_process.
type ProcessIn = types.Request

// PredictIn is the input contract for pulse_predict: a bare
// types.Request at the root (the original contract, unchanged), or
// exactly ONE alternative root naming a different request shape. A root
// key beside an alternative, or two alternatives, is SERVICE_VALIDATION;
// an alternative whose capability the instance hides is an unknown key.
type PredictIn struct {
	types.Request
	Composed *types.ComposedRequest `json:"composed,omitempty" jsonschema:"A pulse_compose request to predict instead of a bare request: each slot over its own cohort, then the batch overlays. Alone at the root."`
	Facet    *types.FacetRequest    `json:"facet,omitempty" jsonschema:"A pulse_facet_schema request to predict instead of a bare request. Alone at the root."`
	Chain    *types.ChainRequest    `json:"chain,omitempty" jsonschema:"A pulse_process_chain request to predict instead of a bare request: stage 0 over the cohort, each later stage over the schema the one before produces. Alone at the root."`
}

// ComposeIn is the input contract for pulse_compose.
type ComposeIn = types.ComposedRequest

// ProcessChainIn is the input contract for pulse_process_chain.
type ProcessChainIn = types.ChainRequest

// FacetSchemaIn is the input contract for pulse_facet_schema.
type FacetSchemaIn = types.FacetRequest

// LookupIn is the input contract for pulse_lookup — the structured point-
// lookup request (cohort, key components, return columns, multiplicity) at
// top level.
type LookupIn = types.LookupRequest

// --- Outputs ------------------------------------------------------------------

// InspectOut is the output contract for pulse_inspect: the inspect
// result with the header read's own DIAGNOSTICS beside it.
//
// The result is EMBEDDED, so every key an agent already parses stays
// exactly where it was and `warnings` is purely additive. The slot
// exists because record_count is a DERIVED figure — payload bytes
// divided by the record stride — and a cohort whose payload is not a
// whole multiple of that stride reports the FLOOR. descriptor.Inspect
// raises an ENCODING_INVALID warning saying so; pulse.Inspect discards
// it, so routing this tool through that wrapper left an MCP agent
// unable to see a truncated tail at all. The CLI reads the same
// envelope (`pulse cohort inspect --json`), so the two surfaces now
// report the same thing.
//
// Warnings is []*descriptor.EnvelopeEntry rather than []string on
// purpose: an agent that can read the code can look it up with
// pulse_errors_lookup, and the details map carries record_stride and
// trailing_bytes — the two numbers needed to act on it.
type InspectOut struct {
	descriptor.InspectResult
	Warnings []*descriptor.EnvelopeEntry `json:"warnings,omitempty" jsonschema:"Diagnostics from the header read — coded {code, message, details} entries. A truncated payload tail raises ENCODING_INVALID and record_count is the floor. Absent when the read was clean."`
}

// PredictOut is the output contract for pulse_predict. A bare request
// answers with the PredictResult EMBEDDED, so its keys sit at the root
// byte-identically to the original contract. An alternative root
// answers under the same key it was sent under, beside the coded
// errors / warnings that explain a `valid: false` verdict.
type PredictOut struct {
	*descriptor.PredictResult
	Composed *pulse.ComposePredictResult `json:"composed,omitempty" jsonschema:"The verdict on a composed root: valid, the echoed request, rejected overlay pairs, overlay cost, p-value count and each slot's advisories."`
	Facet    *pulse.FacetPredictResult   `json:"facet,omitempty" jsonschema:"The verdict on a facet root: valid, the echoed request, schema info and the accepted overlays."`
	Chain    *pulse.ChainPredictResult   `json:"chain,omitempty" jsonschema:"The verdict on a chain root: valid, the echoed request, the source schema and each stage's inferred output columns."`
	Errors   []*descriptor.EnvelopeEntry `json:"errors,omitempty" jsonschema:"Alternative roots only: every coded refusal behind valid false — {code, message, details}. Absent when there is none."`
	Warnings []*descriptor.EnvelopeEntry `json:"warnings,omitempty" jsonschema:"Alternative roots only: coded warnings. Absent when there is none."`
}

// ProcessOut is the output contract for pulse_process.
type ProcessOut = types.Response

// ComposeOut is the output contract for pulse_compose.
type ComposeOut = types.ComposedResponse

// ProcessChainOut is the output contract for pulse_process_chain.
type ProcessChainOut = types.ChainResponse

// FacetSchemaOut is the output contract for pulse_facet_schema.
type FacetSchemaOut = types.FacetResult

// LookupOut is the output contract for pulse_lookup — wraps the matched
// row(s) (Rows) plus any diagnostic Warnings.
type LookupOut = types.LookupResult

// ManifestOut is the output contract for pulse_manifest (the slim manifest is
// the same struct as the full one).
type ManifestOut = descriptor.Manifest

// ExamplesGetOut is the output contract for pulse_examples_get.
type ExamplesGetOut = examples.Example

// ImportOut is the output contract for pulse_import.
type ImportOut = imports.Result

// SampleOut is the output contract for pulse_sample.
type SampleOut struct {
	Rows []map[string]any `json:"rows" jsonschema:"Sampled rows, each a field-name to value map"`
}

// FacetOut is the output contract for pulse_facet.
type FacetOut struct {
	Values []string `json:"values" jsonschema:"Distinct values for the faceted field"`
}

// SkillsListOut is the output contract for pulse_skills_list.
type SkillsListOut struct {
	Skills []skills.Metadata `json:"skills" jsonschema:"Embedded skill-pack entries (name, description, applies_to)"`
}

// SkillsGetOut is the output contract for pulse_skills_get.
type SkillsGetOut struct {
	Body string `json:"body" jsonschema:"Markdown body of the named skill"`
}

// ExamplesSearchOut is the output contract for pulse_examples_search.
type ExamplesSearchOut struct {
	Results []examples.ExampleSummary `json:"results" jsonschema:"Matching example summaries (name, category, tags, operators, description)"`
}

// ErrorsLookupOut is the output contract for pulse_errors_lookup.
type ErrorsLookupOut struct {
	Results []perr.LookupResult `json:"results" jsonschema:"Matching error-code metadata records"`
}

// RecommendIn is the input contract for pulse_recommend. Cohort is a
// path (pulse_inspect's convention), lifted onto
// descriptor.RecommendRequest's cohort by the handler; every other
// slot is the request's own.
type RecommendIn struct {
	Intent string   `json:"intent" jsonschema:"Intent ID naming the kind of question (the intents skill lists every ID). An unknown ID is PULSE_RECOMMEND_INTENT_UNKNOWN; its details.valid lists the IDs."`
	Cohort string   `json:"cohort,omitempty" jsonschema:"Path to a .pulse cohort (relative to PULSE_DATA_DIR). Set it to bind drafts to the cohort's fields and predict-validate each one; omit it for unbound placeholder skeletons."`
	Fields []string `json:"fields,omitempty" jsonschema:"Field hints, cohort only: each names a cohort field whose kind the intent takes and pins the first role it fits. A hint that is not a field, or fits no role, is SERVICE_VALIDATION."`
	Level  string   `json:"level,omitempty" jsonschema:"Rank operators at this level first: basic, intermediate or advanced. Omit to rank the simplest suitable operator first."`
	Limit  int      `json:"limit,omitempty" jsonschema:"Maximum recommendations returned (default 10). truncated reports a cut; candidates_considered counts the survivors before it."`
}

// RecommendOut is the output contract for pulse_recommend.
type RecommendOut = descriptor.RecommendResult

// ExplainIn is the input contract for pulse_explain: the roots of
// descriptor.ExplainRequest as open objects. A result root is decoded
// with the undefined-figure rule (a null figure stays undefined, never
// 0); a request root with plain encoding/json.
type ExplainIn struct {
	Request          map[string]any `json:"request,omitempty" jsonschema:"A pulse_process request body. Alone: described before it runs (predict-checked when it names a cohort). Beside response: the request that produced it."`
	Composed         map[string]any `json:"composed,omitempty" jsonschema:"A pulse_compose request. Alone: described before it runs. Beside composed_response: the request that produced it."`
	Chain            map[string]any `json:"chain,omitempty" jsonschema:"A pulse_process_chain request. Alone: described before it runs. Beside chain_response: the request that produced it."`
	Facet            map[string]any `json:"facet,omitempty" jsonschema:"A pulse_facet request. Alone: described before it runs. Beside facet_result: the request that produced it."`
	Sample           map[string]any `json:"sample,omitempty" jsonschema:"A pulse_sample request, described before it runs (never predicted)."`
	Response         map[string]any `json:"response,omitempty" jsonschema:"A pulse_process result to read into findings, exactly as returned (null figures stay undefined)."`
	ComposedResponse map[string]any `json:"composed_response,omitempty" jsonschema:"A pulse_compose result ({responses, overlays}) to read slot by slot."`
	ChainResponse    map[string]any `json:"chain_response,omitempty" jsonschema:"A pulse_process_chain result to read stage by stage (its echoed normalized_request is read first)."`
	FacetResult      map[string]any `json:"facet_result,omitempty" jsonschema:"A pulse_facet result to read field by field."`
	Detail           string         `json:"detail,omitempty" jsonschema:"terse (default: summary, findings, steps, caveats) or full (adds narrative sentences, glossary refs, follow-ups and assumptions)."`
}

// ExplainOut is the output contract for pulse_explain.
type ExplainOut = descriptor.ExplainResult

// DedupOut is the output contract for pulse_dedup.
type DedupOut = pulse.DedupResult

// DropOut is the output contract for pulse_drop.
type DropOut struct {
	Handle  string `json:"handle" jsonschema:"The managed handle that was dropped"`
	Dropped bool   `json:"dropped" jsonschema:"True when the handle existed and was removed"`
}

// ImportsListOut is the output contract for pulse_imports_list.
type ImportsListOut struct {
	Imports []imports.Entry `json:"imports" jsonschema:"Managed-import pool entries with sidecar metadata"`
}

// LabelTablesOut is the output contract for pulse_label_tables.
type LabelTablesOut struct {
	Tables []pulse.LabelTableInfo `json:"tables" jsonschema:"Registered label tables (name, row count, enumerable flag)"`
}

// LabelResolveOut is the output contract for pulse_label_resolve.
type LabelResolveOut struct {
	Matches []pulse.LabelMatch `json:"matches" jsonschema:"Ranked (key, value, score) resolution candidates"`
}

// RangeTablesOut is the output contract for pulse_range_tables.
type RangeTablesOut struct {
	Tables []pulse.RangeTableInfo `json:"tables" jsonschema:"Registered range tables (name, range count, ordered labeled date ranges)"`
}
