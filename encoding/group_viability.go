package encoding

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/errors"
)

// Per-group viability gate (format 0x02).
//
// A badly chosen parent group is a SILENT pessimization, not an error:
// every row still pays GroupIndexWidth bytes for its index, the
// dictionary holds one entry per distinct tuple, and the file can come
// out bigger than the flat cohort. The dictionary is also fully
// resident, so a low-ratio group trades an O(1) streaming profile for
// O(distinct tuples) of memory. The gate judges each declared INDEXED
// group on its own numbers — never the cohort as a whole — in two
// steps, and is the ONE policy shared by import-time declaration and
// retro-dedup so the two cannot drift:
//
//  1. Width floor (schema-only, before the row pass): a group whose
//     members occupy no more bytes per row than the index that replaces
//     them can never save a byte at any ratio. It is DROPPED — its
//     members stay row fields — with PULSE_GROUP_TOO_NARROW naming the
//     group and both widths. Other groups on the same import proceed.
//  2. Ratio floor (after the dictionary is built): a group whose
//     measured ratio (rows ÷ distinct tuples) is below the floor, or
//     whose deduped bytes are not smaller than its flat bytes, draws
//     PULSE_DEDUP_LOW_RATIO carrying the ratio, the resident dictionary
//     bytes and the byte delta. The group is STILL WRITTEN: a
//     deliberately deduped low-ratio group is a legitimate choice, and
//     a floor cannot be picked honestly for every cohort, so ratio alone
//     never refuses.
//
// Under DedupGate.Strict either finding is returned as a fatal
// *errors.CodedError with the same code instead of a warning — the
// --strict posture predict already applies to its warnings.
// Constant groups (elision) are never gated: they have no per-row index
// and the elision planner already refuses one that would not shrink the
// file.

// DefaultDedupRatioFloor is the ratio floor DedupGate applies when none
// is configured: 2 rows per distinct tuple.
//
// Why 2. For a group whose members are wide against the 4-byte index,
// the resident dictionary is (member bytes per row) × rows ÷ ratio, so
// the ratio is the factor by which the parent block shrinks: at 2 the
// dictionary still holds half the bytes the rows used to carry, and
// below it most of the parent data becomes resident memory while the
// saving tends to zero (a ratio of 1.25 keeps 80% of it resident for a
// 20% cut). A floor of 2 therefore reads "each stored tuple is shared
// by at least two rows on average" and flags exactly the case the
// planning measurements called out — the same declaration measured
// 2.73x on one cohort (passes) and 1.24x on another (warns). It is a
// judgement, not a law: it is overridable (DedupGate.RatioFloor, CLI
// --dedup-ratio-floor) and only ever warns. The grows-the-file check
// is separate and applies at any floor, because a narrow-ish group
// (members only slightly wider than the index) can grow the file at a
// ratio well above 2 — its break-even ratio is reported alongside.
const DefaultDedupRatioFloor = 2.0

// Group viability verdicts, as reported per group.
const (
	// GroupVerdictAdmitted: the group is written and passed both floors.
	GroupVerdictAdmitted = "admitted"
	// GroupVerdictLowRatio: the group is written, with a
	// PULSE_DEDUP_LOW_RATIO warning.
	GroupVerdictLowRatio = "low_ratio"
	// GroupVerdictDroppedTooNarrow: the group was not formed; its
	// members stay row fields (PULSE_GROUP_TOO_NARROW).
	GroupVerdictDroppedTooNarrow = "dropped_too_narrow"
)

// GroupViability is one group's gate verdict and the numbers behind it.
// Every byte figure is exact for the group in isolation; the per-row
// null bitmap is left out (moving a nullable member out of the row can
// shrink it by at most one byte per row, so ByteDelta can only
// understate a saving, never invent one).
type GroupViability struct {
	// Group is the 0-based position in the spec list given to the gate.
	Group int `json:"group"`
	// Label names the group ("group 2 [key: order_id]").
	Label string `json:"label"`
	// Verdict is one of the GroupVerdict* constants.
	Verdict string `json:"verdict"`
	// Reason is the coded finding behind a non-admitted verdict
	// (PULSE_GROUP_TOO_NARROW or PULSE_DEDUP_LOW_RATIO); empty when
	// admitted.
	Reason string `json:"reason,omitempty"`
	// Rows is the record count the ratio was measured over (0 for a
	// group dropped before the row pass).
	Rows int64 `json:"rows"`
	// EntryCount is the number of distinct tuples (0 when dropped).
	EntryCount int `json:"entry_count"`
	// EntryWidth is the bytes per dictionary entry.
	EntryWidth int `json:"entry_width"`
	// MemberRowBytes is the bytes the members occupy in a flat row.
	MemberRowBytes int `json:"member_row_bytes"`
	// IndexWidth is the bytes of the per-row index replacing them.
	IndexWidth int `json:"index_width"`
	// DictionaryBytes is EntryCount × EntryWidth: what the group holds
	// RESIDENT in memory for as long as the cohort is open.
	DictionaryBytes int64 `json:"dictionary_bytes"`
	// DescriptorBytes is the group's descriptor in the schema block,
	// dictionary excluded.
	DescriptorBytes int `json:"descriptor_bytes"`
	// Ratio is Rows ÷ EntryCount (0 when not measured).
	Ratio float64 `json:"ratio"`
	// BreakEvenRatio is the ratio at which the dictionary exactly eats
	// the per-row saving: EntryWidth ÷ (MemberRowBytes − IndexWidth).
	// 0 when the group is too narrow to ever break even.
	BreakEvenRatio float64 `json:"break_even_ratio"`
	// RatioFloor is the floor the ratio was judged against.
	RatioFloor float64 `json:"ratio_floor"`
	// UndedupedBytes is Rows × MemberRowBytes: the members stored flat.
	UndedupedBytes int64 `json:"undeduped_bytes"`
	// DedupedBytes is Rows × IndexWidth + DictionaryBytes +
	// DescriptorBytes: the same information grouped.
	DedupedBytes int64 `json:"deduped_bytes"`
	// ByteDelta is DedupedBytes − UndedupedBytes: negative is a saving,
	// zero or positive means grouping made the file no smaller.
	ByteDelta int64 `json:"byte_delta"`
}

// Viable reports whether the group was written without a finding.
func (v GroupViability) Viable() bool { return v.Verdict == GroupVerdictAdmitted }

// DedupGate is the per-group viability policy. The zero value is the
// default: DefaultDedupRatioFloor, findings as warnings.
type DedupGate struct {
	// RatioFloor is the rows-per-tuple floor below which a group draws
	// PULSE_DEDUP_LOW_RATIO. Zero, negative or NaN selects
	// DefaultDedupRatioFloor. A floor in (0, 1] disables the
	// floor — every ratio is ≥ 1 — leaving only the grows-the-file
	// check.
	RatioFloor float64
	// Strict returns the first finding as a fatal error with the same
	// code instead of a warning.
	Strict bool
}

// Floor returns the effective ratio floor.
func (gt DedupGate) Floor() float64 {
	if gt.RatioFloor > 0 && !math.IsNaN(gt.RatioFloor) && !math.IsInf(gt.RatioFloor, 0) {
		return gt.RatioFloor
	}
	return DefaultDedupRatioFloor
}

// ScreenWidths applies the width floor to specs over flat, an
// ungrouped schema, before any row is read. It returns the specs that
// survive (in order, each with Ordinal set to its 1-based position in
// specs unless already set, so labels do not shift when an earlier
// group is dropped), one GroupViability per INPUT spec (dropped ones
// carry their widths; admitted ones are completed by AssessRatios), and
// one PULSE_GROUP_TOO_NARROW warning per dropped group. Under Strict the
// first narrow group is returned as an error instead. Constant specs
// pass through unjudged. An unknown member name is
// PULSE_GROUP_FIELD_UNKNOWN (NewGroupEncoder is the full declaration
// check; callers normally run it first).
func (gt DedupGate) ScreenWidths(flat *Schema, specs []GroupSpec) ([]GroupSpec, []GroupViability, []*errors.CodedError, error) {
	byName := make(map[string]int, len(flat.Fields))
	for i := range flat.Fields {
		byName[flat.Fields[i].Name] = i
	}
	var (
		admitted []GroupSpec
		views    = make([]GroupViability, len(specs))
		warns    []*errors.CodedError
	)
	for g, sp := range specs {
		if sp.Ordinal == 0 {
			sp.Ordinal = g + 1
		}
		label := sp.Label(g)
		memberBytes, entryWidth := 0, 0
		nullable := false
		for _, name := range sp.Members {
			fi, ok := byName[name]
			if !ok {
				return nil, nil, nil, declErr(errors.PULSE_GROUP_FIELD_UNKNOWN,
					fmt.Sprintf("%s names field %q, which is not in the schema", label, name),
					map[string]any{"group": g, "group_label": label, "field": name})
			}
			memberBytes += onWireWidth(flat.Fields[fi].Type)
			nullable = nullable || flat.Fields[fi].Nullable
		}
		entryWidth = memberBytes
		if nullable {
			entryWidth += (len(sp.Members) + 7) / 8
		}
		v := GroupViability{
			Group:           g,
			Label:           label,
			Verdict:         GroupVerdictAdmitted,
			EntryWidth:      entryWidth,
			MemberRowBytes:  memberBytes,
			IndexWidth:      GroupIndexWidth,
			DescriptorBytes: groupDescriptorBytes(len(sp.Members)),
			RatioFloor:      gt.Floor(),
		}
		if sp.Kind != GroupKindIndexed {
			v.IndexWidth = 0
			views[g] = v
			admitted = append(admitted, sp)
			continue
		}
		if memberBytes > GroupIndexWidth {
			v.BreakEvenRatio = breakEven(entryWidth, memberBytes)
			views[g] = v
			admitted = append(admitted, sp)
			continue
		}
		v.Verdict = GroupVerdictDroppedTooNarrow
		v.Reason = string(errors.PULSE_GROUP_TOO_NARROW)
		views[g] = v
		ce := errors.NewCodedErrorWithDetails(errors.PULSE_GROUP_TOO_NARROW,
			fmt.Sprintf("parent group: %s dropped: its members are %d bytes per row, no wider than the %d-byte index that would replace them, so grouping could only grow the file; its members stay in the row",
				label, memberBytes, GroupIndexWidth),
			map[string]any{
				"group":            g,
				"group_label":      label,
				"member_row_bytes": memberBytes,
				"index_width":      GroupIndexWidth,
				"entry_width":      entryWidth,
			})
		if gt.Strict {
			return nil, nil, nil, ce
		}
		warns = append(warns, ce)
	}
	return admitted, views, warns, nil
}

// AssessRatios applies the ratio floor to the first len(specs) groups
// of grouped, a schema whose dictionaries hold rows records (the
// schema the encoder produced). specs are the admitted specs in group
// order — used only for labels. It returns one GroupViability per spec
// and one PULSE_DEDUP_LOW_RATIO warning per finding; under Strict the
// first finding is returned as an error instead. With rows == 0 nothing
// is measured and nothing is flagged. Constant groups are reported
// admitted, unjudged.
func (gt DedupGate) AssessRatios(grouped *Schema, specs []GroupSpec, rows int64) ([]GroupViability, []*errors.CodedError, error) {
	views := make([]GroupViability, len(specs))
	var warns []*errors.CodedError
	floor := gt.Floor()
	for g, sp := range specs {
		v := AssessGroup(grouped, g, rows, floor)
		v.Label = sp.Label(g)
		if sp.Ordinal > 0 {
			v.Group = sp.Ordinal - 1
		}
		if grouped.Groups[g].Kind != GroupKindIndexed || rows == 0 {
			views[g] = v
			continue
		}
		grows := v.ByteDelta >= 0
		if v.Ratio >= floor && !grows {
			views[g] = v
			continue
		}
		v.Verdict = GroupVerdictLowRatio
		v.Reason = string(errors.PULSE_DEDUP_LOW_RATIO)
		views[g] = v
		ce := lowRatioError(v, grows)
		if gt.Strict {
			return nil, nil, ce
		}
		warns = append(warns, ce)
	}
	return views, warns, nil
}

// AssessGroup measures group g of grouped over rows records against
// floor, without judging it (Verdict is admitted, Label empty). Any
// caller that reports ratio figures — import predict, retro-dedup —
// derives them here so the arithmetic exists once.
func AssessGroup(grouped *Schema, g int, rows int64, floor float64) GroupViability {
	idx := 0
	if grouped.Groups[g].Kind == GroupKindIndexed {
		idx = GroupIndexWidth
	}
	v := GroupViability{
		Group:           g,
		Verdict:         GroupVerdictAdmitted,
		Rows:            rows,
		EntryCount:      grouped.GroupEntryCount(g),
		EntryWidth:      grouped.GroupEntryWidth(g),
		MemberRowBytes:  grouped.GroupMemberRowBytes(g),
		IndexWidth:      idx,
		DescriptorBytes: groupDescriptorBytes(len(grouped.Groups[g].Members)),
		RatioFloor:      floor,
	}
	v.DictionaryBytes = int64(v.EntryCount) * int64(v.EntryWidth)
	if v.EntryCount > 0 {
		v.Ratio = float64(rows) / float64(v.EntryCount)
	}
	if idx > 0 {
		v.BreakEvenRatio = breakEven(v.EntryWidth, v.MemberRowBytes)
	}
	v.UndedupedBytes = rows * int64(v.MemberRowBytes)
	v.DedupedBytes = rows*int64(idx) + v.DictionaryBytes + int64(v.DescriptorBytes)
	v.ByteDelta = v.DedupedBytes - v.UndedupedBytes
	return v
}

// breakEven is entryWidth ÷ (memberBytes − GroupIndexWidth), or 0 when
// the members are no wider than the index (no ratio breaks even).
func breakEven(entryWidth, memberBytes int) float64 {
	if memberBytes <= GroupIndexWidth {
		return 0
	}
	return float64(entryWidth) / float64(memberBytes-GroupIndexWidth)
}

func lowRatioError(v GroupViability, grows bool) *errors.CodedError {
	why := fmt.Sprintf("dedup ratio %.2fx is below the %.2fx floor", v.Ratio, v.RatioFloor)
	if grows {
		why = fmt.Sprintf("dedup ratio %.2fx is at or below its %.2fx break-even, so grouping makes the file no smaller", v.Ratio, v.BreakEvenRatio)
		if v.Ratio < v.RatioFloor {
			why += fmt.Sprintf(" (and below the %.2fx floor)", v.RatioFloor)
		}
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_DEDUP_LOW_RATIO,
		fmt.Sprintf("parent group: %s: %s — %d rows share %d distinct tuples; the dictionary holds %d bytes resident; the group changes the file by %+d bytes versus storing its members per row",
			v.Label, why, v.Rows, v.EntryCount, v.DictionaryBytes, v.ByteDelta),
		map[string]any{
			"group":            v.Group,
			"group_label":      v.Label,
			"rows":             v.Rows,
			"entry_count":      v.EntryCount,
			"ratio":            v.Ratio,
			"ratio_floor":      v.RatioFloor,
			"break_even_ratio": v.BreakEvenRatio,
			"grows_file":       grows,
			"dictionary_bytes": v.DictionaryBytes,
			"undeduped_bytes":  v.UndedupedBytes,
			"deduped_bytes":    v.DedupedBytes,
			"byte_delta":       v.ByteDelta,
			"member_row_bytes": v.MemberRowBytes,
			"entry_width":      v.EntryWidth,
			"index_width":      v.IndexWidth,
		})
}
