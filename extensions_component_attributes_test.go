package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

type emitRuleAttr struct{}

func (emitRuleAttr) Compute(rows extend.Rows, _ string) ([]float64, error) {
	return make([]float64, rows.Len()), nil
}

// TestExtensions_ComponentAttributesRuntimeMatchesSnapshot: whether an
// extension attribute emits Components.Attributes figures is ONE rule
// (attributeEmitsComponents) read by the runtime registry and the
// predict snapshot alike, so the merge gate's two halves cannot
// diverge — a ComponentSchema carrying only a Mergeability class (no
// keys, no emitter) emits on neither side.
func TestExtensions_ComponentAttributesRuntimeMatchesSnapshot(t *testing.T) {
	keys := []descriptor.ComponentKey{{Name: "n_x", Type: "int", Description: "x"}}
	emit := func(extend.AttributeComputer) (map[string]any, error) { return nil, nil }
	cases := []struct {
		name  string
		reg   AttributeRegistration
		emits bool
	}{
		{"none", AttributeRegistration{}, false},
		{"mergeability only", AttributeRegistration{ComponentSchema: descriptor.ComponentSchema{Mergeability: descriptor.Mergeable}}, false},
		{"keys only", AttributeRegistration{ComponentSchema: descriptor.ComponentSchema{Keys: keys, Mergeability: descriptor.Mergeable}}, true},
		{"func only", AttributeRegistration{ComponentsFunc: emit}, true},
		{"keys and func", AttributeRegistration{ComponentSchema: descriptor.ComponentSchema{Keys: keys}, ComponentsFunc: emit}, true},
	}
	var regs []AttributeRegistration
	for i, c := range cases {
		reg := c.reg
		reg.Name = types.AttributeType("ATTR_RULE_C" + string(rune('A'+i)))
		reg.Mode = AttributeModeRowLocal
		reg.Factory = func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) { return emitRuleAttr{}, nil }
		regs = append(regs, reg)
	}
	ext := Extensions{Attributes: regs}
	runtime, snap := buildRuntimeExtensions(ext), buildExtensionsSnapshot(ext)
	for i, c := range cases {
		name := regs[i].Name
		r, s := runtime.ComponentAttributes[name], snap.ComponentAttributes[string(name)]
		if r != s || r != c.emits {
			t.Errorf("%s: runtime emits=%v, snapshot emits=%v, want %v", c.name, r, s, c.emits)
		}
	}
}
