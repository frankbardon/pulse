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
)

// TestParams_ProcessTool (U58 E3-S1): pulse_process accepts a params
// block — strict decode keeps it, a FILTER_EXPRESSION reads
// params.from_wave, a large integer stays exact — and an undeclared read
// is the coded PULSE_PARAMS_UNDECLARED.
func TestParams_ProcessTool(t *testing.T) {
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
