package vectors

import (
	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// DefaultAttributeLabel is the output label of an attribute slot that
// spells none: a vector-input attribute's (VectorAttributeLabel), a
// fit-referenced ATTR_REG_FITTED / _RESIDUAL / _LEVERAGE's
// (model.DefaultFitLabel), an ATTR_REG_PREDICT's
// (model.DefaultScoreLabel), else "<TYPE>_<field>". The one rule the
// engine's attribute arms (processing.DefaultAttributeLabel) and the
// merge gate's total() layering (internal/mergegate) both read, so the
// gate matches a formula's identifiers against the labels the engine
// writes.
func DefaultAttributeLabel(attr *types.Attribute) string {
	if label, ok := VectorAttributeLabel(attr.Type, attr); ok {
		return label
	}
	switch attr.Type {
	case types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
		if model.FitReferenced(attr.Type, attr) {
			return model.DefaultFitLabel(attr)
		}
		return string(attr.Type) + "_" + attr.Target
	case types.ATTR_REG_PREDICT:
		return model.DefaultScoreLabel(attr)
	}
	return string(attr.Type) + "_" + attr.Field
}

// AttributeInputNames returns every name a BUILT-IN attribute slot may
// read off a record — schema fields AND earlier attribute labels: its
// field, target, predictors, vector members, the columns of its
// params.fit_on filterers and every identifier of an ATTR_FORMULA
// expression. known=false means the set cannot be proven complete (an
// ATTR_REG_PREDICT or fit-referenced slot, whose inputs are the bound
// model's predictors, an ATTR_SEM_SCORE bound to a SEM fit, whose group
// may be an earlier label, or an expression that does not parse); a caller
// then treats the slot as depending on every earlier attribute. An
// extension attribute's declared FieldInputs are the caller's to add
// (processing's two-pass planner does; the merge gate, which cannot
// see them, treats an extension as unknown).
func AttributeInputNames(attr *types.Attribute) (map[string]struct{}, bool) {
	if attr.Type == types.ATTR_REG_PREDICT || model.FitReferenced(attr.Type, attr) {
		return nil, false
	}
	if attr.Type == types.ATTR_SEM_SCORE && AttributeFit(attr.Type, attr) != "" {
		// A bound SEM fit's group may be a column its request computed,
		// which the scoring request computes too — an earlier label the
		// params do not name.
		return nil, false
	}
	out := make(map[string]struct{}, 2+len(attr.Predictors))
	add := func(s string) {
		if s != "" {
			out[s] = struct{}{}
		}
	}
	add(attr.Field)
	add(attr.Target)
	for _, p := range attr.Predictors {
		add(p)
	}
	// A vector-input attribute reads its members (source columns, so
	// never an earlier label — added for completeness) and the columns
	// of its params.fit_on filterers, which may be earlier labels.
	for _, f := range attr.Fields {
		add(f)
	}
	for _, f := range AttributeFitOn(attr.Type, attr) {
		add(f.Field)
		if f.Type == types.FILTER_EXPRESSION && f.Expression != "" {
			if !walkIdents(f.Expression, out) {
				return nil, false
			}
		}
	}
	// An ATTR_SEM_SCORE params.group may be an earlier label (a derived
	// group): its fit and scoring read it off the record.
	add(SEMScoreGroupField(attr.Type, attr))
	if attr.Type == types.ATTR_FORMULA && attr.Expression != "" {
		if !walkIdents(attr.Expression, out) {
			return nil, false
		}
	}
	return out, true
}

// walkIdents adds every identifier src names to out; false when src
// does not parse.
func walkIdents(src string, out map[string]struct{}) bool {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return false
	}
	exprast.Walk(&tree.Node, identCollector(out))
	return true
}

// identCollector collects every identifier an expression names,
// whether or not it is a schema field (attribute labels are not).
type identCollector map[string]struct{}

func (v identCollector) Visit(node *exprast.Node) {
	if id, ok := (*node).(*exprast.IdentifierNode); ok {
		v[id.Value] = struct{}{}
	}
}
