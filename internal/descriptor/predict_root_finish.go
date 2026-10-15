package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// finishPredictRoot applies PredictOptions.Strict and EchoRequest to a
// non-Request predict root's envelope (Compose, facet, chain) once its
// validator has run. Strict promotes EVERY warning the root collected
// into an error, in order, at the root envelope only — the per-slot and
// per-stage body call (validateRequestBodyAt) keeps Strict off and
// drops its own warnings, so no entry is promoted twice. Advisories are
// not warnings and never move. The root's Valid is cleared through
// invalidate when any error stands afterwards. echo builds the root's
// normalized request; it is called, and env.Request set, only under
// EchoRequest, and a nil return leaves env.Request nil.
func finishPredictRoot(env *descriptor.Envelope, opts *PredictOptions, invalidate func(), echo func() any) {
	if opts != nil && opts.Strict && len(env.Warnings) > 0 {
		env.Errors = append(env.Errors, env.Warnings...)
		env.Warnings = []*descriptor.EnvelopeEntry{}
	}
	if len(env.Errors) > 0 {
		invalidate()
	}
	if opts != nil && opts.EchoRequest && echo != nil {
		if r := echo(); r != nil {
			env.Request = r
		}
	}
}

// addMetricUnsuitedAt reports each PULSE_VECTOR_METRIC_UNSUITED issue
// of one request of a multi-request root (a Compose slot or fit, chain
// stage 0) as a WARNING located under key = idx — the list the runtime
// reads (vectors.MetricUnsuitedIssues): it warns with every issue, and
// under Strict refuses the request with the first one, located the
// same way. The root's Strict pass (finishPredictRoot) promotes it.
func addMetricUnsuitedAt(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, key string, idx int) {
	for _, w := range vectors.MetricUnsuitedIssues(req, schema) {
		details := make(map[string]any, len(w.Details)+1)
		for k, v := range w.Details {
			details[k] = v
		}
		details[key] = idx
		env.AddWarning(string(w.Code), w.Message, details)
	}
}

// addTargetLeakageAt reports each PULSE_FEAT_TARGET_LEAKAGE_RISK of one
// request of a Compose root (a slot or fit; a chain stage carries no
// features — PULSE_CHAIN_NOT_MERGEABLE) as a WARNING located under
// key = idx — the list the runtime reads
// (TargetLeakageWarnings): it warns with every entry, and under Strict
// refuses the request with the first one, located the same way. The
// root's Strict pass (finishPredictRoot) promotes it.
func addTargetLeakageAt(env *descriptor.Envelope, req *types.Request, inst *InstanceSnapshot, key string, idx int) {
	for _, w := range TargetLeakageWarnings(req, inst) {
		details := make(map[string]any, len(w.Details)+1)
		for k, v := range w.Details {
			details[k] = v
		}
		details[key] = idx
		env.AddWarning(string(w.Code), w.Message, details)
	}
}
