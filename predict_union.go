package pulse

import (
	"context"
	"fmt"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// predictUnionEnvelope is Predict for a request carrying a Union: the
// schema-free union rules first (so a member with no cohort is never
// opened), then every member opened header-only in declared order —
// exactly as a single cohort is (a single file, a shard archive, an
// anchor) — and the request predicted over the merged schema
// (descx.PredictUnion). A union has no SPSS sidecar facts: they are
// per-cohort. A member that cannot be opened is an error naming the
// member, as an unreadable single cohort is.
func (p *Pulse) predictUnionEnvelope(ctx context.Context, req *Request, strictEcho bool) (*descriptor.Envelope, error) {
	opts := p.predictOptions(ctx, sidecarFacts{})
	if strictEcho {
		opts.Strict = p.svc.Strict()
		opts.EchoRequest = p.svc.EchoRequest()
	}
	if descx.UnionShapeRefusal(req) != nil {
		return descx.PredictUnion(nil, req, &opts), nil
	}
	members := make([]descx.UnionSource, 0, len(req.Union))
	paths := make([]string, 0, len(req.Union))
	var closers []func()
	defer func() {
		for _, c := range closers {
			c()
		}
	}()
	for i, m := range req.Union {
		path := resolveCohortPath(m.Cohort)
		src, err := p.openCohortSource(path)
		if err != nil {
			where := "pulse: predict union member " + strconv.Itoa(i) + " (" + strconv.Quote(m.Label) + ")"
			if af, ok := asAnchorFault(err); ok {
				return nil, fmt.Errorf("%s anchor: %w", where, af.err)
			}
			return nil, cohortOpenError(where, path, err)
		}
		closers = append(closers, src.close)
		members = append(members, descx.UnionSource{Label: m.Label, Cohort: m.Cohort, R: src.rs})
		paths = append(paths, path)
	}
	env := descx.PredictUnion(members, req, &opts)
	if _, ok := env.Data.(*descriptor.PredictResult); ok {
		for _, path := range paths {
			p.touchManaged(ctx, path)
		}
	}
	return env, nil
}

// predictResultOf is Predict's reading of a predict envelope: the
// result carrying the envelope's errors and warnings (Valid false when
// it has errors), or the first error when the envelope holds no result.
func predictResultOf(env *descriptor.Envelope) (*descriptor.PredictResult, error) {
	result, ok := env.Data.(*descriptor.PredictResult)
	if !ok {
		if len(env.Errors) > 0 {
			return nil, envelopeEntryError("pulse: predict", env.Errors[0])
		}
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL, "pulse: predict returned unexpected type")
	}
	result.Errors, result.Warnings = env.Errors, env.Warnings
	return result, nil
}
