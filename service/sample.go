package service

import (
	"context"

	"github.com/frankbardon/pulse/errors"
)

// Sample returns up to n rows from the cohort as maps of field name to
// value. Streams from disk — stops reading as soon as n rows are
// collected. For shard archives the rows span the union of shards in
// central-directory (insertion) order; offset+limit apply globally
// across the union, never per-shard (sharding design contract §5.4).
func (s *Service) Sample(ctx context.Context, path string, n int) ([]map[string]any, error) {
	return s.sampleOffsetLimit(ctx, path, 0, n)
}

// sampleOffsetLimit is the internal helper that powers Sample and the
// shard-aware tests. offset>0 walks rows from the head of the union
// stream until offset rows have been seen, then collects up to limit
// rows. For single-file cohorts this iterates a streamingIterator; for
// shard archives the same logic runs over a shardIter that surfaces
// the union row stream in insertion order — Service.newScanIter picks
// the right one.
//
// limit<=0 returns an empty slice (matches the historical
// "Sample(path, 0)" no-op behavior). offset<0 is normalized to zero.
// When offset+limit exceeds the available row count, the result is
// truncated naturally — no error.
func (s *Service) sampleOffsetLimit(ctx context.Context, path string, offset, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		return nil, nil
	}
	if offset < 0 {
		offset = 0
	}

	cohort, err := s.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	iter := s.newScanIter(cohort, path)
	defer iter.Close()

	// Skip past offset rows. We don't decode-and-discard via the
	// scanIterator's projection hook because Sample's contract is "give
	// me whole rows" and the user-facing payload is the field map; the
	// per-row decode cost is the same either way, only the post-decode
	// retention changes.
	skipped := 0
	for skipped < offset && iter.Next() {
		skipped++
	}
	if err := iter.Err(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "sampling cohort")
	}
	if skipped < offset {
		// offset past end of union — no rows to return.
		return nil, nil
	}

	rows := make([]map[string]any, 0, limit)
	for iter.Next() && len(rows) < limit {
		rows = append(rows, iter.Record().AllValues())
	}
	if err := iter.Err(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "sampling cohort")
	}
	return rows, nil
}
