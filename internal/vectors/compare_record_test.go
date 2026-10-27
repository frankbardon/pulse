package vectors

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestComparisonParams_Record pins the {record: {cohort?, keys}}
// reference form (U30 E3-S2) through the one decode predict and the
// runtime share: the decoded operands, the static shape refusals, the
// canonical round trip the factory decodes back, the record key both
// the Service and the engine derive, and the loose reader the Service
// scans with.
func TestComparisonParams_Record(t *testing.T) {
	t.Parallel()

	members := Resolved{Name: "vb", Members: []string{"b1", "b2"}, Labels: []string{"x", "y"}}
	cases := []struct {
		name   string
		ref    string
		cohort *types.Cohort
		keys   []types.LookupKey
		reason string
	}{
		{name: "own cohort", ref: `{"record":{"keys":[{"field":"id","value":"7"}]}}`, keys: []types.LookupKey{{Field: "id", Value: "7"}}},
		{name: "other cohort, composite key", ref: `{"record":{"cohort":{"filename":"ref.pulse","data_dir":"d"},"keys":[{"field":"region","value":"n"},{"field":"id","value":"7"}]}}`,
			cohort: &types.Cohort{Filename: "ref.pulse", DataDir: "d"}, keys: []types.LookupKey{{Field: "region", Value: "n"}, {Field: "id", Value: "7"}}},
		// A label named record stays a label map (its value a number),
		// judged by label.
		{name: "a label named record is a label map", ref: `{"record":1,"x":2}`, reason: ReferenceReasonLabelsDiffer},
		{name: "record beside another key", ref: `{"record":{"keys":[{"field":"id","value":"7"}]},"x":1}`, reason: "bad_reference"},
		{name: "unknown inner key", ref: `{"record":{"key":[{"field":"id","value":"7"}]}}`, reason: "bad_reference"},
		{name: "no keys", ref: `{"record":{}}`, reason: "bad_reference"},
		{name: "empty keys", ref: `{"record":{"keys":[]}}`, reason: "bad_reference"},
		{name: "numeric value", ref: `{"record":{"keys":[{"field":"id","value":7}]}}`, reason: "bad_reference"},
		{name: "empty field", ref: `{"record":{"keys":[{"field":"","value":"7"}]}}`, reason: "bad_reference"},
		{name: "repeated field", ref: `{"record":{"keys":[{"field":"id","value":"7"},{"field":"id","value":"8"}]}}`, reason: "bad_reference"},
		{name: "cohort without filename", ref: `{"record":{"cohort":{"data_dir":"d"},"keys":[{"field":"id","value":"7"}]}}`, reason: "bad_reference"},
		{name: "cohort unknown key", ref: `{"record":{"cohort":{"file":"x"},"keys":[{"field":"id","value":"7"}]}}`, reason: "bad_reference"},
	}
	for _, typ := range ComparisonTypes() {
		for _, c := range cases {
			t.Run(string(typ)+"/"+c.name, func(t *testing.T) {
				metric := `"cosine"`
				if typ == types.ATTR_VEC_DISTANCE {
					metric = `"euclidean"`
				}
				raw := json.RawMessage(`{"metric":` + metric + `,"reference":` + c.ref + `}`)
				cfg, err := ComparisonParams("attributes[0]", typ, raw, members, testInputSchema())
				if c.reason != "" {
					code := errors.SERVICE_VALIDATION
					if c.reason == ReferenceReasonLabelsDiffer {
						code = errors.PULSE_VECTOR_REFERENCE_MISMATCH
					}
					if err == nil || err.Code != code || err.Details["reason"] != c.reason {
						t.Fatalf("got %v (%+v), want SERVICE_VALIDATION %s", err, cfg.Reference, c.reason)
					}
					if _, ok := ComparisonRecord(typ, &types.Attribute{Type: typ, Params: raw}); ok {
						t.Error("ComparisonRecord reads a refused record")
					}
					return
				}
				if err != nil {
					t.Fatalf("refused: %s", err.Message)
				}
				ref := cfg.Reference
				if ref.Form != ReferenceRecord || !reflect.DeepEqual(ref.Cohort, c.cohort) || !reflect.DeepEqual(ref.Keys, c.keys) || !ref.Static() || ref.KeyColumn() != "" {
					t.Fatalf("reference %+v, want record cohort %+v keys %+v, static, no key column", ref, c.cohort, c.keys)
				}
				back, berr := ComparisonParams("attributes[0]", typ, cfg.CanonicalParams(), Resolved{Members: members.Members, Labels: members.Members}, testInputSchema())
				if berr != nil {
					t.Fatalf("canonical %s refused: %s", cfg.CanonicalParams(), berr.Message)
				}
				if !reflect.DeepEqual(back.Reference, ref) || back.Reference.RecordKey() != ref.RecordKey() {
					t.Errorf("canonical %s decodes to %+v, want %+v", cfg.CanonicalParams(), back.Reference, ref)
				}
				loose, ok := ComparisonRecord(typ, &types.Attribute{Type: typ, Params: raw})
				if !ok || loose.RecordKey() != ref.RecordKey() {
					t.Errorf("ComparisonRecord = %+v %v, want the decoded record", loose, ok)
				}
				if b, terr := ComparisonTableIssue("attributes[0]", cfg, nil); terr != nil || b.Static != nil {
					t.Errorf("table issue %v / %+v, want none", terr, b)
				}
				// A record is row-local: never two-pass, no key column.
				a := &types.Attribute{Type: typ, Params: raw}
				if ComparisonTwoPass(typ, a, nil, nil) || ComparisonKeyField(typ, a) != "" || ComparisonCentroid(typ, a) {
					t.Error("a record reference routes like a pre-pass / per-row key")
				}
			})
		}
	}
	// The key separates cohorts and key order.
	a := ComparisonReference{Form: ReferenceRecord, Keys: []types.LookupKey{{Field: "a", Value: "1"}, {Field: "b", Value: "2"}}}
	b := ComparisonReference{Form: ReferenceRecord, Keys: []types.LookupKey{{Field: "b", Value: "2"}, {Field: "a", Value: "1"}}}
	c := ComparisonReference{Form: ReferenceRecord, Cohort: &types.Cohort{Filename: "x.pulse"}, Keys: a.Keys}
	if a.RecordKey() == b.RecordKey() || a.RecordKey() == c.RecordKey() {
		t.Errorf("record keys collide: %s / %s / %s", a.RecordKey(), b.RecordKey(), c.RecordKey())
	}
}

// TestRecordRefusals pins the record member refusal's shape.
func TestRecordRefusals(t *testing.T) {
	t.Parallel()

	ce := RecordMembersAbsent("s", "ref.pulse", []string{"b2"})
	if ce.Code != errors.PULSE_VECTOR_REFERENCE_MISMATCH || ce.Details["reason"] != ReferenceReasonRecordMembersAbsent ||
		!reflect.DeepEqual(ce.Details["missing"], []string{"b2"}) || ce.Details["cohort"] != "ref.pulse" || ce.Details["slot"] != "s.params.reference" {
		t.Errorf("members absent = %s %+v", ce.Code, ce.Details)
	}
}
