//go:build ignore

// TODO(E1-S2/S3/S4): not yet ported to github.com/modelcontextprotocol/go-sdk.
// This file still targets mark3labs/mcp-go and is excluded from the build by
// the constraint above. Handler logic is preserved verbatim; the per-file
// migration stories (tools/strict_decode/import_tools -> E1-S2; resources/
// prompts -> E1-S3; schema_bind -> E1-S4) remove this constraint as they port.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
)

// TestMCPSchemaBinding_IncludesCustomAggregator asserts that
// embedder-registered operator names appear in the JSON Schema
// enum list returned by the schema-bound MCP tools.
func TestMCPSchemaBinding_IncludesCustomAggregator(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	snap := &descriptor.ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{{
			Name: "AGG_ACME_BRAND_SCORE",
		}},
	}
	bound, err := BindWithExtensions(schema, snap)
	if err != nil {
		t.Fatalf("BindWithExtensions: %v", err)
	}
	body, ok := bound["pulse_process"]
	if !ok {
		t.Fatal("pulse_process tool not in bound schemas")
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(string(body), "AGG_ACME_BRAND_SCORE") {
		t.Errorf("bound schema does not include AGG_ACME_BRAND_SCORE:\n%s", string(body))
	}
	if !strings.Contains(string(body), "AGG_COUNT") {
		t.Errorf("bound schema dropped built-in AGG_COUNT after extension merge:\n%s", string(body))
	}
}

// TestMCPSchemaBinding_BackwardCompatBindNoCustomNames asserts the
// no-snapshot path keeps the built-in-only enum (no surprises for
// pure-Pulse CLI users).
func TestMCPSchemaBinding_BackwardCompatBindNoCustomNames(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	bound, err := Bind(schema)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	body := string(bound["pulse_process"])
	if strings.Contains(body, "ACME") {
		t.Error("ACME namespace leaked into built-in-only schema")
	}
	if !strings.Contains(body, "AGG_COUNT") {
		t.Error("built-in AGG_COUNT missing from default schema")
	}
}

// TestMCPSchemaBinding_LabelsSchema asserts the labels slot is
// injected when the cohort has categorical fields AND label tables
// are registered. The field enum is constrained to categoricals; the
// table enum is constrained to registered tables.
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
	body := string(bound["pulse_process"])
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

// TestMCPSchemaBinding_LabelsOmittedWhenNoTables asserts the labels
// slot is omitted when no tables are registered — advertising an
// empty enum would just confuse the LLM.
func TestMCPSchemaBinding_LabelsOmittedWhenNoTables(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("US")
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "country", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict}},
	}
	bound, _ := Bind(schema)
	body := string(bound["pulse_process"])
	if strings.Contains(body, "\"labels\"") {
		t.Fatalf("labels slot should be omitted when no tables registered:\n%s", body)
	}
}

// TestMCPSchemaBinding_DedupAndSort verifies that merging an
// extension snapshot does not introduce duplicate names and the
// final enum is sorted.
func TestMCPSchemaBinding_DedupAndSort(t *testing.T) {
	// First test the helper directly so we exercise the sort + dedup
	// path on a representative input.
	builtin := []string{"AGG_COUNT", "AGG_SUM"}
	snap := &descriptor.ExtensionsSnapshot{
		Aggregators: []descriptor.OperatorMeta{
			{Name: "AGG_COUNT"}, // duplicate of built-in
			{Name: "AGG_ACME_Z"},
			{Name: "AGG_ACME_A"},
		},
	}
	merged := mergeEnumNames(builtin, snap, "aggregator")
	if len(merged) != 4 {
		t.Fatalf("merged enum length = %d, want 4 (deduped); got %v", len(merged), merged)
	}
	want := []string{"AGG_ACME_A", "AGG_ACME_Z", "AGG_COUNT", "AGG_SUM"}
	for i := range want {
		if merged[i] != want[i] {
			t.Errorf("merged[%d] = %q, want %q", i, merged[i], want[i])
		}
	}
}
