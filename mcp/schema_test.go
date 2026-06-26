package mcp

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/mcp/toolmeta"
)

// TestSchemasReflectForEveryTool asserts that init-time reflection produced a
// non-empty, valid-JSON input and output schema for every one of the 19
// registered tools, with no reflection error (and therefore no init panic).
func TestSchemasReflectForEveryTool(t *testing.T) {
	if len(reflectErrors) != 0 {
		for k, err := range reflectErrors {
			t.Errorf("reflection error for %s: %v", k, err)
		}
	}

	names := toolmeta.Names()
	if got := len(Schemas()); got != len(names) {
		t.Fatalf("Schemas() returned %d descriptors, want %d", got, len(names))
	}

	for _, name := range names {
		ts, ok := SchemaFor(name)
		if !ok {
			t.Errorf("%s: no reflected schema registered", name)
			continue
		}
		if ts.Description == "" {
			t.Errorf("%s: empty description", name)
		}
		assertValidNonEmptyObjectSchema(t, name+" input", ts.InputSchema, true)
		assertValidNonEmptyObjectSchema(t, name+" output", ts.OutputSchema, false)
	}
}

// assertValidNonEmptyObjectSchema verifies raw is non-empty, parses as a JSON
// object, and (for inputs, which the low-level go-sdk AddTool requires to be
// type:object) carries a "type":"object" discriminant.
func assertValidNonEmptyObjectSchema(t *testing.T, label string, raw json.RawMessage, requireObject bool) {
	t.Helper()
	if len(raw) == 0 {
		t.Errorf("%s: empty schema", label)
		return
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Errorf("%s: invalid JSON schema: %v", label, err)
		return
	}
	if len(m) == 0 {
		t.Errorf("%s: schema is an empty object", label)
		return
	}
	if requireObject && m["type"] != "object" {
		t.Errorf("%s: input schema type = %v, want \"object\" (go-sdk AddTool requires it)", label, m["type"])
	}
}

// TestSchemasStableOrder asserts the registration order mirrors toolmeta.Names()
// so documentation and adapter scans stay deterministic.
func TestSchemasStableOrder(t *testing.T) {
	want := toolmeta.Names()
	got := Schemas()
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, got[i].Name, want[i])
		}
	}
}
