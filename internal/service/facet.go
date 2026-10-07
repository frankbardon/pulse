package service

import (
	"context"
	"fmt"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
)

// Facet returns distinct values for the named field in the cohort.
//
// For categorical fields the dictionary is the authoritative source:
//   - Single-file cohorts return the schema's field dictionary directly.
//   - Shard archives return the canonical dictionary carried by
//     `_schema.pulse`, which is guaranteed to be the union of every
//     shard's per-shard dictionary by the append-only prefix rule
//     enforced at insert time (sharding design contract §3.2). No
//     record streaming is required.
//
// For numeric fields the cohort is streamed once and the set of
// distinct values observed is returned in encounter order. For shard
// archives the stream spans every shard in central-directory
// (insertion) order — the union semantics specified in §5.4. The
// streamed distinct-value set is bounded by MaxGroups; the categorical
// fast path returns the schema's dictionary, which is already resident
// and mints nothing, so it is not counted.
func (s *Service) Facet(ctx context.Context, path string, field string) ([]string, error) {
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	f := cohort.Schema().Field(field)
	if f == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			fmt.Sprintf("field %q not found in schema", field))
	}

	// Categorical fast path: the canonical schema's dictionary is the
	// union of every shard's dict (append-only prefix rule). Single-file
	// cohorts likewise carry the full dictionary on the field. Either
	// way we sidestep record streaming.
	if f.Type.IsCategorical() && f.Dictionary != nil {
		return f.Dictionary.Values(), nil
	}

	// Numeric path: stream the union of records via newScanIter, which
	// transparently picks streamingIterator for single-file cohorts and
	// shardIter for archives. The iterator surfaces every record from
	// every shard in insertion order, so the seen-set collects distinct
	// values across the whole union.
	iter := s.newScanIter(cohort, path)
	defer iter.Close()

	l := s.Limits()
	seen := make(map[float64]struct{})
	var values []string
	for iter.Next() {
		v, ok := iter.Record().NumericValue(field)
		if !ok {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		// MaxGroups bounds the distinct values the stream mints.
		if err := limits.Check(l, limits.MaxGroups, int64(len(seen))+1); err != nil {
			return nil, err
		}
		seen[v] = struct{}{}
		values = append(values, strconv.FormatFloat(v, 'f', -1, 64))
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}

	return values, nil
}
