package synth_test

import (
	"bytes"
	"context"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/synth"
	"github.com/spf13/afero"
)

// E3-S1 (FR-20) test pack: constraints[] on the profile path — the
// standalone constraints file `synth from-profile --constraints` loads,
// and the rejection-rate ceiling beside it.

func writeConstraintsFile(t *testing.T, fs afero.Fs, path, body string) {
	t.Helper()
	if err := afero.WriteFile(fs, path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestApplyConstraintsFile_ProfilePathHonoursConstraints: a
// profile-derived spec carrying a loaded constraint generates rows that
// all satisfy it, rejecting (and re-drawing) the rest, and the tagged
// top-up keeps the source rows untouched by it.
func TestApplyConstraintsFile_ProfilePathHonoursConstraints(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	p, spec := setupAugmentFixture(t, fs, "/source.pulse", 200)
	spec.RowCount = 400
	writeConstraintsFile(t, fs, "/constraints.json", `[{"expr": "score > -1"}, {"expr": "country != \"DE\""}]`)
	if err := synth.ApplyConstraintsFile(fs, spec, "/constraints.json"); err != nil {
		t.Fatalf("ApplyConstraintsFile: %v", err)
	}
	if want := []synth.ConstraintSpec{{Expr: "score > -1"}, {Expr: `country != "DE"`}}; !reflect.DeepEqual(spec.Constraints, want) {
		t.Fatalf("constraints = %+v, want %+v", spec.Constraints, want)
	}

	res, err := p.Synth(context.Background(), spec, "/only.pulse",
		pulse.SynthOptions{Seed: 4, SourceCohort: "/source.pulse", SyntheticOnly: true})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	if res.RowsGenerated != 400 || res.RowsRejected == 0 {
		t.Fatalf("result = %+v, want 400 accepted rows and some rejected", res)
	}
	data, err := afero.ReadFile(fs, "/only.pulse")
	if err != nil {
		t.Fatal(err)
	}
	scores := readF64Field(t, data, "score")
	if len(scores) != 400 {
		t.Fatalf("rows = %d, want 400", len(scores))
	}
	for i, s := range scores {
		if !(s > -1) {
			t.Fatalf("row %d: score = %g violates the loaded constraint", i, s)
		}
	}
	for i, c := range readCategoricalLabels(t, data, "country") {
		if c == "DE" {
			t.Fatalf("row %d: country = DE violates the loaded constraint", i)
		}
	}
}

// TestApplyConstraintsFile_RefusalsNameTheFile: every refusal carries
// its own code and the file, and leaves the spec's constraints as they
// were.
func TestApplyConstraintsFile_RefusalsNameTheFile(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	_, spec := setupAugmentFixture(t, fs, "/source.pulse", 50)
	before := []synth.ConstraintSpec{{Expr: "score > -10"}}
	for _, c := range []struct {
		name, body string
		code       errors.Code
		detail     string
	}{
		{"unknown_field", `[{"expr": "nosuch > 0"}]`, errors.SERVICE_VALIDATION, "path"},
		{"unknown_key", `[{"exp": "score > 0"}]`, errors.PULSE_REQUEST_UNKNOWN_FIELD, "file"},
		{"object", `{"constraints": [{"expr": "score > 0"}]}`, errors.SERVICE_VALIDATION, "path"},
		{"draw", `[{"expr": "uniform() < 0.5"}]`, errors.PULSE_SYNTH_RULE_DRAW_INVALID, "path"},
		{"now", `[{"expr": "now() > 0"}]`, errors.PULSE_SYNTH_EXPR_NONDETERMINISTIC, "path"},
	} {
		t.Run(c.name, func(t *testing.T) {
			probe := *spec
			probe.Constraints = append([]synth.ConstraintSpec(nil), before...)
			path := "/" + c.name + ".json"
			writeConstraintsFile(t, fs, path, c.body)
			err := synth.ApplyConstraintsFile(fs, &probe, path)
			if !errors.HasCode(err, c.code) {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if got := constraintErrDetails(err)[c.detail]; got != path {
				t.Errorf("details.%s = %v, want %q", c.detail, got, path)
			}
			if !reflect.DeepEqual(probe.Constraints, before) {
				t.Errorf("a refused file changed the constraints to %+v", probe.Constraints)
			}
		})
	}
	if err := synth.ApplyConstraintsFile(fs, spec, "/missing.json"); !errors.HasCode(err, errors.DATA_FILE) {
		t.Errorf("missing file: err = %v, want DATA_FILE", err)
	}
}

// TestApplyMaxRejectionRate_BoundsTheProfilePathRun: the ceiling set on
// a profile-derived spec is the one generation enforces, under the
// existing PULSE_SYNTH_CONSTRAINT_INFEASIBLE, and an out-of-range rate
// is refused before anything is set.
func TestApplyMaxRejectionRate_BoundsTheProfilePathRun(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	p, spec := setupAugmentFixture(t, fs, "/source.pulse", 200)
	spec.RowCount = 400
	// score is ~N(0,1): about half the draws fail, under the default
	// ceiling of 0.5 sometimes and under 0.9 never.
	writeConstraintsFile(t, fs, "/c.json", `[{"expr": "score > 0.2"}]`)
	if err := synth.ApplyConstraintsFile(fs, spec, "/c.json"); err != nil {
		t.Fatal(err)
	}

	if err := synth.ApplyMaxRejectionRate(spec, 0.1); err != nil {
		t.Fatalf("ApplyMaxRejectionRate(0.1): %v", err)
	}
	_, err := p.Synth(context.Background(), spec, "/tight.pulse",
		pulse.SynthOptions{Seed: 5, SourceCohort: "/source.pulse", SyntheticOnly: true})
	if !errors.HasCode(err, errors.PULSE_SYNTH_CONSTRAINT_INFEASIBLE) {
		t.Fatalf("rate 0.1: err = %v, want PULSE_SYNTH_CONSTRAINT_INFEASIBLE", err)
	}
	if got := constraintErrDetails(err)["threshold"]; got != 0.1 {
		t.Errorf("details.threshold = %v, want the applied 0.1", got)
	}
	if ok, _ := afero.Exists(fs, "/tight.pulse"); ok {
		t.Error("an infeasible run wrote its output")
	}

	if err := synth.ApplyMaxRejectionRate(spec, 0.9); err != nil {
		t.Fatalf("ApplyMaxRejectionRate(0.9): %v", err)
	}
	res, err := p.Synth(context.Background(), spec, "/loose.pulse",
		pulse.SynthOptions{Seed: 5, SourceCohort: "/source.pulse", SyntheticOnly: true})
	if err != nil {
		t.Fatalf("rate 0.9: %v", err)
	}
	if res.RowsGenerated != 400 || res.RowsRejected == 0 {
		t.Errorf("rate 0.9: result = %+v", res)
	}

	for _, bad := range []float64{-0.1, 1, math.NaN()} {
		probe := *spec
		if err := synth.ApplyMaxRejectionRate(&probe, bad); !errors.HasCode(err, errors.SERVICE_VALIDATION) {
			t.Errorf("rate %v: err = %v, want SERVICE_VALIDATION", bad, err)
		}
		if probe.MaxRejectionRate != 0.9 {
			t.Errorf("rate %v: a refused rate changed the spec to %v", bad, probe.MaxRejectionRate)
		}
	}
}

func constraintErrDetails(err error) map[string]any {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return nil
	}
	return ce.Details
}

// readCategoricalLabels decodes one categorical field as its labels.
func readCategoricalLabels(t *testing.T, data []byte, name string) []string {
	t.Helper()
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	f := schema.Field(name)
	rr := encx.NewRecordReader(r, schema)
	var out []string
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	for rr.ReadRecord(values, nulls) == nil {
		out = append(out, f.Dictionary.Resolve(uint32(values[name])))
	}
	return out
}
