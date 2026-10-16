package mcp

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// TestInvoke_FormulaCompileErrorVerbatim: pulse_process over a formula
// that does not compile surfaces PULSE_FORMULA_COMPILE_ERROR verbatim
// (the adapter renders the coded error as is), reason and source in
// details.
func TestInvoke_FormulaCompileErrorVerbatim(t *testing.T) {
	p, _ := newImportTestPulse(t)
	if _, err := HandleImport(context.Background(), p, ImportIn{Source: "data.csv", TTL: "24h"}); err != nil {
		t.Fatalf("HandleImport: %v", err)
	}
	var d ToolDescriptor
	for _, td := range Tools(Config{}) {
		if td.Name == toolmeta.ToolProcess {
			d = td
		}
	}
	raw := json.RawMessage(`{"cohort":{"filename":"imports/data.pulse"},` +
		`"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"amount *"}],` +
		`"aggregations":[{"type":"AGG_SUM","field":"f"}]}`)
	_, err := d.Invoke(context.Background(), p, raw)
	// The entry check (the field-reference walk) refuses it before any
	// record, as a located refusal whose chain carries the coded error.
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("Invoke error = %v (%T), want *perr.CodedError", err, err)
	}
	reason, _ := ce.Details["compile_error"].(string)
	if ce.Code != perr.PULSE_FORMULA_COMPILE_ERROR || !strings.Contains(reason, "unexpected token EOF") || ce.Details["expression"] != "amount *" {
		t.Fatalf("error = %s %v, want PULSE_FORMULA_COMPILE_ERROR with the reason and source", ce.Code, ce.Details)
	}
}
