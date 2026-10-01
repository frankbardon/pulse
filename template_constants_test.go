package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/internal/template"
)

// rootTemplateTargets is every root TemplateTarget constant. A new internal
// target without a root spelling fails TestTemplateConstants_RootSetComplete.
var rootTemplateTargets = map[TemplateTarget]template.Target{
	TemplateTargetRequest:  template.TargetRequest,
	TemplateTargetComposed: template.TargetComposed,
	TemplateTargetChain:    template.TargetChain,
	TemplateTargetFacet:    template.TargetFacet,
	TemplateTargetSample:   template.TargetSample,
}

// rootTemplateVarTypes is every root TemplateVarType constant.
var rootTemplateVarTypes = map[TemplateVarType]template.VarType{
	TemplateVarString:  template.VarString,
	TemplateVarNumber:  template.VarNumber,
	TemplateVarInteger: template.VarInteger,
	TemplateVarBoolean: template.VarBoolean,
	TemplateVarField:   template.VarField,
	TemplateVarEnum:    template.VarEnum,
	TemplateVarList:    template.VarList,
	TemplateVarDate:    template.VarDate,
	TemplateVarPeriod:  template.VarPeriod,
}

func TestTemplateConstants_EqualInternal(t *testing.T) {
	for root, internal := range rootTemplateTargets {
		if root != internal {
			t.Errorf("root target %q != internal %q", root, internal)
		}
		if !root.Valid() {
			t.Errorf("root target %q is not Valid()", root)
		}
	}
	for root, internal := range rootTemplateVarTypes {
		if root != internal {
			t.Errorf("root var type %q != internal %q", root, internal)
		}
		if !root.Valid() {
			t.Errorf("root var type %q is not Valid()", root)
		}
	}
}

func TestTemplateConstants_RootSetComplete(t *testing.T) {
	targets := template.AllTargets()
	if len(targets) != len(rootTemplateTargets) {
		t.Errorf("internal has %d targets, root spells %d", len(targets), len(rootTemplateTargets))
	}
	for _, it := range targets {
		if _, ok := rootTemplateTargets[it]; !ok {
			t.Errorf("internal target %q has no root TemplateTarget* constant", it)
		}
	}
	varTypes := template.AllVarTypes()
	if len(varTypes) != len(rootTemplateVarTypes) {
		t.Errorf("internal has %d var types, root spells %d", len(varTypes), len(rootTemplateVarTypes))
	}
	for _, iv := range varTypes {
		if _, ok := rootTemplateVarTypes[iv]; !ok {
			t.Errorf("internal var type %q has no root TemplateVar* constant", iv)
		}
	}
}
