package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeParentChildCSV writes a synthetic order-line CSV: 600 lines, 50
// parents (p_id → p_name, p_score, 12 lines each) and a child column.
func writeParentChildCSV(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("line_id,p_id,p_name,p_score,qty\n")
	for i := 0; i < 600; i++ {
		p := i / 12
		fmt.Fprintf(&b, "%d,%d,parent-%02d,%d.5,%d\n", i, 1000+p, p, 300+p, 1+(i*31)%9)
	}
	path := filepath.Join(dir, "lines.csv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCliImportPredict_SuggestGroups: `import predict --suggest-groups`
// reports the parent as a ready-to-paste --group value inside the
// standard envelope; feeding that value back through `import predict
// --group` evaluates it as the import would.
func TestCliImportPredict_SuggestGroups(t *testing.T) {
	csvPath := writeParentChildCSV(t, t.TempDir())
	out, err := runApp(t, "import", "predict", "--input", csvPath, "--suggest-groups", "--json")
	if err != nil {
		t.Fatalf("import predict --suggest-groups: %v\n%s", err, out)
	}
	var env struct {
		FormatVersion string `json:"format_version"`
		Data          struct {
			GroupCandidates struct {
				Suggested  []string `json:"suggested"`
				Candidates []struct {
					Key     []string `json:"key"`
					Members []string `json:"members"`
					Verdict string   `json:"verdict"`
				} `json:"candidates"`
			}
			Projection struct {
				FlatFileBytes int64 `json:"flat_file_bytes"`
			}
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid envelope: %v\n%s", err, out)
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version %q", env.FormatVersion)
	}
	want := "p_id:p_name,p_score"
	if s := env.Data.GroupCandidates.Suggested; len(s) != 1 || s[0] != want {
		t.Fatalf("suggested %v, want [%s]", s, want)
	}
	if env.Data.Projection.FlatFileBytes <= 0 {
		t.Errorf("projection missing: %s", out)
	}

	out, err = runApp(t, "import", "predict", "--input", csvPath, "--group", want)
	if err != nil {
		t.Fatalf("import predict --group: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Parent group 1 [key: p_id]: admitted, 50 distinct tuples, ratio 12.00x") {
		t.Errorf("text output lacks the evaluated group:\n%s", out)
	}
}

// TestCliImportPredict_GroupViolation: a --group the import would refuse
// fails predict with the import's own code under --json.
func TestCliImportPredict_GroupViolation(t *testing.T) {
	csvPath := writeParentChildCSV(t, t.TempDir())
	out, _ := runApp(t, "import", "predict", "--input", csvPath, "--group", "p_id:p_score,line_id", "--json")
	if !strings.Contains(out, `"code": "PULSE_GROUP_MEMBER_NOT_CONSTANT"`) {
		t.Fatalf("want PULSE_GROUP_MEMBER_NOT_CONSTANT envelope, got:\n%s", out)
	}
}
