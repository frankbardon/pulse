package vectors

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
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
		{"unknown method", []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "v", Params: json.RawMessage(`{"method": "biserial"}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
		{"method on covariance", []types.MatrixSpec{{Type: cov, Vector: "v", Params: json.RawMessage(`{"method": "spearman"}`)}}, vec, nil, errors.SERVICE_VALIDATION, "bad_params"},
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

// TestResolveMatrices_Method: MAT_CORRELATION params.method resolves to
// pearson (default, streamable, mergeable) or a rank method (buffered,
// not mergeable — the spec-level MatrixSpec answers copied through);
// SpecMethod reads the same value off the raw spec.
func TestResolveMatrices_Method(t *testing.T) {
	cases := []struct {
		params    string
		method    string
		buffered  bool
		rankClass bool
	}{
		{``, CorrelationPearson, false, false},
		{`{}`, CorrelationPearson, false, false},
		{`{"method": "pearson"}`, CorrelationPearson, false, false},
		{`{"method": "spearman", "missing": "pairwise"}`, CorrelationSpearman, true, true},
		{`{"method": "kendall", "summary": {"top_pairs": 2}}`, CorrelationKendall, true, true},
	}
	for _, c := range cases {
		spec := types.MatrixSpec{Type: types.MAT_CORRELATION, Fields: []string{"score", "mid"}, Params: json.RawMessage(c.params)}
		got, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{spec}}, testSchema(), nil)
		if err != nil {
			t.Fatalf("%s: unexpected refusal: %v", c.params, err)
		}
		m := got[0]
		if m.Method != c.method || m.Streamable == c.buffered || m.Mergeable == c.buffered {
			t.Errorf("%s: method %q streamable %v mergeable %v, want %q buffered %v", c.params, m.Method, m.Streamable, m.Mergeable, c.method, c.buffered)
		}
		if IsRankMethod(m.Method) != c.rankClass || SpecMethod(spec) != c.method {
			t.Errorf("%s: IsRankMethod %v SpecMethod %q", c.params, IsRankMethod(m.Method), SpecMethod(spec))
		}
		if wantRow := c.buffered; (m.RowBytes() > 0) != wantRow {
			t.Errorf("%s: RowBytes = %d", c.params, m.RowBytes())
		} else if wantRow && m.RowBytes() != 8*3 {
			t.Errorf("%s: RowBytes = %d, want 8·(p + 1) = 24", c.params, m.RowBytes())
		}
	}
	if SpecMethod(types.MatrixSpec{Type: types.MAT_COVARIANCE}) != "" {
		t.Error("SpecMethod on MAT_COVARIANCE must be empty")
	}
}

// TestResolveMatrices_PartialControls: MAT_PARTIAL_CORRELATION's
// params.control — "all" / absent / null control for every member; a
// list may name members (they leave the output axis) and other numeric
// fields (they join the fold after the members, as Extra), each
// resolved by the vector rules; params.repair accepts "nearest" only.
func TestResolveMatrices_PartialControls(t *testing.T) {
	resolve := func(params string) (Matrix, error) {
		spec := types.MatrixSpec{Type: types.MAT_PARTIAL_CORRELATION, Fields: []string{"q_1", "q_2", "q_3"}}
		if params != "" {
			spec.Params = json.RawMessage(params)
		}
		got, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{spec}}, testSchema(), nil)
		if err != nil {
			return Matrix{}, err
		}
		return got[0], nil
	}
	for _, params := range []string{"", `{"control": "all"}`, `{"control": null}`} {
		m, err := resolve(params)
		if err != nil {
			t.Fatalf("%s: %v", params, err)
		}
		if m.Controls != nil || m.Output != nil || m.Extra != nil || !reflect.DeepEqual(m.Columns(), []string{"q_1", "q_2", "q_3"}) {
			t.Errorf("%s: controls %v output %v extra %v", params, m.Controls, m.Output, m.Extra)
		}
		if axis, _ := m.OutputMembers(); !reflect.DeepEqual(axis, []string{"q_1", "q_2", "q_3"}) {
			t.Errorf("%s: axis %v", params, axis)
		}
	}
	m, err := resolve(`{"control": ["score", "q_2", "mid"], "repair": "nearest", "missing": "pairwise"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Columns(), []string{"q_1", "q_2", "q_3", "score", "mid"}) || !reflect.DeepEqual(m.Output, []int{0, 2}) {
		t.Errorf("columns %v output %v", m.Columns(), m.Output)
	}
	if axis, _ := m.OutputMembers(); !reflect.DeepEqual(axis, []string{"q_1", "q_3"}) {
		t.Errorf("axis %v", axis)
	}
	if m.Repair != RepairNearest || !m.Pairwise || !m.Decomposition() || !m.PSDRisk() {
		t.Errorf("repair %q pairwise %v decomposition %v risk %v", m.Repair, m.Pairwise, m.Decomposition(), m.PSDRisk())
	}
	for params, want := range map[string]errors.Code{
		`{"control": []}`:                     errors.SERVICE_VALIDATION,
		`{"control": "none"}`:                 errors.SERVICE_VALIDATION,
		`{"control": 3}`:                      errors.SERVICE_VALIDATION,
		`{"control": ["q_1", "q_1"]}`:         errors.SERVICE_VALIDATION,
		`{"control": ["q_*"]}`:                errors.SERVICE_VALIDATION,
		`{"control": ["q_1", "q_2", "q_3"]}`:  errors.SERVICE_VALIDATION,
		`{"control": ["nope"]}`:               errors.SERVICE_VALIDATION,
		`{"control": ["region"]}`:             errors.PULSE_VECTOR_MEMBER_TYPE,
		`{"repair": "clip"}`:                  errors.SERVICE_VALIDATION,
		`{"summary": {"top_pairs": 1}}`:       errors.SERVICE_VALIDATION,
		`{"control": ["mid"], "method": "x"}`: errors.SERVICE_VALIDATION,
	} {
		_, err := resolve(params)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != want {
			t.Errorf("%s: error %v, want %s", params, err, want)
		}
	}
	// An unknown outside control names its own params.control entry.
	_, cerr := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_PARTIAL_CORRELATION, Fields: []string{"q_1", "q_2"},
		Params: json.RawMessage(`{"control": ["q_1", "score", "nope"]}`)}}}, testSchema(), nil)
	if cerr == nil || cerr.Details["slot"] != "matrices[0].params.control[2]" || cerr.Details["field"] != "nope" {
		t.Errorf("unknown control refusal = %v", cerr)
	}
	// MAT_CORRELATION does not take repair or control.
	for _, params := range []string{`{"repair": "nearest"}`, `{"control": "all"}`} {
		_, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Fields: []string{"q_1", "q_2"}, Params: json.RawMessage(params)}}}, testSchema(), nil)
		if err == nil || err.Details["reason"] != "bad_params" {
			t.Errorf("MAT_CORRELATION %s: %v, want bad_params", params, err)
		}
	}
	// Projection reads the outside controls.
	req := &types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_PARTIAL_CORRELATION, Fields: []string{"q_1", "q_2"},
		Params: json.RawMessage(`{"control": ["score"]}`)}}}
	if got, ok := MatrixMembers(req, testSchema()); !ok || !reflect.DeepEqual(got, []string{"score", "q_1", "q_2"}) {
		t.Errorf("MatrixMembers = %v, %v", got, ok)
	}
	req.Matrices[0].Params = json.RawMessage(`{"control": ["nope"]}`)
	if _, ok := MatrixMembers(req, testSchema()); ok {
		t.Error("MatrixMembers ok with an unknown control")
	}
}

// TestResolveMatrices_Reliability: MAT_RELIABILITY places params.reverse
// on member positions (ascending, whatever the list order), keeps the
// declared range, refuses a missing / half / inverted range with
// PROCESSING_CONFIG, and a non-member reverse name or a one-item
// battery with bad_params; it is a decomposition operator whose PSD
// risk starts at 3 pairwise items.
func TestResolveMatrices_Reliability(t *testing.T) {
	resolve := func(fields []string, params string) (Matrix, *errors.CodedError) {
		spec := types.MatrixSpec{Type: types.MAT_RELIABILITY, Fields: fields}
		if params != "" {
			spec.Params = json.RawMessage(params)
		}
		got, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{spec}}, testSchema(), nil)
		if err != nil {
			return Matrix{}, err
		}
		return got[0], nil
	}
	items := []string{"q_1", "q_2", "q_3"}
	m, err := resolve(items, `{"reverse": ["q_3", "q_1"], "scale_min": 1, "scale_max": 5, "missing": "pairwise"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Reverse, []int{0, 2}) || !m.HasScale || m.ScaleMin != 1 || m.ScaleMax != 5 || m.reverseNames != nil {
		t.Errorf("reverse %v scale %v [%v, %v] names %v", m.Reverse, m.HasScale, m.ScaleMin, m.ScaleMax, m.reverseNames)
	}
	if !m.Decomposition() || !m.PSDRisk() || !m.Streamable || !m.Mergeable {
		t.Errorf("decomposition %v psd risk %v streamable %v mergeable %v", m.Decomposition(), m.PSDRisk(), m.Streamable, m.Mergeable)
	}
	if m, _ := resolve(items[:2], `{"missing": "pairwise"}`); m.PSDRisk() {
		t.Error("a 2-item pairwise battery carries PSD risk")
	}
	if m, _ := resolve(items, ""); m.Reverse != nil || m.HasScale {
		t.Errorf("no params: reverse %v scale %v", m.Reverse, m.HasScale)
	}
	for _, c := range []struct {
		fields []string
		params string
		code   errors.Code
	}{
		{items, `{"reverse": ["q_1"]}`, errors.PROCESSING_CONFIG},
		{items, `{"reverse": ["q_1"], "scale_max": 5}`, errors.PROCESSING_CONFIG},
		{items, `{"scale_min": 5, "scale_max": 5}`, errors.PROCESSING_CONFIG},
		{items, `{"reverse": ["score"], "scale_min": 1, "scale_max": 5}`, errors.SERVICE_VALIDATION},
		{items, `{"reverse": ["q_1", "q_1"], "scale_min": 1, "scale_max": 5}`, errors.SERVICE_VALIDATION},
		{items, `{"repair": "clip"}`, errors.SERVICE_VALIDATION},
		{items, `{"control": "all"}`, errors.SERVICE_VALIDATION},
		{items[:1], "", errors.SERVICE_VALIDATION},
	} {
		if _, err := resolve(c.fields, c.params); err == nil || err.Code != c.code {
			t.Errorf("%v %s: error %v, want %s", c.fields, c.params, err, c.code)
		}
	}
}

// TestResolveMatrices_PCA: MAT_PCA's params.on (correlation default),
// params.components (kaiser default on a correlation; an integer ≤ p;
// {"variance": share}) and their refusals — covariance needs an
// explicit k or share and refuses kaiser; every refusal bad_params. A
// decomposition operator: pairwise PSD risk from 3 members on a
// correlation, from 2 on a covariance.
func TestResolveMatrices_PCA(t *testing.T) {
	resolve := func(fields []string, params string) (Matrix, *errors.CodedError) {
		spec := types.MatrixSpec{Type: types.MAT_PCA, Fields: fields}
		if params != "" {
			spec.Params = json.RawMessage(params)
		}
		got, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{spec}}, testSchema(), nil)
		if err != nil {
			return Matrix{}, err
		}
		return got[0], nil
	}
	items := []string{"q_1", "q_2", "q_3"}
	for _, c := range []struct {
		params string
		on     string
		want   PCAComponents
	}{
		{"", PCAOnCorrelation, PCAComponents{Rule: PCAComponentsKaiser}},
		{`{"components": "kaiser"}`, PCAOnCorrelation, PCAComponents{Rule: PCAComponentsKaiser}},
		{`{"components": 2}`, PCAOnCorrelation, PCAComponents{Rule: PCAComponentsFixed, K: 2}},
		{`{"on": "covariance", "components": 3}`, PCAOnCovariance, PCAComponents{Rule: PCAComponentsFixed, K: 3}},
		{`{"on": "covariance", "components": {"variance": 0.8}}`, PCAOnCovariance, PCAComponents{Rule: PCAComponentsVariance, Share: 0.8}},
	} {
		m, err := resolve(items, c.params)
		if err != nil {
			t.Fatalf("%s: %v", c.params, err)
		}
		if m.On != c.on || m.Components != c.want {
			t.Errorf("%s: on %q components %+v, want %q %+v", c.params, m.On, m.Components, c.on, c.want)
		}
		if !m.Decomposition() || !m.Streamable || !m.Mergeable {
			t.Errorf("%s: decomposition %v streamable %v mergeable %v", c.params, m.Decomposition(), m.Streamable, m.Mergeable)
		}
	}
	if m, _ := resolve(items, `{"missing": "pairwise"}`); !m.PSDRisk() {
		t.Error("3-member pairwise correlation PCA carries no PSD risk")
	}
	if m, _ := resolve(items[:2], `{"missing": "pairwise"}`); m.PSDRisk() {
		t.Error("2-member pairwise correlation PCA carries PSD risk")
	}
	if m, _ := resolve(items[:2], `{"missing": "pairwise", "on": "covariance", "components": 1}`); !m.PSDRisk() {
		t.Error("2-member pairwise covariance PCA carries no PSD risk")
	}
	for _, params := range []string{
		`{"on": "covariance"}`,
		`{"on": "covariance", "components": "kaiser"}`,
		`{"on": "rank"}`,
		`{"components": 0}`,
		`{"components": -1}`,
		`{"components": 1.5}`,
		`{"components": 4}`,
		`{"components": "two"}`,
		`{"components": {"variance": 0}}`,
		`{"components": {"variance": 1.01}}`,
		`{"components": {"variance": 0.5, "k": 1}}`,
		`{"components": {}}`,
		`{"components": [2]}`,
		`{"repair": "clip"}`,
		`{"reverse": ["q_1"]}`,
	} {
		if _, err := resolve(items, params); err == nil || err.Code != errors.SERVICE_VALIDATION || err.Details["reason"] != "bad_params" {
			t.Errorf("%s: error %v, want bad_params", params, err)
		}
	}
}
