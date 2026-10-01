package service

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// validateProcessLabels runs descriptor.ValidateLabels against the
// resolved schema and the request's projected output columns so
// per-binding failures surface before the processor runs. The
// extraFields set carries aggregation / attribute / group output
// labels so an augment-mode sibling "<field>_label" cannot shadow
// them.
func (s *Service) validateProcessLabels(req *types.Request, schema *encoding.Schema) error {
	if len(req.Labels) == 0 {
		return nil
	}
	extras := collectOutputLabels(req)
	env := descriptor.NewEnvelope(nil)
	descriptor.ValidateLabels(env, req.Labels, schema, s.extensionsSnap, extras)
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

// collectOutputLabels enumerates the output column names produced by
// a request's aggregations, attributes, group keys, windows, and
// features so augment-mode label siblings can be checked for
// collisions against them.
func collectOutputLabels(req *types.Request) map[string]bool {
	out := map[string]bool{}
	for _, a := range req.Aggregations {
		if a == nil {
			continue
		}
		if a.Label != "" {
			out[a.Label] = true
		}
	}
	for _, a := range req.Attributes {
		if a == nil {
			continue
		}
		if a.Label != "" {
			out[a.Label] = true
		}
	}
	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		if g.Field != "" {
			out[g.Field] = true
		}
	}
	for _, w := range req.Windows {
		if w == nil {
			continue
		}
		if w.Label != "" {
			out[w.Label] = true
		}
	}
	for _, f := range req.Features {
		if f == nil {
			continue
		}
		if f.Label != "" {
			out[f.Label] = true
		}
	}
	return out
}

// applyLabelsToResponse walks resp.Data through the LabelResolver,
// rewriting bound-field values (replace mode) or emitting sibling
// columns (augment mode). Resolver warnings are appended to
// resp.Warnings as types.ResponseWarning records.
//
// Called with a nil resolver, the function is a no-op so the caller
// path can stay branch-free.
func applyLabelsToResponse(resp *types.Response, r *processing.LabelResolver) {
	if resp == nil || r == nil {
		return
	}
	for _, row := range resp.Data {
		for field, v := range row {
			if !r.Has(field) {
				continue
			}
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
				}
			}
		}
	}
	for _, w := range r.Warnings() {
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{
			Code:    string(w.Code),
			Message: w.Message,
			Details: w.Details,
		})
	}
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
	applyLabelsToResponse(resp, resolver)
	return nil
}
