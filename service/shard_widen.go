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
	//
	// From == To is the one-shard case: the arriving shard declared a
	// NARROWER rung than the archive and was promoted to meet it, so no
	// existing shard moved. Callers deciding whether an archive-wide
	// rewrite happened compare From against To — never the presence of
	// the entry, which covers both directions.
	From string `json:"from"`
	// To is the rung every shard in the archive declares after the
	// rewrite. Never narrower than From or IncomingFrom.
	To string `json:"to"`
	// IncomingFrom is the rung the ARRIVING shard declared. It differs
	// from From exactly when the two sides disagreed on the rung, which
	// is the case a re-import of a new period produces: the source
	// inferred the rung its own data needed.
	IncomingFrom string `json:"incoming_from"`
	// DictEntries is the merged dictionary size the two schemas imply.
	DictEntries int `json:"dict_entries"`
	// ShardsRewritten counts every shard payload actually re-laid-out.
	// An archive widen rewrites the archive's existing shards plus the
	// incoming one, because after it no shard carries the old stride; a
	// promotion of a narrow incoming shard rewrites exactly that one.
	ShardsRewritten int `json:"shards_rewritten"`
	// RecordsRewritten is the total number of records re-laid-out
	// across those shards.
	RecordsRewritten int64 `json:"records_rewritten"`
}

// ArchiveWidened reports whether this widening promoted the ARCHIVE —
// the canonical schema and every shard already stored in it — rather
// than only promoting the arriving shard to meet a rung the archive
// already declared. The two cost wildly different amounts and the
// warning wording differs accordingly.
func (w SetWidening) ArchiveWidened() bool { return w.From != w.To }

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

// CreateShardArchiveResult is what CreateShardArchive reports.
//
// Like AddShardResult it is a RESULT rather than a bare error for one
// reason: the seed path auto-widens too, and the PULSE_SHARD_SET_WIDENED
// warning is mandatory wherever a widen happens. A create that silently
// re-lays-out the shards it was handed is the same failure as a silent
// add — the caller cannot tell a straight zip of their inputs from a
// rewrite of them, and the `.pulse` bytes now in the archive are not the
// `.pulse` bytes on disk beside it.
//
// One rule, no asymmetry: create and add widen on the same conditions,
// through the same planner and executor, and report through the same
// SetWidening / CohesionWarning shapes.
type CreateShardArchiveResult struct {
	// Archive is the archive path that was written.
	Archive string `json:"archive"`
	// ShardCount is the number of shards the archive holds.
	ShardCount int `json:"shard_count"`
	// Widened carries one entry per set field promoted to a wider rung
	// while the archive was being assembled; empty on the ordinary
	// create.
	Widened []SetWidening `json:"widened,omitempty"`
	// Warnings carry every non-fatal diagnostic the create produced —
	// PULSE_SHARD_DESCRIPTION_DIVERGENCE from cohesion, and one
	// PULSE_SHARD_SET_WIDENED per entry in Widened.
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
// Each side is widened only if it is BELOW the plan's target. That is
// not an optimisation: WidenSetFieldBytes refuses a target that is not
// wider than the source (CheckSetWiden), so handing it a payload already
// at the target turns a legitimate reconciliation into a failed add. The
// two below-target cases are the two directions a rung divergence takes
// — the archive rising to meet a wider incoming shard, and a narrower
// incoming shard rising to meet the archive — and both are reachable
// from an ordinary re-import.
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
		var records int64
		shards := 0

		// The archive side. Canonical and every shard already stored in
		// it declare plan.From, so they move together or not at all.
		if plan.From != plan.To {
			next, err := encoding.WidenSchemaSetField(canonical, plan.Field, plan.To)
			if err != nil {
				return nil, nil, nil, err
			}
			canonical = next

			for i := range existing {
				out, rep, werr := encoding.WidenSetFieldBytes(existing[i].payload, plan.Field, plan.To)
				if werr != nil {
					return nil, nil, nil, errors.WrapCodedError(werr, errors.PULSE_SHARD_SCHEMA_MISMATCH,
						fmt.Sprintf("widening set field %q in shard %q", plan.Field, existing[i].name))
				}
				existing[i].payload = out
				records += rep.Records
				shards++
			}
		}

		// The arriving shard. It may already be at the target — that is
		// precisely the case where a wider incoming shard pulled the
		// archive up to itself — and must not be re-widened.
		if plan.IncomingFrom != plan.To {
			out, rep, werr := encoding.WidenSetFieldBytes(incoming, plan.Field, plan.To)
			if werr != nil {
				return nil, nil, nil, errors.WrapCodedError(werr, errors.PULSE_SHARD_SCHEMA_MISMATCH,
					fmt.Sprintf("widening set field %q in the incoming shard", plan.Field))
			}
			incoming = out
			records += rep.Records
			shards++
		}

		widenings = append(widenings, SetWidening{
			Field:            plan.Field,
			From:             plan.From.String(),
			To:               plan.To.String(),
			IncomingFrom:     plan.IncomingFrom.String(),
			DictEntries:      plan.UnionEntries,
			ShardsRewritten:  shards,
			RecordsRewritten: records,
		})
	}
	return canonical, incoming, widenings, nil
}

// setWidenedWarning renders the mandatory PULSE_SHARD_SET_WIDENED
// warning for one widening. It names the field, the old type, the new
// type and the number of shards rewritten — the four facts a caller
// needs to tell an append from an archive-wide rewrite.
//
// The message branches on direction because the two outcomes differ by
// orders of magnitude in cost and a caller has to be able to tell them
// apart from the prose alone: an ARCHIVE widen re-lays-out every record
// of every shard, while a promotion of a narrow arriving shard touches
// only that shard. The code and the details map are the same in both
// cases — a set field was widened, and here are the rungs.
func setWidenedWarning(w SetWidening, archive string) encoding.CohesionWarning {
	var msg string
	if w.ArchiveWidened() {
		msg = fmt.Sprintf(
			"set field %q was widened from %s to %s (arriving shard declared %s; merged dictionary has %d entries); every record of all %d shard(s) in %s was re-laid-out (%d records)",
			w.Field, w.From, w.To, w.IncomingFrom, w.DictEntries, w.ShardsRewritten, archive, w.RecordsRewritten)
	} else {
		msg = fmt.Sprintf(
			"the arriving shard declared set field %q as %s; it was widened to the archive's %s before being stored in %s (%d record(s) re-laid-out). The archive itself was not rewritten",
			w.Field, w.IncomingFrom, w.To, archive, w.RecordsRewritten)
	}
	return encoding.CohesionWarning{
		Code:    string(errors.PULSE_SHARD_SET_WIDENED),
		Message: msg,
		Details: map[string]any{
			"field":             w.Field,
			"from":              w.From,
			"to":                w.To,
			"incoming_from":     w.IncomingFrom,
			"archive_widened":   w.ArchiveWidened(),
			"dict_entries":      w.DictEntries,
			"shards_rewritten":  w.ShardsRewritten,
			"records_rewritten": w.RecordsRewritten,
			"archive":           archive,
		},
	}
}
