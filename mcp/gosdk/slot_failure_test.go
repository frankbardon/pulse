package gosdk_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// TestProcessTool_FailedSlotCarriesItsError: over MCP pulse_process
// (whose `return` defaults to the standard preset) a GLM that separates
// fails alone — the result arrives as {name, type, error} with the
// refusal's own code beside the surviving aggregation, and the
// PULSE_SLOT_FAILED warning rides the result's warnings (an MCP result
// has no envelope) — the shape the CLI and the library return.
func TestProcessTool_FailedSlotCarriesItsError(t *testing.T) {
	fs := afero.NewMemMapFs()
	body := "x,y\n"
	for i := 0; i < 40; i++ {
		y := "0"
		if i > 19 {
			y = "1"
		}
		body += strconv.Itoa(i) + ".5," + y + "\n"
	}
	if err := afero.WriteFile(fs, "s.csv", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p := newPulse(t, fs)
	imp, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "s.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "9.9.9", DisableCohortScan: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()

	res, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "pulse_process", Arguments: map[string]any{
		"cohort":       map[string]any{"filename": imp.Path},
		"aggregations": []any{map[string]any{"type": "AGG_COUNT", "field": "x", "label": "n"}},
		"regressions": []any{map[string]any{"type": "REG_GLM", "name": "default", "family": "binomial",
			"target": "y", "predictors": []string{"x"}}},
	}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text := res.Content[0].(*mcpsdk.TextContent).Text
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	var out struct {
		Data        []map[string]any             `json:"data"`
		Regressions []map[string]json.RawMessage `json:"regressions"`
		Warnings    []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(out.Data) != 1 || len(out.Regressions) != 1 || len(out.Regressions[0]) != 3 {
		t.Fatalf("want the aggregation and a {name, type, error} regression:\n%s", text)
	}
	var se struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out.Regressions[0]["error"], &se); err != nil || se.Code != "PROCESSING_REGRESSION_SEPARATION" || se.Message == "" {
		t.Fatalf("regression error %s (%v)", out.Regressions[0]["error"], err)
	}
	found := false
	for _, w := range out.Warnings {
		found = found || (w.Code == "PULSE_SLOT_FAILED" && w.Details["label"] == "default")
	}
	if !found {
		t.Errorf("no PULSE_SLOT_FAILED warning:\n%s", text)
	}
}
