package pulse

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/imports"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/model"
)

func writeLookupFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func wantLookupCode(t *testing.T, err error, code errors.Code) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want %s, got uncoded error %v", code, err)
	}
	if ce.Code != code {
		t.Fatalf("want %s, got %s (%v)", code, ce.Code, err)
	}
	return ce
}

// TestParseLookupTableFile_Shapes: the bare {key: number} map and the
// wrapped {description, rows} form both parse to a Rows-backed table; a
// bare map MAY carry a numeric "rows" cell (only a non-number "rows"
// selects the wrapped form).
func TestParseLookupTableFile_Shapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, body, desc string
		rows             map[string]float64
	}{
		{"bare", `{"east": 1.07, "west|2025": -0.5, "n": 3}`, "", map[string]float64{"east": 1.07, "west|2025": -0.5, "n": 3}},
		{"wrapped", `{"description": "region multipliers", "rows": {"east": 1.07, "a|b": 2e3}}`, "region multipliers", map[string]float64{"east": 1.07, "a|b": 2000}},
		{"wrapped no description", `{"rows": {"x": 0}}`, "", map[string]float64{"x": 0}},
		{"bare numeric rows key", `{"rows": 4, "cols": 5}`, "", map[string]float64{"rows": 4, "cols": 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tbl, err := ParseLookupTableFile("t", []byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if tbl.Description != tc.desc || tbl.Lookup != nil || len(tbl.Rows) != len(tc.rows) {
				t.Fatalf("got %+v, want desc %q rows %v", tbl, tc.desc, tc.rows)
			}
			for k, v := range tc.rows {
				if got, ok := tbl.Rows[k]; !ok || got != v {
					t.Fatalf("row %q = %v (present %v), want %v", k, got, ok, v)
				}
			}
		})
	}
}

// TestParseLookupTableFile_Refusals: every malformed file is refused
// with its own PULSE_LOOKUP_* code, the table name in details.table and,
// for a shape fault, the reason under details.reason.
func TestParseLookupTableFile_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, body string
		code       errors.Code
		reason     string
		key        string
	}{
		{"not json", `east = 1`, errors.PULSE_LOOKUP_FILE_INVALID, "json_invalid", ""},
		{"truncated", `{"east": 1`, errors.PULSE_LOOKUP_FILE_INVALID, "json_invalid", ""},
		{"trailing content", `{"east": 1} {}`, errors.PULSE_LOOKUP_FILE_INVALID, "json_invalid", ""},
		{"array", `[1, 2]`, errors.PULSE_LOOKUP_FILE_INVALID, "not_object", ""},
		{"duplicate key", `{"east": 1, "east": 2}`, errors.PULSE_LOOKUP_FILE_INVALID, "duplicate_key", "east"},
		{"duplicate row key", `{"rows": {"a": 1, "a": 1}}`, errors.PULSE_LOOKUP_FILE_INVALID, "duplicate_key", "a"},
		{"unknown wrapped key", `{"rows": {"a": 1}, "complet": true}`, errors.PULSE_LOOKUP_FILE_INVALID, "unknown_key", "complet"},
		{"description not string", `{"description": 5, "rows": {"a": 1}}`, errors.PULSE_LOOKUP_FILE_INVALID, "description_not_string", ""},
		{"rows not object", `{"rows": [1]}`, errors.PULSE_LOOKUP_FILE_INVALID, "rows_not_object", ""},
		{"rows null", `{"rows": null}`, errors.PULSE_LOOKUP_FILE_INVALID, "rows_not_object", ""},
		{"string cell", `{"east": "1.07"}`, errors.PULSE_LOOKUP_CELL_NOT_NUMERIC, "", "east"},
		{"null cell", `{"east": 1, "west": null}`, errors.PULSE_LOOKUP_CELL_NOT_NUMERIC, "", "west"},
		{"bool cell wrapped", `{"rows": {"a": true}}`, errors.PULSE_LOOKUP_CELL_NOT_NUMERIC, "", "a"},
		{"object cell", `{"a": {"b": 1}}`, errors.PULSE_LOOKUP_CELL_NOT_NUMERIC, "", "a"},
		{"overflow cell", `{"a": 1e999}`, errors.PULSE_LOOKUP_CELL_NOT_NUMERIC, "", "a"},
		{"description-only bare", `{"description": "x"}`, errors.PULSE_LOOKUP_CELL_NOT_NUMERIC, "", "description"},
		{"empty bare", `{}`, errors.PULSE_LOOKUP_TABLE_EMPTY, "", ""},
		{"empty wrapped", `{"description": "x", "rows": {}}`, errors.PULSE_LOOKUP_TABLE_EMPTY, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseLookupTableFile("rates", []byte(tc.body))
			ce := wantLookupCode(t, err, tc.code)
			if ce.Details["table"] != "rates" {
				t.Errorf("details.table = %v, want rates", ce.Details["table"])
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("details.reason = %v, want %s", ce.Details["reason"], tc.reason)
			}
			if tc.key != "" && ce.Details["key"] != tc.key {
				t.Errorf("details.key = %v, want %s", ce.Details["key"], tc.key)
			}
		})
	}
}

func TestLoadLookupTables_FromDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLookupFile(t, dir, "adjust.json", `{"east": 1.07, "west": 0.98}`)
	writeLookupFile(t, dir, "grid.json", `{"description": "rate grid", "rows": {"a|b": 4.5}}`)
	writeLookupFile(t, dir, "notes.txt", `not a table`)
	if err := os.Mkdir(filepath.Join(dir, "nested.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLookupFile(t, filepath.Join(dir, "nested.json"), "deep.json", `{"x": 1}`)
	opts := Options{LookupTablesDir: dir}
	if err := loadLookupTablesFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	got := opts.Extensions.LookupTables
	if len(got) != 2 || got["adjust"].Rows["east"] != 1.07 || got["grid"].Description != "rate grid" || got["grid"].Rows["a|b"] != 4.5 {
		t.Fatalf("unexpected tables: %+v", got)
	}
}

// TestLoadLookupTables_OptionBeatsEnv: Options.LookupTablesDir wins
// over PULSE_LOOKUP_TABLES_DIR; with the option empty the env var is
// read.
func TestLoadLookupTables_OptionBeatsEnv(t *testing.T) {
	// serial: t.Setenv mutates the process environment
	envDir, optDir := t.TempDir(), t.TempDir()
	writeLookupFile(t, envDir, "from_env.json", `{"k": 1}`)
	writeLookupFile(t, optDir, "from_opt.json", `{"k": 2}`)
	t.Setenv(envLookupTablesDir, envDir)

	opts := Options{LookupTablesDir: optDir}
	if err := loadLookupTablesFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	if _, ok := opts.Extensions.LookupTables["from_opt"]; !ok || len(opts.Extensions.LookupTables) != 1 {
		t.Fatalf("option must win over env: %+v", opts.Extensions.LookupTables)
	}

	opts = Options{}
	if err := loadLookupTablesFromDir(&opts); err != nil {
		t.Fatal(err)
	}
	if _, ok := opts.Extensions.LookupTables["from_env"]; !ok || len(opts.Extensions.LookupTables) != 1 {
		t.Fatalf("env var not honoured: %+v", opts.Extensions.LookupTables)
	}
}

func TestLoadLookupTables_NoDirAndMissingDirNoOp(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"", filepath.Join(t.TempDir(), "absent")} {
		opts := Options{LookupTablesDir: dir}
		if err := loadLookupTablesFromDir(&opts); err != nil {
			t.Fatalf("dir %q: no-op expected, got %v", dir, err)
		}
		if opts.Extensions.LookupTables != nil {
			t.Fatalf("dir %q: no tables expected, got %v", dir, opts.Extensions.LookupTables)
		}
	}
	// An empty directory registers nothing either.
	opts := Options{LookupTablesDir: t.TempDir()}
	if err := loadLookupTablesFromDir(&opts); err != nil || len(opts.Extensions.LookupTables) != 0 {
		t.Fatalf("empty dir: err %v tables %v", err, opts.Extensions.LookupTables)
	}
}

func TestLoadLookupTables_NotADirectory(t *testing.T) {
	t.Parallel()

	file := writeLookupFile(t, t.TempDir(), "plain.json", `{"k": 1}`)
	opts := Options{LookupTablesDir: file}
	err := loadLookupTablesFromDir(&opts)
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("want not-a-directory error, got %v", err)
	}
}

func TestLoadLookupTables_CollisionWithProgrammatic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeLookupFile(t, dir, "adjust.json", `{"east": 1}`)
	opts := Options{
		LookupTablesDir: dir,
		Extensions:      Extensions{LookupTables: map[string]LookupTable{"adjust": {Rows: map[string]float64{"east": 2}}}},
	}
	assertDirCollision(t, loadLookupTablesFromDir(&opts), "lookup_table", "adjust", path)
}

// TestLoadLookupTables_SkipsPulseSidecars: Pulse's own *.json sidecars
// in the directory are skipped unread; a valid neighbour still loads.
func TestLoadLookupTables_SkipsPulseSidecars(t *testing.T) {
	t.Parallel()

	for _, filename := range []string{
		"cohort.pulse" + spss.SidecarSuffix,
		"cohort.pulse" + imports.SidecarSuffix,
		"mmm" + model.FileSuffix,
		"brand" + model.PopulationSuffix,
		"brand" + model.SEMSuffix,
	} {
		t.Run(filename, func(t *testing.T) {
			dir := t.TempDir()
			writeLookupFile(t, dir, "adjust.json", `{"east": 1}`)
			// Not a lookup table: parsing it would be PULSE_LOOKUP_*.
			writeLookupFile(t, dir, filename, `{"handle": "h1", "nested": {"x": "y"}}`)
			opts := Options{LookupTablesDir: dir}
			if err := loadLookupTablesFromDir(&opts); err != nil {
				t.Fatalf("sidecar must be skipped: %v", err)
			}
			if len(opts.Extensions.LookupTables) != 1 {
				t.Fatalf("sidecar registered a table: %+v", opts.Extensions.LookupTables)
			}
		})
	}
}

// TestLoadLookupTables_BadFileNamesPath: a file that does not parse
// fails the load with the parser's own code, its path in the message
// and under details.path.
func TestLoadLookupTables_BadFileNamesPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bad := writeLookupFile(t, dir, "rates.json", `{"east": "high"}`)
	opts := Options{LookupTablesDir: dir}
	ce := wantLookupCode(t, loadLookupTablesFromDir(&opts), errors.PULSE_LOOKUP_CELL_NOT_NUMERIC)
	if !strings.Contains(ce.Message, bad) || ce.Details["path"] != bad {
		t.Fatalf("error must name %s: message %q details %v", bad, ce.Message, ce.Details)
	}
	if ce.Details["table"] != "rates" || ce.Details["key"] != "east" {
		t.Fatalf("parser details lost: %v", ce.Details)
	}
	// The exported parser returns the same code for the same bytes.
	_, perr := ParseLookupTableFile("rates", []byte(`{"east": "high"}`))
	wantLookupCode(t, perr, ce.Code)
}

// TestNew_LoadsLookupTablesDir: pulse.New runs the loader — a disk
// table colliding with a programmatic one fails New with
// PULSE_EXTENSION_DUPLICATE, a bad file with its PULSE_LOOKUP_* code,
// and a good file registers a table New accepts.
func TestNew_LoadsLookupTablesDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLookupFile(t, dir, "adjust.json", `{"east": 1.5}`)
	if _, err := New(Options{FS: afero.NewMemMapFs(), LookupTablesDir: dir}); err != nil {
		t.Fatalf("New with a valid lookup dir: %v", err)
	}
	_, err := New(Options{
		FS: afero.NewMemMapFs(), LookupTablesDir: dir,
		Extensions: Extensions{LookupTables: map[string]LookupTable{"adjust": {Rows: map[string]float64{"east": 1}}}},
	})
	wantLookupCode(t, err, errors.PULSE_EXTENSION_DUPLICATE)

	badDir := t.TempDir()
	writeLookupFile(t, badDir, "empty.json", `{}`)
	_, err = New(Options{FS: afero.NewMemMapFs(), LookupTablesDir: badDir})
	wantLookupCode(t, err, errors.PULSE_LOOKUP_TABLE_EMPTY)
}
