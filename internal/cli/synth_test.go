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
	var discard bytes.Buffer
	return runSynthCLIStreams(t, buf, &discard, args...)
}

// runSynthCLIStreams is runSynthCLI with the diagnostic stream captured
// too. The warning summary (E6-S3) goes to ErrWriter rather than Writer
// so a redirected stdout stays exactly the bytes it was, which means a
// test that only captures stdout cannot see it at all.
func runSynthCLIStreams(t *testing.T, out, errOut *bytes.Buffer, args ...string) error {
	t.Helper()
	root := SynthCommand()
	root.Writer = out
	root.ErrWriter = errOut
	return root.Run(context.Background(), append([]string{"synth"}, args...))
}

// runProfileCLI drives a fresh ProfileCommand with the supplied args,
// capturing stdout into buf. Mirrors runSynthCLI for the sibling
// `pulse profile` command group.
func runProfileCLI(t *testing.T, buf *bytes.Buffer, args ...string) error {
	t.Helper()
	var discard bytes.Buffer
	return runProfileCLIStreams(t, buf, &discard, args...)
}

// runProfileCLIStreams is runProfileCLI with the diagnostic stream
// captured too. See runSynthCLIStreams.
func runProfileCLIStreams(t *testing.T, out, errOut *bytes.Buffer, args ...string) error {
	t.Helper()
	root := ProfileCommand()
	root.Writer = out
	root.ErrWriter = errOut
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

// ---------- synth from-profile --emit-spec / --rules (E2-S1) ----------

// richFromProfileFixture builds a source cohort with a numeric, a
// categorical, a boolean and a nullable numeric column, plus a
// --conditional profile over it. Deliberately wider than
// synthFromProfileFixture's single f64: the round trip below is only
// as strong as the number of distinct slots SpecFromProfile populates,
// and a one-numeric-field spec exercises neither the categorical
// dictionary arm nor the conditional-pair arm.
func richFromProfileFixture(t *testing.T, dir string, rows int, seed int64) (sourcePath, profilePath string) {
	t.Helper()
	sourcePath = filepath.Join(dir, "rich-source.pulse")
	synthLibraryCohort(t, sourcePath, []synth.FieldSpec{
		{Name: "score", Type: "f64", Nullable: true, NullRate: 0.1, Distribution: synth.DistNormal,
			Params: map[string]any{"mean": 10.0, "std": 3.0}},
		{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
			Params: map[string]any{"values": []any{"us", "eu", "apac"}, "weights": []any{3.0, 2.0, 1.0}}},
		{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
			Params: map[string]any{"p": 0.3}},
		{Name: "spend", Type: "f64", Nullable: true, NullRate: 0.25, Distribution: synth.DistNormal,
			Params: map[string]any{"mean": 50.0, "std": 12.0}},
	}, rows, seed)

	profilePath = filepath.Join(dir, "rich-profile.json")
	var buf bytes.Buffer
	if err := runProfileCLI(t, &buf, "create", "--input", sourcePath, "--output", profilePath, "--conditional"); err != nil {
		t.Fatalf("profile create (rich fixture): %v", err)
	}
	return sourcePath, profilePath
}

// readCohortRows decodes every record of a .pulse file into a
// per-field value map keyed by field name, alongside the null flags. A
// categorical field is resolved through the file's OWN dictionary and
// returned as its label string; every other field is returned as a
// float64.
//
// The label resolution is load-bearing rather than convenient. A
// from-profile output's merged schema inherits the SOURCE cohort's
// dictionary (insertion order of the real rows), while a from-schema
// output builds its dictionary from the emitted spec's `values` list
// (frequency order, as `Categorical.Top` ranks it). The same category
// therefore legitimately carries a different ID in the two files, and
// comparing IDs would report a difference where the cohorts agree.
func readCohortRows(t *testing.T, path string) (vals []map[string]any, nulls []map[string]bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	r := bytes.NewReader(raw)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader(%s): %v", path, err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema(%s): %v", path, err)
	}
	rr := encoding.NewRecordReader(r, schema)
	for {
		v := make(map[string]float64)
		n := make(map[string]bool)
		if err := rr.ReadRecord(v, n); err != nil {
			break
		}
		row := make(map[string]any, len(v))
		for name, f := range v {
			fld := schema.Field(name)
			if fld != nil && fld.Dictionary != nil {
				row[name] = fld.Dictionary.Resolve(uint32(f))
				continue
			}
			row[name] = f
		}
		vals = append(vals, row)
		nulls = append(nulls, n)
	}
	return vals, nulls
}

// TestSynthFromProfileCLI_EmitSpecRoundTripsThroughFromSchema is the
// E2-S1 acceptance criterion driven through BOTH real leaves:
// `from-profile --emit-spec` writes the derived spec, `from-schema`
// consumes that file at the same seed, and the rows the two produce
// must agree value for value.
//
// File-level byte-identity is not available here and its absence is
// structural rather than a weakened assertion: from-profile's output
// additionally carries every source row and an appended `_synthetic`
// column, so its records are re-encoded into a merged schema with
// different offsets and a different bitmap width. The library-side
// test synth.TestEmitSpec_RoundTripsToByteIdenticalCohort makes the
// byte comparison directly, on the generated partition alone; this one
// proves the two CLI leaves are actually wired to those code paths.
func TestSynthFromProfileCLI_EmitSpecRoundTripsThroughFromSchema(t *testing.T) {
	dir := t.TempDir()
	const sourceRows = 120
	const newRows = 60
	const seed = "9"
	source, profile := richFromProfileFixture(t, dir, sourceRows, 3)

	emitted := filepath.Join(dir, "derived-spec.json")
	fromProfileOut := filepath.Join(dir, "from-profile.pulse")
	var buf bytes.Buffer
	if err := runSynthCLI(t, &buf, "from-profile",
		"--profile", profile, "--source", source, "--output", fromProfileOut,
		"--rows", strconv.Itoa(newRows), "--seed", seed, "--emit-spec", emitted); err != nil {
		t.Fatalf("synth from-profile --emit-spec: %v", err)
	}
	if !strings.Contains(buf.String(), emitted) {
		t.Errorf("stdout does not name the emitted spec path; got %q", buf.String())
	}

	specRaw, err := os.ReadFile(emitted)
	if err != nil {
		t.Fatalf("--emit-spec wrote no file at %q: %v", emitted, err)
	}
	if !bytes.Contains(specRaw, []byte("\n  \"fields\": [")) {
		t.Error("emitted spec is not indented")
	}
	spec, err := synth.ParseSpec(specRaw)
	if err != nil {
		t.Fatalf("emitted spec does not parse: %v\n%s", err, specRaw)
	}
	if spec.RowCount != newRows {
		t.Errorf("emitted row_count = %d, want %d (--rows)", spec.RowCount, newRows)
	}
	if len(spec.Fields) != 4 {
		t.Fatalf("emitted spec declares %d fields, want the source's 4", len(spec.Fields))
	}

	fromSchemaOut := filepath.Join(dir, "from-schema.pulse")
	buf.Reset()
	if err := runSynthCLI(t, &buf, "from-schema",
		"--spec", emitted, "--output", fromSchemaOut, "--seed", seed); err != nil {
		t.Fatalf("synth from-schema (emitted spec): %v", err)
	}

	profileVals, profileNulls := readCohortRows(t, fromProfileOut)
	schemaVals, schemaNulls := readCohortRows(t, fromSchemaOut)
	if len(profileVals) != sourceRows+newRows {
		t.Fatalf("from-profile output has %d records, want %d", len(profileVals), sourceRows+newRows)
	}
	if len(schemaVals) != newRows {
		t.Fatalf("from-schema output has %d records, want %d", len(schemaVals), newRows)
	}

	// The generated partition is the tail of the from-profile output,
	// tagged _synthetic = 1; the from-schema output is that partition
	// on its own.
	fields := []string{"score", "region", "aware", "spend"}
	for i := 0; i < newRows; i++ {
		gen := profileVals[sourceRows+i]
		if gen["_synthetic"] != float64(1) {
			t.Fatalf("record %d of the from-profile tail is not tagged _synthetic", sourceRows+i)
		}
		for _, f := range fields {
			if gen[f] != schemaVals[i][f] {
				t.Fatalf("row %d field %q: from-profile %v, from-schema %v — the emitted spec did not "+
					"reproduce the run it was emitted from", i, f, gen[f], schemaVals[i][f])
			}
			if profileNulls[sourceRows+i][f] != schemaNulls[i][f] {
				t.Fatalf("row %d field %q: null flag differs between the two leaves", i, f)
			}
		}
	}
}

// TestSynthFromProfileCLI_EmitSpecDoesNotChangeTheCohort pins
// --emit-spec as a pure diagnostic: the cohort a run produces must be
// byte-identical whether or not the spec was written out.
func TestSynthFromProfileCLI_EmitSpecDoesNotChangeTheCohort(t *testing.T) {
	dir := t.TempDir()
	source, profile := richFromProfileFixture(t, dir, 60, 4)

	run := func(name string, extra ...string) []byte {
		out := filepath.Join(dir, name+".pulse")
		var buf bytes.Buffer
		args := append([]string{"from-profile",
			"--profile", profile, "--source", source, "--output", out,
			"--rows", "30", "--seed", "12"}, extra...)
		if err := runSynthCLI(t, &buf, args...); err != nil {
			t.Fatalf("synth from-profile (%s): %v", name, err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", out, err)
		}
		return data
	}

	plain := run("plain")
	withEmit := run("emitted", "--emit-spec", filepath.Join(dir, "spec.json"))
	if !bytes.Equal(plain, withEmit) {
		t.Error("--emit-spec changed the generated cohort; it must be a pure diagnostic")
	}
}

// TestSynthFromProfileCLI_RulesFileAppliesToGeneratedRows drives the
// --rules half end to end. The two halves are asserted in ONE test
// because each is trivially satisfiable by abandoning the other: a
// flag that is parsed and ignored leaves the cohort unchanged, and a
// cohort that changed proves nothing unless the change is the rule's.
func TestSynthFromProfileCLI_RulesFileAppliesToGeneratedRows(t *testing.T) {
	dir := t.TempDir()
	const sourceRows = 80
	const newRows = 200
	source, profile := richFromProfileFixture(t, dir, sourceRows, 5)

	rulesPath := filepath.Join(dir, "rules.json")
	// A gate on the boolean screener plus a block null: exactly the
	// shape the motivating survey needs.
	rules := `[{"when": "aware == 0", "set_null": ["spend"], "null_together": ["spend", "score"]}]`
	if err := os.WriteFile(rulesPath, []byte(rules), 0o644); err != nil {
		t.Fatalf("WriteFile(rules): %v", err)
	}

	emitted := filepath.Join(dir, "with-rules-spec.json")
	out := filepath.Join(dir, "ruled.pulse")
	var buf bytes.Buffer
	if err := runSynthCLI(t, &buf, "from-profile",
		"--profile", profile, "--source", source, "--output", out,
		"--rows", strconv.Itoa(newRows), "--seed", "13",
		"--rules", rulesPath, "--emit-spec", emitted); err != nil {
		t.Fatalf("synth from-profile --rules: %v", err)
	}

	// --rules and --emit-spec compose: the emitted document carries
	// the merged rules, so an analyst can inspect what theirs became.
	specRaw, err := os.ReadFile(emitted)
	if err != nil {
		t.Fatalf("ReadFile(emitted spec): %v", err)
	}
	spec, err := synth.ParseSpec(specRaw)
	if err != nil {
		t.Fatalf("ParseSpec(emitted spec): %v", err)
	}
	if len(spec.Rules) != 1 || spec.Rules[0].When != "aware == 0" {
		t.Fatalf("emitted spec Rules = %+v, want the merged rules file", spec.Rules)
	}

	vals, nulls := readCohortRows(t, out)
	gated, gatedNull, ungatedNonNull := 0, 0, 0
	for i := sourceRows; i < len(vals); i++ {
		if vals[i]["aware"] != float64(0) {
			if !nulls[i]["spend"] && !nulls[i]["score"] {
				ungatedNonNull++
			}
			continue
		}
		gated++
		if nulls[i]["spend"] && nulls[i]["score"] {
			gatedNull++
		}
	}
	if gated == 0 {
		t.Fatal("no generated row satisfied the gate; the fixture cannot show the rule firing")
	}
	if gatedNull != gated {
		t.Errorf("%d of %d gated rows carry the nulled block; want all of them — the --rules file did not fire",
			gatedNull, gated)
	}
	if ungatedNonNull == 0 {
		t.Error("every ungated row is nulled too; the rule is not gated by its `when`")
	}
}

// TestSynthFromProfileCLI_RulesRefusalNamesTheFile covers the two
// refusal classes the story pins, on the --json arm so the envelope's
// own error shape is what gets asserted: the code must be E1-S2's
// rather than a leaf placeholder, and details must name the file.
func TestSynthFromProfileCLI_RulesRefusalNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	source, profile := richFromProfileFixture(t, dir, 40, 6)

	cases := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"unknown_field", `[{"set_null": ["nosuchfield"]}]`, "PULSE_SYNTH_RULE_FIELD_UNKNOWN"},
		{"malformed", `[{"set_null":`, "SERVICE_VALIDATION"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rulesPath := filepath.Join(dir, tc.name+".json")
			if err := os.WriteFile(rulesPath, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			out := filepath.Join(dir, tc.name+".pulse")
			var buf bytes.Buffer
			if err := runSynthCLI(t, &buf, "from-profile",
				"--profile", profile, "--source", source, "--output", out,
				"--rows", "5", "--seed", "1", "--rules", rulesPath, "--json"); err != nil {
				t.Fatalf("--json refusals are written to the envelope, not returned: %v", err)
			}
			var env struct {
				FormatVersion string `json:"format_version"`
				Errors        []struct {
					Code    string         `json:"code"`
					Message string         `json:"message"`
					Details map[string]any `json:"details"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
				t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
			}
			if env.FormatVersion != "1.1" {
				t.Errorf("format_version = %q, want 1.1 — the envelope shape is unchanged", env.FormatVersion)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("errors = %+v, want exactly 1", env.Errors)
			}
			if env.Errors[0].Code != tc.wantCode {
				t.Errorf("errors[0].code = %q, want %q — a placeholder makes `pulse errors lookup` useless",
					env.Errors[0].Code, tc.wantCode)
			}
			if got := env.Errors[0].Details["path"]; got != rulesPath {
				t.Errorf("errors[0].details.path = %v, want %q", got, rulesPath)
			}
			if _, err := os.Stat(out); err == nil {
				t.Error("a refused rules file still produced an output cohort")
			}
		})
	}
}

// TestSynthFromProfileCLI_EmitSpecWithJSONKeepsTheEnvelope pins that
// both new flags coexist with --json and move nothing in the envelope.
func TestSynthFromProfileCLI_EmitSpecWithJSONKeepsTheEnvelope(t *testing.T) {
	dir := t.TempDir()
	source, profile := richFromProfileFixture(t, dir, 40, 7)
	rulesPath := filepath.Join(dir, "ok-rules.json")
	if err := os.WriteFile(rulesPath, []byte(`[{"when": "aware == 0", "set_null": ["spend"]}]`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	out := filepath.Join(dir, "out.pulse")
	var buf bytes.Buffer
	if err := runSynthCLI(t, &buf, "from-profile",
		"--profile", profile, "--source", source, "--output", out,
		"--rows", "10", "--seed", "1",
		"--rules", rulesPath, "--emit-spec", filepath.Join(dir, "spec.json"), "--json"); err != nil {
		t.Fatalf("synth from-profile: %v", err)
	}
	var env struct {
		FormatVersion string `json:"format_version"`
		Data          struct {
			RowsGenerated int    `json:"rows_generated"`
			OutputPath    string `json:"output_path"`
		} `json:"data"`
		Errors   []any `json:"errors"`
		Warnings []any `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}
	if env.Data.RowsGenerated != 10 || env.Data.OutputPath != out {
		t.Errorf("data = %+v, want the unchanged Result shape", env.Data)
	}
	if len(env.Errors) != 0 {
		t.Errorf("errors = %+v, want empty", env.Errors)
	}
}
