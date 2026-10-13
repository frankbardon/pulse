package pulse_test

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// Archive and grouping machinery over vector fields (vec_f32 / vec_f64):
// shard cohesion is strict on a vec field's dim and element type — even
// for a TRAILING vector, where no later field's offset moves and stride
// math alone would accept the shard — and tolerant, with a warning, on
// its labels / kind / scale; the set-widen re-stride, parent groups,
// constant elision and retro-dedup all carry a vector's whole stride
// verbatim.

// vecArchiveFixture builds a single-file cohort at path through the
// public builder and returns its rows.
func vecArchiveFixture(t *testing.T, p *pulse.Pulse, path string, sch encoding.Schema, rows []pulse.CohortRow, opts pulse.CohortBuilderOptions) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), path, sch, opts)
	if err != nil {
		t.Fatalf("builder %s: %v", path, err)
	}
	for r, row := range rows {
		if err := b.Append(row); err != nil {
			t.Fatalf("%s append row %d: %v", path, r, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatalf("%s close: %v", path, err)
	}
}

// vecArchiveRows reads every row of path through Cohort.Reader and
// renders it NaN-safely (fmt prints NaN as NaN, where DeepEqual would
// call two NaN cells different).
func vecArchiveRows(t *testing.T, p *pulse.Pulse, path string) []string {
	t.Helper()
	c, err := p.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	rd, err := c.Reader()
	if err != nil {
		t.Fatalf("reader %s: %v", path, err)
	}
	defer rd.Close()
	out := make([]string, 0, rd.Len())
	for r := int64(0); r < rd.Len(); r++ {
		row, err := rd.RecordAt(r)
		if err != nil {
			t.Fatalf("%s RecordAt(%d): %v", path, r, err)
		}
		out = append(out, fmt.Sprint(row))
	}
	return out
}

func renderRows(rows []pulse.CohortRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprint(r)
	}
	return out
}

func assertRows(t *testing.T, where string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d rows, want %d", where, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s row %d:\n got  %s\n want %s", where, i, got[i], want[i])
		}
	}
}

func assertCodeIs(t *testing.T, err error, want errors.Code) *errors.CodedError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil", want)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != want {
		t.Fatalf("expected %s, got %v", want, err)
	}
	return ce
}

// trailingVecSchema: the vector is the LAST field, so a dim difference
// moves no other field's offset.
func trailingVecSchema(ft encoding.FieldType, dim int) encoding.Schema {
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "emb", Type: ft},
	}, Vectors: []encoding.VectorField{{FieldIndex: 1, Dim: dim}}}
}

func trailingVecRows(ft encoding.FieldType, dim, n, base int) []pulse.CohortRow {
	rows := make([]pulse.CohortRow, n)
	for r := range rows {
		v := make([]float64, dim)
		for k := range v {
			v[k] = float64(base+r) + float64(k)/4
		}
		rows[r] = pulse.CohortRow{uint64(base + r), vecCell(ft, v)}
	}
	return rows
}

// TestVectorShards_DimAndTypeStrict: a shard whose trailing vector has a
// different dim, or the other element type, is refused by both
// CreateShardArchive and AddShard with PULSE_SHARD_SCHEMA_MISMATCH, and
// a refused add leaves the archive byte-identical.
func TestVectorShards_DimAndTypeStrict(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		ft      encoding.FieldType
		dim     int
		wantDim string // details.dimension, "" when the type byte decides
	}{
		{"trailing dim 3 vs 4", encoding.FieldTypeVecF64, 4, "vector_dim"},
		{"trailing f64 vs f32", encoding.FieldTypeVecF32, 3, ""},
		// f32 x 6 is 24 bytes, exactly f64 x 3: same stride, different
		// type and dim — the type byte refuses it.
		{"trailing equal-stride f32x6", encoding.FieldTypeVecF32, 6, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			p, err := pulse.New(pulse.Options{FS: fs})
			if err != nil {
				t.Fatal(err)
			}
			vecArchiveFixture(t, p, "a.pulse", trailingVecSchema(encoding.FieldTypeVecF64, 3),
				trailingVecRows(encoding.FieldTypeVecF64, 3, 5, 0), pulse.CohortBuilderOptions{})
			vecArchiveFixture(t, p, "b.pulse", trailingVecSchema(tc.ft, tc.dim),
				trailingVecRows(tc.ft, tc.dim, 4, 100), pulse.CohortBuilderOptions{})

			_, err = p.CreateShardArchive(ctx, "both.pulse", []string{"a.pulse", "b.pulse"})
			ce := assertCodeIs(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
			if tc.wantDim != "" && ce.Details["dimension"] != tc.wantDim {
				t.Fatalf("create: details %v, want dimension %s", ce.Details, tc.wantDim)
			}

			if _, err := p.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
				t.Fatal(err)
			}
			before, _ := afero.ReadFile(fs, "arch.pulse")
			_, err = p.AddShard(ctx, "arch.pulse", "b.pulse")
			ce = assertCodeIs(t, err, errors.PULSE_SHARD_SCHEMA_MISMATCH)
			if tc.wantDim != "" && (ce.Details["canonical_dim"] != 3 || ce.Details["incoming_dim"] != tc.dim) {
				t.Fatalf("add: details %v", ce.Details)
			}
			after, _ := afero.ReadFile(fs, "arch.pulse")
			if !bytes.Equal(before, after) {
				t.Fatal("a refused add modified the archive")
			}
			assertRows(t, "archive after refused add", vecArchiveRows(t, p, "arch.pulse"),
				renderRows(trailingVecRows(encoding.FieldTypeVecF64, 3, 5, 0)))
		})
	}
}

// TestVectorShards_MetadataDivergenceWarns: labels, kind and scale range
// are description-class — the shard is accepted, the canonical copy
// wins, and each divergence is a PULSE_SHARD_DESCRIPTION_DIVERGENCE
// warning naming the dimension. The archive decodes every row.
func TestVectorShards_MetadataDivergenceWarns(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	canon := trailingVecSchema(encoding.FieldTypeVecF64, 3)
	canon.Vectors[0].Labels = []string{"a", "b", "c"}
	canon.Vectors[0].Kind = encoding.VectorKindScale
	canon.Vectors[0].HasScale, canon.Vectors[0].ScaleMin, canon.Vectors[0].ScaleMax = true, 0, 10
	inc := trailingVecSchema(encoding.FieldTypeVecF64, 3)
	inc.Vectors[0].Labels = []string{"a", "b", "z"}
	inc.Vectors[0].Kind = encoding.VectorKindMeasure

	rowsA := trailingVecRows(encoding.FieldTypeVecF64, 3, 5, 0)
	rowsB := trailingVecRows(encoding.FieldTypeVecF64, 3, 4, 100)
	vecArchiveFixture(t, p, "a.pulse", canon, rowsA, pulse.CohortBuilderOptions{})
	vecArchiveFixture(t, p, "b.pulse", inc, rowsB, pulse.CohortBuilderOptions{})
	if _, err := p.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	res, err := p.AddShard(ctx, "arch.pulse", "b.pulse")
	if err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	got := map[string]bool{}
	for _, w := range res.Warnings {
		if w.Code == string(errors.PULSE_SHARD_DESCRIPTION_DIVERGENCE) && w.Details["field"] == "emb" {
			got[fmt.Sprint(w.Details["dimension"])] = true
		}
	}
	for _, d := range []string{"vector_labels", "vector_kind", "vector_scale"} {
		if !got[d] {
			t.Fatalf("no %s divergence warning; warnings %+v", d, res.Warnings)
		}
	}
	assertRows(t, "archive", vecArchiveRows(t, p, "arch.pulse"), renderRows(append(rowsA, rowsB...)))

	// Canonical wins: the archive still describes the canonical labels.
	c, err := p.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := c.Schema().Vector(1); !ok || fmt.Sprint(v.Labels) != "[a b c]" || v.Kind != encoding.VectorKindScale {
		t.Fatalf("canonical vector metadata = %+v", v)
	}

	// The same divergence through CreateShardArchive is also a warning.
	cres, err := p.CreateShardArchive(ctx, "both.pulse", []string{"a.pulse", "b.pulse"})
	if err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	if len(cres.Warnings) < 3 {
		t.Fatalf("create warnings = %+v, want the three vector divergences", cres.Warnings)
	}
}

// setVecSchema: a set field BEFORE a vector, so a set-rung widen shifts
// the vector's bytes, and a scalar after both.
func setVecSchema() encoding.Schema {
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Nullable: true},
		{Name: "emb", Type: encoding.FieldTypeVecF32, Nullable: true},
		{Name: "tail", Type: encoding.FieldTypeU16},
	}, Vectors: []encoding.VectorField{{FieldIndex: 2, Dim: 4, Labels: []string{"w", "x", "y", "z"}}}}
}

func setVecRows(n, base int, tagPrefix string) []pulse.CohortRow {
	rows := make([]pulse.CohortRow, n)
	for r := range rows {
		row := pulse.CohortRow{uint64(base + r), nil, nil, uint64(1000 + base + r)}
		if r%5 != 2 {
			row[1] = []string{fmt.Sprintf("%s%d", tagPrefix, r%6), fmt.Sprintf("%s%d", tagPrefix, (r+1)%6)}
		}
		if r%4 != 1 {
			row[2] = []float32{float32(r), -float32(r) / 2, float32(math.NaN()), float32(base)}
		}
		rows[r] = row
	}
	return rows
}

// TestVectorShards_SetWidenCarriesVectors: a set-rung widen re-strides
// the record; the vector after the widened set moves by the width delta
// and decodes identically — through Pulse.WidenSetField on a single file
// and through a shard add whose dictionary union outgrows set_u8.
func TestVectorShards_SetWidenCarriesVectors(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	rows := setVecRows(12, 0, "t")
	vecArchiveFixture(t, p, "single.pulse", setVecSchema(), rows, pulse.CohortBuilderOptions{})
	want := vecArchiveRows(t, p, "single.pulse")
	if len(want) != len(rows) {
		t.Fatalf("single: %d rows, want %d", len(want), len(rows))
	}
	if _, err := p.WidenSetField(ctx, "single.pulse", "tags", "set_u128"); err != nil {
		t.Fatalf("WidenSetField: %v", err)
	}
	assertRows(t, "single after widen", vecArchiveRows(t, p, "single.pulse"), want)

	// Shard add: 6 + 6 distinct tags outgrow set_u8 (8), so the archive
	// widens to set_u16 with the vector riding behind the set.
	rowsA, rowsB := setVecRows(12, 0, "a"), setVecRows(9, 50, "b")
	vecArchiveFixture(t, p, "a.pulse", setVecSchema(), rowsA, pulse.CohortBuilderOptions{})
	vecArchiveFixture(t, p, "b.pulse", setVecSchema(), rowsB, pulse.CohortBuilderOptions{})
	if _, err := p.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	res, err := p.AddShard(ctx, "arch.pulse", "b.pulse")
	if err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	if len(res.Widened) != 1 || res.Widened[0].To != "set_u16" {
		t.Fatalf("widened = %+v, want tags -> set_u16", res.Widened)
	}
	// Every b tag is new to the archive, so the union appends b's
	// dictionary after a's in b's order: each row's labels read back in
	// the same order its own single file reports them.
	wantArch := append(vecArchiveRows(t, p, "a.pulse"), vecArchiveRows(t, p, "b.pulse")...)
	assertRows(t, "widened archive", vecArchiveRows(t, p, "arch.pulse"), wantArch)
}

// groupVecSchema: a parent key, a vector that is a function of the key
// (a group member), a constant vector (elidable) and a free vector.
func groupVecSchema() encoding.Schema {
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "centroid", Type: encoding.FieldTypeVecF64, Nullable: true},
		{Name: "const_v", Type: encoding.FieldTypeVecF32},
		{Name: "free", Type: encoding.FieldTypeVecF64},
		{Name: "batch", Type: encoding.FieldTypeU64},
	}, Vectors: []encoding.VectorField{
		{FieldIndex: 2, Dim: 3},
		{FieldIndex: 3, Dim: 2},
		{FieldIndex: 4, Dim: 2},
	}}
}

// groupVecRows: centroid depends on region only (null for region r2);
// const_v is constant across the whole cohort except when lastDiffers,
// in which case ONLY its last element moves on the final row — a
// whole-stride compare must see it.
func groupVecRows(n int, lastDiffers bool) []pulse.CohortRow {
	rows := make([]pulse.CohortRow, n)
	for r := range rows {
		g := (r / 4) % 3
		var centroid any
		if g != 2 {
			centroid = []float64{float64(g), float64(g) * 1.5, -float64(g)}
		}
		cv := []float32{7, 8}
		if lastDiffers && r == n-1 {
			cv = []float32{7, 9}
		}
		rows[r] = pulse.CohortRow{uint64(r + 1), fmt.Sprintf("r%d", g), centroid, cv,
			[]float64{float64(r), math.Sqrt(float64(r))}, uint64(3)}
	}
	return rows
}

// TestVectorGroups_GroupElideDedupRoundTrip: a vector can be a parent
// group member and an elided constant; the grouped twin decodes exactly
// like the flat cohort through the builder (import-time --group /
// --elide-constants), through Pulse.Dedup, and through a sharded
// grouped archive.
func TestVectorGroups_GroupElideDedupRoundTrip(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	rows := groupVecRows(36, false)
	want := renderRows(rows)
	groups := []pio.GroupDecl{{Key: []string{"region"}, Members: []string{"centroid"}}}

	vecArchiveFixture(t, p, "flat.pulse", groupVecSchema(), rows, pulse.CohortBuilderOptions{})
	assertRows(t, "flat", vecArchiveRows(t, p, "flat.pulse"), want)

	vecArchiveFixture(t, p, "grouped.pulse", groupVecSchema(), rows,
		pulse.CohortBuilderOptions{Groups: groups, ElideConstants: true, RatioFloor: 1})
	c, err := p.Open(ctx, "grouped.pulse")
	if err != nil {
		t.Fatal(err)
	}
	gs := c.Schema()
	if !gs.HasGroups() || len(gs.Groups) != 2 {
		t.Fatalf("grouped schema groups = %+v, want the region group plus the constant group", gs.Groups)
	}
	assertRows(t, "builder grouped+elided", vecArchiveRows(t, p, "grouped.pulse"), want)
	if fi, ff := afero.ReadFile(fs, "flat.pulse"); ff == nil {
		if gi, _ := afero.ReadFile(fs, "grouped.pulse"); len(gi) >= len(fi) {
			t.Fatalf("grouped cohort (%d B) is not smaller than flat (%d B)", len(gi), len(fi))
		}
	}

	// Retro-dedup of the flat cohort, groups + constant elision.
	dres, err := p.Dedup(ctx, "flat.pulse", pulse.DedupOptions{Groups: groups, ElideConstants: true, Out: "dedup.pulse", RatioFloor: 1})
	if err != nil {
		t.Fatalf("Dedup: %v", err)
	}
	if dres.StrideAfter >= dres.StrideBefore {
		t.Fatalf("dedup stride %d -> %d did not shrink", dres.StrideBefore, dres.StrideAfter)
	}
	assertRows(t, "dedup", vecArchiveRows(t, p, "dedup.pulse"), want)

	// A grouped, sharded archive.
	vecArchiveFixture(t, p, "sharded.pulse", groupVecSchema(), rows,
		pulse.CohortBuilderOptions{Groups: groups, RatioFloor: 1, Shards: &pulse.ShardSplit{MaxRecords: 10}})
	assertRows(t, "sharded grouped", vecArchiveRows(t, p, "sharded.pulse"), want)
}

// TestVectorGroups_ConstantDetectionComparesWholeStride: a vector whose
// LAST element differs on one row is not a constant — elision must
// compare the vector's whole stride, not a prefix of it.
func TestVectorGroups_ConstantDetectionComparesWholeStride(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	elided := func(lastDiffers bool) bool {
		t.Helper()
		src := fmt.Sprintf("flat_%v.pulse", lastDiffers)
		out := fmt.Sprintf("elided_%v.pulse", lastDiffers)
		rows := groupVecRows(200, lastDiffers)
		vecArchiveFixture(t, p, src, groupVecSchema(), rows, pulse.CohortBuilderOptions{})
		res, err := p.Dedup(ctx, src, pulse.DedupOptions{ElideConstants: true, Out: out})
		if err != nil {
			t.Fatalf("Dedup: %v", err)
		}
		if !slices.Contains(res.ElidedConstants, "batch") {
			t.Fatalf("%s: the scalar constant batch was not detected (elided %v)", out, res.ElidedConstants)
		}
		cv := slices.Contains(res.ElidedConstants, "const_v")
		if res.Rewritten {
			assertRows(t, out, vecArchiveRows(t, p, out), renderRows(rows))
		}
		return cv
	}
	if !elided(false) {
		t.Fatal("a vector constant across every row was not elided")
	}
	if elided(true) {
		t.Fatal("const_v was elided although its last element differs on the final row")
	}
}

// TestVectorSynth_EverySurface: every synth surface that used to refuse
// a vector field now carries it through the public facade — a spec
// declaring one (from-schema), profile capture of a single-file cohort
// and of a shard archive, and a tagged top-up whose source carries one.
func TestVectorSynth_EverySurface(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	vecArchiveFixture(t, p, "v.pulse", trailingVecSchema(encoding.FieldTypeVecF32, 3),
		trailingVecRows(encoding.FieldTypeVecF32, 3, 40, 0), pulse.CohortBuilderOptions{})
	vecArchiveFixture(t, p, "sh.pulse", trailingVecSchema(encoding.FieldTypeVecF64, 2),
		trailingVecRows(encoding.FieldTypeVecF64, 2, 40, 0), pulse.CohortBuilderOptions{Shards: &pulse.ShardSplit{MaxRecords: 15}})

	for _, ft := range []string{"vec_f32", "vec_f64"} {
		spec := &pulse.SynthSpec{RowCount: 5, Fields: []synth.FieldSpec{
			{Name: "id", Type: "u32", Distribution: synth.DistUniform, Params: map[string]any{"min": 0.0, "max": 100.0}},
			{Name: "emb", Type: ft, Distribution: "normal", Params: map[string]any{"mean": 0.0, "std": 1.0},
				Vector: &synth.VectorSpec{Dim: 4, Kind: "measure"}},
		}}
		if _, err := p.Synth(ctx, spec, "out_"+ft+".pulse", pulse.SynthOptions{}); err != nil {
			t.Fatalf("%s spec: %v", ft, err)
		}
		if n, err := p.CountRecords(ctx, "out_"+ft+".pulse"); err != nil || n != 5 {
			t.Fatalf("%s: %d rows (%v)", ft, n, err)
		}
	}
	for src, dim := range map[string]int{"v.pulse": 3, "sh.pulse": 2} {
		prof, err := p.Profile(ctx, src, pulse.ProfileOptions{IncludeStats: true})
		if err != nil {
			t.Fatalf("profile %s: %v", src, err)
		}
		vp := prof.Fields[1].Vector
		if vp == nil || vp.Dim != dim || vp.N != 40 || len(vp.Elements) != dim {
			t.Fatalf("profile %s vector section %+v", src, vp)
		}
		// The fixture's elements all rise with the row, so every pair
		// is captured near +1 — across shard boundaries too.
		if len(vp.Correlations) == 0 || vp.Correlations[0].Rho < 0.99 {
			t.Fatalf("profile %s correlations %+v", src, vp.Correlations)
		}
	}
	prof, err := p.Profile(ctx, "v.pulse", pulse.ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	top, _ := synth.SpecFromProfile(prof, 10)
	if _, err := p.Synth(ctx, top, "topped.pulse", pulse.SynthOptions{SourceCohort: "v.pulse"}); err != nil {
		t.Fatalf("top-up over a vector source: %v", err)
	}
	if n, err := p.CountRecords(ctx, "topped.pulse"); err != nil || n != 50 {
		t.Fatalf("top-up rows %d (%v), want 50", n, err)
	}
}
