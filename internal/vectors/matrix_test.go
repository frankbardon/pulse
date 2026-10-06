package vectors

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestResolveMatrices_Resolves(t *testing.T) {
	req := &types.Request{
		Vectors: []types.VectorSpec{{Name: "qs", Fields: []string{"q_*"}, Labels: []string{"Three", "One", "Two"}}},
		Matrices: []types.MatrixSpec{
			{Type: types.MAT_COVARIANCE, Vector: "qs"},
			{Type: types.MAT_COVARIANCE, Name: "inline", Fields: []string{"score", "mid"}, Params: json.RawMessage(`{"ddof": 0}`), Encoding: types.MatrixEncodingUpper},
			{Type: types.MAT_CORRELATION, Vector: "qs", Params: json.RawMessage(`{}`)},
		},
	}
	got, err := ResolveMatrices(req, testSchema(), nil)
	if err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d matrices", len(got))
	}
	if c := got[2]; c.Name != "MAT_CORRELATION_qs" || c.Type != types.MAT_CORRELATION || c.Index != 2 {
		t.Errorf("correlation spec = %+v", c)
	}
	a, b := got[0], got[1]
	if a.Name != "MAT_COVARIANCE_qs" || a.DDOF != 1 || a.Encoding != types.MatrixEncodingFull || !a.ExplicitLabels {
		t.Errorf("vector spec = %+v", a)
	}
	if !slices.Equal(a.Members.Members, []string{"q_3", "q_1", "q_2"}) || !slices.Equal(a.Members.Labels, []string{"Three", "One", "Two"}) {
		t.Errorf("vector members / labels = %v / %v", a.Members.Members, a.Members.Labels)
	}
	if b.Name != "inline" || b.DDOF != 0 || b.Encoding != types.MatrixEncodingUpper || b.ExplicitLabels || b.Index != 1 {
		t.Errorf("inline spec = %+v", b)
	}
	if !slices.Equal(b.Members.Members, []string{"score", "mid"}) {
		t.Errorf("inline members = %v", b.Members.Members)
	}
}

func TestResolveMatrices_Refusals(t *testing.T) {
	vec := []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "q_2"}}}
	cov := types.MAT_COVARIANCE
	cases := []struct {
		name   string
		specs  []types.MatrixSpec
		vecs   []types.VectorSpec
		known  func(types.MatrixType) bool
		code   errors.Code
		reason string
	}{
		{"unknown type", []types.MatrixSpec{{Type: "MAT_NOPE", Vector: "v"}}, vec, nil, errors.SERVICE_VALIDATION, "unknown_type"},
		{"hidden type", []types.MatrixSpec{{Type: cov, Vector: "v"}}, vec, func(types.MatrixType) bool { return false }, errors.SERVICE_VALIDATION, "unknown_type"},
		{"vector and fields", []types.MatrixSpec{{Type: cov, Vector: "v", Fields: []string{"q_1"}}}, vec, nil, errors.SERVICE_VALIDATION, "vector_and_fields"},
		{"neither", []types.MatrixSpec{{Type: cov}}, vec, nil, errors.SERVICE_VALIDATION, "no_vector_or_fields"},
		{"unknown encoding", []types.MatrixSpec{{Type: cov, Vector: "v", Encoding: "lower"}}, vec, nil, errors.SERVICE_VALIDATION, "unknown_encoding"},
		{"duplicate name", []types.MatrixSpec{{Type: cov, Vector: "v"}, {Type: cov, Vector: "v"}}, vec, nil, errors.SERVICE_VALIDATION, "duplicate_name"},
		{"ddof out of range", []types.MatrixSpec{{Type: cov, Vector: "v", Params: json.RawMessage(`{"ddof": 2}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"unknown param", []types.MatrixSpec{{Type: cov, Vector: "v", Params: json.RawMessage(`{"dof": 1}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"correlation takes no ddof", []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "v", Params: json.RawMessage(`{"ddof": 1}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"unknown missing mode", []types.MatrixSpec{{Type: cov, Vector: "v", Params: json.RawMessage(`{"missing": "casewise"}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"max_drop_share above 1", []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "v", Params: json.RawMessage(`{"max_drop_share": 1.5}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"max_drop_share negative", []types.MatrixSpec{{Type: cov, Vector: "v", Params: json.RawMessage(`{"max_drop_share": -0.1}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"max_drop_share under pairwise", []types.MatrixSpec{{Type: cov, Vector: "v", Params: json.RawMessage(`{"missing": "pairwise", "max_drop_share": 0.2}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"top_pairs on covariance", []types.MatrixSpec{{Type: cov, Vector: "v", Params: json.RawMessage(`{"summary": {"top_pairs": 3}}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"top_pairs zero", []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "v", Params: json.RawMessage(`{"summary": {"top_pairs": 0}}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"top_pairs negative", []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "v", Params: json.RawMessage(`{"summary": {"top_pairs": -2}}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"top_pairs fractional", []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "v", Params: json.RawMessage(`{"summary": {"top_pairs": 2.5}}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"summary without top_pairs", []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "v", Params: json.RawMessage(`{"summary": {}}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"unknown summary key", []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "v", Params: json.RawMessage(`{"summary": {"top_pairs": 1, "bottom_pairs": 1}}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"undefined vector", []types.MatrixSpec{{Type: cov, Vector: "w"}}, vec, nil, errors.PULSE_VECTOR_UNKNOWN, ""},
		{"inline categorical member", []types.MatrixSpec{{Type: cov, Fields: []string{"region"}}}, nil, nil, errors.PULSE_VECTOR_MEMBER_TYPE, ""},
		{"vector refusal wins first", []types.MatrixSpec{{Type: "MAT_NOPE", Vector: "v"}}, []types.VectorSpec{{Name: "v", Pattern: "^nothing$"}}, nil, errors.PULSE_VECTOR_EMPTY, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveMatrices(&types.Request{Vectors: c.vecs, Matrices: c.specs}, testSchema(), c.known)
			if err == nil || err.Code != c.code {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if c.reason != "" && err.Details["reason"] != c.reason {
				t.Errorf("reason = %v, want %s", err.Details["reason"], c.reason)
			}
			if c.name == "top_pairs on covariance" && err.Details["param"] != "summary" {
				t.Errorf("details = %v, want param summary", err.Details)
			}
			if c.code == errors.PULSE_VECTOR_UNKNOWN {
				if err.Details["vector"] != "w" || err.Details["slot"] != "matrices[0].vector" || !slices.Equal(err.Details["defined"].([]string), []string{"v"}) {
					t.Errorf("details = %v", err.Details)
				}
			}
		})
	}
}

// TestResolveMatrices_MissingParams: params.missing and
// params.max_drop_share decode on both operators; listwise is the
// default and max_drop_share has none.
func TestResolveMatrices_MissingParams(t *testing.T) {
	cases := []struct {
		typ      types.MatrixType
		params   string
		pairwise bool
		share    float64 // -1 = unset
		ddof     int
	}{
		{types.MAT_COVARIANCE, ``, false, -1, 1},
		{types.MAT_CORRELATION, ``, false, -1, 0},
		{types.MAT_COVARIANCE, `{"missing": "pairwise"}`, true, -1, 1},
		{types.MAT_CORRELATION, `{"missing": "pairwise"}`, true, -1, 0},
		{types.MAT_COVARIANCE, `{"missing": "listwise", "max_drop_share": 0.25, "ddof": 0}`, false, 0.25, 0},
		{types.MAT_CORRELATION, `{"max_drop_share": 0}`, false, 0, 0},
	}
	for _, c := range cases {
		t.Run(string(c.typ)+c.params, func(t *testing.T) {
			req := &types.Request{Matrices: []types.MatrixSpec{{Type: c.typ, Fields: []string{"q_1", "q_2"}, Params: json.RawMessage(c.params)}}}
			got, err := ResolveMatrices(req, testSchema(), nil)
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			m := got[0]
			if m.Pairwise != c.pairwise || m.DDOF != c.ddof {
				t.Errorf("pairwise=%v ddof=%d, want %v %d", m.Pairwise, m.DDOF, c.pairwise, c.ddof)
			}
			switch {
			case c.share < 0 && m.MaxDropShare != nil:
				t.Errorf("max_drop_share = %v, want unset", *m.MaxDropShare)
			case c.share >= 0 && (m.MaxDropShare == nil || *m.MaxDropShare != c.share):
				t.Errorf("max_drop_share = %v, want %v", m.MaxDropShare, c.share)
			}
		})
	}
}

// TestReferenced_CountsMatrixVectors: a vector a matrix spec names is
// referenced; any other is reported unreferenced.
func TestReferenced_CountsMatrixVectors(t *testing.T) {
	req := &types.Request{
		Vectors:  []types.VectorSpec{{Name: "a", Fields: []string{"q_1"}}, {Name: "b", Fields: []string{"q_2"}}},
		Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Vector: "b"}},
	}
	if !Referenced(req)["b"] || Referenced(req)["a"] {
		t.Errorf("Referenced = %v, want {b}", Referenced(req))
	}
	if got := Unreferenced(req); !slices.Equal(got, []string{"a"}) {
		t.Errorf("Unreferenced = %v, want [a]", got)
	}
}

// TestMatrixMembers_InlineFields: projection sees an inline spec's
// resolved members (a vector spec's ride Members).
func TestMatrixMembers_InlineFields(t *testing.T) {
	req := &types.Request{Matrices: []types.MatrixSpec{
		{Type: types.MAT_COVARIANCE, Fields: []string{"q_*"}},
		{Type: types.MAT_COVARIANCE, Vector: "v"},
	}}
	got, ok := MatrixMembers(req, testSchema())
	if !ok || !slices.Equal(got, []string{"q_3", "q_1", "q_2"}) {
		t.Errorf("MatrixMembers = %v %v", got, ok)
	}
	req.Matrices[0].Fields = []string{"nope"}
	if _, ok := MatrixMembers(req, testSchema()); ok {
		t.Error("an unresolvable inline spec reports ok")
	}
}

// TestResolveMatrices_TopPairs: params.summary.top_pairs decodes onto
// MAT_CORRELATION's plan; absent (or a null summary on either operator)
// leaves it 0.
func TestResolveMatrices_TopPairs(t *testing.T) {
	cases := []struct {
		typ    types.MatrixType
		params string
		want   int
	}{
		{types.MAT_CORRELATION, ``, 0},
		{types.MAT_CORRELATION, `{"summary": {"top_pairs": 1}}`, 1},
		{types.MAT_CORRELATION, `{"missing": "pairwise", "summary": {"top_pairs": 50}}`, 50},
		{types.MAT_CORRELATION, `{"summary": null}`, 0},
		{types.MAT_COVARIANCE, `{"summary": null}`, 0},
	}
	for _, c := range cases {
		t.Run(string(c.typ)+c.params, func(t *testing.T) {
			req := &types.Request{Matrices: []types.MatrixSpec{{Type: c.typ, Fields: []string{"q_1", "q_2"}, Params: json.RawMessage(c.params)}}}
			got, err := ResolveMatrices(req, testSchema(), nil)
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if got[0].TopPairs != c.want {
				t.Errorf("TopPairs = %d, want %d", got[0].TopPairs, c.want)
			}
		})
	}
}
