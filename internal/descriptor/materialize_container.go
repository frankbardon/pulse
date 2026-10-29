package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// materialize_container.go is the no-execute half of the output slot's
// container keys (types.MaterializeOutput.Shards / ElideConstants): the
// container plan predict reports and the shard-count limit the run and
// its predict share.

// MaterializeContainerPlan is the container a materialize run of req
// writes: a single file, or a shard archive of output.shards.max_records
// rows per shard; ElideConstants mirrors output.elide_constants.
// records is the source's record count (-1 unknown): a shard archive's
// MaxShards is ceil(records / max_records), at least 1 — the most shards
// the run writes, every record passing the filters. Schema-only.
func MaterializeContainerPlan(req *types.Request, records int64) *descriptor.MaterializeContainer {
	plan := &descriptor.MaterializeContainer{Kind: "file"}
	if req == nil || req.Output == nil {
		return plan
	}
	plan.ElideConstants = req.Output.ElideConstants
	if sh := req.Output.Shards; sh != nil && sh.MaxRecords > 0 {
		plan.Kind, plan.MaxRecords = "shard_archive", sh.MaxRecords
		if records >= 0 {
			per := int64(sh.MaxRecords)
			n := max((records+per-1)/per, 1)
			plan.MaxShards = &n
		}
	}
	return plan
}

// MaterializeShardLimitRisk is the data-dependent shard-count check of a
// sharded materialize run, or nil when it cannot fire: when the source
// holds more records than max_records × the archive's shard limit
// (encx.MaxArchiveShards), a run whose filters keep too many of them
// fails with PULSE_SHARD_COUNT_LIMIT (MaterializeShardLimit).
func MaterializeShardLimitRisk(plan *descriptor.MaterializeContainer) *descriptor.MaterializeRisk {
	if plan == nil || plan.MaxShards == nil || *plan.MaxShards <= encx.MaxArchiveShards {
		return nil
	}
	return &descriptor.MaterializeRisk{
		Path: "output.shards.max_records",
		Code: string(errors.PULSE_SHARD_COUNT_LIMIT),
		Check: "the written rows must fit " + strconv.Itoa(encx.MaxArchiveShards) + " shards of " +
			strconv.Itoa(plan.MaxRecords) + " rows; the source holds enough records for " +
			strconv.FormatInt(*plan.MaxShards, 10) + " — the run fails at the first row past the limit and writes nothing",
	}
}

// MaterializeShardLimitReached reports whether appending one more row to
// a sharded materialize output already holding rows rows would open a
// shard past the archive's shard limit.
func MaterializeShardLimitReached(rows int64, maxRecords int) bool {
	return maxRecords > 0 && rows >= int64(maxRecords)*encx.MaxArchiveShards
}

// MaterializeShardLimit is the run-time refusal of a row that would open
// one shard more than an archive holds: details archive (the target),
// max_records, limit and row (the 0-based output row). Raised before the
// row is appended, so nothing is written at the target.
func MaterializeShardLimit(target string, maxRecords int, row int64) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_COUNT_LIMIT,
		"output row "+strconv.FormatInt(row, 10)+" would open shard "+strconv.Itoa(encx.MaxArchiveShards+1)+
			" of "+target+"; an archive holds at most "+strconv.Itoa(encx.MaxArchiveShards)+
			" shards — raise output.shards.max_records; nothing was written",
		map[string]any{"archive": target, "max_records": maxRecords, "limit": encx.MaxArchiveShards, "row": row})
}
