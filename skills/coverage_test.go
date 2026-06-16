package skills

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
)

// embeddedSkillSet returns the set of embedded markdown stem names as a
// map for O(1) membership checks. Filesystem-driven so the gates stay
// valid as the skill pack grows or shrinks without touching the test
// surface.
func embeddedSkillSet(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := fs.ReadDir(content, ".")
	if err != nil {
		t.Fatalf("read embedded skills dir: %v", err)
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		out[strings.TrimSuffix(e.Name(), ".md")] = true
	}
	return out
}

// kebabName converts an identifier to its skill-file kebab form: lowercase
// with underscores rewritten to hyphens. Mirrors the helper used by
// atomic_test.go so the coverage gates and the operator-existence gate
// share one naming convention.
func kebabName(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}

func TestSkillsCoverAllComponents(t *testing.T) {
	embedded := embeddedSkillSet(t)

	for _, v := range types.AllAggregationTypes() {
		stem := "op-agg-" + kebabName(strings.TrimPrefix(string(v), "AGG_"))
		if !embedded[stem] {
			t.Errorf("aggregator %s: missing atomic skill skills/%s.md", v, stem)
		}
	}
	for _, v := range types.AllAttributeTypes() {
		stem := "op-attr-" + kebabName(strings.TrimPrefix(string(v), "ATTR_"))
		if !embedded[stem] {
			t.Errorf("attribute %s: missing atomic skill skills/%s.md", v, stem)
		}
	}
	for _, v := range types.AllFiltererTypes() {
		stem := "op-filter-" + kebabName(strings.TrimPrefix(string(v), "FILTER_"))
		if !embedded[stem] {
			t.Errorf("filterer %s: missing atomic skill skills/%s.md", v, stem)
		}
	}
	for _, v := range types.AllGroupTypes() {
		stem := "op-group-" + kebabName(strings.TrimPrefix(string(v), "GROUP_"))
		if !embedded[stem] {
			t.Errorf("grouper %s: missing atomic skill skills/%s.md", v, stem)
		}
	}
	for _, v := range types.AllFeatureTypes() {
		stem := "op-feat-" + kebabName(strings.TrimPrefix(string(v), "FEAT_"))
		if !embedded[stem] {
			t.Errorf("feature %s: missing atomic skill skills/%s.md", v, stem)
		}
	}
}

// TestSkillsCoverAllFieldTypes verifies every FieldType has a matching
// type-<kebab>.md atomic skill file.
func TestSkillsCoverAllFieldTypes(t *testing.T) {
	embedded := embeddedSkillSet(t)

	fieldTypes := []encoding.FieldType{
		encoding.FieldTypeU4,
		encoding.FieldTypeU8,
		encoding.FieldTypeU16,
		encoding.FieldTypeU32,
		encoding.FieldTypeU64,
		encoding.FieldTypeF32,
		encoding.FieldTypeF64,
		encoding.FieldTypeDate,
		encoding.FieldTypePackedBool,
		encoding.FieldTypeCategoricalU8,
		encoding.FieldTypeCategoricalU16,
		encoding.FieldTypeCategoricalU32,
		encoding.FieldTypeDecimal128,
		encoding.FieldTypeSetU8,
		encoding.FieldTypeSetU16,
		encoding.FieldTypeSetU32,
		encoding.FieldTypeSetU64,
	}
	for _, ft := range fieldTypes {
		stem := "type-" + kebabName(ft.String())
		if !embedded[stem] {
			t.Errorf("field type %s: missing atomic skill skills/%s.md", ft.String(), stem)
		}
	}
}

// TestSkillsCoverAllSynthDistributions verifies every distribution
// kind registered in synth.AllDistributions() has a matching
// op-synth-<kebab>.md atomic skill file.
func TestSkillsCoverAllSynthDistributions(t *testing.T) {
	embedded := embeddedSkillSet(t)
	for _, d := range synth.AllDistributions() {
		stem := "op-synth-" + kebabName(d)
		if !embedded[stem] {
			t.Errorf("synth distribution %q: missing atomic skill skills/%s.md", d, stem)
		}
	}
}

// TestSkillsCoverAllRegressions verifies every constant in
// types.AllRegressionTypes() has a matching op-reg-<kebab>.md atomic
// skill file.
func TestSkillsCoverAllRegressions(t *testing.T) {
	embedded := embeddedSkillSet(t)
	for _, r := range types.AllRegressionTypes() {
		stem := "op-reg-" + kebabName(strings.TrimPrefix(string(r), "REG_"))
		if !embedded[stem] {
			t.Errorf("regression %s: missing atomic skill skills/%s.md", r, stem)
		}
	}
}

// TestSkillsCoverAllWindowTypes verifies every constant in
// types.AllWindowTypes() has a matching op-win-<kebab>.md atomic skill
// file.
func TestSkillsCoverAllWindowTypes(t *testing.T) {
	embedded := embeddedSkillSet(t)
	for _, w := range types.AllWindowTypes() {
		stem := "op-win-" + kebabName(strings.TrimPrefix(string(w), "WIN_"))
		if !embedded[stem] {
			t.Errorf("window %s: missing atomic skill skills/%s.md", w, stem)
		}
	}
}

// TestSkillsCoverAllOverlayKinds verifies every constant in
// types.AllOverlayKinds() has a matching op-overlay-<kebab>.md atomic
// skill file. Mirrors the gate pattern used by the other operator
// coverage gates.
func TestSkillsCoverAllOverlayKinds(t *testing.T) {
	embedded := embeddedSkillSet(t)
	for _, k := range types.AllOverlayKinds() {
		stem := "op-overlay-" + kebabName(strings.TrimPrefix(string(k), "OVERLAY_"))
		if !embedded[stem] {
			t.Errorf("overlay kind %s: missing atomic skill skills/%s.md", k, stem)
		}
	}
}
