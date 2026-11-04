package pulse

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// envLookupTablesDir is the environment variable consulted when
// Options.LookupTablesDir is empty.
const envLookupTablesDir = "PULSE_LOOKUP_TABLES_DIR"

// loadLookupTablesFromDir walks Options.LookupTablesDir (or, when empty,
// the PULSE_LOOKUP_TABLES_DIR env var) and loads every *.json file as a
// LookupTable through ParseLookupTableFile — the same parser an
// embedder calls to validate a file exactly as the loader does. Tables
// loaded from disk merge into opts.Extensions.LookupTables; a name that
// is already declared programmatically is PULSE_EXTENSION_DUPLICATE
// (dirCollision, category "lookup_table" — a table has exactly one
// source of truth).
//
// The walk is non-recursive and names each table after its file stem.
// Files matching a known Pulse sidecar suffix (isPulseSidecarName) are
// skipped without being read; any other *.json that fails to parse is a
// hard error naming the path, carrying the parser's PULSE_LOOKUP_* code.
//
// Empty dir + empty env var, or a directory that does not exist: the
// call is a no-op. A path that is not a directory is an error.
func loadLookupTablesFromDir(opts *Options) error {
	dir := opts.LookupTablesDir
	if dir == "" {
		dir = os.Getenv(envLookupTablesDir)
	}
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return tableFileError("lookup_table", dir, fmt.Sprintf("pulse: lookup tables dir %q", dir), err)
	}
	if !info.IsDir() {
		return tableFileError("lookup_table", dir, fmt.Sprintf("pulse: lookup tables dir %q is not a directory", dir), nil)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return tableFileError("lookup_table", dir, fmt.Sprintf("pulse: reading lookup tables dir %q", dir), err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		// Pulse's own sidecars are *.json and may share the directory;
		// skip them by suffix BEFORE parsing (see isPulseSidecarName).
		if isPulseSidecarName(name) {
			continue
		}
		tableName := strings.TrimSuffix(name, ".json")
		if tableName == "" {
			continue
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return tableFileError("lookup_table", path, "pulse: reading lookup table "+path, err)
		}
		tbl, err := ParseLookupTableFile(tableName, raw)
		if err != nil {
			return lookupFileErrorAtPath(err, path)
		}
		if opts.Extensions.LookupTables == nil {
			opts.Extensions.LookupTables = map[string]LookupTable{}
		}
		if _, exists := opts.Extensions.LookupTables[tableName]; exists {
			return dirCollision("lookup_table", tableName, path)
		}
		opts.Extensions.LookupTables[tableName] = tbl
	}
	return nil
}

// lookupFileErrorAtPath re-issues a parser error with the file path in
// its message and details, keeping the parser's code: the envelope
// prints the coded error's own message, so a wrap alone would drop the
// path.
func lookupFileErrorAtPath(err error, path string) error {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return tableFileError("lookup_table", path, "pulse: parsing lookup table "+path, err)
	}
	details := make(map[string]any, len(ce.Details)+1)
	for k, v := range ce.Details {
		details[k] = v
	}
	details["path"] = path
	out := errors.NewCodedErrorWithDetails(ce.Code,
		fmt.Sprintf("pulse: parsing lookup table %s: %s", path, ce.Message), details)
	out.Cause = ce.Cause
	return out
}

// lookupTableFile is one parsed lookup-table document. It is the seam
// later file keys (declared axes, completeness) extend: the wrapped
// form's accepted top-level keys are lookupTableFileKeys, and every
// field here is carried onto the LookupTable by toLookupTable.
type lookupTableFile struct {
	Description string
	Rows        map[string]float64
	Keys        []LookupAxis
	Complete    bool
}

// lookupTableFileKeys is the closed set of top-level keys the wrapped
// form accepts; any other key is PULSE_LOOKUP_FILE_INVALID
// (unknown_key) so a typo'd option cannot be silently ignored.
var lookupTableFileKeys = map[string]bool{"description": true, "rows": true, "keys": true, "complete": true}

func (f lookupTableFile) toLookupTable() LookupTable {
	return LookupTable{Description: f.Description, Rows: f.Rows, Keys: f.Keys, Complete: f.Complete}
}

// ParseLookupTableFile parses one lookup-table file's bytes into a
// LookupTable — the parser the PULSE_LOOKUP_TABLES_DIR /
// Options.LookupTablesDir loader runs on every *.json file, exported so
// an embedder can validate a user-supplied file with the loader's own
// coded errors. name is the table name (the loader passes the file
// stem); it rides every error's details as `table`.
//
// Two shapes are accepted:
//
//   - a bare object mapping each joined key to a number:
//     {"east": 1.07, "west|2025": 0.98}
//   - a wrapped object: {"description": "...", "rows": {key: number}}
//
// An object whose "rows" member is anything but a JSON number is the
// wrapped form; then only "description" (a string), "rows" (an
// object), "keys" and "complete" (a boolean) may appear. Multi-part keys
// are joined with "|", as lookup() joins its key arguments.
//
// "keys" declares the key axes (LookupTable.Keys), one per lookup()
// argument: [{"name": "region", "values": ["east", "west"]},
// {"name": "score", "range_table": "score_bands"}]. "complete": true
// (LookupTable.Complete) requires a row for every combination. The
// per-file axis checks run here; a range-table axis is resolved — and
// its membership and completeness judged — only at pulse.New, where the
// instance's range tables are known.
//
// Refusals: PULSE_LOOKUP_FILE_INVALID (not JSON, not an object, a
// duplicated key, an unknown wrapped key, a non-string description, a
// non-object rows, a non-boolean complete — details.reason says which),
// PULSE_LOOKUP_CELL_NOT_NUMERIC (a cell that is not a finite JSON
// number, details.key), PULSE_LOOKUP_TABLE_EMPTY (no rows),
// PULSE_LOOKUP_AXIS_INVALID (a malformed axis, a "|" in a value,
// complete without keys — details.reason), PULSE_LOOKUP_KEY_ARITY,
// PULSE_LOOKUP_KEY_OFF_AXIS, PULSE_LOOKUP_KEY_COLLISION (a repeated axis
// value) and PULSE_LOOKUP_TABLE_INCOMPLETE.
func ParseLookupTableFile(name string, data []byte) (LookupTable, error) {
	f, err := parseLookupTableDoc(name, data)
	if err != nil {
		return LookupTable{}, err
	}
	tbl := f.toLookupTable()
	// The per-file axis checks; range-table axes wait for pulse.New
	// (validateLookupTableAxes), where the instance's tables are known.
	if err := checkLookupAxes(name, tbl, nil, false); err != nil {
		return LookupTable{}, err
	}
	return tbl, nil
}

func parseLookupTableDoc(name string, data []byte) (lookupTableFile, error) {
	invalid := func(reason, msg string, extra map[string]any) error {
		details := map[string]any{"table": name, "reason": reason}
		for k, v := range extra {
			details[k] = v
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_FILE_INVALID,
			fmt.Sprintf("lookup table %q: %s", name, msg), details)
	}
	top, err := decodeLookupObject(data, invalid)
	if err != nil {
		return lookupTableFile{}, err
	}
	var f lookupTableFile
	cells := top
	if rows, ok := lookupMember(top, "rows"); ok && !isJSONNumber(rows) {
		for _, m := range top {
			if !lookupTableFileKeys[m.key] {
				return lookupTableFile{}, invalid("unknown_key",
					fmt.Sprintf("unknown top-level key %q in the wrapped form (accepted: description, rows, keys, complete)", m.key),
					map[string]any{"key": m.key})
			}
		}
		if desc, ok := lookupMember(top, "description"); ok {
			if err := json.Unmarshal(desc, &f.Description); err != nil || bytes.Equal(bytes.TrimSpace(desc), []byte("null")) {
				return lookupTableFile{}, invalid("description_not_string", "description must be a JSON string", nil)
			}
		}
		if raw, ok := lookupMember(top, "keys"); ok {
			if f.Keys, err = parseLookupAxes(name, raw); err != nil {
				return lookupTableFile{}, err
			}
		}
		if raw, ok := lookupMember(top, "complete"); ok {
			if err := json.Unmarshal(raw, &f.Complete); err != nil || isJSONNull(raw) {
				return lookupTableFile{}, invalid("complete_not_bool", "complete must be a JSON boolean", nil)
			}
		}
		if t := bytes.TrimSpace(rows); len(t) == 0 || t[0] != '{' {
			return lookupTableFile{}, invalid("rows_not_object", "rows must be a JSON object mapping each key to a number", nil)
		}
		cells, err = decodeLookupObject(rows, invalid)
		if err != nil {
			return lookupTableFile{}, err
		}
	}
	if len(cells) == 0 {
		return lookupTableFile{}, errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_TABLE_EMPTY,
			fmt.Sprintf("lookup table %q has no rows", name), map[string]any{"table": name})
	}
	f.Rows = make(map[string]float64, len(cells))
	for _, c := range cells {
		var v float64
		if !isJSONNumber(c.value) || json.Unmarshal(c.value, &v) != nil {
			return lookupTableFile{}, errors.NewCodedErrorWithDetails(errors.PULSE_LOOKUP_CELL_NOT_NUMERIC,
				fmt.Sprintf("lookup table %q: cell %q is not a finite number (got %s)", name, c.key, truncateLookupRaw(c.value)),
				map[string]any{"table": name, "key": c.key})
		}
		f.Rows[c.key] = v
	}
	return f, nil
}

// lookupObjectMember is one key/value of a JSON object, in file order.
type lookupObjectMember struct {
	key   string
	value json.RawMessage
}

// decodeLookupObject decodes a JSON object into its members in file
// order, refusing anything but a single object and any duplicated key
// (encoding/json would silently keep the last one).
func decodeLookupObject(data []byte, invalid func(string, string, map[string]any) error) ([]lookupObjectMember, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, invalid("json_invalid", "not valid JSON: "+err.Error(), nil)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, invalid("not_object", "a lookup table file is a JSON object", nil)
	}
	var members []lookupObjectMember
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, invalid("json_invalid", "not valid JSON: "+err.Error(), nil)
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, invalid("json_invalid", "not valid JSON: "+err.Error(), nil)
		}
		if seen[key] {
			return nil, invalid("duplicate_key", fmt.Sprintf("key %q appears more than once", key), map[string]any{"key": key})
		}
		seen[key] = true
		members = append(members, lookupObjectMember{key: key, value: raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, invalid("json_invalid", "not valid JSON: "+err.Error(), nil)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, invalid("json_invalid", "not valid JSON: trailing content after the object", nil)
	}
	return members, nil
}

func lookupMember(members []lookupObjectMember, key string) (json.RawMessage, bool) {
	for _, m := range members {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// isJSONNumber reports whether raw is a JSON number literal (JSON has no
// NaN / Inf, so a decodable number is finite).
func isJSONNumber(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && (t[0] == '-' || (t[0] >= '0' && t[0] <= '9'))
}

func truncateLookupRaw(raw json.RawMessage) string {
	s := string(bytes.TrimSpace(raw))
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return s
}
