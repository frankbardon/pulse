package mcp

// bind.go is the SDK-free half of schema-bind-on-inspect: given a cohort
// schema (and an optional embedder extensions snapshot) it derives the
// per-tool JSON Schemas whose field-name / operator-type enums are
// constrained to that cohort. The result is a name→json.RawMessage map the
// go-sdk adapter (mcp/gosdk) feeds into a per-session Server.AddTool swap.
//
// Everything here is pure: it imports only the Pulse domain packages
// (encoding, descriptor, types) plus the leaf toolmeta. No MCP SDK — the
// import firewall (firewall_test.go) keeps it that way. The per-session
// server mutation that consumes these schemas lives in the adapter, not here.
//
// JSON Schema limitations note: per-element correlation between an
// operator-type enum and the field-name enum (e.g. "AGG_SUM permitted only
// for numeric fields") is hard to express across array elements. The v1 cut
// applies enums on field names and on the operator-type catalogues, and
// relies on Type-level descriptions to convey operator compatibility. Strict
// correlation enforcement remains the job of predict.

import (
	"encoding/json"
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
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

// Bind returns per-tool JSON Schemas keyed by tool name with no embedder
// extensions applied. Equivalent to BindWithExtensions with a nil snapshot.
func Bind(schema *encoding.Schema) (map[string]json.RawMessage, error) {
	return BindWithExtensions(schema, nil)
}

// BindWithExtensions returns per-tool JSON Schemas keyed by tool name,
// merging any embedder-registered operator names into the per-category
// enums so LLM agents can author requests that reference custom operators.
// Empty schemas are omitted so the caller can decide which tools to
// override. SDK-free: the go-sdk adapter consumes the returned map.
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
// tool with the per-cohort enum constraints applied to every stage's inner
// Request shape.
func buildProcessChainSchemaWithExtensions(c fieldClassification, snap *descriptor.ExtensionsSnapshot) (json.RawMessage, error) {
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
			"overlays": overlaysSchemaForFacade(overlayFacadeChain, snap),
		},
		"required":             []string{"stages"},
		"additionalProperties": true,
	}

	return json.Marshal(requestObject)
}

// buildFacetSchemaRequestSchema describes the pulse_facet_schema tool with
// field enums constrained to the bound cohort.
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
			"overlays": overlaysSchemaForFacade(overlayFacadeFacet, snap),
		},
		"required":             []string{"fields"},
		"additionalProperties": true,
	}
	if labels := buildLabelsSchema(c, snap); labels != nil {
		requestObject["properties"].(map[string]any)["labels"] = labels
	}
	return json.Marshal(requestObject)
}

// extensionNames returns the operator-name slice for a category from the
// snapshot, or nil when no snapshot is registered.
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
// snapshot, sorting the combined list and removing duplicates.
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

// orderKeySchema is the shared schema for OrderKey entries used by windows
// and tier-1/tier-2 tests.
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
// types.Request with per-field enums for the bound cohort.
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

	return json.Marshal(requestObject)
}

// crosstabSchema returns the JSON Schema for the Crosstab section.
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

// overlayFacade identifies which MCP tool surface a per-facade overlay_kind
// enum belongs to.
type overlayFacade int

const (
	overlayFacadeRequest overlayFacade = iota
	overlayFacadeCompose
	overlayFacadeFacet
	overlayFacadeChain
)

// composeOnlyOverlayKinds enumerates the overlay kinds that resolve their
// Reference / Targets via ComposeOverlaySpec slot labels.
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

// chainOnlyOverlayKinds enumerates the overlay kinds whose Ref + Target are
// StageRef values pointing at stages of a ChainRequest.
func chainOnlyOverlayKinds() []string {
	return []string{
		string(types.OverlayKindDeltaVsStage),
		string(types.OverlayKindIndexVsStage),
	}
}

// facetOnlyOverlayKinds enumerates the overlay kinds whose Ref.Population
// resolves to a separate cohort.
func facetOnlyOverlayKinds() []string {
	return []string{
		string(types.OverlayKindChiSqVsPop),
		string(types.OverlayKindIndexVsPop),
		string(types.OverlayKindKSVsPop),
		string(types.OverlayKindZScoreVsPop),
	}
}

// overlayKindEnumForFacade returns the per-facade overlay_kind enum drawn
// from descriptor.OverlayCapabilities() filtered by facade membership, with
// embedder-registered kinds merged in.
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
			if composeSet[name] || chainSet[name] || facetSet[name] {
				continue
			}
		case overlayFacadeCompose:
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
	if facade == overlayFacadeRequest {
		out = append(out, extensionNames(snap, "overlay")...)
	}
	return sortedDedupe(out)
}

// stringSetFrom builds a membership set from a slice.
func stringSetFrom(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[s] = true
	}
	return out
}

// sortedDedupe alphabetises and deduplicates a slice, returning a new slice.
// Per-facade enums consume this so the emitted JSON Schema is byte-stable.
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

// overlaysSchemaForFacade returns the JSON Schema fragment describing the
// Overlays array on the named facade's request shape.
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

// testsArraySchema returns the JSON Schema for a tests/post_tests array with
// field references constrained by classification.
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

// buildComposeSchemaWithExtensions describes a ComposedRequest by wrapping
// the bound Request schema in a requests array.
func buildComposeSchemaWithExtensions(c fieldClassification, snap *descriptor.ExtensionsSnapshot) (json.RawMessage, error) {
	inner, err := buildRequestSchemaWithExtensions(c, snap)
	if err != nil {
		return nil, err
	}
	var reqSchema map[string]any
	if err := json.Unmarshal(inner, &reqSchema); err != nil {
		return nil, err
	}

	outer := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"requests": map[string]any{
				"type":  "array",
				"items": reqSchema,
			},
			"overlays": overlaysSchemaForFacade(overlayFacadeCompose, snap),
		},
		"required":             []string{"requests"},
		"additionalProperties": true,
	}
	return json.Marshal(outer)
}

// buildSampleSchema describes the pulse_sample tool params.
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
// FacetRequest Labels slot.
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

// labelTableNames extracts table names from the snapshot in sorted order.
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

// enumStringField returns a property schema for a string with an enum drawn
// from values. When values is empty the enum is omitted.
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
