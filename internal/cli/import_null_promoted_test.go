package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
)

// lateNullCSV writes an id,v CSV of n rows whose only null is the last v,
// so any inference sample shorter than n misses it.
func lateNullCSV(t *testing.T, path string, n int) {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("id,v\n")
	for i := range n {
		v := "7"
		if i == n-1 {
			v = ""
		}
		fmt.Fprintf(&sb, "%d,%s\n", i, v)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertNullPromotedLifted decodes an import --json envelope and requires
// exactly one PULSE_IMPORT_NULL_PROMOTED warning naming [v], with data
// keeping promoted_fields.
func assertNullPromotedLifted(t *testing.T, raw []byte) {
	t.Helper()
	var env struct {
		Data struct {
			PromotedFields []string `json:"promoted_fields"`
		} `json:"data"`
		Warnings []struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, raw)
	}
	if len(env.Data.PromotedFields) != 1 || env.Data.PromotedFields[0] != "v" {
		t.Fatalf("data.promoted_fields = %v, want [v]", env.Data.PromotedFields)
	}
	n := 0
	for _, w := range env.Warnings {
		if w.Code != string(perrors.PULSE_IMPORT_NULL_PROMOTED) {
			continue
		}
		n++
		fields, _ := w.Details["promoted_fields"].([]any)
		if len(fields) != 1 || fields[0] != "v" || w.Message == "" {
			t.Errorf("warning = %+v, want message + details.promoted_fields [v]", w)
		}
	}
	if n != 1 {
		t.Fatalf("%d PULSE_IMPORT_NULL_PROMOTED warnings in %s, want 1", n, raw)
	}
}

// TestImportCSV_JSON_LiftsNullPromoted: `pulse import csv --json` lifts the
// PULSE_IMPORT_NULL_PROMOTED warning the text path prints onto the envelope
// `warnings`, details naming the promoted fields, while data keeps
// promoted_fields.
func TestImportCSV_JSON_LiftsNullPromoted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	in := filepath.Join(dir, "in.csv")
	lateNullCSV(t, in, 60)
	root := ImportCommand()
	var buf bytes.Buffer
	root.Writer = &buf
	args := []string{"import", "csv", "-i", in, "-o", filepath.Join(dir, "out.pulse"), "--sample-rows", "50", "--json"}
	if err := root.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	assertNullPromotedLifted(t, buf.Bytes())
}

// TestImportAutoCLI_JSON_LiftsNullPromoted: the managed `import auto --json`
// path lifts the same warning.
func TestImportAutoCLI_JSON_LiftsNullPromoted(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	lateNullCSV(t, filepath.Join(dir, "late.csv"), 600) // past the default 500-row sample
	root := ImportCommand()
	var buf bytes.Buffer
	root.Writer = &buf
	if err := root.Run(context.Background(), []string{"import", "auto", "--json", "late.csv"}); err != nil {
		t.Fatal(err)
	}
	assertNullPromotedLifted(t, buf.Bytes())
}
