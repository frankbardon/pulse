package pulse

import (
	"context"
	"fmt"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
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
	if descx.UnionExecRefusal(req) != nil {
		return descx.PredictUnion(nil, req, &opts), nil
	}
	members, paths, closeAll, err := p.openUnionSources(req.Union, "predict")
	if err != nil {
		return nil, err
	}
	defer closeAll()
	env := descx.PredictUnion(members, req, &opts)
	if _, ok := env.Data.(*descriptor.PredictResult); ok {
		for _, path := range paths {
			p.touchManaged(ctx, path)
		}
	}
	return env, nil
}

// openUnionSources opens every member of union header-only, in
// declared order, exactly as a single cohort is (a single file, a
// shard archive, an anchor): the members PredictUnion and the union
// validators read, their paths (to slide each managed import's TTL)
// and the closer of every opened source. A member that cannot be
// opened is an error naming the member (op prefixes it), as an
// unreadable single cohort is; every source opened before it is
// closed.
func (p *Pulse) openUnionSources(union []types.UnionMember, op string) ([]descx.UnionSource, []string, func(), error) {
	members := make([]descx.UnionSource, 0, len(union))
	paths := make([]string, 0, len(union))
	var closers []func()
	closeAll := func() {
		for _, c := range closers {
			c()
		}
	}
	for i, m := range union {
		path := resolveCohortPath(m.Cohort)
		src, err := p.openCohortSource(path)
		if err != nil {
			closeAll()
			where := "pulse: " + op + " union member " + strconv.Itoa(i) + " (" + strconv.Quote(m.Label) + ")"
			if af, ok := asAnchorFault(err); ok {
				return nil, nil, nil, fmt.Errorf("%s anchor: %w", where, af.err)
			}
			return nil, nil, nil, cohortOpenError(where, path, err)
		}
		closers = append(closers, src.close)
		members = append(members, descx.UnionSource{Label: m.Label, Cohort: m.Cohort, R: src.rs})
		paths = append(paths, path)
	}
	return members, paths, closeAll, nil
}

// predictUnionRoot runs a non-Request predict root over a union: the
// shape rules first (a refused shape opens no member — check gets nil
// members and refuses it from its cohort head), then every member
// opened (openUnionSources) and check run over them with the
// instance's predict options (a union has no SPSS sidecar facts: they
// are per-cohort); touch slides each member's managed-import TTL after
// (Explain's checks do not).
func (p *Pulse) predictUnionRoot(ctx context.Context, cohort *types.Cohort, union []types.UnionMember, sourceField string, touch bool, check func([]descx.UnionSource, *descx.PredictOptions) *descriptor.Envelope) (*descriptor.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts := p.predictOptions(ctx, sidecarFacts{})
	if descx.UnionShapeRefusalOf(cohort, union, sourceField) != nil {
		return check(nil, &opts), nil
	}
	op := "predict"
	if !touch {
		op = "explain"
	}
	members, paths, closeAll, err := p.openUnionSources(union, op)
	if err != nil {
		return nil, err
	}
	defer closeAll()
	env := check(members, &opts)
	if touch {
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

// touchRequestCohorts slides the managed-import expiry of every cohort
// a successful run read: the request cohort, or each union member.
func (p *Pulse) touchRequestCohorts(ctx context.Context, req *Request) {
	p.touchRootCohorts(ctx, req.Cohort, req.Union)
}

// touchRootCohorts is touchRequestCohorts over any root's cohort and
// union slots.
func (p *Pulse) touchRootCohorts(ctx context.Context, cohort *types.Cohort, union []types.UnionMember) {
	if cohort != nil {
		p.touchManaged(ctx, resolveCohortPath(cohort))
	}
	for _, m := range union {
		if m.Cohort != nil {
			p.touchManaged(ctx, resolveCohortPath(m.Cohort))
		}
	}
}

// estimatedRecords is a stream's header-fast record estimate: the
// request cohort's count, or the sum of every union member's; -1 when
// any count cannot be read.
func (p *Pulse) estimatedRecords(ctx context.Context, req *Request) int64 {
	var names []string
	if req.Cohort != nil && req.Cohort.Filename != "" {
		names = append(names, req.Cohort.Filename)
	}
	for _, m := range req.Union {
		if m.Cohort == nil || m.Cohort.Filename == "" {
			return -1
		}
		names = append(names, m.Cohort.Filename)
	}
	if len(names) == 0 {
		return -1
	}
	var total uint64
	for _, name := range names {
		n, err := p.countRecords(ctx, name)
		if err != nil || n > uint64(1<<62)-total {
			return -1
		}
		total += n
	}
	return int64(total)
}
