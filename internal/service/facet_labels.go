package service

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// validateFacetLabels runs descriptor.ValidateLabels against the
// resolved schema so per-binding failures surface as typed errors
// before the facet pass starts. Facet has no projected output columns
// beyond the requested fields, so the extraFields set is empty.
func (s *Service) validateFacetLabels(req *types.FacetRequest, schema *encoding.Schema) error {
	if len(req.Labels) == 0 {
		return nil
	}
	env := descriptor.NewEnvelope(nil)
	descx.ValidateLabels(env, req.Labels, schema, s.ExtensionsSnapshot(), s.InstanceSnapshot(), nil)
	if len(env.Errors) == 0 {
		return nil
	}
	first := env.Errors[0]
	code, ok := errors.ParseCode(first.Code)
	if !ok {
		code = errors.SERVICE_VALIDATION
	}
	return errors.NewCodedErrorWithDetails(code, first.Message, first.Details)
}

// applyFacetLabels walks each per-field FacetField through the
// LabelResolver:
//
//   - replace: rewrites FacetDiscrete.Values[i].Value in place; map
//     keys collide silently here because the value list is a slice,
//     so disambiguation happens via the resolver's normal "label (raw)"
//     output for source values that share a label.
//   - augment: produces a parallel FacetField under "<field>_label"
//     in FacetResult.Fields. The sibling carries the same Counts but
//     each Value is replaced with its label (or left raw on miss).
//
// Resolver warnings (PULSE_LABEL_COLLISION, PULSE_LABEL_LOOKUP_MISS)
// and one PULSE_LABEL_NOT_APPLIED per caller-authored binding on a field
// the facet did not tally as discrete values are appended to FacetResult.Warnings as coded {code, message,
// details} entries — the shape Response.Warnings carries them in.
//
// It returns the resolver it labelled the host with (nil when no
// binding applies): the facet overlays relabel the population with
// that same resolver (relabelPopulationField), so a function-driven
// table's online collision order, set by the host's values, keys both
// sides alike.
func (s *Service) applyFacetLabels(req *types.FacetRequest, result *types.FacetResult) (*processing.LabelResolver, error) {
	if result == nil || len(req.Labels) == 0 {
		return nil, nil
	}
	resolver, err := processing.BuildLabelResolver(req.Labels, s.extensions)
	if err != nil {
		return nil, err
	}
	if resolver == nil {
		return nil, nil
	}

	var notApplied []*types.ResponseWarning
	for i, b := range req.Labels {
		if b == nil {
			continue
		}
		field := result.Fields[b.Field]
		if field == nil || field.Discrete == nil {
			// A binding on a field the facet did not tally as discrete
			// values reaches nothing: say so, never drop it silently.
			if !isAutoBinding(b, s.autoLabels) {
				notApplied = append(notApplied, labelNotApplied(i, b, labelReasonNoSurface))
			}
			continue
		}
		switch resolver.Mode(b.Field) {
		case types.LabelModeReplace:
			for i := range field.Discrete.Values {
				raw := field.Discrete.Values[i].Value
				if out, _, ok := resolver.Apply(b.Field, raw); ok {
					field.Discrete.Values[i].Value = out
				}
			}
		case types.LabelModeAugment:
			sibling := facetSiblingFromDiscrete(field, resolver, b.Field)
			result.Fields[b.Field+"_label"] = sibling
		}
	}

	for _, w := range resolver.Warnings() {
		result.Warnings = append(result.Warnings, &types.ResponseWarning{Code: string(w.Code), Message: w.Message, Details: w.Details})
	}
	result.Warnings = append(result.Warnings, notApplied...)
	return resolver, nil
}

// relabelPopulationField returns the population's copy of field with
// its discrete values relabelled by the host's resolver when the host
// labels that field in replace mode — the host's own key space, the
// one its overlay coordinates use. The resolver already saw every host
// value, so a function-driven table's online collisions resolve the
// way they did on the host (the clean label stays with the source the
// host gave it), never in the population's own value order. Augment,
// unbound or non-discrete fields come back as is: the host's overlay
// field keeps its raw values there. The population result is never
// mutated.
func relabelPopulationField(pop *types.FacetResult, field string, resolver *processing.LabelResolver) *types.FacetResult {
	if pop == nil || resolver.Mode(field) != types.LabelModeReplace {
		return pop
	}
	src := pop.Fields[field]
	if src == nil || src.Discrete == nil {
		return pop
	}
	values := make([]types.FacetValueCount, len(src.Discrete.Values))
	for i, vc := range src.Discrete.Values {
		if out, _, ok := resolver.Apply(field, vc.Value); ok {
			vc.Value = out
		}
		values[i] = vc
	}
	discrete := *src.Discrete
	discrete.Values = values
	ff := *src
	ff.Discrete = &discrete
	out := *pop
	out.Fields = make(map[string]*types.FacetField, len(pop.Fields))
	for k, v := range pop.Fields {
		out.Fields[k] = v
	}
	out.Fields[field] = &ff
	return &out
}

// facetSiblingFromDiscrete copies a FacetField's discrete value list
// into a parallel FacetField whose values are translated through the
// resolver. Numeric / null tallies and metadata mirror the source so
// consumers can dereference the sibling without re-querying.
func facetSiblingFromDiscrete(src *types.FacetField, r *processing.LabelResolver, field string) *types.FacetField {
	if src == nil || src.Discrete == nil {
		return nil
	}
	cloned := &types.FacetDiscrete{
		Values:        make([]types.FacetValueCount, len(src.Discrete.Values)),
		DistinctCount: src.Discrete.DistinctCount,
		TruncatedAt:   src.Discrete.TruncatedAt,
	}
	for i, vc := range src.Discrete.Values {
		out := vc.Value
		// In augment mode resolver.Apply returns (raw, label, ok). The
		// sibling FacetField should carry the label, falling back to
		// the raw value on miss.
		if _, label, ok := r.Apply(field, vc.Value); ok {
			out = label
		}
		cloned.Values[i] = types.FacetValueCount{Value: out, Count: vc.Count}
	}
	return &types.FacetField{
		Kind:        src.Kind,
		TypeName:    src.TypeName,
		Description: src.Description,
		NullCount:   src.NullCount,
		Discrete:    cloned,
	}
}
