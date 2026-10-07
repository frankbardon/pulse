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

// TestAPI_ComposeAndChainPassReturnThrough: `pulse api compose` and
// `pulse api process-chain` hand the request's `return` blocks (per
// slot, Compose-level, per stage) to the facade; the `--json` data is
// shaped — `returned` stamped, the excluded key absent.
func TestAPI_ComposeAndChainPassReturnThrough(t *testing.T) {
	dir := withTempDataDir(t)
	csv := "cat,n\n"
	for i := range 20 {
		csv += fmt.Sprintf("%s,%d\n", []string{"a", "b"}[i%2], i)
	}
	cohort := seedCohortViaImport(t, dir, "r.csv", csv)
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
	stage := func() map[string]any {
		return map[string]any{
			"aggregations": []any{map[string]any{"type": "AGG_SUM", "field": "n", "label": "total"}},
			"groups":       []any{map[string]any{"type": "GROUP_CATEGORY", "field": "cat"}},
			"return":       map[string]any{"exclude": []any{"metadata"}},
		}
	}
	slot := stage()
	slot["cohort"] = map[string]any{"filename": cohort}
	compose := map[string]any{"requests": []any{slot}, "return": map[string]any{"preset": "minimal"}}
	chain := map[string]any{
		"cohort": map[string]any{"filename": cohort},
		"stages": []any{map[string]any{"name": "s0", "request": stage()}},
	}
	for name, args := range map[string][]string{
		"compose":       {"api", "compose", "--json", "-r", write("c.json", compose)},
		"process-chain": {"api", "process-chain", "--json", "-r", write("ch.json", chain)},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			root := APICommand()
			root.Writer = &buf
			if err := root.Run(context.Background(), args); err != nil {
				t.Fatalf("run: %v", err)
			}
			var env struct {
				Data   json.RawMessage `json:"data"`
				Errors []any           `json:"errors"`
			}
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil || len(env.Errors) != 0 {
				t.Fatalf("envelope: %v %v\n%s", err, env.Errors, buf.String())
			}
			if bytes.Contains(env.Data, []byte(`"metadata"`)) || !bytes.Contains(env.Data, []byte(`"returned"`)) || !bytes.Contains(env.Data, []byte(`"total"`)) {
				t.Errorf("data not shaped by return: %s", env.Data)
			}
			if name == "compose" && !bytes.Contains(env.Data, []byte(`"minimal"`)) {
				t.Errorf("compose-level return not stamped: %s", env.Data)
			}
		})
	}
}
