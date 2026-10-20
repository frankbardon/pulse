package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"math"
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

// TestAPI_InfiniteTestStatisticsAreNull (U36 E1-S3): an infinite test
// figure — TEST_FISHER_EXACT's conditional MLE odds ratio over a zero
// off-diagonal cell (a at the top of its support; R fisher.test: Inf,
// U36 E1-S4) and the upper end of its conf.int there (Inf, U36 E1-S8), TEST_PEARSON_R / TEST_SPEARMAN_R details.t at |r| = 1,
// TEST_BROWN_FORSYTHE's F at zero within-group spread — is +Inf in Go and
// `null` (key kept) through `pulse api process --json`, while its p-value
// stays a number: Fisher's exact p, 0 at |r| = 1 and at the infinite F.
func TestAPI_InfiniteTestStatisticsAreNull(t *testing.T) {
	dir := withTempDataDir(t)
	csv := "g,o,x,y,v\n" +
		"a,no,1,2,0\na,no,2,4,2\na,no,3,6,0\na,no,4,8,2\n" +
		"b,no,5,10,5\nb,yes,6,12,5\nb,yes,7,14,5\nb,yes,8,16,5\n"
	cohort := seedCohortViaImport(t, dir, "inf.csv", csv)
	req := map[string]any{
		"cohort": map[string]any{"filename": cohort},
		"tests": []any{
			map[string]any{"type": "TEST_FISHER_EXACT", "rows": "g", "cols": "o", "label": "fisher"},
			map[string]any{"type": "TEST_PEARSON_R", "field": "x", "field2": "y", "label": "pearson"},
			map[string]any{"type": "TEST_SPEARMAN_R", "field": "x", "field2": "y", "label": "spearman"},
			map[string]any{"type": "TEST_BROWN_FORSYTHE", "field": "v", "split_by": "g", "label": "bf"},
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "inf.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	root := APICommand()
	root.Writer = &buf
	if err := root.Run(context.Background(), []string{"api", "process", "--json", "-r", path}); err != nil {
		t.Fatalf("run: %v\n%s", err, buf.String())
	}
	var env struct {
		Errors []any `json:"errors"`
		Data   struct {
			Tests []map[string]json.RawMessage `json:"tests"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if len(env.Errors) != 0 || len(env.Data.Tests) != 4 {
		t.Fatalf("errors %v, %d tests:\n%s", env.Errors, len(env.Data.Tests), buf.String())
	}
	detail := func(test map[string]json.RawMessage, key string) json.RawMessage {
		var d map[string]json.RawMessage
		if err := json.Unmarshal(test["details"], &d); err != nil {
			t.Fatalf("details: %v", err)
		}
		v, ok := d[key]
		if !ok {
			t.Fatalf("details.%s absent, want null with the key kept", key)
		}
		return v
	}
	number := func(raw json.RawMessage) float64 {
		var f *float64
		if err := json.Unmarshal(raw, &f); err != nil || f == nil {
			t.Fatalf("%s, want a number", raw)
		}
		return *f
	}
	fisher, pearson, spearman, bf := env.Data.Tests[0], env.Data.Tests[1], env.Data.Tests[2], env.Data.Tests[3]
	for _, c := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"fisher statistic", fisher["statistic"]},
		{"fisher details.odds_ratio", detail(fisher, "odds_ratio")},
		{"fisher details.ci_high", detail(fisher, "ci_high")},
		{"pearson details.t", detail(pearson, "t")},
		{"spearman details.t", detail(spearman, "t")},
		{"bf statistic", bf["statistic"]},
	} {
		if string(c.raw) != "null" {
			t.Errorf("%s = %s, want null", c.name, c.raw)
		}
	}
	if p := number(fisher["p_value"]); !(p > 0 && p <= 1) {
		t.Errorf("fisher p_value %v, want Fisher's exact p in (0, 1]", p)
	}
	// The table is [[4, 0], [1, 3]]: the interval's lower end stays a
	// number (R: fisher.test(matrix(c(4, 0, 1, 3), 2, byrow = TRUE))
	// $conf.int[1], refined past uniroot's tolerance, U36 E1-S8 —
	// test_fisher_exact.json zero_b).
	if lo := number(detail(fisher, "ci_low")); math.Abs(lo-0.5328644553110231)/0.5328644553110231 > 1e-10 {
		t.Errorf("fisher details.ci_low %v, want fisher.test's 0.5328644553110231", lo)
	}
	if r, p := number(pearson["statistic"]), number(pearson["p_value"]); r != 1 || p != 0 {
		t.Errorf("pearson: r %v p %v, want r = 1 and p = 0", r, p)
	}
	// Spearman's p at n = 8 untied is the exact permutation p, 2 / 8!
	// (R: cor.test(1:8, 2 * (1:8), method = "spearman")$p.value =
	// 4.9603174603174603e-05), not the t form's 0.
	if r, p := number(spearman["statistic"]), number(spearman["p_value"]); r != 1 || math.Abs(p-2.0/40320)/(2.0/40320) > 1e-12 {
		t.Errorf("spearman: r %v p %v, want r = 1 and p = 2/8!", r, p)
	}
	if p := number(bf["p_value"]); p != 0 || string(bf["reject_null"]) != "true" {
		t.Errorf("bf p_value %v reject_null %s, want p = 0 and a rejection", p, bf["reject_null"])
	}
}
