package main

import (
	"strings"
	"testing"
)

// fixtureCohortTree is a miniature tree with one site (and one near miss)
// per fixture-cohort rule. Cohorts: alpha, bravo, charlie, delta,
// foxtrot (echo has a CSV but no schema, so it is no cohort).
var fixtureCohortTree = func() map[string]string {
	t := map[string]string{
		"internal/examples/fixtures/echo.csv":        "x\n1\n",
		"internal/examples/fixtures/cmd/gen/main.go": "package main\n\nfunc main() {}\n",
		"internal/examples/fixtures/gen_test.go":     "package fixtures\n\nimport \"testing\"\n\nfunc TestFixtures_Regenerate(t *testing.T) {}\n",
		"internal/examples/fixtures/build.sh":        "#!/bin/sh\nfor cohort in alpha bravo charlie delta foxtrot echo; do\n  echo $cohort\ndone\n",
		"internal/examples/aggregations/01_sum.json": `{"cohort": {"filename": "alpha.pulse"}}`,
		"internal/integration/examples_test.go": `package integration

import "testing"

func buildExampleFixtures() {
	cohorts := []string{"alpha", "bravo", "charlie", "delta", "foxtrot"}
	_ = cohorts
}

func TestExamples_Run(t *testing.T) { buildExampleFixtures() }

func TestUnrelated(t *testing.T) {}

// near miss: names cohorts only in a comment — "alpha", "bravo", "charlie"
func helper() {}
`,
		"request_fuzz_test.go": `package pulse

import "testing"

var fuzzCohorts = []string{"alpha", "bravo", "charlie"}

func FuzzRequest(f *testing.F) { _ = fuzzCohorts }
`,
		"internal/examples/README.md": "# Examples\n\n## Available cohorts\n\n| File | Fields |\n|---|---|\n" +
			"| `alpha.pulse` | a |\n| `bravo.pulse` | b |\n| `charlie.pulse` | c |\n| `delta.pulse` | d |\n\n" +
			"## Catalog\n\n| File | What |\n|---|---|\n" +
			"| `01_x.json` | reads `alpha.pulse` |\n| `02_y.json` | reads `bravo.pulse` |\n| `03_z.json` | reads `charlie.pulse` |\n",
		// near miss: three bare words, and possessives, are prose, not a list
		"docs/prose.md": "# Prose\n\nthe alpha bravo charlie of it\nalpha's, bravo's and charlie's rows\n",
	}
	for _, c := range []string{"alpha", "bravo", "charlie", "delta", "foxtrot"} {
		t["internal/examples/fixtures/"+c+".csv"] = "x\n1\n"
		t["internal/examples/fixtures/schemas/"+c+".json"] = "[]\n"
	}
	return t
}()

func TestFixtureReport_Rules(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, fixtureCohortTree)
	if got := strings.Join(r.fixtureCohorts(), ","); got != "alpha,bravo,charlie,delta,foxtrot" {
		t.Fatalf("cohorts = %s (echo has no schema)", got)
	}

	rep, err := fixtureReport(r, "foxtrot", options{})
	if err != nil {
		t.Fatal(err)
	}
	lists := map[string]Site{}
	for _, sec := range rep.Sections {
		if sec.Class != "fixture-lists" {
			continue
		}
		for _, s := range sec.Sites {
			lists[s.Path+" "+s.Symbols[0].Name] = s
		}
	}
	want := map[string]string{
		"internal/integration/examples_test.go func buildExampleFixtures":               "complete",
		"internal/examples/fixtures/build.sh for cohort in alpha bravo charlie delta …": "complete",
		"internal/examples/README.md ## Available cohorts":                              "complete",
		"request_fuzz_test.go var fuzzCohorts":                                          "subset",
	}
	if len(lists) != len(want) {
		t.Errorf("lists = %v, want exactly %v (no comment, prose or catalog-table list)", keys(lists), want)
	}
	for k, cov := range want {
		if s, ok := lists[k]; !ok || s.Coverage != cov {
			t.Errorf("list %q = %+v, want coverage %s", k, s, cov)
		}
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "internal/examples/README.md") {
		t.Errorf("warnings = %v, want the README table missing foxtrot", rep.Warnings)
	}

	gates := map[string]string{}
	for _, g := range rep.Gates {
		gates[g.Package] += strings.Join(g.Tests, ",")
	}
	for pkg, w := range map[string]string{
		"internal/integration":       "TestExamples_Run",
		".":                          "FuzzRequest",
		"internal/examples/fixtures": "TestFixtures_Regenerate",
	} {
		if gates[pkg] != w {
			t.Errorf("gates in %s = %q, want %q (TestUnrelated reads no list)", pkg, gates[pkg], w)
		}
	}
	if len(rep.Regenerate) != 1 || rep.Regenerate[0].Command != "go run ./internal/examples/fixtures/cmd/gen" {
		t.Errorf("regenerate = %+v", rep.Regenerate)
	}

	// An existing cohort lists its readers; a new one its files to create.
	rep, _ = fixtureReport(r, "alpha", options{})
	var readers []string
	for _, sec := range rep.Sections {
		if sec.Class == "fixture-uses" {
			for _, s := range sec.Sites {
				readers = append(readers, s.Path)
			}
		}
	}
	if strings.Join(readers, ",") != "internal/examples/aggregations/01_sum.json" {
		t.Errorf("alpha readers = %v (list sites are not readers)", readers)
	}
	rep, _ = fixtureReport(r, "golf", options{})
	if rep.Sections[0].Sites[0].Note != "create" || len(rep.Warnings) != 3 {
		t.Errorf("new cohort: files %+v, warnings %v (every complete list misses it)", rep.Sections[0].Sites, rep.Warnings)
	}
	if _, err := fixtureReport(r, "alpha.csv", options{}); err == nil {
		t.Error("a file name is not a cohort stem")
	}
}

func keys(m map[string]Site) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
