package descriptor

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/synth"
)

// The tests below pin the synth distribution behaviours the manifest
// capability notes, the op-synth-* skills and the Purposes in
// purposes_synth.go state, so a doc that drifts from the sampler (or a
// sampler that drifts from its docs) fails here. Each one drives the
// real generator through synth.SynthBytes and reads the bytes back.

// synthDocsRows generates spec and returns the decoded value of field per
// row, plus the read schema (for dictionary-bearing fields).
func synthDocsRows(t *testing.T, spec *synth.Spec, field string) ([]float64, *encoding.Schema) {
	t.Helper()
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	rr := encx.NewRecordReader(r, schema)
	values := map[string]float64{}
	nulls := map[string]bool{}
	var out []float64
	for rr.ReadRecord(values, nulls) == nil {
		out = append(out, values[field])
	}
	if len(out) != spec.RowCount {
		t.Fatalf("read %d rows, want %d", len(out), spec.RowCount)
	}
	return out, schema
}

func distributionMeta(t *testing.T, name string) (out struct {
	description string
	params      map[string]any // param name -> default (nil when none)
	paramDesc   map[string]string
}) {
	t.Helper()
	for _, d := range distributionCapabilities() {
		if d.Name != name {
			continue
		}
		out.description = d.Description
		out.params = map[string]any{}
		out.paramDesc = map[string]string{}
		for _, p := range d.Params {
			out.params[p.Name] = p.Default
			out.paramDesc[p.Name] = p.Description
		}
		return out
	}
	t.Fatalf("no capability for distribution %s", name)
	return out
}

// TestDistributionDocs_RegexCapsOpenEndedRepeats: an unbounded `*` is
// accepted (not refused as "must be finite") and capped at max_repeat,
// which the manifest declares with its default of 8.
func TestDistributionDocs_RegexCapsOpenEndedRepeats(t *testing.T) {
	meta := distributionMeta(t, synth.DistRegex)
	if def, ok := meta.params["max_repeat"]; !ok || def != 8 {
		t.Errorf("regex capability params: max_repeat default = %v (declared %v), want 8", def, ok)
	}
	if strings.Contains(meta.description, "must be finite") {
		t.Errorf("regex description still says repeats must be finite: %q", meta.description)
	}

	spec := &synth.Spec{RowCount: 400, Fields: []synth.FieldSpec{{
		Name: "code", Type: "categorical_u8", Distribution: synth.DistRegex,
		Params: map[string]any{"pattern": "x-a*", "max_repeat": 3},
	}}}
	_, schema := synthDocsRows(t, spec, "code")
	dict := schema.Field("code").Dictionary
	longest := 0
	for _, s := range dict.Values() {
		if !strings.HasPrefix(s, "x-") || strings.Trim(s[2:], "a") != "" {
			t.Errorf("generated %q does not match x-a*", s)
		}
		longest = max(longest, len(s)-2)
	}
	if longest != 3 {
		t.Errorf("longest a-run = %d, want exactly max_repeat (3)", longest)
	}
}

// TestDistributionDocs_UniformDateBounds: end may equal start (the
// manifest no longer says "must exceed"), and a pre-1970 range is
// generated as negative epoch days (the skill no longer says `date`
// cannot store it).
func TestDistributionDocs_UniformDateBounds(t *testing.T) {
	meta := distributionMeta(t, synth.DistUniformDate)
	if strings.Contains(meta.paramDesc["end"], "exceed") {
		t.Errorf("uniform_date end param says it must exceed start: %q", meta.paramDesc["end"])
	}

	same := &synth.Spec{RowCount: 20, Fields: []synth.FieldSpec{{
		Name: "d", Type: "date", Distribution: synth.DistUniformDate,
		Params: map[string]any{"start": "2024-03-01", "end": "2024-03-01"},
	}}}
	got, _ := synthDocsRows(t, same, "d")
	for i, v := range got {
		if v != got[0] {
			t.Fatalf("row %d: equal bounds drew %v and %v", i, got[0], v)
		}
	}

	pre := &synth.Spec{RowCount: 50, Fields: []synth.FieldSpec{{
		Name: "d", Type: "date", Distribution: synth.DistUniformDate,
		Params: map[string]any{"start": "1969-12-01", "end": "1969-12-31"},
	}}}
	got, _ = synthDocsRows(t, pre, "d")
	for i, v := range got {
		if v < -31 || v > -1 {
			t.Fatalf("row %d: pre-epoch date stored as %v, want a day in [-31, -1]", i, v)
		}
	}
}

// TestDistributionDocs_ConstantRefusesWrongShapeAtParse: a value the
// field cannot hold is SERVICE_VALIDATION when the spec is compiled, not
// a write-time cast failure under some other code.
func TestDistributionDocs_ConstantRefusesWrongShapeAtParse(t *testing.T) {
	spec := &synth.Spec{RowCount: 5, Fields: []synth.FieldSpec{{
		Name: "n", Type: "u8", Distribution: synth.DistConstant,
		Params: map[string]any{"value": "seven"},
	}}}
	_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Fatalf("constant text on u8: err = %v, want SERVICE_VALIDATION", err)
	}
}

// TestDistributionDocs_IntegerFieldsRoundDraws: a u8 field rounds each
// draw to the nearest whole number (never truncates), so uniform [0, 10)
// stores 10 for draws at or above 9.5 and 0 for draws below 0.5.
func TestDistributionDocs_IntegerFieldsRoundDraws(t *testing.T) {
	spec := &synth.Spec{RowCount: 2000, Fields: []synth.FieldSpec{{
		Name: "n", Type: "u8", Distribution: synth.DistUniform,
		Params: map[string]any{"min": 0.0, "max": 10.0},
	}}}
	got, _ := synthDocsRows(t, spec, "n")
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range got {
		lo, hi = min(lo, v), max(hi, v)
	}
	if lo != 0 || hi != 10 {
		t.Errorf("uniform [0,10) on u8 stored range [%v, %v], want [0, 10] (rounding)", lo, hi)
	}
}

// TestDistributionDocs_LognormalSpreadGrowsWithSigma: a LARGER sigma
// gives the wider spread (the skill once said smaller).
func TestDistributionDocs_LognormalSpreadGrowsWithSigma(t *testing.T) {
	spread := func(sigma float64) float64 {
		spec := &synth.Spec{RowCount: 4000, Fields: []synth.FieldSpec{{
			Name: "x", Type: "f64", Distribution: synth.DistLogNormal,
			Params: map[string]any{"mu": 0.0, "sigma": sigma},
		}}}
		got, _ := synthDocsRows(t, spec, "x")
		var sum, sq float64
		for _, v := range got {
			sum += v
		}
		mean := sum / float64(len(got))
		for _, v := range got {
			sq += (v - mean) * (v - mean)
		}
		return math.Sqrt(sq / float64(len(got)))
	}
	if narrow, wide := spread(0.25), spread(1.0); !(wide > narrow) {
		t.Errorf("lognormal spread: sigma 1.0 gave %v, sigma 0.25 gave %v; want the larger sigma wider", wide, narrow)
	}
}

// TestDistributionDocs_SkillsDropRetiredClaims: the op-synth-* skill
// sentences the tests above disproved stay gone.
func TestDistributionDocs_SkillsDropRetiredClaims(t *testing.T) {
	retired := map[string][]string{
		"op-synth-constant.md":     {"never converts", "PULSE_SYNTH_VALUE_INVALID", "17 `.pulse` field types"},
		"op-synth-uniform.md":      {"truncation", "max-observed `9`"},
		"op-synth-lognormal.md":    {"small `sigma` increases"},
		"op-synth-uniform-date.md": {"cannot store"},
	}
	for file, phrases := range retired {
		raw, err := os.ReadFile(filepath.Join("..", "skills", file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, p := range phrases {
			if strings.Contains(string(raw), p) {
				t.Errorf("%s still says %q", file, p)
			}
		}
	}
}
