package service

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// SetWidening records one set field promoted to a wider rung while a
// shard was being added, together with the size of the rewrite it cost.
//
// It is TYPED rather than only narrated in the warning message because
// an embedder that adds shards on a schedule needs to act on the fact
// (rebuild the point-lookup index, re-export, alert) without parsing
// prose. The mandatory warning carries the same figures for a human or
// an LLM reading the `--json` envelope.
type SetWidening struct {
	// Field is the widened set field's name.
	Field string `json:"field"`
	// From is the rung the archive declared before the rewrite.
	From string `json:"from"`
	// To is the rung it declares after.
	To string `json:"to"`
	// DictEntries is the merged dictionary size that forced the widen.
	DictEntries int `json:"dict_entries"`
	// ShardsRewritten counts every shard payload re-laid-out — the
	// archive's existing shards plus the incoming one, because after a
	// widen no shard in the archive carries the old stride.
	ShardsRewritten int `json:"shards_rewritten"`
	// RecordsRewritten is the total number of records re-laid-out
	// across those shards.
	RecordsRewritten int64 `json:"records_rewritten"`
}

// AddShardResult is what AddShard reports.
//
// AddShard returns a result rather than a bare error specifically so the
// widen warning cannot be dropped. Auto-widening is a whole-archive byte
// rewrite: it is far cheaper than the full re-import it replaces, but it
// is not free, and a caller who never learns it happened cannot tell an
// O(1) append from an O(archive) rewrite. The warning is mandatory, so
// the channel carrying it is mandatory too.
type AddShardResult struct {
	// Archive is the archive path that was rewritten.
	Archive string `json:"archive"`
	// Added is the source path of the shard that was appended.
	Added string `json:"added"`
	// ShardCount is the archive's shard count after the add.
	ShardCount int `json:"shard_count"`
	// Widened carries one entry per set field promoted to a wider rung;
	// empty on the ordinary append.
	Widened []SetWidening `json:"widened,omitempty"`
	// Warnings carry every non-fatal diagnostic the add produced —
	// PULSE_SHARD_DESCRIPTION_DIVERGENCE from cohesion, and one
	// PULSE_SHARD_SET_WIDENED per entry in Widened. Shaped like the
	// descriptor envelope's warning entries so a caller lifts them
	// straight onto `warnings` without reshaping.
	Warnings []encoding.CohesionWarning `json:"warnings"`
}

// shardPayload is one archive entry held in memory during a rebuild.
type shardPayload struct {
	name    string
	payload []byte
}

// applySetWidening executes a SetWidenPlan set against the canonical
// schema, every existing shard payload and the incoming shard payload,
// returning the widened canonical schema and one SetWidening per plan.
//
// The canonical schema and the shard payloads go through the SAME
// encoding helper pair (WidenSchemaSetField / WidenSetFieldBytes, which
// share one offset derivation), so the canonical `_schema.pulse` block
// and every shard's own schema block describe the identical post-widen
// layout by construction rather than by agreement. A divergence there
// would not fail to open — it would decode every field after the
// widened one at the wrong offset.
//
// existing and incoming are mutated in place (their payload slices are
// replaced, never edited), so the caller's slices carry the widened
// bytes on return.
func applySetWidening(
	plans []encoding.SetWidenPlan,
	canonical *encoding.Schema,
	existing []shardPayload,
	incoming []byte,
) (*encoding.Schema, []byte, []SetWidening, error) {
	widenings := make([]SetWidening, 0, len(plans))
	for _, plan := range plans {
		next, err := encoding.WidenSchemaSetField(canonical, plan.Field, plan.To)
		if err != nil {
			return nil, nil, nil, err
		}
		canonical = next

		var records int64
		for i := range existing {
			out, rep, werr := encoding.WidenSetFieldBytes(existing[i].payload, plan.Field, plan.To)
			if werr != nil {
				return nil, nil, nil, errors.WrapCodedError(werr, errors.PULSE_SHARD_SCHEMA_MISMATCH,
					fmt.Sprintf("widening set field %q in shard %q", plan.Field, existing[i].name))
			}
			existing[i].payload = out
			records += rep.Records
		}

		out, rep, werr := encoding.WidenSetFieldBytes(incoming, plan.Field, plan.To)
		if werr != nil {
			return nil, nil, nil, errors.WrapCodedError(werr, errors.PULSE_SHARD_SCHEMA_MISMATCH,
				fmt.Sprintf("widening set field %q in the incoming shard", plan.Field))
		}
		incoming = out
		records += rep.Records

		widenings = append(widenings, SetWidening{
			Field:            plan.Field,
			From:             plan.From.String(),
			To:               plan.To.String(),
			DictEntries:      plan.UnionEntries,
			ShardsRewritten:  len(existing) + 1,
			RecordsRewritten: records,
		})
	}
	return canonical, incoming, widenings, nil
}

// setWidenedWarning renders the mandatory PULSE_SHARD_SET_WIDENED
// warning for one widening. It names the field, the old type, the new
// type and the number of shards rewritten — the four facts a caller
// needs to tell an append from an archive-wide rewrite.
func setWidenedWarning(w SetWidening, archive string) encoding.CohesionWarning {
	return encoding.CohesionWarning{
		Code: string(errors.PULSE_SHARD_SET_WIDENED),
		Message: fmt.Sprintf(
			"set field %q was widened from %s to %s to fit a merged dictionary of %d entries; every record of all %d shard(s) in %s was re-laid-out (%d records)",
			w.Field, w.From, w.To, w.DictEntries, w.ShardsRewritten, archive, w.RecordsRewritten),
		Details: map[string]any{
			"field":             w.Field,
			"from":              w.From,
			"to":                w.To,
			"dict_entries":      w.DictEntries,
			"shards_rewritten":  w.ShardsRewritten,
			"records_rewritten": w.RecordsRewritten,
			"archive":           archive,
		},
	}
}
