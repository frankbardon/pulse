package cli

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// ---------- writeWarningSummary: grouping, ordering, cap ----------

// manyWarnings returns n copies of a thin categorical pair warning,
// each naming a distinct pair so no two lines are byte-identical (which
// would make dedupeWarnings, not the cap, do the bounding).
func manyWarnings(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, "thin categorical pair a"+strconv.Itoa(i)+
			" x b: only 3 supporting observation(s) (below 30) — reconstructed correlation may be unstable")
	}
	return out
}

// TestWriteWarningSummary_GroupsAndCapsRatherThanDumping is the
// acceptance bar for the story: 2,900 warnings of one kind must produce
// a counted group with a bounded example list, not 2,900 lines. A test
// that only asserted "something printed" would pass on the dump.
func TestWriteWarningSummary_GroupsAndCapsRatherThanDumping(t *testing.T) {
	const total = 2900
	var buf bytes.Buffer
	writeWarningSummary(&buf, manyWarnings(total), "prof.json (.warnings)")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// 1 headline + 1 group header + maxWarningExamples examples
	// + 1 "+N more" + 1 full-list pointer.
	if want := 1 + 1 + maxWarningExamples + 1 + 1; len(lines) != want {
		t.Fatalf("printed %d line(s), want %d — the summary is not bounded:\n%s",
			len(lines), want, buf.String())
	}
	if !strings.Contains(lines[0], strconv.Itoa(total)) {
		t.Errorf("headline %q does not carry the true total %d", lines[0], total)
	}
	if !strings.Contains(lines[1], "thin categorical pair") || !strings.Contains(lines[1], "("+strconv.Itoa(total)+")") {
		t.Errorf("group header = %q, want the kind label with its full count", lines[1])
	}
	rollup := lines[len(lines)-2]
	if want := "+" + strconv.Itoa(total-maxWarningExamples) + " more of this kind"; !strings.Contains(rollup, want) {
		t.Errorf("roll-up line = %q, want it to contain %q", rollup, want)
	}
	if !strings.Contains(lines[len(lines)-1], "prof.json (.warnings)") {
		t.Errorf("last line = %q, want the full-list pointer", lines[len(lines)-1])
	}
}

// TestWriteWarningSummary_AttentionGroupIsOnTheFirstScreen pins the
// property that makes the summary worth having: the one line that names
// a model nothing applied must be visible above thousands of
// expected-outcome lines, not sorted underneath them by count.
func TestWriteWarningSummary_AttentionGroupIsOnTheFirstScreen(t *testing.T) {
	ws := append(manyWarnings(2900),
		`model for numeric field "regard" not applied: predictor "brand" carries unsupported kind "numeric"`)
	var buf bytes.Buffer
	writeWarningSummary(&buf, ws, "")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) > 24 {
		t.Fatalf("summary is %d lines; it has to fit a screen:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[1], "model not applied") || !strings.HasPrefix(strings.TrimSpace(lines[1]), "!") {
		t.Errorf("lines[1] = %q, want the attention-marked 'model not applied' group first", lines[1])
	}
	if got := lines[2]; !strings.Contains(got, "unsupported kind") {
		t.Errorf("lines[2] = %q, want the drop's own text right under its group header", got)
	}
	if !strings.Contains(lines[0], "1 needing attention") {
		t.Errorf("headline = %q, want exactly 1 needing attention", lines[0])
	}
	if !strings.Contains(lines[0], "2900 expected") {
		t.Errorf("headline = %q, want the 2900 thin pairs counted as expected, not as faults", lines[0])
	}
}

// TestWriteWarningSummary_EmptyPrintsNothing lets every leaf call it
// unconditionally.
func TestWriteWarningSummary_EmptyPrintsNothing(t *testing.T) {
	var buf bytes.Buffer
	writeWarningSummary(&buf, nil, "prof.json")
	writeWarningSummary(&buf, []string{}, "prof.json")
	if buf.Len() != 0 {
		t.Errorf("wrote %q for an empty warning slice, want nothing", buf.String())
	}
}

// TestDedupeWarnings_CollapsesTheDoubleReportedConflicts pins the reason
// dedupe exists: `synth from-profile` merges SpecFromProfile's warnings
// with generate()'s, and both derive their conflict lines from
// resolveConflicts over the same *Spec, so each conflict arrives twice
// verbatim. Counting it twice would report 1,590 conflicts where there
// are 795.
func TestDedupeWarnings_CollapsesTheDoubleReportedConflicts(t *testing.T) {
	conflict := `conditional relationship conflict: field "dma" is already claimed by categorical pair (wave -> dma); dropping categorical pair (study -> dma)`
	other := `conditional relationship conflict: field "region" is already claimed by categorical pair (wave -> region); dropping categorical pair (study -> region)`
	got := dedupeWarnings([]string{conflict, other, conflict, other})
	if len(got) != 2 {
		t.Fatalf("dedupeWarnings kept %d, want 2", len(got))
	}
	if got[0] != conflict || got[1] != other {
		t.Errorf("first-occurrence order lost: %v", got)
	}
}

// ---------- writeModelRecoverySummary ----------

// TestWriteModelRecoverySummary_ReportsCheckedAndFlagged is the E5-S1
// hand-off: the recovery sections flag 30 of 55 models on the motivating
// cohort inside a 1.65 MB document, and nothing told the operator they
// existed.
func TestWriteModelRecoverySummary_ReportsCheckedAndFlagged(t *testing.T) {
	fsys := afero.NewMemMapFs()
	rep := synth.FidelityReport{
		SourceRows:    100,
		SyntheticRows: 50,
		Models: []*synth.ModelFidelity{
			{Field: "a", Flagged: true},
			{Field: "b"},
			{Field: "c", Flagged: true},
		},
		ModelResidualCorrelations: &synth.ModelResidualCorrelationFidelity{
			Fields: []string{"a", "b", "c"}, Compared: 3, Flagged: 1,
		},
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := afero.WriteFile(fsys, "/report.json", raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var buf bytes.Buffer
	writeModelRecoverySummary(&buf, fsys, "/report.json")
	out := buf.String()
	if !strings.Contains(out, "Model recovery: 3 model(s) checked, 2 flagged") {
		t.Errorf("output = %q, want the model-recovery headline", out)
	}
	if !strings.Contains(out, "Residual recovery: 3 pair(s) compared, 1 flagged") {
		t.Errorf("output = %q, want the residual-recovery headline", out)
	}
	if !strings.Contains(out, "/report.json") {
		t.Errorf("output = %q, want the report path so a reader can open it", out)
	}
}

// TestWriteModelRecoverySummary_SilentWithoutAModelsSection covers the
// three no-op paths: no report at all, an unreadable one, and a report
// carrying no recovery sections (every spec predating --fit-models).
// A courtesy line must never speak up about a section that is not there,
// and must never fail a generation that already succeeded.
func TestWriteModelRecoverySummary_SilentWithoutAModelsSection(t *testing.T) {
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "/plain.json", []byte(`{"source_rows":1,"synthetic_rows":1}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := afero.WriteFile(fsys, "/broken.json", []byte(`{not json`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, path := range []string{"", "/missing.json", "/plain.json", "/broken.json"} {
		var buf bytes.Buffer
		writeModelRecoverySummary(&buf, fsys, path)
		if buf.Len() != 0 {
			t.Errorf("path %q printed %q, want nothing", path, buf.String())
		}
	}
}

// TestFidelityWarningsLocation_NamesTheFlagWhenThereIsNoReport — the
// fidelity report is the only document carrying all three warning
// channels, so without --fidelity-report there is no file to point at.
func TestFidelityWarningsLocation_NamesTheFlagWhenThereIsNoReport(t *testing.T) {
	if got := fidelityWarningsLocation(""); !strings.Contains(got, "--fidelity-report") {
		t.Errorf("location without a report = %q, want the flag that would produce one", got)
	}
	if got := fidelityWarningsLocation("/r.json"); !strings.Contains(got, "/r.json") {
		t.Errorf("location with a report = %q, want the report path", got)
	}
}

// ---------- end to end through the leaves ----------

// writeConditioningCohort emits a three-field cohort — a categorical
// "g", an f64 "v" whose mean depends on g, and an f64 "w" that does not
// — so one capture exercises both model outcomes at once: `v` lands a
// model with predictors, `w` lands the complete zero-predictor model.
// A cohort drawn independently (synthLibraryCohort) can only ever
// produce the second, which is the case this fixture exists NOT to
// limit itself to.
func writeConditioningCohort(t *testing.T, path string, rowCount int) {
	t.Helper()
	gDict := encoding.NewDictionary()
	for _, v := range []string{"lo", "hi"} {
		if _, err := gDict.Add(v); err != nil {
			t.Fatalf("gDict.Add(%q): %v", v, err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "g", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: gDict},
			{Name: "v", Type: encoding.FieldTypeF64, ByteOffset: 1},
			{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 9},
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
		gID := uint64(r % 2)
		// A clean group separation plus a small deterministic wobble, so
		// the between-group share of variance is far above the 1%
		// admission floor while v is not literally constant per group.
		v := 10.0*float64(gID) + math.Sin(float64(r))
		w := math.Cos(float64(r) * 2.3)
		if err := encoding.WriteFieldValue(&buf, schema.Fields[0].Type, gID); err != nil {
			t.Fatalf("WriteFieldValue(g): %v", err)
		}
		if err := encoding.WriteFieldValue(&buf, schema.Fields[1].Type, math.Float64bits(v)); err != nil {
			t.Fatalf("WriteFieldValue(v): %v", err)
		}
		if err := encoding.WriteFieldValue(&buf, schema.Fields[2].Type, math.Float64bits(w)); err != nil {
			t.Fatalf("WriteFieldValue(w): %v", err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile cohort: %v", err)
	}
}

// TestProfileCreateCLI_WarningsReachStderrAndNotStdout is the core
// regression: before E6-S3 `profile create` printed the row count and
// nothing else, so a capture where something was thin or dropped looked
// identical to one where nothing was — which is how a defect that
// disabled 80% of --fit-models survived an entire effort.
//
// It also pins the redirect contract: stdout keeps exactly the bytes it
// had, so `pulse profile create … > out` is unaffected.
func TestProfileCreateCLI_WarningsReachStderrAndNotStdout(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "thin.pulse")
	// 10 rows is below MinPairObservations (30), so --conditional emits
	// a thin numeric pair warning.
	synthLibraryCohort(t, source, []synth.FieldSpec{
		{Name: "a", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
		{Name: "b", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{"mean": 5.0, "std": 2.0}},
	}, 10, 1)

	out := filepath.Join(dir, "prof.json")
	var stdout, stderr bytes.Buffer
	if err := runProfileCLIStreams(t, &stdout, &stderr, "create",
		"--input", source, "--output", out, "--conditional"); err != nil {
		t.Fatalf("profile create: %v", err)
	}

	prof := readProfileJSON(t, out)
	if len(prof.Warnings) == 0 {
		t.Fatal("precondition failed: the fixture raised no capture warnings")
	}
	if got, want := stdout.String(), "Profiled 10 rows from "+source+" -> "+out+"\n"; got != want {
		t.Errorf("stdout = %q, want exactly %q — a diagnostic on stdout corrupts every redirect", got, want)
	}
	diag := stderr.String()
	if !strings.HasPrefix(diag, "Warnings: ") {
		t.Fatalf("stderr = %q, want the warning summary headline", diag)
	}
	if !strings.Contains(diag, "thin numeric pair") {
		t.Errorf("stderr = %q, want the thin-pair kind named", diag)
	}
	if !strings.Contains(diag, out+" (.warnings)") {
		t.Errorf("stderr = %q, want the profile document named as the full list", diag)
	}
}

// TestProfileCreateCLI_JSONOutputCarriesNoWarningSummary pins the
// --json contract: the envelope already carries the warnings inside
// data, so the human summary must not run — stdout stays exactly one
// envelope and stderr stays empty.
func TestProfileCreateCLI_JSONOutputCarriesNoWarningSummary(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "thin.pulse")
	synthLibraryCohort(t, source, []synth.FieldSpec{
		{Name: "a", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
		{Name: "b", Type: "f64", Distribution: synth.DistNormal, Params: map[string]any{"mean": 5.0, "std": 2.0}},
	}, 10, 1)

	out := filepath.Join(dir, "prof.json")
	var stdout, stderr bytes.Buffer
	if err := runProfileCLIStreams(t, &stdout, &stderr, "create",
		"--input", source, "--output", out, "--conditional", "--json"); err != nil {
		t.Fatalf("profile create --json: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing on the --json path", stderr.String())
	}
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var env struct {
		FormatVersion string `json:"format_version"`
		Data          struct {
			Warnings []string `json:"warnings"`
		} `json:"data"`
	}
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not a single JSON envelope: %v\nraw: %s", err, stdout.String())
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}
	if len(env.Data.Warnings) == 0 {
		t.Error("data.warnings is empty; the envelope is where --json consumers read them")
	}
	if dec.More() {
		t.Error("stdout carries more than the envelope on the --json path")
	}
}

// TestSynthFromProfileCLI_WarningSummaryAndRecoveryLineOnStderr covers
// the generation leaf end to end: the merged three-channel warning
// summary and the model-recovery headline both land on stderr, and
// stdout keeps exactly its two result lines.
func TestSynthFromProfileCLI_WarningSummaryAndRecoveryLineOnStderr(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "cond.pulse")
	writeConditioningCohort(t, source, 400)

	profile := filepath.Join(dir, "prof.json")
	var pout, perr bytes.Buffer
	if err := runProfileCLIStreams(t, &pout, &perr, "create",
		"--input", source, "--output", profile, "--fit-models"); err != nil {
		t.Fatalf("profile create --fit-models: %v", err)
	}
	prof := readProfileJSON(t, profile)
	if len(prof.Models) == 0 || len(prof.Models[0].Predictors) == 0 {
		t.Fatalf("precondition failed: fixture captured no model with predictors (%+v)", prof.Models)
	}

	output := filepath.Join(dir, "out.pulse")
	report := filepath.Join(dir, "report.json")
	var stdout, stderr bytes.Buffer
	if err := runSynthCLIStreams(t, &stdout, &stderr, "from-profile",
		"--profile", profile, "--source", source, "--output", output,
		"--rows", "200", "--seed", "3", "--fidelity-report", report); err != nil {
		t.Fatalf("synth from-profile: %v", err)
	}

	wantStdout := "Generated 200 rows -> " + output + " (rejected 0)\nFidelity report -> " + report + "\n"
	if got := stdout.String(); got != wantStdout {
		t.Errorf("stdout = %q, want exactly %q", got, wantStdout)
	}
	diag := stderr.String()
	if !strings.Contains(diag, "Model recovery: 1 model(s) checked") {
		t.Errorf("stderr = %q, want the model-recovery headline naming the checked count", diag)
	}
	if !strings.Contains(diag, report) {
		t.Errorf("stderr = %q, want the report path so a reader can open it", diag)
	}
}

// TestSynthFromProfileCLI_WarningSummarySpansAllThreeChannels is the
// routing assertion. The three channels are genuinely distinct — the
// profile document's own warnings, SpecFromProfile's translation
// warnings, and generate()'s compilation warnings — and only the middle
// one carries the model-drop lines that hid the v0.32.x defect. A
// summary reading one channel would still be blind to it.
func TestSynthFromProfileCLI_WarningSummarySpansAllThreeChannels(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "thin.pulse")
	// 10 rows is below MinPairObservations (30), so --conditional emits
	// a thin numeric pair warning for v x w; w is unexplained by g, so
	// --fit-models emits the zero-predictor outcome for it.
	writeConditioningCohort(t, source, 10)

	profile := filepath.Join(dir, "prof.json")
	var pout, perr bytes.Buffer
	if err := runProfileCLIStreams(t, &pout, &perr, "create",
		"--input", source, "--output", profile, "--conditional", "--fit-models"); err != nil {
		t.Fatalf("profile create: %v", err)
	}
	prof := readProfileJSON(t, profile)
	if !containsSubstring(prof.Warnings, "thin numeric pair") {
		t.Fatalf("precondition failed: no capture-time thin-pair warning in %v", prof.Warnings)
	}
	spec, conflictWarnings := synth.SpecFromProfile(prof, 5)
	_ = spec
	if !containsSubstring(conflictWarnings, "carries no predictors") {
		t.Fatalf("precondition failed: no SpecFromProfile warning in %v", conflictWarnings)
	}

	output := filepath.Join(dir, "out.pulse")
	var stdout, stderr bytes.Buffer
	if err := runSynthCLIStreams(t, &stdout, &stderr, "from-profile",
		"--profile", profile, "--source", source, "--output", output,
		"--rows", "5", "--seed", "1"); err != nil {
		t.Fatalf("synth from-profile: %v", err)
	}
	diag := stderr.String()
	if !strings.Contains(diag, "thin numeric pair") {
		t.Errorf("stderr = %q, want the capture-time channel represented", diag)
	}
	if !strings.Contains(diag, "model carries no predictors") {
		t.Errorf("stderr = %q, want the SpecFromProfile channel represented", diag)
	}
	// No --fidelity-report, so no document holds the merged list.
	if !strings.Contains(diag, "--fidelity-report") {
		t.Errorf("stderr = %q, want the pointer to name the flag that would capture every line", diag)
	}
	if strings.Contains(stdout.String(), "Warnings:") {
		t.Errorf("stdout = %q, want no diagnostics on the redirected stream", stdout.String())
	}
}

// TestSynthFromProfileCLI_GeneratedCohortUnchangedByTheSummary — E6-S3
// is presentation only. Two runs at one seed, one of which is a --json
// run that prints no summary at all, must produce byte-identical
// cohorts.
func TestSynthFromProfileCLI_GeneratedCohortUnchangedByTheSummary(t *testing.T) {
	dir := t.TempDir()
	source, profile := synthFromProfileFixture(t, dir, 40, 2)

	paths := make([]string, 2)
	for i, extra := range [][]string{nil, {"--json"}} {
		out := filepath.Join(dir, "out"+strconv.Itoa(i)+".pulse")
		args := append([]string{"from-profile",
			"--profile", profile, "--source", source, "--output", out,
			"--rows", "25", "--seed", "9"}, extra...)
		var stdout, stderr bytes.Buffer
		if err := runSynthCLIStreams(t, &stdout, &stderr, args...); err != nil {
			t.Fatalf("synth from-profile %v: %v", extra, err)
		}
		paths[i] = out
	}
	a, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	b, err := os.ReadFile(paths[1])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Error("generated cohorts differ between the text and --json runs; the summary must be presentation only")
	}
}
