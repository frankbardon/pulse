package service

import (
	"fmt"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
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
	descriptor.ValidateLabels(env, req.Labels, schema, s.extensionsSnap, nil)
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
// are appended to FacetResult.Warnings as human-readable strings —
// the typed-warning slot lives on Response, not FacetResult, and
// FacetResult already documents Warnings as advisory diagnostics.
func (s *Service) applyFacetLabels(req *types.FacetRequest, result *types.FacetResult) error {
	if result == nil || len(req.Labels) == 0 {
		return nil
	}
	resolver, err := processing.BuildLabelResolver(req.Labels, s.extensions)
	if err != nil {
		return err
	}
	if resolver == nil {
		return nil
	}

	for _, b := range req.Labels {
		if b == nil {
			continue
		}
		field := result.Fields[b.Field]
		if field == nil || field.Discrete == nil {
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
		result.Warnings = append(result.Warnings, fmt.Sprintf("[%s] %s", w.Code, w.Message))
	}
	return nil
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
