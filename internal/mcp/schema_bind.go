package mcp

// schema_bind.go derives session-scoped tool variants whose JSON Schemas
// embed enum constraints on field-name parameters. The trigger is a
// successful pulse_inspect call: once the server has learned the schema of
// a .pulse file in this session, we register bound variants of the
// action tools so the LLM picks field names from a typed list instead of
// free-texting them.
//
// go-sdk has no per-session tool override — the Server holds one global
// tool set keyed by name. Over stdio that is exactly what we want: one
// process = one session = one Server, so re-registering a tool by name
// (Server.AddTool replaces same-name entries) mutates this session's view
// and go-sdk auto-emits notifications/tools/list_changed to the connected
// client. Multi-file binding is not supported in v1: the latest inspect
// wins. A documented limitation; see docs/src/internals/adding-mcp-tool.md.
//
// Concurrency note: divergent schemas across CONCURRENT sessions would
// require one Server per session (the HTTP StreamableHTTPHandler getServer
// factory pattern). That is an HTTP consumer's concern and is not handled
// here — the stdio server this package serves has a single session.
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

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
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
	Set           []string // set_u8/u16/u32/u64 multi-select bitmasks
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
		case f.Type.IsSet():
			c.Set = append(c.Set, f.Name)
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
	out[toolmeta.ToolProcess] = reqBody
	out[toolmeta.ToolPredict] = reqBody

	composeBody, err := buildComposeSchemaWithExtensions(c, snap)
	if err != nil {
		return nil, err
	}
	out[toolmeta.ToolCompose] = composeBody

	sampleBody, err := buildSampleSchema(c)
	if err != nil {
		return nil, err
	}
	out[toolmeta.ToolSample] = sampleBody

	facetBody, err := buildFacetSchema(c)
	if err != nil {
		return nil, err
	}
	out[toolmeta.ToolFacet] = facetBody

	facetSchemaBody, err := buildFacetSchemaRequestSchema(c, snap)
	if err != nil {
		return nil, err
	}
	out[toolmeta.ToolFacetSchema] = facetSchemaBody

	chainBody, err := buildProcessChainSchemaWithExtensions(c, snap)
	if err != nil {
		return nil, err
	}
	out[toolmeta.ToolProcessChain] = chainBody

	return out, nil
}

// buildProcessChainSchemaWithExtensions describes the pulse_process_chain
// tool with the per-cohort enum constraints applied to every stage's
// inner Request shape. ChainRequest.Stages[].Request inherits the same
// Request schema the pulse_process facade emits — stage 0's request
// supplies the source cohort, later stages ignore their inner cohort
// field and consume the prior stage's synthesised in-memory cohort.
// The outer ChainRequest.Overlays slot carries the whole-chain overlay
// catalog (CHAIN-host kinds bound by StageRef pointers).
func buildProcessChainSchemaWithExtensions(c fieldClassification, snap *descriptor.ExtensionsSnapshot) (json.RawMessage, error) {
	// Reuse the per-stage Request body the pulse_process facade emits
	// so per-cohort field enums + extension-merged operator enums
	// flow into every stage automatically.
	inner, err := buildRequestSchemaWithExtensions(c, snap)
	if err != nil {
		return nil, err
	}
	var reqSchema map[string]any
	if err := json.Unmarshal(inner, &reqSchema); err != nil {
		return nil, err
	}

	stageItem := map[string]any{
		"type":        "object",
		"description": "One stage in the source-rooted linear chain. Stage 0's request supplies the source cohort; later stages consume the prior stage's synthesised output.",
		"properties": map[string]any{
			"name":    map[string]any{"type": "string", "description": "Optional stage label used in PULSE_CHAIN_NOT_MERGEABLE error details."},
			"request": reqSchema,
		},
		"required":             []string{"request"},
		"additionalProperties": true,
	}

	requestObject := map[string]any{
		"type":        "object",
		"description": "pulse.ChainRequest — source-rooted linear chain. See types.ChainRequest.",
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
			"stages": map[string]any{
				"type":        "array",
				"description": "Ordered list of chain stages. Mergeable-only at v1; see Manifest.ProcessChain for the per-stage catalog gate.",
				"items":       stageItem,
			},
			// ChainRequest.Overlays carries the whole-chain catalog.
			"overlays": overlaysSchemaForFacade(overlayFacadeChain, snap),
		},
		"required":             []string{"stages"},
		"additionalProperties": true,
	}

	// Structured contract: ChainRequest fields at the top level.
	return json.Marshal(requestObject)
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
			// FacetRequest.Overlays accepts the four population-comparison
			// FACET-host kinds (Ref.Population resolves to a separate
			// cohort). Other kinds are routed onto their respective
			// facades.
			"overlays": overlaysSchemaForFacade(overlayFacadeFacet, snap),
		},
		"required":             []string{"fields"},
		"additionalProperties": true,
	}
	if labels := buildLabelsSchema(c, snap); labels != nil {
		requestObject["properties"].(map[string]any)["labels"] = labels
	}
	// Structured contract: FacetRequest fields at the top level.
	return json.Marshal(requestObject)
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
	case "overlay":
		metas = snap.OverlayKinds
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
				"type":        "array",
				"description": "Aggregation operations. The request key is \"aggregations\" (NOT \"aggregators\" — \"aggregators\" is the manifest's operator-catalog field name). Each entry selects an AGG_* operator.",
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
				"type":        "array",
				"description": "Grouping operations. The request key is \"groups\" (NOT \"groupers\" — \"groupers\" is the manifest's operator-catalog field name). Each entry selects a GROUP_* operator.",
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
			"crosstab":   crosstabSchema(c, aggTypes, groupTypes),
			"overlays":   overlaysSchemaForFacade(overlayFacadeRequest, snap),
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
	if labels := buildLabelsSchema(c, snap); labels != nil {
		requestObject["properties"].(map[string]any)["labels"] = labels
	}

	// Canonical structured contract (E2-S2): the tool input IS the typed
	// Request at the top level — no {request: ...} wrapper. Field-name
	// enum injection therefore lands on the same paths the reflected
	// base schema advertises.
	return json.Marshal(requestObject)
}

// crosstabSchema returns the JSON Schema for the Crosstab section. Row
// and column axis groupers reuse the same Group shape as the top-level
// groups array; the cell aggregation reuses the Aggregation shape; the
// normalize / shape enums are constrained to known values.
func crosstabSchema(c fieldClassification, aggTypes, groupTypes []string) map[string]any {
	groupItem := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":     map[string]any{"type": "string", "enum": groupTypes},
			"field":    enumStringField(c.AllFields, "Field to group by on this axis."),
			"interval": map[string]any{"type": "number"},
			"params":   map[string]any{},
		},
		"required":             []string{"type", "field"},
		"additionalProperties": true,
	}
	return map[string]any{
		"type":        "object",
		"description": "Cross-tabulation directive. Composes rows × columns groupers with a cell aggregation; emits matrix or long-form result. Mutually exclusive with top-level groups + aggregations.",
		"properties": map[string]any{
			"rows": map[string]any{
				"type":        "array",
				"description": "Row-axis groupers. Multiple entries produce nested rows.",
				"items":       groupItem,
			},
			"columns": map[string]any{
				"type":        "array",
				"description": "Column-axis groupers. Multiple entries produce nested columns.",
				"items":       groupItem,
			},
			"cell": map[string]any{
				"type":        "object",
				"description": "Cell aggregation. Most aggregators emit scalar cells (number in matrix payload). Map-valued aggregators (advertised under Manifest.Crosstab.MapValuedCellAggregators — AGG_SET_FREQUENCY today) emit per-label row-count maps (object); pairing them with normalize=row/column/total raises PULSE_CROSSTAB_NORMALIZE_MAP_VALUED.",
				"properties": map[string]any{
					"type":   map[string]any{"type": "string", "enum": aggTypes},
					"field":  enumStringField(c.AllFields, "Field the cell aggregation reads. AGG_COUNT may name any field."),
					"label":  map[string]any{"type": "string"},
					"params": map[string]any{},
				},
				"required":             []string{"type", "field"},
				"additionalProperties": true,
			},
			"margins": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"rows":    map[string]any{"type": "boolean"},
					"columns": map[string]any{"type": "boolean"},
					"grand":   map[string]any{"type": "boolean"},
				},
				"additionalProperties": false,
			},
			"normalize": map[string]any{
				"type":        "string",
				"enum":        []string{"none", "row", "column", "total"},
				"description": "Normalization direction. Row/column/total each divide cells by the corresponding margin; none leaves cells raw.",
			},
			"shape": map[string]any{
				"type":        "string",
				"enum":        []string{"matrix", "long"},
				"description": "Output shape. matrix (default) populates Response.Crosstab.Matrix; long emits tuple rows on Response.Data.",
			},
			"normalize_level": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "Zero-indexed depth in the nested axis whose value constitutes the 100% denominator. 0 selects the top-level grouper; len(axis)-1 (default) selects the leaf. Applies only when normalize is row or column; rejected when set with normalize=none or normalize=total.",
			},
			"normalize_within": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "Zero-indexed depth on the OPPOSITE axis whose value fixes a partition of the 100% denominator. With normalize=row it fixes a prefix of the column axis (so the denominator collapses only columns deeper than this level while holding the full row key); with normalize=column it fixes a prefix of the row axis. Composes independently with normalize_level. Rejected when normalize is none or total.",
			},
		},
		"required":             []string{"rows", "columns", "cell"},
		"additionalProperties": true,
	}
}

// overlayFacade identifies which MCP tool surface a per-facade
// overlay_kind enum belongs to. Each facade carries a constrained slice
// of the universal overlay catalog so LLM authors see only the kinds
// that are addressable on the bound tool's request shape:
//
//   - overlayFacadeRequest  — pulse_process / pulse_predict.
//     Request.Overlays accepts the in-Request
//     catalog: MATRIX-host kinds layered on a
//     crosstab (`Request.Crosstab`) plus
//     SERIES-host kinds layered on a grouped
//     Process / Window result (`Request.Groups`).
//     Excludes COMPOSE-only (slot-label-bound)
//     / CHAIN-only (StageRef-bound) / FACET-only
//     (population-bound) kinds — those address
//     a different request shape and surface on
//     their own facade.
//   - overlayFacadeCompose  — pulse_compose. ComposedRequest.Overlays
//     accepts COMPOSE-only kinds whose Ref +
//     Targets resolve to slot labels in the
//     parent ComposedRequest (`ComposeOverlaySpec`).
//     FORMULA crosses over (in-Request AND
//     Compose surfaces per the kind's catalog
//     row); RANK rides the same slot-label
//     plumbing as the comparison family even
//     though the rank computation reads only
//     the target matrix.
//   - overlayFacadeFacet    — pulse_facet / pulse_facet_schema.
//     FacetRequest.Overlays accepts only the
//     four population-comparison kinds whose
//     Ref.Population resolves to a separate
//     cohort cohort.
//   - overlayFacadeChain    — pulse_process_chain. ChainRequest.Overlays
//     accepts the two whole-chain kinds whose
//     Ref + Target are StageRef values pointing
//     at stages of the parent ChainRequest.
type overlayFacade int

const (
	overlayFacadeRequest overlayFacade = iota
	overlayFacadeCompose
	overlayFacadeFacet
	overlayFacadeChain
)

// composeOnlyOverlayKinds enumerates the overlay kinds that resolve
// their Reference / Targets via ComposeOverlaySpec slot labels rather
// than the in-Request OverlayRef discriminated union. The list is the
// authoritative source the per-facade classifier consults; each entry's
// capability row at descriptor.overlayCapabilityFor carries a matching
// "COMPOSE-only kind" comment so the catalog audit stays grep-clean.
// Sorted alphabetically by constant so the slice itself is golden
// across edits — but the per-facade enum surfaces the names through
// sortedDedupe, so list order is not load-bearing for the enum output.
func composeOnlyOverlayKinds() []string {
	return []string{
		string(types.OverlayKindChiSqVsRef),
		string(types.OverlayKindDeltaVsRef),
		string(types.OverlayKindIndexVsRef),
		string(types.OverlayKindPanelIndexVsRef),
		string(types.OverlayKindPropZCell),
		string(types.OverlayKindPropZPanel),
		string(types.OverlayKindRank),
		string(types.OverlayKindTCell),
		string(types.OverlayKindTVsRef),
		string(types.OverlayKindZCell),
		string(types.OverlayKindZVsRef),
	}
}

// chainOnlyOverlayKinds enumerates the overlay kinds whose Ref +
// Target are StageRef values pointing at stages of a ChainRequest.
// Mirrors descriptor.processChainCapability().OverlayKinds — single
// source of truth for the whole-chain catalog membership.
func chainOnlyOverlayKinds() []string {
	return []string{
		string(types.OverlayKindDeltaVsStage),
		string(types.OverlayKindIndexVsStage),
	}
}

// facetOnlyOverlayKinds enumerates the overlay kinds whose Ref.Population
// resolves to a separate cohort. Mirrors
// descriptor.facetCapability().SupportedOverlayKinds — single source of
// truth for the FACET-host catalog membership.
func facetOnlyOverlayKinds() []string {
	return []string{
		string(types.OverlayKindChiSqVsPop),
		string(types.OverlayKindIndexVsPop),
		string(types.OverlayKindKSVsPop),
		string(types.OverlayKindZScoreVsPop),
	}
}

// overlayKindEnumForFacade returns the per-facade overlay_kind enum
// drawn from descriptor.OverlayCapabilities() filtered by facade
// membership, with embedder-registered kinds (snap.OverlayKinds)
// merged in via the same dedupe + alphabetise convention the operator
// enums use.
//
// Classification rules (per acceptance criteria):
//
//   - Request facade   — every kind NOT in the compose / facet / chain
//     lists. Includes the MATRIX-host crosstab kinds
//     (INDEX_VS_MARGIN, SHARE_OF_ROW/COL/TOTAL,
//     DELTA_VS_MARGIN, ZSCORE_VS_MARGIN, CHISQ_MATRIX,
//     CHISQ_ROW, CHISQ_COL, FISHER_EXACT_CELL) plus
//     the SERIES-host grouped / windowed kinds
//     (INDEX_VS_TOTAL, INDEX_VS_SIBLING,
//     DELTA_VS_SIBLING, ZSCORE_VS_TOTAL, SHARE_OF_TOTAL
//     series arm, INDEX_VS_PRIOR, INDEX_VS_BASELINE,
//     DELTA_VS_BASELINE, INDEX_VS_ROLLING_MEAN,
//     ZSCORE_VS_ROLLING, YOY) and FORMULA (cross-shape).
//   - Compose facade   — the compose-only catalog plus FORMULA. The kind
//     catalog rows note FORMULA's Compose surface
//     directly; it stays on the Request enum too
//     because its in-Request surface also ships.
//   - Facet facade     — exactly the four FACET-host kinds.
//   - Chain facade     — exactly the two CHAIN-host kinds.
//
// Extension-registered kinds (snap.OverlayKinds) appear on the Request
// facade enum today (lowest-risk fallback). The registration surface
// is not yet implemented and the per-kind facade tag does not exist
// yet — once registrations carry a per-kind facade tag, extension
// kinds can be routed through the matching arm. snapshot-less paths
// return the built-in catalog only.
func overlayKindEnumForFacade(facade overlayFacade, snap *descriptor.ExtensionsSnapshot) []string {
	composeSet := stringSetFrom(composeOnlyOverlayKinds())
	chainSet := stringSetFrom(chainOnlyOverlayKinds())
	facetSet := stringSetFrom(facetOnlyOverlayKinds())

	caps := descriptor.OverlayCapabilities()
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		name := string(c.Kind)
		switch facade {
		case overlayFacadeRequest:
			// Request facade carries every kind that is NOT
			// exclusively addressed via a slot-label /
			// stage-index / population-cohort ref family — i.e.
			// every in-Request kind. FORMULA stays here too
			// (Request.Overlays is a documented FORMULA surface).
			if composeSet[name] || chainSet[name] || facetSet[name] {
				continue
			}
		case overlayFacadeCompose:
			// Compose facade carries the compose-only catalog
			// plus FORMULA (cross-facade surface per the kind's
			// catalog row).
			if !(composeSet[name] || c.Kind == types.OverlayKindFormula) {
				continue
			}
		case overlayFacadeFacet:
			if !facetSet[name] {
				continue
			}
		case overlayFacadeChain:
			if !chainSet[name] {
				continue
			}
		}
		out = append(out, name)
	}
	// Extension-registered kinds — today every extension kind lands on
	// the Request-facade enum (the lowest-risk fallback while the
	// registration surface evolves). Routing by a per-kind facade tag
	// becomes possible once registrations carry one.
	if facade == overlayFacadeRequest {
		out = append(out, extensionNames(snap, "overlay")...)
	}
	return sortedDedupe(out)
}

// stringSetFrom builds a membership set from a slice. Helper kept tight
// because the per-facade classifier reads from three of these tables
// every call.
func stringSetFrom(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[s] = true
	}
	return out
}

// sortedDedupe alphabetises and deduplicates a slice in place-free
// fashion, returning a new slice. Per-facade enums consume this so the
// emitted JSON Schema is byte-stable across calls — the per-call enum
// MUST be deterministic so the schema-bound MCP cache key remains
// stable.
func sortedDedupe(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// overlaysSchemaForFacade returns the JSON Schema fragment describing
// the Overlays array on the named facade's request shape. Each entry
// is an OverlaySpec (Request / Facet) or a ComposeOverlaySpec /
// ChainOverlaySpec (Compose / Chain); the `kind` enum is the per-
// facade catalog returned by overlayKindEnumForFacade. scope and ref
// (or reference / targets / stage) carry the on-wire union
// discriminator one level deeper — the per-kind validation surface
// lives in predict, not in the schema.
//
// Scaffolding only: this surface mirrors the per-facade Spec shape
// but does NOT attempt per-kind correlation in JSON Schema (the same
// limitation noted at the top of this file for operator-type /
// field-type pairs). The enum on `kind` is the load-bearing addition.
func overlaysSchemaForFacade(facade overlayFacade, snap *descriptor.ExtensionsSnapshot) map[string]any {
	kinds := overlayKindEnumForFacade(facade, snap)
	kindField := map[string]any{
		"type":        "string",
		"description": "Overlay catalog kind. Enum is constrained to the kinds the bound facade's request shape accepts (Request / Compose / Facet / Chain). Drawn from descriptor.OverlayCapabilities() filtered by facade membership; new kinds in the same facade flow through automatically. See Manifest.Overlays for the per-kind shape / scope / ref-kind matrix.",
	}
	if len(kinds) > 0 {
		kindField["enum"] = kinds
	}
	switch facade {
	case overlayFacadeCompose:
		return map[string]any{
			"type":        "array",
			"description": "Compose-only overlay layer specifications. Each entry produces one OverlayLayer in ComposedResponse.Overlays in matching order. Reference + Targets resolve against per-Request Label fields (empty Labels auto-default to request_<i+1>). See types.ComposeOverlaySpec.",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":      map[string]any{"type": "string", "description": "Renderer-facing label. Empty triggers a deterministic default keyed by Kind+Reference+Targets."},
					"kind":      kindField,
					"scope":     map[string]any{"type": "string", "description": "Where the overlay lands relative to the target slot's result."},
					"reference": map[string]any{"type": "string", "description": "Baseline slot's per-Request Label (after auto-default substitution)."},
					"targets":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "One-or-more slot labels whose results this overlay decorates."},
					"level":     map[string]any{"type": "integer", "minimum": 0, "description": "Matrix-shape Compose: same-axis prefix depth. Ignored on non-matrix kinds."},
					"within":    map[string]any{"type": "integer", "minimum": 0, "description": "Matrix-shape Compose: opposite-axis prefix depth. Ignored on non-matrix kinds."},
					"params":    map[string]any{"description": "Kind-specific configuration. Per-kind schema lives alongside the kind's processor."},
					"options":   map[string]any{"type": "object", "description": "Per-spec optimization knobs (e.g. MaxPanelTargets for multi-reference kinds)."},
				},
				"required":             []string{"kind", "reference"},
				"additionalProperties": true,
			},
		}
	case overlayFacadeChain:
		stageRef := map[string]any{
			"type":        "object",
			"description": "Discriminated stage reference — populate exactly one of index / name.",
			"properties": map[string]any{
				"index": map[string]any{"type": "integer", "minimum": 0, "description": "Zero-based stage index into ChainRequest.Stages."},
				"name":  map[string]any{"type": "string", "description": "Matches against ChainStage.Name verbatim."},
			},
			"additionalProperties": false,
		}
		return map[string]any{
			"type":        "array",
			"description": "Whole-chain overlay layer specifications. Each entry produces one OverlayLayer in ChainResponse.Overlays in matching order. Ref + Target are StageRef values pointing at stages of the parent ChainRequest. See types.ChainOverlaySpec.",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string", "description": "Renderer-facing label. Empty triggers a deterministic default keyed by Kind+Ref+Target."},
					"kind":   kindField,
					"scope":  map[string]any{"type": "string", "description": "Where the overlay lands relative to the target stage's result. Whole-chain kinds use scope=total."},
					"ref":    stageRef,
					"target": stageRef,
					"params": map[string]any{"description": "Kind-specific configuration."},
				},
				"required":             []string{"kind", "ref", "target"},
				"additionalProperties": true,
			},
		}
	default:
		// Request + Facet facades reuse the canonical OverlaySpec
		// shape (types/overlay.go) — Ref is the discriminated union
		// pointer family rather than a slot label.
		return map[string]any{
			"type":        "array",
			"description": "Overlay layer specifications. Each entry produces one OverlayLayer in the response's matching Overlays slot. See types.OverlaySpec.",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string", "description": "Renderer-facing label. Empty triggers a deterministic default keyed by Kind+Scope+Ref."},
					"kind":   kindField,
					"scope":  map[string]any{"type": "string", "description": "Where the overlay lands relative to the base result."},
					"ref":    map[string]any{"type": "object", "description": "Discriminated reference family pointer; per-kind contract documented in Manifest.Overlays."},
					"level":  map[string]any{"type": "integer", "minimum": 0, "description": "Same-axis prefix depth. Honoured by the share / index / delta / zscore family; non-zero rejected by implicit-margin kinds."},
					"within": map[string]any{"type": "integer", "minimum": 0, "description": "Opposite-axis prefix depth. Honoured by the share / index / delta / zscore family; non-zero rejected by implicit-margin kinds."},
					"params": map[string]any{"description": "Operator-specific configuration. Per-kind schema lives alongside the kind's processor."},
				},
				"required":             []string{"kind", "scope"},
				"additionalProperties": true,
			},
		}
	}
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
	// inner is the structured Request schema; nest it under requests[].
	var reqSchema map[string]any
	if err := json.Unmarshal(inner, &reqSchema); err != nil {
		return nil, err
	}

	// Structured contract: ComposedRequest fields (requests, overlays) at the
	// top level — no {request: ...} wrapper.
	outer := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"requests": map[string]any{
				"type":  "array",
				"items": reqSchema,
			},
			// ComposedRequest.Overlays carries the Compose-only catalog
			// (slot-label-bound kinds + FORMULA). The per-Request inherited
			// Overlays slot lives inside `requests[].overlays` via the
			// inlined request schema and stays Request-facade-scoped.
			"overlays": overlaysSchemaForFacade(overlayFacadeCompose, snap),
		},
		"required":             []string{"requests"},
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

// buildLabelsSchema returns the JSON Schema fragment for a Request /
// FacetRequest Labels slot, constrained to the cohort's categorical
// fields and the snapshot's registered label tables. Returns nil
// when there are no categorical fields OR no registered tables — in
// either case the slot is non-functional and omitting it from the
// schema is more honest than advertising an empty enum.
func buildLabelsSchema(c fieldClassification, snap *descriptor.ExtensionsSnapshot) map[string]any {
	if len(c.Categorical) == 0 {
		return nil
	}
	tables := labelTableNames(snap)
	if len(tables) == 0 {
		return nil
	}
	field := map[string]any{
		"type":        "string",
		"description": "Categorical field whose values will be translated to labels.",
		"enum":        c.Categorical,
	}
	table := map[string]any{
		"type":        "string",
		"description": "Registered label-table name (Extensions.LabelTables or PULSE_LABEL_TABLES_DIR).",
		"enum":        tables,
	}
	binding := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"field": field,
			"table": table,
			"mode": map[string]any{
				"type":        "string",
				"description": "replace: rewrite the value with the label. augment: emit a sibling \"<field>_label\" column.",
				"enum":        []string{"replace", "augment"},
			},
		},
		"required":             []string{"field", "table"},
		"additionalProperties": false,
	}
	return map[string]any{
		"type":        "array",
		"description": "Label bindings translate categorical values to display strings at output time. See types.LabelBinding.",
		"items":       binding,
	}
}

// labelTableNames extracts table names from the snapshot in sorted
// order. Returns nil when the snapshot is empty so callers can skip
// adding a useless empty enum.
func labelTableNames(snap *descriptor.ExtensionsSnapshot) []string {
	if snap == nil || len(snap.LabelTables) == 0 {
		return nil
	}
	out := make([]string, 0, len(snap.LabelTables))
	for _, t := range snap.LabelTables {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
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

// BindSessionTools is the entry point used by bindSessionFromPath (the
// inspect/import bind hook). Given a
// schema, it derives per-tool JSON Schemas and re-registers the action
// tools on the server by name with the enum-constrained variants. Over
// stdio the server has a single session, so a same-name Server.AddTool
// replaces the base tool for this session and go-sdk auto-emits
// notifications/tools/list_changed.
func BindSessionTools(s *mcpsdk.Server, schema *encoding.Schema, handlers boundHandlers) error {
	return BindSessionToolsWithExtensions(s, schema, nil, handlers)
}

// BindSessionToolsWithExtensions mirrors BindSessionTools but routes
// an extensions snapshot into the per-tool JSON Schemas so
// embedder-registered operator names appear in the enum lists.
//
// Mechanism (go-sdk): Server.AddTool with an existing tool name replaces
// the prior registration in the server's single global tool set and fires
// notifications/tools/list_changed to the connected client. There is no
// per-session tool map in go-sdk; that is correct for stdio, where one
// Server serves exactly one session. RemoveTools is unnecessary here — the
// same-name AddTool swap fully replaces the base tool's schema.
func BindSessionToolsWithExtensions(s *mcpsdk.Server, schema *encoding.Schema, snap *descriptor.ExtensionsSnapshot, handlers boundHandlers) error {
	if s == nil || schema == nil {
		return nil
	}
	schemas, err := BindWithExtensions(schema, snap)
	if err != nil {
		return err
	}
	for _, entry := range []struct {
		name        string
		description string
		handler     mcpsdk.ToolHandler
	}{
		{toolmeta.ToolProcess, toolmeta.DescProcess + " (schema-bound)", handlers.process},
		{toolmeta.ToolPredict, toolmeta.DescPredict + " (schema-bound)", handlers.predict},
		{toolmeta.ToolCompose, toolmeta.DescCompose + " (schema-bound)", handlers.compose},
		{toolmeta.ToolSample, toolmeta.DescSample + " (schema-bound)", handlers.sample},
		{toolmeta.ToolFacet, toolmeta.DescFacet + " (schema-bound)", handlers.facet},
		{toolmeta.ToolFacetSchema, toolmeta.DescFacetSchema + " (schema-bound)", handlers.facetSchema},
		{toolmeta.ToolProcessChain, toolmeta.DescProcessChain + " (schema-bound)", handlers.processChain},
	} {
		raw, ok := schemas[entry.name]
		if !ok || entry.handler == nil {
			continue
		}
		s.AddTool(&mcpsdk.Tool{
			Name:        entry.name,
			Description: entry.description,
			InputSchema: raw,
		}, entry.handler)
	}
	return nil
}

// boundHandlers carries the per-tool handler closures so BindSessionTools
// can attach them to the bound variants. We reuse the existing global
// handlers verbatim — the wire-shape stays identical; only the input
// schema changes.
type boundHandlers struct {
	process      mcpsdk.ToolHandler
	predict      mcpsdk.ToolHandler
	compose      mcpsdk.ToolHandler
	sample       mcpsdk.ToolHandler
	facet        mcpsdk.ToolHandler
	facetSchema  mcpsdk.ToolHandler
	processChain mcpsdk.ToolHandler
}

// boundHandlersFor constructs the per-tool handler set for the bound
// variants from the live Pulse facade. The handlers are byte-identical to
// the globally registered ones (they wrap the same core descriptor Invoke) —
// only the advertised input schema changes on re-registration. The bound
// action tools never themselves trigger the bind hook, so a nil server and
// bindOnOpen=false are passed to coreHandler.
func boundHandlersFor(p *pulse.Pulse) boundHandlers {
	mk := func(name string) mcpsdk.ToolHandler {
		d, ok := coreDescriptor(name)
		if !ok {
			return nil
		}
		return coreHandler(nil, p, false, d)
	}
	return boundHandlers{
		process:      mk(toolmeta.ToolProcess),
		predict:      mk(toolmeta.ToolPredict),
		compose:      mk(toolmeta.ToolCompose),
		sample:       mk(toolmeta.ToolSample),
		facet:        mk(toolmeta.ToolFacet),
		facetSchema:  mk(toolmeta.ToolFacetSchema),
		processChain: mk(toolmeta.ToolProcessChain),
	}
}
