package io

import (
	stderrors "errors"
	"fmt"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
)

// Source-zone datetime parsing (ImportJob.SourceTZ / ColumnSourceTZ /
// DSTPolicy). Every offset and DST decision is temporal.ParseLocal's; this
// file only resolves which field reads in which zone and turns temporal's
// typed outcome into the import's codes.

// DSTPolicy decides a naive source-zone wall clock a DST transition makes
// ambiguous or nonexistent. See ImportJob.DSTPolicy.
type DSTPolicy string

const (
	// DSTPolicyError refuses the import at the first such row (the
	// default; the empty string means it).
	DSTPolicyError DSTPolicy = "error"
	// DSTPolicyEarlier resolves with the pre-transition offset.
	DSTPolicyEarlier DSTPolicy = "earlier"
	// DSTPolicyLater resolves with the post-transition offset.
	DSTPolicyLater DSTPolicy = "later"
)

// DetailOption marks a refusal of an import OPTION (rather than of the
// data): its details carry DetailOption = the option's name, so an adapter
// (the CLI) can tell a mistyped flag value from a bad source.
const DetailOption = "option"

// sourceZones is one pass's per-field source-zone context: zones[i] is the
// zone field i's naive datetime literals read in, nil for every field
// that takes today's naive-UTC path (no zone, a UTC-equivalent zone, a
// non-datetime field). It counts the values a non-default policy
// resolved. A nil *sourceZones is "no source zone anywhere". Single
// goroutine: the zones are forks owned by this pass.
type sourceZones struct {
	zones  []*temporal.Zone
	names  []string
	policy temporal.Ambiguity

	ambiguousN, nonexistentN int
}

// zoneRefusal is an option refusal: SERVICE_VALIDATION naming the option.
func zoneRefusal(option, msg string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details[DetailOption] = option
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg, details)
}

// parseDSTPolicy maps the public spelling onto temporal's policy.
func parseDSTPolicy(p DSTPolicy) (temporal.Ambiguity, error) {
	a, ok := temporal.ParseAmbiguity(string(p))
	if !ok {
		return 0, zoneRefusal("dst_policy",
			fmt.Sprintf("unknown DST policy %q: use \"error\" (the default), \"earlier\" or \"later\"", string(p)),
			map[string]any{"value": string(p)})
	}
	return a, nil
}

// resolveSourceZones validates the job's source-zone options against the
// resolved schema and returns the per-field context, or nil when no field
// reads in a non-UTC zone — the caller then runs today's path unchanged.
// Refusals, all before the row pass: an unknown DSTPolicy or a
// ColumnSourceTZ key naming no field or a non-datetime field
// (SERVICE_VALIDATION, details carry DetailOption), an unknown zone
// (PULSE_TIMEZONE_UNKNOWN).
func (j *ImportJob) resolveSourceZones(schema *encoding.Schema) (*sourceZones, error) {
	policy, err := parseDSTPolicy(j.DSTPolicy)
	if err != nil {
		return nil, err
	}
	if j.SourceTZ == "" && len(j.ColumnSourceTZ) == 0 {
		return nil, nil
	}
	byName := make(map[string]int, len(schema.Fields))
	for i := range schema.Fields {
		byName[schema.Fields[i].Name] = i
	}
	cols := make([]string, 0, len(j.ColumnSourceTZ))
	for c := range j.ColumnSourceTZ {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	for _, c := range cols {
		i, ok := byName[c]
		if !ok {
			return nil, zoneRefusal("source_tz",
				fmt.Sprintf("source time zone names column %q, which the source does not carry", c),
				map[string]any{"column": c})
		}
		if ft := schema.Fields[i].Type; ft != encoding.FieldTypeDateTime {
			return nil, zoneRefusal("source_tz",
				fmt.Sprintf("source time zone names column %q, which is %s, not datetime; a source zone applies only to datetime columns (a date is a calendar day and has no zone)", c, ft),
				map[string]any{"column": c, "type": ft.String()})
		}
	}

	loaded := map[string]*temporal.Zone{}
	load := func(name string) (*temporal.Zone, error) {
		if z, ok := loaded[name]; ok {
			return z, nil
		}
		z, err := temporal.LoadImportZone(name)
		if err != nil {
			return nil, err
		}
		loaded[name] = z
		return z, nil
	}
	sz := &sourceZones{
		zones:  make([]*temporal.Zone, len(schema.Fields)),
		names:  make([]string, len(schema.Fields)),
		policy: policy,
	}
	zonedAny := false
	for i := range schema.Fields {
		if schema.Fields[i].Type != encoding.FieldTypeDateTime {
			continue
		}
		name, ok := j.ColumnSourceTZ[schema.Fields[i].Name]
		if !ok {
			name = j.SourceTZ
		}
		if name == "" {
			continue
		}
		z, err := load(name)
		if err != nil {
			return nil, err
		}
		if z.IsUTC() {
			// Naive-as-UTC is today's answer; keep today's path.
			continue
		}
		sz.zones[i], sz.names[i] = z.Fork(), name
		zonedAny = true
	}
	// A zone naming no datetime field still had to be valid (above).
	if j.SourceTZ != "" {
		if _, err := load(j.SourceTZ); err != nil {
			return nil, err
		}
	}
	if !zonedAny {
		return nil, nil
	}
	return sz, nil
}

// zoned reports whether field i reads in a source zone.
func (sz *sourceZones) zoned(i int) bool {
	return sz != nil && sz.zones[i] != nil
}

// parse converts raw, a present cell of zoned field i (column name col,
// 1-based source row rowNum), to the on-wire datetime value. A literal no
// layout accepts returns ParseDateTime's own error (a row error, as
// today); a DST refusal returns the fatal PULSE_IMPORT_DST_* code.
func (sz *sourceZones) parse(i int, raw, col string, rowNum int) (uint64, error) {
	sec, kind, err := temporal.ParseLocal(raw, sz.zones[i], sz.policy)
	if err != nil {
		code := errors.Code("")
		what := ""
		switch {
		case stderrors.Is(err, temporal.ErrLocalAmbiguous):
			code, what = errors.PULSE_IMPORT_DST_AMBIGUOUS, "is ambiguous: the zone shows it twice (a DST fall-back)"
		case stderrors.Is(err, temporal.ErrLocalNonexistent):
			code, what = errors.PULSE_IMPORT_DST_NONEXISTENT, "does not exist: the zone skips it (a DST spring-forward)"
		default:
			return 0, err
		}
		return 0, errors.NewCodedErrorWithDetails(code,
			fmt.Sprintf("row %d, column %q: local time %q in %s %s; choose a DST policy (earlier or later) or give the value an offset", rowNum, col, raw, sz.names[i], what),
			map[string]any{"row": rowNum, "column": col, "value": raw, "zone": sz.names[i]})
	}
	switch kind {
	case temporal.LocalAmbiguous:
		sz.ambiguousN++
	case temporal.LocalNonexistent:
		sz.nonexistentN++
	}
	return uint64(sec), nil
}

// resetCounts forgets the resolved counts (a re-measured predict pass
// counts again from the top).
func (sz *sourceZones) resetCounts() {
	if sz != nil {
		sz.ambiguousN, sz.nonexistentN = 0, 0
	}
}

// warnings returns the one PULSE_IMPORT_DST_RESOLVED warning when the
// policy resolved anything, nil otherwise.
func (sz *sourceZones) warnings() []*errors.CodedError {
	if sz == nil || sz.ambiguousN+sz.nonexistentN == 0 {
		return nil
	}
	return []*errors.CodedError{errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_DST_RESOLVED,
		fmt.Sprintf("DST policy %q resolved %d ambiguous and %d nonexistent source-zone local times", sz.policy, sz.ambiguousN, sz.nonexistentN),
		map[string]any{"policy": sz.policy.String(), "ambiguous_n": sz.ambiguousN, "nonexistent_n": sz.nonexistentN})}
}

// isDSTRefusal reports whether err is a fatal source-zone DST refusal.
func isDSTRefusal(err error) bool {
	var ce *errors.CodedError
	return stderrors.As(err, &ce) &&
		(ce.Code == errors.PULSE_IMPORT_DST_AMBIGUOUS || ce.Code == errors.PULSE_IMPORT_DST_NONEXISTENT)
}
