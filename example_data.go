package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
	iio "github.com/frankbardon/pulse/internal/io"
	isynth "github.com/frankbardon/pulse/internal/synth"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// The derived fixtures every served example can name besides the
// per-CSV cohorts. The names are the ones the examples read.
const (
	exampleShardedCohort  = "transactions_sharded"
	exampleShardSource    = "transactions"
	exampleTwinCohort     = "customers_twin"
	exampleTwinSource     = "customers"
	exampleTwinProfile    = "customers.profile.json"
	exampleIndexedCohort  = "experiment"
	exampleIndexKey       = "id"
	exampleTwinRows       = 200
	exampleTwinSeed       = 7
	exampleDataFileMode   = 0o644
	exampleDataDirMode    = 0o755
	exampleCohortFromCSV  = "csv"
	exampleCohortFromShrd = "shards"
	exampleCohortFromSyn  = "synth"
)

// ExampleDataOptions configures WriteExampleData.
type ExampleDataOptions struct {
	// Overwrite replaces fixture files already present in the
	// directory. Without it the call refuses (SERVICE_VALIDATION,
	// details.reason "target_exists", details.paths the files found)
	// before it writes anything.
	Overwrite bool
}

// ExampleDataCohort is one cohort WriteExampleData wrote.
type ExampleDataCohort struct {
	// File is the cohort's file name inside the directory — the
	// `cohort.filename` the examples name (experiment.pulse).
	File string `json:"file"`
	// Path is File joined to the directory.
	Path string `json:"path"`
	// Records is the cohort's record count.
	Records int `json:"records"`
	// From says how it was built: "csv" (an embedded CSV imported
	// under its embedded schema), "shards" (a two-shard archive of
	// transactions) or "synth" (a synthetic twin of customers).
	From string `json:"from"`
}

// ExampleDataReport is WriteExampleData's result.
type ExampleDataReport struct {
	// Dir is the directory written to.
	Dir string `json:"dir"`
	// Cohorts lists every cohort written, sorted by File.
	Cohorts []ExampleDataCohort `json:"cohorts"`
	// Files lists the other files written (the point-lookup index and
	// its manifest, the twin's profile JSON), sorted.
	Files []string `json:"files"`
	// Warnings carries every non-fatal coded warning a step raised
	// (none on the shipped fixtures); a --json caller lifts them onto
	// the envelope.
	Warnings []*errors.CodedError `json:"-"`
}

// WriteExampleData writes the example fixture cohorts into dir through
// the instance filesystem, from CSVs and schemas embedded in the
// binary: every cohort the built-in examples and the Analysis Guide's
// worked examples read, so they run offline with dir as the data root
// (Options.DataDir, `pulse mcp --data-dir`; a CLI leaf run from dir).
// It writes what the repo's
// internal/examples/fixtures/build.sh writes: each embedded CSV
// imported under its schema, the point-lookup index of
// experiment.pulse on id, transactions_sharded.pulse (transactions
// split evenly into the shards wave1.pulse and wave2.pulse) and
// customers_twin.pulse (200 synthetic rows from a default profile of
// customers.pulse, seed 7, written beside it as
// customers.profile.json). The population file compose/06 loads is
// not written: running compose/05 saves it.
//
// dir is created when missing. An existing fixture file is refused
// unless opts.Overwrite (see ExampleDataOptions); other files in dir
// are left alone.
func (p *Pulse) WriteExampleData(ctx context.Context, dir string, opts ExampleDataOptions) (*ExampleDataReport, error) {
	if dir == "" {
		return nil, argumentError("pulse: example data requires a directory")
	}
	stems := examples.FixtureCohorts()
	if len(stems) == 0 {
		return nil, argumentError("pulse: this build embeds no example fixtures")
	}
	targets := make([]string, 0, len(stems)+3)
	for _, s := range stems {
		targets = append(targets, s+".pulse")
	}
	targets = append(targets, exampleShardedCohort+".pulse", exampleTwinCohort+".pulse", exampleTwinProfile)
	if err := p.clearExampleTargets(dir, targets, opts.Overwrite); err != nil {
		return nil, err
	}
	if err := p.fsys.MkdirAll(dir, exampleDataDirMode); err != nil {
		return nil, err
	}

	rep := &ExampleDataReport{Dir: dir, Cohorts: []ExampleDataCohort{}, Files: []string{}}
	src := afero.NewMemMapFs()
	for _, s := range stems {
		csvBody, err := examples.FixtureCSV(s)
		if err != nil {
			return nil, err
		}
		rows, err := importExampleCSV(ctx, p, src, s, csvBody, filepath.Join(dir, s+".pulse"), rep)
		if err != nil {
			return nil, err
		}
		rep.addCohort(dir, s, rows, exampleCohortFromCSV)
	}

	ix, err := p.BuildIndex(ctx, filepath.Join(dir, exampleIndexedCohort+".pulse"), []string{exampleIndexKey})
	if err != nil {
		return nil, err
	}
	rep.Files = append(rep.Files, ix.IndexPath)
	if ix.ManifestPath != "" {
		rep.Files = append(rep.Files, ix.ManifestPath)
	}

	rows, err := p.writeExampleShards(ctx, src, filepath.Join(dir, exampleShardedCohort+".pulse"), rep)
	if err != nil {
		return nil, err
	}
	rep.addCohort(dir, exampleShardedCohort, rows, exampleCohortFromShrd)

	rows, err = p.writeExampleTwin(ctx, dir, rep)
	if err != nil {
		return nil, err
	}
	rep.addCohort(dir, exampleTwinCohort, rows, exampleCohortFromSyn)

	sort.Slice(rep.Cohorts, func(i, j int) bool { return rep.Cohorts[i].File < rep.Cohorts[j].File })
	sort.Strings(rep.Files)
	return rep, nil
}

func (r *ExampleDataReport) addCohort(dir, stem string, records int, from string) {
	r.Cohorts = append(r.Cohorts, ExampleDataCohort{
		File: stem + ".pulse", Path: filepath.Join(dir, stem+".pulse"), Records: records, From: from,
	})
}

// clearExampleTargets refuses (or, under overwrite, removes) the files
// the call is about to write, before any is written: the shard archive
// and the synthetic output refuse an existing target of their own, and
// build.sh removes them first too.
func (p *Pulse) clearExampleTargets(dir string, names []string, overwrite bool) error {
	var found []string
	for _, n := range names {
		path := filepath.Join(dir, n)
		if _, err := p.fsys.Stat(path); err == nil {
			found = append(found, path)
		}
	}
	if len(found) == 0 {
		return nil
	}
	if !overwrite {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"example data target already exists; pass overwrite to replace it",
			map[string]any{"dir": dir, "paths": found, "reason": "target_exists"})
	}
	for _, path := range found {
		if err := p.fsys.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

// importExampleCSV imports one CSV body (staged on src) under the named
// fixture's embedded schema into target through via — the job
// `pulse import csv --schema` runs.
func importExampleCSV(ctx context.Context, via *Pulse, src afero.Fs, stem string, body []byte, target string, rep *ExampleDataReport) (int, error) {
	rawSchema, err := examples.FixtureSchema(stem)
	if err != nil {
		return 0, err
	}
	schema, err := iio.ParseSchemaJSON(rawSchema)
	if err != nil {
		return 0, err
	}
	staged := "/" + filepath.Base(target) + ".csv"
	if err := afero.WriteFile(src, staged, body, exampleDataFileMode); err != nil {
		return 0, err
	}
	reader, err := pio.NewReader(pio.Format("csv"), src, staged, pio.ReaderOptions{})
	if err != nil {
		return 0, err
	}
	job := pio.NewImportJob(reader, target)
	job.Schema = schema
	report, err := via.Import(ctx, job)
	if err != nil {
		return 0, err
	}
	for _, ws := range [][]*errors.CodedError{report.SourceWarnings, report.GroupWarnings, report.NullWarnings,
		report.WidthWarnings, report.ZoneWarnings, report.DroppedWarnings} {
		rep.Warnings = append(rep.Warnings, ws...)
	}
	return report.RowsImported, nil
}

// writeExampleShards writes the two-shard transactions archive:
// transactions.csv's rows split evenly by line (the first half under
// wave1.pulse, the rest under wave2.pulse, each with the header), each
// imported under the transactions schema, the pair seeded into an
// archive. The shards are built on a scratch in-memory instance, so
// nothing but the archive reaches the target filesystem.
func (p *Pulse) writeExampleShards(ctx context.Context, src afero.Fs, archive string, rep *ExampleDataReport) (int, error) {
	body, err := examples.FixtureCSV(exampleShardSource)
	if err != nil {
		return 0, err
	}
	lines := bytes.SplitAfter(body, []byte("\n"))
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	if len(lines) < 3 {
		return 0, argumentError("pulse: the embedded transactions fixture is too short to shard")
	}
	half := (len(lines) - 1) / 2
	waves := map[string][]byte{
		"wave1": bytes.Join(lines[:half+1], nil),
		"wave2": append(append([]byte(nil), lines[0]...), bytes.Join(lines[half+1:], nil)...),
	}
	scratchFS := afero.NewMemMapFs()
	scratch, err := New(Options{FS: scratchFS})
	if err != nil {
		return 0, err
	}
	var shards []string
	for _, w := range []string{"wave1", "wave2"} {
		target := "/" + w + ".pulse"
		if _, err := importExampleCSV(ctx, scratch, src, exampleShardSource, waves[w], target, rep); err != nil {
			return 0, err
		}
		shards = append(shards, target)
	}
	staged := "/" + filepath.Base(archive)
	res, err := scratch.CreateShardArchive(ctx, staged, shards)
	if err != nil {
		return 0, err
	}
	for _, w := range res.Warnings {
		rep.Warnings = append(rep.Warnings, errors.NewCodedErrorWithDetails(errors.Code(w.Code), w.Message, w.Details))
	}
	built, err := afero.ReadFile(scratchFS, staged)
	if err != nil {
		return 0, err
	}
	if err := afero.WriteFile(p.fsys, archive, built, exampleDataFileMode); err != nil {
		return 0, err
	}
	return len(lines) - 1, nil
}

// writeExampleTwin writes customers.profile.json and customers_twin.pulse
// as `pulse profile create` (its default options) then `pulse synth
// from-profile --rows 200 --seed 7 --synthetic-only` do: the profile
// document is written, read back and translated to a spec.
func (p *Pulse) writeExampleTwin(ctx context.Context, dir string, rep *ExampleDataReport) (int, error) {
	source := filepath.Join(dir, exampleTwinSource+".pulse")
	prof, err := p.Profile(ctx, source, ProfileOptions{TopK: 32, IncludeStats: true, CorrelationTopK: 16})
	if err != nil {
		return 0, err
	}
	doc, err := json.MarshalIndent(prof, "", "  ")
	if err != nil {
		return 0, err
	}
	profPath := filepath.Join(dir, exampleTwinProfile)
	if err := afero.WriteFile(p.fsys, profPath, doc, exampleDataFileMode); err != nil {
		return 0, err
	}
	rep.Files = append(rep.Files, profPath)
	parsed, err := isynth.ParseProfile(doc)
	if err != nil {
		return 0, err
	}
	spec, _ := isynth.SpecFromProfile(parsed, exampleTwinRows)
	res, err := p.Synth(ctx, spec, filepath.Join(dir, exampleTwinCohort+".pulse"), SynthOptions{
		Seed: exampleTwinSeed, SourceCohort: source, SyntheticOnly: true,
	})
	if err != nil {
		return 0, err
	}
	return res.RowsGenerated, nil
}
