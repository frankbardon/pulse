package service

import (
	"context"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// limitsPreflight is the process pre-flight for the instance resource
// limits: it refuses the first certain breach predict reports for req
// over schema (descx.LimitRefusal — the shared rule) with
// PULSE_LIMIT_EXCEEDED. Every Request arm calls it right after
// checkFieldRefs, so a certain breach is refused before any record is
// decoded. It reads only the schema and the header counts in in
// (limitInputs; a join host's built by openJoinStream).
func (s *Service) limitsPreflight(req *types.Request, schema *encoding.Schema, in descx.LimitInputs) error {
	if err := descx.LimitRefusal(req, schema, s.instance, s.Limits(), in); err != nil {
		return markLocated(err)
	}
	return nil
}

// memoryBounded reports whether MaxEstimatedMemory is set — the only
// rule that needs the record counts, so an Unlimited memory limit (the
// default) reads none.
func (s *Service) memoryBounded() bool {
	return !limits.IsUnlimited(s.Limits().MaxEstimatedMemory)
}

// limitInputs are the run facts the pre-flight reads for a request
// scanning cohort (opened from path) — the figures predict reads from
// the same header: an archive's per-shard counts from its manifest, a
// single file's header-only count (CountRecords, never
// Cohort.RecordCount). Counts are read only when MaxEstimatedMemory is
// set; otherwise Records is -1 (unknown), which no refusing rule needs.
func (s *Service) limitInputs(ctx context.Context, cohort *Cohort, path string) (descx.LimitInputs, error) {
	in := descx.LimitInputs{
		Records:        -1,
		JoinRightRows:  -1,
		Extensions:     s.ExtensionsSnapshot(),
		FusionDisabled: s.disableCrosstabFusion,
	}
	if !s.memoryBounded() {
		return in, nil
	}
	if shards := cohort.Shards(); len(shards) > 0 {
		var total int64
		for _, sh := range shards {
			in.ShardRecords = append(in.ShardRecords, sh.RecordCount)
			total += sh.RecordCount
		}
		in.Records = total
		return in, nil
	}
	n, err := s.CountRecords(ctx, path)
	if err != nil {
		return in, err
	}
	in.Records = int64(n)
	return in, nil
}

// composeSlotsPreflight refuses a Compose call carrying more requests
// than MaxComposeSlots — the whole call, before any slot runs (FailFast
// does not apply).
func (s *Service) composeSlotsPreflight(composed *types.ComposedRequest) error {
	if err := limits.CheckComposeSlots(s.Limits(), len(composed.Requests)); err != nil {
		return err
	}
	return nil
}
