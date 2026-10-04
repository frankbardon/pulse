package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAPI_UndefinedFiguresAreNullOnTheWire: an AGG_RATIO over an
// all-zero denominator is NaN in Go; every `pulse api` JSON surface —
// the --json envelope and the --stream NDJSON rows, for process and
// compose — emits it as null and still serialises the whole response
// (types.MarshalFinite).
func TestAPI_UndefinedFiguresAreNullOnTheWire(t *testing.T) {
	dir := withTempDataDir(t)
	csv := "cat,num,den\n"
	for _, row := range []string{"a,1,0", "a,2,0", "b,3,1", "b,4,2"} {
		csv += row + "\n"
	}
	cohort := seedCohortViaImport(t, dir, "nf.csv", csv)
	req := map[string]any{
		"cohort": map[string]any{"filename": cohort},
		"groups": []any{map[string]any{"type": "GROUP_CATEGORY", "field": "cat"}},
		"aggregations": []any{map[string]any{"type": "AGG_RATIO", "field": "num", "label": "r",
			"params": map[string]any{"numerator_field": "num", "denominator_field": "den"}}},
	}
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
	process := write("p.json", req)
	compose := write("c.json", map[string]any{"requests": []any{req}})
	for _, tc := range []struct {
		name   string
		args   []string
		stream bool
	}{
		{"process --json", []string{"api", "process", "--json", "-r", process}, false},
		{"process --stream", []string{"api", "process", "--stream", "-r", process}, true},
		{"compose --json", []string{"api", "compose", "--json", "-r", compose}, false},
		{"compose --stream", []string{"api", "compose", "--stream", "-r", compose}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			root := APICommand()
			root.Writer = &buf
			if err := root.Run(context.Background(), tc.args); err != nil {
				t.Fatalf("run: %v", err)
			}
			out := buf.String()
			if tc.stream {
				sc := bufio.NewScanner(strings.NewReader(out))
				for sc.Scan() {
					if !json.Valid(sc.Bytes()) {
						t.Errorf("invalid NDJSON line: %s", sc.Text())
					}
				}
			} else if !json.Valid(buf.Bytes()) {
				t.Fatalf("invalid JSON:\n%s", out)
			}
			if strings.Contains(out, "NaN") || strings.Contains(out, "unsupported value") {
				t.Errorf("non-finite leaked to the wire:\n%s", out)
			}
			if !strings.Contains(strings.Join(strings.Fields(out), ""), `"r":null`) {
				t.Errorf("no r: null for the 0/0 group:\n%s", out)
			}
		})
	}
}
