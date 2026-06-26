package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/mcp/toolmeta"
	"github.com/frankbardon/pulse/skills"
	"github.com/spf13/afero"
)

// newImportTestPulse builds a Pulse over an in-memory fs seeded with a small
// CSV so the import/drop/imports-list handlers have something to act on.
func newImportTestPulse(t *testing.T) (*pulse.Pulse, afero.Fs) {
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

func TestHandleImport_CSV(t *testing.T) {
	p, afs := newImportTestPulse(t)
	out, err := HandleImport(context.Background(), p, ImportIn{Source: "data.csv", TTL: "24h"})
	if err != nil {
		t.Fatalf("HandleImport: %v", err)
	}
	if out.Handle != "data" || out.Path != "imports/data.pulse" || !out.Managed {
		t.Errorf("result = %+v, want managed handle 'data' at imports/data.pulse", out)
	}
	if out.TTLSeconds != int64(24*3600) {
		t.Errorf("TTLSeconds = %d, want %d", out.TTLSeconds, 24*3600)
	}
	if ok, _ := afero.Exists(afs, "imports/data.pulse"); !ok {
		t.Errorf("managed .pulse file not created on disk")
	}
}

func TestHandleImport_MissingSource(t *testing.T) {
	p, _ := newImportTestPulse(t)
	if _, err := HandleImport(context.Background(), p, ImportIn{}); err == nil {
		t.Fatal("expected error for missing source")
	}
}

func TestHandleImport_InvalidTTL(t *testing.T) {
	p, _ := newImportTestPulse(t)
	if _, err := HandleImport(context.Background(), p, ImportIn{Source: "data.csv", TTL: "weekly"}); err == nil {
		t.Fatal("expected error for unparseable TTL")
	}
}

func TestHandleDrop_RoundTrip(t *testing.T) {
	p, afs := newImportTestPulse(t)
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	out, err := HandleDrop(context.Background(), p, DropIn{Handle: "data"})
	if err != nil {
		t.Fatalf("HandleDrop: %v", err)
	}
	if !out.Dropped || out.Handle != "data" {
		t.Errorf("DropOut = %+v, want {data,true}", out)
	}
	if ok, _ := afero.Exists(afs, "imports/data.pulse"); ok {
		t.Errorf("managed file still present after Drop")
	}
}

func TestHandleDrop_UnknownHandle(t *testing.T) {
	p, _ := newImportTestPulse(t)
	if _, err := HandleDrop(context.Background(), p, DropIn{Handle: "ghost"}); err == nil {
		t.Fatal("expected error for unknown handle")
	}
}

func TestHandleImportsList(t *testing.T) {
	p, _ := newImportTestPulse(t)
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	out, err := HandleImportsList(context.Background(), p, ImportsListIn{})
	if err != nil {
		t.Fatalf("HandleImportsList: %v", err)
	}
	if len(out.Imports) != 1 || out.Imports[0].Handle != "data" {
		t.Errorf("imports = %+v, want one entry for handle 'data'", out.Imports)
	}
}

func TestHandleSkillsList(t *testing.T) {
	out, err := HandleSkillsList(context.Background(), nil, SkillsListIn{})
	if err != nil {
		t.Fatalf("HandleSkillsList: %v", err)
	}
	if len(out.Skills) == 0 {
		t.Fatal("HandleSkillsList returned 0 entries")
	}
	var saw bool
	for _, m := range out.Skills {
		if m.Name == "response-components" {
			saw = true
		}
		if m.Name == "" || m.Description == "" {
			t.Errorf("skill entry missing name/description: %+v", m)
		}
	}
	if !saw {
		t.Error("response-components skill missing from list")
	}
}

func TestHandleSkillsGet_BodyIsMarkdown(t *testing.T) {
	list := skills.List()
	if len(list) == 0 {
		t.Fatal("skills.List() returned 0 entries")
	}
	for _, meta := range list {
		out, err := HandleSkillsGet(context.Background(), nil, SkillsGetIn{Name: meta.Name})
		if err != nil {
			t.Fatalf("HandleSkillsGet(%q): %v", meta.Name, err)
		}
		if !strings.HasPrefix(out.Body, "---\n") {
			t.Errorf("skill %q body does not start with YAML frontmatter fence", meta.Name)
		}
	}
}

func TestHandleSkillsGet_Missing(t *testing.T) {
	for _, name := range []string{"", "definitely-not-a-real-skill"} {
		if _, err := HandleSkillsGet(context.Background(), nil, SkillsGetIn{Name: name}); err == nil {
			t.Errorf("expected error for skill name %q", name)
		}
	}
}

// TestHandleErrorsLookup_RoundTrip drives the lookup handler with each of the
// three axes (code / domain / query) plus intersection paths.
func TestHandleErrorsLookup_RoundTrip(t *testing.T) {
	p, _ := newImportTestPulse(t)
	call := func(in ErrorsLookupIn) []perr.LookupResult {
		t.Helper()
		out, err := HandleErrorsLookup(context.Background(), p, in)
		if err != nil {
			t.Fatalf("HandleErrorsLookup(%+v): %v", in, err)
		}
		return out.Results
	}

	hit := call(ErrorsLookupIn{Code: string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL)})
	if len(hit) != 1 || hit[0].Domain != "PULSE" || hit[0].Message == "" {
		t.Fatalf("code hit = %+v, want 1 PULSE result with message", hit)
	}
	if miss := call(ErrorsLookupIn{Code: "NOT_A_REAL_CODE"}); len(miss) != 0 {
		t.Errorf("miss returned %d, want 0", len(miss))
	}
	dom := call(ErrorsLookupIn{Domain: "PULSE"})
	if len(dom) == 0 {
		t.Error("domain=PULSE returned 0")
	}
	if q := call(ErrorsLookupIn{Query: "numeric aggregation"}); len(q) == 0 {
		t.Error("query returned 0 results")
	}
	both := call(ErrorsLookupIn{Code: string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL), Domain: "PULSE"})
	if len(both) != 1 {
		t.Errorf("code+domain intersection = %d, want 1", len(both))
	}
	none := call(ErrorsLookupIn{Code: string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL), Domain: "CLI"})
	if len(none) != 0 {
		t.Errorf("disjoint intersection = %d, want 0", len(none))
	}

	// All-empty axes is a validation error.
	if _, err := HandleErrorsLookup(context.Background(), p, ErrorsLookupIn{}); err == nil {
		t.Error("expected error for all-empty axes")
	}
}

// TestTools_CatalogComplete asserts Tools(cfg) yields one descriptor per
// registered tool, each carrying schemas and a non-nil Invoke.
func TestTools_CatalogComplete(t *testing.T) {
	tools := Tools(Config{Version: "test"})
	names := toolmeta.Names()
	if len(tools) != len(names) {
		t.Fatalf("Tools returned %d, want %d", len(tools), len(names))
	}
	for i, d := range tools {
		if d.Name != names[i] {
			t.Errorf("tools[%d].Name = %q, want %q", i, d.Name, names[i])
		}
		if d.Description == "" {
			t.Errorf("%s: empty description", d.Name)
		}
		if len(d.InputSchema) == 0 || len(d.OutputSchema) == 0 {
			t.Errorf("%s: missing schema", d.Name)
		}
		if d.Invoke == nil {
			t.Errorf("%s: nil Invoke", d.Name)
		}
	}
}

// TestInvoke_RoundTrip proves the type-erased Invoke unmarshals structured
// arguments, calls the typed handler, and returns the typed Out.
func TestInvoke_RoundTrip(t *testing.T) {
	p, _ := newImportTestPulse(t)
	var d ToolDescriptor
	for _, td := range Tools(Config{}) {
		if td.Name == toolmeta.ToolErrorsLookup {
			d = td
		}
	}
	if d.Invoke == nil {
		t.Fatal("errors_lookup descriptor not found")
	}
	raw, _ := json.Marshal(ErrorsLookupIn{Domain: "PULSE"})
	res, err := d.Invoke(context.Background(), p, raw)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	out, ok := res.(ErrorsLookupOut)
	if !ok {
		t.Fatalf("Invoke returned %T, want ErrorsLookupOut", res)
	}
	if len(out.Results) == 0 {
		t.Error("expected PULSE-domain results")
	}
}

// TestInvoke_CodedErrorVerbatim proves strict-decode surfaces the coded error
// through Invoke without flattening it to a string.
func TestInvoke_CodedErrorVerbatim(t *testing.T) {
	p, _ := newImportTestPulse(t)
	var d ToolDescriptor
	for _, td := range Tools(Config{}) {
		if td.Name == toolmeta.ToolProcess {
			d = td
		}
	}
	raw := json.RawMessage(`{"cohort":{"filename":"demo.pulse"},"groupers":[{"type":"GROUP_CATEGORY","field":"region"}]}`)
	_, err := d.Invoke(context.Background(), p, raw)
	if err == nil {
		t.Fatal("expected unknown-key coded error")
	}
	ce, ok := err.(*perr.CodedError)
	if !ok {
		t.Fatalf("Invoke error = %T, want *perr.CodedError", err)
	}
	if ce.Code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Errorf("code = %s, want PULSE_REQUEST_UNKNOWN_FIELD", ce.Code)
	}
}
