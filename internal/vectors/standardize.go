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
	const rowWise = "it reads the member values row by row; only a listwise co-moment MAT_* slot standardizes its vector"
	return nonMatrixConsumerRefusal(req, func(name, slot string, kind consumerKind) *errors.CodedError {
		i, ok := std[name]
		if !ok {
			return nil
		}
		why := rowWise
		switch kind {
		case consumerBetween:
			why = "a between grouper keys rows by their raw member values"
		case consumerExpr:
			why = "an expression reads the member values row by row; only a listwise co-moment MAT_* slot standardizes its vector"
		}
		return standardizeUnsupported(slotPath(i), name, slot, why)
	})
}

// consumerKind tells a non-matrix vector consumer's refusal which
// sentence fits.
type consumerKind int

const (
	consumerSlot    consumerKind = iota // a test / attribute / grouper / aggregation vector
	consumerBetween                     // a MAT_DISTANCE params.between vector
	consumerExpr                        // an expression identifier
)

// nonMatrixConsumerRefusal visits, in a fixed order, every slot of req
// that reads a Request.Vectors entry by name other than a MAT_* member
// vector — a MAT_DISTANCE params.between vector, a test vector /
// vector2, an attribute / grouper / aggregation vector and an
// expression identifier (the slots exprReferenced scans) — and returns
// the first refusal refuse makes (refuse returns nil for a name it
// admits).
func nonMatrixConsumerRefusal(req *types.Request, refuse func(name, slot string, kind consumerKind) *errors.CodedError) *errors.CodedError {
	for i, m := range req.Matrices {
		if g, ok := SpecBetween(m); ok && g.Vector != "" {
			if err := refuse(g.Vector, matrixPath(i)+".params.between.vector", consumerBetween); err != nil {
				return err
			}
		}
	}
	for i, t := range req.Tests {
		if t == nil {
			continue
		}
		at := "tests[" + strconv.Itoa(i) + "]"
		if err := refuse(t.Vector, at+".vector", consumerSlot); err != nil {
			return err
		}
		if err := refuse(t.Vector2, at+".vector2", consumerSlot); err != nil {
			return err
		}
	}
	for i, a := range req.Attributes {
		if a == nil {
			continue
		}
		if err := refuse(a.Vector, "attributes["+strconv.Itoa(i)+"].vector", consumerSlot); err != nil {
			return err
		}
	}
	for _, s := range GroupSlots(req) {
		if err := refuse(s.Group.Vector, s.Path+".vector", consumerSlot); err != nil {
			return err
		}
	}
	for _, s := range AggregationSlots(req) {
		if err := refuse(s.Agg.Vector, s.Path+".vector", consumerSlot); err != nil {
			return err
		}
	}
	return exprConsumerRefusal(req, refuse)
}

// exprConsumerRefusal visits every expression identifier of the slots
// exprReferenced scans (a FILTER_EXPRESSION in Request.Filterers or a
// vector attribute's params.fit_on, an ATTR_FORMULA expression).
func exprConsumerRefusal(req *types.Request, refuse func(name, slot string, kind consumerKind) *errors.CodedError) *errors.CodedError {
	scan := func(src, slot string) *errors.CodedError {
		if src == "" {
			return nil
		}
		idents, _, _ := exprident.Free(src)
		for _, id := range idents {
			if err := refuse(id, slot, consumerExpr); err != nil {
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
