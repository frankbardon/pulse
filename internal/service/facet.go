package service

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
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
// A set_* field lists its dictionary the same way (its members, in
// dictionary order); a vec_* field is refused SERVICE_VALIDATION
// (descx.FacetTypeUnsupported) — it has no scalar values to list.
//
// For numeric fields the cohort is streamed once and the set of
// distinct values observed is returned in encounter order, each
// rendered by its field type (processing.FacetValue): a date as
// YYYY-MM-DD, a datetime as RFC 3339 UTC, a decimal128 at its column
// scale, a u64 as its exact word, a packed_bool as "1" / "0",
// any other number as a plain float. For shard
// archives the stream spans every shard in central-directory
// (insertion) order — the union semantics specified in §5.4. The
// streamed distinct-value set is bounded by MaxGroups, and a set
// MaxEstimatedMemory refuses the streamed path before any record is
// read (FacetLimitRefusal); the categorical fast path returns the
// schema's dictionary, which is already resident, reads no record and
// mints nothing, so neither limit applies to it.
func (s *Service) Facet(ctx context.Context, path string, field string) ([]string, error) {
	ctx, release := s.BoundRequest(ctx)
	defer release()
	values, err := s.facet(ctx, path, field)
	if err != nil {
		return nil, MapRequestTimeout(ctx, err)
	}
	return values, nil
}

func (s *Service) facet(ctx context.Context, path string, field string) ([]string, error) {
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
	// A set field lists its dictionary the same way: its members, in
	// dictionary order — the labels a FILTER_SET_* value names — never
	// the bitmasks. A shard archive's canonical dictionary is the union
	// of every shard's (set fields widen across shards).
	if f.Type.IsSet() {
		if f.Dictionary == nil {
			return []string{}, nil
		}
		return f.Dictionary.Values(), nil
	}
	// A vector has no distinct scalar values to list: refused, never
	// an empty list.
	if f.Type.IsVector() {
		return nil, descx.FacetTypeUnsupported(f)
	}

	// Instance resource limits on the streamed path: a set
	// MaxEstimatedMemory refuses before the iterator opens — the rich
	// facet's pre-flight (FacetLimitRefusal) over the one field. The
	// categorical fast path above reads no record and mints nothing, so
	// it has no pre-flight (its estimate would charge a payload read it
	// never does).
	lin, err := s.limitInputs(ctx, cohort, path)
	if err != nil {
		return nil, err
	}
	freq := &types.FacetRequest{Cohort: &types.Cohort{Filename: path}, Fields: []string{field}}
	if lerr := descx.FacetLimitRefusal(freq, cohort.Schema(), s.Limits(), lin); lerr != nil {
		return nil, lerr
	}

	// Numeric path: stream the union of records via newScanIter, which
	// transparently picks streamingIterator for single-file cohorts and
	// shardIter for archives. The iterator surfaces every record from
	// every shard in insertion order, so the seen-set collects distinct
	// values across the whole union.
	iter := s.newScanIter(cohort, path)
	defer iter.Close()

	l := s.Limits()
	seen := make(map[any]struct{})
	var values []string
	poll := processing.NewCtxPoller(ctx)
	for iter.Next() {
		if err := poll.Poll(); err != nil {
			return nil, err
		}
		// Each value rendered by its field type, as Sample and Lookup
		// render it (processing.FacetValue): a date's calendar literal,
		// a decimal at its scale, a u64's exact word.
		key, text, ok := processing.FacetValue(iter.Record(), f)
		if !ok {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		// MaxGroups bounds the distinct values the stream mints.
		if err := limits.Check(l, limits.MaxGroups, int64(len(seen))+1); err != nil {
			return nil, err
		}
		seen[key] = struct{}{}
		values = append(values, text)
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}

	return values, nil
}
