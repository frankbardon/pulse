package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]string{"key": "value"}
	if err := writeJSON(&buf, data); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["key"] != "value" {
		t.Errorf("key = %q, want %q", got["key"], "value")
	}
}

func TestWriteEnvelope(t *testing.T) {
	var buf bytes.Buffer
	if err := writeEnvelope(&buf, "test-data"); err != nil {
		t.Fatalf("writeEnvelope: %v", err)
	}
	var env descriptor.Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}
}

func TestWriteErrorEnvelope(t *testing.T) {
	var buf bytes.Buffer
	if err := writeErrorEnvelope(&buf, "TEST_ERR", "something failed"); err != nil {
		t.Fatalf("writeErrorEnvelope: %v", err)
	}
	var env descriptor.Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(env.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(env.Errors))
	}
	if env.Errors[0].Code != "TEST_ERR" {
		t.Errorf("error code = %q, want TEST_ERR", env.Errors[0].Code)
	}
}
