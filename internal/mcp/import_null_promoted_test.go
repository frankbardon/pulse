package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
)

// TestHandleImport_CarriesNullPromotedWarning: pulse_import has no envelope,
// so the PULSE_IMPORT_NULL_PROMOTED warning the CLI lifts rides the result's
// null_warnings, beside the unchanged promoted_fields — parity with
// `pulse import --json`.
func TestHandleImport_CarriesNullPromotedWarning(t *testing.T) {
	t.Parallel()

	var sb strings.Builder
	sb.WriteString("id,v\n")
	for i := range 600 { // past the default 500-row inference sample
		v := "7"
		if i == 599 {
			v = ""
		}
		fmt.Fprintf(&sb, "%d,%s\n", i, v)
	}
	afs := afero.NewMemMapFs()
	if err := afero.WriteFile(afs, "late_null.csv", []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	out, err := invokeImportOut(t, p, `{"source":"late_null.csv"}`)
	if err != nil {
		t.Fatalf("pulse_import: %v", err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		PromotedFields []string `json:"promoted_fields"`
		NullWarnings   []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"null_warnings"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.PromotedFields) != 1 || wire.PromotedFields[0] != "v" {
		t.Fatalf("promoted_fields = %v, want [v]", wire.PromotedFields)
	}
	if len(wire.NullWarnings) != 1 || wire.NullWarnings[0].Code != string(perr.PULSE_IMPORT_NULL_PROMOTED) {
		t.Fatalf("null_warnings = %+v, want one PULSE_IMPORT_NULL_PROMOTED\n%s", wire.NullWarnings, raw)
	}
	if f, _ := wire.NullWarnings[0].Details["promoted_fields"].([]any); len(f) != 1 || f[0] != "v" {
		t.Fatalf("details.promoted_fields = %v, want [v]", wire.NullWarnings[0].Details["promoted_fields"])
	}
}
