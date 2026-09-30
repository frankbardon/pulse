package io

import (
	stderrors "errors"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Import-time parent-group declaration (format 0x02).
//
// A parent group is a set of fields that repeat as a unit — the parent
// block of a denormalised join. Declaring one at import stores each
// distinct member tuple ONCE in the schema block and gives every row a
// u32 index into it (see encoding/group.go). Declaration is the only
// way a group is formed: import infers from a bounded sample, which can
// nominate a parent block but never confirm one, so nothing here is
// automatic.

// GroupDecl declares one parent group by field NAME.
//
// Key names the fields that identify the parent (e.g. its ID); Members
// names the fields the key DETERMINES (the parent's attributes). The
// group stores Key ∪ Members. With a Key, every row that carries a key
// tuple already seen must agree with that earlier row on every Member —
// value and null state — or the import fails with
// PULSE_GROUP_MEMBER_NOT_CONSTANT naming the member and the row: a
// declaration that is not actually a parent group is refused, never
// silently turned into a larger dictionary. With no Key, Members is a
// plain tuple group: each distinct combination is one entry and there
// is nothing to violate.
type GroupDecl struct {
	Key     []string `json:"key,omitempty"`
	Members []string `json:"members"`
}

// spec lowers the declaration to the encoder's GroupSpec: Members =
// Key ∪ Members (key first), Key = Key.
func (d GroupDecl) spec() encoding.GroupSpec {
	sp := encoding.GroupSpec{Kind: encoding.GroupKindIndexed}
	sp.Members = append(append(sp.Members, d.Key...), d.Members...)
	if len(d.Key) > 0 {
		sp.Key = append([]string(nil), d.Key...)
	}
	return sp
}

// groupSpecs lowers every declaration.
func groupSpecs(decls []GroupDecl) []encoding.GroupSpec {
	if len(decls) == 0 {
		return nil
	}
	out := make([]encoding.GroupSpec, len(decls))
	for i, d := range decls {
		out[i] = d.spec()
	}
	return out
}

// ParseGroupDecl parses the CLI form of one group declaration:
//
//	KEY[,KEY...]:MEMBER[,MEMBER...]   a keyed group (members determined by the key)
//	MEMBER[,MEMBER...]                a plain tuple group (no key check)
//
// Names are trimmed of surrounding whitespace. An empty name, an empty
// side of the colon, or more than one colon is
// PULSE_GROUP_DECLARATION_INVALID. Field names containing ',' or ':'
// cannot be written in this form; use ImportJob.Groups directly.
func ParseGroupDecl(s string) (GroupDecl, error) {
	bad := func(msg string) (GroupDecl, error) {
		return GroupDecl{}, errors.NewCodedErrorWithDetails(errors.PULSE_GROUP_DECLARATION_INVALID,
			"parent group declaration "+strconv.Quote(s)+": "+msg,
			map[string]any{"declaration": s})
	}
	names := func(part string) ([]string, bool) {
		var out []string
		for _, n := range strings.Split(part, ",") {
			n = strings.TrimSpace(n)
			if n == "" {
				return nil, false
			}
			out = append(out, n)
		}
		return out, true
	}
	switch strings.Count(s, ":") {
	case 0:
		m, ok := names(s)
		if !ok {
			return bad("expected MEMBER[,MEMBER...] with no empty names")
		}
		return GroupDecl{Members: m}, nil
	case 1:
		i := strings.IndexByte(s, ':')
		k, okK := names(s[:i])
		m, okM := names(s[i+1:])
		if !okK || !okM {
			return bad("expected KEY[,KEY...]:MEMBER[,MEMBER...] with no empty names on either side")
		}
		return GroupDecl{Key: k, Members: m}, nil
	default:
		return bad("more than one ':' — expected KEY[,KEY...]:MEMBER[,MEMBER...]")
	}
}

// GroupReport describes one declared group: its verdict from the
// per-group viability gate (encoding.DedupGate) and the numbers behind
// it. A dropped group (verdict dropped_too_narrow) was not formed — its
// members stay row fields — and carries only its widths; an admitted
// or low_ratio group was written and carries the measured ratio, the
// dictionary it holds resident and the byte delta versus storing its
// members per row.
type GroupReport struct {
	// Label names the group in errors and output ("group 1 [key: id]").
	Label string `json:"label"`
	// Key and Members are as declared; Fields is every member in
	// logical (schema) order.
	Key     []string `json:"key,omitempty"`
	Members []string `json:"members"`
	Fields  []string `json:"fields"`
	// Verdict is admitted, low_ratio (written, with a
	// PULSE_DEDUP_LOW_RATIO warning) or dropped_too_narrow (not
	// written, PULSE_GROUP_TOO_NARROW). Reason is the code behind a
	// non-admitted verdict.
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
	// EntryCount is the number of distinct tuples stored.
	EntryCount int `json:"entry_count"`
	// EntryWidth is the bytes per dictionary entry (member bytes plus a
	// member null bitmap when any member is nullable).
	EntryWidth int `json:"entry_width"`
	// MemberRowBytes is the bytes the members occupied in each flat row.
	MemberRowBytes int `json:"member_row_bytes"`
	// IndexWidth is the bytes of the per-row index that replaces them.
	IndexWidth int `json:"index_width"`
	// DictionaryBytes is EntryCount × EntryWidth, resident in memory
	// whenever the cohort is open.
	DictionaryBytes int64 `json:"dictionary_bytes"`
	// Ratio is rows ÷ EntryCount; BreakEvenRatio the ratio below which
	// the group grows the file; RatioFloor the floor it was judged
	// against.
	Ratio          float64 `json:"ratio"`
	BreakEvenRatio float64 `json:"break_even_ratio"`
	RatioFloor     float64 `json:"ratio_floor"`
	// ByteDelta is the grouped bytes (indexes + dictionary + descriptor)
	// minus the members stored per row: negative is a saving.
	ByteDelta int64 `json:"byte_delta"`
}

// groupReports describes every declaration, in declaration order,
// from the width screen (one view per declaration) and the ratio
// assessment (one view per ADMITTED declaration, in order). Admitted
// declarations are the written schema's first groups.
func groupReports(written *encoding.Schema, decls []GroupDecl, screen, ratio []encoding.GroupViability) []GroupReport {
	if len(decls) == 0 {
		return nil
	}
	out := make([]GroupReport, len(decls))
	wg := 0
	for i, d := range decls {
		v := screen[i]
		admitted := v.Verdict != encoding.GroupVerdictDroppedTooNarrow
		if admitted {
			v = ratio[wg]
		}
		gr := GroupReport{
			Label:           v.Label,
			Key:             d.Key,
			Members:         d.Members,
			Verdict:         v.Verdict,
			Reason:          v.Reason,
			EntryCount:      v.EntryCount,
			EntryWidth:      v.EntryWidth,
			MemberRowBytes:  v.MemberRowBytes,
			IndexWidth:      v.IndexWidth,
			DictionaryBytes: v.DictionaryBytes,
			Ratio:           v.Ratio,
			BreakEvenRatio:  v.BreakEvenRatio,
			RatioFloor:      v.RatioFloor,
			ByteDelta:       v.ByteDelta,
		}
		if admitted {
			for _, m := range written.Groups[wg].Members {
				gr.Fields = append(gr.Fields, written.Fields[m.Field].Name)
			}
			wg++
		} else {
			gr.Fields = append(append(gr.Fields, d.Key...), d.Members...)
		}
		out[i] = gr
	}
	return out
}

// groupMemberNames is every field an admitted group claims — the
// reserved set constant elision must not touch. A group the gate
// dropped reserves nothing: its members are ordinary row fields again.
func groupMemberNames(specs []encoding.GroupSpec) []string {
	var out []string
	for _, sp := range specs {
		out = append(out, sp.Members...)
	}
	return out
}

// screenGroups validates the declared groups against schema and runs
// the viability gate's width screen before the row pass (fail fast).
// widenable marks fields the row pass may still promote: a promotion
// only ever widens a group, so a --strict refusal of a group with a
// widenable member is not final yet. Such a screen runs non-strict and
// rescreen reports that the caller must repeat it, strict, over the
// final schema after the pass. With nothing declared everything is
// nil.
func (j *ImportJob) screenGroups(schema *encoding.Schema, widenable []bool) (specs []encoding.GroupSpec, views []encoding.GroupViability, warns []*errors.CodedError, rescreen bool, err error) {
	declared := groupSpecs(j.Groups)
	if len(declared) == 0 {
		return nil, nil, nil, false, nil
	}
	if _, err := encoding.NewGroupEncoder(schema, declared); err != nil {
		return nil, nil, nil, false, err
	}
	gate := j.dedupGate()
	if gate.Strict {
		byName := make(map[string]int, len(schema.Fields))
		for i := range schema.Fields {
			byName[schema.Fields[i].Name] = i
		}
		for _, sp := range declared {
			for _, m := range sp.Members {
				if fi, ok := byName[m]; ok && widenable[fi] {
					rescreen = true
				}
			}
		}
		gate.Strict = !rescreen
	}
	specs, views, warns, err = gate.ScreenWidths(schema, declared)
	return specs, views, warns, rescreen, err
}

// dedupGate is the viability policy this job's options select.
func (j *ImportJob) dedupGate() encoding.DedupGate {
	return encoding.DedupGate{RatioFloor: j.DedupRatioFloor, Strict: j.StrictDedup}
}

// withSourceRow adds details["source_row"] — the 1-based data row of the
// SOURCE, the numbering RowError.Row uses — to a group encode failure
// that names a record index. Records are the rows that imported, so a
// record index skips every row-error row before it.
func withSourceRow(err error, rowErrors []RowError) error {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Details == nil {
		return err
	}
	rec, ok := ce.Details["row"].(int64)
	if !ok {
		return err
	}
	src := int(rec) + 1
	for _, re := range rowErrors {
		if re.Row > src {
			break
		}
		src++
	}
	ce.Details["source_row"] = src
	return err
}
