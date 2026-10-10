package vectors

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

func bucketSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatalf("dict.Add: %v", err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("a", "b", "c")},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict("T0", "T1", "T2", "T3", "T4")},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "d", Type: encoding.FieldTypeDate},
	}}
}

// TestEstimateBuckets: the schema-only bucket bound predict reports per
// grouped matrix, per grouper and field type.
func TestEstimateBuckets(t *testing.T) {
	schema := bucketSchema(t)
	cases := []struct {
		name    string
		groups  []*types.Group
		buckets int64
		basis   string
		known   bool
	}{
		{"ungrouped", nil, 1, BucketBasisUngrouped, true},
		{"category over a dictionary", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}, 3, BucketBasisDictionary, true},
		{"category over packed_bool", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "flag"}}, 2, BucketBasisBoolean, true},
		{"category over a number", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "x"}}, 0, BucketBasisUnknown, false},
		{"category include, unreachable and repeated keys dropped", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region", Include: []string{"c", "zz", "a", "c"}}}, 2, BucketBasisInclude, true},
		{"packed_bool include", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "flag", Include: []string{"1", "true"}}}, 1, BucketBasisInclude, true},
		{"numeric category include bounds the keys", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "x", Include: []string{"1", "2", "2"}}}, 2, BucketBasisInclude, true},
		{"per-element over a set dictionary", []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}}, 5, BucketBasisDictionary, true},
		{"per-element include", []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags", Include: []string{"T4", "T1"}}}, 2, BucketBasisInclude, true},
		{"set value composition", []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "tags"}}, 0, BucketBasisUnknown, false},
		{"set value include", []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "tags", Include: []string{"T0|T1", "T2"}}}, 2, BucketBasisInclude, true},
		{"quantile default bins", []*types.Group{{Type: types.GROUP_QUANTILE, Field: "x"}}, 4, BucketBasisQuantileBins, true},
		{"quantile bins", []*types.Group{{Type: types.GROUP_QUANTILE, Field: "x", Interval: 10}}, 10, BucketBasisQuantileBins, true},
		{"range", []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 5}}, 0, BucketBasisUnknown, false},
		{"range ignores include", []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 5, Include: []string{"0"}}}, 0, BucketBasisUnknown, false},
		{"date", []*types.Group{{Type: types.GROUP_DATE, Field: "d"}}, 0, BucketBasisUnknown, false},
		{"unknown field", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "nope"}}, 0, BucketBasisUnknown, false},
		{"Groups[0] only", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}}, 3, BucketBasisDictionary, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, basis, known := EstimateBuckets(c.groups, schema)
			if b != c.buckets || basis != c.basis || known != c.known {
				t.Errorf("EstimateBuckets = (%d, %q, %v), want (%d, %q, %v)", b, basis, known, c.buckets, c.basis, c.known)
			}
		})
	}
}

// TestPSDRisk_PerType: the NOT_PSD risk is decided per type — pairwise
// covariance from p = 2, pairwise correlation from p = 3, listwise
// never, and a type the rule does not name carries none (an operator
// must opt in, never inherit the catch-all a non-correlation type used
// to fall into).
func TestPSDRisk_PerType(t *testing.T) {
	members := func(p int) Resolved {
		r := Resolved{}
		for i := 0; i < p; i++ {
			r.Members = append(r.Members, "m"+string(rune('a'+i)))
		}
		return r
	}
	cases := []struct {
		name     string
		typ      types.MatrixType
		pairwise bool
		p        int
		want     bool
	}{
		{"listwise covariance", types.MAT_COVARIANCE, false, 4, false},
		{"pairwise covariance p=1", types.MAT_COVARIANCE, true, 1, false},
		{"pairwise covariance p=2", types.MAT_COVARIANCE, true, 2, true},
		{"pairwise correlation p=2", types.MAT_CORRELATION, true, 2, false},
		{"pairwise correlation p=3", types.MAT_CORRELATION, true, 3, true},
		{"listwise correlation", types.MAT_CORRELATION, false, 5, false},
		{"pairwise unnamed type p=3", types.MatrixType("MAT_OTHER"), true, 3, false},
		{"pairwise unnamed type p=8", types.MatrixType("MAT_OTHER"), true, 8, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := Matrix{Type: c.typ, Pairwise: c.pairwise, Members: members(c.p)}
			if got := m.PSDRisk(); got != c.want {
				t.Errorf("PSDRisk = %v, want %v", got, c.want)
			}
		})
	}
}

// TestResolveMatrices_CopiesSpecStreamability: the resolved plan carries
// the spec-level answers, so the engine's slot builder reads the same
// rule as the routing gates.
func TestResolveMatrices_CopiesSpecStreamability(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64},
		{Name: "b", Type: encoding.FieldTypeF64},
	}}
	req := &types.Request{Matrices: []types.MatrixSpec{
		{Type: types.MAT_COVARIANCE, Fields: []string{"a", "b"}},
		{Type: types.MAT_CORRELATION, Fields: []string{"a", "b"}},
	}}
	plans, err := ResolveMatrices(req, schema, nil)
	if err != nil {
		t.Fatalf("ResolveMatrices: %v", err)
	}
	for i, m := range plans {
		spec := req.Matrices[i]
		if m.Streamable != spec.Streamable() || m.Mergeable != spec.Mergeable() {
			t.Errorf("%s: plan (streamable %v, mergeable %v) != spec (%v, %v)",
				m.Name, m.Streamable, m.Mergeable, spec.Streamable(), spec.Mergeable())
		}
		if !m.Streamable || !m.Mergeable {
			t.Errorf("%s: a co-moment operator must resolve streamable and mergeable", m.Name)
		}
	}
}
