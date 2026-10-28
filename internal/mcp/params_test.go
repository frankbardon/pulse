package mcp

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// TestParams_ProcessTool (U58 E3-S1): pulse_process accepts a params
// block — strict decode keeps it, a FILTER_EXPRESSION reads
// params.from_wave, a large integer stays exact — and an undeclared read
// is the coded PULSE_PARAMS_UNDECLARED.
func TestParams_ProcessTool(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), "waves.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "wave", Type: encoding.FieldTypeU16},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := b.Append(pulse.CohortRow{uint64(i%5 + 1), float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	body := func(params string) string {
		return `{"cohort":{"filename":"waves.pulse"},"params":` + params + `,` +
			`"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.from_wave && params.big == 9007199254740993"}],` +
			`"aggregations":[{"type":"AGG_COUNT","field":"x","label":"c"}],"return":{"preset":"full"}}`
	}
	out := invokeTool(t, p, Config{}, "pulse_process", body(`{"from_wave":3,"big":9007199254740993}`))
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || len(resp.Data) != 1 || resp.Data[0]["c"] != float64(2) {
		t.Fatalf("pulse_process: %v %s", err, out)
	}

	for _, d := range Tools(Config{}) {
		if d.Name != "pulse_process" {
			continue
		}
		_, err := d.Invoke(context.Background(), p, json.RawMessage(body(`{"from_wave":3}`)))
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_PARAMS_UNDECLARED || ce.Details["param"] != "big" {
			t.Fatalf("undeclared: %v", err)
		}
		return
	}
	t.Fatal("no pulse_process tool")
}

// TestParams_ComposeAndChainTools (U58 E3-S2): pulse_compose and
// pulse_process_chain take a top-level params block every slot / stage
// inherits (a sub-request's own key wins), and both tools' input
// schemas declare it.
func TestParams_ComposeAndChainTools(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), "waves.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "wave", Type: encoding.FieldTypeU16},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// wave = i%5+1, x = i: Σx over wave 3 = 2+7 = 9, over wave 5 = 4+9 = 13.
	for i := 0; i < 10; i++ {
		if err := b.Append(pulse.CohortRow{uint64(i%5 + 1), float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	slot := func(label, own string) string {
		return `{"label":"` + label + `","cohort":{"filename":"waves.pulse"},` + own +
			`"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.from_wave"}],` +
			`"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"return":{"preset":"full"}}`
	}
	out := invokeTool(t, p, Config{}, "pulse_compose",
		`{"params":{"from_wave":3},"requests":[`+slot("a", "")+`,`+slot("b", `"params":{"from_wave":5},`)+`]}`)
	var cresp struct {
		Responses []struct {
			Data []map[string]any `json:"data"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(out, &cresp); err != nil || len(cresp.Responses) != 2 ||
		cresp.Responses[0].Data[0]["s"] != float64(9) || cresp.Responses[1].Data[0]["s"] != float64(13) {
		t.Fatalf("pulse_compose: %v %s", err, out)
	}

	out = invokeTool(t, p, Config{}, "pulse_process_chain",
		`{"cohort":{"filename":"waves.pulse"},"params":{"from_wave":3},"stages":[{"request":{`+
			`"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.from_wave"}],`+
			`"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"return":{"preset":"full"}}}]}`)
	var chresp struct {
		Final struct {
			Data []map[string]any `json:"data"`
		} `json:"final"`
	}
	if err := json.Unmarshal(out, &chresp); err != nil || len(chresp.Final.Data) != 1 || chresp.Final.Data[0]["s"] != float64(9) {
		t.Fatalf("pulse_process_chain: %v %s", err, out)
	}

	// The bound schemas declare the root params, and drop it with
	// capability:params.
	base := []string{"capability:process", "capability:compose", "capability:process_chain", "FILTER_EXPRESSION", "AGG_SUM"}
	for _, c := range []struct {
		features []string
		want     bool
	}{{nil, true}, {base, false}, {append(append([]string{}, base...), "capability:params"), true}} {
		var fp *pulse.FeatureProfile
		if c.features != nil {
			fp = &pulse.FeatureProfile{Features: c.features}
		}
		_, inst := instanceFor(t, fp, pulse.Extensions{})
		bound, err := BindForInstance(makeBindSchema(), inst)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range []string{toolmeta.ToolCompose, toolmeta.ToolProcessChain} {
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(bound[tool], &schema); err != nil {
				t.Fatal(err)
			}
			if _, ok := schema.Properties["params"]; ok != c.want {
				t.Fatalf("%v: %s bound schema params present = %v, want %v", c.features, tool, ok, c.want)
			}
		}
	}
}

// TestParams_FacetSchemaTool (expression-wires E2-S4): pulse_facet_schema
// accepts a FacetRequest params block its filterers read, an undeclared
// read is the coded PULSE_PARAMS_UNDECLARED, and the bound schema
// declares params only with capability:params.
func TestParams_FacetSchemaTool(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), "waves.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "wave", Type: encoding.FieldTypeU16},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := b.Append(pulse.CohortRow{uint64(i%5 + 1), float64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	body := func(params string) string {
		return `{"cohort":{"filename":"waves.pulse"},"fields":["x"],"params":` + params + `,` +
			`"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.from_wave"}]}`
	}
	out := invokeTool(t, p, Config{}, toolmeta.ToolFacetSchema, body(`{"from_wave":3}`))
	var resp struct {
		FilteredRecords int64 `json:"filtered_records"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.FilteredRecords != 2 {
		t.Fatalf("pulse_facet_schema: %v %s", err, out)
	}
	invoked := false
	for _, d := range Tools(Config{}) {
		if d.Name != toolmeta.ToolFacetSchema {
			continue
		}
		invoked = true
		_, err := d.Invoke(context.Background(), p, json.RawMessage(body(`{"to_wave":3}`)))
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_PARAMS_UNDECLARED {
			t.Fatalf("undeclared: %v", err)
		}
	}
	if !invoked {
		t.Fatal("no pulse_facet_schema tool")
	}

	base := []string{"capability:process", "capability:facet", "FILTER_EXPRESSION"}
	for _, c := range []struct {
		features []string
		want     bool
	}{{nil, true}, {base, false}, {append(append([]string{}, base...), "capability:params"), true}} {
		var fp *pulse.FeatureProfile
		if c.features != nil {
			fp = &pulse.FeatureProfile{Features: c.features}
		}
		_, inst := instanceFor(t, fp, pulse.Extensions{})
		bound, err := BindForInstance(makeBindSchema(), inst)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(bound[toolmeta.ToolFacetSchema], &schema); err != nil {
			t.Fatal(err)
		}
		if _, ok := schema.Properties["params"]; ok != c.want {
			t.Fatalf("%v: bound facet schema params present = %v, want %v", c.features, ok, c.want)
		}
	}
}
