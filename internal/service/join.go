package service

import (
	"context"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// processWithJoin handles a Request whose Joins slot is non-empty.
// v1 supports exactly one inner join per Request. The right side is
// opened, fully decoded into a slice of Records, hashed by the
// join-key tuple, then the left side is streamed via a HashJoinIterator
// that emits joined records through the standard processor pipeline.
//
// Defer: multi-join chains, outer/left/anti kinds, spill, parallel
// shards on the join leg. See skills/join-design.md for the v1
// scope envelope.
func (s *Service) processWithJoin(ctx context.Context, req *types.Request) (*types.Response, error) {
	// Strip Joins from the spec passed to the processor so the
	// processor's standard pipeline runs against the joined records
	// without re-triggering join logic. Defaults, zones, field
	// references and the limits pre-flight run on the joined schema
	// before the build side decodes a record (openJoinStream's
	// preflight hook).
	clone := *req
	clone.Joins = nil
	clone.Cohort = nil
	var zones []descriptor.ResolvedZone
	join, joinedSchema, leftPath, leftIter, err := s.openJoinStream(ctx, req, func(joined *encoding.Schema, lin descx.LimitInputs) error {
		s.applyDefaults(&clone, joined)
		z, err := s.resolveZones(&clone, joined)
		if err != nil {
			return err
		}
		zones = z
		if err := s.checkFieldRefs(&clone, joined); err != nil {
			return err
		}
		return s.limitsPreflight(&clone, joined, lin)
	})
	if err != nil {
		return nil, err
	}
	defer leftIter.Close()
	execInfoFrom(ctx).setPlan(observe.ArmJoin, 1, 0, 0)

	proc := s.newProcessor(ctx, joinedSchema, req)
	resp, err := proc.Process(ctx, s.zoned(&clone, zones), join)
	if err != nil {
		return nil, err
	}
	if resp.Metadata != nil {
		resp.Metadata.CohortFile = leftPath
	}
	// Inject configured default label bindings against the joined output
	// schema before rendering. Defaults whose field was renamed by a
	// JoinSpec.As prefix simply do not match and are skipped.
	s.applyAutoLabels(&req.Labels, joinedSchema, collectOutputLabels(req), nil)
	if err := s.buildAndApplyLabels(req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// openJoinStream builds the joined row stream for a Request carrying
// exactly one JoinSpec: the right side is opened and fully decoded into
// a slice of Records, hashed by the join-key tuple, and the left side
// is wrapped in a HashJoinIterator. It is shared by processWithJoin and
// the crosstab arm (processCrosstabWithJoin) so both read the identical
// joined stream. The returned left scan iterator is the one the join
// wraps: the caller owns it (Close, and Err after draining) whenever
// err is nil.
//
// preflight is the host's own checks over the joined schema —
// defaults, zones, field references and the limits pre-flight, which
// reads the LimitInputs built here (the left header count and the
// right one MaxJoinBuildRows / MaxEstimatedMemory read). It runs after
// the join-key rule and the MaxJoinBuildRows check and BEFORE the build
// side decodes a record, so a refusal costs no record decode on either
// side.
//
// Process applied descx.JoinCountRefusal before either caller runs.
func (s *Service) openJoinStream(ctx context.Context, req *types.Request, preflight func(joined *encoding.Schema, lin descx.LimitInputs) error) (*processing.HashJoinIterator, *encoding.Schema, string, scanIterator, error) {
	spec := req.Joins[0]
	if spec == nil {
		return nil, nil, "", nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "JoinSpec is required")
	}
	leftPath := resolveCohortPath(req.Cohort)
	leftCohort, err := s.Open(ctx, leftPath)
	if err != nil {
		return nil, nil, "", nil, err
	}
	rightCohort, err := s.Open(ctx, spec.Right)
	if err != nil {
		return nil, nil, "", nil, err
	}
	countReads(ctx, leftCohort)
	countReads(ctx, rightCohort)
	// Kind and OnPairs, before the right side is decoded: the one
	// join-key rule predict and the validators call
	// (internal/encoding.JoinKeysRefusals). A located refusal, like the
	// join-count rule: Compose adds details.request, a chain
	// details.stage.
	if err := encx.JoinKeysRefusal(leftCohort.Schema(), rightCohort.Schema(), spec); err != nil {
		return nil, nil, "", nil, markLocated(err)
	}

	// MaxJoinBuildRows, before the build decodes a record: the right
	// side is decoded unfiltered, so its header-only count is the exact
	// build size — the figure predict grades certain
	// (descx joinBuildLimitFinding). A located refusal, like the
	// join-key rule.
	l := s.Limits()
	buildBounded := !limits.IsUnlimited(l.MaxJoinBuildRows)
	rightRows := int64(-1)
	if buildBounded || s.memoryBounded() {
		n, err := joinBuildCount(ctx, s, spec.Right)
		if err != nil {
			return nil, nil, "", nil, err
		}
		rightRows = int64(n)
	}
	if buildBounded {
		if lerr := limits.CheckJoinBuildRows(l, rightRows); lerr != nil {
			return nil, nil, "", nil, markLocated(lerr)
		}
	}

	// The host's checks over the joined schema — defaults, zones, field
	// references and the limits pre-flight (MaxEstimatedMemory reads
	// the left header count and the right one above) — before the
	// build decodes a record.
	joined, err := processing.JoinedSchema(leftCohort.Schema(), rightCohort.Schema(), spec)
	if err != nil {
		return nil, nil, "", nil, err
	}
	lin, err := s.limitInputs(ctx, leftCohort, leftPath)
	if err != nil {
		return nil, nil, "", nil, err
	}
	lin.Join, lin.JoinRightRows = true, rightRows
	if err := preflight(joined, lin); err != nil {
		return nil, nil, "", nil, err
	}

	// Materialise the right side as a slice. v1 does not spill; the
	// memory cost is O(right_record_count × per_record_state). Tests
	// and skills call this out.
	rightIter := s.newScanIter(rightCohort, spec.Right)
	defer rightIter.Close()
	var rightRecords []*processing.Record
	poll := processing.NewCtxPoller(ctx)
	for rightIter.Next() {
		// The caller's ctx error, unchanged, every
		// processing.CtxPollInterval build rows.
		if err := poll.Poll(); err != nil {
			return nil, nil, "", nil, err
		}
		// Backstop for a header miscount: refuse as soon as the decode
		// passes the limit, before the slice grows further.
		if buildBounded {
			if lerr := limits.CheckJoinBuildRows(l, int64(len(rightRecords))+1); lerr != nil {
				return nil, nil, "", nil, markLocated(lerr)
			}
		}
		// Copy values so the slice survives iterator reuse.
		src := rightIter.Record()
		values := make(map[string]float64, len(src.Schema().Fields))
		nulls := make(map[string]bool)
		wide := make(map[string]any)
		for _, f := range src.Schema().Fields {
			if v, ok := src.NumericValue(f.Name); ok {
				values[f.Name] = v
			}
			if w, ok := src.WideValue(f.Name); ok {
				wide[f.Name] = w
			}
		}
		rightRecords = append(rightRecords, processing.NewRecordWithWide(src.Schema(), values, nulls, wide))
	}
	if rightIter.Err() != nil {
		return nil, nil, "", nil, rightIter.Err()
	}

	leftIter := s.newScanIter(leftCohort, leftPath)
	join, joinedSchema, err := processing.NewHashJoinIterator(leftIter, rightRecords, leftCohort.Schema(), rightCohort.Schema(), spec)
	if err != nil {
		_ = leftIter.Close()
		return nil, nil, "", nil, err
	}
	return join, joinedSchema, leftPath, leftIter, nil
}

// joinBuildCount is the header-only right-side record count the join
// build pre-flight reads (Service.CountRecords — never
// Cohort.RecordCount, which reads the whole file). A variable so a test
// can feed a miscount and prove the in-loop backstop.
var joinBuildCount = func(ctx context.Context, s *Service, path string) (uint64, error) {
	return s.CountRecords(ctx, path)
}
