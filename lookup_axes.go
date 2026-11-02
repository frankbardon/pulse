package pulse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// lookupKeySeparator joins lookup() key arguments into a Rows key
// (internal/processing's lookup builtin hard-codes the same "|"). An
// axis value or range-table label containing it would make a joined key
// ambiguous, so declared axes refuse it.
const lookupKeySeparator = "|"

// lookupAxisInvalid builds a PULSE_LOOKUP_AXIS_INVALID for table with
// details.reason (+ extra details).
func lookupAxisInvalid(table, reason, msg string, extra map[string]any) error {
	details := map[string]any{"table": table, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_AXIS_INVALID,
		fmt.Sprintf("lookup table %q: %s", table, msg), details)
}

// parseLookupAxes decodes a lookup-table file's `keys` member: a
// non-empty array of {name, values} / {name, range_table} objects, each
// decoded strictly (duplicate and unknown keys refused). The shape rules
// shared with programmatic tables (blank / repeated names, both or
// neither source, blank values, the separator) are left to
// checkLookupAxes so file and code tables refuse alike.
func parseLookupAxes(table string, raw json.RawMessage) ([]LookupAxis, error) {
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '[' {
		return nil, lookupAxisInvalid(table, "keys_not_array", "keys must be a JSON array of axis objects", nil)
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, lookupAxisInvalid(table, "keys_not_array", "keys must be a JSON array of axis objects: "+err.Error(), nil)
	}
	if len(elems) == 0 {
		return nil, lookupAxisInvalid(table, "keys_empty", "keys declares no axis; omit it or list one axis per lookup() argument", nil)
	}
	axes := make([]LookupAxis, 0, len(elems))
	for i, el := range elems {
		invalid := func(reason, msg string, extra map[string]any) error {
			details := map[string]any{"position": i}
			for k, v := range extra {
				details[k] = v
			}
			return lookupAxisInvalid(table, reason, fmt.Sprintf("keys[%d]: %s", i, msg), details)
		}
		if t := bytes.TrimSpace(el); len(t) == 0 || t[0] != '{' {
			return nil, invalid("axis_not_object", `an axis is a JSON object {"name", "values"} or {"name", "range_table"}`, nil)
		}
		members, err := decodeLookupObject(el, invalid)
		if err != nil {
			return nil, err
		}
		var ax LookupAxis
		for _, m := range members {
			switch m.key {
			case "name":
				if err := json.Unmarshal(m.value, &ax.Name); err != nil || isJSONNull(m.value) {
					return nil, invalid("name_not_string", "name must be a JSON string", nil)
				}
			case "values":
				if t := bytes.TrimSpace(m.value); len(t) == 0 || t[0] != '[' {
					return nil, invalid("values_not_strings", "values must be a JSON array of strings", nil)
				}
				ax.Values = []string{}
				if err := json.Unmarshal(m.value, &ax.Values); err != nil {
					return nil, invalid("values_not_strings", `values must be a JSON array of strings (write a numeric key as its lookup() spelling, e.g. "1")`, nil)
				}
			case "range_table":
				if err := json.Unmarshal(m.value, &ax.RangeTable); err != nil || isJSONNull(m.value) {
					return nil, invalid("range_table_not_string", "range_table must be a JSON string naming a range table", nil)
				}
				if strings.TrimSpace(ax.RangeTable) == "" {
					return nil, invalid("range_table_empty", "range_table is blank", nil)
				}
			default:
				return nil, invalid("unknown_key",
					fmt.Sprintf("unknown axis key %q (accepted: name, values, range_table)", m.key),
					map[string]any{"key": m.key})
			}
		}
		axes = append(axes, ax)
	}
	return axes, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// checkLookupAxes validates table t's declared axes and, against them,
// its Rows. ranges is the instance's range tables; resolveRanges false
// (the per-file pass in ParseLookupTableFile) skips every range-table
// axis — its labels are unknown until pulse.New — so the off-axis and
// completeness checks then judge only the inline axes (completeness only
// when every axis is inline). pulse.New runs it again with
// resolveRanges true for every table, file-loaded or programmatic.
//
// Checks, in order: `complete` needs axes; axes need a Rows table; each
// axis has a unique non-blank name and exactly one of Values / RangeTable;
// inline values are non-blank, free of the separator and unique
// (PULSE_LOOKUP_KEY_COLLISION — a repeated value joins two declared
// combinations to one key); a range table exists and its labels are free
// of the separator; every row key has one part per axis
// (PULSE_LOOKUP_KEY_ARITY), each on its axis (PULSE_LOOKUP_KEY_OFF_AXIS);
// a complete table covers the cross product
// (PULSE_LOOKUP_TABLE_INCOMPLETE). Row keys are judged in sorted order so
// the reported fault is deterministic.
func checkLookupAxes(name string, t LookupTable, ranges map[string]RangeTable, resolveRanges bool) error {
	if len(t.Keys) == 0 {
		if t.Complete {
			return lookupAxisInvalid(name, "complete_without_keys",
				"complete needs declared keys axes to define the cross product", nil)
		}
		return nil
	}
	if t.Rows == nil {
		// Conservative: a function-driven table's keys are the
		// embedder's to interpret, and no row set exists to check.
		return lookupAxisInvalid(name, "lookup_func_with_keys",
			"a function-driven (Lookup) table cannot declare keys axes; declare axes on a Rows table", nil)
	}
	values := make([][]string, len(t.Keys))
	sets := make([]map[string]bool, len(t.Keys))
	known := make([]bool, len(t.Keys))
	names := make(map[string]bool, len(t.Keys))
	for i, ax := range t.Keys {
		at := map[string]any{"axis": ax.Name, "position": i}
		if strings.TrimSpace(ax.Name) == "" {
			return lookupAxisInvalid(name, "name_empty", fmt.Sprintf("axis %d has no name", i), at)
		}
		if names[ax.Name] {
			return lookupAxisInvalid(name, "duplicate_name", fmt.Sprintf("axis name %q appears more than once", ax.Name), at)
		}
		names[ax.Name] = true
		hasValues, hasTable := ax.Values != nil, ax.RangeTable != ""
		switch {
		case hasValues && hasTable:
			return lookupAxisInvalid(name, "values_and_range_table",
				fmt.Sprintf("axis %q sets both values and range_table; an axis has exactly one", ax.Name), at)
		case !hasValues && !hasTable:
			return lookupAxisInvalid(name, "no_values",
				fmt.Sprintf("axis %q sets neither values nor range_table", ax.Name), at)
		case hasValues:
			if len(ax.Values) == 0 {
				return lookupAxisInvalid(name, "values_empty", fmt.Sprintf("axis %q lists no values", ax.Name), at)
			}
			set := make(map[string]bool, len(ax.Values))
			for _, v := range ax.Values {
				if strings.TrimSpace(v) == "" {
					return lookupAxisInvalid(name, "value_empty", fmt.Sprintf("axis %q has a blank value", ax.Name), at)
				}
				if strings.Contains(v, lookupKeySeparator) {
					return lookupAxisInvalid(name, "separator_in_value",
						fmt.Sprintf("axis %q value %q contains the key separator %q", ax.Name, v, lookupKeySeparator),
						map[string]any{"axis": ax.Name, "position": i, "value": v})
				}
				if set[v] {
					return errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_KEY_COLLISION,
						fmt.Sprintf("lookup table %q: axis %q lists %q more than once, so two declared combinations join to the same key", name, ax.Name, v),
						map[string]any{"table": name, "axis": ax.Name, "key": v})
				}
				set[v] = true
			}
			values[i], sets[i], known[i] = ax.Values, set, true
		default:
			if !resolveRanges {
				continue
			}
			rt, ok := ranges[ax.RangeTable]
			if !ok {
				return lookupAxisInvalid(name, "range_table_unknown",
					fmt.Sprintf("axis %q references range table %q, which is not registered", ax.Name, ax.RangeTable),
					map[string]any{"axis": ax.Name, "position": i, "range_table": ax.RangeTable})
			}
			labels := rangeTableLabels(rt)
			set := make(map[string]bool, len(labels))
			for _, l := range labels {
				if strings.Contains(l, lookupKeySeparator) {
					return lookupAxisInvalid(name, "separator_in_range_label",
						fmt.Sprintf("axis %q: range table %q label %q contains the key separator %q", ax.Name, ax.RangeTable, l, lookupKeySeparator),
						map[string]any{"axis": ax.Name, "position": i, "range_table": ax.RangeTable, "value": l})
				}
				set[l] = true
			}
			values[i], sets[i], known[i] = labels, set, true
		}
	}

	keys := make([]string, 0, len(t.Rows))
	for k := range t.Rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts := strings.Split(k, lookupKeySeparator)
		if len(parts) != len(t.Keys) {
			return errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_KEY_ARITY,
				fmt.Sprintf("lookup table %q: row key %q has %d part(s), the table declares %d axes", name, k, len(parts), len(t.Keys)),
				map[string]any{"table": name, "key": k, "want": len(t.Keys), "got": len(parts)})
		}
		for i, p := range parts {
			if known[i] && !sets[i][p] {
				return errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_KEY_OFF_AXIS,
					fmt.Sprintf("lookup table %q: row key %q part %d (%q) is not a value of axis %q", name, k, i, p, t.Keys[i].Name),
					map[string]any{"table": name, "key": k, "axis": t.Keys[i].Name, "position": i, "value": p})
			}
		}
	}

	if !t.Complete {
		return nil
	}
	for _, k := range known {
		if !k {
			return nil // a range-table axis: judged at pulse.New
		}
	}
	// Every row is distinct and on-axis (and axis values are unique),
	// so the table is complete iff it has as many rows as the cross
	// product has cells. Saturate the product: it only has to beat
	// len(Rows).
	cells := 1
	for _, v := range values {
		if cells > len(t.Rows) {
			break
		}
		cells *= len(v)
	}
	if cells == len(t.Rows) {
		return nil
	}
	missing := firstMissingLookupCell(values, t.Rows)
	details := map[string]any{"table": name, "key": missing, "rows": len(t.Rows)}
	if cellsFit(values) {
		details["cells"] = crossProductSize(values)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_TABLE_INCOMPLETE,
		fmt.Sprintf("lookup table %q is declared complete but has no row for %q (%d row(s))", name, missing, len(t.Rows)),
		details)
}

// firstMissingLookupCell walks the cross product in axis order (the
// first axis slowest) and returns the first joined key Rows lacks. The
// caller has established that one is missing, so the walk ends within
// len(rows)+1 cells.
func firstMissingLookupCell(values [][]string, rows map[string]float64) string {
	idx := make([]int, len(values))
	parts := make([]string, len(values))
	for {
		for i, j := range idx {
			parts[i] = values[i][j]
		}
		k := strings.Join(parts, lookupKeySeparator)
		if _, ok := rows[k]; !ok {
			return k
		}
		i := len(idx) - 1
		for ; i >= 0; i-- {
			idx[i]++
			if idx[i] < len(values[i]) {
				break
			}
			idx[i] = 0
		}
		if i < 0 {
			return "" // unreachable: every cell present
		}
	}
}

// cellsFit reports whether the cross-product size fits an int without
// overflow (it is reported in details only then).
func cellsFit(values [][]string) bool {
	const maxCells = 1 << 53
	n := 1
	for _, v := range values {
		if len(v) > 0 && n > maxCells/len(v) {
			return false
		}
		n *= len(v)
	}
	return true
}

func crossProductSize(values [][]string) int {
	n := 1
	for _, v := range values {
		n *= len(v)
	}
	return n
}

// rangeTableLabels returns a range table's labels in authored order.
func rangeTableLabels(t RangeTable) []string {
	labels := make([]string, 0, len(t.Ranges)+len(t.NumericRanges))
	for _, r := range t.Ranges {
		labels = append(labels, r.Label)
	}
	for _, r := range t.NumericRanges {
		labels = append(labels, r.Label)
	}
	return labels
}

// validateLookupTableAxes is the pulse.New cross-check of every lookup
// table's declared axes against the instance's range tables: it runs in
// validateExtensions after validateRangeTables — and so after every
// directory loader and programmatic registration — so an axis may
// reference a range table loaded from Options.RangeTablesDir. Tables
// are judged in name order for a deterministic first fault.
func validateLookupTableAxes(tables map[string]LookupTable, ranges map[string]RangeTable) error {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := checkLookupAxes(name, tables[name], ranges, true); err != nil {
			return err
		}
	}
	return nil
}
