package pulse

import (
	"context"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// buildVecSource writes vsrc.pulse: an id, a described vec_f64 (kind,
// scale, labels) and a nullable vec_f32, three rows (one null vector).
func buildVecSource(t *testing.T, p *Pulse) {
	t.Helper()
	schema := encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, Description: "Record identifier, unique per row"},
			{Name: "emb", Type: encoding.FieldTypeVecF64, Description: "Three-element preference embedding"},
			{Name: "mix", Type: encoding.FieldTypeVecF32, Nullable: true, Description: "Two-way channel share of spend"},
		},
		Vectors: []encoding.VectorField{
			{FieldIndex: 1, Dim: 3, Kind: encoding.VectorKindScale, ScaleMin: 1, ScaleMax: 5, HasScale: true, Labels: []string{"a", "b", "c"}},
			{FieldIndex: 2, Dim: 2, Kind: encoding.VectorKindComposition},
		},
	}
	b, err := p.NewCohortBuilder(context.Background(), "vsrc.pulse", schema, CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []CohortRow{
		{uint64(1), []float64{1, 2, 3}, []float32{0.25, 0.75}},
		{uint64(2), []float64{4, 5, 1}, nil},
		{uint64(3), []float64{2, 2, 2}, []float32{1, 0}},
	} {
		if err := b.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestMaterialize_VectorFieldsCarryTheirMetadata: a source holding
// vec_* fields materializes — each kept vector's VECTORS entry (dim,
// kind, scale, labels) follows it to its output position under its
// output name; a renamed or reordered vector keeps its own entry, an
// excluded one leaves none, and dropping every vector writes 0x01.
func TestMaterialize_VectorFieldsCarryTheirMetadata(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		out     *types.MaterializeOutput
		fields  []string
		vectors map[string]int // output name → source field index
		version byte
	}{
		{"kept", nil, []string{"id", "emb", "mix"}, map[string]int{"emb": 1, "mix": 2}, encoding.FormatVersionV2},
		{"renamed and reordered", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
			{From: "mix", As: "shares"}, {From: "id"}, {From: "emb", As: "pref"},
		}}, []string{"shares", "id", "pref"}, map[string]int{"shares": 2, "pref": 1}, encoding.FormatVersionV2},
		{"one excluded", &types.MaterializeOutput{Exclude: []string{"emb"}}, []string{"id", "mix"}, map[string]int{"mix": 2}, encoding.FormatVersionV2},
		{"all excluded", &types.MaterializeOutput{Exclude: []string{"emb", "mix"}}, []string{"id"}, nil, encoding.FormatVersionV1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p, fsys := matPulse(t, Options{})
			buildVecSource(t, p)
			req := &Request{Cohort: &types.Cohort{Filename: "vsrc.pulse"},
				Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "id"}}, Output: c.out}
			if c.out != nil {
				req.Features = nil
			}
			if _, err := p.Materialize(context.Background(), req, "out.pulse", MaterializeOptions{}); err != nil {
				t.Fatal(err)
			}
			schema, rows := readAll(t, p, "out.pulse")
			names := fieldNames(schema)
			if c.out == nil {
				names = names[:len(names)-1] // the LOG_id derived column
			}
			if !slices.Equal(names, c.fields) {
				t.Fatalf("fields = %v, want %v", names, c.fields)
			}
			if len(schema.Vectors) != len(c.vectors) {
				t.Fatalf("vectors = %+v, want %d entries", schema.Vectors, len(c.vectors))
			}
			srcSchema, srcRows := readAll(t, p, "vsrc.pulse")
			for i, f := range schema.Fields {
				from, ok := c.vectors[f.Name]
				if !ok {
					continue
				}
				got, ok := schema.Vector(i)
				want, _ := srcSchema.Vector(from)
				want.FieldIndex = i
				if !ok || got.Dim != want.Dim || got.Kind != want.Kind || got.HasScale != want.HasScale ||
					got.ScaleMin != want.ScaleMin || got.ScaleMax != want.ScaleMax || !slices.Equal(got.Labels, want.Labels) {
					t.Errorf("%s vector = %+v, want %+v", f.Name, got, want)
				}
				for r := range rows {
					if !vecEqual(rows[r][i], srcRows[r][from]) {
						t.Errorf("row %d %s = %v, want %v", r, f.Name, rows[r][i], srcRows[r][from])
					}
				}
			}
			raw, err := afero.ReadFile(fsys, "out.pulse")
			if err != nil {
				t.Fatal(err)
			}
			if ver := raw[encoding.HeaderSize-1]; ver != c.version {
				t.Errorf("format version = %#x, want %#x", ver, c.version)
			}
		})
	}
}

func vecEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case []float64:
		y, ok := b.([]float64)
		return ok && slices.Equal(x, y)
	case []float32:
		y, ok := b.([]float32)
		return ok && slices.Equal(x, y)
	}
	return false
}
