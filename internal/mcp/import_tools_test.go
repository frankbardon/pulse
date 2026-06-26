//go:build ignore

// TODO(E1-S2/S3/S4): not yet ported to github.com/modelcontextprotocol/go-sdk.
// This file still targets mark3labs/mcp-go and is excluded from the build by
// the constraint above. Handler logic is preserved verbatim; the per-file
// migration stories (tools/strict_decode/import_tools -> E1-S2; resources/
// prompts -> E1-S3; schema_bind -> E1-S4) remove this constraint as they port.

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/imports"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/afero"
)

func newMCPImportTestPulse(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	afs := afero.NewMemMapFs()
	body := "id,name,amount\n1,Alice,10.5\n2,Bob,20.0\n3,Carol,30.75\n"
	if err := afero.WriteFile(afs, "data.csv", []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, afs
}

func TestImport_MCPHandler_CSV(t *testing.T) {
	p, afs := newMCPImportTestPulse(t)
	handler := handleImport(nil, p, false, boundHandlers{})
	call := mcpgo.CallToolRequest{}
	call.Params.Name = ToolImport
	call.Params.Arguments = map[string]any{
		"source": "data.csv",
		"ttl":    "24h",
	}

	out, err := handler(context.Background(), call)
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	if out.IsError {
		t.Fatalf("handler returned error: %+v", out.Content)
	}
	text, ok := out.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", out.Content[0])
	}
	var res imports.Result
	if err := json.Unmarshal([]byte(text.Text), &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, text.Text)
	}
	if res.Handle != "data" || res.Path != "imports/data.pulse" || !res.Managed {
		t.Errorf("result = %+v, want managed handle 'data' at imports/data.pulse", res)
	}
	if res.TTLSeconds != int64(24*3600) {
		t.Errorf("TTLSeconds = %d, want %d", res.TTLSeconds, 24*3600)
	}
	if ok, _ := afero.Exists(afs, "imports/data.pulse"); !ok {
		t.Errorf("managed .pulse file not created on disk")
	}
}

func TestImport_MCPHandler_MissingSource_ReturnsError(t *testing.T) {
	p, _ := newMCPImportTestPulse(t)
	handler := handleImport(nil, p, false, boundHandlers{})
	call := mcpgo.CallToolRequest{}
	call.Params.Arguments = map[string]any{}

	out, err := handler(context.Background(), call)
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	if !out.IsError {
		t.Errorf("expected IsError=true for missing source, got: %+v", out)
	}
}

func TestImport_MCPHandler_InvalidTTL_ReturnsError(t *testing.T) {
	p, _ := newMCPImportTestPulse(t)
	handler := handleImport(nil, p, false, boundHandlers{})
	call := mcpgo.CallToolRequest{}
	call.Params.Arguments = map[string]any{
		"source": "data.csv",
		"ttl":    "weekly",
	}

	out, err := handler(context.Background(), call)
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	if !out.IsError {
		t.Errorf("expected error for unparseable TTL, got: %+v", out)
	}
}

func TestDrop_MCPHandler_RoundTrip(t *testing.T) {
	p, afs := newMCPImportTestPulse(t)
	// Seed a managed import via the facade.
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	handler := handleDrop(p)
	call := mcpgo.CallToolRequest{}
	call.Params.Arguments = map[string]any{"handle": "data"}

	out, err := handler(context.Background(), call)
	if err != nil {
		t.Fatalf("handleDrop: %v", err)
	}
	if out.IsError {
		t.Fatalf("Drop returned error: %+v", out.Content)
	}
	if ok, _ := afero.Exists(afs, "imports/data.pulse"); ok {
		t.Errorf("managed file still present after Drop")
	}
}

func TestDrop_MCPHandler_UnknownHandle_Errors(t *testing.T) {
	p, _ := newMCPImportTestPulse(t)
	handler := handleDrop(p)
	call := mcpgo.CallToolRequest{}
	call.Params.Arguments = map[string]any{"handle": "ghost"}

	out, err := handler(context.Background(), call)
	if err != nil {
		t.Fatalf("handleDrop: %v", err)
	}
	if !out.IsError {
		t.Errorf("expected IsError=true for unknown handle, got: %+v", out)
	}
}

func TestImportsList_MCPHandler(t *testing.T) {
	p, _ := newMCPImportTestPulse(t)
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	handler := handleImportsList(p)
	call := mcpgo.CallToolRequest{}
	call.Params.Arguments = map[string]any{}

	out, err := handler(context.Background(), call)
	if err != nil {
		t.Fatalf("handleImportsList: %v", err)
	}
	if out.IsError {
		t.Fatalf("list returned error: %+v", out.Content)
	}
	text, ok := out.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", out.Content[0])
	}
	var entries []imports.Entry
	if err := json.Unmarshal([]byte(text.Text), &entries); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, text.Text)
	}
	if len(entries) != 1 || entries[0].Handle != "data" {
		t.Errorf("entries = %+v, want one entry for handle 'data'", entries)
	}
}
