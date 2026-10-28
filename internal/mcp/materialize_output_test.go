package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// invokeToolErr calls one catalog tool through Invoke — the strict decode
// the transport runs — and returns its result and error.
func invokeToolErr(t *testing.T, p *pulse.Pulse, name, args string) (any, error) {
	t.Helper()
	for _, td := range Tools(Config{}) {
		if td.Name == name {
			return td.Invoke(context.Background(), p, json.RawMessage(args))
		}
	}
	t.Fatalf("%s not in the catalog", name)
	return nil, nil
}

// TestPulseMaterialize_OutputReshapes: pulse_materialize accepts the
// request's `output` block — a pure reshape with no derived column.
func TestPulseMaterialize_OutputReshapes(t *testing.T) {
	t.Parallel()

	p, src := materializeSource(t)
	res, err := invokeMaterialize(t, p, `{"request":{"cohort":{"filename":"`+src+`"},
		"output":{"columns":[{"from":"amount","as":"total"},{"from":"id"}]}},"out":"reshaped.pulse"}`)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if res.Records != 3 || len(res.DerivedColumns) != 0 {
		t.Fatalf("result = %+v", res)
	}
	schema, _ := readCohort(t, p, "reshaped.pulse")
	var names []string
	for _, f := range schema.Fields {
		names = append(names, f.Name)
	}
	if !slices.Equal(names, []string{"total", "id"}) {
		t.Fatalf("fields = %v", names)
	}
	// A later-story key is not decoded: strict decode refuses it.
	_, err = invokeMaterialize(t, p, `{"request":{"cohort":{"filename":"`+src+`"},
		"output":{"columns":[{"from":"id","type":"u64"}]}},"out":"typed.pulse"}`)
	if codeOf(err) != perr.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("output.columns[0].type: err %v, want PULSE_REQUEST_UNKNOWN_FIELD", err)
	}
}

// TestMCP_OutputRefusedOutsideMaterialize: one call per MCP entry point
// taking a request other than pulse_materialize — the request tools
// refuse `output` with PULSE_MATERIALIZE_OUTPUT_ONLY, the tools whose
// arguments carry no request refuse it as an unknown argument.
func TestMCP_OutputRefusedOutsideMaterialize(t *testing.T) {
	t.Parallel()

	p, src := materializeSource(t)
	cohort := `{"filename":"` + src + `"}`
	agg := `"aggregations":[{"type":"AGG_COUNT","field":"id","label":"n"}]`
	out := `"output":{"exclude":["name"]}`
	req := `{"cohort":` + cohort + `,` + agg + `,` + out + `}`
	cases := []struct {
		tool, args string
		want       perr.Code
	}{
		{toolmeta.ToolProcess, req, perr.PULSE_MATERIALIZE_OUTPUT_ONLY},
		{toolmeta.ToolCompose, `{"requests":[` + req + `]}`, perr.PULSE_MATERIALIZE_OUTPUT_ONLY},
		{toolmeta.ToolProcessChain, `{"cohort":` + cohort + `,"stages":[{"name":"a","request":{` + agg + `,` + out + `}}]}`, perr.PULSE_MATERIALIZE_OUTPUT_ONLY},
		{toolmeta.ToolPredict, req, perr.PULSE_MATERIALIZE_OUTPUT_ONLY},
		{toolmeta.ToolFacetSchema, `{"cohort":` + cohort + `,"fields":["name"],` + out + `}`, perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{toolmeta.ToolFacet, `{"path":"` + src + `","field":"name",` + out + `}`, perr.PULSE_REQUEST_UNKNOWN_FIELD},
		{toolmeta.ToolSample, `{"path":"` + src + `",` + out + `}`, perr.PULSE_REQUEST_UNKNOWN_FIELD},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			res, err := invokeToolErr(t, p, c.tool, c.args)
			got := codeOf(err)
			if po, ok := res.(PredictOut); ok && err == nil && len(po.Errors) > 0 {
				got = perr.Code(po.Errors[0].Code)
			}
			if got != c.want {
				t.Fatalf("err %v (result %+v), want %s", err, res, c.want)
			}
		})
	}
}
