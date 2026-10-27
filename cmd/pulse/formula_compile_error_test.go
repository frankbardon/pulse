package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestFormulaCompileError_Envelope: `pulse api process --json` over an
// expression that does not compile writes PULSE_FORMULA_COMPILE_ERROR —
// the fatal coded error's own code, never the PROCESS_ERROR placeholder —
// with the reason in the message and details, and exits 1.
func TestFormulaCompileError_Envelope(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := t.TempDir()
	t.Setenv("PULSE_DATA_DIR", dir)
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	cohort := createTestPulseFile(t, dir)
	req := filepath.Join(dir, "req.json")
	body := `{"cohort":{"filename":"` + cohort + `"},"filterers":[{"type":"FILTER_EXPRESSION","expression":"age >"}],` +
		`"aggregations":[{"type":"AGG_COUNT","field":"age"}]}`
	if err := os.WriteFile(req, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	status, out, _ := runExit(t, "api", "process", "--json", "-r", req)
	if status != 1 {
		t.Fatalf("exit %d, want 1\n%s", status, out)
	}
	env := decodeExitEnvelope(t, "api process", out)
	if env == nil || len(env.Errors) != 1 {
		t.Fatalf("want one envelope error, got %s", out)
	}
	e := env.Errors[0]
	if e.Code != string(errors.PULSE_FORMULA_COMPILE_ERROR) {
		t.Fatalf("code %s, want PULSE_FORMULA_COMPILE_ERROR", e.Code)
	}
	reason, _ := e.Details["compile_error"].(string)
	if !strings.Contains(reason, "unexpected token EOF") || e.Details["expression"] != "age >" || !strings.Contains(e.Message, reason) {
		t.Fatalf("entry %+v, want the reason in message and details.compile_error, the source in details.expression", e)
	}
}
