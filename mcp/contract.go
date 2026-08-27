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
	"github.com/frankbardon/pulse/examples"
	"github.com/frankbardon/pulse/imports"
	"github.com/frankbardon/pulse/skills"
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

// SkillsListIn is the (empty) input contract for pulse_skills_list.
type SkillsListIn struct{}

// SkillsGetIn is the input contract for pulse_skills_get.
type SkillsGetIn struct {
	Name string `json:"name" jsonschema:"Skill name (e.g. 'aggregation-design')"`
}

// ManifestIn is the (empty) input contract for pulse_manifest.
type ManifestIn struct{}

// ExamplesSearchIn is the input contract for pulse_examples_search. All three
// filters are optional and ANDed.
type ExamplesSearchIn struct {
	Query    string   `json:"query,omitempty" jsonschema:"Case-insensitive substring matched against name, description, and operators"`
	Tags     []string `json:"tags,omitempty" jsonschema:"Canonical taxonomy tags; results must carry every tag (AND)"`
	Category string   `json:"category,omitempty" jsonschema:"Exact directory: aggregations, attributes, features, filterers, groupers, regression, tests, windows"`
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

// PredictIn is the input contract for pulse_predict.
type PredictIn = types.Request

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

// InspectOut is the output contract for pulse_inspect.
type InspectOut = descriptor.InspectResult

// PredictOut is the output contract for pulse_predict.
type PredictOut = descriptor.PredictResult

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
