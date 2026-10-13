package gosdk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// TestImportVectors_NotGatedByMatrices: native vector fields are a
// format feature, not a matrix one. An instance whose profile enables
// import but NOT capability:matrices still mounts pulse_import with its
// `vectors` / `vector_kinds` arguments described in full (the prose
// scrub keeps the import sentences although `vectors` is the matrices
// slot token), imports a vector cohort through them — sibling columns
// and a native list column alike — and serves the tool-import skill
// with its vector lines.
func TestImportVectors_NotGatedByMatrices(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "wide.csv", []byte("id,q_a,q_b,q_c\n1,1,2,3\n2,4,,6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "lists.ndjson", []byte(`{"id":1,"v":[1,2,3]}`+"\n"+`{"id":2,"v":[4,null,6]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp := &pulse.FeatureProfile{Features: []string{"capability:import"}}
	c, done := profiledServer(t, fs, fp, gosdk.Config{})
	defer done()
	ctx := context.Background()

	tools, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var imp *mcpsdk.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "pulse_import" {
			imp = tool
		}
	}
	if imp == nil {
		t.Fatal("pulse_import is not mounted")
	}
	if !strings.Contains(imp.Description, "`vectors` folds sibling columns") {
		t.Errorf("pulse_import description lost its vector prose:\n%s", imp.Description)
	}
	raw, _ := json.Marshal(imp.InputSchema)
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	for prop, want := range map[string]string{
		"vectors":      "Refused before any row with PULSE_IMPORT_VECTOR_INVALID",
		"vector_kinds": "A name no vectors entry declares",
	} {
		p, ok := schema.Properties[prop]
		if !ok {
			t.Errorf("pulse_import input schema has no %q argument", prop)
			continue
		}
		if !strings.Contains(p.Description, want) {
			t.Errorf("%s description scrubbed: %q lacks %q", prop, p.Description, want)
		}
	}

	for _, args := range []map[string]any{
		{"source": "wide.csv", "handle": "wide", "vectors": []string{"q:/^q_(.)$/"}, "vector_kinds": []string{"q:scale:1:7"}},
		{"source": "lists.ndjson", "handle": "lists", "vectors": []string{"v"}},
	} {
		res, err := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: "pulse_import", Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("pulse_import %v: %v %s", args, err, resultTextOrEmpty(res))
		}
		var out struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
			t.Fatal(err)
		}
		cohort, err := afero.ReadFile(fs, out.Path)
		if err != nil {
			t.Fatalf("read %s: %v", out.Path, err)
		}
		r := bytes.NewReader(cohort)
		ver, err := encoding.ReadHeader(r)
		if err != nil {
			t.Fatal(err)
		}
		s, err := encoding.ReadSchema(r, ver)
		if err != nil {
			t.Fatal(err)
		}
		if v, ok := s.Vector(1); !ok || !s.Fields[1].Type.IsVector() || v.Dim != 3 {
			t.Fatalf("%v: fields %+v", args, s.Fields)
		}
	}

	res, err := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: "pulse_skills_get", Arguments: map[string]any{"name": "tool-import"}})
	if err != nil || res.IsError {
		t.Fatalf("pulse_skills_get: %v", err)
	}
	body := resultText(t, res)
	for _, want := range []string{"`vector_kinds`", "Wide batteries fold into ONE vector", "native list column"} {
		if !strings.Contains(body, want) {
			t.Errorf("tool-import under a matrices-free profile lacks %q", want)
		}
	}
}

func resultTextOrEmpty(res *mcpsdk.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
		return tc.Text
	}
	return ""
}
