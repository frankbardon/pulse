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
