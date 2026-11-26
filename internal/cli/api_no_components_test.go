package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAPINoComponentsFlag drives `--no-components` on the three leaves
// that mount it — `pulse api process`, `process-chain` and `compose`.
// Without the flag every response carries data.components; with it the
// block is absent and the rest of the envelope is byte-identical (the
// components-bearing run with its components keys deleted re-marshals to
// exactly the flagged run's bytes).
func TestAPINoComponentsFlag(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "nc.csv", "n,cat\n1,a\n2,b\n3,a\n4,b\n")
	single := map[string]any{
		"cohort":       map[string]any{"filename": cohort},
		"groups":       []any{map[string]any{"type": "GROUP_CATEGORY", "field": "cat"}},
		"aggregations": []any{map[string]any{"type": "AGG_SUM", "field": "n", "label": "s"}},
	}
	leaves := []struct {
		leaf string
		body any
		// components returns the components objects a run carries and
		// deletes them from data, so the remainder can be compared.
		components func(data map[string]any) int
	}{
		{"process", single, func(d map[string]any) int { return popComponents(d) }},
		{
			"process-chain",
			map[string]any{"cohort": map[string]any{"filename": cohort}, "stages": []any{map[string]any{"name": "s0", "request": single}}},
			func(d map[string]any) int {
				n := popComponents(asMap(d["final"]))
				for _, st := range asSlice(d["stages"]) {
					n += popComponents(asMap(st))
				}
				return n
			},
		},
		{
			"compose",
			map[string]any{"requests": []any{single, single}},
			func(d map[string]any) int {
				n := 0
				for _, r := range asSlice(d["responses"]) {
					n += popComponents(asMap(r))
				}
				return n
			},
		},
	}
	for _, lc := range leaves {
		body, err := json.Marshal(lc.body)
		if err != nil {
			t.Fatal(err)
		}
		reqPath := filepath.Join(dir, lc.leaf+".json")
		if err := os.WriteFile(reqPath, body, 0o644); err != nil {
			t.Fatal(err)
		}
		run := func(extra ...string) map[string]any {
			t.Helper()
			var buf bytes.Buffer
			root := APICommand()
			root.Writer = &buf
			args := append([]string{"api", lc.leaf, "-r", reqPath, "--json"}, extra...)
			if err := root.Run(context.Background(), args); err != nil {
				t.Fatalf("%s: %v\n%s", lc.leaf, err, buf.String())
			}
			var env map[string]any
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
				t.Fatalf("%s: not an envelope: %v\n%s", lc.leaf, err, buf.String())
			}
			if errs := asSlice(env["errors"]); len(errs) != 0 {
				t.Fatalf("%s: errors %v", lc.leaf, errs)
			}
			return env
		}
		with, without := run(), run("--no-components")
		if n := lc.components(asMap(without["data"])); n != 0 {
			t.Errorf("%s --no-components: %d components blocks, want none", lc.leaf, n)
		}
		want := 1
		if lc.leaf != "process" {
			want = 2 // compose: two slots; process-chain: the stage and final
		}
		if n := lc.components(asMap(with["data"])); n != want {
			t.Errorf("%s: %d components blocks, want %d (the flag must have something to drop)", lc.leaf, n, want)
		}
		a, _ := json.Marshal(with)
		b, _ := json.Marshal(without)
		if !bytes.Equal(a, b) {
			t.Errorf("%s: --no-components changed more than data.components:\nwith (components removed): %s\nwithout: %s", lc.leaf, a, b)
		}
	}
}

// popComponents deletes a response object's "components" key, reporting
// whether one was there.
func popComponents(resp map[string]any) int {
	if resp == nil {
		return 0
	}
	if _, ok := resp["components"]; !ok {
		return 0
	}
	delete(resp, "components")
	return 1
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

func asSlice(v any) []any { s, _ := v.([]any); return s }
