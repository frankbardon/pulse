package vectors

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// adstock_guard_test.go pins internal/model's schema-free overwrite
// guard on an adstock record (model.FeatureMayWrite /
// AttributeMayWrite) to the naming the engine computes: every output a
// built-in feature emits and every default attribute label must be one
// the guard says may write, or a column a slot overwrites could keep a
// window's adstock record.

// TestFeatureMayWrite_CoversOutputLabels: for every built-in feature
// type, labelled and not, each FeatureOutputLabels name is covered.
func TestFeatureMayWrite_CoversOutputLabels(t *testing.T) {
	t.Parallel()

	s := testSchema()
	field := map[types.FeatureType]string{types.FEAT_DATE_FEATURES: "ts", types.FEAT_ONE_HOT: "flag"}
	for _, typ := range types.AllFeatureTypes() {
		for _, label := range []string{"", "lbl"} {
			f := &types.Feature{Type: typ, Field: "q_2", Label: label, Params: json.RawMessage(`{"degree": 3}`)}
			if fd, ok := field[typ]; ok {
				f.Field = fd
			}
			outs := FeatureOutputLabels(f, s)
			if (typ == types.FEAT_ONE_HOT || typ == types.FEAT_DATE_FEATURES || typ == types.FEAT_POLY) && len(outs) == 0 {
				t.Fatalf("%s names no output over the test schema", typ)
			}
			for _, out := range outs {
				if !model.FeatureMayWrite(f, out) {
					t.Errorf("%s (label %q): output %q not covered by model.FeatureMayWrite", typ, label, out)
				}
			}
		}
	}
}

// TestAttributeMayWrite_CoversDefaultLabels: every attribute type's
// default label (DefaultAttributeLabel) is covered for an unlabelled
// slot, and a labelled slot writes exactly its label.
func TestAttributeMayWrite_CoversDefaultLabels(t *testing.T) {
	t.Parallel()

	for _, typ := range types.AllAttributeTypes() {
		a := &types.Attribute{Type: typ, Field: "q_2", Target: "q_1", Vector: "v", Fields: []string{"q_1", "q_2"},
			Params: json.RawMessage(`{"fit": "m"}`)}
		if l := DefaultAttributeLabel(a); !model.AttributeMayWrite(a, l) {
			t.Errorf("%s: default label %q not covered by model.AttributeMayWrite", typ, l)
		}
		a.Label = "lbl"
		if !model.AttributeMayWrite(a, "lbl") || model.AttributeMayWrite(a, string(typ)+"_q_2") {
			t.Errorf("%s: a labelled slot's guard is not exactly its label", typ)
		}
	}
}
