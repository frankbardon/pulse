package gosdk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/afero"
)

// TestRegister_BindOnInspectJoinTargets (#260): pulse_inspect with
// `joins` binds the request-carrying tools with the join target's joined
// field names, so a joined request predict accepts also validates
// against the advertised input schema under a strict draft 2020-12
// validator. Inspected without `joins`, the same request is refused by
// the bound schema (the documented pre-fix limit).
func TestRegister_BindOnInspectJoinTargets(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeRichCohort(t, fs, "left.pulse")
	dict := encoding.NewDictionary()
	dict.Add("alpha")
	dict.Add("beta")
	writeCohortSchema(t, fs, "right.pulse", &encoding.Schema{Fields: []encoding.Field{
		{Name: "category_key", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Description: "Join key"},
		{Name: "spend", Type: encoding.FieldTypeF64, Description: "Spend per category"},
	}})
	p := newPulse(t, fs)
	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "1.0.0", BindOnInspect: true, DisableCohortScan: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()
	ctx := context.Background()

	request := map[string]any{
		"cohort": map[string]any{"filename": "left.pulse"},
		"joins": []any{map[string]any{
			"right": "right.pulse", "as": "r_",
			"on": []any{map[string]any{"left_field": "category", "right_field": "category_key"}},
		}},
		"aggregations": []any{map[string]any{"type": "AGG_SUM", "field": "r_spend"}},
		"groups":       []any{map[string]any{"type": "GROUP_CATEGORY", "field": "category"}},
	}

	// Predict accepts the joined request.
	out, err := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolmeta.ToolPredict, Arguments: request})
	if err != nil || out.IsError {
		t.Fatalf("predict: %v %+v", err, out)
	}
	var verdict struct {
		Valid bool `json:"valid"`
	}
	if err := json.Unmarshal([]byte(out.Content[0].(*mcpsdk.TextContent).Text), &verdict); err != nil || !verdict.Valid {
		t.Fatalf("predict refused the joined request: %v %s", err, out.Content[0].(*mcpsdk.TextContent).Text)
	}

	inspect := func(args map[string]any) {
		t.Helper()
		out, err := c.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolmeta.ToolInspect, Arguments: args})
		if err != nil || out.IsError {
			t.Fatalf("inspect: %v %+v", err, out)
		}
	}
	validatesBound := func(tool string) error {
		t.Helper()
		raw, err := json.Marshal(inputSchemaOf(t, c, tool))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		comp := jsonschema.NewCompiler()
		comp.DefaultDraft(jsonschema.Draft2020)
		if err := comp.AddResource("bound.json", doc); err != nil {
			t.Fatal(err)
		}
		sch, err := comp.Compile("bound.json")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(request)
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return sch.Validate(inst)
	}

	inspect(map[string]any{"path": "left.pulse"})
	if validatesBound(toolmeta.ToolProcess) == nil {
		t.Fatal("left-only binding accepted r_spend: the test would pass vacuously")
	}

	inspect(map[string]any{"path": "left.pulse", "joins": []any{map[string]any{"right": "right.pulse", "as": "r_"}}})
	for _, tool := range []string{toolmeta.ToolProcess, toolmeta.ToolPredict} {
		if err := validatesBound(tool); err != nil {
			t.Errorf("%s bound schema refused a joined request predict accepts: %v", tool, err)
		}
	}
	if got := enumAt(inputSchemaOf(t, c, toolmeta.ToolProcess), "aggregations", "field"); !slices.Contains(got, "r_spend") || slices.Contains(got, "nope") {
		t.Errorf("aggregations.field enum = %v, want r_spend among the left fields", got)
	}

	// A target that does not open widens nothing; the bind still lands.
	inspect(map[string]any{"path": "left.pulse", "joins": []any{map[string]any{"right": "missing.pulse"}}})
	if validatesBound(toolmeta.ToolProcess) == nil {
		t.Error("an unopenable join target still widened the bound enums")
	}
}
