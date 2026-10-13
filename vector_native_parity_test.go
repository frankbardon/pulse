package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The native-vector parity suite (U29 E2-S2, PRD FR-18): every
// vector-consuming built-in operator gives the SAME response — data,
// tests, matrices, warnings and every Components entry (n, n_null,
// n_listwise_dropped, the weighted floor keys) — on a cohort storing a
// native vec_f64 / vec_f32 field as on its scalar twin (the same values
// as f64 / f32 columns read through a virtual vector), NaN elements and
// null vectors included (a NaN element is a null twin cell, a null
// vector every twin cell null). Each case runs ungrouped (streaming),
// with a buffered-forcing attribute and grouped, over a single file and
// a shard archive.
//
// The one thing a native vector does NOT share with its twin is member
// identity: a native element member is "emb[k]", the twin's the column
// "emb_<label>". vparWire maps the first to the second before comparing
// (every value, count and label must still match byte for byte).
//
// TestNativeVectorParity_CoversEveryVectorOperator enumerates the
// vector-consuming operators from the registry lists, so a new one
// fails until a case here exercises it.

const vparRows = 240

var (
	vparLabels  = []string{"a", "b", "c", "d"}
	vparLabels2 = []string{"emb2_1", "emb2_2", "emb2_3", "emb2_4"}
)

// vparEmb is row r's element k of emb (kind scale, range [0, 10]);
// ok=false is a missing member. vparEmbNull marks a null emb vector.
// Values are multiples of 1/4, exact in float32.
func vparEmb(r, k int) (float64, bool) {
	if (r*3+k)%17 == 4 {
		return 0, false
	}
	return float64((r*7+k*13+(r*r%5)*k)%29) / 4, true
}

func vparEmbNull(r int) bool { return r%13 == 5 }

// vparEmb2 / vparEmb2Null are the same for emb2 (kind measure).
func vparEmb2(r, k int) (float64, bool) {
	if (r+k*5)%19 == 3 {
		return 0, false
	}
	return float64((r*5+k*11+(r/3)%7)%31)/4 + 1, true
}

func vparEmb2Null(r int) bool { return r%11 == 7 }

var (
	vparGroups = []string{"x", "y", "z"}
	vparArms   = []string{"p", "q"}
)

// writeVparCohorts writes native.pulse (id, g, grp, arm, emb vec dim 4
// labelled a..d kind scale [0, 10], emb2 vec dim 4 unlabelled kind
// measure, s) and twin.pulse (emb_a..emb_d, emb2_1..emb2_4 scalars in
// their place), element type ft against f32 / f64 scalars.
func writeVparCohorts(t *testing.T, p *pulse.Pulse, ft encoding.FieldType, sharded bool) {
	t.Helper()
	ctx := context.Background()
	scalar := encoding.FieldTypeF64
	if ft == encoding.FieldTypeVecF32 {
		scalar = encoding.FieldTypeF32
	}
	head := []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "g", Type: encoding.FieldTypeU8},
		{Name: "grp", Type: encoding.FieldTypeCategoricalU8},
		{Name: "arm", Type: encoding.FieldTypeCategoricalU8},
	}
	native := encoding.Schema{
		Fields: append(append([]encoding.Field(nil), head...),
			encoding.Field{Name: "emb", Type: ft, Nullable: true},
			encoding.Field{Name: "emb2", Type: ft, Nullable: true},
			encoding.Field{Name: "s", Type: encoding.FieldTypeF64}),
		Vectors: []encoding.VectorField{
			{FieldIndex: 4, Dim: 4, Labels: vparLabels, Kind: encoding.VectorKindScale, HasScale: true, ScaleMin: 0, ScaleMax: 10},
			{FieldIndex: 5, Dim: 4, Kind: encoding.VectorKindMeasure},
		},
	}
	twinFields := append([]encoding.Field(nil), head...)
	for _, l := range vparLabels {
		twinFields = append(twinFields, encoding.Field{Name: "emb_" + l, Type: scalar, Nullable: true})
	}
	for _, l := range vparLabels2 {
		twinFields = append(twinFields, encoding.Field{Name: l, Type: scalar, Nullable: true})
	}
	twinFields = append(twinFields, encoding.Field{Name: "s", Type: encoding.FieldTypeF64})
	twin := encoding.Schema{Fields: twinFields}

	var opts pulse.CohortBuilderOptions
	if sharded {
		opts.Shards = &pulse.ShardSplit{MaxRecords: vparRows/3 + 1}
	}
	write := func(path string, schema encoding.Schema, row func(r int) pulse.CohortRow) {
		b, err := p.NewCohortBuilder(ctx, path, schema, opts)
		if err != nil {
			t.Fatal(err)
		}
		for r := 0; r < vparRows; r++ {
			if err := b.Append(row(r)); err != nil {
				t.Fatalf("%s row %d: %v", path, r, err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
	elems := []struct {
		at   func(r, k int) (float64, bool)
		null func(r int) bool
	}{{vparEmb, vparEmbNull}, {vparEmb2, vparEmb2Null}}
	write("native.pulse", native, func(r int) pulse.CohortRow {
		row := pulse.CohortRow{uint64(r + 1), uint64(r % 4), vparGroups[r%3], vparArms[r%2], nil, nil, float64(r%37) / 8}
		for j, e := range elems {
			if e.null(r) {
				continue
			}
			v := make([]float64, 4)
			for k := range v {
				x, ok := e.at(r, k)
				if !ok {
					x = math.NaN()
				}
				v[k] = x
			}
			row[4+j] = vecCell(ft, v)
		}
		return row
	})
	write("twin.pulse", twin, func(r int) pulse.CohortRow {
		row := make(pulse.CohortRow, 4+8+1)
		row[0], row[1], row[2], row[3], row[12] = uint64(r+1), uint64(r%4), vparGroups[r%3], vparArms[r%2], float64(r%37)/8
		for j, e := range elems {
			if e.null(r) {
				continue
			}
			for k := 0; k < 4; k++ {
				if x, ok := e.at(r, k); ok {
					row[4+4*j+k] = x
					if scalar == encoding.FieldTypeF32 {
						row[4+4*j+k] = float32(x)
					}
				}
			}
		}
		return row
	})
}

// vparTwinVectors are the twin's virtual vectors: named like the native
// fields, with their labels, kinds and scale range.
func vparTwinVectors() []types.VectorSpec {
	emb := types.VectorSpec{Name: "emb", Labels: vparLabels, Kind: types.VectorKindScale}
	for _, l := range vparLabels {
		emb.Fields = append(emb.Fields, "emb_"+l)
	}
	lo, hi := 0.0, 10.0
	emb.ScaleMin, emb.ScaleMax = &lo, &hi
	emb2 := types.VectorSpec{Name: "emb2", Fields: vparLabels2, Labels: vparLabels2, Kind: types.VectorKindMeasure}
	return []types.VectorSpec{emb, emb2}
}

// vparCase is one request run on both cohorts. ops are the operators it
// exercises (the coverage gate); req builds the request for the native
// (twin=false) or the twin cohort.
type vparCase struct {
	name string
	ops  []string
	req  func(twin bool) *types.Request
}

func vparRaw(s string) json.RawMessage { return json.RawMessage(s) }

// vparReq finishes a case's request: on the twin it prepends the
// virtual vectors named like the native fields, so a slot naming "emb"
// reads alike on both cohorts and only Request.Vectors differs.
func vparReq(twin bool, r *types.Request) *types.Request {
	if !twin {
		return r
	}
	// Only the twin vectors the request reads: a native field nobody
	// names raises nothing, an unreferenced spec a warning.
	r.Vectors = append(vparTwinVectors(), r.Vectors...)
	unused := map[string]bool{}
	for _, n := range vectors.Unreferenced(r) {
		unused[n] = true
	}
	kept := r.Vectors[:0]
	for _, v := range r.Vectors {
		if !unused[v.Name] {
			kept = append(kept, v)
		}
	}
	r.Vectors = kept
	return r
}

func vparAvg(labels ...string) []*types.Aggregation {
	out := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}
	for _, l := range labels {
		out = append(out, &types.Aggregation{Type: types.AGG_AVERAGE, Field: l, Label: "avg_" + l},
			&types.Aggregation{Type: types.AGG_STDDEV, Field: l, Label: "sd_" + l})
	}
	return out
}

func vparCases() []vparCase {
	matrix := func(typ types.MatrixType, vector, params string) types.MatrixSpec {
		m := types.MatrixSpec{Type: typ, Name: strings.ToLower(string(typ)), Vector: vector}
		if params != "" {
			m.Params = vparRaw(params)
		}
		return m
	}
	return []vparCase{
		{
			name: "co-moment matrices by native name",
			ops:  []string{"MAT_COVARIANCE", "MAT_CORRELATION", "MAT_PARTIAL_CORRELATION", "MAT_COLLINEARITY"},
			req: func(twin bool) *types.Request {
				return vparReq(twin, &types.Request{Matrices: []types.MatrixSpec{
					matrix(types.MAT_COVARIANCE, "emb", ""),
					matrix(types.MAT_CORRELATION, "emb", `{"missing":"pairwise","summary":{"top_pairs":2}}`),
					matrix(types.MAT_PARTIAL_CORRELATION, "emb", ""),
					matrix(types.MAT_COLLINEARITY, "emb2", ""),
				}})
			},
		},
		{
			name: "decomposition matrices by native name",
			ops:  []string{"MAT_PCA", "MAT_FACTOR", "MAT_RELIABILITY"},
			req: func(twin bool) *types.Request {
				return vparReq(twin, &types.Request{Matrices: []types.MatrixSpec{
					matrix(types.MAT_PCA, "emb", ""),
					matrix(types.MAT_FACTOR, "emb", `{"factors":1}`),
					matrix(types.MAT_RELIABILITY, "emb", ""),
				}})
			},
		},
		{
			// emb2 is kind measure and names no metric: the kind default
			// fill (std_euclidean) must read the native field's schema kind.
			name: "MAT_DISTANCE kind default",
			ops:  []string{"MAT_DISTANCE", "GROUP_KMEANS"},
			req: func(twin bool) *types.Request {
				return vparReq(twin, &types.Request{Matrices: []types.MatrixSpec{
					matrix(types.MAT_DISTANCE, "emb2", `{"between":{"type":"GROUP_CATEGORY","field":"grp"}}`),
					{Type: types.MAT_DISTANCE, Name: "km", Vector: "emb",
						Params: vparRaw(`{"metric":"euclidean","between":{"type":"GROUP_KMEANS","vector":"emb","params":{"k":2,"seed":3}}}`)},
				}})
			},
		},
		{
			// Rank correlation (a buffered matrix) and an inline element
			// scope mixing a scalar column.
			name: "buffered and inline matrices",
			ops:  []string{"MAT_CORRELATION", "MAT_COVARIANCE"},
			req: func(twin bool) *types.Request {
				if twin {
					return vparReq(true, &types.Request{
						Vectors: []types.VectorSpec{{Name: "mix", Fields: []string{"emb_b", "s", "emb_d"}, Labels: []string{"b", "s", "d"}}},
						Matrices: []types.MatrixSpec{
							matrix(types.MAT_CORRELATION, "emb", `{"method":"spearman"}`),
							{Type: types.MAT_COVARIANCE, Name: "inline", Vector: "emb"},
							{Type: types.MAT_COVARIANCE, Name: "scoped", Vector: "mix"},
						}})
				}
				return &types.Request{Matrices: []types.MatrixSpec{
					matrix(types.MAT_CORRELATION, "emb", `{"method":"spearman"}`),
					{Type: types.MAT_COVARIANCE, Name: "inline", Fields: []string{"emb"}},
					{Type: types.MAT_COVARIANCE, Name: "scoped", Fields: []string{"emb[2]", "s", "emb[4]"}},
				}}
			},
		},
		{
			name: "vector tests",
			ops:  []string{"TEST_BARTLETT_SPHERICITY", "TEST_BOX_M", "TEST_HOTELLING_T2", "TEST_MANOVA", "TEST_MARDIA"},
			req: func(twin bool) *types.Request {
				return vparReq(twin, &types.Request{
					Tests: []*types.Test{
						{Type: types.TEST_BARTLETT_SPHERICITY, Vector: "emb", Label: "bart"},
						{Type: types.TEST_BOX_M, Vector: "emb", SplitBy: "grp", Label: "boxm"},
						{Type: types.TEST_HOTELLING_T2, Vector: "emb", Label: "t2_one"},
						{Type: types.TEST_HOTELLING_T2, Vector: "emb", Vector2: "emb2", Label: "t2_paired"},
						{Type: types.TEST_MANOVA, Vector: "emb", SplitBy: "grp", Label: "manova"},
						{Type: types.TEST_MARDIA, Vector: "emb2", Label: "mardia"},
					},
					Aggregations: vparAvg(),
				})
			},
		},
		{
			name: "row-local vector attributes",
			ops:  []string{"ATTR_SCALE_SCORE", "ATTR_PROFILE_SIMILARITY", "ATTR_VEC_DISTANCE"},
			req: func(twin bool) *types.Request {
				return vparReq(twin, &types.Request{
					Attributes: []*types.Attribute{
						{Type: types.ATTR_SCALE_SCORE, Vector: "emb", Label: "ss", Params: vparRaw(`{"reverse":["b"],"min_valid":2}`)},
						{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "emb", Label: "ps", Params: vparRaw(`{"reference":[1,2,3,4]}`)},
						// cosine on a scale vector: PULSE_VECTOR_METRIC_UNSUITED
						// off the native field's schema kind.
						{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "emb", Label: "pc", Params: vparRaw(`{"metric":"cosine","reference":{"a":1,"b":2,"c":3,"d":4}}`)},
						{Type: types.ATTR_VEC_DISTANCE, Vector: "emb", Label: "vd", Params: vparRaw(`{"metric":"manhattan","reference":[1,2,3,4]}`)},
					},
					Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "cosine(emb, emb2) > -2 || vcount(emb) == 0"}},
					Aggregations: vparAvg("ss", "ps", "pc", "vd"),
				})
			},
		},
		{
			// ATTR_VEC_DISTANCE on kind measure with no metric defaults to
			// std_euclidean — a FITTED metric, so the slot is two-pass;
			// the routing gates must read the native field's kind.
			name: "fitted vector attributes",
			ops:  []string{"ATTR_VEC_DISTANCE", "ATTR_MAHALANOBIS", "ATTR_PC_SCORE", "ATTR_FACTOR_SCORE"},
			req: func(twin bool) *types.Request {
				return vparReq(twin, &types.Request{
					Attributes: []*types.Attribute{
						{Type: types.ATTR_VEC_DISTANCE, Vector: "emb2", Label: "vds", Params: vparRaw(`{"reference":[2,3,4,5]}`)},
						{Type: types.ATTR_MAHALANOBIS, Vector: "emb", Label: "d2"},
						{Type: types.ATTR_PC_SCORE, Vector: "emb", Label: "pc1", Params: vparRaw(`{"component":1}`)},
						{Type: types.ATTR_FACTOR_SCORE, Vector: "emb2", Label: "f1", Params: vparRaw(`{"factor":1,"factors":1}`)},
					},
					Aggregations: vparAvg("vds", "d2", "pc1", "f1"),
				})
			},
		},
		{
			// A request weight: the weighted floor keys (sum_weights,
			// n_eff, n_weight_invalid) must match too.
			name: "weighted",
			ops:  []string{"MAT_COVARIANCE", "TEST_HOTELLING_T2", "ATTR_MAHALANOBIS", "AGG_VEC_MEAN"},
			req: func(twin bool) *types.Request {
				return vparReq(twin, &types.Request{
					Weight:     &types.WeightSpec{Field: "s"},
					Matrices:   []types.MatrixSpec{matrix(types.MAT_COVARIANCE, "emb", "")},
					Tests:      []*types.Test{{Type: types.TEST_HOTELLING_T2, Vector: "emb", SplitBy: "arm", Label: "t2"}},
					Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Vector: "emb2", Label: "d2"}},
					Aggregations: append(vparAvg("d2"),
						&types.Aggregation{Type: types.AGG_VEC_MEAN, Vector: "emb", Label: "m"}),
				})
			},
		},
		{
			name: "k-means grouper and vector aggregations",
			ops:  []string{"GROUP_KMEANS", "AGG_VEC_MEAN", "AGG_VEC_SUM"},
			req: func(twin bool) *types.Request {
				return vparReq(twin, &types.Request{
					Groups: []*types.Group{{Type: types.GROUP_KMEANS, Vector: "emb", Params: vparRaw(`{"k":3,"seed":7}`)}},
					Aggregations: append(vparAvg("s"),
						&types.Aggregation{Type: types.AGG_VEC_MEAN, Vector: "emb2", Label: "m2"},
						&types.Aggregation{Type: types.AGG_VEC_SUM, Vector: "emb", Label: "t"}),
				})
			},
		},
	}
}

// vparMemberNames maps every native element member to its twin column.
var vparMemberNames = func() *strings.Replacer {
	var pairs []string
	for k, l := range vparLabels {
		pairs = append(pairs, vectors.ElementMember("emb", k), "emb_"+l)
	}
	for k, l := range vparLabels2 {
		pairs = append(pairs, vectors.ElementMember("emb2", k), l)
	}
	return strings.NewReplacer(pairs...)
}()

// vparWire is the response the twin must match: everything but the run
// metadata (cohort name, timings), as JSON, native member names mapped
// to their twin columns.
func vparWire(t *testing.T, resp *types.Response, native bool) string {
	t.Helper()
	cp := *resp
	cp.Metadata = nil
	raw, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	if native {
		return vparMemberNames.Replace(string(raw))
	}
	return string(raw)
}

// TestNativeVectorParity: every case, every mode, single file and shard
// archive, vec_f64 and vec_f32 — the native response equals the twin's.
// A mode the operator refuses must be refused identically on both.
func TestNativeVectorParity(t *testing.T) {
	ctx := context.Background()
	for _, ft := range []encoding.FieldType{encoding.FieldTypeVecF64, encoding.FieldTypeVecF32} {
		for _, sharded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/sharded=%v", ft, sharded), func(t *testing.T) {
				p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), ShardWorkers: 3})
				if err != nil {
					t.Fatal(err)
				}
				writeVparCohorts(t, p, ft, sharded)
				for _, c := range vparCases() {
					for _, mode := range []string{"streaming", "buffered", "grouped"} {
						t.Run(c.name+"/"+mode, func(t *testing.T) {
							run := func(twin bool) (string, error) {
								req := c.req(twin)
								req.Cohort = &types.Cohort{Filename: "native.pulse"}
								if twin {
									req.Cohort.Filename = "twin.pulse"
								}
								switch mode {
								case "buffered":
									// Unweighted: ATTR_PERCENTILE refuses a weight.
									req.Attributes = append(req.Attributes,
										&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "s", Label: "s_pct", Weight: types.NullSlotWeight()})
									req.Aggregations = append(req.Aggregations,
										&types.Aggregation{Type: types.AGG_AVERAGE, Field: "s_pct", Label: "avg_s_pct"})
								case "grouped":
									req.Groups = append(req.Groups, &types.Group{Type: types.GROUP_CATEGORY, Field: "g"})
								}
								resp, err := p.Process(ctx, req)
								if err != nil {
									return "", err
								}
								return vparWire(t, resp, !twin), nil
							}
							got, gerr := run(false)
							want, werr := run(true)
							if gerr != nil || werr != nil {
								if mode == "streaming" || gerr == nil || werr == nil ||
									vparMemberNames.Replace(gerr.Error()) != werr.Error() {
									t.Fatalf("native err %v, twin err %v", gerr, werr)
								}
								return
							}
							if got != want {
								t.Errorf("native response differs from the twin %s", vparDiff(got, want))
							}
						})
					}
				}
			})
		}
	}
}

// TestNativeVectorParity_CoversEveryVectorOperator: every built-in
// vector-consuming operator (every MAT_*, the vector tests, attributes,
// groupers and aggregations) is exercised by a parity case.
func TestNativeVectorParity_CoversEveryVectorOperator(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range vparCases() {
		for _, op := range c.ops {
			covered[op] = true
		}
	}
	var want []string
	for _, m := range types.AllMatrixTypes() {
		want = append(want, string(m))
	}
	for _, x := range vectors.VectorTestTypes() {
		want = append(want, string(x))
	}
	for _, x := range vectors.VectorAttributeTypes() {
		want = append(want, string(x))
	}
	for _, x := range vectors.VectorGroupTypes() {
		want = append(want, string(x))
	}
	for _, x := range vectors.VectorAggregationTypes() {
		want = append(want, string(x))
	}
	for _, op := range want {
		if !covered[op] {
			t.Errorf("vector operator %s has no native-vector parity case (vparCases)", op)
		}
	}
}

// vparDiff renders the first difference between two wire strings with
// some context.
func vparDiff(got, want string) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	lo := max(0, i-200)
	return fmt.Sprintf("at byte %d\nnative: …%s…\ntwin:   …%s…", i, got[lo:min(len(got), i+200)], want[lo:min(len(want), i+200)])
}
