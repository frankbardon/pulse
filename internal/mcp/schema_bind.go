package mcp

// schema_bind.go derives session-scoped tool variants whose JSON Schemas
// embed enum constraints on field-name parameters. The trigger is a
// successful pulse_inspect call: once the server has learned the schema of
// a .pulse file in this session, we register bound variants of the
// action tools so the LLM picks field names from a typed list instead of
// free-texting them.
//
// The bound variants reuse the global tool names; mcp-go's session-scoped
// tools override globals for that session. Multi-file binding is not
// supported in v1: the latest inspect wins. A documented limitation; see
// skills/mcp-integration.md.
//
// JSON Schema limitations note: per-element correlation between an
// operator-type enum and the field-name enum (e.g. "AGG_SUM permitted only
// for numeric fields") is hard to express across array elements. The v1
// cut applies enums on field names and on the operator-type catalogues,
// and relies on Type-level descriptions to convey operator compatibility.
// Strict correlation enforcement remains the job of predict.

import (
	"encoding/json"
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/mcp/mcptools"
	"github.com/frankbardon/pulse/types"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// fieldClassification slots schema fields into the coarse categories the
// JSON Schema builders care about. Membership is mutually exclusive within
// the primary slots (numeric vs categorical vs date vs bool); AllFields is
// the union for filterer/group references.
type fieldClassification struct {
	AllFields     []string
	Numeric       []string // u4/u8/u16/u32/u64, f32/f64, decimal128
	NumericNoDec  []string // numeric minus decimal (kept for symmetry with capabilities tables)
	Categorical   []string // categorical_u8/u16/u32
	Date          []string // date
	Bool          []string // packed_bool
	NumericOrDate []string // window OrderBy targets
}

// classifyFields sorts schema fields into per-type-category buckets. Order
// within each slice follows declaration order in the schema; we do not
// resort because the JSON Schema enum order is preserved verbatim and a
// stable order keeps test goldens deterministic.
func classifyFields(schema *encoding.Schema) fieldClassification {
	c := fieldClassification{}
	if schema == nil {
		return c
	}
	for _, f := range schema.Fields {
		c.AllFields = append(c.AllFields, f.Name)
		switch {
		case isNumericType(f.Type):
			c.Numeric = append(c.Numeric, f.Name)
			c.NumericOrDate = append(c.NumericOrDate, f.Name)
			if !f.Type.IsDecimal() {
				c.NumericNoDec = append(c.NumericNoDec, f.Name)
			}
		case f.Type.IsCategorical():
			c.Categorical = append(c.Categorical, f.Name)
		case f.Type == encoding.FieldTypeDate:
			c.Date = append(c.Date, f.Name)
			c.NumericOrDate = append(c.NumericOrDate, f.Name)
		case f.Type == encoding.FieldTypePackedBool:
			c.Bool = append(c.Bool, f.Name)
		}
	}
	return c
}

// isNumericType reports whether the type participates in numeric operators
// as seen by the MCP schema binder. Delegates to the canonical
// encoding.FieldType.IsNumericForAnalytics() so the LLM-facing "numeric
// field" enum matches what aggregator + regression validators now accept
// (bit-packed integer encodings included). Date stays in this bucket too
// because the binder treats it as a numeric-orderable target via
// classifyFields' NumericOrDate slice.
func isNumericType(t encoding.FieldType) bool {
	return t.IsNumericForAnalytics()
}

// stringSlice is a small helper so the builders can stay dense.
func stringSlice[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

// Bind returns per-tool JSON Schemas keyed by tool name with no
// embedder extensions applied. Equivalent to BindWithExtensions with
// a nil snapshot.
func Bind(schema *encoding.Schema) (map[string]json.RawMessage, error) {
	return BindWithExtensions(schema, nil)
}

// BindWithExtensions returns per-tool JSON Schemas keyed by tool
// name, merging any embedder-registered operator names into the
// per-category enums so LLM agents can author requests that
// reference custom operators. Empty schemas are omitted so the
// caller can decide which tools to override.
func BindWithExtensions(schema *encoding.Schema, snap *descriptor.ExtensionsSnapshot) (map[string]json.RawMessage, error) {
	if schema == nil {
		return nil, nil
	}
	c := classifyFields(schema)

	out := make(map[string]json.RawMessage, 5)

	// pulse_process and pulse_predict share the same Request shape; the
	// builder produces one schema body and we register it under both names.
	reqBody, err := buildRequestSchemaWithExtensions(c, snap)
	if err != nil {
		return nil, err
	}
	out[mcptools.ToolProcess] = reqBody
	out[mcptools.ToolPredict] = reqBody

	composeBody, err := buildComposeSchemaWithExtensions(c, snap)
	if err != nil {
		return nil, err
	}
	out[mcptools.ToolCompose] = composeBody

	sampleBody, err := buildSampleSchema(c)
	if err != nil {
		return nil, err
	}
	out[mcptools.ToolSample] = sampleBody

	facetBody, err := buildFacetSchema(c)
	if err != nil {
		return nil, err
	}
	out[mcptools.ToolFacet] = facetBody

	facetSchemaBody, err := buildFacetSchemaRequestSchema(c, snap)
	if err != nil {
		return nil, err
	}
	out[mcptools.ToolFacetSchema] = facetSchemaBody

	return out, nil
}

// buildFacetSchemaRequestSchema describes the pulse_facet_schema tool
// with field enums constrained to the bound cohort. The fields,
// additive_fields, and per-filterer field references all draw from the
// schema's field list; filterer types reuse the global filterer enum.
func buildFacetSchemaRequestSchema(c fieldClassification, snap *descriptor.ExtensionsSnapshot) (json.RawMessage, error) {
	filterTypes := mergeEnumNames(stringSlice(types.AllFiltererTypes()), snap, "filterer")
	requestObject := map[string]any{
		"type":        "object",
		"description": "pulse.FacetRequest — schema-bound for this cohort. fields and additive_fields enums are constrained to the cohort's actual fields.",
		"properties": map[string]any{
			"cohort": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filename": map[string]any{"type": "string"},
					"data_dir": map[string]any{"type": "string"},
				},
				"required":             []string{"filename"},
				"additionalProperties": true,
			},
			"fields": map[string]any{
				"type":  "array",
				"items": enumStringField(c.AllFields, "Field name to summarise."),
			},
			"additive_fields": map[string]any{
				"type":  "array",
				"items": enumStringField(c.AllFields, "Field name to compute additive contribution counts for."),
			},
			"filterers": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":       map[string]any{"type": "string", "enum": filterTypes},
						"field":      enumStringField(c.AllFields, "Field to filter on. FILTER_EXPRESSION may omit this."),
						"values":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"expression": map[string]any{"type": "string"},
					},
					"required":             []string{"type"},
					"additionalProperties": true,
				},
			},
			"discrete_top_k": map[string]any{
				"type":        "integer",
				"description": "Cap discrete values per field; 0 means no cap.",
			},
			"numeric_percentiles": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "number", "description": "Strictly in (0, 1)."},
			},
			"include_histogram": map[string]any{"type": "boolean"},
			"histogram_bins":    map[string]any{"type": "integer"},
			"histogram_range": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "number"},
				"minItems":    2,
				"maxItems":    2,
				"description": "[min, max] bounds for fixed-width histogram binning. Required when include_histogram=true.",
			},
		},
		"required":             []string{"fields"},
		"additionalProperties": true,
	}
	outer := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request": requestObject,
		},
		"required":             []string{"request"},
		"additionalProperties": true,
	}
	return json.Marshal(outer)
}

// extensionNames returns the operator-name slice for a category from
// the snapshot, or nil when no snapshot is registered. Used by the
// enum-merging helper below.
func extensionNames(snap *descriptor.ExtensionsSnapshot, category string) []string {
	if snap == nil {
		return nil
	}
	var metas []descriptor.OperatorMeta
	switch category {
	case "aggregator":
		metas = snap.Aggregators
	case "attribute":
		metas = snap.Attributes
	case "filterer":
		metas = snap.Filterers
	case "grouper":
		metas = snap.Groupers
	case "window":
		metas = snap.Windows
	case "feature":
		metas = snap.Features
	case "test":
		metas = snap.Tests
	default:
		return nil
	}
	if len(metas) == 0 {
		return nil
	}
	out := make([]string, len(metas))
	for i, m := range metas {
		out[i] = m.Name
	}
	return out
}

// mergeEnumNames merges built-in names with extension names from the
// snapshot, sorting the combined list and removing duplicates. The
// result is the enum used for a category's `type` field in the
// schema-bound tool schema.
func mergeEnumNames(builtin []string, snap *descriptor.ExtensionsSnapshot, category string) []string {
	customs := extensionNames(snap, category)
	if len(customs) == 0 {
		return builtin
	}
	combined := make([]string, 0, len(builtin)+len(customs))
	combined = append(combined, builtin...)
	combined = append(combined, customs...)
	seen := make(map[string]struct{}, len(combined))
	out := make([]string, 0, len(combined))
	for _, n := range combined {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// orderKeySchema is the shared schema for OrderKey entries used by
// windows and tier-1/tier-2 tests. enumFields constrains the field name.
func orderKeySchema(enumFields []string) map[string]any {
	field := map[string]any{
		"type":        "string",
		"description": "Field name to order by",
	}
	if len(enumFields) > 0 {
		field["enum"] = enumFields
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"field": field,
			"desc": map[string]any{
				"type":        "boolean",
				"description": "Descending order (default false)",
			},
		},
		"required":             []string{"field"},
		"additionalProperties": true,
	}
}

// buildRequestSchemaWithExtensions produces a JSON Schema describing
// types.Request with per-field enums for the bound cohort and
// optionally with embedder-registered operator names merged into
// each category enum.
func buildRequestSchemaWithExtensions(c fieldClassification, snap *descriptor.ExtensionsSnapshot) (json.RawMessage, error) {
	aggTypes := mergeEnumNames(stringSlice(types.AllAggregationTypes()), snap, "aggregator")
	attrTypes := mergeEnumNames(stringSlice(types.AllAttributeTypes()), snap, "attribute")
	filterTypes := mergeEnumNames(stringSlice(types.AllFiltererTypes()), snap, "filterer")
	groupTypes := mergeEnumNames(stringSlice(types.AllGroupTypes()), snap, "grouper")
	windowTypes := mergeEnumNames(stringSlice(types.AllWindowTypes()), snap, "window")
	featureTypes := mergeEnumNames(stringSlice(types.AllFeatureTypes()), snap, "feature")
	testTypes := mergeEnumNames(stringSlice(types.AllTestTypes()), snap, "test")

	requestObject := map[string]any{
		"type":        "object",
		"description": "types.Request — schema-bound for this cohort. Field-name enums are constrained to the cohort's actual fields.",
		"properties": map[string]any{
			"cohort": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filename": map[string]any{"type": "string"},
					"data_dir": map[string]any{"type": "string"},
				},
				"required":             []string{"filename"},
				"additionalProperties": true,
			},
			"aggregations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type": map[string]any{
							"type":        "string",
							"enum":        aggTypes,
							"description": "Aggregator. Operators differ in accepted field-type classes — AGG_SUM/AVG/STDDEV/MIN/MAX need numeric fields; AGG_COUNT/FREQUENCY/MODE/DISTINCT_COUNT accept any. See pulse_manifest for the full Accepts table.",
						},
						"field":  enumStringField(c.AllFields, "Field to aggregate. Categorical and decimal fields are valid for some operators only — see Type description and the manifest's Operator.AcceptsTypes."),
						"label":  map[string]any{"type": "string"},
						"params": map[string]any{},
					},
					"required":             []string{"type", "field"},
					"additionalProperties": true,
				},
			},
			"attributes": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":       map[string]any{"type": "string", "enum": attrTypes},
						"field":      enumStringField(c.Numeric, "Source field (numeric, including decimal)."),
						"label":      map[string]any{"type": "string"},
						"expression": map[string]any{"type": "string"},
						"params":     map[string]any{},
					},
					"required":             []string{"type", "field"},
					"additionalProperties": true,
				},
			},
			"filterers": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":       map[string]any{"type": "string", "enum": filterTypes},
						"field":      enumStringField(c.AllFields, "Field to filter on. FILTER_EXPRESSION may omit this."),
						"values":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"expression": map[string]any{"type": "string"},
					},
					"required":             []string{"type"},
					"additionalProperties": true,
				},
			},
			"groups": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":     map[string]any{"type": "string", "enum": groupTypes},
						"field":    enumStringField(c.AllFields, "Field to group by. GROUP_CATEGORY expects a categorical field; GROUP_ROUNDED/RANGE expect numeric; GROUP_DATE expects date."),
						"interval": map[string]any{"type": "number"},
						"params":   map[string]any{},
					},
					"required":             []string{"type", "field"},
					"additionalProperties": true,
				},
			},
			"windows": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":         map[string]any{"type": "string", "enum": windowTypes},
						"field":        enumStringField(c.AllFields, "Source field for the window operator (omitted for ROW_NUMBER/RANK/DENSE_RANK)."),
						"label":        map[string]any{"type": "string"},
						"partition_by": map[string]any{"type": "array", "items": enumStringField(c.AllFields, "Partition key field name.")},
						"order_by":     map[string]any{"type": "array", "items": orderKeySchema(c.NumericOrDate)},
						"frame":        map[string]any{"type": "object"},
						"params":       map[string]any{},
					},
					"required":             []string{"type", "order_by"},
					"additionalProperties": true,
				},
			},
			"features": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type":   map[string]any{"type": "string", "enum": featureTypes},
						"field":  enumStringField(c.AllFields, "Source field for the feature operator."),
						"label":  map[string]any{"type": "string"},
						"params": map[string]any{},
					},
					"required":             []string{"type"},
					"additionalProperties": true,
				},
			},
			"sort": map[string]any{
				"type":  "array",
				"items": orderKeySchema(nil), // sort keys may reference output labels; do not constrain
			},
			"tests":      testsArraySchema(c, testTypes),
			"post_tests": testsArraySchema(c, testTypes),
			"outputs": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"format":      map[string]any{"type": "string"},
						"filename":    map[string]any{"type": "string"},
						"pretty":      map[string]any{"type": "boolean"},
						"include_nil": map[string]any{"type": "boolean"},
					},
					"additionalProperties": true,
				},
			},
		},
		"additionalProperties": true,
	}

	outer := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request": requestObject,
		},
		"required":             []string{"request"},
		"additionalProperties": true,
	}
	return json.Marshal(outer)
}

// testsArraySchema returns the JSON Schema for a tests/post_tests array
// with field references constrained by classification.
func testsArraySchema(c fieldClassification, testTypes []string) map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":          map[string]any{"type": "string", "enum": testTypes},
				"field":         enumStringField(c.Numeric, "Primary numeric field under test."),
				"field2":        enumStringField(c.Numeric, "Secondary numeric field (paired / bivariate tests)."),
				"split_by":      enumStringField(c.AllFields, "Categorical split field for two-sample / k-group tests."),
				"rows":          enumStringField(c.AllFields, "Row-axis categorical field for TEST_CHISQ."),
				"cols":          enumStringField(c.AllFields, "Column-axis categorical field for TEST_CHISQ."),
				"subject_field": enumStringField(c.AllFields, "Within-subject grouping for TEST_ANOVA_RM."),
				"alpha":         map[string]any{"type": "number"},
				"label":         map[string]any{"type": "string"},
				"order_by":      map[string]any{"type": "array", "items": orderKeySchema(c.NumericOrDate)},
				"params":        map[string]any{},
			},
			"required":             []string{"type"},
			"additionalProperties": true,
		},
	}
}

// buildComposeSchema describes a ComposedRequest by wrapping the bound
// Request schema in a requests array. Each entry inherits the same
// per-cohort enum constraints — multi-file batches that target a
// different cohort will still pass the schema check (the enum is a
// "best-known" guide) but predict will catch field-name mismatches.
// buildComposeSchemaWithExtensions mirrors buildRequestSchema with
// the extension snapshot routed through so the nested request shape
// includes custom operator names in its enums.
func buildComposeSchemaWithExtensions(c fieldClassification, snap *descriptor.ExtensionsSnapshot) (json.RawMessage, error) {
	inner, err := buildRequestSchemaWithExtensions(c, snap)
	if err != nil {
		return nil, err
	}
	var innerOuter map[string]any
	if err := json.Unmarshal(inner, &innerOuter); err != nil {
		return nil, err
	}
	// Extract the request sub-schema so we can nest it under requests[].
	props, _ := innerOuter["properties"].(map[string]any)
	reqSchema := props["request"]

	outer := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"requests": map[string]any{
						"type":  "array",
						"items": reqSchema,
					},
				},
				"required":             []string{"requests"},
				"additionalProperties": true,
			},
		},
		"required":             []string{"request"},
		"additionalProperties": true,
	}
	return json.Marshal(outer)
}

// buildSampleSchema describes the pulse_sample tool params. count stays a
// number; path is unconstrained because sample doesn't read schema.
func buildSampleSchema(c fieldClassification) (json.RawMessage, error) {
	outer := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":  map[string]any{"type": "string", "description": "Filesystem path to the .pulse file"},
			"count": map[string]any{"type": "number", "description": "Maximum rows to return (default 10)"},
		},
		"required":             []string{"path"},
		"additionalProperties": true,
	}
	// Sample doesn't reference field names. Classification is unused but
	// kept in signature for symmetry with the other builders.
	_ = c
	return json.Marshal(outer)
}

// buildFacetSchema describes the pulse_facet tool with the field arg
// constrained to schema fields.
func buildFacetSchema(c fieldClassification) (json.RawMessage, error) {
	field := map[string]any{
		"type":        "string",
		"description": "Field name to facet (returns distinct values)",
	}
	if len(c.AllFields) > 0 {
		field["enum"] = c.AllFields
	}
	outer := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":  map[string]any{"type": "string", "description": "Filesystem path to the .pulse file"},
			"field": field,
		},
		"required":             []string{"path", "field"},
		"additionalProperties": true,
	}
	return json.Marshal(outer)
}

// enumStringField returns a property schema for a string with an enum
// drawn from values. When values is empty (no fields of this category in
// the cohort), the enum is omitted so the schema stays valid — predict
// will still reject the request, but the schema doesn't list an empty
// enum which some validators reject outright.
func enumStringField(values []string, description string) map[string]any {
	out := map[string]any{
		"type": "string",
	}
	if description != "" {
		out["description"] = description
	}
	if len(values) > 0 {
		out["enum"] = values
	}
	return out
}

// BindSessionTools is the entry point used by handleInspect. Given a
// schema, it derives per-tool JSON Schemas and registers them as
// session-scoped tools that override the global variants for the
// current session. mcp-go fires notifications/tools/list_changed on
// success.
func BindSessionTools(s *server.MCPServer, sessionID string, schema *encoding.Schema, handlers boundHandlers) error {
	return BindSessionToolsWithExtensions(s, sessionID, schema, nil, handlers)
}

// BindSessionToolsWithExtensions mirrors BindSessionTools but routes
// an extensions snapshot into the per-tool JSON Schemas so
// embedder-registered operator names appear in the enum lists.
func BindSessionToolsWithExtensions(s *server.MCPServer, sessionID string, schema *encoding.Schema, snap *descriptor.ExtensionsSnapshot, handlers boundHandlers) error {
	if s == nil || sessionID == "" || schema == nil {
		return nil
	}
	schemas, err := BindWithExtensions(schema, snap)
	if err != nil {
		return err
	}
	bound := make([]server.ServerTool, 0, len(schemas))
	for _, entry := range []struct {
		name        string
		description string
		handler     server.ToolHandlerFunc
	}{
		{mcptools.ToolProcess, mcptools.DescProcess + " (schema-bound)", handlers.process},
		{mcptools.ToolPredict, mcptools.DescPredict + " (schema-bound)", handlers.predict},
		{mcptools.ToolCompose, mcptools.DescCompose + " (schema-bound)", handlers.compose},
		{mcptools.ToolSample, mcptools.DescSample + " (schema-bound)", handlers.sample},
		{mcptools.ToolFacet, mcptools.DescFacet + " (schema-bound)", handlers.facet},
		{mcptools.ToolFacetSchema, mcptools.DescFacetSchema + " (schema-bound)", handlers.facetSchema},
	} {
		raw, ok := schemas[entry.name]
		if !ok {
			continue
		}
		tool := mcpgo.NewToolWithRawSchema(entry.name, entry.description, raw)
		bound = append(bound, server.ServerTool{Tool: tool, Handler: entry.handler})
	}
	if len(bound) == 0 {
		return nil
	}
	return s.AddSessionTools(sessionID, bound...)
}

// boundHandlers carries the per-tool handler closures so BindSessionTools
// can attach them to the bound variants. We reuse the existing global
// handlers verbatim — the wire-shape stays identical; only the input
// schema changes.
type boundHandlers struct {
	process     server.ToolHandlerFunc
	predict     server.ToolHandlerFunc
	compose     server.ToolHandlerFunc
	sample      server.ToolHandlerFunc
	facet       server.ToolHandlerFunc
	facetSchema server.ToolHandlerFunc
}
