package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// TestTools_ComposeAndChainPassReturnThrough: a slot's `return`, the
// Compose-level `return` and a chain stage's `return` survive the MCP
// strict decode and shape the tool output (each slot / stage marked,
// excluded keys absent).
func TestTools_ComposeAndChainPassReturnThrough(t *testing.T) {
	p, _ := newImportTestPulse(t)
	if _, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "data.csv"}); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	invoke := map[string]InvokeFunc{}
	for _, d := range Tools(Config{}) {
		invoke[d.Name] = d.Invoke
	}
	stage := `"aggregations":[{"type":"AGG_SUM","field":"amount","label":"s"}],"groups":[{"type":"GROUP_CATEGORY","field":"name"}]`
	req := `{"cohort":{"filename":"imports/data.pulse"},` + stage
	for _, c := range []struct {
		tool, body string
	}{
		{toolmeta.ToolCompose, `{"requests":[` + req + `,"return":{"exclude":["metadata"]}}],"return":{"preset":"minimal"}}`},
		{toolmeta.ToolProcessChain, `{"cohort":{"filename":"imports/data.pulse"},"stages":[{"name":"a","request":{` + stage + `,"return":{"exclude":["metadata"]}}}]}`},
	} {
		t.Run(c.tool, func(t *testing.T) {
			out, err := invoke[c.tool](context.Background(), p, json.RawMessage(c.body))
			if err != nil {
				t.Fatalf("%s: %v", c.tool, err)
			}
			b, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(b, []byte(`"metadata"`)) || !bytes.Contains(b, []byte(`"returned"`)) || !bytes.Contains(b, []byte(`"data"`)) {
				t.Errorf("%s output not shaped by return: %s", c.tool, b)
			}
			if c.tool == toolmeta.ToolCompose && !bytes.Contains(b, []byte(`"preset":"minimal"`)) {
				t.Errorf("compose-level return not stamped: %s", b)
			}
		})
	}
}
