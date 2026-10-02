package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestAPI_TimeZoneRefusalKeepsItsCode: a zone refusal raised by the
// resolver reaches `--json` output under its own code through
// writeCodedErrorEnvelope — PROCESSING_CONFIG (with the slot/operator/tz
// details) for a non-UTC zone on a datetime GROUP_DATE, and
// PULSE_TIMEZONE_UNKNOWN for an unknown name — on process, compose and
// the rich facet leaf alike, never a PROCESS_ERROR-style placeholder.
// Compose and chain refusals keep their location (details.request /
// details.stage) through the envelope.
func TestAPI_TimeZoneRefusalKeepsItsCode(t *testing.T) {
	dir := withTempDataDir(t)
	csv := "ts,cat,n\n"
	for i := range 40 {
		csv += fmt.Sprintf("2024-01-%02dT10:00:00Z,%s,%d\n", 1+i%28, []string{"a", "b"}[i%2], i)
	}
	cohort := seedCohortViaImport(t, dir, "tz.csv", csv)

	write := func(name string, v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	request := func(tz string) map[string]any {
		return map[string]any{
			"cohort":       map[string]any{"filename": cohort},
			"time_zone":    tz,
			"aggregations": []any{map[string]any{"type": "AGG_COUNT", "field": "n"}},
			"groups":       []any{map[string]any{"type": "GROUP_DATE", "field": "ts"}},
		}
	}
	chain := map[string]any{
		"cohort": map[string]any{"filename": cohort},
		"stages": []any{map[string]any{"request": map[string]any{
			"time_zone":    "Asia/Tokyo",
			"aggregations": []any{map[string]any{"type": "AGG_SUM", "field": "n", "label": "total"}},
			"groups":       []any{map[string]any{"type": "GROUP_DATE", "field": "ts"}},
		}}},
	}
	cases := []struct {
		name string
		args []string
		code string
		loc  string // details key naming the request / stage index (0)
	}{
		{"process non-UTC", []string{"api", "process", "--json", "-r", write("p.json", request("Europe/Berlin"))}, "PROCESSING_CONFIG", ""},
		{"process unknown", []string{"api", "process", "--json", "-r", write("u.json", request("Mars/Base"))}, "PULSE_TIMEZONE_UNKNOWN", ""},
		{"compose non-UTC", []string{"api", "compose", "--json", "-r", write("c.json", map[string]any{"requests": []any{request("Asia/Tokyo")}})}, "PROCESSING_CONFIG", "request"},
		{"compose parallel non-UTC", []string{"api", "compose", "--json", "--parallel", "2", "-r", write("c1.json", map[string]any{"requests": []any{request("Asia/Tokyo")}})}, "PROCESSING_CONFIG", "request"},
		{"chain non-UTC", []string{"api", "process-chain", "--json", "-r", write("ch.json", chain)}, "PROCESSING_CONFIG", "stage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			root := APICommand()
			root.Writer = &buf
			if err := root.Run(context.Background(), tc.args); err != nil {
				t.Fatalf("hard error instead of an envelope: %v", err)
			}
			var env struct {
				Errors []struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
				t.Fatalf("not an envelope: %v\n%s", err, buf.String())
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != tc.code {
				t.Fatalf("errors = %+v, want first code %s\n%s", env.Errors, tc.code, buf.String())
			}
			if tc.code == "PROCESSING_CONFIG" && (env.Errors[0].Details["slot"] != "groups[0]" || env.Errors[0].Details["operator"] != "GROUP_DATE") {
				t.Errorf("details = %v", env.Errors[0].Details)
			}
			if tc.loc != "" && env.Errors[0].Details[tc.loc] != float64(0) {
				t.Errorf("details[%q] = %v, want 0; details = %v", tc.loc, env.Errors[0].Details[tc.loc], env.Errors[0].Details)
			}
		})
	}
}
