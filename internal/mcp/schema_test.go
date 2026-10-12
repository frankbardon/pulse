package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// TestSchemasReflectForEveryTool asserts that init-time reflection produced a
// non-empty, valid-JSON input and output schema for every registered tool,
// with no reflection error (and therefore no init panic).
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

// TestSchemasNoRawMessageByteArray: no reflected tool schema describes a
// json.RawMessage field (operator `params`, a recommendation's drafted
// `request`) as the byte array the reflector derives from []byte — the
// wire carries any JSON value there, so the schema must be any-JSON.
func TestSchemasNoRawMessageByteArray(t *testing.T) {
	for _, ts := range Schemas() {
		for side, raw := range map[string]json.RawMessage{"input": ts.InputSchema, "output": ts.OutputSchema} {
			var root any
			if err := json.Unmarshal(raw, &root); err != nil {
				t.Fatalf("%s %s: %v", ts.Name, side, err)
			}
			for _, path := range byteArrayPaths(root, "") {
				t.Errorf("%s %s schema: %s reflects as a byte array, want any-JSON", ts.Name, side, path)
			}
		}
	}

	// Not vacuous: a RawMessage-carrying output reflects the field as
	// the empty schema, which marshals as the boolean schema `true`
	// (any JSON value).
	ts, _ := SchemaFor(toolmeta.ToolRecommend)
	if !bytes.Contains(ts.OutputSchema, []byte(`"request":true`)) {
		t.Errorf("pulse_recommend output: recommendations[].request is not the any-JSON schema")
	}
}

// byteArrayPaths returns the JSON-pointer-ish path of every schema node
// shaped like a reflected []byte: an array whose items are integers
// bounded to [0, 255].
func byteArrayPaths(node any, path string) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		if items, ok := v["items"].(map[string]any); ok &&
			items["type"] == "integer" && items["minimum"] == float64(0) && items["maximum"] == float64(255) {
			out = append(out, path)
		}
		for k, child := range v {
			out = append(out, byteArrayPaths(child, path+"/"+k)...)
		}
	case []any:
		for i, child := range v {
			out = append(out, byteArrayPaths(child, fmt.Sprintf("%s/%d", path, i))...)
		}
	}
	return out
}

// TestSchemas_OverlayPartsNeverNest: every tool output that reaches an
// overlay layer reflects `payload.parts` as a map whose values are the
// OverlayPayload schema without `parts` — the recursive Go type is
// reflected once, non-nesting, instead of failing on the cycle.
func TestSchemas_OverlayPartsNeverNest(t *testing.T) {
	for _, name := range []string{toolmeta.ToolProcess, toolmeta.ToolCompose, toolmeta.ToolProcessChain, toolmeta.ToolFacetSchema} {
		ts, ok := SchemaFor(name)
		if !ok {
			t.Fatalf("%s: no schema", name)
		}
		var doc any
		if err := json.Unmarshal(ts.OutputSchema, &doc); err != nil {
			t.Fatal(err)
		}
		found := 0
		var walk func(v any)
		walk = func(v any) {
			switch n := v.(type) {
			case map[string]any:
				if props, ok := n["properties"].(map[string]any); ok {
					if parts, ok := props["parts"].(map[string]any); ok {
						if _, isPayload := props["shape"]; isPayload {
							found++
							part, _ := parts["additionalProperties"].(map[string]any)
							pprops, _ := part["properties"].(map[string]any)
							if _, ok := pprops["shape"]; !ok {
								t.Errorf("%s: a part is not an OverlayPayload: %v", name, part)
							}
							if _, nested := pprops["parts"]; nested {
								t.Errorf("%s: a part may carry parts", name)
							}
						}
					}
				}
				for _, c := range n {
					walk(c)
				}
			case []any:
				for _, c := range n {
					walk(c)
				}
			}
		}
		walk(doc)
		if found == 0 {
			t.Errorf("%s: no overlay payload parts in the output schema", name)
		}
	}
}
