package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// rakeParts declares the named part OVERLAY_RAKE writes beside its
// primary fitted table, with its payload shape. overlayPartShapes reads
// it.
var rakeParts = map[string]overlayPart{
	"factors": {Shape: types.OverlayShapeMatrix},
}

// validateOverlayRake enforces the predict contract of OVERLAY_RAKE
// (route = the instance's routing of the kind; any other route
// returns): Ref must be EMPTY (implicit-margin), the host must be
// MATRIX-shaped (Request.Crosstab), Scope must be MATRIX, params must
// parse (overlayhost.ParseRakeParams — counts targets whose totals
// differ included), the cell aggregation must be AGG_COUNT or AGG_SUM
// (overlayhost.NonnegativeCellRefusal) and, on an axis whose keys the
// schema bounds (one GROUP_CATEGORY over a categorical field), every
// target key must be a dictionary entry (overlayhost.RakeKeyRefusal
// with no host_only side: which keys the host actually observes is the
// runtime's call, predict reads no record). Every refusal but the shape
// / scope ones is the shared overlayhost refusal the runtime handler
// raises — same code and message, plus index.
//
// Errors (first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE: a Ref family, or no crosstab.
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED: Scope other than matrix.
//   - PULSE_OVERLAY_PARAM_MISSING: a malformed / unknown / missing param.
//   - PULSE_OVERLAY_RAKE_TARGET_MISMATCH: counts totals differ, or a
//     target key outside a categorical axis's dictionary.
//   - PULSE_OVERLAY_NONNEGATIVE_REQUIRED: a cell aggregation other
//     than AGG_COUNT / AGG_SUM.
func validateOverlayRake(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, schema *encoding.Schema, inst *InstanceSnapshot, route types.OverlayKind, index int) {
	if route != types.OverlayKindRake || req == nil || spec == nil {
		return
	}
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, route, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	if req.Crosstab == nil {
		addHostRequired(env, spec, route, len(req.Groups) > 0, index)
		return
	}
	params, cerr := overlayhost.ParseRakeParams(spec, index)
	if cerr != nil {
		addCodedError(env, cerr)
		return
	}
	if cell := req.Crosstab.Cell; cell != nil {
		if cerr := overlayhost.NonnegativeCellRefusal(spec.Kind, cell.Type, index); cerr != nil {
			addCodedError(env, cerr)
			return
		}
	}
	for _, axis := range []struct {
		name    string
		groups  []*types.Group
		targets map[string]float64
	}{
		{overlayhost.RakeAxisRow, req.Crosstab.Rows, params.RowTargets},
		{overlayhost.RakeAxisColumn, req.Crosstab.Columns, params.ColTargets},
	} {
		if axis.name == overlayhost.RakeAxisColumn && req.Crosstab.HasImplicitTotalColumn() {
			// The implicit Total column has exactly one key, known
			// without a record: the runtime's both-way key check
			// (rakeAxisTargets) applies in full.
			if cerr := rakeTotalColumnKeyRefusal(spec.Kind, axis.targets, index); cerr != nil {
				addCodedError(env, cerr)
				return
			}
			continue
		}
		dict := axisDictionary(axis.groups, schema, inst)
		if dict == nil {
			continue
		}
		var targetOnly []string
		for key := range axis.targets {
			if _, ok := dict.IDFor(key); !ok {
				targetOnly = append(targetOnly, key)
			}
		}
		if cerr := overlayhost.RakeKeyRefusal(spec.Kind, axis.name, targetOnly, nil, index); cerr != nil {
			addCodedError(env, cerr)
			return
		}
	}
}

// axisDictionary is the dictionary bounding a crosstab axis's keys when
// the schema knows it — one GROUP_CATEGORY (as routed) over a
// categorical field — else nil.
func axisDictionary(axis []*types.Group, schema *encoding.Schema, inst *InstanceSnapshot) *encoding.Dictionary {
	if len(axis) != 1 || axis[0] == nil || schema == nil || opRoute(inst, axis[0].Type) != types.GROUP_CATEGORY {
		return nil
	}
	for _, f := range schema.Fields {
		if f.Name == axis[0].Field && f.Type.IsCategorical() {
			return f.Dictionary
		}
	}
	return nil
}

// rakeTotalColumnKeyRefusal is the rake key check against the implicit
// Total column's one key (types.CrosstabTotalColumnKey): a target naming
// any other key is target-only, and an absent "Total" target leaves the
// host key unmatched — the refusal the runtime raises off the host.
func rakeTotalColumnKeyRefusal(kind types.OverlayKind, targets map[string]float64, index int) *errors.CodedError {
	var targetOnly, hostOnly []string
	for key := range targets {
		if key != types.CrosstabTotalColumnKey {
			targetOnly = append(targetOnly, key)
		}
	}
	if _, ok := targets[types.CrosstabTotalColumnKey]; !ok {
		hostOnly = []string{types.CrosstabTotalColumnKey}
	}
	return overlayhost.RakeKeyRefusal(kind, overlayhost.RakeAxisColumn, targetOnly, hostOnly, index)
}
