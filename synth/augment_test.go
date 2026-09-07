package synth_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// setupAugmentFixture builds a 200-row source cohort, profiles it, and
// returns the pulse handle + profile-derived spec ready to hand to
// p.Synth with SourceCohort set. Callers pick the RowCount override and
// output/source paths.
func setupAugmentFixture(t *testing.T, fs afero.Fs, sourcePath string, sourceRows int) (*pulse.Pulse, *synth.Spec) {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if _, err := p.Synth(context.Background(), smallSpec(sourceRows), sourcePath,
		pulse.SynthOptions{Seed: 1}); err != nil {
		t.Fatalf("source synth: %v", err)
	}
	prof, err := p.Profile(context.Background(), sourcePath, pulse.ProfileOptions{IncludeStats: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	return p, synth.SpecFromProfile(prof, 0) // RowCount set per-call by caller
}

// TestAugmentFromProfile_AppendsExactlyOneSyntheticField locks in
// acceptance criterion 1: the output schema has exactly one new field
// (_synthetic, packed_bool) versus the source schema.
func TestAugmentFromProfile_AppendsExactlyOneSyntheticField(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, spec := setupAugmentFixture(t, fs, "/source.pulse", 200)
	spec.RowCount = 500

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse",
		pulse.SynthOptions{Seed: 2, SourceCohort: "/source.pulse"}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	srcInsp, err := p.Inspect(context.Background(), "/source.pulse")
	if err != nil {
		t.Fatalf("inspect source: %v", err)
	}
	outInsp, err := p.Inspect(context.Background(), "/augmented.pulse")
	if err != nil {
		t.Fatalf("inspect output: %v", err)
	}

	if len(outInsp.Fields) != len(srcInsp.Fields)+1 {
		t.Fatalf("output field count = %d, want source field count (%d) + 1",
			len(outInsp.Fields), len(srcInsp.Fields))
	}
	last := outInsp.Fields[len(outInsp.Fields)-1]
	if last.Name != synth.SyntheticFieldName {
		t.Errorf("appended field name = %q, want %q", last.Name, synth.SyntheticFieldName)
	}
	if last.Type != "packed_bool" {
		t.Errorf("appended field type = %q, want packed_bool", last.Type)
	}
	for i, sf := range srcInsp.Fields {
		if outInsp.Fields[i].Name != sf.Name || outInsp.Fields[i].Type != sf.Type {
			t.Errorf("field %d = (%s,%s), want (%s,%s)",
				i, outInsp.Fields[i].Name, outInsp.Fields[i].Type, sf.Name, sf.Type)
		}
	}
}

// TestAugmentFromProfile_TagsRealAndGeneratedPartitions locks in
// acceptance criterion 2: every row copied from source has
// _synthetic=false, every newly generated row has _synthetic=true, and
// the two partitions are the exact sizes requested (source rows, then
// generated rows).
func TestAugmentFromProfile_TagsRealAndGeneratedPartitions(t *testing.T) {
	fs := afero.NewMemMapFs()
	const sourceRows, newRows = 200, 500
	p, spec := setupAugmentFixture(t, fs, "/source.pulse", sourceRows)
	spec.RowCount = newRows

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse",
		pulse.SynthOptions{Seed: 2, SourceCohort: "/source.pulse"}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	data, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	flags := readField(t, data, synth.SyntheticFieldName)
	if len(flags) != sourceRows+newRows {
		t.Fatalf("total rows = %d, want %d", len(flags), sourceRows+newRows)
	}
	for i, v := range flags {
		got := v != 0
		want := i >= sourceRows
		if got != want {
			t.Fatalf("row %d: _synthetic = %v, want %v", i, got, want)
		}
	}
}

// TestAugmentFromProfile_RowsIsExplicitCountNotTopUp is the FR-14
// regression: --rows 500 against a 200-row source produces a 700-row
// output (200 real + 500 synthetic), never a 500-row output.
func TestAugmentFromProfile_RowsIsExplicitCountNotTopUp(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, spec := setupAugmentFixture(t, fs, "/source.pulse", 200)
	spec.RowCount = 500

	res, err := p.Synth(context.Background(), spec, "/augmented.pulse",
		pulse.SynthOptions{Seed: 2, SourceCohort: "/source.pulse"})
	if err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	if res.RowsGenerated != 500 {
		t.Errorf("RowsGenerated = %d, want 500 (the explicit new-row count)", res.RowsGenerated)
	}

	data, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	total := readRecordCount(t, data)
	if total != 700 {
		t.Fatalf("total output rows = %d, want 700 (200 real + 500 synthetic), not 500", total)
	}
}

// TestAugmentFromProfile_SourceUntouched asserts the source cohort's
// bytes and mtime are unchanged after a from-profile run — the source is
// never opened for write, explicit test rather than "no error".
func TestAugmentFromProfile_SourceUntouched(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, spec := setupAugmentFixture(t, fs, "/source.pulse", 200)
	spec.RowCount = 500

	beforeBytes, err := afero.ReadFile(fs, "/source.pulse")
	if err != nil {
		t.Fatalf("read source before: %v", err)
	}
	beforeInfo, err := fs.Stat("/source.pulse")
	if err != nil {
		t.Fatalf("stat source before: %v", err)
	}
	beforeMod := beforeInfo.ModTime()

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse",
		pulse.SynthOptions{Seed: 2, SourceCohort: "/source.pulse"}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	afterBytes, err := afero.ReadFile(fs, "/source.pulse")
	if err != nil {
		t.Fatalf("read source after: %v", err)
	}
	afterInfo, err := fs.Stat("/source.pulse")
	if err != nil {
		t.Fatalf("stat source after: %v", err)
	}

	if string(beforeBytes) != string(afterBytes) {
		t.Error("source cohort bytes changed after synth from-profile run")
	}
	if !beforeMod.Equal(afterInfo.ModTime()) {
		t.Errorf("source cohort mtime changed: before=%v after=%v", beforeMod, afterInfo.ModTime())
	}
}

// TestAugmentFromProfile_RefusesMissingSource asserts a clear coded
// error rather than a panic or silent misbehavior when the augment path
// is invoked directly with no source path.
func TestAugmentFromProfile_RefusesMissingSource(t *testing.T) {
	fs := afero.NewMemMapFs()
	_, spec := setupAugmentFixture(t, fs, "/source.pulse", 50)
	spec.RowCount = 10

	_, err := synth.AugmentFromProfile(fs, spec, "", "/augmented2.pulse", synth.Options{Seed: 1})
	if !errors.HasCode(err, errors.PULSE_SYNTH_SOURCE_REQUIRED) {
		t.Fatalf("expected PULSE_SYNTH_SOURCE_REQUIRED, got %v", err)
	}
}

// TestAugmentFromProfile_RefusesMissingOutput asserts a clear coded
// error when no output path is given.
func TestAugmentFromProfile_RefusesMissingOutput(t *testing.T) {
	fs := afero.NewMemMapFs()
	_, spec := setupAugmentFixture(t, fs, "/source.pulse", 50)
	spec.RowCount = 10

	_, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "", synth.Options{Seed: 1})
	if !errors.HasCode(err, errors.PULSE_SYNTH_OUTPUT_REQUIRED) {
		t.Fatalf("expected PULSE_SYNTH_OUTPUT_REQUIRED, got %v", err)
	}
}

// TestAugmentFromProfile_RefusesOutputEqualToSource asserts the source
// is never silently mutated: an output path resolving to the same file
// as the source is refused outright.
func TestAugmentFromProfile_RefusesOutputEqualToSource(t *testing.T) {
	fs := afero.NewMemMapFs()
	_, spec := setupAugmentFixture(t, fs, "/source.pulse", 50)
	spec.RowCount = 10

	_, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/source.pulse", synth.Options{Seed: 1})
	if !errors.HasCode(err, errors.PULSE_SYNTH_OUTPUT_COLLISION) {
		t.Fatalf("expected PULSE_SYNTH_OUTPUT_COLLISION, got %v", err)
	}

	// An uncleaned-but-equivalent path must be caught too.
	_, err = synth.AugmentFromProfile(fs, spec, "/source.pulse", "/./source.pulse", synth.Options{Seed: 1})
	if !errors.HasCode(err, errors.PULSE_SYNTH_OUTPUT_COLLISION) {
		t.Fatalf("expected PULSE_SYNTH_OUTPUT_COLLISION for equivalent-but-uncleaned path, got %v", err)
	}
}

// TestAugmentFromProfile_RefusesAlreadyTaggedSource asserts a source
// cohort that already declares a _synthetic field is refused rather
// than silently gaining a second, ambiguous provenance column.
func TestAugmentFromProfile_RefusesAlreadyTaggedSource(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, spec := setupAugmentFixture(t, fs, "/source.pulse", 50)
	spec.RowCount = 10

	if _, err := p.Synth(context.Background(), spec, "/once.pulse",
		pulse.SynthOptions{Seed: 2, SourceCohort: "/source.pulse"}); err != nil {
		t.Fatalf("first augment: %v", err)
	}

	// /once.pulse now already carries _synthetic; using it as a source
	// again must be refused.
	prof2, err := p.Profile(context.Background(), "/once.pulse", pulse.ProfileOptions{IncludeStats: true})
	if err != nil {
		t.Fatalf("profile once.pulse: %v", err)
	}
	spec2 := synth.SpecFromProfile(prof2, 5)
	_, err = synth.AugmentFromProfile(fs, spec2, "/once.pulse", "/twice.pulse", synth.Options{Seed: 3})
	if !errors.HasCode(err, errors.PULSE_SYNTH_ALREADY_TAGGED) {
		t.Fatalf("expected PULSE_SYNTH_ALREADY_TAGGED, got %v", err)
	}
}

// TestAugmentFromProfile_CategoricalValuesResolveCorrectly asserts that
// real rows' categorical values survive the merge with their original
// string values intact (the merged output gets a fresh, shared
// dictionary distinct from the source's own — real rows must still
// resolve to the same category names they had in the source).
func TestAugmentFromProfile_CategoricalValuesResolveCorrectly(t *testing.T) {
	fs := afero.NewMemMapFs()
	const sourceRows = 200
	p, spec := setupAugmentFixture(t, fs, "/source.pulse", sourceRows)
	spec.RowCount = 300

	sourceData, err := afero.ReadFile(fs, "/source.pulse")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	wantReal := readCategoricalField(t, sourceData, "country")

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse",
		pulse.SynthOptions{Seed: 2, SourceCohort: "/source.pulse"}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	augData, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	gotAll := readCategoricalField(t, augData, "country")
	if len(gotAll) != sourceRows+300 {
		t.Fatalf("total rows = %d, want %d", len(gotAll), sourceRows+300)
	}
	gotReal := gotAll[:sourceRows]
	for i := range wantReal {
		if gotReal[i] != wantReal[i] {
			t.Fatalf("row %d: country = %q, want %q (source value)", i, gotReal[i], wantReal[i])
		}
	}
	// Every generated category must be one of the values the source
	// profile actually observed (weighted_categorical only draws from
	// its declared top-K values).
	allowed := map[string]bool{}
	for _, v := range wantReal {
		allowed[v] = true
	}
	for i, v := range gotAll[sourceRows:] {
		if !allowed[v] {
			t.Errorf("generated row %d: country = %q, not among source's observed categories", i, v)
		}
	}
}

// TestAugmentFromProfile_RefusesSchemaMismatch asserts that a spec whose
// fields don't line up with the source cohort's own schema (a profile
// captured from a different cohort, or a source that has since changed
// shape) is refused with a clear coded error rather than silently
// misinterpreting one side's bytes as the other's.
func TestAugmentFromProfile_RefusesSchemaMismatch(t *testing.T) {
	fs := afero.NewMemMapFs()
	_, spec := setupAugmentFixture(t, fs, "/source.pulse", 50)
	spec.RowCount = 5
	// Drop a field so the profile-derived spec no longer matches the
	// 3-field source schema (id, score, country).
	spec.Fields = spec.Fields[:len(spec.Fields)-1]

	_, err := synth.AugmentFromProfile(fs, spec, "/source.pulse", "/augmented.pulse", synth.Options{Seed: 1})
	if !errors.HasCode(err, errors.PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH) {
		t.Fatalf("expected PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH, got %v", err)
	}
}

// TestSynth_PlainPathUnaffectedByAugmentAddition is the FR-20/FR-21
// backward-compat lock: SourceCohort left unset (today's default)
// reproduces byte-identical plain-synthesis output, with no
// _synthetic field anywhere.
func TestSynth_PlainPathUnaffectedByAugmentAddition(t *testing.T) {
	spec := smallSpec(50)
	a, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	b, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7, SourceCohort: ""})
	if err != nil {
		t.Fatalf("synth with explicit empty SourceCohort: %v", err)
	}
	if string(a) != string(b) {
		t.Error("empty SourceCohort changed plain-synthesis output")
	}

	r := bytes.NewReader(a)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if schema.Field(synth.SyntheticFieldName) != nil {
		t.Error("plain synthesis output unexpectedly carries a _synthetic field")
	}
}
