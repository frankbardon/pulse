package main

import (
	"strings"
	"testing"
)

var opExtrasTree = map[string]string{
	"internal/opconfig/opconfig.go": `package opconfig

// DefaultQuantileBuckets is GROUP_QUANTILE's bucket count.
const DefaultQuantileBuckets = 4
`,
	"internal/vectors/rules.go": `package vectors

import "github.com/frankbardon/pulse/types"

func estimate(g *types.Group) int {
	switch g.Type {
	case types.GROUP_QUANTILE:
		bins := int(g.Interval)
		if bins <= 0 {
			bins = 4
		}
		return bins
	case types.GROUP_CATEGORY:
		return 4 // near miss: another operator's branch
	}
	if g.Type == types.GROUP_QUANTILE && g.Interval == 0 {
		return 4
	}
	return 0
}
`,
	"internal/processing/overlay_reference_oracle_test.go": `package processing

import "testing"

func TestReferenceOracle_OverlayChiSqVsPop(t *testing.T) { _ = "OVERLAY_CHISQ_VS_POP" }
func TestReferenceOracle_OverlayChiSqVsRef(t *testing.T) { _ = "OVERLAY_CHISQ_VS_REF" }
`,
	"internal/processing/fixture_reader_oracle_test.go": `package processing

import "testing"

func TestReferenceOracle_Markov(t *testing.T) { _ = loadReference(t, "markov") }
func TestReferenceOracle_Rake(t *testing.T)   { _ = loadReference(t, "rake") }
`,
	"internal/processing/lone_oracle_test.go": `package processing

import "testing"

// near miss: one oracle test is no suite
func TestReferenceOracle_Symmetry(t *testing.T) { _ = "OVERLAY_CHISQ_VS_REF" }
`,
	"docs/src/mcp/index.md": "# MCP\n\n## Schema-bound enums\n\n| Path | Enum |\n|---|---|\n" +
		"| `aggregations[].type` | `AGG_*` |\n| `groups[].type` | `GROUP_*` |\n" +
		"<!-- feature: capability:matrices -->\n| `matrices[].type` | `MAT_*` |\n<!-- /feature -->\n" +
		"| `tests[].type` | `TEST_*` |\n| `windows[].type` | `WIN_*` |\n| `attributes[].type` | `ATTR_*` |\n" +
		"| `filterers[].type` | `FILTER_*` |\n| `regressions[].type` | `REG_*` |\n",
	"README.md": "# Pulse\n\n| Skill | What |\n|---|---|\n| a | `AGG_*` |\n| b | `WIN_*` |\n| c | `REG_*` |\n",
}

func TestDefaultCopySection_Rules(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, opExtrasTree)
	op := operator{Name: "GROUP_QUANTILE"}
	sec, ok := r.defaultCopySection(op, []string{"internal/opconfig/opconfig.go", "internal/vectors/rules.go"})
	if !ok || len(sec.Sites) != 1 || len(sec.Sites[0].Symbols) != 1 {
		t.Fatalf("default copies = %+v", sec)
	}
	sym := sec.Sites[0].Symbols[0]
	if sec.Sites[0].Path != "internal/vectors/rules.go" || sym.Name != "func estimate" ||
		len(sym.Lines) != 2 || sym.Lines[0] != 10 || sym.Lines[1] != 17 || !strings.Contains(sym.Note, "DefaultQuantileBuckets") {
		t.Errorf("copy = %+v, want L10 (case) and L17 (if), never GROUP_CATEGORY's 4", sec.Sites[0])
	}
}

func TestOracleSuiteSection_Rules(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, opExtrasTree)
	op := operator{Name: "OVERLAY_CHISQ_VS_REF"}
	files := map[string][]hit{
		"internal/processing/overlay_reference_oracle_test.go": {{Op: 0}},
		"internal/processing/lone_oracle_test.go":              {{Op: 0}},
	}
	sec, gates, ok := r.oracleSuiteSection(op, files, []string{"internal/processing/testdata/reference/rake.json"})
	if !ok {
		t.Fatal("no suites")
	}
	var got []string
	for _, s := range sec.Sites {
		got = append(got, s.Path)
	}
	if strings.Join(got, ",") != "internal/processing/fixture_reader_oracle_test.go,internal/processing/overlay_reference_oracle_test.go" {
		t.Errorf("suites = %v (one oracle test is no suite)", got)
	}
	if len(gates) != 1 || len(gates[0].Tests) != 4 {
		t.Errorf("gates = %+v", gates)
	}
}

func TestCatalogueSection_Rules(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, opExtrasTree)
	for _, tc := range []struct {
		prefix string
		warn   bool
	}{{"MAT_", false}, {"OVERLAY_", true}} {
		def := categoryDef{Prefix: tc.prefix}
		sec, warns, ok := r.catalogueSection(&category{categoryDef: def})
		if !ok {
			t.Fatalf("%s: no catalogue", tc.prefix)
		}
		got := sectionSites(&Report{Sections: []Section{sec}}, "catalogues")
		if !hasKey(got, "docs/src/mcp/index.md ## Schema-bound enums") || !hasKey(got, "README.md # Pulse") {
			t.Errorf("%s: catalogues = %v", tc.prefix, got)
		}
		if (len(warns) == 1) != tc.warn || tc.warn && !strings.Contains(warns[0], "docs/src/mcp/index.md") {
			t.Errorf("%s: warnings = %v (the fenced MAT_* row continues the table; README's 3/10 is partial)", tc.prefix, warns)
		}
	}
}

var optionsTree = map[string]string{
	"pulse.go": `package pulse

import "github.com/frankbardon/pulse/internal/service"

type Options struct {
	DataDir string
	Workers int
}

type ComposeOptions = service.ComposeOptions

// near miss: not an options type
type Pulse struct{ opts Options }
`,
	"internal/service/compose_parallel.go": `package service

type ComposeOptions struct {
	MaxWorkers    int
	CollectErrors bool
}
`,
}

func TestResolveOptions(t *testing.T) {
	t.Parallel()
	r := fixtureRepo(t, optionsTree)
	for _, tc := range []struct{ name, key, field string }{
		{"ComposeOptions.CollectErrors", modulePath + "/internal/service.ComposeOptions", "CollectErrors"},
		{"ComposeOptions.collecterrors", modulePath + "/internal/service.ComposeOptions", "CollectErrors"},
		{"ComposeOptions", modulePath + "/internal/service.ComposeOptions", ""},
		{"ComposeOptions.NewKnob", modulePath + "/internal/service.ComposeOptions", ""},
		{"Options.Workers", modulePath + ".Options", "Workers"},
	} {
		key, owners, err := r.resolveOptions(tc.name)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		field := ""
		if len(owners) == 1 {
			field = owners[0].Field.Name
		}
		if key != tc.key || field != tc.field {
			t.Errorf("%s = %s / %q, want %s / %q", tc.name, key, field, tc.key, tc.field)
		}
	}
	if _, _, err := r.resolveOptions("Pulse"); err == nil {
		t.Error("Pulse is no options struct")
	}
	if got := displayStruct(modulePath + "/internal/service.ComposeOptions"); got != "service.ComposeOptions" {
		t.Errorf("display = %s", got)
	}
	if got := displayStruct(modulePath + ".Options"); got != "pulse.Options" {
		t.Errorf("display = %s", got)
	}
}
