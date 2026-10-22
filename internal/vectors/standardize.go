package vectors

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/types"
)

// standardize.go judges who may read a standardized vector
// (VectorSpec.standardize). The engine standardizes the MERGED
// co-moment of a matrix slot (processing.standardizeCoMoment), which
// equals z-scoring the members over the slot's listwise rows and
// refitting. Only a listwise co-moment MAT_* slot has that one state;
// every other consumer reads raw member values row by row (tests,
// attributes, groupers, aggregators, expressions), per-pair samples
// (missing "pairwise"), the rows themselves (a rank method, a buffered
// extension) or per-group centroids (MAT_DISTANCE, which has its own
// params.standardize). Each is refused rather than silently fed the raw
// values. It runs inside ResolveMatricesWith, so predict and the
// runtime refuse alike.

// StandardizeRefusal returns the first consumer slot of req that reads
// a standardized vector it cannot standardize, nil when none does.
// The matrix slot's own operator rules are standardizeMatrixRefusal's
// (per resolved matrix).
func StandardizeRefusal(req *types.Request) *errors.CodedError {
	if req == nil {
		return nil
	}
	std := map[string]int{}
	for i, v := range req.Vectors {
		if v.Standardize != "" {
			std[v.Name] = i
		}
	}
	if len(std) == 0 {
		return nil
	}
	refuse := func(name, slot, why string) *errors.CodedError {
		i, ok := std[name]
		if !ok {
			return nil
		}
		return standardizeUnsupported(slotPath(i), name, slot, why)
	}
	const rowWise = "it reads the member values row by row; only a listwise co-moment MAT_* slot standardizes its vector"
	for i, m := range req.Matrices {
		if g, ok := SpecBetween(m); ok && g.Vector != "" {
			if err := refuse(g.Vector, matrixPath(i)+".params.between.vector", "a between grouper keys rows by their raw member values"); err != nil {
				return err
			}
		}
	}
	for i, t := range req.Tests {
		if t == nil {
			continue
		}
		at := "tests[" + strconv.Itoa(i) + "]"
		if err := refuse(t.Vector, at+".vector", rowWise); err != nil {
			return err
		}
		if err := refuse(t.Vector2, at+".vector2", rowWise); err != nil {
			return err
		}
	}
	for i, a := range req.Attributes {
		if a == nil {
			continue
		}
		if err := refuse(a.Vector, "attributes["+strconv.Itoa(i)+"].vector", rowWise); err != nil {
			return err
		}
	}
	for _, s := range GroupSlots(req) {
		if err := refuse(s.Group.Vector, s.Path+".vector", rowWise); err != nil {
			return err
		}
	}
	for _, s := range AggregationSlots(req) {
		if err := refuse(s.Agg.Vector, s.Path+".vector", rowWise); err != nil {
			return err
		}
	}
	return standardizeExprRefusal(req, refuse)
}

// standardizeExprRefusal refuses an expression that binds a
// standardized vector as an identifier (the slots exprReferenced
// scans): the expression program reads raw member values per row.
func standardizeExprRefusal(req *types.Request, refuse func(name, slot, why string) *errors.CodedError) *errors.CodedError {
	const why = "an expression reads the member values row by row; only a listwise co-moment MAT_* slot standardizes its vector"
	scan := func(src, slot string) *errors.CodedError {
		if src == "" {
			return nil
		}
		idents, _, _ := exprident.Free(src)
		for _, id := range idents {
			if err := refuse(id, slot, why); err != nil {
				return err
			}
		}
		return nil
	}
	filters := func(prefix string, fs []*types.Filterer) *errors.CodedError {
		for i, f := range fs {
			if f != nil && f.Type == types.FILTER_EXPRESSION {
				if err := scan(f.Expression, prefix+"["+strconv.Itoa(i)+"].expression"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := filters("filterers", req.Filterers); err != nil {
		return err
	}
	for i, a := range req.Attributes {
		if a == nil {
			continue
		}
		at := "attributes[" + strconv.Itoa(i) + "]"
		if a.Type == types.ATTR_FORMULA {
			if err := scan(a.Expression, at+".expression"); err != nil {
				return err
			}
		}
		if err := filters(at+".params.fit_on", AttributeFitOn(a.Type, a)); err != nil {
			return err
		}
	}
	return nil
}

// standardizeMatrixRefusal judges a matrix slot reading a standardized
// vector (m.Members.Standardize set): the slot must fold one listwise
// co-moment and emit from it. at is the slot path.
func standardizeMatrixRefusal(at string, specs []types.VectorSpec, m *Matrix) *errors.CodedError {
	if m.Members.Standardize == "" {
		return nil
	}
	var why string
	switch {
	case m.Type == types.MAT_DISTANCE:
		why = "MAT_DISTANCE compares per-group centroids, not one listwise state; use its params.standardize"
	case m.Pairwise:
		why = "missing \"pairwise\" has no single listwise sample to standardize over; use listwise"
	case !m.Streamable:
		why = "its operator reads the rows themselves (a rank method or a buffered operator), not the co-moment state"
	default:
		return nil
	}
	i := indexOfVector(specs, m.Members.Name)
	return standardizeUnsupported(slotPath(i), m.Members.Name, at+".vector", why)
}

func standardizeUnsupported(at, name, slot, why string) *errors.CodedError {
	return invalid(at, name, "standardize_unsupported",
		"vector "+describe(at, name)+" sets standardize, which "+slot+" cannot read: "+why,
		map[string]any{"slot": slot})
}
