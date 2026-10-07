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

	// MatricesSlot is the whole Response.Matrices slot. A run folds its
	// matrix specs per record iff it computes MatricesSlot or Matrices
	// (components.matrices rests on the same co-moment state), and
	// renders a MatrixResult only under MatricesSlot. MatrixAuxiliary /
	// MatrixScalars / MatrixVectors are matrices[*].auxiliary /
	// .scalars / .vectors — finalize-only sub-parts, each implying
	// MatricesSlot.
	MatricesSlot    bool
	MatrixAuxiliary bool
	MatrixScalars   bool
	MatrixVectors   bool

	// Overlays is the Response.Overlays slot: whether the run folds ANY
	// request overlay layer. overlayLayers, when non-nil, narrows it to
	// the layers it marks (index-aligned with Request.Overlays) — the
	// layers a veto keeps while the selection excludes the slot (see
	// WithOverlayLayers). A layer is one compute unit: its payload and
	// summary are never split.
	Overlays      bool
	overlayLayers *layerMask

	// Whole-slot parts outside Components. Carried for the stories that
	// wire them (tests, regressions); the service keeps them on until
	// their veto rules land.
	Tests       bool
	PostTests   bool
	Regressions bool
}

// layerMask is an immutable per-entry compute mask. ComputePlan holds
// it by pointer so the plan stays a comparable value; a plan built
// without one (every plan but a partially vetoed one) compares as
// before.
type layerMask struct{ keep []bool }

// WithOverlayLayers narrows the overlay slot to the layers keep marks
// (index-aligned with Request.Overlays): none marked turns the slot
// off, every one marked is the plain whole slot (no mask), anything
// between computes exactly the marked layers. keep is copied.
func (c ComputePlan) WithOverlayLayers(keep []bool) ComputePlan {
	some, all := false, len(keep) > 0
	for _, k := range keep {
		some = some || k
		all = all && k
	}
	c.overlayLayers = nil
	switch {
	case !some:
		c.Overlays = false
	case all:
		c.Overlays = true
	default:
		c.Overlays = true
		c.overlayLayers = &layerMask{keep: append([]bool(nil), keep...)}
	}
	return c
}

// ComputesOverlay reports whether the run folds request overlay layer
// i. A skipped layer runs no handler and raises no refusal or warning;
// its position in Response.Overlays (when any other layer is computed)
// holds a zero layer, so index alignment with Request.Overlays — which
// the multiplicity fold reads — survives.
func (c ComputePlan) ComputesOverlay(i int) bool {
	if !c.Overlays {
		return false
	}
	if c.overlayLayers == nil {
		return true
	}
	return i >= 0 && i < len(c.overlayLayers.keep) && c.overlayLayers.keep[i]
}

// overlayKeep is ComputesOverlay as a per-index predicate for the
// overlay dispatch loops; nil when every layer is computed (the
// unplanned loop).
func (c ComputePlan) overlayKeep() func(int) bool {
	if c.Overlays && c.overlayLayers == nil {
		return nil
	}
	return c.ComputesOverlay
}

// FullComputePlan computes every part — the plan of a request without
// a `return` block on an engine whose Components gate is open, and the
// default of every Processor.
func FullComputePlan() ComputePlan {
	return ComputePlan{
		Aggs: true, Groups: true, Groupers: true, Filterers: true, Run: true,
		Crosstab: true, AuxMargins: true, Matrices: true,
		MatricesSlot: true, MatrixAuxiliary: true, MatrixScalars: true, MatrixVectors: true,
		Overlays: true, Tests: true, PostTests: true, Regressions: true,
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
	c.MatrixAuxiliary = c.MatricesSlot && k("matrices", "[*]", "auxiliary").Keep
	c.MatrixScalars = c.MatricesSlot && k("matrices", "[*]", "scalars").Keep
	c.MatrixVectors = c.MatricesSlot && k("matrices", "[*]", "vectors").Keep
	c.AuxMargins = c.Crosstab && (k("components", "crosstab", "row_margin_aggregations").Keep ||
		k("components", "crosstab", "column_margin_aggregations").Keep ||
		k("components", "crosstab", "grand_total_aggregations").Keep)
	return c
}

// AccumulatesMatrices reports whether the run folds Request.Matrices
// per record at all: the slot or its Components entries need the
// co-moment state; neither, and no matrix slot is ever built.
func (c ComputePlan) AccumulatesMatrices() bool {
	return c.MatricesSlot || c.Matrices
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
