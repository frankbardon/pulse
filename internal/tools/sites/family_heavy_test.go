//go:build heavy

package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestFamilyReports_KnownSites pins the real tree's sites of each family
// mode, including the ones found by grep during the pre-v1 effort before
// the modes existed (decimal cell readers, the facet field-type rule, the
// grouper key formatter).
func TestFamilyReports_KnownSites(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	type want struct{ class, key string }
	for _, tc := range []struct {
		mode, name string
		want       []want
	}{
		{"field-type", "decimal128", []want{
			{"family-tables", "internal/descriptor/capabilities_aggregators.go var allCohortFieldTypes"},
			{"field-type-reach", "internal/processing/decimal_cells.go func decimalCellFloat"},
			{"field-type-reach", "internal/processing/test_post.go func toFloat64"},
			{"field-type-reach", "internal/processing/test_tukey.go func floatValue"},
			{"field-type-reach", "internal/processing/overlay.go func scalarFromCell"},
			{"field-type-reach", "internal/processing/crosstab.go func coerceFloat64"},
			{"field-type-reach", "internal/processing/chain.go func coerceFloat"},
			{"field-type-reach", "internal/processing/overlay_index_vs_stage.go func coerceNumericValue"},
			{"field-type-reach", "internal/descriptor/facet.go func FacetFieldTypeRefusal"},
			{"field-type-reach", "internal/descriptor/facet.go func validateFacetWithOptions"},
			{"field-type-reach", "internal/service/facet_rich.go func validateFacetSchemaRequest"},
			{"field-type-formatters", "internal/processing/grouper.go func formatRoundedNumeric"},
		}},
		{"error-code", "PULSE_SLOT_FAILED", []want{
			{"family-tables", "errors/fixup_metadata.go var codeMetadata"},
			{"family-tables", "internal/descriptor/error_owners.go var errorOwners"},
			{"family-tables", "errors/codes.go var allCodes"},
		}},
		{"mcp-tool", "inspect", []want{
			{"family-tables", "internal/mcp/toolmeta/meta.go func Meta"},
			{"family-tables", "internal/descriptor/features.go var mcpToolBindings"},
			{"skill-atomic", "internal/skills/tool-inspect.md"},
		}},
		{"io", "csv", []want{
			{"family-tables", "io/format.go var formats"},
			{"io-adapter", "internal/io/csv/csv.go"},
		}},
		{"io", "SchemaAwareReader", []want{
			{"io-implementers", "internal/io/spss/ type Reader"},
			{"io-pipelines", "internal/io/import.go func readerSchema"},
		}},
		{"io", "WidthHintReader", []want{ // a NEW hook
			{"family-norm", "io/facade.go"},
			{"family-norm", "internal/io/core.go"},
			{"io-implementers", "internal/io/csv/ type Reader"},
			{"io-pipelines", "internal/io/import.go func readerSchema"},
		}},
	} {
		rep, err := familyModes[tc.mode](r, tc.name, options{All: tc.mode == "mcp-tool"})
		if err != nil {
			t.Fatalf("%s %s: %v", tc.mode, tc.name, err)
		}
		for _, w := range tc.want {
			got := sectionSites(rep, w.class)
			if w.class == "skill-atomic" {
				if findSite(rep, w.key) == nil {
					t.Errorf("%s %s: no site %s", tc.mode, tc.name, w.key)
				}
				continue
			}
			if !hasKey(got, w.key) {
				t.Errorf("%s %s: %s lacks %s (got %d sites)", tc.mode, tc.name, w.class, w.key, len(got))
			}
		}
	}
	// a NEW member joins every complete table
	rep, err := errorCodeReport(r, "PULSE_SITES_PROBE_NEW", options{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(sectionSites(rep, "family-tables")); n < 4 {
		t.Errorf("new code: %d complete tables, want codeMetadata, errorOwners, allCodes and the const block", n)
	}
}

// TestOpReport_ShapeSites pins the op sites found by shape on the real
// tree: the oracle suite a new fixture's test joins and the MCP book
// page's bound-enum catalogue (whose OVERLAY_* and FEAT_* rows the
// catalogue rule found missing).
func TestOpReport_ShapeSites(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	rep, err := opReport(r, "OVERLAY_CHISQ_VS_REF", options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasKey(sectionSites(rep, "oracle-suites"), "internal/processing/overlay_reference_oracle_test.go") {
		t.Errorf("oracle suites = %v", sectionSites(rep, "oracle-suites"))
	}
	cat := sectionSites(rep, "catalogues")
	if n, ok := cat["docs/src/mcp/index.md ## Schema-bound enums"]; !ok || strings.Contains(n, "NO ") {
		t.Errorf("catalogue = %v, want the bound-enum table with an OVERLAY_* row", cat)
	}
	for _, w := range rep.Warnings {
		if strings.Contains(w, "has rows for") {
			t.Errorf("catalogue warning: %s", w)
		}
	}
}

// TestSlotReport_Options pins the options root: a field of the facade's
// ComposeOptions alias resolves to service.ComposeOptions, and the CLI's
// composite literal (through the pulse alias) is a site.
func TestSlotReport_Options(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	for _, which := range []string{"options", "compose"} {
		rep, err := slotReport(r, which, "ComposeOptions.CollectErrors", options{})
		if err != nil {
			t.Fatal(err)
		}
		if rep.Target != "service.ComposeOptions.CollectErrors" {
			t.Errorf("%s: target %s", which, rep.Target)
		}
		if s := findSite(rep, "internal/cli/api.go"); !hasSymbol(s, "func apiComposeCmd") {
			t.Errorf("%s: the CLI literal is not a site: %+v", which, s)
		}
		if s := findSite(rep, "internal/service/compose_parallel.go"); s == nil {
			t.Errorf("%s: the reader is not a site", which)
		}
	}
	var out, errb bytes.Buffer
	for _, args := range [][]string{{"field-type", "decimal128"}, {"error-code", "PULSE_SLOT_FAILED"}, {"mcp-tool", "inspect"}, {"io", "csv"}, {"slot", "options", "ComposeOptions"}} {
		out.Reset()
		if code := run(args, &out, &errb); code != 0 || !strings.Contains(out.String(), "## Gates to rerun") {
			t.Errorf("run(%v) = %d: %s", args, code, errb.String())
		}
	}
	if code := run([]string{"field-type"}, &out, &errb); code != 2 {
		t.Errorf("field-type without a name: exit %d, want 2", code)
	}
}

// TestSlotReport_EmbedderSmoke: slot mode type-checks the nested
// embedder smoke module too, so its uses of a slot are sites — the
// compile-time surface pin of ComposeOptions.MaxWorkers (a non-test
// file) and the flow test setting it — and that test's rerun command
// runs from the nested module.
func TestSlotReport_EmbedderSmoke(t *testing.T) {
	t.Parallel()

	rep, err := slotReport(sharedRepo(t), "options", "ComposeOptions.MaxWorkers", options{})
	if err != nil {
		t.Fatal(err)
	}
	if s := findSite(rep, "internal/embeddersmoke/surface.go"); s == nil {
		t.Error("the smoke module's surface pin is not a site")
	}
	var gate *Gate
	for i := range rep.Gates {
		if rep.Gates[i].Package == embedderSmokeDir {
			gate = &rep.Gates[i]
		}
	}
	if gate == nil || !contains(gate.Tests, "TestQueryProcessAndCompose") ||
		!strings.HasPrefix(gate.Command, "cd "+embedderSmokeDir+" && ") {
		t.Errorf("no smoke-module rerun gate naming TestQueryProcessAndCompose: %+v", gate)
	}
}
