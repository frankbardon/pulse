package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Native vectors named in requests (U29 E2-S1): a request names a
// native vec field directly in a vector slot or an expression, or lists
// it (or one element, "emb[k]") inside a virtual vector's fields — and
// gets the SAME response, components included, as the scalar-column
// twin of the cohort read through a virtual vector. The twin maps a NaN
// element to a null scalar and a null vector to every scalar null (the
// shared fill's rule). The consumers here are the ones E2-S1 adopts:
// the expression binding and AGG_VEC_MEAN / AGG_VEC_SUM.

const vnatRows = 150

// vnatElem is row r's element k, ok=false for a missing member (a NaN
// element natively, a null scalar in the twin); vnatNull marks a row
// whose whole vector is null. Values are float32-exact so the vec_f32
// twin reads the same numbers.
func vnatElem(r, k int) (float64, bool) {
	if (r*3+k)%11 == 4 {
		return 0, false
	}
	return float64((r*7+k*13)%29) / 4, true
}

func vnatNull(r int) bool { return r%9 == 5 }

var vnatLabels = []string{"a", "b", "c"}

// writeVnatCohorts writes the native cohort (native.pulse: id, g, emb
// vec dim 3 with labels a/b/c and kind scale, s) and its scalar twin
// (twin.pulse: id, g, emb_a, emb_b, emb_c, s) with element type ft
// (vec_f32 against f32 scalars, vec_f64 against f64), as single files
// or shard archives.
func writeVnatCohorts(t *testing.T, p *pulse.Pulse, ft encoding.FieldType, sharded bool) {
	t.Helper()
	ctx := context.Background()
	scalar := encoding.FieldTypeF64
	if ft == encoding.FieldTypeVecF32 {
		scalar = encoding.FieldTypeF32
	}
	native := encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32},
			{Name: "g", Type: encoding.FieldTypeU8},
			{Name: "emb", Type: ft, Nullable: true},
			{Name: "s", Type: encoding.FieldTypeF64},
		},
		Vectors: []encoding.VectorField{{FieldIndex: 2, Dim: 3, Labels: vnatLabels,
			Kind: encoding.VectorKindScale, HasScale: true, ScaleMin: 0, ScaleMax: 10}},
	}
	twin := encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "g", Type: encoding.FieldTypeU8},
		{Name: "emb_a", Type: scalar, Nullable: true},
		{Name: "emb_b", Type: scalar, Nullable: true},
		{Name: "emb_c", Type: scalar, Nullable: true},
		{Name: "s", Type: encoding.FieldTypeF64},
	}}
	var opts pulse.CohortBuilderOptions
	if sharded {
		opts.Shards = &pulse.ShardSplit{MaxRecords: vnatRows/3 + 1}
	}
	write := func(path string, schema encoding.Schema, row func(r int) pulse.CohortRow) {
		b, err := p.NewCohortBuilder(ctx, path, schema, opts)
		if err != nil {
			t.Fatal(err)
		}
		for r := 0; r < vnatRows; r++ {
			if err := b.Append(row(r)); err != nil {
				t.Fatalf("%s row %d: %v", path, r, err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
	write("native.pulse", native, func(r int) pulse.CohortRow {
		row := pulse.CohortRow{uint64(r + 1), uint64(r % 4), nil, float64(r) / 8}
		if !vnatNull(r) {
			v := make([]float64, 3)
			for k := range v {
				x, ok := vnatElem(r, k)
				if !ok {
					x = math.NaN()
				}
				v[k] = x
			}
			row[2] = vecCell(ft, v)
		}
		return row
	})
	write("twin.pulse", twin, func(r int) pulse.CohortRow {
		row := pulse.CohortRow{uint64(r + 1), uint64(r % 4), nil, nil, nil, float64(r) / 8}
		if !vnatNull(r) {
			for k := 0; k < 3; k++ {
				if x, ok := vnatElem(r, k); ok {
					row[2+k] = x
					if scalar == encoding.FieldTypeF32 {
						row[2+k] = float32(x)
					}
				}
			}
		}
		return row
	})
}

// vnatTwinVector is the twin's virtual vector over the scalar columns,
// labelled like the native field.
func vnatTwinVector(name string) types.VectorSpec {
	return types.VectorSpec{Name: name, Fields: []string{"emb_a", "emb_b", "emb_c"}, Labels: vnatLabels}
}

// vnatCase is one request pair: the native form and its twin form.
type vnatCase struct {
	name         string
	native, twin func() *types.Request
}

func vnatCases() []vnatCase {
	labels := func(l ...string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"labels": l})
		return raw
	}
	return []vnatCase{
		{
			// The native field named directly in an aggregation's
			// vector slot — no Request.Vectors entry; the labels come
			// from the schema.
			name: "AGG_VEC_MEAN by native name",
			native: func() *types.Request {
				return &types.Request{Aggregations: []*types.Aggregation{
					{Type: types.AGG_VEC_MEAN, Vector: "emb", Label: "m"},
					{Type: types.AGG_VEC_SUM, Vector: "emb", Label: "t"},
				}}
			},
			twin: func() *types.Request {
				return &types.Request{Vectors: []types.VectorSpec{vnatTwinVector("embv")},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_VEC_MEAN, Vector: "embv", Label: "m"},
						{Type: types.AGG_VEC_SUM, Vector: "embv", Label: "t"},
					}}
			},
		},
		{
			// Inline fields listing the native field (expanded to its
			// elements, the schema labels carried) beside a scalar, and
			// a single element (a re-scope).
			name: "AGG_VEC_SUM inline fields mixed",
			native: func() *types.Request {
				return &types.Request{Aggregations: []*types.Aggregation{
					{Type: types.AGG_VEC_SUM, Fields: []string{"s", "emb"}, Label: "t"},
					{Type: types.AGG_VEC_MEAN, Fields: []string{"emb[3]", "emb[1]"}, Label: "m", Params: json.RawMessage(`{"expand":true}`)},
				}}
			},
			twin: func() *types.Request {
				return &types.Request{Aggregations: []*types.Aggregation{
					{Type: types.AGG_VEC_SUM, Fields: []string{"s", "emb_a", "emb_b", "emb_c"}, Label: "t", Params: labels("s", "a", "b", "c")},
					{Type: types.AGG_VEC_MEAN, Fields: []string{"emb_c", "emb_a"}, Label: "m", Params: json.RawMessage(`{"expand":true,"labels":["c","a"]}`)},
				}}
			},
		},
		{
			// A virtual vector re-kinding the native field, beside the
			// native field named directly in the same request.
			name: "virtual vector over the native field",
			native: func() *types.Request {
				return &types.Request{
					Vectors: []types.VectorSpec{{Name: "rk", Fields: []string{"emb"}, Kind: types.VectorKindMeasure}},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_VEC_MEAN, Vector: "rk", Label: "m"},
						{Type: types.AGG_VEC_SUM, Vector: "emb", Label: "t"},
					},
				}
			},
			twin: func() *types.Request {
				spec := vnatTwinVector("rk")
				spec.Kind = types.VectorKindMeasure
				return &types.Request{Vectors: []types.VectorSpec{spec, vnatTwinVector("embv")},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_VEC_MEAN, Vector: "rk", Label: "m"},
						{Type: types.AGG_VEC_SUM, Vector: "embv", Label: "t"},
					}}
			},
		},
		{
			// Expressions: the native field is a []float64 identifier;
			// a null vector binds all-NaN (vcount 0), never nil.
			name: "formula and filter",
			native: func() *types.Request {
				return &types.Request{
					Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "vcount(emb) < 3"}},
					Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "vcount(emb) > 0 ? vmean(emb) : nil", Label: "f"}},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: "f", Label: "sf"},
						{Type: types.AGG_COUNT, Field: "id", Label: "n"},
					},
				}
			},
			twin: func() *types.Request {
				return &types.Request{
					Vectors:    []types.VectorSpec{vnatTwinVector("emb")},
					Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "vcount(emb) < 3"}},
					Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "vcount(emb) > 0 ? vmean(emb) : nil", Label: "f"}},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: "f", Label: "sf"},
						{Type: types.AGG_COUNT, Field: "id", Label: "n"},
					},
				}
			},
		},
		{
			// An expression over a virtual vector mixing the native
			// field's elements with a scalar column.
			name: "expression over a mixed virtual vector",
			native: func() *types.Request {
				return &types.Request{
					Vectors:      []types.VectorSpec{{Name: "mix", Fields: []string{"emb[2]", "s", "emb[1]"}}},
					Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "vsum(mix) + vcount(mix)", Label: "f"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "sf"}},
				}
			},
			twin: func() *types.Request {
				return &types.Request{
					Vectors:      []types.VectorSpec{{Name: "mix", Fields: []string{"emb_b", "s", "emb_a"}}},
					Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "vsum(mix) + vcount(mix)", Label: "f"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "sf"}},
				}
			},
		},
	}
}

// vnatWire is the part of a response the twin must match: the data
// rows and every component the request shape owns (aggregations,
// filterers, run counters), as JSON (NaN → null).
func vnatWire(t *testing.T, resp *types.Response) string {
	t.Helper()
	var comp any
	if c := resp.Components; c != nil {
		comp = map[string]any{"aggregations": c.Aggregations, "filterers": c.Filterers, "run": c.Run}
	}
	raw, err := json.Marshal(map[string]any{"data": resp.Data, "components": comp})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestNativeVector_RequestsMatchTwin: every case, ungrouped and grouped,
// streaming / buffered, single file and shard archive, vec_f64 and
// vec_f32 — the native response equals the twin's.
func TestNativeVector_RequestsMatchTwin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, ft := range []encoding.FieldType{encoding.FieldTypeVecF64, encoding.FieldTypeVecF32} {
		for _, sharded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/sharded=%v", ft, sharded), func(t *testing.T) {
				p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), ShardWorkers: 3})
				if err != nil {
					t.Fatal(err)
				}
				writeVnatCohorts(t, p, ft, sharded)
				for _, c := range vnatCases() {
					for _, mode := range []string{"streaming", "buffered", "grouped"} {
						t.Run(c.name+"/"+mode, func(t *testing.T) {
							run := func(cohort string, req *types.Request) string {
								req.Cohort = &types.Cohort{Filename: cohort}
								switch mode {
								case "buffered":
									req.Attributes = append(req.Attributes,
										&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "s", Label: "s_pct"})
								case "grouped":
									req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
								}
								resp, err := p.Process(ctx, req)
								if err != nil {
									t.Fatalf("%s: Process: %v", cohort, err)
								}
								return vnatWire(t, resp)
							}
							got, want := run("native.pulse", c.native()), run("twin.pulse", c.twin())
							if got != want {
								t.Errorf("native response differs from the twin\nnative: %s\ntwin:   %s", got, want)
							}
						})
					}
				}
			})
		}
	}
}

// TestNativeVector_CollisionRefused: a Request.Vectors entry named like
// a native vector field is PULSE_VECTOR_NAME_COLLISION (no shadowing),
// marked native, from Process and predict alike.
func TestNativeVector_CollisionRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	writeVnatCohorts(t, p, encoding.FieldTypeVecF64, false)
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "native.pulse"},
		Vectors:      []types.VectorSpec{{Name: "emb", Fields: []string{"s"}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_MEAN, Vector: "emb", Label: "m"}},
	}
	_, err = p.Process(ctx, req)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_VECTOR_NAME_COLLISION || ce.Details["native"] != true {
		t.Fatalf("Process: %v, want PULSE_VECTOR_NAME_COLLISION {native: true}", err)
	}
	data, err := afero.ReadFile(fsys, "native.pulse")
	if err != nil {
		t.Fatal(err)
	}
	env, _ := p.PredictBytes(ctx, data, req)
	if env == nil || len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_VECTOR_NAME_COLLISION) {
		t.Errorf("predict errors = %+v, want %s", env, perr.PULSE_VECTOR_NAME_COLLISION)
	}
}
