package processing

import (
	exprast "github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/types"
)

// twoPassPlan is the declared-order schedule processStreamingTwoPass
// drives. The buffered arm (applyAttributes) runs every attribute in
// DECLARED order over the filtered set, so attribute i sees the final
// per-row output of every attribute j < i — including a row-local
// formula feeding a two-pass z-score, or a two-pass output feeding a
// later two-pass. The streaming arm must reproduce that: a two-pass
// attribute's PrePass has to see the same per-row inputs Compute would.
//
// Each two-pass attribute t is assigned a PREPASS LAYER: the scan in
// which its PrePass runs. Its inputs must be fully available in that
// scan, so
//
//	avail(row-local j) = max(avail(d) for d in deps(j)), 0 if none
//	layer(two-pass t)  = max(avail(d) for d in deps(t)), 0 if none
//	avail(two-pass t)  = layer(t) + 1   (Row needs Finalize)
//
// Scans 0..layers-1 are prepass layers; scan `layers` emits every
// attribute in declared order and folds the aggregations. A request
// whose two-pass attributes read only source fields (directly or
// through row-locals of source fields) has one prepass layer — the
// historical two scans. Each dependent two-pass layer costs one more.
//
// Within prepass layer L only the attributes a layer-L PrePass actually
// depends on (transitively) are evaluated, walking declared order and
// interleaving each layer-L PrePass at its own position — so a PrePass
// observes exactly the record state the buffered arm's Compute would,
// duplicate labels included, and no attribute is evaluated against an
// input that has not been produced yet in this scan.
type twoPassPlan struct {
	twoPass []bool  // per attribute: drives PrePass/Finalize
	layer   []int   // per two-pass attribute: its prepass layer (-1 for row-local)
	needed  [][]int // per prepass layer: declared-order indices to evaluate (Row + Set)
	layers  int     // number of prepass layers
}

// layerCount reports the number of prepass scans the plan needs.
func (p twoPassPlan) layerCount() int { return p.layers }

// planTwoPassLayers plans attrs with each attribute's two-pass-ness
// read from the registry (built-in set + declared extensions). The
// runtime calls planTwoPassStages with the constructed computers'
// interface assertion instead; the two agree by probe validation.
func planTwoPassLayers(attrs []*types.Attribute, exts *ExtensionRegistry) twoPassPlan {
	tp := make([]bool, len(attrs))
	for i, a := range attrs {
		tp[i] = exts.attributeRequiresTwoPass(a.Type)
	}
	return planTwoPassStages(attrs, tp, exts)
}

func planTwoPassStages(attrs []*types.Attribute, twoPass []bool, exts *ExtensionRegistry) twoPassPlan {
	n := len(attrs)
	labels := make([]string, n)
	for i, a := range attrs {
		labels[i] = a.Label
		if labels[i] == "" {
			labels[i] = defaultAttributeLabel(a)
		}
	}
	deps := make([][]int, n)
	for i, a := range attrs {
		inputs, known := attributeInputNames(a, exts)
		for j := 0; j < i; j++ {
			if !known {
				deps[i] = append(deps[i], j)
				continue
			}
			if _, ok := inputs[labels[j]]; ok {
				deps[i] = append(deps[i], j)
			}
		}
	}

	plan := twoPassPlan{twoPass: twoPass, layer: make([]int, n)}
	avail := make([]int, n)
	for i := 0; i < n; i++ {
		m := 0
		for _, d := range deps[i] {
			if avail[d] > m {
				m = avail[d]
			}
		}
		if twoPass[i] {
			plan.layer[i] = m
			avail[i] = m + 1
			if m+1 > plan.layers {
				plan.layers = m + 1
			}
		} else {
			plan.layer[i] = -1
			avail[i] = m
		}
	}

	plan.needed = make([][]int, plan.layers)
	for l := 0; l < plan.layers; l++ {
		mark := make([]bool, n)
		var visit func(int)
		visit = func(j int) {
			for _, d := range deps[j] {
				if !mark[d] {
					mark[d] = true
					visit(d)
				}
			}
		}
		for i := 0; i < n; i++ {
			if twoPass[i] && plan.layer[i] == l {
				visit(i)
			}
		}
		for j := 0; j < n; j++ {
			if mark[j] {
				plan.needed[l] = append(plan.needed[l], j)
			}
		}
	}
	return plan
}

// attributeInputNames returns every name attr may read off a record —
// schema fields AND earlier attribute labels. known=false means the
// set cannot be proven complete (an unparsable formula, an extension
// without a FieldInputs hook), and the planner then treats attr as
// depending on every earlier attribute: correct, at the price of extra
// layers.
func attributeInputNames(attr *types.Attribute, exts *ExtensionRegistry) (map[string]struct{}, bool) {
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
	if attr.Type == types.ATTR_FORMULA && attr.Expression != "" {
		tree, err := exprparser.Parse(attr.Expression)
		if err != nil {
			return nil, false
		}
		exprast.Walk(&tree.Node, allIdentVisitor(out))
	}
	if exts != nil {
		if _, custom := exts.Attributes[attr.Type]; custom {
			inputs, ok := exts.FieldInputsFor("attribute", string(attr.Type), attr.Params)
			if !ok {
				return nil, false
			}
			for _, s := range inputs {
				add(s)
			}
		}
	}
	return out, true
}

// allIdentVisitor collects every identifier an expression names,
// whether or not it is a schema field (attribute labels are not).
type allIdentVisitor map[string]struct{}

func (v allIdentVisitor) Visit(node *exprast.Node) {
	if id, ok := (*node).(*exprast.IdentifierNode); ok {
		v[id.Value] = struct{}{}
	}
}
