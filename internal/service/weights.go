package service

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// SetDefaultWeight installs pulse.Options.DefaultWeight (nil = none),
// the weight every weight-bearing slot inherits when neither the slot
// nor the request names one. pulse.New validates it first. The spec is
// copied.
func (s *Service) SetDefaultWeight(spec *types.WeightSpec) {
	if spec == nil {
		s.defaultWeight = nil
		return
	}
	c := *spec
	s.defaultWeight = &c
}

// DefaultWeight returns a copy of the installed default weight, or nil.
func (s *Service) DefaultWeight() *types.WeightSpec {
	if s.defaultWeight == nil {
		return nil
	}
	c := *s.defaultWeight
	return &c
}

// resolveWeights runs the single weight-resolution pass
// (internal/descriptor.ResolveWeights — the function predict calls) for
// one Request against the schema it executes over. checkFieldRefs calls
// it once the field references pass, so every execution mode resolves
// weights exactly once per Request, before any record is read. A
// located refusal: Compose adds details.request, a chain details.stage.
func (s *Service) resolveWeights(req *types.Request, schema *encoding.Schema) error {
	_, err := descx.ResolveWeights(req, schema, s.defaultWeight, s.instance)
	return markLocated(err)
}

// neededFields is processing.NeededFields plus the instance default
// weight's column: NeededFields sees the request and slot weights but
// not pulse.Options.DefaultWeight, and a weight column left out of the
// projection decodes as null on every row.
func (s *Service) neededFields(req *types.Request, schema *encoding.Schema) processing.FieldSet {
	set := processing.NeededFields(req, schema, s.extensions)
	if s.defaultWeight != nil && schema != nil && schema.Field(s.defaultWeight.Field) != nil {
		set.Add(s.defaultWeight.Field)
	}
	return set
}

// newProcessor builds the processor every execution mode runs a
// Request on: the instance's extensions, the request's ComputePlan
// (computePlanFor: the plan Service.process resolved onto ctx), and the
// weighting knobs (Options.DefaultWeight, which the
// processor folds into each run through processing.StampWeights, and
// strict mode, which turns PULSE_WEIGHT_INVALID_ROWS into an error),
// and the instance's effective resource limits (Service.Limits — the
// parallel reducers bypass this and take them through newShardPartial).
func (s *Service) newProcessor(ctx context.Context, schema *encoding.Schema, req *types.Request) *processing.Processor {
	proc := processing.NewProcessorWithExtensions(schema, s.extensions)
	proc.SetComputePlan(s.computePlanFor(ctx, req))
	proc.SetWeighting(s.defaultWeight, s.strict)
	proc.SetLimits(s.Limits())
	return proc
}
