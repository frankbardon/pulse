package processing

import "github.com/frankbardon/pulse/internal/returnplan"

// ComputePlan says which excludable response parts a run COMPUTES. It
// supersedes the single Components opt-out bool: the service resolves
// one per request before dispatch (the request's `return` selection,
// the DisableComponents gate and the veto set folded together) and
// every execution arm — serial streaming / buffered, crosstab buffered
// / fused, the parallel-decode and shard reducers — consults it at its
// build points. A part a field turns off is never built, so it is
// absent from the response; the facade still prunes the wire through
// the same `return` plan, so a part computed under a veto is pruned at
// serialization exactly as before.
//
// The plan never changes WHICH arm runs: dispatch (the merge gate,
// canStream, crosstab fusion) reads the original Request, and the skip
// happens inside the chosen arm.
//
// Every field is "compute this"; the zero value computes nothing, so
// start from FullComputePlan.
type ComputePlan struct {
	// Components sub-parts (Response.Components.*).
	//
	// Aggs is components.aggregations: the slot floors and operator
	// maps. Groups is components.aggregations[*].groups — the grouped
	// run's per-bucket floor tallies and Components() builds; it
	// implies Aggs.
	Aggs   bool
	Groups bool
	// Groupers, Filterers and Run are components.groupers /
	// components.filterers / components.run.
	Groupers  bool
	Filterers bool
	Run       bool
	// Crosstab is components.crosstab; AuxMargins its
	// `*_margin_aggregations` figures (implies Crosstab).
	Crosstab   bool
	AuxMargins bool
	// Matrices is components.matrices.
	Matrices bool

	// Whole-slot parts outside Components. Carried for the stories that
	// wire them (matrices, overlays, tests, regressions); the service
	// keeps them on until their veto rules land.
	MatricesSlot bool
	Overlays     bool
	Tests        bool
	PostTests    bool
	Regressions  bool
}

// FullComputePlan computes every part — the plan of a request without
// a `return` block on an engine whose Components gate is open, and the
// default of every Processor.
func FullComputePlan() ComputePlan {
	return ComputePlan{
		Aggs: true, Groups: true, Groupers: true, Filterers: true, Run: true,
		Crosstab: true, AuxMargins: true, Matrices: true,
		MatricesSlot: true, Overlays: true, Tests: true, PostTests: true, Regressions: true,
	}
}

// WithoutComponents turns every Components sub-part off (the closed
// DisableComponents gate) and leaves the rest of the plan alone.
func (c ComputePlan) WithoutComponents() ComputePlan {
	c.Aggs, c.Groups, c.Groupers, c.Filterers, c.Run = false, false, false, false, false
	c.Crosstab, c.AuxMargins, c.Matrices = false, false, false
	return c
}

// WithComponentsOf copies every Components sub-part flag from o.
func (c ComputePlan) WithComponentsOf(o ComputePlan) ComputePlan {
	c.Aggs, c.Groups, c.Groupers, c.Filterers, c.Run = o.Aggs, o.Groups, o.Groupers, o.Filterers, o.Run
	c.Crosstab, c.AuxMargins, c.Matrices = o.Crosstab, o.AuxMargins, o.Matrices
	return c
}

// AnyComponents reports whether any Components sub-part is computed.
func (c ComputePlan) AnyComponents() bool {
	return c.Aggs || c.Groups || c.Groupers || c.Filterers || c.Run ||
		c.Crosstab || c.AuxMargins || c.Matrices
}

// ComputePlanFor derives the plan a resolved `return` selection allows:
// a part is computed iff the plan keeps it or something below it (the
// returnplan Visit verdict), so a narrow include such as
// `components.aggregations[*].n` still computes its slot. A nil plan
// (no effective block) computes everything. Pure: it knows nothing of
// the DisableComponents gate or the veto set, which the service folds
// on top.
func ComputePlanFor(ret *returnplan.Plan) ComputePlan {
	if ret == nil {
		return FullComputePlan()
	}
	k := func(names ...string) returnplan.Verdict {
		return visitChain(ret, names...)
	}
	c := ComputePlan{
		Aggs:         k("components", "aggregations").Keep,
		Groupers:     k("components", "groupers").Keep,
		Filterers:    k("components", "filterers").Keep,
		Run:          k("components", "run").Keep,
		Crosstab:     k("components", "crosstab").Keep,
		Matrices:     k("components", "matrices").Keep,
		MatricesSlot: k("matrices").Keep,
		Overlays:     k("overlays").Keep,
		Tests:        k("tests").Keep,
		PostTests:    k("post_tests").Keep,
		Regressions:  k("regressions").Keep,
	}
	c.Groups = c.Aggs && k("components", "aggregations", "[*]", "groups").Keep
	c.AuxMargins = c.Crosstab && (k("components", "crosstab", "row_margin_aggregations").Keep ||
		k("components", "crosstab", "column_margin_aggregations").Keep ||
		k("components", "crosstab", "grand_total_aggregations").Keep)
	return c
}

// visitChain walks the concrete path named step by step ("[*]" is an
// array element), asking the plan only for a child of a node it emits
// (the Visit contract) and stopping early on a whole subtree.
func visitChain(p *returnplan.Plan, names ...string) returnplan.Verdict {
	path := make([]returnplan.Segment, 0, len(names))
	v := p.Visit(nil)
	for _, n := range names {
		if !v.Keep || v.Whole {
			return v
		}
		if n == "[*]" {
			path = append(path, returnplan.Elem())
		} else {
			path = append(path, returnplan.Key(n))
		}
		v = p.Visit(path)
	}
	return v
}
