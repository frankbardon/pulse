package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	pcli "github.com/frankbardon/pulse/internal/cli"
)

// TestCliSlotFailure_ExitFollowsTheEnvelope: `pulse api process --json`
// on a request whose GLM separates exits 0 with the failed slot's
// `error` in data and a PULSE_SLOT_FAILED warning lifted to the
// envelope; with --strict it exits 1 and the envelope's error carries
// the slot's OWN code (writeCodedErrorEnvelope), never a placeholder.
func TestCliSlotFailure_ExitFollowsTheEnvelope(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	f := newExitFixture(t)
	var b strings.Builder
	b.WriteString("x,y\n")
	for i := range 40 {
		y := 0
		if i > 19 {
			y = 1
		}
		fmt.Fprintf(&b, "%d.5,%d\n", i, y)
	}
	src := writeExitFile(t, f.p("sep.csv"), b.String())
	cohort := filepath.Join(f.dir, "sep.pulse")
	if out, err := runApp(t, "import", "csv", "-i", src, "-o", cohort); err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	req := writeExitFile(t, f.p("sep_req.json"), `{"cohort":{"filename":"`+cohort+`"},"aggregations":[{"type":"AGG_COUNT","field":"x","label":"n"}],`+
		`"regressions":[{"type":"REG_GLM","name":"default","family":"binomial","target":"y","predictors":["x"]}]}`)

	s, out, errOut := runExit(t, "api", "process", "--json", "-r", req)
	if s != pcli.ExitOK {
		t.Fatalf("partial success: exit %d (want 0)\n%s\n%s", s, out, errOut)
	}
	var env struct {
		Data struct {
			Regressions []struct {
				Name  string `json:"name"`
				Error *struct {
					Code string `json:"code"`
				} `json:"error"`
			} `json:"regressions"`
			Data []map[string]any `json:"data"`
		} `json:"data"`
		Errors   []map[string]any `json:"errors"`
		Warnings []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, out)
	}
	if len(env.Errors) != 0 || len(env.Data.Data) != 1 || len(env.Data.Regressions) != 1 ||
		env.Data.Regressions[0].Error == nil || env.Data.Regressions[0].Error.Code != "PROCESSING_REGRESSION_SEPARATION" {
		t.Fatalf("partial success envelope %s", out)
	}
	lifted := false
	for _, w := range env.Warnings {
		lifted = lifted || (w.Code == "PULSE_SLOT_FAILED" && w.Details["slot_kind"] == "regressions" && w.Details["code"] == "PROCESSING_REGRESSION_SEPARATION")
	}
	if !lifted {
		t.Errorf("no lifted PULSE_SLOT_FAILED warning: %s", out)
	}

	s, out, _ = runExit(t, "api", "process", "--json", "--strict", "-r", req)
	envS := decodeExitEnvelope(t, "strict", out)
	if s != pcli.ExitFatal || envS == nil || len(envS.Errors) != 1 || envS.Errors[0].Code != "PROCESSING_REGRESSION_SEPARATION" {
		t.Fatalf("--strict: exit %d (want 1), envelope %s", s, out)
	}
}
