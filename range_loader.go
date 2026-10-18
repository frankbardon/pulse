package pulse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// envRangeTablesDir is the environment variable consulted when
// Options.RangeTablesDir is empty.
const envRangeTablesDir = "PULSE_RANGE_TABLES_DIR"

// loadRangeTablesFromDir walks Options.RangeTablesDir (or, when empty,
// the PULSE_RANGE_TABLES_DIR env var) and loads every *.json file as a
// RangeTable. Tables loaded from disk merge into
// opts.Extensions.RangeTables; a name that is already declared
// programmatically is PULSE_EXTENSION_DUPLICATE (dirCollision — a
// table must have exactly one source of truth). The registered ranges are validated later, at
// extension-validation time, via daterange.Compile / numrange.Compile.
//
// File format: either a bare array of range objects
// [{"label":"Q1","start":"2024-01-01","end":"2024-03-31"}, ...] or a
// wrapped object {"description": "...", "ranges": [ ... ]}. The bounds
// type the table: ISO date strings a date table, JSON numbers a numeric
// one ([{"label":"train","start":0,"end":91}, ...]). The filename
// without .json becomes the registered table name.
//
// Files matching a known Pulse sidecar suffix (internal/imports.SidecarSuffix,
// spsssidecar.Suffix — see isPulseSidecarName) are skipped without
// being read: they are Pulse's own artefacts, they sit beside cohorts,
// and this directory may legitimately be a data directory. Any other
// *.json file that fails to parse is still a hard error naming the
// path — an unreadable range table is a typo the caller must see.
//
// Empty dir + empty env var: the call is a no-op.
func loadRangeTablesFromDir(opts *Options) error {
	dir := opts.RangeTablesDir
	if dir == "" {
		dir = os.Getenv(envRangeTablesDir)
	}
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("pulse: range tables dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("pulse: range tables dir %q is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("pulse: reading range tables dir %q: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		// Pulse's own sidecars are *.json and land next to cohorts,
		// so a range-tables dir aimed at a data directory sees them.
		// Skip by suffix BEFORE parsing: they are not range tables and
		// a parse failure on one would blame the wrong thing. This is
		// not tolerance of unparseable JSON — see isPulseSidecarName.
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
			return fmt.Errorf("pulse: reading range table %s: %w", path, err)
		}
		tbl, err := parseRangeTableFile(raw)
		if err != nil {
			return fmt.Errorf("pulse: parsing range table %s: %w", path, err)
		}
		if opts.Extensions.RangeTables == nil {
			opts.Extensions.RangeTables = map[string]RangeTable{}
		}
		if _, exists := opts.Extensions.RangeTables[tableName]; exists {
			return dirCollision("range_table", tableName, path)
		}
		opts.Extensions.RangeTables[tableName] = tbl
	}
	return nil
}

// parseRangeTableFile accepts either a bare array of range specs or a
// wrapped object carrying an optional description, and types the table
// by its bounds: JSON numbers make a numeric table (NumericRanges), ISO
// date strings a date table (Ranges). A table mixing the two is refused
// here; a table whose every bound is omitted / null is a date table (the
// shape every pre-numeric table file had). Structural validation of the
// ranges themselves (overlap / duplicate label / empty / invalid
// boundary) is deferred to the shared range-compilation pass at
// extension-validation time — this parser only unmarshals the file shape.
func parseRangeTableFile(raw []byte) (RangeTable, error) {
	var wrapped struct {
		Description string            `json:"description"`
		Ranges      []json.RawMessage `json:"ranges"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Ranges != nil {
		return typedRangeTable(wrapped.Description, wrapped.Ranges)
	}
	var flat []json.RawMessage
	if err := json.Unmarshal(raw, &flat); err != nil {
		return RangeTable{}, err
	}
	if len(flat) == 0 {
		return RangeTable{}, fmt.Errorf("empty range table")
	}
	return typedRangeTable("", flat)
}

// typedRangeTable decodes ranges into a date or a numeric table by the
// JSON type of their present, non-null bounds.
func typedRangeTable(description string, ranges []json.RawMessage) (RangeTable, error) {
	var sawString, sawNumber bool
	for _, r := range ranges {
		var bounds struct {
			Start json.RawMessage `json:"start"`
			End   json.RawMessage `json:"end"`
		}
		if err := json.Unmarshal(r, &bounds); err != nil {
			return RangeTable{}, err
		}
		for _, b := range []json.RawMessage{bounds.Start, bounds.End} {
			b = bytes.TrimSpace(b)
			switch {
			case len(b) == 0 || string(b) == "null":
			case b[0] == '"':
				sawString = true
			default:
				sawNumber = true
			}
		}
	}
	if sawString && sawNumber {
		return RangeTable{}, fmt.Errorf("range table mixes date (string) and numeric bounds; a table is one kind")
	}
	joined, err := json.Marshal(ranges)
	if err != nil {
		return RangeTable{}, err
	}
	tbl := RangeTable{Description: description}
	if sawNumber {
		err = json.Unmarshal(joined, &tbl.NumericRanges)
	} else {
		err = json.Unmarshal(joined, &tbl.Ranges)
	}
	if err != nil {
		return RangeTable{}, err
	}
	return tbl, nil
}
