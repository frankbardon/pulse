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

// TestResolveMatrices_ReliabilityVectorBounds: without its own
// scale_min / scale_max MAT_RELIABILITY takes its kind "scale" vector's
// declared bounds (so a reverse list needs no params range); explicit
// params win; inline fields and a bound-free vector have none.
func TestResolveMatrices_ReliabilityVectorBounds(t *testing.T) {
	vecs := []types.VectorSpec{
		{Name: "sat", Fields: []string{"q_1", "q_2", "q_3"}, Kind: types.VectorKindScale, ScaleMin: f64(1), ScaleMax: f64(5)},
		{Name: "plain", Fields: []string{"q_1", "q_2", "q_3"}, Kind: types.VectorKindScale},
	}
	resolve := func(spec types.MatrixSpec) (Matrix, *errors.CodedError) {
		spec.Type = types.MAT_RELIABILITY
		got, err := ResolveMatrices(&types.Request{Vectors: vecs, Matrices: []types.MatrixSpec{spec}}, testSchema(), nil)
		if err != nil {
			return Matrix{}, err
		}
		return got[0], nil
	}
	cases := []struct {
		name     string
		spec     types.MatrixSpec
		scale    bool
		lo, hi   float64
		reverse  []int
		code     errors.Code
		refusing bool
	}{
		{"vector bounds stand in", types.MatrixSpec{Vector: "sat"}, true, 1, 5, nil, "", false},
		{"vector bounds feed reverse", types.MatrixSpec{Vector: "sat", Params: json.RawMessage(`{"reverse": ["q_3"]}`)}, true, 1, 5, []int{2}, "", false},
		{"explicit params win", types.MatrixSpec{Vector: "sat", Params: json.RawMessage(`{"scale_min": 0, "scale_max": 10}`)}, true, 0, 10, nil, "", false},
		{"bound-free vector", types.MatrixSpec{Vector: "plain"}, false, 0, 0, nil, "", false},
		{"bound-free vector refuses reverse", types.MatrixSpec{Vector: "plain", Params: json.RawMessage(`{"reverse": ["q_1"]}`)}, false, 0, 0, nil, errors.PROCESSING_CONFIG, true},
		{"inline fields refuse reverse", types.MatrixSpec{Fields: []string{"q_1", "q_2"}, Params: json.RawMessage(`{"reverse": ["q_1"]}`)}, false, 0, 0, nil, errors.PROCESSING_CONFIG, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := resolve(c.spec)
			if c.refusing {
				if err == nil || err.Code != c.code {
					t.Fatalf("err %v, want %s", err, c.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.HasScale != c.scale || m.ScaleMin != c.lo || m.ScaleMax != c.hi || !reflect.DeepEqual(m.Reverse, c.reverse) {
				t.Errorf("scale %v [%v, %v] reverse %v", m.HasScale, m.ScaleMin, m.ScaleMax, m.Reverse)
			}
		})
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

// TestResolveMatrices_Collinearity: MAT_COLLINEARITY's params.center
// (false by default — Belsley uncentered with the intercept), repair
// and the missing-data knobs; every other key (a response included:
// the members are the predictors) is refused bad_params. A
// decomposition operator: pairwise PSD risk from 3 members, the
// correlation rule.
func TestResolveMatrices_Collinearity(t *testing.T) {
	resolve := func(fields []string, params string) (Matrix, *errors.CodedError) {
		spec := types.MatrixSpec{Type: types.MAT_COLLINEARITY, Fields: fields}
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
	for params, center := range map[string]bool{"": false, `{"center": false}`: false, `{"center": true, "repair": "nearest"}`: true} {
		m, err := resolve(items, params)
		if err != nil {
			t.Fatalf("%s: %v", params, err)
		}
		if m.Center != center || !m.Decomposition() || !m.Streamable || !m.Mergeable {
			t.Errorf("%s: center %v decomposition %v streamable %v mergeable %v", params, m.Center, m.Decomposition(), m.Streamable, m.Mergeable)
		}
	}
	if m, _ := resolve(items, `{"missing": "pairwise"}`); !m.PSDRisk() {
		t.Error("3-member pairwise collinearity carries no PSD risk")
	}
	if m, _ := resolve(items[:2], `{"missing": "pairwise"}`); m.PSDRisk() {
		t.Error("2-member pairwise collinearity carries PSD risk")
	}
	if m, _ := resolve(items, ""); m.PSDRisk() {
		t.Error("listwise collinearity carries PSD risk")
	}
	for _, params := range []string{
		`{"center": "yes"}`,
		`{"repair": "clip"}`,
		`{"response": "q_1"}`,
		`{"on": "covariance"}`,
		`{"reverse": ["q_1"]}`,
	} {
		if _, err := resolve(items, params); err == nil || err.Code != errors.SERVICE_VALIDATION || err.Details["reason"] != "bad_params" {
			t.Errorf("%s: error %v, want bad_params", params, err)
		}
	}
}

// TestResolveMatrices_PCARotation: params.rotation decodes an
// orthogonal criterion to Matrix.Rotation (with the geomin_orthogonal
// epsilon, default 0.01), "none" (and absent) to "" — the unrotated
// operator — refuses every OBLIQUE criterion with the distinct
// rotation_oblique_unsupported reason (components are uncorrelated by
// construction) and anything else bad_params.
func TestResolveMatrices_PCARotation(t *testing.T) {
	items := []string{"q_1", "q_2", "q_3"}
	resolve := func(params string) (Matrix, *errors.CodedError) {
		spec := types.MatrixSpec{Type: types.MAT_PCA, Fields: items, Params: json.RawMessage(params)}
		got, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{spec}}, testSchema(), nil)
		if err != nil {
			return Matrix{}, err
		}
		return got[0], nil
	}
	for _, c := range []struct {
		params string
		want   string
		eps    float64
	}{
		{`{}`, "", 0},
		{`{"rotation": "none"}`, "", 0},
		{`{"rotation": "varimax"}`, RotationVarimax, 0},
		{`{"rotation": "varimax", "components": 2}`, RotationVarimax, 0},
		{`{"rotation": "quartimax"}`, RotationQuartimax, 0},
		{`{"rotation": "entropy"}`, RotationEntropy, 0},
		{`{"rotation": "geomin_orthogonal"}`, RotationGeominOrthogonal, GeominDefaultEpsilon},
		{`{"rotation": "geomin_orthogonal", "epsilon": 0.05}`, RotationGeominOrthogonal, 0.05},
	} {
		m, err := resolve(c.params)
		if err != nil {
			t.Fatalf("%s: %v", c.params, err)
		}
		if m.Rotation != c.want || m.Epsilon != c.eps {
			t.Errorf("%s: rotation %q epsilon %v, want %q %v", c.params, m.Rotation, m.Epsilon, c.want, c.eps)
		}
	}
	for _, rot := range ObliqueRotations() {
		params := `{"rotation": "` + rot + `"}`
		_, err := resolve(params)
		if err == nil || err.Code != errors.SERVICE_VALIDATION || err.Details["reason"] != "rotation_oblique_unsupported" ||
			err.Details["oblique"] != true || err.Details["value"] != rot {
			t.Errorf("%s: error %v, want rotation_oblique_unsupported", params, err)
		}
	}
	for _, params := range []string{`{"rotation": "Varimax"}`, `{"rotation": "bentler"}`, `{"rotation": 1}`,
		`{"epsilon": 0.01}`, `{"rotation": "varimax", "epsilon": 0.01}`, `{"rotation": "geomin_orthogonal", "epsilon": 0}`,
		`{"rotation": "geomin_orthogonal", "epsilon": -0.1}`} {
		_, err := resolve(params)
		if err == nil || err.Code != errors.SERVICE_VALIDATION || err.Details["reason"] != "bad_params" {
			t.Errorf("%s: error %v, want bad_params", params, err)
		}
	}
	want := []string{RotationNone, RotationVarimax, RotationQuartimax, RotationGeominOrthogonal, RotationEntropy}
	if got := PCARotations(); !slices.Equal(got, want) {
		t.Errorf("PCARotations() = %v, want %v", got, want)
	}
}

// TestRotationOblique_FollowsGPArotation pins the one classifier
// against each criterion's R reference: stats::varimax and
// GPArotation's quartimax / entropy / geominT are orthogonal;
// stats::promax and GPArotation's oblimin / geominQ oblique.
// Every FactorRotations value is classified, and PCARotations ∪
// ObliqueRotations is FactorRotations.
func TestRotationOblique_FollowsGPArotation(t *testing.T) {
	want := map[string]bool{
		RotationNone: false, RotationVarimax: false, RotationQuartimax: false, RotationEntropy: false, RotationGeominOrthogonal: false,
		RotationPromax: true, RotationOblimin: true, RotationGeomin: true,
	}
	if len(want) != len(FactorRotations()) {
		t.Fatalf("FactorRotations() = %v; classify every value here", FactorRotations())
	}
	for _, r := range FactorRotations() {
		w, ok := want[r]
		if !ok {
			t.Errorf("%q is not classified by this test", r)
		}
		if RotationOblique(r) != w {
			t.Errorf("RotationOblique(%q) = %v, want %v", r, !w, w)
		}
	}
	if n := len(PCARotations()) + len(ObliqueRotations()); n != len(FactorRotations()) {
		t.Errorf("PCARotations %v + ObliqueRotations %v != FactorRotations %v", PCARotations(), ObliqueRotations(), FactorRotations())
	}
	for _, r := range []string{RotationGeomin, RotationGeominOrthogonal} {
		if !RotationTakesEpsilon(r) {
			t.Errorf("%s takes no epsilon", r)
		}
	}
	if RotationTakesEpsilon(RotationOblimin) {
		t.Error("oblimin takes epsilon")
	}
}

// TestResolveMatrices_Factor: MAT_FACTOR's params — factors (an integer
// up to p, or "kaiser", the default), extraction (minres default,
// principal_axis, ml),
// rotation with promax-only kappa (default 4) and oblimin-only gamma,
// ml-only conf_level (default 0.9, strictly inside (0, 1)), repair and the missing-data knobs — and its refusals, every one
// bad_params. A decomposition operator: pairwise PSD risk from 3
// members.
func TestResolveMatrices_Factor(t *testing.T) {
	items := []string{"q_1", "q_2", "q_3", "score", "mid"}
	resolve := func(fields []string, params string) (Matrix, *errors.CodedError) {
		spec := types.MatrixSpec{Type: types.MAT_FACTOR, Fields: fields}
		if params != "" {
			spec.Params = json.RawMessage(params)
		}
		got, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{spec}}, testSchema(), nil)
		if err != nil {
			return Matrix{}, err
		}
		return got[0], nil
	}
	for _, c := range []struct {
		params             string
		k                  int
		extraction, rotate string
		kappa, gamma       float64
	}{
		{"", 0, FactorExtractionMinres, "", 0, 0},
		{`{"factors": "kaiser"}`, 0, FactorExtractionMinres, "", 0, 0},
		{`{"factors": 2, "extraction": "principal_axis"}`, 2, FactorExtractionPrincipalAxis, "", 0, 0},
		{`{"factors": 2, "extraction": "ml", "rotation": "oblimin"}`, 2, FactorExtractionML, RotationOblimin, 0, 0},
		{`{"factors": 2, "rotation": "none"}`, 2, FactorExtractionMinres, "", 0, 0},
		{`{"factors": 2, "rotation": "varimax"}`, 2, FactorExtractionMinres, RotationVarimax, 0, 0},
		{`{"factors": 2, "rotation": "promax"}`, 2, FactorExtractionMinres, RotationPromax, 4, 0},
		{`{"factors": 2, "rotation": "promax", "kappa": 2.5}`, 2, FactorExtractionMinres, RotationPromax, 2.5, 0},
		{`{"factors": 2, "rotation": "oblimin", "gamma": 0.5}`, 2, FactorExtractionMinres, RotationOblimin, 0, 0.5},
		{`{"factors": 2, "rotation": "quartimax"}`, 2, FactorExtractionMinres, RotationQuartimax, 0, 0},
		{`{"factors": 2, "rotation": "entropy"}`, 2, FactorExtractionMinres, RotationEntropy, 0, 0},
		{`{"factors": 2, "rotation": "geomin"}`, 2, FactorExtractionMinres, RotationGeomin, 0, 0},
		{`{"factors": 2, "rotation": "geomin_orthogonal"}`, 2, FactorExtractionMinres, RotationGeominOrthogonal, 0, 0},
		{`{"factors": 5, "repair": "nearest", "missing": "pairwise"}`, 5, FactorExtractionMinres, "", 0, 0},
	} {
		m, err := resolve(items, c.params)
		if err != nil {
			t.Fatalf("%s: %v", c.params, err)
		}
		wantConf := 0.0
		if c.extraction == FactorExtractionML {
			wantConf = FactorDefaultConfLevel
		}
		if m.ConfLevel != wantConf {
			t.Errorf("%s: conf_level %v, want %v", c.params, m.ConfLevel, wantConf)
		}
		if m.Factors.K != c.k || m.Factors.Kaiser() != (c.k == 0) || m.Extraction != c.extraction || m.Rotation != c.rotate || m.Kappa != c.kappa || m.Gamma != c.gamma {
			t.Errorf("%s: factors %+v extraction %q rotation %q kappa %v gamma %v", c.params, m.Factors, m.Extraction, m.Rotation, m.Kappa, m.Gamma)
		}
		if !m.Decomposition() || !m.Streamable || !m.Mergeable {
			t.Errorf("%s: decomposition %v streamable %v mergeable %v", c.params, m.Decomposition(), m.Streamable, m.Mergeable)
		}
	}
	if m, err := resolve(items, `{"factors": 2, "extraction": "ml", "conf_level": 0.95}`); err != nil || m.ConfLevel != 0.95 {
		t.Errorf("conf_level 0.95: %v %v", m.ConfLevel, err)
	}
	if m, _ := resolve(items[:3], `{"missing": "pairwise"}`); !m.PSDRisk() {
		t.Error("3-member pairwise factor analysis carries no PSD risk")
	}
	if m, _ := resolve(items[:2], `{"missing": "pairwise"}`); m.PSDRisk() {
		t.Error("2-member pairwise factor analysis carries PSD risk")
	}
	for _, params := range []string{
		`{"factors": 0}`,
		`{"factors": -1}`,
		`{"factors": 1.5}`,
		`{"factors": 6}`,
		`{"factors": "two"}`,
		`{"factors": [2]}`,
		`{"extraction": "ML"}`,
		`{"extraction": "mle"}`,
		`{"extraction": "pa"}`,
		`{"rotation": "bentler"}`,
		`{"rotation": "geominQ"}`,
		`{"rotation": "Varimax"}`,
		`{"epsilon": 0.01}`,
		`{"rotation": "oblimin", "epsilon": 0.01}`,
		`{"rotation": "geomin", "epsilon": 0}`,
		`{"rotation": "geomin", "epsilon": -1}`,
		`{"rotation": "geomin", "gamma": 0}`,
		`{"kappa": 4}`,
		`{"rotation": "varimax", "kappa": 4}`,
		`{"rotation": "promax", "kappa": 1}`,
		`{"rotation": "promax", "gamma": 0}`,
		`{"gamma": 0}`,
		`{"rotation": "oblimin", "kappa": 4}`,
		`{"repair": "clip"}`,
		`{"conf_level": 0.95}`,
		`{"extraction": "minres", "conf_level": 0.95}`,
		`{"extraction": "ml", "conf_level": 0}`,
		`{"extraction": "ml", "conf_level": 1}`,
		`{"extraction": "ml", "conf_level": 95}`,
		`{"extraction": "ml", "conf_level": -0.1}`,
		`{"components": 2}`,
		`{"on": "covariance"}`,
	} {
		if _, err := resolve(items, params); err == nil || err.Code != errors.SERVICE_VALIDATION || err.Details["reason"] != "bad_params" {
			t.Errorf("%s: error %v, want bad_params", params, err)
		}
	}
	for params, eps := range map[string]float64{
		`{"rotation": "geomin"}`:                          GeominDefaultEpsilon,
		`{"rotation": "geomin", "epsilon": 0.1}`:          0.1,
		`{"rotation": "geomin_orthogonal", "epsilon": 1}`: 1,
		`{"rotation": "oblimin"}`:                         0,
	} {
		if m, err := resolve(items, params); err != nil || m.Epsilon != eps {
			t.Errorf("%s: epsilon %v (%v), want %v", params, m.Epsilon, err, eps)
		}
	}
	if got := FactorRotations(); len(got) != 8 || got[0] != RotationNone {
		t.Errorf("FactorRotations() = %v", got)
	}
	if got := FactorExtractions(); len(got) != 3 || got[0] != FactorExtractionMinres || got[2] != FactorExtractionML {
		t.Errorf("FactorExtractions() = %v", got)
	}
}

// TestFactorIdentified: (p − k)² ≥ p + k, the non-negative model
// degrees of freedom; one factor needs 3 members, two need 5, three 6.
func TestFactorIdentified(t *testing.T) {
	for _, c := range []struct {
		p, k int
		want bool
	}{
		{2, 1, false}, {3, 1, true}, {4, 2, false}, {5, 2, true}, {5, 3, false}, {6, 3, true}, {7, 3, true}, {7, 4, false}, {11, 6, true}, {11, 7, false},
	} {
		if got := FactorIdentified(c.p, c.k); got != c.want {
			t.Errorf("FactorIdentified(%d, %d) = %v, want %v", c.p, c.k, got, c.want)
		}
	}
	for p, want := range map[int]int{1: 0, 2: 0, 3: 1, 4: 1, 5: 2, 6: 3, 7: 3, 11: 6} {
		if got := MaxIdentifiedFactors(p); got != want {
			t.Errorf("MaxIdentifiedFactors(%d) = %d, want %d", p, got, want)
		}
	}
}

// TestResolveMatrices_Inference: params.inference / params.alpha on
// MAT_CORRELATION (every method, rank methods included) and
// MAT_PARTIAL_CORRELATION — off by default (alpha 0), on with alpha
// 0.05 unless set; alpha without inference, alpha outside (0, 1) and
// either key on another operator are bad_params (param named).
func TestResolveMatrices_Inference(t *testing.T) {
	ok := []struct {
		typ       types.MatrixType
		params    string
		inference bool
		alpha     float64
	}{
		{types.MAT_CORRELATION, ``, false, 0},
		{types.MAT_CORRELATION, `{"inference": false}`, false, 0},
		{types.MAT_CORRELATION, `{"inference": true}`, true, InferenceDefaultAlpha},
		{types.MAT_CORRELATION, `{"inference": true, "alpha": 0.1, "method": "pearson"}`, true, 0.1},
		{types.MAT_PARTIAL_CORRELATION, `{"inference": true, "alpha": 0.01}`, true, 0.01},
		{types.MAT_PARTIAL_CORRELATION, `{"inference": null}`, false, 0},
		{types.MAT_CORRELATION, `{"method": "spearman", "inference": false}`, false, 0},
		{types.MAT_CORRELATION, `{"method": "spearman", "inference": true}`, true, InferenceDefaultAlpha},
		{types.MAT_CORRELATION, `{"inference": true, "alpha": 0.2, "method": "kendall"}`, true, 0.2},
	}
	for _, c := range ok {
		t.Run(string(c.typ)+c.params, func(t *testing.T) {
			req := &types.Request{Matrices: []types.MatrixSpec{{Type: c.typ, Fields: []string{"q_1", "q_2", "score"}, Params: json.RawMessage(c.params)}}}
			got, err := ResolveMatrices(req, testSchema(), nil)
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if got[0].Inference != c.inference || got[0].Alpha != c.alpha {
				t.Errorf("Inference, Alpha = %v, %v; want %v, %v", got[0].Inference, got[0].Alpha, c.inference, c.alpha)
			}
		})
	}
	bad := []struct {
		name   string
		typ    types.MatrixType
		params string
		param  string
	}{
		{"alpha without inference", types.MAT_CORRELATION, `{"alpha": 0.1}`, "alpha"},
		{"alpha with inference false", types.MAT_PARTIAL_CORRELATION, `{"inference": false, "alpha": 0.1}`, "alpha"},
		{"alpha 0", types.MAT_CORRELATION, `{"inference": true, "alpha": 0}`, "alpha"},
		{"alpha 1", types.MAT_CORRELATION, `{"inference": true, "alpha": 1}`, "alpha"},
		{"spearman alpha without inference", types.MAT_CORRELATION, `{"method": "spearman", "alpha": 0.1}`, "alpha"},
		{"covariance", types.MAT_COVARIANCE, `{"inference": true}`, ""},
		{"pca", types.MAT_PCA, `{"inference": true}`, ""},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Matrices: []types.MatrixSpec{{Type: c.typ, Fields: []string{"q_1", "q_2", "score"}, Params: json.RawMessage(c.params)}}}
			_, err := ResolveMatrices(req, testSchema(), nil)
			if err == nil {
				t.Fatal("accepted")
			}
			if err.Code != errors.SERVICE_VALIDATION || err.Details["reason"] != "bad_params" {
				t.Fatalf("refusal = %s %v, want SERVICE_VALIDATION bad_params", err.Code, err.Details)
			}
			if c.param != "" && err.Details["param"] != c.param {
				t.Errorf("details.param = %v, want %s (%s)", err.Details["param"], c.param, err.Message)
			}
		})
	}
}
