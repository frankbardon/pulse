package pulse

import (
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
// programmatically is a hard error (a table must have exactly one
// source of truth). The registered ranges are validated later, at
// extension-validation time, via CompileDateRanges.
//
// File format: either a bare array of range objects
// [{"label":"Q1","start":"2024-01-01","end":"2024-03-31"}, ...] or a
// wrapped object {"description": "...", "ranges": [ ... ]}. The filename
// without .json becomes the registered table name.
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
			return fmt.Errorf("pulse: range table %q declared both programmatically and on disk", tableName)
		}
		opts.Extensions.RangeTables[tableName] = tbl
	}
	return nil
}

// parseRangeTableFile accepts either a bare array of range specs or a
// wrapped object carrying an optional description. Structural validation
// of the ranges themselves (overlap / duplicate label / empty / invalid
// boundary) is deferred to CompileDateRanges at extension-validation
// time — this parser only unmarshals the file shape.
func parseRangeTableFile(raw []byte) (RangeTable, error) {
	var wrapped struct {
		Description string          `json:"description"`
		Ranges      []DateRangeSpec `json:"ranges"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Ranges != nil {
		return RangeTable{Description: wrapped.Description, Ranges: wrapped.Ranges}, nil
	}
	var flat []DateRangeSpec
	if err := json.Unmarshal(raw, &flat); err != nil {
		return RangeTable{}, err
	}
	if len(flat) == 0 {
		return RangeTable{}, fmt.Errorf("empty range table")
	}
	return RangeTable{Ranges: flat}, nil
}
