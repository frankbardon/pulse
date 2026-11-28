package pulse

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
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
	fsys := configDirFs(opts)
	info, err := fsys.Stat(dir)
	if err != nil {
		if stderrors.Is(err, iofs.ErrNotExist) {
			return nil
		}
		return tableFileError("range_table", dir, fmt.Sprintf("pulse: range tables dir %q", dir), err)
	}
	if !info.IsDir() {
		return tableFileError("range_table", dir, fmt.Sprintf("pulse: range tables dir %q is not a directory", dir), nil)
	}
	entries, err := afero.ReadDir(fsys, dir)
	if err != nil {
		return tableFileError("range_table", dir, fmt.Sprintf("pulse: reading range tables dir %q", dir), err)
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
		raw, err := afero.ReadFile(fsys, path)
		if err != nil {
			return tableFileError("range_table", path, "pulse: reading range table "+path, err)
		}
		tbl, err := parseRangeTableFile(raw)
		if err != nil {
			return tableFileError("range_table", path, "pulse: parsing range table "+path, err)
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

// ParseRangeTableFile parses one range-table file — the bytes a
// RangeTablesDir (PULSE_RANGE_TABLES_DIR) *.json file holds — exactly as
// the directory loader does, so a tool that writes or mirrors the format
// can check a file against Pulse's own parser. name is the table name
// the loader would register (the file stem); it only locates a refusal.
//
// Accepted shapes: a bare array of {label, start, end} range objects, or
// a wrapped object {"description": "...", "ranges": [...]}. The bounds
// type the table: ISO date strings make a date table (Ranges), JSON
// numbers a numeric one (NumericRanges); a bound that is omitted or null
// is open.
//
// It is PARSE-ONLY. The structural checks — overlapping ranges, a
// duplicate label, an empty set, an invalid boundary — run at pulse.New
// (the range-compilation pass every registered table goes through), so a
// file this accepts can still be refused there (PULSE_RANGE_OVERLAP,
// PULSE_RANGE_DUPLICATE_LABEL, PULSE_RANGE_EMPTY, PULSE_RANGE_INVALID).
// To validate fully, register the table (Extensions.RangeTables) on a
// pulse.New.
//
// A file that does not parse — not JSON, not one of the two shapes, an
// empty array, bounds mixing dates and numbers — is DATA_FILE, details
// {kind: "range_table", table: name}, the parse error kept as Cause.
func ParseRangeTableFile(name string, data []byte) (RangeTable, error) {
	tbl, err := parseRangeTableFile(data)
	if err != nil {
		ce := errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"pulse: parsing range table "+strconv.Quote(name)+": "+err.Error(),
			map[string]any{"kind": "range_table", "table": name})
		ce.Cause = err
		return RangeTable{}, ce
	}
	return tbl, nil
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
