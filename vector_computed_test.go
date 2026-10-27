package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// vector_computed_test.go pins computed vector members (sem-contribution
// E2-S2): a vector member naming a feature output, an attribute label
// or a window label of the same request reads the column the request
// computes. Every MAT_* over such members equals — byte for byte, every
// matrix, auxiliary, scalar and Components entry — the same matrices
// over the request first materialized (pulse.Materialize) and then fit
// over the materialized columns; predict resolves the members and the
// routing exactly as the runtime does; a two-pass member runs buffered
// and serial; every non-matrix consumer is refused.

const compRows = 180

// compSchema is a synthetic battery: an id, a three-level group, four
// f64 items (a nullable) and a small frequency weight.
func compSchema() encoding.Schema {
	d := encoding.NewDictionary()
	for _, v := range []string{"x", "y", "z"} {
		if _, err := d.Add(v); err != nil {
			panic(err)
		}
	}
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Synthetic record identifier"},
		{Name: "grp", Type: encoding.FieldTypeCategoricalU8, Description: "Synthetic three level group", Dictionary: d},
		{Name: "a", Type: encoding.FieldTypeF64, Nullable: true, Description: "Synthetic item a, sometimes missing"},
		{Name: "b", Type: encoding.FieldTypeF64, Description: "Synthetic item b, strictly positive"},
		{Name: "c", Type: encoding.FieldTypeF64, Description: "Synthetic item c on a ten point range"},
		{Name: "d", Type: encoding.FieldTypeF64, Description: "Synthetic item d with a wide spread"},
		{Name: "w", Type: encoding.FieldTypeU8, Description: "Synthetic frequency weight from one to three"},
	}}
}

func compPulse(t *testing.T) (*Pulse, afero.Fs, []byte) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b, err := p.NewCohortBuilder(ctx, "src.pulse", compSchema(), CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < compRows; i++ {
		x := float64(i)
		var a any = 2 + float64((i*7)%13)/2 + 0.01*x
		if i%11 == 3 {
			a = nil
		}
		bv := 1.5 + float64((i*5)%17)/4 + 0.02*x
		cv := float64((i*13)%21) / 2
		dv := 40 + 9*float64((i*3)%7) - 0.3*x + float64((i*i)%5)
		row := CohortRow{uint64(i), []string{"x", "y", "z"}[i%3], a, bv, cv, dv, uint64(1 + i%3)}
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := afero.ReadFile(fsys, "src.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return p, fsys, data
}

// compImpute is an imputation-like formula: null where c is high.
var compImpute = &types.Attribute{Type: types.ATTR_FORMULA, Label: "imp", Expression: "c > 8.5 ? nil : b + 0.5 * c"}

type compCase struct {
	name    string
	derived func(r *types.Request)
	members []string
	// streamable / mergeable are the runtime routing the members imply.
	streamable, mergeable bool
}

func compCases() []compCase {
	return []compCase{
		{"row-local formula", func(r *types.Request) {
			r.Attributes = []*types.Attribute{compImpute}
		}, []string{"a", "imp", "c", "d"}, true, true},
		{"feature and formula", func(r *types.Request) {
			r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "b"}}
			r.Attributes = []*types.Attribute{compImpute}
		}, []string{"a", "LOG_b", "imp", "c"}, true, false},
		{"two-pass attribute", func(r *types.Request) {
			r.Attributes = []*types.Attribute{compImpute, {Type: types.ATTR_ZSCORE, Field: "d", Label: "zd"}}
		}, []string{"a", "imp", "zd", "c"}, false, false},
		{"total formula", func(r *types.Request) {
			r.Attributes = []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "share", Expression: "1000 * d / total(d)"}}
		}, []string{"a", "share", "b", "c"}, false, false},
		{"window", func(r *types.Request) {
			r.Windows = []*types.Window{{Type: types.WIN_LAG, Field: "c", Label: "lagc", OrderBy: []types.OrderKey{{Field: "id"}}}}
		}, []string{"a", "lagc", "b", "d"}, false, false},
	}
}

// compMatrices is every built-in MAT_* over vector v (members m), plus a
// weighted covariance and SEM.
func compMatrices(m []string) []types.MatrixSpec {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	freq := types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	return []types.MatrixSpec{
		{Name: "cov", Type: types.MAT_COVARIANCE, Vector: "v"},
		{Name: "corr", Type: types.MAT_CORRELATION, Vector: "v", Params: raw(`{"inference": true}`)},
		{Name: "spearman", Type: types.MAT_CORRELATION, Vector: "v", Params: raw(`{"method": "spearman"}`)},
		{Name: "pairwise", Type: types.MAT_CORRELATION, Vector: "v", Params: raw(`{"missing": "pairwise"}`)},
		{Name: "partial", Type: types.MAT_PARTIAL_CORRELATION, Vector: "v", Params: raw(`{"control": ["` + m[1] + `"]}`)},
		{Name: "collin", Type: types.MAT_COLLINEARITY, Vector: "v"},
		{Name: "pca", Type: types.MAT_PCA, Vector: "v", Params: raw(`{"components": 2}`)},
		{Name: "factor", Type: types.MAT_FACTOR, Vector: "v", Params: raw(`{"factors": 1}`)},
		{Name: "rel", Type: types.MAT_RELIABILITY, Vector: "v"},
		{Name: "sem", Type: types.MAT_SEM, Vector: "v"},
		{Name: "dist", Type: types.MAT_DISTANCE, Vector: "v", Params: raw(`{"between": {"type": "GROUP_CATEGORY", "field": "grp"}}`)},
		{Name: "inline", Type: types.MAT_COVARIANCE, Fields: m[:3]},
		{Name: "wcov", Type: types.MAT_COVARIANCE, Vector: "v", Weight: freq},
		{Name: "wsem", Type: types.MAT_SEM, Vector: "v", Weight: freq},
	}
}

func compMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestComputedMembers_EqualMaterializeThenFit: for every case, every
// matrix operator over computed members — a feature output, a nullable
// formula, a two-pass z-score, a total() share and a window lag — is
// byte-identical (matrices and Components.Matrices) to materializing the
// derived columns first and fitting over them; a standardized vector
// over the same members likewise. Predict resolves the members and the
// matrix axes as the runtime emits them and reports the runtime's
// streamability; the runtime routing (stream / merge) is the case's.
func TestComputedMembers_EqualMaterializeThenFit(t *testing.T) {
	t.Parallel()

	p, _, data := compPulse(t)
	ctx := context.Background()
	for _, c := range compCases() {
		t.Run(c.name, func(t *testing.T) {
			filter := []*types.Filterer{{Type: types.FILTER_RANGE, Field: "id", Values: []string{"4", "171"}}}
			derived := &types.Request{Cohort: &types.Cohort{Filename: "src.pulse"}, Filterers: filter}
			c.derived(derived)
			out := "mat_" + strings.ReplaceAll(c.name, " ", "_") + ".pulse"
			if _, err := p.Materialize(ctx, derived, out, MaterializeOptions{}); err != nil {
				t.Fatalf("Materialize: %v", err)
			}
			for _, std := range []types.VectorStandardize{"", types.VectorStandardizeZScore} {
				vec := []types.VectorSpec{{Name: "v", Fields: c.members, Standardize: std}}
				mats := compMatrices(c.members)
				if std != "" {
					// Only the listwise co-moment operators read a
					// standardized vector.
					mats = []types.MatrixSpec{mats[0], mats[1], mats[4], mats[9], mats[12], mats[13]}
				}
				comp := *derived
				comp.Vectors, comp.Matrices = vec, mats
				twin := &types.Request{Cohort: &types.Cohort{Filename: out}, Vectors: vec, Matrices: mats}

				got, err := p.Process(ctx, &comp)
				if err != nil {
					t.Fatalf("Process (computed, standardize %q): %v", std, err)
				}
				want, err := p.Process(ctx, twin)
				if err != nil {
					t.Fatalf("Process (materialized): %v", err)
				}
				if g, w := compMarshal(t, got.Matrices), compMarshal(t, want.Matrices); g != w {
					t.Fatalf("standardize %q: matrices differ from materialize-then-fit:\ncomputed     %s\nmaterialized %s", std, g, w)
				}
				if g, w := compMarshal(t, got.Components.Matrices), compMarshal(t, want.Components.Matrices); g != w {
					t.Fatalf("standardize %q: components differ:\ncomputed     %s\nmaterialized %s", std, g, w)
				}
				for _, m := range got.Matrices {
					if m.Error != nil {
						t.Fatalf("matrix %s failed: %+v", m.Name, m.Error)
					}
				}

				// Predict ≡ runtime: members, axes, routing.
				env, _ := p.PredictBytes(ctx, data, &comp)
				if env == nil || len(env.Errors) != 0 {
					t.Fatalf("predict errors: %v", env)
				}
				res := env.Data.(*descriptor.PredictResult)
				if !reflect.DeepEqual(res.ResolvedVectors["v"], c.members) {
					t.Errorf("predict resolved_vectors %v, want %v", res.ResolvedVectors, c.members)
				}
				if len(res.Matrices) != len(got.Matrices) {
					t.Fatalf("predict echoes %d matrices, runtime %d", len(res.Matrices), len(got.Matrices))
				}
				for i, pm := range res.Matrices {
					if pm.Type == types.MAT_DISTANCE {
						continue // keyed by the bucket labels, unknown before the run
					}
					if rk := got.Matrices[i].Primary.RowKeys; !reflect.DeepEqual(pm.AxisKeys, rk) {
						t.Errorf("%s: predict axis %v, runtime row keys %v", pm.Name, pm.AxisKeys, rk)
					}
				}
				// The routing the members imply, over one co-moment
				// matrix (a rank method or a buffered between would
				// route buffered on its own).
				route := comp
				route.Matrices = mats[:1]
				renv, _ := p.PredictBytes(ctx, data, &route)
				schema := compSchema()
				if s := processing.CanStreamRequest(&route, &schema); s != c.streamable || renv.Data.(*descriptor.PredictResult).Streamable != s {
					t.Errorf("streamable: runtime %v, predict %v, want %v", s, renv.Data.(*descriptor.PredictResult).Streamable, c.streamable)
				}
				if m := processing.CanMergeRequest(&route, &schema); m != c.mergeable {
					t.Errorf("mergeable %v, want %v", m, c.mergeable)
				}
			}
		})
	}
}

// TestComputedMembers_NullDropsListwise: a null computed value is a null
// member — the row is dropped listwise and counted, exactly as the
// materialized null cell is.
func TestComputedMembers_NullDropsListwise(t *testing.T) {
	t.Parallel()

	p, _, _ := compPulse(t)
	ctx := context.Background()
	req := &types.Request{
		Cohort:     &types.Cohort{Filename: "src.pulse"},
		Attributes: []*types.Attribute{compImpute},
		Vectors:    []types.VectorSpec{{Name: "v", Fields: []string{"b", "imp", "c"}}},
		Matrices:   []types.MatrixSpec{{Name: "cov", Type: types.MAT_COVARIANCE, Vector: "v"}},
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	dropped := 0
	for i := 0; i < compRows; i++ {
		if float64((i*13)%21)/2 > 8.5 {
			dropped++
		}
	}
	c := resp.Components.Matrices[0]
	if dropped == 0 || c.NListwiseDropped != dropped || c.N != compRows-dropped {
		t.Fatalf("components n %d dropped %d, want %d / %d", c.N, c.NListwiseDropped, compRows-dropped, dropped)
	}
}

// TestComputedMembers_ProjectionDecodesSources: a matrix over a computed
// member decodes the columns its producer reads — not wide, and not the
// computed name, which no record stores.
func TestComputedMembers_ProjectionDecodesSources(t *testing.T) {
	t.Parallel()

	schema := compSchema()
	req := &types.Request{
		Cohort:     &types.Cohort{Filename: "src.pulse"},
		Features:   []*types.Feature{{Type: types.FEAT_LOG, Field: "d"}},
		Attributes: []*types.Attribute{compImpute},
		Vectors:    []types.VectorSpec{{Name: "v", Fields: []string{"a", "imp", "LOG_d"}}},
		Matrices:   []types.MatrixSpec{{Name: "cov", Type: types.MAT_COVARIANCE, Vector: "v"}},
	}
	got := processing.NeededFields(req, &schema, nil)
	if got.IsWide() {
		t.Fatalf("projection widened: %v", got)
	}
	for _, f := range []string{"a", "b", "c", "d"} {
		if !got.Has(f) {
			t.Errorf("projection %v lacks source field %s", got, f)
		}
	}
	for _, f := range []string{"imp", "LOG_d", "id", "grp", "w"} {
		if got.Has(f) {
			t.Errorf("projection %v decodes %s", got, f)
		}
	}
}

// TestComputedMembers_NonMatrixConsumersRefused: only a MAT_* slot reads
// a vector with a computed member; every other consumer — test,
// attribute, grouper, aggregation, expression, a MAT_DISTANCE between —
// is refused with PULSE_VECTOR_INVALID computed_member_unsupported, by
// Process and Predict alike; a glob never matches a computed column.
func TestComputedMembers_NonMatrixConsumersRefused(t *testing.T) {
	t.Parallel()

	p, _, data := compPulse(t)
	ctx := context.Background()
	build := func() *types.Request {
		return &types.Request{
			Cohort:     &types.Cohort{Filename: "src.pulse"},
			Attributes: []*types.Attribute{compImpute},
			Vectors:    []types.VectorSpec{{Name: "v", Fields: []string{"b", "imp", "c"}}},
		}
	}
	count := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}
	for _, c := range []struct {
		name string
		edit func(*types.Request)
		slot string
	}{
		{"test", func(r *types.Request) {
			r.Tests = []*types.Test{{Type: types.TEST_HOTELLING_T2, Vector: "v"}}
		}, "tests[0].vector"},
		{"attribute", func(r *types.Request) {
			r.Attributes = append(r.Attributes, &types.Attribute{Type: types.ATTR_SCALE_SCORE, Vector: "v", Label: "s"})
		}, "attributes[1].vector"},
		{"aggregation", func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_VEC_MEAN, Vector: "v", Label: "m"}}
		}, "aggregations[0].vector"},
		{"grouper", func(r *types.Request) {
			r.Groups = []*types.Group{{Type: types.GROUP_KMEANS, Vector: "v", Params: json.RawMessage(`{"k": 2}`)}}
			r.Aggregations = count
		}, "groups[0].vector"},
		{"expression", func(r *types.Request) {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "len(v) > 0"}}
			r.Aggregations = count
		}, "filterers[0].expression"},
		{"between", func(r *types.Request) {
			r.Vectors = append(r.Vectors, types.VectorSpec{Name: "plain", Fields: []string{"b", "c"}})
			r.Matrices = []types.MatrixSpec{{Type: types.MAT_DISTANCE, Vector: "plain",
				Params: json.RawMessage(`{"between": {"type": "GROUP_KMEANS", "vector": "v", "params": {"k": 2}}}`)}}
		}, "matrices[0].params.between.vector"},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := build()
			c.edit(req)
			_, err := p.Process(ctx, req)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_VECTOR_INVALID || ce.Details["reason"] != "computed_member_unsupported" ||
				ce.Details["slot"] != c.slot || ce.Details["member"] != "imp" {
				t.Fatalf("Process err = %v (%+v), want PULSE_VECTOR_INVALID computed_member_unsupported at %s", err, ce, c.slot)
			}
			env, _ := p.PredictBytes(ctx, data, req)
			if env == nil || len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_VECTOR_INVALID) || env.Errors[0].Message != ce.Message {
				t.Errorf("predict errors = %v, want the runtime's %s %q", env.Errors, ce.Code, ce.Message)
			}
		})
	}

	// A glob matches schema fields only: "im*" is no member.
	req := build()
	req.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"b", "im*"}}}
	req.Matrices = []types.MatrixSpec{{Name: "cov", Type: types.MAT_COVARIANCE, Vector: "v"}}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if rk := resp.Matrices[0].Primary.RowKeys; !slices.Equal(rk, []string{"b"}) {
		t.Errorf("glob row keys %v, want [b]", rk)
	}
	// A literal naming nothing computed is still the unknown-field
	// refusal.
	req.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"b", "nosuch"}}}
	if _, err := p.Process(ctx, req); !stderrors.As(err, new(*perr.CodedError)) || !strings.Contains(err.Error(), "vector references unknown field: nosuch") {
		t.Errorf("unknown literal: %v", err)
	}
}

// TestComputedMembers_ComposeFitCaptures: a Compose vector fit whose
// battery reads a computed member captures it (the fit's labels name
// it), and a params.fit consumer emits the fit request's own covariance
// to the bit.
func TestComputedMembers_ComposeFitCaptures(t *testing.T) {
	t.Parallel()

	p, _, _ := compPulse(t)
	ctx := context.Background()
	fitReq := &types.Request{
		Cohort:     &types.Cohort{Filename: "src.pulse"},
		Filterers:  []*types.Filterer{{Type: types.FILTER_RANGE, Field: "id", Values: []string{"0", "99"}}},
		Attributes: []*types.Attribute{compImpute},
		Matrices:   []types.MatrixSpec{{Name: "cov", Type: types.MAT_COVARIANCE, Fields: []string{"a", "imp", "c"}}},
	}
	consumer := &types.Request{
		Cohort:     &types.Cohort{Filename: "src.pulse"},
		Attributes: []*types.Attribute{compImpute},
		Matrices:   []types.MatrixSpec{{Name: "m", Type: types.MAT_COVARIANCE, Fields: []string{"a", "imp", "c"}, Params: json.RawMessage(`{"fit": "pop"}`)}},
	}
	batch, err := p.Compose(ctx, &types.ComposedRequest{Fits: []types.FitSpec{{Name: "pop", Request: fitReq}}, Requests: []*types.Request{consumer}})
	if err != nil {
		t.Fatal(err)
	}
	fit := batch.Fits["pop"]
	if fit == nil || fit.Vector == nil || !slices.Equal(fit.Vector.Labels, []string{"a", "imp", "c"}) {
		t.Fatalf("fits[pop] = %+v, want the vector arm labelled a, imp, c", fit)
	}
	alone, err := p.Process(ctx, fitReq)
	if err != nil {
		t.Fatal(err)
	}
	if g, w := compMarshal(t, batch.Responses[0].Matrices[0].Primary.Values), compMarshal(t, alone.Matrices[0].Primary.Values); g != w {
		t.Fatalf("fit-bound covariance %s, the fit request's own %s", g, w)
	}
	if fmt.Sprint(fit.Vector.N) == "0" {
		t.Fatalf("empty fit: %+v", fit.Vector)
	}
}

// TestComputedMembers_GroupedEqualsMaterialized: per bucket, a grouped
// matrix over computed members (row-local and two-pass) equals the same
// grouped matrices over the materialized columns.
func TestComputedMembers_GroupedEqualsMaterialized(t *testing.T) {
	t.Parallel()

	p, _, _ := compPulse(t)
	ctx := context.Background()
	derived := &types.Request{
		Cohort:     &types.Cohort{Filename: "src.pulse"},
		Attributes: []*types.Attribute{compImpute, {Type: types.ATTR_ZSCORE, Field: "d", Label: "zd"}},
	}
	if _, err := p.Materialize(ctx, derived, "grouped.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	shape := func(r *types.Request) *types.Request {
		r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grp"}}
		r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}}
		r.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"a", "imp", "zd", "c"}}}
		r.Matrices = []types.MatrixSpec{
			{Name: "cov", Type: types.MAT_COVARIANCE, Vector: "v"},
			{Name: "sem", Type: types.MAT_SEM, Vector: "v"},
		}
		return r
	}
	comp := *derived
	got, err := p.Process(ctx, shape(&comp))
	if err != nil {
		t.Fatal(err)
	}
	want, err := p.Process(ctx, shape(&types.Request{Cohort: &types.Cohort{Filename: "grouped.pulse"}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Matrices) != 6 {
		t.Fatalf("%d grouped matrices, want 2 specs x 3 buckets", len(got.Matrices))
	}
	if g, w := compMarshal(t, got.Matrices), compMarshal(t, want.Matrices); g != w {
		t.Fatalf("grouped matrices differ:\ncomputed     %s\nmaterialized %s", g, w)
	}
}
