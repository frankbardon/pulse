package pulse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// envLabelTablesDir is the environment variable consulted when
// Options.LabelTablesDir is empty.
const envLabelTablesDir = "PULSE_LABEL_TABLES_DIR"

// loadLabelTablesFromDir walks Options.LabelTablesDir (or, when
// empty, the PULSE_LABEL_TABLES_DIR env var) and loads every
// *.json file as a LabelTable. Tables loaded from disk merge into
// opts.Extensions.LabelTables; collisions surface at extension
// validation time as PULSE_EXTENSION_DUPLICATE.
//
// File format: either a flat map {"k": "v"} or a wrapped object
// {"description": "...", "rows": {"k": "v"}}. The filename without
// .json becomes the registered table name.
//
// Empty dir + empty env var: the call is a no-op.
func loadLabelTablesFromDir(opts *Options) error {
	dir := opts.LabelTablesDir
	if dir == "" {
		dir = os.Getenv(envLabelTablesDir)
	}
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("pulse: label tables dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("pulse: label tables dir %q is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("pulse: reading label tables dir %q: %w", dir, err)
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
			return fmt.Errorf("pulse: reading label table %s: %w", path, err)
		}
		tbl, err := parseLabelTableFile(raw)
		if err != nil {
			return fmt.Errorf("pulse: parsing label table %s: %w", path, err)
		}
		if opts.Extensions.LabelTables == nil {
			opts.Extensions.LabelTables = map[string]LabelTable{}
		}
		if _, exists := opts.Extensions.LabelTables[tableName]; exists {
			return fmt.Errorf("pulse: label table %q declared both programmatically and on disk", tableName)
		}
		opts.Extensions.LabelTables[tableName] = tbl
	}
	return nil
}

// parseLabelTableFile accepts either a flat map or a wrapped object.
func parseLabelTableFile(raw []byte) (LabelTable, error) {
	var wrapped struct {
		Description string            `json:"description"`
		Rows        map[string]string `json:"rows"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Rows != nil {
		return LabelTable{Description: wrapped.Description, Rows: wrapped.Rows}, nil
	}
	var flat map[string]string
	if err := json.Unmarshal(raw, &flat); err != nil {
		return LabelTable{}, err
	}
	if len(flat) == 0 {
		return LabelTable{}, fmt.Errorf("empty label table")
	}
	return LabelTable{Rows: flat}, nil
}
