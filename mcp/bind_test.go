package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/mcp/toolmeta"
)

// makeBindSchema constructs a schema with one numeric, one categorical, and a
// date field so the bound JSON Schema builders exercise every primary
// classification slot (numeric vs categorical vs date).
func makeBindSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	dict.Add("alpha")
	dict.Add("beta")
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64},
			{Name: "category", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
			{Name: "observed_on", Type: encoding.FieldTypeDate},
		},
	}
}

// decodeBoundRequest unmarshals a bound tool's JSON Schema body. Under the
// canonical structured contract the body IS the typed request schema at the
// top level — there is no {request: ...} wrapper.
func decodeBoundRequest(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var req map[string]any
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal request schema: %v", err)
	}
	return req
}

// enumOf navigates request.<arrayKey>.items.properties.<innerKey>.enum and
// returns the enum as a string slice. A missing path returns nil.
func enumOf(req map[string]any, arrayKey, innerKey string) []string {
	props, _ := req["properties"].(map[string]any)
	if props == nil {
		return nil
	}
	a, _ := props[arrayKey].(map[string]any)
	if a == nil {
		return nil
	}
	items, _ := a["items"].(map[string]any)
	if items == nil {
		return nil
	}
	ps, _ := items["properties"].(map[string]any)
	if ps == nil {
		return nil
	}
	field, _ := ps[innerKey].(map[string]any)
	if field == nil {
		return nil
	}
	enum, _ := field["enum"].([]any)
	if enum == nil {
		return nil
	}
	out := make([]string, 0, len(enum))
	for _, v := range enum {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// TestMCPSchemaBinding_RemovesInvalidFields verifies attributes[].field is
// constrained to numeric fields only (categorical "category" must not appear),
// while the v1 aggregation cut applies the all-fields union but still carries
// the numeric "score".
func TestMCPSchemaBinding_RemovesInvalidFields(t *testing.T) {
	schemas, err := Bind(makeBindSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	req := decodeBoundRequest(t, schemas[toolmeta.ToolProcess])

	attrFields := enumOf(req, "attributes", "field")
	if !slices.Contains(attrFields, "score") {
		t.Errorf("attributes.field enum missing numeric 'score': %v", attrFields)
	}
	if slices.Contains(attrFields, "category") {
		t.Errorf("attributes.field enum must not contain categorical 'category': %v", attrFields)
	}

	aggFields := enumOf(req, "aggregations", "field")
	if !slices.Contains(aggFields, "score") {
		t.Errorf("aggregations.field enum missing 'score': %v", aggFields)
	}
}

// TestMCPSchemaBinding_AllFieldsInFiltererEnum verifies the filterer field
// enum is the full set of cohort field names.
func TestMCPSchemaBinding_AllFieldsInFiltererEnum(t *testing.T) {
	schemas, err := Bind(makeBindSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	req := decodeBoundRequest(t, schemas[toolmeta.ToolProcess])

	filterFields := enumOf(req, "filterers", "field")
	want := []string{"score", "category", "observed_on"}
	for _, w := range want {
		if !slices.Contains(filterFields, w) {
			t.Errorf("filterer field enum missing %q: %v", w, filterFields)
		}
	}
	if len(filterFields) != len(want) {
		t.Errorf("filterer field enum size = %d, want %d", len(filterFields), len(want))
	}
}

// TestMCPSchemaBinding_SampleAndFacetFieldEnum verifies the standalone
// sample/facet bound schemas: sample has no field arg; facet's field carries
// an enum of all field names.
func TestMCPSchemaBinding_SampleAndFacetFieldEnum(t *testing.T) {
	schemas, err := Bind(makeBindSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	facetRaw, ok := schemas[toolmeta.ToolFacet]
	if !ok {
		t.Fatal("missing facet schema")
	}
	var facetSchema map[string]any
	if err := json.Unmarshal(facetRaw, &facetSchema); err != nil {
		t.Fatalf("unmarshal facet: %v", err)
	}
	fprops, _ := facetSchema["properties"].(map[string]any)
	field, _ := fprops["field"].(map[string]any)
	enum, _ := field["enum"].([]any)
	if len(enum) != 3 {
		t.Errorf("facet field enum size = %d, want 3", len(enum))
	}

	sampleRaw, ok := schemas[toolmeta.ToolSample]
	if !ok {
		t.Fatal("missing sample schema")
	}
	var sampleSchema map[string]any
	if err := json.Unmarshal(sampleRaw, &sampleSchema); err != nil {
		t.Fatalf("unmarshal sample: %v", err)
	}
	sprops, _ := sampleSchema["properties"].(map[string]any)
	if _, has := sprops["path"]; !has {
		t.Error("sample schema missing 'path' property")
	}
}

// crosstabIntProperty fetches request.crosstab.properties.<key> as a map and
// asserts its type:integer + minimum:0 + non-empty description constraints.
func crosstabIntProperty(t *testing.T, raw json.RawMessage, key string) {
	t.Helper()
	req := decodeBoundRequest(t, raw)
	props, _ := req["properties"].(map[string]any)
	cross, _ := props["crosstab"].(map[string]any)
	if cross == nil {
		t.Fatal("crosstab schema missing from request")
	}
	cprops, _ := cross["properties"].(map[string]any)
	p, _ := cprops[key].(map[string]any)
	if p == nil {
		t.Fatalf("crosstab.%s property missing", key)
	}
	if typ, _ := p["type"].(string); typ != "integer" {
		t.Errorf("%s.type = %q, want integer", key, typ)
	}
	mn, ok := p["minimum"]
	if !ok {
		t.Errorf("%s.minimum missing", key)
	} else if v, _ := mn.(int); v != 0 {
		if f, _ := mn.(float64); f != 0 {
			t.Errorf("%s.minimum = %v, want 0", key, mn)
		}
	}
	if desc, _ := p["description"].(string); desc == "" {
		t.Errorf("%s.description should be non-empty", key)
	}
}

// TestMCPSchemaBinding_CrosstabNormalizeLevel verifies the bound process schema
// exposes Crosstab.normalize_level with the documented integer constraint.
func TestMCPSchemaBinding_CrosstabNormalizeLevel(t *testing.T) {
	schemas, err := Bind(makeBindSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	crosstabIntProperty(t, schemas[toolmeta.ToolProcess], "normalize_level")
}

// TestMCPSchemaBinding_CrosstabNormalizeWithin verifies the bound process
// schema exposes Crosstab.normalize_within with the documented integer
// constraint.
func TestMCPSchemaBinding_CrosstabNormalizeWithin(t *testing.T) {
	schemas, err := Bind(makeBindSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	crosstabIntProperty(t, schemas[toolmeta.ToolProcess], "normalize_within")
}

// overlayKindEnum extracts request.overlays.items.properties.kind.enum.
func overlayKindEnum(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	req := decodeBoundRequest(t, raw)
	props, _ := req["properties"].(map[string]any)
	overlays, _ := props["overlays"].(map[string]any)
	if overlays == nil {
		t.Fatal("request.overlays property missing")
	}
	if typ, _ := overlays["type"].(string); typ != "array" {
		t.Errorf("overlays.type = %q, want array", typ)
	}
	items, _ := overlays["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	kind, _ := itemProps["kind"].(map[string]any)
	if kind == nil {
		t.Fatal("overlays.items.properties.kind missing")
	}
	enumAny, ok := kind["enum"].([]any)
	if !ok {
		t.Fatal("overlays.items.properties.kind.enum missing or wrong shape")
	}
	got := make([]string, 0, len(enumAny))
	for _, v := range enumAny {
		s, _ := v.(string)
		got = append(got, s)
	}
	return got
}

// TestMCPSchemaBinding_OverlayKindEnum verifies the Request-facade overlay_kind
// enum is the per-facade narrowed set: Request-only kinds present, Compose- /
// Chain- / Facet-only kinds excluded.
func TestMCPSchemaBinding_OverlayKindEnum(t *testing.T) {
	schemas, err := Bind(makeBindSchema())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	got := overlayKindEnum(t, schemas[toolmeta.ToolProcess])

	want := overlayKindEnumForFacade(overlayFacadeRequest, nil)
	if !slices.Equal(got, want) {
		t.Errorf("overlay_kind enum = %v, want %v", got, want)
	}
	if !slices.Contains(got, "OVERLAY_INDEX_VS_MARGIN") {
		t.Errorf("overlay_kind enum missing OVERLAY_INDEX_VS_MARGIN: %v", got)
	}
	for _, leak := range []string{
		"OVERLAY_INDEX_VS_POP",       // FACET-only
		"OVERLAY_INDEX_VS_STAGE",     // CHAIN-only
		"OVERLAY_INDEX_VS_REF",       // COMPOSE-only
		"OVERLAY_PROP_Z_PANEL",       // COMPOSE-only multi-ref
		"OVERLAY_PANEL_INDEX_VS_REF", // COMPOSE-only multi-ref
	} {
		if slices.Contains(got, leak) {
			t.Errorf("Request facade overlay_kind enum leaks %q (belongs to another facade): %v", leak, got)
		}
	}
}

// TestMCPSchemaBinding_IncludesCustomAggregator asserts embedder-registered
// operator names merge into the per-category enum returned by the bound tools
// without dropping the built-ins.
func TestMCPSchemaBinding_IncludesCustomAggregator(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	snap := &descriptor.ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{{Name: "AGG_ACME_BRAND_SCORE"}},
	}
	bound, err := BindWithExtensions(schema, snap)
	if err != nil {
		t.Fatalf("BindWithExtensions: %v", err)
	}
	body := string(bound[toolmeta.ToolProcess])
	if !strings.Contains(body, "AGG_ACME_BRAND_SCORE") {
		t.Errorf("bound schema does not include AGG_ACME_BRAND_SCORE:\n%s", body)
	}
	if !strings.Contains(body, "AGG_COUNT") {
		t.Errorf("bound schema dropped built-in AGG_COUNT after extension merge:\n%s", body)
	}
}

// TestMCPSchemaBinding_IncludesCustomOverlayKind asserts an embedder-registered
// overlay kind merges into the Request-facade overlay_kind enum (the per-facade
// merge path) while built-in Request kinds survive.
func TestMCPSchemaBinding_IncludesCustomOverlayKind(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	snap := &descriptor.ExtensionsSnapshot{
		OverlayKinds: []descriptor.OperatorMeta{{Name: "OVERLAY_ACME_HEAT"}},
	}

	// Core enum builder must merge the custom kind onto the Request facade.
	merged := overlayKindEnumForFacade(overlayFacadeRequest, snap)
	if !slices.Contains(merged, "OVERLAY_ACME_HEAT") {
		t.Fatalf("overlayKindEnumForFacade(Request) dropped custom OVERLAY_ACME_HEAT: %v", merged)
	}
	if !slices.Contains(merged, "OVERLAY_INDEX_VS_MARGIN") {
		t.Errorf("custom-overlay merge dropped built-in OVERLAY_INDEX_VS_MARGIN: %v", merged)
	}

	// And it must surface on the bound pulse_process overlays.kind enum.
	bound, err := BindWithExtensions(schema, snap)
	if err != nil {
		t.Fatalf("BindWithExtensions: %v", err)
	}
	got := overlayKindEnum(t, bound[toolmeta.ToolProcess])
	if !slices.Contains(got, "OVERLAY_ACME_HEAT") {
		t.Errorf("bound pulse_process overlays.kind enum missing custom OVERLAY_ACME_HEAT: %v", got)
	}
}

// TestMCPSchemaBinding_BackwardCompatNoCustomNames asserts the no-snapshot path
// keeps the built-in-only enum.
func TestMCPSchemaBinding_BackwardCompatNoCustomNames(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	bound, err := Bind(schema)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	body := string(bound[toolmeta.ToolProcess])
	if strings.Contains(body, "ACME") {
		t.Error("ACME namespace leaked into built-in-only schema")
	}
	if !strings.Contains(body, "AGG_COUNT") {
		t.Error("built-in AGG_COUNT missing from default schema")
	}
}

// TestMCPSchemaBinding_LabelsSchema asserts the labels slot is injected when the
// cohort has categorical fields AND label tables are registered, with the field
// enum constrained to categoricals and the table enum to registered tables.
func TestMCPSchemaBinding_LabelsSchema(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("US")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
			{Name: "amount", Type: encoding.FieldTypeF64},
		},
	}
	snap := &descriptor.ExtensionsSnapshot{
		LabelTables: []descriptor.LabelTableMeta{{Name: "country_names", HasRowsData: true}},
	}
	bound, err := BindWithExtensions(schema, snap)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bound[toolmeta.ToolProcess])
	if !strings.Contains(body, "\"labels\"") {
		t.Fatalf("expected labels slot in pulse_process schema:\n%s", body)
	}
	if !strings.Contains(body, "country_names") {
		t.Fatalf("expected table enum to include country_names:\n%s", body)
	}
	if !strings.Contains(body, `"replace"`) || !strings.Contains(body, `"augment"`) {
		t.Fatalf("expected mode enum to contain replace + augment:\n%s", body)
	}
}

// TestMCPSchemaBinding_LabelsOmittedWhenNoTables asserts the labels slot is
// omitted when no tables are registered.
func TestMCPSchemaBinding_LabelsOmittedWhenNoTables(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("US")
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "country", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict}},
	}
	bound, err := Bind(schema)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	body := string(bound[toolmeta.ToolProcess])
	if strings.Contains(body, "\"labels\"") {
		t.Fatalf("labels slot should be omitted when no tables registered:\n%s", body)
	}
}

// TestMCPSchemaBinding_DedupAndSort verifies merging an extension snapshot does
// not introduce duplicate names and the final enum is sorted.
func TestMCPSchemaBinding_DedupAndSort(t *testing.T) {
	builtin := []string{"AGG_COUNT", "AGG_SUM"}
	snap := &descriptor.ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{
			{Name: "AGG_COUNT"}, // duplicate of built-in
			{Name: "AGG_ACME_Z"},
			{Name: "AGG_ACME_A"},
		},
	}
	merged := mergeEnumNames(builtin, snap, "aggregator")
	want := []string{"AGG_ACME_A", "AGG_ACME_Z", "AGG_COUNT", "AGG_SUM"}
	if !slices.Equal(merged, want) {
		t.Errorf("merged enum = %v, want %v", merged, want)
	}
}

// TestMCPSchemaBinding_NilSchema asserts Bind tolerates a nil schema (no panic,
// nil map) — the bind hook degrades to the unbound global tools in that case.
func TestMCPSchemaBinding_NilSchema(t *testing.T) {
	bound, err := Bind(nil)
	if err != nil {
		t.Fatalf("Bind(nil): %v", err)
	}
	if bound != nil {
		t.Errorf("Bind(nil) = %v, want nil map", bound)
	}
}
