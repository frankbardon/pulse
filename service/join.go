package service

import (
	"context"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
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
	if len(req.Joins) > 1 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_JOIN_TOO_MANY,
			"v1 supports exactly one JoinSpec per Request",
			map[string]any{"count": len(req.Joins)})
	}
	spec := req.Joins[0]
	if spec == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "JoinSpec is required")
	}
	if len(spec.On) == 0 {
		return nil, errors.NewCodedError(errors.PULSE_JOIN_KEYS_EMPTY,
			"JoinSpec.On is empty; at least one OnPair is required")
	}

	leftPath := resolveCohortPath(req.Cohort)
	leftCohort, err := s.Open(ctx, leftPath)
	if err != nil {
		return nil, err
	}
	rightCohort, err := s.Open(ctx, spec.Right)
	if err != nil {
		return nil, err
	}

	// Materialise the right side as a slice. v1 does not spill; the
	// memory cost is O(right_record_count × per_record_state). Tests
	// and skills call this out.
	rightIter := s.newScanIter(rightCohort, spec.Right)
	defer rightIter.Close()
	var rightRecords []*processing.Record
	for rightIter.Next() {
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
		return nil, rightIter.Err()
	}

	leftIter := s.newScanIter(leftCohort, leftPath)
	defer leftIter.Close()

	join, joinedSchema, err := processing.NewHashJoinIterator(leftIter, rightRecords, leftCohort.Schema(), rightCohort.Schema(), spec)
	if err != nil {
		return nil, err
	}

	// Strip Joins from the spec passed to the processor so the
	// processor's standard pipeline runs against the joined records
	// without re-triggering join logic.
	clone := *req
	clone.Joins = nil
	clone.Cohort = nil

	s.applyDefaults(&clone, joinedSchema)

	proc := processing.NewProcessorWithExtensions(joinedSchema, s.extensions)
	resp, err := proc.Process(ctx, &clone, join)
	if err != nil {
		return nil, err
	}
	if resp.Metadata != nil {
		resp.Metadata.CohortFile = leftPath
	}
	return resp, nil
}
