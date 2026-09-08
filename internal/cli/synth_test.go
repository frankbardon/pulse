package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	pulse "github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
)

// runSynthCLI drives a fresh SynthCommand with the supplied args,
// capturing stdout into buf so --json assertions can decode it. Mirrors
// runIndexCLI (index_test.go) for the sibling `pulse synth` command
// group.
func runSynthCLI(t *testing.T, buf *bytes.Buffer, args ...string) error {
	t.Helper()
	root := SynthCommand()
	root.Writer = buf
	return root.Run(context.Background(), append([]string{"synth"}, args...))
}

// runProfileCLI drives a fresh ProfileCommand with the supplied args,
// capturing stdout into buf. Mirrors runSynthCLI for the sibling
// `pulse profile` command group.
func runProfileCLI(t *testing.T, buf *bytes.Buffer, args ...string) error {
	t.Helper()
	root := ProfileCommand()
	root.Writer = buf
	return root.Run(context.Background(), append([]string{"profile"}, args...))
}

// synthLibraryCohort writes a synthetic .pulse cohort directly to the
// real OS filesystem at path via the library Synth call (bypassing the
// CLI) — used as test-fixture setup, exactly as index_test.go's
// writeIndexTestCohort builds fixtures via the encoding package
// directly rather than through a CLI leaf. newPulse() (shared with the
// production CLI leaves under test) wires afero.NewOsFs() with no
// DataDir, so fixtures land on real disk under t.TempDir().
func synthLibraryCohort(t *testing.T, path string, fields []synth.FieldSpec, rows int, seed int64) {
	t.Helper()
	p, err := newPulse()
	if err != nil {
		t.Fatalf("newPulse: %v", err)
	}
	spec := &pulse.SynthSpec{RowCount: rows, Fields: fields}
	if _, err := p.Synth(context.Background(), spec, path, pulse.SynthOptions{Seed: seed}); err != nil {
		t.Fatalf("p.Synth (fixture %s): %v", path, err)
	}
}

// writeBlockOrderedCategoricalCohort emits a minimal single-file .pulse
// cohort with two categorical_u8 fields, "region" and "plan", whose
// rows are block-ordered by region: rows [0, half) are all "us", rows
// [half, rowCount) are all "eu" — mirroring
// synth.buildBlockOrderedRegionPlanCohort
// (synth/conditional_categorical_reservoir_test.go), reimplemented here
// because that helper is unexported from an internal _test.go file.
// rowCount exceeding synth.conditionalJointCap (10000, unexported) is
// what makes `profile create --conditional`'s categorical-categorical
// reservoir sampling actually draw on its seeded RNG rather than
// retaining every row outright.
func writeBlockOrderedCategoricalCohort(t *testing.T, path string, rowCount, half int) {
	t.Helper()
	if half < 0 || half > rowCount {
		t.Fatalf("half=%d out of [0, rowCount=%d] range", half, rowCount)
	}
	regionDict := encoding.NewDictionary()
	for _, v := range []string{"us", "eu"} {
		if _, err := regionDict.Add(v); err != nil {
			t.Fatalf("regionDict.Add(%q): %v", v, err)
		}
	}
	planDict := encoding.NewDictionary()
	for _, v := range []string{"paid", "free"} {
		if _, err := planDict.Add(v); err != nil {
			t.Fatalf("planDict.Add(%q): %v", v, err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: regionDict},
			{Name: "plan", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, Dictionary: planDict},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for r := 0; r < rowCount; r++ {
		regionID := uint64(0) // "us"
		if r >= half {
			regionID = 1 // "eu"
		}
		planID := uint64(r % 2)
		if err := encoding.WriteFieldValue(&buf, schema.Fields[0].Type, regionID); err != nil {
			t.Fatalf("WriteFieldValue(region): %v", err)
		}
		if err := encoding.WriteFieldValue(&buf, schema.Fields[1].Type, planID); err != nil {
			t.Fatalf("WriteFieldValue(plan): %v", err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile cohort: %v", err)
	}
}

// readProfileJSON reads and decodes a profile document written by
// `profile create --output`. The CLI writes the raw synth.Profile JSON
// directly to that path (json.MarshalIndent(prof, ...)) — no envelope
// wrapping — regardless of whether --json was also passed for the
// stdout echo.
func readProfileJSON(t *testing.T, path string) *synth.Profile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var prof synth.Profile
	if err := json.Unmarshal(raw, &prof); err != nil {
		t.Fatalf("json.Unmarshal profile: %v\nraw: %s", err, raw)
	}
	return &prof
}

// synthFromProfileFixture builds a source cohort with rows and a
// captured profile document over it (via the real `profile create`
// leaf, plain — no --conditional/--fit-shape), returning both paths for
// use by `synth from-profile` CLI tests.
func synthFromProfileFixture(t *testing.T, dir string, rows int, seed int64) (sourcePath, profilePath string) {
	t.Helper()
	sourcePath = filepath.Join(dir, "source.pulse")
	synthLibraryCohort(t, sourcePath, []synth.FieldSpec{
		{Name: "v", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
	}, rows, seed)

	profilePath = filepath.Join(dir, "profile.json")
	var buf bytes.Buffer
	if err := runProfileCLI(t, &buf, "create", "--input", sourcePath, "--output", profilePath); err != nil {
		t.Fatalf("profile create (fixture): %v", err)
	}
	return sourcePath, profilePath
}

func containsSubstring(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// ---------- profile create ----------

// TestProfileCreateCLI_ConditionalFlagPopulatesConditionalSection
// verifies --conditional actually reaches ProfileOptions.IncludeConditional
// rather than merely being accepted and ignored: a profile captured
// without it must carry no Conditional section at all (nil, matching
// every pre-existing profile document), while one captured with it must
// carry exactly the numeric-numeric pair this two-field source supports.
func TestProfileCreateCLI_ConditionalFlagPopulatesConditionalSection(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.pulse")
	synthLibraryCohort(t, source, []synth.FieldSpec{
		{Name: "a", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
		{Name: "b", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{"mean": 5.0, "std": 2.0}},
	}, 200, 1)

	outPlain := filepath.Join(dir, "plain.json")
	var buf bytes.Buffer
	if err := runProfileCLI(t, &buf, "create", "--input", source, "--output", outPlain); err != nil {
		t.Fatalf("profile create (no --conditional): %v", err)
	}
	plain := readProfileJSON(t, outPlain)
	if plain.Conditional != nil {
		t.Errorf("Conditional = %+v, want nil when --conditional is not set", plain.Conditional)
	}

	outCond := filepath.Join(dir, "cond.json")
	buf.Reset()
	if err := runProfileCLI(t, &buf, "create", "--input", source, "--output", outCond, "--conditional"); err != nil {
		t.Fatalf("profile create (--conditional): %v", err)
	}
	cond := readProfileJSON(t, outCond)
	if cond.Conditional == nil || len(cond.Conditional.NumericPairs) != 1 {
		t.Fatalf("Conditional = %+v, want exactly 1 numeric pair when --conditional is set", cond.Conditional)
	}
	if pair := cond.Conditional.NumericPairs[0]; pair.N != 200 {
		t.Errorf("pair.N = %d, want 200 (every row has both a and b non-null)", pair.N)
	}
}

// TestProfileCreateCLI_FitShapeFlagPopulatesShape verifies --fit-shape
// actually reaches ProfileOptions.FitShape: profiling a clearly bimodal
// numeric field without the flag must leave Numeric.Shape nil (the
// plain-normal capture), while profiling the same field with it must
// populate a genuine 2-component mixture.
func TestProfileCreateCLI_FitShapeFlagPopulatesShape(t *testing.T) {
	dir := t.TempDir()
	bimodal := filepath.Join(dir, "bimodal.pulse")
	synthLibraryCohort(t, bimodal, []synth.FieldSpec{
		{Name: "v", Type: "f64", Distribution: synth.DistMixture, Params: map[string]any{
			"means":   []any{-10.0, 10.0},
			"stds":    []any{1.5, 1.5},
			"weights": []any{0.5, 0.5},
		}},
	}, 8000, 11)

	outPlain := filepath.Join(dir, "plain.json")
	var buf bytes.Buffer
	if err := runProfileCLI(t, &buf, "create", "--input", bimodal, "--output", outPlain); err != nil {
		t.Fatalf("profile create (no --fit-shape): %v", err)
	}
	plain := readProfileJSON(t, outPlain)
	if len(plain.Fields) != 1 || plain.Fields[0].Numeric == nil {
		t.Fatalf("expected exactly 1 numeric field, got %+v", plain.Fields)
	}
	if plain.Fields[0].Numeric.Shape != nil {
		t.Errorf("Numeric.Shape = %+v, want nil when --fit-shape is not set", plain.Fields[0].Numeric.Shape)
	}

	outShape := filepath.Join(dir, "shape.json")
	buf.Reset()
	if err := runProfileCLI(t, &buf, "create", "--input", bimodal, "--output", outShape, "--fit-shape"); err != nil {
		t.Fatalf("profile create (--fit-shape): %v", err)
	}
	shaped := readProfileJSON(t, outShape)
	shape := shaped.Fields[0].Numeric.Shape
	if shape == nil {
		t.Fatal("Numeric.Shape = nil, want a populated 2-component mixture for a clearly bimodal field with --fit-shape set")
	}
	if len(shape.Means) != 2 || len(shape.Stds) != 2 || len(shape.Weights) != 2 {
		t.Errorf("shape = %+v, want 2-component mixture", shape)
	}
}

// TestProfileCreateCLI_SeedFlagControlsReservoirSampling verifies
// --seed actually reaches ProfileOptions.Seed: --conditional's
// categorical-categorical contingency capture only draws from its
// seeded RNG once the source exceeds the internal reservoir cap
// (10000 rows), so a 30000-row block-ordered fixture makes the seed's
// effect on the captured output directly observable — same seed twice
// must reproduce byte-identical captured structure, and two different
// seeds must (overwhelmingly likely, given a reservoir of 10000 draws
// over a low-cardinality contingency table) diverge.
func TestProfileCreateCLI_SeedFlagControlsReservoirSampling(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "blockordered.pulse")
	writeBlockOrderedCategoricalCohort(t, source, 30000, 15000)

	profileWithSeed := func(name, seed string) *synth.Profile {
		out := filepath.Join(dir, name+".json")
		var buf bytes.Buffer
		if err := runProfileCLI(t, &buf, "create", "--input", source, "--output", out, "--conditional", "--seed", seed); err != nil {
			t.Fatalf("profile create --seed %s: %v", seed, err)
		}
		return readProfileJSON(t, out)
	}

	seed1RunA := profileWithSeed("seed1a", "1")
	seed1RunB := profileWithSeed("seed1b", "1")
	seed2Run := profileWithSeed("seed2", "2")

	if seed1RunA.Conditional == nil || len(seed1RunA.Conditional.CategoricalPairs) != 1 {
		t.Fatalf("Conditional = %+v, want exactly 1 categorical pair", seed1RunA.Conditional)
	}
	if !reflect.DeepEqual(seed1RunA, seed1RunB) {
		t.Error("same --seed value produced divergent captured reservoir output across two runs")
	}
	if reflect.DeepEqual(seed1RunA, seed2Run) {
		t.Error("different --seed values produced byte-identical reservoir output — --seed does not appear to reach ProfileOptions.Seed")
	}
}

func TestProfileCreateCLI_MissingInputFlagErrors(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.json")
	var buf bytes.Buffer
	if err := runProfileCLI(t, &buf, "create", "--output", out); err == nil {
		t.Fatal("expected error for missing required --input flag")
	}
}

// ---------- synth from-profile ----------

// TestSynthFromProfileCLI_SourceAndRowsFlagsProduceTaggedTopUp verifies
// --source and --rows (plus --output) reach AugmentFromProfile's tagged
// top-up path rather than a plain from-schema-style synthesis: the
// output cohort's total record count must equal the source's row count
// PLUS the --rows value, not --rows alone.
func TestSynthFromProfileCLI_SourceAndRowsFlagsProduceTaggedTopUp(t *testing.T) {
	dir := t.TempDir()
	const sourceRows = 50
	source, profile := synthFromProfileFixture(t, dir, sourceRows, 1)

	output := filepath.Join(dir, "augmented.pulse")
	const newRows = 30
	var buf bytes.Buffer
	if err := runSynthCLI(t, &buf, "from-profile",
		"--profile", profile, "--source", source, "--output", output,
		"--rows", strconv.Itoa(newRows), "--seed", "5", "--json"); err != nil {
		t.Fatalf("synth from-profile: %v", err)
	}

	var env struct {
		Data struct {
			RowsGenerated int    `json:"rows_generated"`
			RowsRejected  int    `json:"rows_rejected"`
			OutputPath    string `json:"output_path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if env.Data.RowsGenerated != newRows {
		t.Errorf("rows_generated = %d, want %d (--rows value, new rows only)", env.Data.RowsGenerated, newRows)
	}
	if env.Data.OutputPath != output {
		t.Errorf("output_path = %q, want %q (--output value)", env.Data.OutputPath, output)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("output cohort not written at --output path %q: %v", output, err)
	}

	p, err := newPulse()
	if err != nil {
		t.Fatalf("newPulse: %v", err)
	}
	total, err := p.CountRecords(context.Background(), output)
	if err != nil {
		t.Fatalf("CountRecords: %v", err)
	}
	if want := uint64(sourceRows + newRows); total != want {
		t.Errorf("total records in output = %d, want %d (sourceRows=%d + --rows=%d) — proves --source activated "+
			"the tagged top-up path rather than plain synthesis", total, want, sourceRows, newRows)
	}
}

// TestSynthFromProfileCLI_SeedFlagControlsGeneratedRows verifies --seed
// reaches the generator: same seed must reproduce a byte-identical
// output cohort, different seeds must (for a continuous normal field)
// diverge.
func TestSynthFromProfileCLI_SeedFlagControlsGeneratedRows(t *testing.T) {
	dir := t.TempDir()
	source, profile := synthFromProfileFixture(t, dir, 20, 1)

	genOnce := func(name, seed string) []byte {
		out := filepath.Join(dir, name+".pulse")
		var buf bytes.Buffer
		if err := runSynthCLI(t, &buf, "from-profile",
			"--profile", profile, "--source", source, "--output", out,
			"--rows", "40", "--seed", seed); err != nil {
			t.Fatalf("synth from-profile --seed %s: %v", seed, err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", out, err)
		}
		return data
	}

	seed5RunA := genOnce("seed5a", "5")
	seed5RunB := genOnce("seed5b", "5")
	seed6Run := genOnce("seed6", "6")

	if !bytes.Equal(seed5RunA, seed5RunB) {
		t.Error("same --seed value produced byte-different output cohorts across two runs")
	}
	if bytes.Equal(seed5RunA, seed6Run) {
		t.Error("different --seed values produced byte-identical output — --seed does not appear to reach the generator")
	}
}

// TestSynthFromProfileCLI_FidelityReportFlagWritesReport verifies
// --fidelity-report actually reaches SynthOptions.FidelityReportPath:
// omitting it leaves the result's FidelityReportPath empty and no file
// written; supplying it writes a real FidelityReport document at that
// path whose SourceRows/SyntheticRows figures independently corroborate
// --source and --rows.
func TestSynthFromProfileCLI_FidelityReportFlagWritesReport(t *testing.T) {
	dir := t.TempDir()
	const sourceRows = 60
	source, profile := synthFromProfileFixture(t, dir, sourceRows, 2)

	outNoReport := filepath.Join(dir, "no-report.pulse")
	var buf bytes.Buffer
	if err := runSynthCLI(t, &buf, "from-profile",
		"--profile", profile, "--source", source, "--output", outNoReport,
		"--rows", "10", "--seed", "1", "--json"); err != nil {
		t.Fatalf("synth from-profile (no --fidelity-report): %v", err)
	}
	var envNoReport struct {
		Data struct {
			FidelityReportPath string `json:"fidelity_report_path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &envNoReport); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if envNoReport.Data.FidelityReportPath != "" {
		t.Errorf("fidelity_report_path = %q, want empty when --fidelity-report is not set", envNoReport.Data.FidelityReportPath)
	}

	reportPath := filepath.Join(dir, "report.json")
	outReport := filepath.Join(dir, "report.pulse")
	const newRows = 25
	buf.Reset()
	if err := runSynthCLI(t, &buf, "from-profile",
		"--profile", profile, "--source", source, "--output", outReport,
		"--rows", strconv.Itoa(newRows), "--seed", "1",
		"--fidelity-report", reportPath, "--json"); err != nil {
		t.Fatalf("synth from-profile (--fidelity-report): %v", err)
	}
	var envReport struct {
		Data struct {
			FidelityReportPath string `json:"fidelity_report_path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &envReport); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if envReport.Data.FidelityReportPath != reportPath {
		t.Errorf("fidelity_report_path = %q, want %q (--fidelity-report value)", envReport.Data.FidelityReportPath, reportPath)
	}

	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("fidelity report not written at %q: %v", reportPath, err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("json.Unmarshal fidelity report: %v\nraw: %s", err, raw)
	}
	if report.SourceRows != sourceRows {
		t.Errorf("SourceRows = %d, want %d (--source cohort's row count)", report.SourceRows, sourceRows)
	}
	if report.SyntheticRows != newRows {
		t.Errorf("SyntheticRows = %d, want %d (--rows value)", report.SyntheticRows, newRows)
	}
}

// TestSynthFromProfileCLI_FidelityReportIncludesCaptureTimeWarnings
// exercises the prof.Warnings/FidelityWarnings merge point in
// internal/cli/synth.go's from-profile action: a profile captured with
// --conditional over a source too small to clear MinPairObservations
// (30) carries a thin-pair warning on Profile.Warnings, and that
// warning must reappear verbatim on the written FidelityReport's own
// Warnings slot when --fidelity-report is also set — proving the CLI
// wires the capture-time warning through rather than dropping it.
func TestSynthFromProfileCLI_FidelityReportIncludesCaptureTimeWarnings(t *testing.T) {
	dir := t.TempDir()
	const sourceRows = 10 // below MinPairObservations(30) -> thin numeric pair warning
	source := filepath.Join(dir, "thin-source.pulse")
	synthLibraryCohort(t, source, []synth.FieldSpec{
		{Name: "a", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
		{Name: "b", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{"mean": 5.0, "std": 2.0}},
	}, sourceRows, 1)

	profile := filepath.Join(dir, "thin-profile.json")
	var pbuf bytes.Buffer
	if err := runProfileCLI(t, &pbuf, "create", "--input", source, "--output", profile, "--conditional"); err != nil {
		t.Fatalf("profile create --conditional (thin fixture): %v", err)
	}
	prof := readProfileJSON(t, profile)
	if !containsSubstring(prof.Warnings, "thin numeric pair") {
		t.Fatalf("precondition failed: Profile.Warnings = %v, want a thin numeric pair warning", prof.Warnings)
	}

	output := filepath.Join(dir, "thin-out.pulse")
	reportPath := filepath.Join(dir, "thin-report.json")
	var sbuf bytes.Buffer
	if err := runSynthCLI(t, &sbuf, "from-profile",
		"--profile", profile, "--source", source, "--output", output,
		"--rows", "5", "--seed", "1", "--fidelity-report", reportPath, "--json"); err != nil {
		t.Fatalf("synth from-profile: %v", err)
	}

	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", reportPath, err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, raw)
	}
	if !containsSubstring(report.Warnings, "thin numeric pair") {
		t.Errorf("FidelityReport.Warnings = %v, want the capture-time thin-pair warning carried over from Profile.Warnings", report.Warnings)
	}
}

func TestSynthFromProfileCLI_MissingSourceFlagErrors(t *testing.T) {
	dir := t.TempDir()
	_, profile := synthFromProfileFixture(t, dir, 10, 1)
	output := filepath.Join(dir, "out.pulse")

	var buf bytes.Buffer
	if err := runSynthCLI(t, &buf, "from-profile", "--profile", profile, "--output", output, "--rows", "5"); err == nil {
		t.Fatal("expected error for missing required --source flag")
	}
}
