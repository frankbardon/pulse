package skills

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestSkillsCoverAllComponents verifies that every aggregator, attribute,
// filterer, and grouper registry entry has a heading in its target skill.
func TestSkillsCoverAllComponents(t *testing.T) {
	aggContent, ok := Get("aggregation-guide")
	if !ok {
		t.Fatal("aggregation-guide.md not found")
	}
	for _, agg := range types.AllAggregationTypes() {
		if !strings.Contains(aggContent, string(agg)) {
			t.Errorf("aggregation-guide.md does not mention aggregator %s", agg)
		}
	}

	attrContent, ok := Get("attribute-composition")
	if !ok {
		t.Fatal("attribute-composition.md not found")
	}
	for _, attr := range types.AllAttributeTypes() {
		if !strings.Contains(attrContent, string(attr)) {
			t.Errorf("attribute-composition.md does not mention attribute %s", attr)
		}
	}

	// Filterers should be mentioned across skills; check in getting-started or compose
	// Actually, filterers are referenced in multiple skills. Check that each one appears
	// in at least one skill.
	allContent := collectAllSkillContent(t)
	for _, f := range types.AllFiltererTypes() {
		if !strings.Contains(allContent, string(f)) {
			t.Errorf("no skill mentions filterer %s", f)
		}
	}

	grouperContent, ok := Get("grouper-design")
	if !ok {
		t.Fatal("grouper-design.md not found")
	}
	for _, g := range types.AllGroupTypes() {
		if !strings.Contains(grouperContent, string(g)) {
			t.Errorf("grouper-design.md does not mention grouper %s", g)
		}
	}
}

// TestSkillsCoverAllErrorCodes verifies that every constant in errors/codes.go
// appears in error-code-reference.md.
func TestSkillsCoverAllErrorCodes(t *testing.T) {
	content, ok := Get("error-code-reference")
	if !ok {
		t.Fatal("error-code-reference.md not found")
	}
	for _, code := range errors.AllCodes() {
		if !strings.Contains(content, string(code)) {
			t.Errorf("error-code-reference.md does not mention error code %s", code)
		}
	}
}

// TestSkillsCoverAllCliLeaves verifies that every user-facing CLI leaf
// appears in getting-started.md.
func TestSkillsCoverAllCliLeaves(t *testing.T) {
	content, ok := Get("getting-started")
	if !ok {
		t.Fatal("getting-started.md not found")
	}

	// These are the CLI leaf commands from descriptor/manifest.go commands().
	leaves := []string{
		"process",
		"compose",
		"sample",
		"facet",
		"inspect",
		"predict",
		"manifest",
	}

	for _, leaf := range leaves {
		// Check for the leaf as a heading or backtick-quoted reference
		if !strings.Contains(content, leaf) {
			t.Errorf("getting-started.md does not mention CLI leaf %q", leaf)
		}
	}
}

// TestSkillsCoverAllFieldTypes verifies that every FieldType constant
// appears in cohort-schema-design.md.
func TestSkillsCoverAllFieldTypes(t *testing.T) {
	content, ok := Get("cohort-schema-design")
	if !ok {
		t.Fatal("cohort-schema-design.md not found")
	}

	// All 15 field types from encoding/field_type.go
	fieldTypes := []encoding.FieldType{
		encoding.FieldTypeU8,
		encoding.FieldTypeU16,
		encoding.FieldTypeU32,
		encoding.FieldTypeU64,
		encoding.FieldTypeF32,
		encoding.FieldTypeF64,
		encoding.FieldTypeNullableBool,
		encoding.FieldTypeNullableU4,
		encoding.FieldTypeNullableU8,
		encoding.FieldTypeNullableU16,
		encoding.FieldTypeDate,
		encoding.FieldTypePackedBool,
		encoding.FieldTypeCategoricalU8,
		encoding.FieldTypeCategoricalU16,
		encoding.FieldTypeCategoricalU32,
	}

	for _, ft := range fieldTypes {
		name := ft.String()
		if !strings.Contains(content, name) {
			t.Errorf("cohort-schema-design.md does not mention field type %s", name)
		}
	}
}

// TestSkillsCoverAllWindowTypes verifies that every constant in
// types.AllWindowTypes appears in window-operations.md.
func TestSkillsCoverAllWindowTypes(t *testing.T) {
	content, ok := Get("window-operations")
	if !ok {
		t.Fatal("window-operations.md not found")
	}
	for _, w := range types.AllWindowTypes() {
		if !strings.Contains(content, string(w)) {
			t.Errorf("window-operations.md does not mention window type %s", w)
		}
	}
}

// collectAllSkillContent returns the concatenated content of all skills.
func collectAllSkillContent(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	for _, m := range List() {
		content, ok := Get(m.Name)
		if !ok {
			t.Errorf("skill %q not found", m.Name)
			continue
		}
		sb.WriteString(content)
		sb.WriteString("\n")
	}
	return sb.String()
}
