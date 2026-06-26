package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/imports"
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
	handler := handleImport(nil, p, false)
	out, err := handler(context.Background(), toolCall(t, ToolImport, map[string]any{
		"source": "data.csv",
		"ttl":    "24h",
	}))
	text := handlerText(t, out, err)
	var res imports.Result
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, text)
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
	handler := handleImport(nil, p, false)
	out, err := handler(context.Background(), toolCall(t, ToolImport, map[string]any{}))
	if err != nil {
		t.Fatalf("handleImport: %v", err)
	}
	if !out.IsError {
		t.Errorf("expected IsError=true for missing source, got: %+v", out)
	}
}

func TestImport_MCPHandler_InvalidTTL_ReturnsError(t *testing.T) {
	p, _ := newMCPImportTestPulse(t)
	handler := handleImport(nil, p, false)
	out, err := handler(context.Background(), toolCall(t, ToolImport, map[string]any{
		"source": "data.csv",
		"ttl":    "weekly",
	}))
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
	out, err := handler(context.Background(), toolCall(t, ToolDrop, map[string]any{"handle": "data"}))
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
	out, err := handler(context.Background(), toolCall(t, ToolDrop, map[string]any{"handle": "ghost"}))
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
	out, err := handler(context.Background(), toolCall(t, ToolImportsList, map[string]any{}))
	text := handlerText(t, out, err)
	var entries []imports.Entry
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, text)
	}
	if len(entries) != 1 || entries[0].Handle != "data" {
		t.Errorf("entries = %+v, want one entry for handle 'data'", entries)
	}
}
