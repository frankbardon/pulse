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
)

// TestAPIPredict_ShowsAdvisories: `pulse api predict` carries
// data.advisories under --json and prints one "Advisory [CODE]" line in
// text mode — a TEST_T split by a three-group categorical.
func TestAPIPredict_ShowsAdvisories(t *testing.T) {
	dir := withTempDataDir(t)
	csv := "n,cat\n"
	for i := range 30 {
		csv += fmt.Sprintf("%d,%s\n", i, []string{"a", "b", "c"}[i%3])
	}
	cohort := seedCohortViaImport(t, dir, "adv.csv", csv)
	req, err := json.Marshal(map[string]any{
		"cohort": map[string]any{"filename": cohort},
		"tests":  []any{map[string]any{"type": "TEST_T", "field": "n", "split_by": "cat"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reqPath := filepath.Join(dir, "r.json")
	if err := os.WriteFile(reqPath, req, 0o644); err != nil {
		t.Fatal(err)
	}
	const code = "PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS"
	run := func(args ...string) string {
		t.Helper()
		var buf bytes.Buffer
		root := APICommand()
		root.Writer = &buf
		_ = root.Run(context.Background(), append([]string{"api", "predict", "-r", reqPath}, args...))
		return buf.String()
	}

	var env struct {
		Data struct {
			Advisories []struct {
				Code string `json:"code"`
			} `json:"advisories"`
		} `json:"data"`
	}
	out := run("--json")
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, out)
	}
	if len(env.Data.Advisories) != 1 || env.Data.Advisories[0].Code != code {
		t.Errorf("--json advisories = %+v, want one %s\n%s", env.Data.Advisories, code, out)
	}
	if text := run(); !strings.Contains(text, "Advisory ["+code+"]: ") {
		t.Errorf("text output lacks the advisory line:\n%s", text)
	}
}
