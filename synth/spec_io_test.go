package synth_test

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// loadTestProfile reads one of the checked-in captured profile
// documents. They are real captures rather than hand-built Profile
// values on purpose: SpecFromProfile's output is only as
// round-trippable as the shapes a real capture puts in it, and the
// narrow hand-built fixture is exactly what let the all-null set_*
// column below go unnoticed.
func loadTestProfile(t *testing.T, name string) *synth.Profile {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("ReadFile(testdata/%s): %v", name, err)
	}
	var prof synth.Profile
	if err := json.Unmarshal(raw, &prof); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", name, err)
	}
	return &prof
}

// TestEmitSpec_RoundTripsToByteIdenticalCohort is the acceptance
// criterion that carries E2-S1: --emit-spec must write the REAL spec,
// not a rendering of it.
//
// A test that asserts the emitted JSON parses would pass against an
// emit that dropped `models`, coarsened a float, or re-derived the spec
// from the profile a second time — every one of which is a silent
// structure loss of exactly the class this package has shipped three
// times. The only assertion that cannot be satisfied that way is
// generating from BOTH specs at the same seed and comparing the
// cohorts byte for byte, because a single dropped predictor or a single
// rounded parameter moves a value and therefore moves a byte.
//
// The comparison stands in for `from-profile --emit-spec` feeding
// `from-schema`: from-schema IS ParseSpec followed by generation, and
// AugmentFromProfile generates its synthetic partition through the same
// generate() call at the same seed (synth/augment.go). The CLI
// counterpart that drives both real leaves end to end is
// TestSynthFromProfileCLI_EmitSpecRoundTripsThroughFromSchema; file-level
// byte-identity is not available THERE because from-profile's output
// additionally carries the source rows and the appended `_synthetic`
// column, so its records are re-encoded into a merged schema.
func TestEmitSpec_RoundTripsToByteIdenticalCohort(t *testing.T) {
	cases := []struct {
		name    string
		profile func(*testing.T) *synth.Profile
		rows    int
		seed    int64
		// require guards the fixture itself. A round trip over a spec
		// that happens not to populate a slot silently stops testing
		// that slot, which is how the first draft of this test passed
		// while a rounded model coefficient was being emitted: the
		// captured profile it used carries no `models` section at all.
		require func(*testing.T, *synth.Spec)
	}{
		{
			// A real --conditional capture: categorical dictionaries,
			// numeric normals and every conditional-pair arm.
			name:    "conditional_capture",
			profile: func(t *testing.T) *synth.Profile { return loadTestProfile(t, "profile_pre_models.json") },
			rows:    300,
			seed:    7,
			require: func(t *testing.T, spec *synth.Spec) {
				if len(spec.CategoricalPairs) == 0 && len(spec.CategoricalNumericPairs) == 0 {
					t.Fatal("fixture no longer populates any conditional-pair slot")
				}
			},
		},
		{
			// Every non-numeric field 100% null, so SpecFromProfile
			// falls to the DistConstant/sentinelFor arm for a
			// categorical_u8, a set_u8, a date, a packed_bool and a
			// decimal128. The set_u8 sentinel is a map[string]bool,
			// whose JSON form is an object — the one shape in the
			// whole Spec that does not decode back to the Go type it
			// was marshalled from.
			name:    "all_null_columns",
			profile: func(t *testing.T) *synth.Profile { return loadTestProfile(t, "profile_all_null_columns.json") },
			rows:    120,
			seed:    3,
			require: func(t *testing.T, spec *synth.Spec) {
				constants := 0
				for _, f := range spec.Fields {
					if f.Distribution == synth.DistConstant {
						constants++
					}
				}
				if constants < 5 {
					t.Fatalf("fixture reaches the unsummarisable fallback for %d fields, want >= 5", constants)
				}
			},
		},
		{
			// A live --fit-models --residual-correlations capture.
			// This is the case the flag's stated diagnostic purpose
			// rests on ("the only way to see which captured models
			// survived translation"), and neither checked-in profile
			// document carries a models section.
			name:    "fitted_models_capture",
			profile: capturedModelsProfile,
			rows:    250,
			seed:    5,
			require: func(t *testing.T, spec *synth.Spec) {
				if len(spec.Models) == 0 {
					t.Fatal("fixture produced no models; the models slot is untested")
				}
				predictors := 0
				for _, m := range spec.Models {
					predictors += len(m.Predictors)
				}
				if predictors == 0 {
					t.Fatal("every captured model is zero-predictor; the coefficients are untested")
				}
				if len(spec.ResidualCorrelations) == 0 {
					t.Fatal("fixture produced no residual correlations")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prof := tc.profile(t)
			spec, _ := synth.SpecFromProfile(prof, tc.rows)
			tc.require(t, spec)

			direct, directRes, err := synth.SynthBytes(spec, synth.Options{Seed: tc.seed})
			if err != nil {
				t.Fatalf("SynthBytes(derived spec): %v", err)
			}
			// Guard the guard: a degenerate cohort (no rows, no
			// fields) would make byte-equality trivially true.
			if directRes.RowsGenerated != tc.rows {
				t.Fatalf("rows_generated = %d, want %d", directRes.RowsGenerated, tc.rows)
			}
			if len(spec.Fields) < 2 {
				t.Fatalf("derived spec has %d field(s); the round trip needs a non-trivial spec", len(spec.Fields))
			}

			memfs := fs.NewMemMap().Fs()
			const emitted = "/emitted-spec.json"
			if err := synth.WriteSpec(memfs, spec, emitted); err != nil {
				t.Fatalf("WriteSpec: %v", err)
			}
			raw, err := afero.ReadFile(memfs, emitted)
			if err != nil {
				t.Fatalf("ReadFile(emitted): %v", err)
			}

			// Exactly what `synth from-schema --spec` does with the file.
			reparsed, err := synth.ParseSpec(raw)
			if err != nil {
				t.Fatalf("ParseSpec(emitted spec): %v\nemitted:\n%s", err, raw)
			}
			round, _, err := synth.SynthBytes(reparsed, synth.Options{Seed: tc.seed})
			if err != nil {
				t.Fatalf("SynthBytes(reparsed spec): %v", err)
			}

			if !bytes.Equal(direct, round) {
				t.Errorf("cohort generated from the EMITTED spec differs from the cohort generated from the "+
					"derived spec at the same seed (%d bytes vs %d) — --emit-spec did not write the real spec",
					len(direct), len(round))
			}
		})
	}
}

// capturedModelsProfile builds a cohort with genuine categorical ->
// numeric structure, captures it with --fit-models
// --residual-correlations, and hands the document back the way
// `synth from-profile` receives one: through a JSON round trip.
//
// It is generated rather than checked in because the two checked-in
// fixtures predate the models section, and a static document would
// freeze whatever the fitter happened to produce on the day it was
// written. The structure is imposed with CategoricalNumericPairs so
// the between-group variance is real — independent marginals would sit
// under synth's variance-explained floor and select no predictors at
// all, leaving the slot as untested as it was before.
func capturedModelsProfile(t *testing.T) *synth.Profile {
	t.Helper()
	byRegion := func(mean, std float64) []synth.CategoricalNumericCategorySpec {
		return []synth.CategoricalNumericCategorySpec{
			{Category: "east", Mean: mean, Std: std},
			{Category: "west", Mean: mean + 40, Std: std},
			{Category: "north", Mean: mean - 25, Std: std},
		}
	}
	src := &synth.Spec{
		RowCount: 4000,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west", "north"}, "weights": []any{3.0, 2.0, 2.0}}},
			{Name: "tier", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"gold", "silver"}, "weights": []any{1.0, 1.0}}},
			{Name: "spend", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 100.0, "std": 8.0}},
			{Name: "visits", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 30.0, "std": 4.0}},
		},
		CategoricalNumericPairs: []synth.CategoricalNumericPairSpec{
			{A: "region", B: "spend", Categories: byRegion(100, 8)},
			{A: "region", B: "visits", Categories: byRegion(30, 4)},
		},
	}
	data, _, err := synth.SynthBytes(src, synth.Options{Seed: 99})
	if err != nil {
		t.Fatalf("SynthBytes(models fixture cohort): %v", err)
	}
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{
		TopK:                    8,
		IncludeStats:            true,
		IncludeConditional:      true,
		FitModels:               true,
		FitResidualCorrelations: true,
		Seed:                    1,
	})
	if err != nil {
		t.Fatalf("ProfileBytes(--fit-models): %v", err)
	}
	// A real from-profile run decodes the document from disk, which is
	// where every float has already been through json.Marshal.
	raw, err := json.Marshal(prof)
	if err != nil {
		t.Fatalf("json.Marshal(profile): %v", err)
	}
	var decoded synth.Profile
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(profile): %v", err)
	}
	return &decoded
}

// TestWriteSpec_IsIndentedAndCarriesEverySlot pins the two properties
// the emitted document has beyond parsing: it is indented (the real
// workflow is emit -> read -> write rules -> apply, so readability is
// functional) and it carries the slots an analyst emits it to see.
func TestWriteSpec_IsIndentedAndCarriesEverySlot(t *testing.T) {
	prof := loadTestProfile(t, "profile_pre_models.json")
	spec, _ := synth.SpecFromProfile(prof, 50)

	memfs := fs.NewMemMap().Fs()
	if err := synth.WriteSpec(memfs, spec, "/s.json"); err != nil {
		t.Fatalf("WriteSpec: %v", err)
	}
	raw, err := afero.ReadFile(memfs, "/s.json")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(raw, []byte("\n  \"fields\": [")) {
		t.Errorf("emitted spec is not indented; first 120 bytes: %q", raw[:min(120, len(raw))])
	}
	// The diagnostic payload: which distribution each field
	// reconstructed to, and which conditional pairs survived.
	for _, want := range []string{`"row_count"`, `"distribution"`, `"categorical_pairs"`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("emitted spec is missing %s", want)
		}
	}
}

// rulesFileJSON is a standalone rules document: the bare Spec.Rules
// array, which E1-S2 fixed deliberately so the loader needs no
// translation layer.
const rulesFileJSON = `[
  {"when": "score < 0", "set_null": ["note"]},
  {"set_expr": {"flag": "score >= 0"}}
]`

// rulesTargetSpec is a small hand-authored spec the rules above are
// well-formed against.
func rulesTargetSpec() *synth.Spec {
	return &synth.Spec{
		RowCount: 40,
		Fields: []synth.FieldSpec{
			{Name: "score", Type: "f64", Distribution: synth.DistNormal,
				Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "note", Type: "categorical_u8", Nullable: true, Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"a", "b"}, "weights": []any{1.0, 1.0}}},
			{Name: "flag", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.5}},
		},
	}
}

// TestApplyRulesFile_LoadsMergesAndFires asserts BOTH halves in one
// test, because each is trivially satisfiable by abandoning the other:
// a loader that parses the file but never attaches it leaves the rules
// inert, and a hard-coded rule set fires without the file having been
// read at all.
func TestApplyRulesFile_LoadsMergesAndFires(t *testing.T) {
	memfs := fs.NewMemMap().Fs()
	const path = "/rules.json"
	if err := afero.WriteFile(memfs, path, []byte(rulesFileJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	spec := rulesTargetSpec()
	baseline, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes(baseline): %v", err)
	}

	if err := synth.ApplyRulesFile(memfs, spec, path); err != nil {
		t.Fatalf("ApplyRulesFile: %v", err)
	}
	// Half one: the file reached the slot, verbatim.
	if len(spec.Rules) != 2 {
		t.Fatalf("Spec.Rules = %d entries, want 2 from the file", len(spec.Rules))
	}
	if spec.Rules[0].When != "score < 0" || len(spec.Rules[0].SetNull) != 1 || spec.Rules[0].SetNull[0] != "note" {
		t.Errorf("Rules[0] = %+v, want the file's first rule verbatim", spec.Rules[0])
	}
	if got := spec.Rules[1].SetExpr["flag"]; got != "score >= 0" {
		t.Errorf("Rules[1].set_expr[flag] = %q, want %q", got, "score >= 0")
	}

	// Half two: the merged rules actually reach generation.
	withRules, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes(with rules): %v", err)
	}
	if bytes.Equal(baseline, withRules) {
		t.Error("cohort is byte-identical with and without the loaded rules — the merged rules never fired")
	}
}

// TestApplyRulesFile_ReplacesRatherThanAppends pins the settled merge
// semantics. SpecFromProfile emits no rules, so there is nothing to
// append to and an append mode would only create an ordering question
// nobody asked.
func TestApplyRulesFile_ReplacesRatherThanAppends(t *testing.T) {
	memfs := fs.NewMemMap().Fs()
	if err := afero.WriteFile(memfs, "/rules.json", []byte(rulesFileJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	spec := rulesTargetSpec()
	spec.Rules = []synth.RuleSpec{{SetNull: []string{"note"}}}

	if err := synth.ApplyRulesFile(memfs, spec, "/rules.json"); err != nil {
		t.Fatalf("ApplyRulesFile: %v", err)
	}
	if len(spec.Rules) != 2 {
		t.Fatalf("Spec.Rules = %d entries, want exactly the file's 2 (REPLACE, not append)", len(spec.Rules))
	}
	if spec.Rules[0].When == "" {
		t.Error("Spec.Rules[0] is the pre-existing rule; the file must have replaced the slot outright")
	}
}

// TestApplyRulesFile_RefusalsNameThePath covers the two refusal classes
// the story pins. Both must name the FILE — an analyst editing a rules
// file beside a spec beside a profile needs to know which document is
// wrong — and the field-unknown case must additionally keep E1-S2's own
// coded error rather than degrading to a bare parse failure.
func TestApplyRulesFile_RefusalsNameThePath(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCode errors.Code
		wantMsg  string
	}{
		{
			name:     "unknown_field_keeps_the_E1S2_code",
			body:     `[{"set_null": ["nosuchfield"]}]`,
			wantCode: errors.PULSE_SYNTH_RULE_FIELD_UNKNOWN,
			wantMsg:  "nosuchfield",
		},
		{
			name:     "malformed_json",
			body:     `[{"set_null": ["note"]`,
			wantCode: errors.SERVICE_VALIDATION,
			wantMsg:  "rules file",
		},
		{
			name:     "object_not_array",
			body:     `{"rules": [{"set_null": ["note"]}]}`,
			wantCode: errors.SERVICE_VALIDATION,
			wantMsg:  "rules file",
		},
		{
			name:     "uncompilable_when",
			body:     `[{"when": "score >", "set_null": ["note"]}]`,
			wantCode: errors.PULSE_SYNTH_RULE_EXPR_INVALID,
			wantMsg:  "does not compile",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memfs := fs.NewMemMap().Fs()
			const path = "/cohort-rules.json"
			if err := afero.WriteFile(memfs, path, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			spec := rulesTargetSpec()
			err := synth.ApplyRulesFile(memfs, spec, path)
			if err == nil {
				t.Fatal("ApplyRulesFile returned nil, want a refusal")
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("error is %T, want *errors.CodedError", err)
			}
			if ce.Code != tc.wantCode {
				t.Errorf("code = %s, want %s (message: %s)", ce.Code, tc.wantCode, ce.Message)
			}
			if !strings.Contains(ce.Message, tc.wantMsg) {
				t.Errorf("message = %q, want it to mention %q", ce.Message, tc.wantMsg)
			}
			if got := ce.Details["path"]; got != path {
				t.Errorf("details[path] = %v, want %q — a refusal must name the file", got, path)
			}
			// A refused file must leave the spec untouched, or a
			// caller that logs and continues generates against half
			// a rules document.
			if len(spec.Rules) != 0 {
				t.Errorf("Spec.Rules = %+v after a refusal, want the slot untouched", spec.Rules)
			}
		})
	}
}

// TestApplyRulesFile_MissingFileNamesThePath keeps an unreadable path a
// DATA_FILE fault rather than a rule fault: the document is not wrong,
// it is absent.
func TestApplyRulesFile_MissingFileNamesThePath(t *testing.T) {
	memfs := fs.NewMemMap().Fs()
	err := synth.ApplyRulesFile(memfs, rulesTargetSpec(), "/nope.json")
	if err == nil {
		t.Fatal("want a refusal for a missing rules file")
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error is %T, want *errors.CodedError", err)
	}
	if ce.Code != errors.DATA_FILE {
		t.Errorf("code = %s, want DATA_FILE", ce.Code)
	}
	if got := ce.Details["path"]; got != "/nope.json" {
		t.Errorf("details[path] = %v, want the missing path", got)
	}
}

// TestApplyRulesFile_EmptyArrayIsAcceptedAndInert pins the boundary an
// empty document sits on: an empty rules array is a legal statement
// ("no rules"), not a malformed file, and must leave output
// byte-identical to no --rules at all.
func TestApplyRulesFile_EmptyArrayIsAcceptedAndInert(t *testing.T) {
	memfs := fs.NewMemMap().Fs()
	if err := afero.WriteFile(memfs, "/empty.json", []byte(`[]`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	spec := rulesTargetSpec()
	baseline, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4})
	if err != nil {
		t.Fatalf("SynthBytes(baseline): %v", err)
	}
	if err := synth.ApplyRulesFile(memfs, spec, "/empty.json"); err != nil {
		t.Fatalf("ApplyRulesFile([]): %v", err)
	}
	got, _, err := synth.SynthBytes(spec, synth.Options{Seed: 4})
	if err != nil {
		t.Fatalf("SynthBytes(after empty rules): %v", err)
	}
	if !bytes.Equal(baseline, got) {
		t.Error("an empty rules array changed the generated bytes")
	}
}

// TestWriteSpec_CarriesMergedRules is the compose criterion: an analyst
// emits, inspects what their rules became, and re-runs. The emitted
// document must therefore be the spec AFTER the --rules merge, and the
// re-run must reproduce the ruled cohort — not the pre-rules one.
func TestWriteSpec_CarriesMergedRules(t *testing.T) {
	memfs := fs.NewMemMap().Fs()
	if err := afero.WriteFile(memfs, "/rules.json", []byte(rulesFileJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	spec := rulesTargetSpec()
	if err := synth.ApplyRulesFile(memfs, spec, "/rules.json"); err != nil {
		t.Fatalf("ApplyRulesFile: %v", err)
	}
	ruled, _, err := synth.SynthBytes(spec, synth.Options{Seed: 21})
	if err != nil {
		t.Fatalf("SynthBytes(ruled): %v", err)
	}

	if err := synth.WriteSpec(memfs, spec, "/emitted.json"); err != nil {
		t.Fatalf("WriteSpec: %v", err)
	}
	raw, err := afero.ReadFile(memfs, "/emitted.json")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"rules"`)) {
		t.Fatal("emitted spec carries no rules key after --rules merged")
	}
	reparsed, err := synth.ParseSpec(raw)
	if err != nil {
		t.Fatalf("ParseSpec(emitted): %v", err)
	}
	if len(reparsed.Rules) != 2 {
		t.Fatalf("re-parsed Rules = %d, want 2", len(reparsed.Rules))
	}
	again, _, err := synth.SynthBytes(reparsed, synth.Options{Seed: 21})
	if err != nil {
		t.Fatalf("SynthBytes(re-parsed): %v", err)
	}
	if !bytes.Equal(ruled, again) {
		t.Error("re-running the emitted spec did not reproduce the ruled cohort")
	}
}

// setConstantSpecJSON is one set_u8 field whose constant value is
// supplied in the form named by %s, so the two encodings of the same
// selection can be generated and compared byte for byte.
const setConstantSpecJSON = `{
  "row_count": 12,
  "fields": [
    {"name": "topics", "type": "set_u8", "distribution": "constant",
     "params": {"options": ["tv", "radio"], "value": %s}}
  ]
}`

// TestParseSpec_SetConstantAcceptsTheJSONObjectForm covers the one
// shape in the whole Spec that does not decode back to the Go type it
// was marshalled from: a set_* constant held as map[string]bool.
//
// It is reachable without --emit-spec at all — an author may write
// {"value": {"tv": true}} by hand — but --emit-spec is what makes it
// UNAVOIDABLE: SpecFromProfile's unsummarisable fallback (E1-S6,
// sentinelFor) puts an empty map[string]bool on every all-null set_*
// column, so before this arm existed the emitted spec for a survey
// with one always-null multi-select parsed cleanly and then refused at
// generation. The object form must mean exactly what the array form
// means, and a false-valued key must mean "not selected" rather than
// "selected".
func TestParseSpec_SetConstantAcceptsTheJSONObjectForm(t *testing.T) {
	gen := func(t *testing.T, value string) []byte {
		t.Helper()
		spec, err := synth.ParseSpec(fmt.Appendf(nil, setConstantSpecJSON, value))
		if err != nil {
			t.Fatalf("ParseSpec(value=%s): %v", value, err)
		}
		data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 2})
		if err != nil {
			t.Fatalf("SynthBytes(value=%s): %v", value, err)
		}
		return data
	}

	arrayForm := gen(t, `["tv"]`)
	objectForm := gen(t, `{"tv": true}`)
	if !bytes.Equal(arrayForm, objectForm) {
		t.Error(`{"tv": true} did not generate the same cohort as ["tv"]`)
	}
	withFalseKey := gen(t, `{"tv": true, "radio": false}`)
	if !bytes.Equal(arrayForm, withFalseKey) {
		t.Error(`a false-valued key was treated as selected; {"tv":true,"radio":false} must equal ["tv"]`)
	}
	empty := gen(t, `{}`)
	if bytes.Equal(arrayForm, empty) {
		t.Error(`{} generated the same cohort as ["tv"]; the empty selection must differ`)
	}

	// A non-bool value is not a selection map in any reading. The
	// refusal lands at sampler construction (buildSampler ->
	// constantRowValue), not at ParseSpec: validateSpec checks the
	// document's shape, and what a distribution's params mean is the
	// sampler's own question.
	bad, err := synth.ParseSpec(fmt.Appendf(nil, setConstantSpecJSON, `{"tv": 1}`))
	if err != nil {
		t.Fatalf(`ParseSpec({"tv": 1}): %v`, err)
	}
	if _, _, err := synth.SynthBytes(bad, synth.Options{Seed: 2}); err == nil {
		t.Error(`{"tv": 1} was accepted as a set constant; want a refusal`)
	}
}
