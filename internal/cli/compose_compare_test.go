package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pulse "github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/optfixture"
)

// TestCompare_CLIComposeReturnsComparisons (U65 E3-S3): `pulse api
// compose --json` returns the nested-model comparisons byte for byte as
// the library's ComposedResponse, serial and --parallel alike, and
// `--stream` ends with one {"comparisons":[...]} line equal to the
// library's.
func TestCompare_CLIComposeReturnsComparisons(t *testing.T) {
	// serial: t.Chdir moves the process working directory; t.Setenv mutates the process environment

	dir, p, at := optCLIFixture(t)
	cohort := `{"filename":"` + optfixture.CohortFile + `"}`
	fit := func(preds string) string {
		return `{"cohort":` + cohort + `,"regressions":[{"type":"REG_OLS","target":"units","predictors":` + preds + `}]}`
	}
	raw := at([]byte(`{"requests":[{"label":"n","cohort":` + cohort + `,"aggregations":[{"type":"AGG_COUNT","field":"units","label":"n"}]}],
		"fits":[{"name":"price","request":` + fit(`["price"]`) + `},{"name":"trend","request":` + fit(`["price","week"]`) + `}],
		"compare":[{"name":"week_adds","restricted":"price","full":"trend"},{"name":"week_wald","restricted":"price","full":"trend","test":"wald"}]}`))
	reqPath := filepath.Join(dir, "compare.json")
	if err := os.WriteFile(reqPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var req pulse.ComposedRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	lib, err := p.Compose(t.Context(), &req)
	if err != nil {
		t.Fatalf("library Compose: %v", err)
	}
	if len(lib.Comparisons) != 2 || lib.Comparisons[0].Error != nil || lib.Comparisons[1].Error != nil {
		t.Fatalf("library comparisons %+v", lib.Comparisons)
	}
	want, err := json.Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"compose", "--json", "-r", reqPath}, {"compose", "--json", "--parallel", "3", "-r", reqPath}} {
		var buf bytes.Buffer
		if err := runAPICLI(t, &buf, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, buf.String())
		}
		var env struct {
			Errors []any           `json:"errors"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(buf.Bytes(), &env); err != nil || len(env.Errors) != 0 {
			t.Fatalf("envelope (%v): %s", err, buf.String())
		}
		var got bytes.Buffer
		if err := json.Compact(&got, env.Data); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("%v: CLI data differs from the library response:\n cli %s\n lib %s", args, got.Bytes(), want)
		}
	}
	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "compose", "--stream", "-r", reqPath); err != nil {
		t.Fatalf("stream: %v\n%s", err, buf.String())
	}
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	wantLast, err := json.Marshal(map[string]any{"comparisons": lib.Comparisons})
	if err != nil {
		t.Fatal(err)
	}
	if last := lines[len(lines)-1]; !bytes.Equal(last, wantLast) {
		t.Fatalf("stream ends with %s, want %s", last, wantLast)
	}
}
