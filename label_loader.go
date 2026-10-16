package pulse

import (
	"bytes"
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
// opts.Extensions.LabelTables; a name already declared
// programmatically is PULSE_EXTENSION_DUPLICATE (dirCollision).
//
// File format: either a flat map {"k": "v"} or a wrapped object
// {"description": "...", "rows": {"k": "v"}}. The filename without
// .json becomes the registered table name. The file's key order becomes
// the table's Order (the code order of a `.sav` export bound to it).
//
// Files matching a known Pulse sidecar suffix (internal/imports.SidecarSuffix,
// spsssidecar.Suffix — see isPulseSidecarName) are skipped without
// being read: they are Pulse's own artefacts, they sit beside cohorts,
// and this directory may legitimately be a data directory. Any other
// *.json file that fails to parse is still a hard error naming the
// path — an unreadable label table is a typo the caller must see.
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
		// Pulse's own sidecars are *.json and land next to cohorts,
		// so a label-tables dir aimed at a data directory sees them.
		// Skip by suffix BEFORE parsing: they are not label tables and
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
			return dirCollision("label_table", tableName, path)
		}
		opts.Extensions.LabelTables[tableName] = tbl
	}
	return nil
}

// parseLabelTableFile accepts either a flat map or a wrapped object.
// Order is the rows object's key order; a key the file repeats keeps its
// first position (its value, as encoding/json decodes it, is the last).
func parseLabelTableFile(raw []byte) (LabelTable, error) {
	var wrapped struct {
		Description string            `json:"description"`
		Rows        map[string]string `json:"rows"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Rows != nil {
		var shell struct {
			Rows json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(raw, &shell); err != nil {
			return LabelTable{}, err
		}
		order, err := objectKeyOrder(shell.Rows)
		if err != nil {
			return LabelTable{}, err
		}
		return LabelTable{Description: wrapped.Description, Rows: wrapped.Rows, Order: order}, nil
	}
	var flat map[string]string
	if err := json.Unmarshal(raw, &flat); err != nil {
		return LabelTable{}, err
	}
	if len(flat) == 0 {
		return LabelTable{}, fmt.Errorf("empty label table")
	}
	order, err := objectKeyOrder(raw)
	if err != nil {
		return LabelTable{}, err
	}
	return LabelTable{Rows: flat, Order: order}, nil
}

// objectKeyOrder returns a JSON object's keys in document order, each once.
// The caller has already decoded raw as a string map, so every value is a
// string token.
func objectKeyOrder(raw []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil { // '{'
		return nil, err
	}
	var order []string
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		if _, err := dec.Token(); err != nil { // the value
			return nil, err
		}
		if !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
	}
	return order, nil
}
