package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// slotListColumnKeys are the list-valued slot keys (Param.Slot) that
// name record columns. A slot key is a field of the slot struct, so the
// projection probe (processing's TestManifestNamesColumnMatchesProjection,
// which sets params keys) cannot reach it; its column-naming entries
// are held here by name. Type=="field" slot keys are held by the type
// rule below.
var slotListColumnKeys = map[string]bool{
	"predictors":  true, // ATTR_REG_*, REG_*: the predictor columns
	"unpenalized": true, // REG_OLS: a subset of the predictors
}

// TestCapabilities_FieldParamsNameColumns holds the shape of the
// manifest's Param.NamesColumn flag on every declared param:
//
//   - every Type=="field" entry carries it, so "names a column" is one
//     flag to read and never a union of a type and a flag;
//   - it sits only on a "string", "list" or "field" value — no other
//     type can hold a column name;
//   - the list-valued slot keys naming columns (slotListColumnKeys)
//     carry it;
//   - no post test or overlay param carries it: those name RESULT
//     columns (n_col, an overlay's field, partition_by), resolved over
//     the output rows, never a record column a request reads.
//
// Which params keys name columns is held against the runtime by
// processing's TestManifestNamesColumnMatchesProjection.
func TestCapabilities_FieldParamsNameColumns(t *testing.T) {
	t.Parallel()

	m := BuildManifest()
	marked := 0
	check := func(section, op string, ps []descriptor.Param) {
		for _, p := range ps {
			if p.NamesColumn {
				marked++
			}
			switch {
			case p.Type == "field" && !p.NamesColumn:
				t.Errorf("%s %s param %q is Type field without NamesColumn", section, op, p.Name)
			case p.NamesColumn && p.Type != "field" && p.Type != "string" && p.Type != "list":
				t.Errorf("%s %s param %q carries NamesColumn on Type %q; only string, list and field values name columns", section, op, p.Name, p.Type)
			case p.Slot && slotListColumnKeys[p.Name] && !p.NamesColumn:
				t.Errorf("%s %s slot key %q names columns but lacks NamesColumn", section, op, p.Name)
			}
		}
	}
	for _, g := range []struct {
		section string
		ops     []descriptor.Operator
	}{
		{"aggregator", m.Components.Aggregators},
		{"attribute", m.Components.Attributes},
		{"filterer", m.Components.Filterers},
		{"grouper", m.Components.Groupers},
		{"window", m.Components.Windows},
		{"feature", m.Components.Features},
	} {
		for _, o := range g.ops {
			check(g.section, o.Name, o.Params)
		}
	}
	for _, o := range m.Tests {
		check("test", o.Name, o.Params)
	}
	for _, o := range m.Regressions {
		check("regression", o.Name, o.Params)
	}
	for _, o := range m.Matrices {
		check("matrix", o.Name, o.Params)
	}
	for _, o := range m.PostTests {
		for _, p := range o.Params {
			if p.NamesColumn {
				t.Errorf("post test %s param %q carries NamesColumn; a post test reads result rows, not record columns", o.Name, p.Name)
			}
		}
	}
	for _, o := range m.Overlays {
		for _, p := range o.Params {
			if p.NamesColumn {
				t.Errorf("overlay %s param %q carries NamesColumn; an overlay reads result cells, not record columns", o.Kind, p.Name)
			}
		}
	}
	if marked == 0 {
		t.Fatal("no param carries NamesColumn; the manifest walk is broken")
	}
}
