package vectors

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// computedRequest computes a feature output, two attribute labels (one
// defaulted) and a window label over testSchema.
func computedRequest() *types.Request {
	return &types.Request{
		Features:   []*types.Feature{{Type: types.FEAT_LOG, Field: "q_2"}},
		Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "imp", Expression: "q_1 + q_2"}, {Type: types.ATTR_ZSCORE, Field: "q_3"}},
		Windows:    []*types.Window{{Type: types.WIN_LAG, Field: "q_1", Label: "lag"}},
	}
}

// TestComputedColumns: features, attributes (label or default) and —
// only when windows run over the record rows — window labels; a label
// equal to a schema field is the field's; an ATTR_PREVIOUS_STATE over
// a categorical keeps the state field's type.
func TestComputedColumns(t *testing.T) {
	s := testSchema()
	got := ComputedColumns(computedRequest(), s, nil)
	want := Computed{
		"LOG_q_2":         {Slot: "feature", Index: 0, Type: encoding.FieldTypeF64},
		"imp":             {Slot: "attribute", Index: 0, Type: encoding.FieldTypeF64},
		"ATTR_ZSCORE_q_3": {Slot: "attribute", Index: 1, Type: encoding.FieldTypeF64},
		"lag":             {Slot: "window", Index: 0, Type: encoding.FieldTypeF64},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ComputedColumns = %+v\nwant %+v", got, want)
	}
	grouped := computedRequest()
	grouped.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "q_1"}}
	if _, ok := ComputedColumns(grouped, s, nil)["lag"]; ok {
		t.Error("a window over aggregated rows is no record column")
	}
	shadow := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "q_1", Expression: "q_2"}}}
	if c := ComputedColumns(shadow, s, nil); c != nil {
		t.Errorf("a label equal to a schema field is computed: %+v", c)
	}
	if ComputedColumns(&types.Request{}, s, nil) != nil || ComputedColumns(nil, s, nil) != nil {
		t.Error("a request computing nothing has computed columns")
	}
	prev := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "region", Label: "was"}}}
	if c := ComputedColumns(prev, s, nil)["was"]; c.Type != encoding.FieldTypeCategoricalU8 {
		t.Errorf("previous-state column type %v, want the state field's", c.Type)
	}
}

// TestResolveRequest_ComputedMembers: a literal entry naming a computed
// column is a member (Resolved.Computed lists it); a glob never matches
// one; an unknown literal is still SERVICE_VALIDATION; a categorical
// computed column is PULSE_VECTOR_MEMBER_TYPE; the computed-free shape
// is unchanged.
func TestResolveRequest_ComputedMembers(t *testing.T) {
	s := testSchema()
	req := computedRequest()
	req.Vectors = []types.VectorSpec{
		{Name: "v", Fields: []string{"q_1", "imp", "LOG_q_2", "lag"}},
		{Name: "g", Fields: []string{"q_*", "im*"}},
		{Name: "plain", Fields: []string{"q_1", "q_2"}},
	}
	got, err := ResolveRequest(req, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[0].Members, []string{"q_1", "imp", "LOG_q_2", "lag"}) || !slices.Equal(got[0].Computed, []string{"imp", "LOG_q_2", "lag"}) {
		t.Errorf("v = %+v", got[0])
	}
	if got[0].Refs != nil || !slices.Equal(got[0].Labels, got[0].Members) {
		t.Errorf("computed members carry refs / labels: %+v", got[0])
	}
	if !slices.Equal(got[1].Members, []string{"q_3", "q_1", "q_2"}) || got[1].Computed != nil {
		t.Errorf("glob matched a computed column: %+v", got[1])
	}
	if got[2].Computed != nil {
		t.Errorf("plain vector: %+v", got[2])
	}
	if _, err := Resolve(req.Vectors[:1], s); err == nil || err.Code != errors.SERVICE_VALIDATION {
		t.Errorf("Resolve without the request admits a computed member: %v", err)
	}
	req.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "nosuch"}}}
	if _, err := ResolveRequest(req, s, nil); err == nil || err.Code != errors.SERVICE_VALIDATION || err.Details["field"] != "nosuch" {
		t.Errorf("unknown literal: %v", err)
	}
	prev := &types.Request{
		Attributes: []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "region", Label: "was"}},
		Vectors:    []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "was"}}},
	}
	if _, err := ResolveRequest(prev, s, nil); err == nil || err.Code != errors.PULSE_VECTOR_MEMBER_TYPE || err.Details["field"] != "was" {
		t.Errorf("categorical computed member: %v", err)
	}
	// Members (projection) resolves the computed vector and adds no
	// computed name as a source field it could not decode.
	req = computedRequest()
	req.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "imp"}}}
	if m, ok := Members(req, s, nil); !ok || !slices.Equal(m, []string{"q_1", "imp"}) {
		t.Errorf("Members = %v, %v", m, ok)
	}
}

// TestResolveMatrices_ComputedMembers: a matrix reads computed members
// by vector, inline fields and params.control; MatrixComputedMembers
// lists them in first-read order.
func TestResolveMatrices_ComputedMembers(t *testing.T) {
	s := testSchema()
	req := computedRequest()
	req.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "imp", "q_2"}}}
	req.Matrices = []types.MatrixSpec{
		{Name: "a", Type: types.MAT_COVARIANCE, Vector: "v"},
		{Name: "b", Type: types.MAT_COVARIANCE, Fields: []string{"lag", "q_1"}},
		{Name: "c", Type: types.MAT_PARTIAL_CORRELATION, Fields: []string{"q_1", "q_2"}, Params: json.RawMessage(`{"control": ["LOG_q_2"]}`)},
	}
	mats, err := ResolveMatrices(req, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(mats[0].Members.Computed, []string{"imp"}) || !slices.Equal(mats[1].Members.Computed, []string{"lag"}) ||
		!slices.Equal(mats[2].Extra, []string{"LOG_q_2"}) {
		t.Errorf("matrices = %+v", mats)
	}
	if got := MatrixComputedMembers(req, s, nil); !slices.Equal(got, []string{"imp", "lag", "LOG_q_2"}) {
		t.Errorf("MatrixComputedMembers = %v", got)
	}
	if m, ok := MatrixMembers(req, s, nil); !ok || slices.Contains(m, "LOG_q_2") {
		t.Errorf("MatrixMembers = %v, %v", m, ok)
	}
}

// TestComputedRefusal: every non-matrix consumer of a vector with a
// computed member is PULSE_VECTOR_INVALID computed_member_unsupported
// naming the slot and the member, judged even on a matrix-free request.
func TestComputedRefusal(t *testing.T) {
	s := testSchema()
	for _, c := range []struct {
		edit func(*types.Request)
		slot string
	}{
		{func(r *types.Request) { r.Tests = []*types.Test{{Type: types.TEST_HOTELLING_T2, Vector: "v"}} }, "tests[0].vector"},
		{func(r *types.Request) {
			r.Attributes = append(r.Attributes, &types.Attribute{Type: types.ATTR_SCALE_SCORE, Vector: "v", Label: "s"})
		}, "attributes[2].vector"},
		{func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_VEC_MEAN, Vector: "v", Label: "m"}}
		}, "aggregations[0].vector"},
		{func(r *types.Request) {
			r.Groups = []*types.Group{{Type: types.GROUP_KMEANS, Vector: "v", Params: json.RawMessage(`{"k": 2}`)}}
		}, "groups[0].vector"},
		{func(r *types.Request) {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "len(v) > 1"}}
		}, "filterers[0].expression"},
	} {
		req := computedRequest()
		req.Windows = nil
		req.Vectors = []types.VectorSpec{{Name: "plain", Fields: []string{"q_1"}}, {Name: "v", Fields: []string{"q_1", "imp"}}}
		c.edit(req)
		_, err := ResolveMatrices(req, s, nil)
		if err == nil || err.Code != errors.PULSE_VECTOR_INVALID || err.Details["reason"] != "computed_member_unsupported" ||
			err.Details["slot"] != c.slot || err.Details["member"] != "imp" || err.Details["vector"] != "vectors[1]" {
			t.Errorf("%s: %v", c.slot, err)
		}
	}
	// A matrix reads it.
	req := computedRequest()
	req.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "imp"}}}
	req.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Vector: "v"}}
	if _, err := ResolveMatrices(req, s, nil); err != nil {
		t.Errorf("matrix consumer refused: %v", err)
	}
}

// TestFeatureOutputLabels: the shared naming (singles, the date and
// polynomial families).
func TestFeatureOutputLabels(t *testing.T) {
	if got := FeatureOutputLabels(&types.Feature{Type: types.FEAT_SQRT, Field: "x"}, nil); !slices.Equal(got, []string{"SQRT_x"}) {
		t.Errorf("sqrt: %v", got)
	}
	if got := FeatureOutputLabels(&types.Feature{Type: types.FEAT_POLY, Field: "x", Params: json.RawMessage(`{"degree": 3}`)}, nil); !slices.Equal(got, []string{"x_poly_2", "x_poly_3"}) {
		t.Errorf("poly: %v", got)
	}
	if got := FeatureOutputLabels(&types.Feature{Type: "FEAT_NOSUCH", Field: "x"}, nil); got != nil {
		t.Errorf("unknown: %v", got)
	}
	if got := WindowLabel(&types.Window{Type: types.WIN_ROW_NUMBER}); got != "WIN_ROW_NUMBER" {
		t.Errorf("window label %q", got)
	}
}
