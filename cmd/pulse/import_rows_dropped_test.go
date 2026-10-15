package main

// PULSE_IMPORT_ROWS_DROPPED ON EVERY IMPORT ENTRY POINT (#304).
//
// One source — a non-nullable set_u8 column with two blank cells among
// four rows — goes through every surface that imports rows: the CLI
// `import csv`, `import auto` and `convert --keep-pulse` leaves, the
// library Import / ImportFile / Convert, and MCP pulse_import. Each must
// write the two good rows and carry the coded warning with the exact
// counts and first failure; under strict (CLI --strict, Options.Strict)
// each must fail with the same code, exit 1 on the CLI, and write
// nothing.

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
	pcli "github.com/frankbardon/pulse/internal/cli"
	iio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/mcp"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

const (
	droppedCSV    = "id,picks\n1,a|b\n2,\n3,b\n4,\n"
	droppedSchema = `[{"name":"id","type":"u8"},{"name":"picks","type":"set_u8"}]`
)

// droppedDetails is the warning's details contract.
type droppedDetails struct {
	Dropped      int `json:"dropped"`
	Imported     int `json:"imported"`
	FirstFailure struct {
		Row    int    `json:"row"`
		Column string `json:"column"`
		Reason string `json:"reason"`
	} `json:"first_failure"`
	Stage string `json:"stage"`
}

// wantDropped checks one warning's details against the fixture's exact
// figures (rows 2 and 4 dropped, rows 1 and 3 imported).
func wantDropped(t *testing.T, raw any, stage string) {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var d droppedDetails
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("details %s: %v", b, err)
	}
	if d.Dropped != 2 || d.Imported != 2 || d.FirstFailure.Row != 2 || d.FirstFailure.Column != "picks" ||
		d.FirstFailure.Reason != "null value in non-nullable field" || d.Stage != stage {
		t.Errorf("details = %s, want dropped 2, imported 2, first_failure {2, picks, null value in non-nullable field}, stage %q", b, stage)
	}
}

// droppedWarnings filters coded warnings to PULSE_IMPORT_ROWS_DROPPED.
func droppedWarnings(ws []*perrors.CodedError) []*perrors.CodedError {
	var out []*perrors.CodedError
	for _, w := range ws {
		if w != nil && w.Code == perrors.PULSE_IMPORT_ROWS_DROPPED {
			out = append(out, w)
		}
	}
	return out
}

func wantOneDropped(t *testing.T, ws []*perrors.CodedError, stage string) {
	t.Helper()
	got := droppedWarnings(ws)
	if len(got) != 1 {
		t.Fatalf("PULSE_IMPORT_ROWS_DROPPED warnings = %d, want 1 (%v)", len(got), ws)
	}
	wantDropped(t, got[0].Details, stage)
}

func wantStrictRefusal(t *testing.T, err error, stage string) {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_IMPORT_ROWS_DROPPED {
		t.Fatalf("strict err = %v, want PULSE_IMPORT_ROWS_DROPPED as the error's own code", err)
	}
	wantDropped(t, ce.Details, stage)
}

func wantAbsent(t *testing.T, afs afero.Fs, path string) {
	t.Helper()
	if ok, _ := afero.Exists(afs, path); ok {
		t.Errorf("%s was written under strict", path)
	}
}

func wantRecords(t *testing.T, afs afero.Fs, path string, n uint64) {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.CountRecords(context.Background(), path)
	if err != nil {
		t.Fatalf("CountRecords %s: %v", path, err)
	}
	if got != n {
		t.Errorf("%s holds %d records, want %d", path, got, n)
	}
}

// TestImportRowsDropped_CLI: the CLI leaves, warning then --strict.
func TestImportRowsDropped_CLI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PULSE_DATA_DIR", dir)
	t.Setenv("PULSE_IMPORTS_DIR", "imports")
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	src := writeExitFile(t, filepath.Join(dir, "partial.csv"), droppedCSV)
	schema := writeExitFile(t, filepath.Join(dir, "partial.schema.json"), droppedSchema)
	osfs := afero.NewOsFs()

	cases := []struct {
		name   string
		args   func(strict bool) []string
		stage  string
		output string // the cohort the leaf writes (absent under strict)
	}{
		{name: "import csv", output: filepath.Join(dir, "p.pulse"), args: func(strict bool) []string {
			a := []string{"import", "csv", "--json", "-i", src, "-o", filepath.Join(dir, "p.pulse"), "--schema", schema}
			if strict {
				a = append(a, "--strict")
			}
			return a
		}},
		{name: "import auto", output: filepath.Join(dir, "imports", "partial.pulse"), args: func(strict bool) []string {
			a := []string{"import", "auto", "--json", "--overwrite", "--schema", schema, "partial.csv"}
			if strict {
				a = append(a, "--strict")
			}
			return a
		}},
		{name: "convert", stage: iio.DroppedStageKeepPulse, output: filepath.Join(dir, "k.pulse"), args: func(strict bool) []string {
			a := []string{"convert", "--json", "--schema", schema, "--keep-pulse", filepath.Join(dir, "k.pulse"), src, filepath.Join(dir, "out.tsv")}
			if strict {
				a = append(a, "--strict")
			}
			return a
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Strict first, so a cohort on disk can only be strict's.
			s, out, _ := runExit(t, c.args(true)...)
			env := decodeExitEnvelope(t, "strict", out)
			if s != pcli.ExitFatal || env == nil || len(env.Errors) != 1 || env.Errors[0].Code != string(perrors.PULSE_IMPORT_ROWS_DROPPED) {
				t.Fatalf("--strict: exit %d (want 1), envelope %s", s, out)
			}
			wantDropped(t, env.Errors[0].Details, c.stage)
			wantAbsent(t, osfs, c.output)

			s, out, _ = runExit(t, c.args(false)...)
			env = decodeExitEnvelope(t, "warn", out)
			if s != pcli.ExitOK || env == nil || len(env.Errors) != 0 {
				t.Fatalf("warn: exit %d (want 0), envelope %s", s, out)
			}
			var hits []*descriptor.EnvelopeEntry
			for _, w := range env.Warnings {
				if w.Code == string(perrors.PULSE_IMPORT_ROWS_DROPPED) {
					hits = append(hits, w)
				}
			}
			if len(hits) != 1 {
				t.Fatalf("warnings carry %d PULSE_IMPORT_ROWS_DROPPED, want 1: %s", len(hits), out)
			}
			wantDropped(t, hits[0].Details, c.stage)
			wantRecords(t, osfs, c.output, 2)
		})
	}
}

// newDroppedPulse is a hermetic instance over a MemMapFs seeded with the
// fixture.
func newDroppedPulse(t *testing.T, strict bool) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	afs := afero.NewMemMapFs()
	if err := afero.WriteFile(afs, "partial.csv", []byte(droppedCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: afs, Strict: strict})
	if err != nil {
		t.Fatal(err)
	}
	return p, afs
}

// TestImportRowsDropped_Library: Import, ImportFile, Convert and MCP
// pulse_import, warning then Options.Strict.
func TestImportRowsDropped_Library(t *testing.T) {
	ctx := context.Background()
	defs := []iio.SchemaFileField{{Name: "id", Type: "u8"}, {Name: "picks", Type: "set_u8"}}

	type outcome struct {
		warns  []*perrors.CodedError
		rows   int
		output string
		stage  string
	}
	entries := []struct {
		name string
		run  func(t *testing.T, p *pulse.Pulse, afs afero.Fs) (outcome, error)
	}{
		{name: "Import", run: func(t *testing.T, p *pulse.Pulse, afs afero.Fs) (outcome, error) {
			s, err := iio.SchemaFromFields(defs)
			if err != nil {
				t.Fatal(err)
			}
			r, err := pio.NewReader(pio.FormatCSV, afs, "partial.csv", pio.ReaderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			job := pio.NewImportJob(r, "lib.pulse")
			job.Schema = s
			rep, err := p.Import(ctx, job)
			if err != nil {
				return outcome{output: "lib.pulse"}, err
			}
			if len(rep.RowErrors) != 2 {
				t.Errorf("RowErrors = %d, want 2 (unchanged)", len(rep.RowErrors))
			}
			return outcome{warns: rep.DroppedWarnings, rows: rep.RowsImported, output: "lib.pulse"}, nil
		}},
		{name: "ImportFile", run: func(t *testing.T, p *pulse.Pulse, afs afero.Fs) (outcome, error) {
			s, err := iio.SchemaFromFields(defs)
			if err != nil {
				t.Fatal(err)
			}
			res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "partial.csv", Schema: s})
			if err != nil {
				return outcome{output: "imports/partial.pulse"}, err
			}
			if res.RowsDropped != 2 {
				t.Errorf("RowsDropped = %d, want 2", res.RowsDropped)
			}
			return outcome{warns: res.DroppedWarnings, rows: res.RowsImported, output: res.Path}, nil
		}},
		{name: "MCP pulse_import", run: func(t *testing.T, p *pulse.Pulse, afs afero.Fs) (outcome, error) {
			in := mcp.ImportIn{Source: "partial.csv", Schema: []mcp.ImportSchemaField{{Name: "id", Type: "u8"}, {Name: "picks", Type: "set_u8"}}}
			res, err := mcp.HandleImport(ctx, p, in)
			if err != nil {
				return outcome{output: "imports/partial.pulse"}, err
			}
			if res.RowsDropped != 2 {
				t.Errorf("rows_dropped = %d, want 2", res.RowsDropped)
			}
			return outcome{warns: res.DroppedWarnings, rows: res.RowsImported, output: res.Path}, nil
		}},
		{name: "Convert", run: func(t *testing.T, p *pulse.Pulse, afs afero.Fs) (outcome, error) {
			s, err := iio.SchemaFromFields(defs)
			if err != nil {
				t.Fatal(err)
			}
			r, err := pio.NewReader(pio.FormatCSV, afs, "partial.csv", pio.ReaderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			w, err := pio.NewWriter(pio.FormatTSV, afs, "out.tsv", pio.WriterOptions{})
			if err != nil {
				t.Fatal(err)
			}
			job := iio.NewConvertJob(r, w)
			job.Schema = s
			job.KeepPulseAt = "keep.pulse"
			rep, err := p.Convert(ctx, job)
			if err != nil {
				return outcome{output: "keep.pulse", stage: iio.DroppedStageKeepPulse}, err
			}
			if rep.RowsConverted != 4 {
				t.Errorf("RowsConverted = %d, want 4 (the text pass drops nothing)", rep.RowsConverted)
			}
			return outcome{warns: rep.DroppedWarnings, rows: 2, output: "keep.pulse", stage: iio.DroppedStageKeepPulse}, nil
		}},
	}
	for _, e := range entries {
		t.Run(e.name, func(t *testing.T) {
			p, afs := newDroppedPulse(t, false)
			o, err := e.run(t, p, afs)
			if err != nil {
				t.Fatalf("partial import failed: %v", err)
			}
			if o.rows != 2 {
				t.Errorf("rows imported = %d, want 2", o.rows)
			}
			wantOneDropped(t, o.warns, o.stage)
			wantRecords(t, afs, o.output, 2)

			ps, afs2 := newDroppedPulse(t, true)
			o, err = e.run(t, ps, afs2)
			wantStrictRefusal(t, err, o.stage)
			wantAbsent(t, afs2, o.output)
		})
	}
}
