package service

import (
	"bytes"
	stderrors "errors"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// Reasons a GroupReconciliation records.
const (
	// RegroupIncomingFlattened: the archive is ungrouped (0x01) and the
	// arriving shard was grouped; it was stored as its logical (0x01)
	// twin. Only the arriving shard was rewritten.
	RegroupIncomingFlattened = "incoming_flattened"
	// RegroupIncomingRegrouped: the archive is grouped and the arriving
	// shard declared a different group layout (none, or other groups);
	// it was re-encoded to the archive's layout. Only the arriving shard
	// was rewritten.
	RegroupIncomingRegrouped = "incoming_regrouped"
	// RegroupConstantPromoted: the arriving shard disagreed with a
	// CONSTANT group's one value, so that group became an indexed group
	// across the WHOLE archive — every shard re-encoded (each row gains a
	// 4-byte index).
	RegroupConstantPromoted = "constant_promoted"
)

// GroupReconciliation records one parent-group layout change a
// CreateShardArchive or AddShard made to fit a shard into the archive,
// with the size of the rewrite it cost. Typed for the same reason
// SetWidening is: an embedder adding shards on a schedule must be able
// to act on it without parsing prose. The mandatory
// PULSE_SHARD_GROUPS_REWRITTEN warning carries the same figures.
//
// A plain dictionary union — an arriving shard with the archive's group
// layout whose tuples are renumbered into the canonical dictionary — is
// NOT reported here, exactly as a categorical dictionary union is not:
// it rewrites only the arriving shard and is the ordinary cost of an add.
type GroupReconciliation struct {
	// Reason is one of "incoming_flattened", "incoming_regrouped",
	// "constant_promoted".
	Reason string `json:"reason"`
	// ArchiveRewritten is true when every shard already in the archive
	// was re-encoded (constant_promoted), false when only the arriving
	// shard was.
	ArchiveRewritten bool `json:"archive_rewritten"`
	// Groups labels the groups involved: the promoted groups for
	// constant_promoted, the archive's groups otherwise.
	Groups []string `json:"groups,omitempty"`
	// ArchiveFormatVersion / IncomingFormatVersion are the .pulse format
	// versions the archive and the arriving shard declared (1 = no
	// groups, 2 = grouped).
	ArchiveFormatVersion  int `json:"archive_format_version"`
	IncomingFormatVersion int `json:"incoming_format_version"`
	// ShardsRewritten / RecordsRewritten size the rewrite.
	ShardsRewritten  int   `json:"shards_rewritten"`
	RecordsRewritten int64 `json:"records_rewritten"`
}

// shardMerge is what mergeShard reports: the reconciled canonical
// schema, the arriving shard's bytes as they will be stored, and every
// widening, regrouping and warning the merge produced.
type shardMerge struct {
	canonical *encoding.Schema
	incoming  []byte
	widened   []SetWidening
	regrouped []GroupReconciliation
	warnings  []encx.CohesionWarning
}

// mergeShard folds one arriving shard into an archive whose canonical
// schema is canonical and whose stored shards are existing. It is the
// ONE merge rule CreateShardArchive (per seeded shard after the first)
// and AddShard share, so the two cannot disagree about what the same
// pair of files produces.
//
// existing is mutated in place — payloads replaced, never edited — when
// the archive itself moves (a set-rung widen of the archive, or a
// constant-group promotion); otherwise it is untouched.
func mergeShard(canonical *encoding.Schema, existing []shardPayload, incoming []byte, name, archivePath string) (*shardMerge, error) {
	incSchema, err := readSinglePulseSchema(incoming)
	if err != nil {
		return nil, err
	}
	if canonical.HasGroups() || incSchema.HasGroups() {
		return mergeGroupedShard(canonical, incSchema, existing, incoming, name, archivePath)
	}
	m := &shardMerge{}
	canon, incoming, incSchema, err := reconcileFlat(m, canonical, existing, incoming, incSchema, archivePath)
	if err != nil {
		return nil, err
	}
	// Union-merge the dictionaries. When the incoming shard's
	// dictionaries diverge from canonical, the union is adopted and the
	// incoming bytes are rewritten to use canonical indices before being
	// placed in the archive.
	canon, remap, err := encx.MergeDictUnion(canon, incSchema)
	if err != nil {
		return nil, err
	}
	if len(remap) > 0 {
		if incoming, err = encx.RewriteShardCategoricals(incoming, canon, remap); err != nil {
			return nil, err
		}
	}
	m.canonical, m.incoming = canon, incoming
	return m, nil
}

// reconcileFlat is the ungrouped half of every merge: set-rung
// reconciliation PLANNED before strict cohesion runs, and that order is
// the whole of the relaxation. A set column whose rung differs between
// the archive and the arriving shard fails ValidateStructuralCohesion
// twice over — on the type byte, and on every ByteOffset the rung shift
// moves — so cohesion running first would refuse the shard before the
// planner ever saw it. Nothing else loosens: the strict validator runs
// over the RECONCILED schemas, where a name, a non-set type, a bit
// position or a categorical width that diverges is still fatal.
//
// canonical, existing and incoming are all UNGROUPED here (the grouped
// path flattens first). Returns the reconciled canonical schema, the
// reconciled incoming bytes and their schema.
func reconcileFlat(m *shardMerge, canonical *encoding.Schema, existing []shardPayload, incoming []byte, incSchema *encoding.Schema, archivePath string) (*encoding.Schema, []byte, *encoding.Schema, error) {
	plans, err := encx.PlanSetWidening(canonical, incSchema)
	if err != nil {
		// A width overflow past the widest rung is the one fatal
		// set-width verdict and names itself precisely. Anything else
		// means the two schemas are not positionally comparable at all,
		// and the structural validator has the better diagnostic — so
		// give it the chance to speak before falling back.
		if !errors.HasCode(err, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW) {
			if _, cerr := encx.ValidateStructuralCohesion(canonical, incSchema); cerr != nil {
				return nil, nil, nil, cerr
			}
		}
		return nil, nil, nil, err
	}
	if len(plans) > 0 {
		widened, widenedIncoming, widenings, werr := applySetWidening(plans, canonical, existing, incoming)
		if werr != nil {
			return nil, nil, nil, werr
		}
		canonical, incoming = widened, widenedIncoming
		m.widened = append(m.widened, widenings...)
		for _, w := range widenings {
			m.warnings = append(m.warnings, setWidenedWarning(w, archivePath))
		}
		// Re-read the incoming schema from the widened bytes: the
		// canonical block and the shard block were produced by
		// different helpers, and the strict cohesion pass below is what
		// proves they agree.
		if incSchema, err = readSinglePulseSchema(incoming); err != nil {
			return nil, nil, nil, err
		}
	}
	cohesionWarnings, err := encx.ValidateStructuralCohesion(canonical, incSchema)
	m.warnings = append(m.warnings, cohesionWarnings...)
	if err != nil {
		return nil, nil, nil, err
	}
	return canonical, incoming, incSchema, nil
}

// mergeGroupedShard is mergeShard when either side carries parent
// groups (format 0x02). Every shard of an archive is decoded with the
// canonical schema, so the archive has ONE group layout and every
// shard's row indices address the canonical group dictionaries. The
// rules, in order:
//
//  1. Work on LOGICAL rows. The arriving shard is flattened to its 0x01
//     twin, and reconciled exactly as an ungrouped shard is — set-rung
//     plan, strict field cohesion, categorical dictionary union — because
//     every one of those speaks logical offsets. (The stored shards are
//     flattened only if the archive itself has to move.)
//  2. The ARCHIVE's layout wins; the arriving shard is conformed to it.
//     An ungrouped archive stores a grouped arrival flattened; a grouped
//     archive re-encodes an arrival with another layout (or none) into
//     its own. Adopting the arrival's layout instead would turn an append
//     into an archive-wide rewrite that can FAIL on data the archive
//     already accepted (a key its rows violate). Both are reported
//     (PULSE_SHARD_GROUPS_REWRITTEN, archive_rewritten false).
//  3. Group dictionaries union-merge canonical-first: the encoder is
//     SEEDED with the canonical dictionaries, so every entry a stored
//     shard references keeps its index, each stored shard's own
//     dictionary stays a prefix of the canonical one, and only the
//     arriving shard's indices are renumbered. A union past the u32
//     index space is PULSE_SHARD_DICT_WIDTH_OVERFLOW, never a wrap.
//  4. A CONSTANT group the arrival disagrees with is promoted to an
//     indexed group across the whole archive — every stored shard is
//     re-encoded — and reported as a MANDATORY warning with
//     archive_rewritten true. A key the arrival violates (same key,
//     different non-key member) is refused: PULSE_GROUP_MEMBER_NOT_CONSTANT
//     naming the shard, exactly as import refuses it.
//  5. Strict cohesion runs over the reconciled GROUPED schemas last, so
//     a reconciliation bug is a refusal, not a mixed-layout archive.
//
// A set-rung widen of a grouped archive re-encodes every shard too
// (entry widths move with a member's rung, the physical stride with a
// row field's); PULSE_SHARD_SET_WIDENED reports it.
func mergeGroupedShard(canonical, incSchema *encoding.Schema, existing []shardPayload, incoming []byte, name, archivePath string) (*shardMerge, error) {
	m := &shardMerge{}
	flatInc, incLogical, err := encx.FlattenCohortBytes(incoming)
	if err != nil {
		return nil, shardErr(err, name)
	}
	canonFlat := cloneSchemaForArchive(canonical.Logical())

	// The stored shards' logical form, built only when the archive moves.
	// For an ungrouped archive the stored payloads ARE logical.
	stored := existing
	flattened := !canonical.HasGroups()
	flattenStored := func() error {
		if flattened {
			return nil
		}
		flattened = true
		stored = make([]shardPayload, len(existing))
		for i, sh := range existing {
			b, _, ferr := encx.FlattenCohortBytes(sh.payload)
			if ferr != nil {
				return shardErr(ferr, sh.name)
			}
			stored[i] = shardPayload{name: sh.name, payload: b}
		}
		return nil
	}

	// An archive-side set widen re-lays-out every stored shard, so their
	// logical form must exist before the widen runs over it.
	plans, err := encx.PlanSetWidening(canonFlat, incLogical)
	rebuild := false
	if err == nil {
		for _, p := range plans {
			rebuild = rebuild || p.From != p.To
		}
	}
	if rebuild && canonical.HasGroups() {
		if err := flattenStored(); err != nil {
			return nil, err
		}
	}
	canonFlat, flatInc, incLogical, err = reconcileFlat(m, canonFlat, stored, flatInc, incLogical, archivePath)
	if err != nil {
		return nil, err
	}
	canonFlat, remap, err := encx.MergeDictUnion(canonFlat, incLogical)
	if err != nil {
		return nil, err
	}

	incVersion := int(incSchema.RequiredFormatVersion())
	archVersion := int(canonical.RequiredFormatVersion())

	if !canonical.HasGroups() {
		// Rule 2, ungrouped archive: the arrival is stored as its logical
		// twin — byte-identical to adding that twin directly, so the
		// ordinary categorical rewrite applies unchanged.
		if len(remap) > 0 {
			if flatInc, err = encx.RewriteShardCategoricals(flatInc, canonFlat, remap); err != nil {
				return nil, err
			}
		}
		rows, _ := recordCountFromBytes(flatInc, canonFlat)
		rec := GroupReconciliation{
			Reason:                RegroupIncomingFlattened,
			Groups:                groupLabels(incSchema, nil),
			ArchiveFormatVersion:  archVersion,
			IncomingFormatVersion: incVersion,
			ShardsRewritten:       1,
			RecordsRewritten:      rows,
		}
		m.regrouped = append(m.regrouped, rec)
		m.warnings = append(m.warnings, groupsRewrittenWarning(rec, archivePath))
		m.canonical, m.incoming = canonFlat, flatInc
		return m, nil
	}

	if len(remap) > 0 {
		// Null placeholders keep their bytes: they are part of a group
		// entry's identity (see RewriteShardCategoricalsKeepNulls).
		if flatInc, err = encx.RewriteShardCategoricalsKeepNulls(flatInc, canonFlat, remap); err != nil {
			return nil, err
		}
	}

	specs := encx.GroupSpecsOf(canonical)
	var promoted []int
	var incRows, storedRows int64
	for {
		if !rebuild {
			// Cheap path: seed with the canonical dictionaries and encode
			// only the arrival.
			enc, err := encx.NewGroupEncoder(canonFlat, specs)
			if err != nil {
				return nil, err
			}
			if err := enc.Seed(canonical); err != nil {
				return nil, shardErr(err, "_schema.pulse")
			}
			spool, rows, err := encodeLogicalShard(enc, flatInc)
			if g, ok := promotableConstant(err, specs); ok {
				specs[g].Kind = encoding.GroupKindIndexed
				promoted = append(promoted, g)
				rebuild = true
				if err := flattenStored(); err != nil {
					return nil, err
				}
				continue
			}
			if err != nil {
				return nil, shardErr(err, name)
			}
			out := enc.Schema()
			if m.incoming, err = assembleShard(out, spool); err != nil {
				return nil, err
			}
			m.canonical, incRows = out, rows
			break
		}

		// Rebuild: every shard re-encoded, fresh dictionaries in archive
		// order (the stored shards, then the arrival), so the canonical
		// dictionary is again a union built canonical-first.
		enc, err := encx.NewGroupEncoder(canonFlat, specs)
		if err != nil {
			return nil, err
		}
		spools := make([][]byte, len(stored))
		storedRows = 0
		retry := false
		for i, sh := range stored {
			spool, rows, err := encodeLogicalShard(enc, sh.payload)
			if g, ok := promotableConstant(err, specs); ok {
				specs[g].Kind = encoding.GroupKindIndexed
				promoted = append(promoted, g)
				retry = true
				break
			}
			if err != nil {
				return nil, shardErr(err, sh.name)
			}
			spools[i] = spool
			storedRows += rows
		}
		if retry {
			continue
		}
		spool, rows, err := encodeLogicalShard(enc, flatInc)
		if g, ok := promotableConstant(err, specs); ok {
			specs[g].Kind = encoding.GroupKindIndexed
			promoted = append(promoted, g)
			continue
		}
		if err != nil {
			return nil, shardErr(err, name)
		}
		out := enc.Schema()
		for i := range existing {
			payload, err := assembleShard(out, spools[i])
			if err != nil {
				return nil, err
			}
			existing[i].payload = payload
		}
		if m.incoming, err = assembleShard(out, spool); err != nil {
			return nil, err
		}
		m.canonical, incRows = out, rows
		break
	}

	// Rule 5: strict cohesion over the reconciled grouped schemas. The
	// arrival was encoded against the canonical layout, so this can only
	// fail on a reconciliation bug — which must refuse, not store.
	regrouped, err := readSinglePulseSchema(m.incoming)
	if err != nil {
		return nil, err
	}
	if _, err := encx.ValidateStructuralCohesion(m.canonical, regrouped); err != nil {
		return nil, err
	}

	if encx.ValidateGroupCohesion(canonical, incSchema) != nil {
		rec := GroupReconciliation{
			Reason:                RegroupIncomingRegrouped,
			Groups:                groupLabels(canonical, nil),
			ArchiveFormatVersion:  archVersion,
			IncomingFormatVersion: incVersion,
			ShardsRewritten:       1,
			RecordsRewritten:      incRows,
		}
		m.regrouped = append(m.regrouped, rec)
		m.warnings = append(m.warnings, groupsRewrittenWarning(rec, archivePath))
	}
	if len(promoted) > 0 {
		rec := GroupReconciliation{
			Reason:                RegroupConstantPromoted,
			ArchiveRewritten:      true,
			Groups:                groupLabels(canonical, promoted),
			ArchiveFormatVersion:  archVersion,
			IncomingFormatVersion: incVersion,
			ShardsRewritten:       len(existing) + 1,
			RecordsRewritten:      storedRows + incRows,
		}
		m.regrouped = append(m.regrouped, rec)
		m.warnings = append(m.warnings, groupsRewrittenWarning(rec, archivePath))
	}
	return m, nil
}

// encodeLogicalShard encodes a logical (0x01) shard's rows through enc
// and returns the physical rows and their count.
func encodeLogicalShard(enc *encx.GroupEncoder, flat []byte) ([]byte, int64, error) {
	r := bytes.NewReader(flat)
	if _, _, err := encx.ReadPreamble(r); err != nil {
		return nil, 0, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"reading shard preamble for regrouping")
	}
	var spool bytes.Buffer
	spool.Grow(r.Len())
	n, err := enc.EncodeStream(&spool, r)
	if err != nil {
		return nil, 0, err
	}
	return spool.Bytes(), n, nil
}

// assembleShard writes schema's preamble followed by the physical rows.
func assembleShard(schema *encoding.Schema, rows []byte) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(len(rows) + 4096)
	if err := encx.WritePreamble(&out, schema); err != nil {
		return nil, err
	}
	out.Write(rows)
	return out.Bytes(), nil
}

// promotableConstant reports whether err is a CONSTANT group seeing a
// second value — the one encoder refusal a shard merge resolves (by
// promoting the group to indexed) rather than propagates.
func promotableConstant(err error, specs []encx.GroupSpec) (int, bool) {
	if err == nil || !errors.HasCode(err, errors.PULSE_GROUP_MEMBER_NOT_CONSTANT) {
		return 0, false
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return 0, false
	}
	g, ok := ce.Details["group"].(int)
	if !ok || g < 0 || g >= len(specs) || specs[g].Kind != encoding.GroupKindConstant {
		return 0, false
	}
	return g, true
}

// shardErr names the shard a merge failure belongs to, and turns the
// encoder's u32 exhaustion into the archive vocabulary: a merged group
// dictionary past the index space is PULSE_SHARD_DICT_WIDTH_OVERFLOW,
// the same code a categorical or set union past its capacity carries.
func shardErr(err error, shard string) error {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return err
	}
	details := map[string]any{}
	for k, v := range ce.Details {
		details[k] = v
	}
	details["shard"] = shard
	if ce.Code == errors.PULSE_GROUP_ENTRIES_EXHAUSTED {
		details["capacity"] = encoding.MaxGroupEntries
		return errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW,
			fmt.Sprintf("merging shard %q: parent group dictionary union exceeds the u32 index space (%d entries): %s",
				shard, encoding.MaxGroupEntries, ce.Message),
			details)
	}
	out := errors.NewCodedErrorWithDetails(ce.Code, fmt.Sprintf("shard %q: %s", shard, ce.Message), details)
	out.Cause = ce.Cause
	return out
}

// groupLabels returns the labels of s's groups (all when only is nil).
func groupLabels(s *encoding.Schema, only []int) []string {
	var out []string
	if only == nil {
		for g := range s.Groups {
			out = append(out, encx.GroupSpecOf(s, g).Label(g))
		}
		return out
	}
	for _, g := range only {
		out = append(out, encx.GroupSpecOf(s, g).Label(g))
	}
	return out
}

// groupsRewrittenWarning renders the MANDATORY PULSE_SHARD_GROUPS_REWRITTEN
// warning. It names the reason and the cost — shards and records
// re-encoded, and whether the archive itself moved — for the reason
// PULSE_SHARD_SET_WIDENED exists: an expensive rewrite that happens
// silently is indistinguishable from a cheap append.
func groupsRewrittenWarning(r GroupReconciliation, archive string) encx.CohesionWarning {
	var msg string
	switch r.Reason {
	case RegroupIncomingFlattened:
		msg = fmt.Sprintf("the arriving shard is grouped (format 0x02) but %s is not; it was stored as its ungrouped twin (%d record(s) re-encoded, its parent groups dropped). The archive itself was not rewritten",
			archive, r.RecordsRewritten)
	case RegroupIncomingRegrouped:
		msg = fmt.Sprintf("the arriving shard's parent-group layout differs from %s's; it was re-encoded into the archive's groups before being stored (%d record(s) re-encoded). The archive itself was not rewritten",
			archive, r.RecordsRewritten)
	default:
		msg = fmt.Sprintf("the arriving shard holds a second value for constant parent group(s) %v, so they were promoted to indexed groups across the whole archive: every record of all %d shard(s) in %s was re-encoded (%d records, each row gains a 4-byte group index)",
			r.Groups, r.ShardsRewritten, archive, r.RecordsRewritten)
	}
	return encx.CohesionWarning{
		Code:    string(errors.PULSE_SHARD_GROUPS_REWRITTEN),
		Message: msg,
		Details: map[string]any{
			"reason":                  r.Reason,
			"archive_rewritten":       r.ArchiveRewritten,
			"groups":                  r.Groups,
			"archive_format_version":  r.ArchiveFormatVersion,
			"incoming_format_version": r.IncomingFormatVersion,
			"shards_rewritten":        r.ShardsRewritten,
			"records_rewritten":       r.RecordsRewritten,
			"archive":                 archive,
		},
	}
}
