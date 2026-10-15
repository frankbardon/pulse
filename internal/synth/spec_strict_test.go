package synth_test

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
	"github.com/spf13/afero"
)

func unknownFieldOf(t *testing.T, err error) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want a *CodedError, got %T: %v", err, err)
	}
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD {
		t.Fatalf("code = %s (%s), want PULSE_REQUEST_UNKNOWN_FIELD", ce.Code, ce.Message)
	}
	return ce
}

// strictSpecBase is a minimal valid spec; the cases splice one bad key
// into it.
const strictSpecField = `{"name":"x","type":"f64","distribution":"normal","params":{"mean":0,"std":1}}`

// TestParseSpec_RefusesUnknownKeys: a synth spec decodes strictly — an
// unknown key at any depth, including a distribution's params object,
// is refused PULSE_REQUEST_UNKNOWN_FIELD with its path and a
// suggestion, never ignored (U44 #302). The pre-strict decode dropped
// every one of these and generated without the intended structure.
func TestParseSpec_RefusesUnknownKeys(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		path    string // "" = root (no details.path)
		unknown string
		suggest string // "" = no suggestion expected
	}{
		{name: "top-level misspelling",
			body:    `{"row_count":10,"fields":[` + strictSpecField + `],"corelations":[]}`,
			unknown: "corelations", suggest: "correlations"},
		{name: "top-level bogus",
			body:    `{"row_count":10,"fields":[` + strictSpecField + `],"bogus":1}`,
			unknown: "bogus"},
		{name: "exponential rate",
			body: `{"row_count":10,"fields":[{"name":"e","type":"f64","distribution":"exponential","params":{"rate":5}}]}`,
			path: "fields[0].params.rate", unknown: "rate", suggest: "lambda"},
		{name: "normal params misspelling",
			body: `{"row_count":10,"fields":[` + strictSpecField + `,{"name":"y","type":"f64","distribution":"normal","params":{"mean":0,"stdev":1}}]}`,
			path: "fields[1].params.stdev", unknown: "stdev", suggest: "std"},
		{name: "correlation key",
			body: `{"row_count":10,"fields":[` + strictSpecField + `,{"name":"y","type":"f64","distribution":"normal"}],` +
				`"correlations":[{"a":"x","b":"y","correlation":0.6}]}`,
			path: "correlations[0].correlation", unknown: "correlation", suggest: "rho"},
		{name: "residual correlation key",
			body: `{"row_count":10,"fields":[` + strictSpecField + `],` +
				`"residual_correlations":[{"a":"x","b":"y","correlation":0.6}]}`,
			path: "residual_correlations[0].correlation", unknown: "correlation", suggest: "rho"},
		{name: "vector correlation key",
			body: `{"row_count":10,"fields":[{"name":"v","type":"vec_f64","distribution":"normal",` +
				`"vector":{"dim":2,"correlations":[{"a":1,"b":2,"correlation":0.5}]}}]}`,
			path: "fields[0].vector.correlations[0].correlation", unknown: "correlation", suggest: "rho"},
		{name: "vector element params",
			body: `{"row_count":10,"fields":[{"name":"v","type":"vec_f64","distribution":"exponential",` +
				`"vector":{"dim":2,"elements":[{},{"params":{"rate":2}}]}}]}`,
			path: "fields[0].vector.elements[1].params.rate", unknown: "rate", suggest: "lambda"},
		{name: "vector element own distribution params",
			body: `{"row_count":10,"fields":[{"name":"v","type":"vec_f64","distribution":"normal",` +
				`"vector":{"dim":2,"elements":[{},{"distribution":"uniform","params":{"lambda":2}}]}}]}`,
			path: "fields[0].vector.elements[1].params.lambda", unknown: "lambda"},
		{name: "constant options on a scalar",
			body: `{"row_count":10,"fields":[{"name":"c","type":"f64","distribution":"constant","params":{"value":1,"options":["a"]}}]}`,
			path: "fields[0].params.options", unknown: "options"},
		{name: "rule key",
			body: `{"row_count":10,"fields":[` + strictSpecField + `],` +
				`"rules":[{"when":"x > 0","set_nul":["x"]}]}`,
			path: "rules[0].set_nul", unknown: "set_nul", suggest: "set_null"},
		{name: "field key",
			body: `{"row_count":10,"fields":[{"name":"x","type":"f64","distribution":"normal","nullrate":0.1}]}`,
			path: "fields[0].nullrate", unknown: "nullrate", suggest: "null_rate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := synth.ParseSpec([]byte(tc.body))
			ce := unknownFieldOf(t, err)
			if got, _ := ce.Details["path"].(string); got != tc.path {
				t.Errorf("details.path = %q, want %q", got, tc.path)
			}
			keys, _ := ce.Details["unknown_keys"].([]string)
			if len(keys) != 1 || keys[0] != tc.unknown {
				t.Errorf("unknown_keys = %v, want [%s]", keys, tc.unknown)
			}
			sugg, _ := ce.Details["suggestions"].(map[string]any)
			if got, _ := sugg[tc.unknown].(string); got != tc.suggest {
				t.Errorf("suggestion for %q = %q, want %q", tc.unknown, got, tc.suggest)
			}
		})
	}
}

// A programmatic Spec never passes through ParseSpec, so the params
// allowlist is enforced in validateSpec: Synth refuses it too.
func TestSynth_RefusesUnknownDistributionParam(t *testing.T) {
	spec := &synth.Spec{RowCount: 10, Fields: []synth.FieldSpec{
		{Name: "e", Type: "f64", Distribution: synth.DistExponential, Params: map[string]any{"rate": 5.0}},
	}}
	_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
	ce := unknownFieldOf(t, err)
	if got := ce.Details["path"]; got != "fields[0].params.rate" {
		t.Errorf("path = %v", got)
	}
}

// ParseRules (the --rules document) decodes strictly; ApplyRulesFile
// keeps the JSON location in details.path and names the file in
// details.file.
func TestParseRules_RefusesUnknownKeys(t *testing.T) {
	_, err := synth.ParseRules([]byte(`[{"when":"x > 0","set_null":["x"]},{"set_nul":["x"]}]`))
	ce := unknownFieldOf(t, err)
	if got := ce.Details["path"]; got != "[1].set_nul" {
		t.Errorf("path = %v, want [1].set_nul", got)
	}
	if got := ce.Details["suggestions"].(map[string]any)["set_nul"]; got != "set_null" {
		t.Errorf("suggestion = %v", got)
	}

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "r.json", []byte(`[{"set_nul":["x"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := &synth.Spec{RowCount: 1, Fields: []synth.FieldSpec{{Name: "x", Type: "f64", Distribution: synth.DistNormal}}}
	ce = unknownFieldOf(t, synth.ApplyRulesFile(fs, spec, "r.json"))
	if got := ce.Details["path"]; got != "[0].set_nul" {
		t.Errorf("ApplyRulesFile path = %v, want [0].set_nul", got)
	}
	if got := ce.Details["file"]; got != "r.json" {
		t.Errorf("ApplyRulesFile file = %v, want r.json", got)
	}
}

// ParseProfile (the from-profile document) decodes strictly.
func TestParseProfile_RefusesUnknownKeys(t *testing.T) {
	_, err := synth.ParseProfile([]byte(`{"row_count":3,"fields":[{"name":"x","type":"f64","nul_rate":0}]}`))
	ce := unknownFieldOf(t, err)
	if got := ce.Details["path"]; got != "fields[0].nul_rate" {
		t.Errorf("path = %v, want fields[0].nul_rate", got)
	}
	if got := ce.Details["suggestions"].(map[string]any)["nul_rate"]; got != "null_rate" {
		t.Errorf("suggestion = %v, want null_rate", got)
	}
}

// TestSynth_RhoPlantsCorrelation: `correlations[].rho` is the key the
// copula reads. Before the rename the spec key was `correlation`, so a
// spec written as the skill documented (`rho`) generated with NO
// correlation and no message.
func TestSynth_RhoPlantsCorrelation(t *testing.T) {
	const target, tol = 0.6, 0.03
	spec, err := synth.ParseSpec([]byte(`{"row_count":20000,"fields":[
		{"name":"a","type":"f64","distribution":"normal","params":{"mean":0,"std":1}},
		{"name":"b","type":"f64","distribution":"normal","params":{"mean":10,"std":3}}],
		"correlations":[{"a":"a","b":"b","rho":0.6}]}`))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	if got := pearsonOf(readF64Field(t, data, "a"), readF64Field(t, data, "b")); math.Abs(got-target) > tol {
		t.Fatalf("realised rho = %.4f, want within %.2f of %.2f", got, tol, target)
	}
}

// TestSpecFromProfile_RoundTripsRho: a captured correlation reaches the
// derived spec's Rho, and the written spec (WriteSpec) carries `rho` and
// parses back strictly to the same figure; the profile document itself
// round-trips through ParseProfile.
func TestSpecFromProfile_RoundTripsRho(t *testing.T) {
	src, err := synth.ParseSpec([]byte(`{"row_count":5000,"fields":[
		{"name":"a","type":"f64","distribution":"normal","params":{"mean":0,"std":1}},
		{"name":"b","type":"f64","distribution":"normal","params":{"mean":0,"std":1}}],
		"correlations":[{"a":"a","b":"b","rho":0.7}]}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := synth.SynthBytes(src, synth.Options{Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{IncludeCorrelations: true, IncludeConditional: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(prof)
	if err != nil {
		t.Fatal(err)
	}
	back, err := synth.ParseProfile(raw)
	if err != nil {
		t.Fatalf("a captured profile must decode strictly: %v", err)
	}
	spec, _ := synth.SpecFromProfile(back, 100)
	if len(spec.Correlations) != 1 {
		t.Fatalf("derived correlations = %+v, want one", spec.Correlations)
	}
	got := spec.Correlations[0].Rho
	if math.Abs(got-0.7) > 0.05 {
		t.Fatalf("derived rho = %.4f, want ≈0.7", got)
	}

	fs := afero.NewMemMapFs()
	if err := synth.WriteSpec(fs, spec, "s.json"); err != nil {
		t.Fatal(err)
	}
	written, _ := afero.ReadFile(fs, "s.json")
	if !strings.Contains(string(written), `"rho"`) || strings.Contains(string(written), `"correlation"`) {
		t.Fatalf("written spec must carry rho, never correlation:\n%s", written)
	}
	reparsed, err := synth.ParseSpec(written)
	if err != nil {
		t.Fatalf("written spec must parse strictly: %v", err)
	}
	if reparsed.Correlations[0].Rho != got {
		t.Fatalf("round trip rho = %v, want %v", reparsed.Correlations[0].Rho, got)
	}
}
