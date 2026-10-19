package io

import (
	stderrors "errors"
	"fmt"
	"sort"
	"strings"

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
//
// Under ImportJob.SourceTZField, perRow[i] marks the datetime fields that
// read in the zone their own row names (zones[i] stays nil for them):
// beginRow hands the pass's current source row over, and the row's zone
// is resolved from its tzCol cell only when a naive literal needs it,
// through memo (one fork per distinct name, unknown names remembered as
// their refusal) — never a per-row zone load.
type sourceZones struct {
	zones  []*temporal.Zone
	names  []string
	policy temporal.Ambiguity

	ambiguousN, nonexistentN int

	perRow       []bool
	tzCol        int
	tzField      string
	fallback     *temporal.Zone
	fallbackName string
	utc          *temporal.Zone
	memo         map[string]rowZone
	row          []string
}

// rowZone is one memoised zone-cell verdict: the fork, or the load's
// refusal.
type rowZone struct {
	z   *temporal.Zone
	err error
}

// detailZoneColumn is the details key naming the SourceTZField column on
// a per-row zone row error; its presence is what marks one (isZoneRowError).
const detailZoneColumn = "zone_column"

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
	if j.SourceTZ == "" && len(j.ColumnSourceTZ) == 0 && j.SourceTZField == "" {
		return nil, nil
	}
	byName := make(map[string]int, len(schema.Fields))
	for i := range schema.Fields {
		byName[schema.Fields[i].Name] = i
	}
	tzIdx := -1
	if c := j.SourceTZField; c != "" {
		i, ok := byName[c]
		if !ok {
			return nil, zoneRefusal("source_tz_field",
				fmt.Sprintf("source time zone field %q names a column the source does not carry (or the schema does not import)", c),
				map[string]any{"column": c})
		}
		if ft := schema.Fields[i].Type; ft == encoding.FieldTypeDateTime || ft.IsVector() {
			return nil, zoneRefusal("source_tz_field",
				fmt.Sprintf("source time zone field %q is %s; it must be a text column holding a zone name (IANA, \"UTC\" or ±HH:MM) per row", c, ft),
				map[string]any{"column": c, "type": ft.String()})
		}
		tzIdx = i
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
		if !ok && tzIdx >= 0 {
			// Read in the row's own zone (ColumnSourceTZ wins above).
			if sz.perRow == nil {
				sz.perRow = make([]bool, len(schema.Fields))
			}
			sz.perRow[i] = true
			zonedAny = true
			continue
		}
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
		z, err := load(j.SourceTZ)
		if err != nil {
			return nil, err
		}
		if sz.perRow != nil {
			sz.fallback, sz.fallbackName = z.Fork(), j.SourceTZ
		}
	}
	if sz.perRow != nil {
		utc, err := temporal.LoadZone("UTC")
		if err != nil {
			return nil, err
		}
		sz.utc = utc
		sz.tzCol = schema.Fields[tzIdx].CsvColumnIdx
		sz.tzField = j.SourceTZField
		sz.memo = map[string]rowZone{}
		for name, z := range loaded {
			// Names the options already loaded seed the memo.
			sz.memo[name] = rowZone{z: z.Fork()}
		}
	}
	if !zonedAny {
		return nil, nil
	}
	return sz, nil
}

// zoned reports whether field i reads in a source zone.
func (sz *sourceZones) zoned(i int) bool {
	return sz != nil && (sz.zones[i] != nil || sz.perRow != nil && sz.perRow[i])
}

// beginRow hands the pass's current source row to a per-row-zone pass
// (ImportJob.SourceTZField); every caller converting zoned cells calls it
// once per row before the first cell. A no-op otherwise.
func (sz *sourceZones) beginRow(row []string) {
	if sz != nil && sz.perRow != nil {
		sz.row = row
	}
}

// zoneFor returns the zone (and its spelling) zoned field i reads in on
// the current row. A fixed field returns its own; a per-row field resolves
// the row's zone cell: an empty / null-token cell takes the SourceTZ
// fallback, else is a PULSE_IMPORT_ROW_ERROR; an unknown name is a
// PULSE_TIMEZONE_UNKNOWN row error. Both carry detailZoneColumn.
func (sz *sourceZones) zoneFor(i int, col string, rowNum int) (*temporal.Zone, string, error) {
	if sz.zones[i] != nil {
		return sz.zones[i], sz.names[i], nil
	}
	var cell string
	if sz.tzCol < len(sz.row) {
		cell = strings.TrimSpace(sz.row[sz.tzCol])
	}
	if isNullToken(cell) {
		if sz.fallback != nil {
			return sz.fallback, sz.fallbackName, nil
		}
		reason := fmt.Sprintf("zone column %q is empty on this row and no source_tz fallback is set", sz.tzField)
		return nil, "", errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_ROW_ERROR,
			fmt.Sprintf("row %d, column %q: %s", rowNum, col, reason),
			map[string]any{"row": rowNum, "column": col, "reason": reason, detailZoneColumn: sz.tzField})
	}
	m, ok := sz.memo[cell]
	if !ok {
		z, err := temporal.LoadImportZone(cell)
		if err != nil {
			m.err = err
		} else {
			m.z = z.Fork()
		}
		sz.memo[cell] = m
	}
	if m.err != nil {
		reason := m.err.Error()
		var ce *errors.CodedError
		if stderrors.As(m.err, &ce) {
			reason = ce.Message
		}
		return nil, "", errors.NewCodedErrorWithDetails(errors.PULSE_TIMEZONE_UNKNOWN,
			fmt.Sprintf("row %d, column %q: zone column %q: %s", rowNum, col, sz.tzField, reason),
			map[string]any{"row": rowNum, "column": col, "reason": reason, "value": cell,
				errors.DetailTimeZone: cell, detailZoneColumn: sz.tzField})
	}
	return m.z, cell, nil
}

// isZoneRowError reports whether err is a per-row source-zone row error
// (zoneFor): the row is skipped, never the import refused.
func isZoneRowError(err error) bool {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return false
	}
	_, ok := ce.Details[detailZoneColumn]
	return ok
}

// parse converts raw, a present cell of zoned field i (column name col,
// 1-based source row rowNum), to the on-wire datetime value. A literal no
// layout accepts returns ParseDateTime's own error (a row error, as
// today); a DST refusal returns the fatal PULSE_IMPORT_DST_* code.
func (sz *sourceZones) parse(i int, raw, col string, rowNum int) (uint64, error) {
	v, _, err := sz.parseKind(i, raw, col, rowNum)
	return v, err
}

// render is convert's twin of parse: it turns a present cell of zoned
// field i into the text the convert target receives. A naive literal
// becomes the canonical UTC literal of the instant it names
// (encoding.FormatDateTime, `…Z`), so every target — text, a row-path
// `.sav` rebuild, a later import — reads the instant, not a wall clock
// it would take for UTC. A literal carrying its own `Z` / offset already
// names its instant and passes through verbatim, as does one no layout
// accepts (convert hands unparseable cells through, as it always has). A
// DST refusal is returned as parse returns it.
func (sz *sourceZones) render(i int, raw, col string, rowNum int) (string, error) {
	v, kind, err := sz.parseKind(i, raw, col, rowNum)
	if err != nil {
		if isDSTRefusal(err) || isZoneRowError(err) {
			return "", err
		}
		return raw, nil
	}
	if kind == temporal.LocalOffset {
		return raw, nil
	}
	return encoding.FormatDateTime(v), nil
}

// parseKind is parse plus temporal's verdict on the literal's kind.
//
// A per-row field whose row zone cannot be resolved still reads a literal
// carrying its own `Z` / offset (that literal never consults a zone);
// only a naive one returns zoneFor's row error.
func (sz *sourceZones) parseKind(i int, raw, col string, rowNum int) (uint64, temporal.LocalKind, error) {
	z, zname, zerr := sz.zoneFor(i, col, rowNum)
	if zerr != nil {
		sec, kind, err := temporal.ParseLocal(raw, sz.utc, sz.policy)
		if err == nil && kind == temporal.LocalOffset {
			return uint64(sec), kind, nil
		}
		return 0, 0, zerr
	}
	sec, kind, err := temporal.ParseLocal(raw, z, sz.policy)
	if err != nil {
		code := errors.Code("")
		what := ""
		switch {
		case stderrors.Is(err, temporal.ErrLocalAmbiguous):
			code, what = errors.PULSE_IMPORT_DST_AMBIGUOUS, "is ambiguous: the zone shows it twice (a DST fall-back)"
		case stderrors.Is(err, temporal.ErrLocalNonexistent):
			code, what = errors.PULSE_IMPORT_DST_NONEXISTENT, "does not exist: the zone skips it (a DST spring-forward)"
		default:
			return 0, 0, err
		}
		return 0, 0, errors.NewCodedErrorWithDetails(code,
			fmt.Sprintf("row %d, column %q: local time %q in %s %s; choose a DST policy (earlier or later) or give the value an offset", rowNum, col, raw, zname, what),
			map[string]any{"row": rowNum, "column": col, "value": raw, "zone": zname})
	}
	switch kind {
	case temporal.LocalAmbiguous:
		sz.ambiguousN++
	case temporal.LocalNonexistent:
		sz.nonexistentN++
	}
	return uint64(sec), kind, nil
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
