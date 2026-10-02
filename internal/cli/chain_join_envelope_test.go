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

// TestAPI_LocatedJoinRefusalsKeepCodeAndDetails: the chain stage-join
// refusal and a join-count refusal inside Compose reach `--json`
// output under their own codes, with their location (details.stage /
// details.request) intact through writeCodedErrorEnvelope.
func TestAPI_LocatedJoinRefusalsKeepCodeAndDetails(t *testing.T) {
	dir := withTempDataDir(t)
	csv := "cat,n\n"
	for i := range 20 {
		csv += fmt.Sprintf("%s,%d\n", []string{"a", "b"}[i%2], i)
	}
	cohort := seedCohortViaImport(t, dir, "j.csv", csv)
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
	join := func(as string) map[string]any {
		return map[string]any{"right": cohort, "on": []any{map[string]any{"left_field": "n", "right_field": "n"}}, "as": as}
	}
	chain := map[string]any{
		"cohort": map[string]any{"filename": cohort},
		"stages": []any{
			map[string]any{"name": "s0", "request": map[string]any{
				"aggregations": []any{map[string]any{"type": "AGG_SUM", "field": "n", "label": "total"}},
				"groups":       []any{map[string]any{"type": "GROUP_CATEGORY", "field": "cat"}},
			}},
			map[string]any{"name": "s1", "request": map[string]any{
				"aggregations": []any{map[string]any{"type": "AGG_SUM", "field": "total"}},
				"joins":        []any{join("r_")},
			}},
		},
	}
	compose := map[string]any{"requests": []any{map[string]any{
		"cohort":       map[string]any{"filename": cohort},
		"aggregations": []any{map[string]any{"type": "AGG_COUNT", "field": "n"}},
		"joins":        []any{join("a_"), join("b_")},
	}}}
	cases := []struct {
		name string
		args []string
		code string
		want map[string]any
	}{
		{"chain stage join", []string{"api", "process-chain", "--json", "-r", write("ch.json", chain)}, "PULSE_CHAIN_STAGE_JOIN",
			map[string]any{"stage": float64(1), "stage_name": "s1", "count": float64(1)}},
		{"compose join count", []string{"api", "compose", "--json", "-r", write("c.json", compose)}, "PULSE_JOIN_TOO_MANY",
			map[string]any{"request": float64(0), "count": float64(2)}},
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
			for k, v := range tc.want {
				if env.Errors[0].Details[k] != v {
					t.Errorf("details[%q] = %v, want %v; details = %v", k, env.Errors[0].Details[k], v, env.Errors[0].Details)
				}
			}
		})
	}
}
