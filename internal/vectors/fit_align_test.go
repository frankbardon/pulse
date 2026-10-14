package vectors

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestAlignFit: a bound fit aligns to a slot by label in any order;
// unequal label sets are PULSE_VECTOR_REFERENCE_MISMATCH naming the fit
// and the slot — the one rule the bind and the field-reference walk
// share.
func TestAlignFit(t *testing.T) {
	perm, ce := AlignFit("matrix", "m", "pop", []string{"a", "b", "c"}, []string{"c", "a", "b"})
	if ce != nil || !reflect.DeepEqual(perm, []int{2, 0, 1}) {
		t.Fatalf("perm %v, err %v", perm, ce)
	}
	_, ce = AlignFit("attribute", "d", "pop", []string{"a", "b", "c"}, []string{"a", "b"})
	if ce == nil || ce.Code != errors.PULSE_VECTOR_REFERENCE_MISMATCH || ce.Details["fit"] != "pop" || ce.Details["attribute"] != "d" {
		t.Fatalf("mismatch: %v %+v", ce, ce)
	}
	if ce.Message[:3] != "d: " {
		t.Fatalf("message %q is not prefixed with the slot", ce.Message)
	}
}

// TestAttributeFitLabels: the labels a fitted or comparison slot aligns
// a fit to — params.labels over inline fields, else the member names;
// a non-fit type resolves none.
func TestAttributeFitLabels(t *testing.T) {
	members := Resolved{Members: []string{"x", "y"}}
	a := &types.Attribute{Type: types.ATTR_MAHALANOBIS, Fields: []string{"x", "y"}, Params: json.RawMessage(`{"labels":["q1","q2"]}`)}
	if got, ok := AttributeFitLabels(types.ATTR_MAHALANOBIS, a, members, nil); !ok || !reflect.DeepEqual(got, []string{"q1", "q2"}) {
		t.Fatalf("fitted: %v %v", got, ok)
	}
	plain := &types.Attribute{Type: types.ATTR_PC_SCORE, Fields: []string{"x", "y"}}
	if got, ok := AttributeFitLabels(types.ATTR_PC_SCORE, plain, members, nil); !ok || !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("member names: %v %v", got, ok)
	}
	if _, ok := AttributeFitLabels(types.ATTR_FORMULA, plain, members, nil); ok {
		t.Fatal("a non-fit attribute resolved fit labels")
	}
}

// TestFitBound: a fitted slot is bound (row-local) exactly when its
// params.fit names a fit — on the three fitted types and on
// ATTR_VEC_DISTANCE; fit_on, an absent / empty fit, undecodable params
// and every other type are not.
func TestFitBound(t *testing.T) {
	cases := []struct {
		name   string
		typ    types.AttributeType
		params string
		want   bool
	}{
		{"mahalanobis fit", types.ATTR_MAHALANOBIS, `{"fit":"pop"}`, true},
		{"pc score fit", types.ATTR_PC_SCORE, `{"fit":"pop","component":1}`, true},
		{"factor score fit", types.ATTR_FACTOR_SCORE, `{"fit":"pop","factor":1}`, true},
		{"vec distance fit", types.ATTR_VEC_DISTANCE, `{"fit":"pop","metric":"mahalanobis","reference":[0,0]}`, true},
		{"fit_on", types.ATTR_MAHALANOBIS, `{"fit_on":"filtered"}`, false},
		{"no params", types.ATTR_MAHALANOBIS, ``, false},
		{"empty fit", types.ATTR_MAHALANOBIS, `{"fit":""}`, false},
		{"undecodable", types.ATTR_MAHALANOBIS, `{"fit":1}`, false},
		{"scale score", types.ATTR_SCALE_SCORE, `{"fit":"pop"}`, false},
		{"similarity", types.ATTR_PROFILE_SIMILARITY, `{"fit":"pop"}`, false},
		{"zscore", types.ATTR_ZSCORE, `{"fit":"pop"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &types.Attribute{Type: tc.typ}
			if tc.params != "" {
				a.Params = json.RawMessage(tc.params)
			}
			if got := FitBound(tc.typ, a); got != tc.want {
				t.Fatalf("FitBound = %v, want %v", got, tc.want)
			}
		})
	}
	if FitBound(types.ATTR_MAHALANOBIS, nil) {
		t.Fatal("a nil slot is not bound")
	}
}
