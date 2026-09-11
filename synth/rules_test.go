package synth_test

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/synth"
)

// rulesSpecJSON is one spec carrying every rule slot, used by the
// round-trip and shape tests below. Deliberately hand-written JSON rather
// than a marshal of a Go value: the document is the contract three later
// consumers share (the per-row pass, the standalone `--rules` file and
// the detection output), so what is being pinned is the JSON an author
// types, not the struct it happens to decode into.
const rulesSpecJSON = `{
  "row_count": 8,
  "fields": [
    {"name": "id", "type": "u32", "distribution": "monotonic_from", "params": {"start": 1}},
    {"name": "aware", "type": "packed_bool", "distribution": "bernoulli", "params": {"p": 0.5}},
    {"name": "nps", "type": "u4", "distribution": "uniform", "params": {"min": 0, "max": 11}},
    {"name": "promoter", "type": "packed_bool", "distribution": "bernoulli", "params": {"p": 0.3}},
    {"name": "region", "type": "categorical_u8", "distribution": "weighted_categorical",
     "params": {"values": ["east", "west"]}},
    {"name": "perception_a", "type": "u4", "nullable": true, "null_rate": 0.1,
     "distribution": "uniform", "params": {"min": 1, "max": 6}},
    {"name": "perception_b", "type": "u4", "nullable": true, "null_rate": 0.1,
     "distribution": "uniform", "params": {"min": 1, "max": 6}}
  ],
  "rules": [
    {"when": "aware == 0", "set_null": ["perception_a", "perception_b"], "set": {"region": "east"}},
    {"set_expr": {"promoter": "nps >= 9"}},
    {"null_together": ["perception_a", "perception_b"]}
  ]
}`

// TestRules_StandaloneFileShapeIsTheSpecSlot asserts the standalone
// rules-file format IS the `Spec.Rules` array, so an inline declaration
// and a file are the same JSON.
//
// This is the criterion the E2-S1 loader rests on: if the file needed a
// wrapper object, or spelled a slot differently, an author would have two
// formats to learn and the loader would have a translation layer to get
// wrong.
func TestRules_StandaloneFileShapeIsTheSpecSlot(t *testing.T) {
	spec, err := synth.ParseSpec([]byte(rulesSpecJSON))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}

	// The same three rules, lifted out of the spec into a bare array —
	// which is what a `--rules` file holds.
	var inline map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rulesSpecJSON), &inline); err != nil {
		t.Fatalf("unmarshal spec: %v", err)
	}
	var fromFile []synth.RuleSpec
	if err := json.Unmarshal(inline["rules"], &fromFile); err != nil {
		t.Fatalf("a rules array must decode standalone: %v", err)
	}
	if !reflect.DeepEqual(fromFile, spec.Rules) {
		t.Fatalf("standalone array decoded to %#v, inline slot to %#v", fromFile, spec.Rules)
	}
	if len(spec.Rules) != 3 {
		t.Fatalf("len(Rules) = %d, want 3", len(spec.Rules))
	}
	if spec.Rules[0].When != "aware == 0" {
		t.Errorf("Rules[0].When = %q", spec.Rules[0].When)
	}
	if got := spec.Rules[0].Set["region"]; got != "east" {
		t.Errorf("Rules[0].Set[region] = %#v, want \"east\"", got)
	}
	if got := spec.Rules[1].SetExpr["promoter"]; got != "nps >= 9" {
		t.Errorf("Rules[1].SetExpr[promoter] = %q", got)
	}
	if got := spec.Rules[2].NullTogether; !reflect.DeepEqual(got, []string{"perception_a", "perception_b"}) {
		t.Errorf("Rules[2].NullTogether = %#v", got)
	}
}

// TestRules_AbsentSlotRoundTripsByteIdentically asserts the slot is
// additive on the wire: a spec with no `rules` key marshals without one,
// so an existing document survives a decode/encode round trip unchanged.
func TestRules_AbsentSlotRoundTripsByteIdentically(t *testing.T) {
	const noRules = `{"row_count":4,"fields":[{"name":"id","type":"u32","distribution":"monotonic_from","params":{"start":1}}]}`
	spec, err := synth.ParseSpec([]byte(noRules))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if spec.Rules != nil {
		t.Fatalf("Rules = %#v, want nil for a document with no rules key", spec.Rules)
	}
	out, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(out, []byte("rules")) {
		t.Fatalf("omitempty broken: marshalled form carries a rules key: %s", out)
	}

	var again synth.Spec
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	twice, err := json.Marshal(&again)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Equal(out, twice) {
		t.Fatalf("round trip is not byte-identical:\n first: %s\nsecond: %s", out, twice)
	}
}

// TestRules_ValidateAndDoNothing is the SCOPE BOUNDARY of this story and
// the precondition of the next one's byte-identity gate. A declared rule
// is fully validated and applied to no row: the generated file is
// byte-identical to the same spec with the rules removed.
//
// Both halves matter and each is trivially satisfiable by abandoning the
// other — a validator that refuses nothing would also change no bytes,
// and a slot that is ignored entirely would also produce identical bytes
// — so the inert half is asserted beside a refusal over the SAME spec.
func TestRules_ValidateAndDoNothing(t *testing.T) {
	withRules, err := synth.ParseSpec([]byte(rulesSpecJSON))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	withoutRules, err := synth.ParseSpec([]byte(rulesSpecJSON))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	withoutRules.Rules = nil

	ruled, _, err := synth.SynthBytes(withRules, synth.Options{Seed: 99})
	if err != nil {
		t.Fatalf("SynthBytes with rules: %v", err)
	}
	plain, _, err := synth.SynthBytes(withoutRules, synth.Options{Seed: 99})
	if err != nil {
		t.Fatalf("SynthBytes without rules: %v", err)
	}
	if !bytes.Equal(ruled, plain) {
		t.Fatalf("a declared rule changed %d bytes; at this story rules validate and apply to no row",
			byteDiffCount(ruled, plain))
	}

	// ...and the validator over that same spec is live: break one rule
	// and the run is refused rather than silently generating.
	broken, err := synth.ParseSpec([]byte(rulesSpecJSON))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	broken.Rules[0].SetNull = append(broken.Rules[0].SetNull, "no_such_field")
	if _, _, err := synth.SynthBytes(broken, synth.Options{Seed: 99}); err == nil {
		t.Fatal("want a refusal for a rule naming an undeclared field, got nil")
	}
}

// TestRules_ParseSpecRefusesEagerly asserts the refusal reaches the
// PUBLIC entry points — ParseSpec, SynthBytes and Synth all validate — so
// a malformed rule cannot survive to row time on any path.
func TestRules_ParseSpecRefusesEagerly(t *testing.T) {
	const bad = `{
      "row_count": 4,
      "fields": [{"name":"id","type":"u32","distribution":"monotonic_from","params":{"start":1}}],
      "rules": [{"when": "id >", "set_null": ["id"]}]
    }`
	_, err := synth.ParseSpec([]byte(bad))
	if err == nil {
		t.Fatal("ParseSpec accepted an uncompilable when")
	}
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("want *CodedError, got %T", err)
	}
	if coded.Code != errors.PULSE_SYNTH_RULE_EXPR_INVALID {
		t.Fatalf("code = %s, want PULSE_SYNTH_RULE_EXPR_INVALID", coded.Code)
	}

	// The same document reaching SynthBytes through a hand-built Spec
	// (no ParseSpec) is refused too: validateSpec is the shared gate.
	spec := &synth.Spec{
		RowCount: 4,
		Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistMonotonicFrom,
				Params: map[string]any{"start": 1.0}},
		},
		Rules: []synth.RuleSpec{{When: "id >", SetNull: []string{"id"}}},
	}
	if _, _, err := synth.SynthBytes(spec, synth.Options{}); err == nil {
		t.Fatal("SynthBytes accepted an uncompilable when")
	}
}

func byteDiffCount(a, b []byte) int {
	n := 0
	if len(a) != len(b) {
		n += len(a) - len(b)
		if n < 0 {
			n = -n
		}
	}
	min := len(a)
	if len(b) < min {
		min = len(b)
	}
	for i := 0; i < min; i++ {
		if a[i] != b[i] {
			n++
		}
	}
	return n
}
