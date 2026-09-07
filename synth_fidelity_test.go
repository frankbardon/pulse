package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// fidelitySmallSpec mirrors synth's own smallSpec test fixture (one
// monotonic numeric id, one normal-distributed numeric score, one
// weighted-categorical field) so the fidelity report has both a
// TEST_KS-eligible and a TEST_CHISQ-eligible field to exercise.
func fidelitySmallSpec(rows int) *synth.Spec {
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
			{Name: "score", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "country", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"US", "UK", "DE"},
					"weights": []any{0.5, 0.3, 0.2},
				}},
		},
	}
}

// setupFidelityFixture builds a 200-row source cohort and its profile,
// returning the pulse handle and a profile-derived spec ready for a
// from-profile augment call.
func setupFidelityFixture(t *testing.T, fs afero.Fs) (*Pulse, *synth.Spec) {
	t.Helper()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Synth(context.Background(), fidelitySmallSpec(200), "/source.pulse",
		SynthOptions{Seed: 1}); err != nil {
		t.Fatalf("source synth: %v", err)
	}
	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{IncludeStats: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	return p, synth.SpecFromProfile(prof, 300)
}

// TestSynth_FidelityReportOmittedWritesNoFile locks in acceptance
// criterion 1: leaving --fidelity-report / FidelityReportPath unset
// changes nothing — no report file, no behavior change, and
// Result.FidelityReportPath stays empty.
func TestSynth_FidelityReportOmittedWritesNoFile(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, spec := setupFidelityFixture(t, fs)

	res, err := p.Synth(context.Background(), spec, "/augmented.pulse",
		SynthOptions{Seed: 2, SourceCohort: "/source.pulse"})
	if err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	if res.FidelityReportPath != "" {
		t.Errorf("FidelityReportPath = %q, want empty when the flag was not set", res.FidelityReportPath)
	}
	if exists, _ := afero.Exists(fs, "/report.json"); exists {
		t.Error("no report path was requested, but a file exists anyway")
	}
}

// TestSynth_FidelityReportIgnoredWithoutSourceCohort asserts
// FidelityReportPath has no effect on the plain synthesis path
// (SourceCohort empty) — there is no _synthetic partition to compare.
func TestSynth_FidelityReportIgnoredWithoutSourceCohort(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := p.Synth(context.Background(), fidelitySmallSpec(50), "/plain.pulse",
		SynthOptions{Seed: 3, FidelityReportPath: "/report.json"})
	if err != nil {
		t.Fatalf("Synth: %v", err)
	}
	if res.FidelityReportPath != "" {
		t.Errorf("FidelityReportPath = %q, want empty (SourceCohort unset)", res.FidelityReportPath)
	}
	if exists, _ := afero.Exists(fs, "/report.json"); exists {
		t.Error("plain synthesis path wrote a fidelity report despite SourceCohort being empty")
	}
}

// TestSynth_FidelityReportWrittenWhenRequested locks in acceptance
// criterion 2: a valid JSON report lands at the requested path,
// covering every numeric field via TEST_KS and every categorical
// field via TEST_CHISQ, with no per-field errors on this
// well-behaved fixture.
func TestSynth_FidelityReportWrittenWhenRequested(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, spec := setupFidelityFixture(t, fs)

	res, err := p.Synth(context.Background(), spec, "/augmented.pulse",
		SynthOptions{Seed: 2, SourceCohort: "/source.pulse", FidelityReportPath: "/report.json"})
	if err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	if res.FidelityReportPath != "/report.json" {
		t.Errorf("FidelityReportPath = %q, want /report.json", res.FidelityReportPath)
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}

	if report.SourceRows != 200 || report.SyntheticRows != 300 {
		t.Errorf("SourceRows/SyntheticRows = %d/%d, want 200/300", report.SourceRows, report.SyntheticRows)
	}

	byField := map[string]*synth.FieldFidelity{}
	for _, f := range report.Fields {
		byField[f.Field] = f
	}
	if len(byField) != 3 {
		t.Fatalf("report has %d field entries, want 3 (id, score, country)", len(byField))
	}
	for _, name := range []string{"id", "score"} {
		f := byField[name]
		if f == nil {
			t.Fatalf("missing report entry for %q", name)
		}
		if f.Test != types.TEST_KS {
			t.Errorf("%s: Test = %s, want TEST_KS", name, f.Test)
		}
		if f.Error != "" {
			t.Errorf("%s: unexpected Error %q", name, f.Error)
		}
		if f.Result == nil {
			t.Fatalf("%s: Result is nil", name)
		}
	}
	country := byField["country"]
	if country == nil {
		t.Fatal("missing report entry for country")
	}
	if country.Test != types.TEST_CHISQ {
		t.Errorf("country: Test = %s, want TEST_CHISQ", country.Test)
	}
	if country.Error != "" {
		t.Errorf("country: unexpected Error %q", country.Error)
	}
	if country.Result == nil {
		t.Fatal("country: Result is nil")
	}
}

// TestSynth_FidelityReportOmitsPairwiseKey locks in acceptance
// criterion 4: the wire JSON carries no "pairwise" key at all — later
// epics add it as a genuinely new key, not an emptied-out placeholder.
func TestSynth_FidelityReportOmitsPairwiseKey(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, spec := setupFidelityFixture(t, fs)

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse",
		SynthOptions{Seed: 2, SourceCohort: "/source.pulse", FidelityReportPath: "/report.json"}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if _, ok := wire["pairwise"]; ok {
		t.Error("report JSON carries a \"pairwise\" key; this story must leave it entirely absent")
	}
}

// TestSynth_FidelityReportMatchesHandRunTest locks in acceptance
// criterion 3: the report's numeric-field and categorical-field
// statistics are exactly what a hand-built TEST_KS / TEST_CHISQ
// request against the same tagged output cohort produces. "Hand-run"
// here means the same bridging path a caller would have to use
// themselves — _synthetic is on-wire packed_bool, so both TEST_KS's
// SplitBy and TEST_CHISQ's Cols require the same presentation-schema
// widening synth.SyntheticAsCategoricalSchema performs; this test
// builds that request independently of buildFidelityReport/
// runFidelityTest to prove the report is not fabricating numbers.
func TestSynth_FidelityReportMatchesHandRunTest(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, spec := setupFidelityFixture(t, fs)

	res, err := p.Synth(context.Background(), spec, "/augmented.pulse",
		SynthOptions{Seed: 2, SourceCohort: "/source.pulse", FidelityReportPath: "/report.json"})
	if err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	var scoreEntry, countryEntry *synth.FieldFidelity
	for _, f := range report.Fields {
		switch f.Field {
		case "score":
			scoreEntry = f
		case "country":
			countryEntry = f
		}
	}
	if scoreEntry == nil || countryEntry == nil {
		t.Fatal("report missing score and/or country entries")
	}

	// Independently open the output cohort and drive the same two
	// operators by hand.
	data, err := afero.ReadFile(fs, res.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	physical, records := parseFidelityFixtureCohort(t, data)
	presented := synth.SyntheticAsCategoricalSchema(physical)

	ksResp := runHandBuiltFidelityRequest(t, physical, presented, records,
		&types.Test{Type: types.TEST_KS, Field: "score", SplitBy: synth.SyntheticFieldName})
	if ksResp.Statistic != scoreEntry.Result.Statistic || ksResp.PValue != scoreEntry.Result.PValue {
		t.Errorf("score: report stat/p = %v/%v, hand-run stat/p = %v/%v",
			scoreEntry.Result.Statistic, scoreEntry.Result.PValue, ksResp.Statistic, ksResp.PValue)
	}

	chisqResp := runHandBuiltFidelityRequest(t, physical, presented, records,
		&types.Test{Type: types.TEST_CHISQ, Rows: "country", Cols: synth.SyntheticFieldName})
	if chisqResp.Statistic != countryEntry.Result.Statistic || chisqResp.PValue != countryEntry.Result.PValue {
		t.Errorf("country: report stat/p = %v/%v, hand-run stat/p = %v/%v",
			countryEntry.Result.Statistic, countryEntry.Result.PValue, chisqResp.Statistic, chisqResp.PValue)
	}
}

// parseFidelityFixtureCohort splits a .pulse file's bytes into its
// physical schema and raw record bytes.
func parseFidelityFixtureCohort(t *testing.T, data []byte) (*encoding.Schema, []byte) {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	return schema, data[len(data)-r.Len():]
}

// runHandBuiltFidelityRequest drives a single Test through the
// ordinary Process pipeline exactly as a caller would: decode against
// physical, attach presented for accessor purposes, run one Test.
func runHandBuiltFidelityRequest(t *testing.T, physical, presented *encoding.Schema, records []byte, test *types.Test) *types.TestResult {
	t.Helper()
	req := &types.Request{Tests: []*types.Test{test}}
	resp, err := processing.NewProcessor(presented).Process(
		context.Background(), req, newFidelityIterator(records, physical, presented))
	if err != nil {
		t.Fatalf("hand-run %s: %v", test.Type, err)
	}
	if len(resp.Tests) != 1 {
		t.Fatalf("hand-run %s: got %d test results, want 1", test.Type, len(resp.Tests))
	}
	return resp.Tests[0]
}
