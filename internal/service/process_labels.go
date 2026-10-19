package service

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// validateProcessLabels runs descriptor.ValidateLabels against the
// resolved schema and the request's output columns (outputLabels) so
// per-binding failures surface before the processor runs; an
// augment-mode sibling "<field>_label" cannot shadow an output column.
func (s *Service) validateProcessLabels(req *types.Request, schema *encoding.Schema) error {
	if len(req.Labels) == 0 {
		return nil
	}
	extras := s.outputLabels(req, schema)
	env := descriptor.NewEnvelope(nil)
	// The dictionary-inheritance view: a binding on an ATTR_PREVIOUS_STATE
	// output column validates as one on its state field (predict reads
	// the same view).
	descx.ValidateLabels(env, req.Labels, prevstate.KeySchema(req.Attributes, schema), s.ExtensionsSnapshot(), s.InstanceSnapshot(), extras)
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

// outputLabels is the request's output column set an augment-mode
// label sibling may not collide with — descx.OutputLabels, the one set
// predict judges the collision against too (#201b).
func (s *Service) outputLabels(req *types.Request, schema *encoding.Schema) map[string]bool {
	return descx.OutputLabels(req, schema, s.InstanceSnapshot())
}

// autoLabelOccupied is outputLabels for the auto-label pass
// (applyAutoLabels' occupied set), built only when the service has
// default label bindings to inject — nil otherwise, so an instance
// without them pays nothing.
func (s *Service) autoLabelOccupied(req *types.Request, schema *encoding.Schema) map[string]bool {
	if len(s.autoLabels) == 0 {
		return nil
	}
	return s.outputLabels(req, schema)
}

// applyLabelsToResponse walks resp.Data through the LabelResolver,
// rewriting bound-field values (replace mode) or emitting sibling
// columns (augment mode), then carries the same translation onto every
// key surface derived from those values (applyLabelSurfaces: crosstab
// axes, overlay keys incl. pairwise PAIR keys, group-key tuples).
// Resolver warnings are appended to resp.Warnings as
// types.ResponseWarning records, then one PULSE_LABEL_NOT_APPLIED per
// caller-authored binding no surface carried (auto: the instance's
// default bindings, which never warn).
//
// Called with a nil resolver, the function is a no-op so the caller
// path can stay branch-free.
func applyLabelsToResponse(req *types.Request, resp *types.Response, r *processing.LabelResolver, auto []*types.LabelBinding) {
	if resp == nil || r == nil {
		return
	}
	p := newLabelPass(r)
	for _, row := range resp.Data {
		for field, v := range row {
			if !r.Has(field) {
				continue
			}
			p.reached[field] = true
			raw, ok := rawAsString(v)
			if !ok {
				continue
			}
			out, sibling, hit := r.Apply(field, raw)
			switch r.Mode(field) {
			case types.LabelModeAugment:
				if hit {
					row[field+"_label"] = sibling
				} else {
					row[field+"_label"] = nil
				}
			case types.LabelModeReplace:
				if hit {
					row[field] = out
					p.record(field, raw, out)
				}
			}
		}
	}
	p.applyLabelSurfaces(resp, req.Groups)
	for _, w := range r.Warnings() {
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
			Code:    string(w.Code),
			Message: w.Message,
			Details: w.Details,
		})
	}
	resp.Warnings = append(resp.Warnings, p.notAppliedWarnings(req.Labels, auto, req.Groups)...)
}

// buildAndApplyLabels is the standard post-process hook: builds the
// resolver from req.Labels + the runtime extension registry, applies
// it in-place to the response, and surfaces any resolver-build error
// (PULSE_LABEL_TABLE_UNKNOWN). Resolver-build is cheap; per-Request
// reuse beyond this call is unnecessary today.
func (s *Service) buildAndApplyLabels(req *types.Request, resp *types.Response) error {
	if len(req.Labels) == 0 || resp == nil {
		return nil
	}
	resolver, err := processing.BuildLabelResolver(req.Labels, s.extensions)
	if err != nil {
		return err
	}
	applyLabelsToResponse(req, resp, resolver, s.autoLabelDefaults(req.Attributes))
	return nil
}
