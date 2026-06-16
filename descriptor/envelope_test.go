package descriptor

import (
	"encoding/json"
	"testing"
)

func TestNewEnvelope(t *testing.T) {
	env := NewEnvelope("test")
	if env.FormatVersion != "1.1" {
		t.Errorf("FormatVersion = %q, want %q", env.FormatVersion, "1.1")
	}
	if env.Data != "test" {
		t.Errorf("Data = %v, want %q", env.Data, "test")
	}
	if len(env.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", env.Errors)
	}
	if len(env.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty", env.Warnings)
	}
}

func TestEnvelope_AddError(t *testing.T) {
	env := NewEnvelope(nil)
	env.AddError("CODE", "msg", map[string]any{"key": "val"})
	if len(env.Errors) != 1 {
		t.Fatalf("Errors count = %d, want 1", len(env.Errors))
	}
	if env.Errors[0].Code != "CODE" {
		t.Errorf("Code = %q, want %q", env.Errors[0].Code, "CODE")
	}
	if env.Errors[0].Details["key"] != "val" {
		t.Error("Details not preserved")
	}
}

func TestEnvelope_AddWarning(t *testing.T) {
	env := NewEnvelope(nil)
	env.AddWarning("WARN", "warning msg", nil)
	if len(env.Warnings) != 1 {
		t.Fatalf("Warnings count = %d, want 1", len(env.Warnings))
	}
	if env.Warnings[0].Code != "WARN" {
		t.Errorf("Code = %q, want %q", env.Warnings[0].Code, "WARN")
	}
}

func TestEnvelope_MarshalJSON(t *testing.T) {
	env := NewEnvelope(map[string]string{"key": "value"})
	env.AddWarning("W1", "test warning", nil)

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !json.Valid(data) {
		t.Fatal("output is not valid JSON")
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if parsed["format_version"] != "1.1" {
		t.Error("format_version missing or wrong")
	}
}

// TestEnvelope_RequestOmittedByDefault verifies the new opt-in echo
// surface does not change the wire shape for existing callers — the
// "request" key must be absent from JSON when Request is nil.
func TestEnvelope_RequestOmittedByDefault(t *testing.T) {
	env := NewEnvelope("data")
	if env.Request != nil {
		t.Fatalf("Request defaulted non-nil: %#v", env.Request)
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, present := parsed["request"]; present {
		t.Errorf("request key present when Request was nil: %s", string(data))
	}
}

// TestEnvelope_RequestPopulatedWhenSet verifies the echo field surfaces
// the value the caller attached via NewEnvelopeWithRequest / WithRequest.
func TestEnvelope_RequestPopulatedWhenSet(t *testing.T) {
	req := map[string]any{"name": "demo"}
	env := NewEnvelopeWithRequest("data", req)
	if env.Request == nil {
		t.Fatal("Request nil after NewEnvelopeWithRequest")
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	got, present := parsed["request"]
	if !present {
		t.Fatalf("request key missing: %s", string(data))
	}
	gotMap, ok := got.(map[string]any)
	if !ok || gotMap["name"] != "demo" {
		t.Errorf("request payload = %#v, want {name: demo}", got)
	}

	// WithRequest fluent form should produce the same surface.
	env2 := NewEnvelope("data").WithRequest(req)
	if env2.Request == nil {
		t.Fatal("WithRequest did not attach request")
	}
}
