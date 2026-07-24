package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/mcp/toolmeta"
	"github.com/frankbardon/pulse/types"
)

// TestHandleLookup_Hit builds a sidecar index over the shared import
// fixture and confirms HandleLookup delegates to Pulse.Lookup and returns
// the matched row.
func TestHandleLookup_Hit(t *testing.T) {
	p, _ := newImportTestPulse(t)
	ctx := context.Background()

	if _, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if _, err := p.BuildIndex(ctx, "imports/data.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	out, err := HandleLookup(ctx, p, LookupIn{
		Cohort: &types.Cohort{Filename: "imports/data.pulse"},
		Field:  "id",
		Value:  "2",
	})
	if err != nil {
		t.Fatalf("HandleLookup: %v", err)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("Rows = %d, want 1: %+v", len(out.Rows), out.Rows)
	}
	if name, ok := out.Rows[0]["name"]; !ok || name != "Bob" {
		t.Errorf("row = %+v, want name=Bob", out.Rows[0])
	}
}

// TestHandleLookup_CodedErrorVerbatim asserts a lookup against a cohort
// with no sidecar index surfaces PULSE_INDEX_MISSING verbatim, not
// flattened to a plain error.
func TestHandleLookup_CodedErrorVerbatim(t *testing.T) {
	p, _ := newImportTestPulse(t)
	ctx := context.Background()

	if _, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}

	_, err := HandleLookup(ctx, p, LookupIn{
		Cohort: &types.Cohort{Filename: "imports/data.pulse"},
		Field:  "id",
		Value:  "2",
	})
	if err == nil {
		t.Fatal("expected error for missing sidecar index")
	}
	if !perr.HasCode(err, perr.PULSE_INDEX_MISSING) {
		t.Errorf("expected PULSE_INDEX_MISSING, got: %v", err)
	}
}

// TestTools_LookupRegistered confirms pulse_lookup appears in the type-
// erased catalog with schemas and a working Invoke round-trip.
func TestTools_LookupRegistered(t *testing.T) {
	p, _ := newImportTestPulse(t)
	ctx := context.Background()

	if _, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if _, err := p.BuildIndex(ctx, "imports/data.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	var d ToolDescriptor
	for _, td := range Tools(Config{Version: "test"}) {
		if td.Name == toolmeta.ToolLookup {
			d = td
		}
	}
	if d.Invoke == nil {
		t.Fatal("pulse_lookup descriptor not found")
	}
	if len(d.InputSchema) == 0 || len(d.OutputSchema) == 0 {
		t.Fatal("pulse_lookup missing reflected schema")
	}

	raw, _ := json.Marshal(LookupIn{
		Cohort: &types.Cohort{Filename: "imports/data.pulse"},
		Field:  "id",
		Value:  "1",
	})
	res, err := d.Invoke(ctx, p, raw)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	out, ok := res.(LookupOut)
	if !ok {
		t.Fatalf("Invoke returned %T, want LookupOut", res)
	}
	if len(out.Rows) != 1 {
		t.Errorf("Rows = %d, want 1", len(out.Rows))
	}
}
