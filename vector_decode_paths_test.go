package pulse_test

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Mixed-layout property test for vector fields: random schemas with
// vec_f32 / vec_f64 fields at the first, a middle and the last position,
// nullable and not, written by the public cohort builder as an
// ungrouped single file, a grouped single file (a vector inside a parent
// group) and a shard archive, then read back through every decode path
// an embedder reaches — Cohort.Reader, Sample, point lookup, and Process
// driving an extension aggregator that reads extend.Record.VectorValue
// on the streaming reuse decoder (run-skip, projected plan and full
// stride), the buffered decoder and the per-shard reducer. Every path
// must return the stored elements exactly (NaN included) and the null
// state exactly.

// vecProp is one generated cohort: its schema, the builder options that
// make the layout variant, and the ground-truth rows (CohortRow form).
type vecProp struct {
	schema encoding.Schema
	opts   pulse.CohortBuilderOptions
	rows   []pulse.CohortRow
	vecs   []int // positions of the vector fields
	idPos  int
	grpPos int
}

// vecPropSchema lays out a random schema: vector fields first, middle and
// last; an id (u32, the lookup key) and a parent-key categorical in
// between, plus a few scalar neighbours of the awkward kinds (bit-packed,
// nullable f64).
func vecPropSchema(rng *rand.Rand) (encoding.Schema, []int, int, int) {
	vecType := func() encoding.FieldType {
		if rng.Intn(2) == 0 {
			return encoding.FieldTypeVecF32
		}
		return encoding.FieldTypeVecF64
	}
	middle := []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "grp", Type: encoding.FieldTypeCategoricalU8},
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "flag", Type: encoding.FieldTypePackedBool, Nullable: rng.Intn(2) == 0},
		{Name: "nib", Type: encoding.FieldTypeU4},
		{Name: "v_mid", Type: vecType(), Nullable: rng.Intn(2) == 0},
	}
	rng.Shuffle(len(middle), func(i, j int) { middle[i], middle[j] = middle[j], middle[i] })
	fields := append([]encoding.Field{{Name: "v_first", Type: vecType(), Nullable: rng.Intn(2) == 0}}, middle...)
	fields = append(fields, encoding.Field{Name: "v_last", Type: vecType(), Nullable: true})
	var vecs []int
	var vf []encoding.VectorField
	idPos, grpPos := -1, -1
	for i, f := range fields {
		switch {
		case f.Type.IsVector():
			vecs = append(vecs, i)
			vf = append(vf, encoding.VectorField{FieldIndex: i, Dim: encoding.MinVectorDim + rng.Intn(9)})
		case f.Name == "id":
			idPos = i
		case f.Name == "grp":
			grpPos = i
		}
	}
	return encoding.Schema{Fields: fields, Vectors: vf}, vecs, idPos, grpPos
}

// vecPropElems draws dim elements a vec_f32 can hold exactly, with NaN
// and ±Inf mixed in.
func vecPropElems(rng *rand.Rand, dim int) []float64 {
	out := make([]float64, dim)
	for k := range out {
		switch rng.Intn(12) {
		case 0:
			out[k] = math.NaN()
		case 1:
			out[k] = math.Inf(1 - 2*rng.Intn(2))
		case 2:
			out[k] = 0
		default:
			out[k] = float64(float32(rng.NormFloat64() * 100))
		}
	}
	return out
}

func vecCell(ft encoding.FieldType, v []float64) any {
	if ft == encoding.FieldTypeVecF64 {
		return v
	}
	out := make([]float32, len(v))
	for k, x := range v {
		out[k] = float32(x)
	}
	return out
}

// buildVecProp generates the rows. The parent key grp runs in blocks so
// a grouped variant has repeating entries and the run-skip decoder sees
// unchanged spans; v_mid is a function of grp (it can be a group
// member), every other vector repeats in short runs and is null on some
// rows when nullable.
func buildVecProp(t *testing.T, rng *rand.Rand, variant string, nRows int) *vecProp {
	t.Helper()
	sch, vecs, idPos, grpPos := vecPropSchema(rng)
	p := &vecProp{schema: sch, vecs: vecs, idPos: idPos, grpPos: grpPos}
	switch variant {
	case "grouped":
		p.opts.Groups = []pio.GroupDecl{{Key: []string{"grp"}, Members: []string{"v_mid", "nib"}}}
		p.opts.RatioFloor = 1
	case "sharded":
		p.opts.Shards = &pulse.ShardSplit{MaxRecords: nRows/3 + 1}
	}
	perGrp := map[int][]float64{}
	perGrpNull := map[int]bool{}
	var prev = make(map[int][]float64)
	for r := 0; r < nRows; r++ {
		row := make(pulse.CohortRow, len(sch.Fields))
		g := (r / 7) % 5
		for i, f := range sch.Fields {
			switch {
			case f.Name == "id":
				row[i] = uint64(r + 1)
			case f.Name == "grp":
				row[i] = "g" + strconv.Itoa(g)
			case f.Name == "x":
				if r%5 != 3 {
					row[i] = float64(r) / 4
				}
			case f.Name == "flag":
				if !f.Nullable || r%4 != 1 {
					row[i] = r%3 == 0
				}
			case f.Name == "nib":
				row[i] = uint64(g)
			case f.Name == "v_mid":
				dim := sch.VectorDim(i)
				if _, ok := perGrp[g]; !ok {
					perGrp[g] = vecPropElems(rng, dim)
					perGrpNull[g] = f.Nullable && g == 2
				}
				if !perGrpNull[g] {
					row[i] = vecCell(f.Type, perGrp[g])
				}
			case f.Type.IsVector():
				dim := sch.VectorDim(i)
				if f.Nullable && rng.Intn(5) == 0 {
					continue
				}
				v := prev[i]
				if v == nil || rng.Intn(3) == 0 {
					v = vecPropElems(rng, dim)
					prev[i] = v
				}
				row[i] = vecCell(f.Type, v)
			}
		}
		p.rows = append(p.rows, row)
	}
	return p
}

// widen is a CohortRow vector cell as float64 elements (nil for null).
func widen(cell any) []float64 {
	switch v := cell.(type) {
	case []float64:
		return v
	case []float32:
		out := make([]float64, len(v))
		for k, x := range v {
			out[k] = float64(x)
		}
		return out
	}
	return nil
}

// sameElems compares element-wise, NaN equal to NaN.
func sameElems(a, b []float64) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for k := range a {
		if a[k] != b[k] && !(math.IsNaN(a[k]) && math.IsNaN(b[k])) {
			return false
		}
	}
	return true
}

// vecFingerprint folds one row's vector into an integer-valued float64
// that is exact under any summation order (each term < 2^24, far below
// 2^53 even summed over every row): a null row contributes 1, a present
// one 2 plus a hash of its element bits.
func vecFingerprint(v []float64, null bool) float64 {
	if null {
		return 1
	}
	var h uint64 = 1469598103934665603
	for _, x := range v {
		bits := math.Float64bits(x)
		if math.IsNaN(x) {
			bits = 0x7ff8000000000001 // every NaN alike
		}
		h ^= bits
		h *= 1099511628211
	}
	return 2 + float64(h&0xFFFFFF)
}

// vecFP is an extension aggregator that reads a vector through
// extend.Record only. It fails the run if NumericValue ever answers for
// the vector, or if VectorValue and IsNull disagree.
type vecFP struct{ sum float64 }

func (a *vecFP) row(rec extend.Record, field string) error {
	if v, ok := rec.NumericValue(field); ok {
		return fmt.Errorf("NumericValue(%s) = %v, true on a vector field", field, v)
	}
	v, ok := rec.VectorValue(field)
	if ok == rec.IsNull(field) {
		return fmt.Errorf("VectorValue ok=%v but IsNull=%v", ok, rec.IsNull(field))
	}
	if ok {
		v[0] = math.Pi // a fresh copy: must not disturb the next read
		if again, _ := rec.VectorValue(field); again[0] == math.Pi && len(v) > 0 {
			return fmt.Errorf("VectorValue aliases the record")
		}
		v, _ = rec.VectorValue(field)
	}
	a.sum += vecFingerprint(v, !ok)
	return nil
}

func (a *vecFP) Aggregate(rows extend.Rows, field string) (float64, error) {
	for i := 0; i < rows.Len(); i++ {
		if err := a.row(rows.At(i), field); err != nil {
			return 0, err
		}
	}
	return a.sum, nil
}
func (a *vecFP) UpdateRow(rec extend.Record, field string) error { return a.row(rec, field) }
func (a *vecFP) Finalize() (float64, error)                      { return a.sum, nil }
func (a *vecFP) Merge(o extend.OnlineAggregator) error {
	a.sum += o.(*vecFP).sum
	return nil
}

func newVecFP(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
	return &vecFP{}, nil
}

func TestVectorFields_EveryDecodePathAgrees(t *testing.T) {
	ctx := context.Background()
	exts := pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
		{Name: "AGG_VECT_FP_ONLINE", Description: "Vector fingerprint, streaming.", Factory: newVecFP, Streamable: true, Mergeable: true},
		{Name: "AGG_VECT_FP_BUFFERED", Description: "Vector fingerprint, buffered.", Factory: newVecFP},
	}}
	for seed := int64(1); seed <= 6; seed++ {
		for _, variant := range []string{"single", "grouped", "sharded"} {
			t.Run(fmt.Sprintf("seed%d/%s", seed, variant), func(t *testing.T) {
				rng := rand.New(rand.NewSource(seed))
				const nRows = 140
				prop := buildVecProp(t, rng, variant, nRows)
				fs := afero.NewMemMapFs()
				p, err := pulse.New(pulse.Options{FS: fs, Extensions: exts})
				if err != nil {
					t.Fatal(err)
				}
				b, err := p.NewCohortBuilder(ctx, "v.pulse", prop.schema, prop.opts)
				if err != nil {
					t.Fatal(err)
				}
				for r, row := range prop.rows {
					if err := b.Append(row); err != nil {
						t.Fatalf("append row %d: %v", r, err)
					}
				}
				res, err := b.Close()
				if err != nil {
					t.Fatal(err)
				}
				if variant == "grouped" {
					// The vector really is a group member, stored once
					// per entry in the schema block, not in the row.
					if len(res.Groups) != 1 || res.Groups[0].Verdict == "dropped_too_narrow" ||
						!slices.Contains(res.Groups[0].Fields, "v_mid") || res.Groups[0].EntryCount != 5 {
						t.Fatalf("grouped variant: groups %+v, warnings %v", res.Groups, res.Warnings)
					}
				}
				if variant == "sharded" && len(res.Shards) < 3 {
					t.Fatalf("sharded variant wrote %d shards", len(res.Shards))
				}
				checkVecReader(t, p, prop)
				checkVecSample(t, p, prop)
				if variant != "sharded" { // point lookup refuses archives
					checkVecLookup(t, p, prop)
				}
				checkVecProcess(t, prop, fs, exts)
			})
		}
	}
}

func checkVecReader(t *testing.T, p *pulse.Pulse, prop *vecProp) {
	t.Helper()
	c, err := p.Open(context.Background(), "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	rd, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	if rd.Len() != int64(len(prop.rows)) {
		t.Fatalf("reader Len = %d, want %d", rd.Len(), len(prop.rows))
	}
	for r, want := range prop.rows {
		got, err := rd.RecordAt(int64(r))
		if err != nil {
			t.Fatalf("RecordAt(%d): %v", r, err)
		}
		for _, i := range prop.vecs {
			f := prop.schema.Fields[i]
			if want[i] == nil {
				if got[i] != nil {
					t.Fatalf("reader row %d %s = %#v, want nil (null)", r, f.Name, got[i])
				}
				continue
			}
			// The cell keeps the native element type.
			if fmt.Sprintf("%T", got[i]) != fmt.Sprintf("%T", want[i]) || !sameElems(widen(got[i]), widen(want[i])) {
				t.Fatalf("reader row %d %s = %#v, want %#v", r, f.Name, got[i], want[i])
			}
		}
		if got[prop.idPos] != want[prop.idPos] {
			t.Fatalf("reader row %d id = %v, want %v (neighbour misaligned)", r, got[prop.idPos], want[prop.idPos])
		}
	}
}

// checkVecRowMap checks a name-keyed row (Sample, Lookup) against row r.
func checkVecRowMap(t *testing.T, where string, prop *vecProp, got map[string]any) {
	t.Helper()
	id, ok := got["id"].(float64)
	if !ok {
		t.Fatalf("%s: row has no numeric id: %#v", where, got)
	}
	want := prop.rows[int(id)-1]
	for _, i := range prop.vecs {
		f := prop.schema.Fields[i]
		cell, present := got[f.Name]
		if want[i] == nil {
			if present {
				t.Fatalf("%s id %v: %s = %#v, want absent (null)", where, id, f.Name, cell)
			}
			continue
		}
		v, isVec := cell.([]float64)
		if !isVec || !sameElems(v, widen(want[i])) {
			t.Fatalf("%s id %v: %s = %#v, want %v", where, id, f.Name, cell, widen(want[i]))
		}
	}
	if g, _ := got["grp"].(string); g != want[prop.grpPos] {
		t.Fatalf("%s id %v: grp = %v, want %v (neighbour misaligned)", where, id, got["grp"], want[prop.grpPos])
	}
}

func checkVecSample(t *testing.T, p *pulse.Pulse, prop *vecProp) {
	t.Helper()
	rows, err := p.Sample(context.Background(), "v.pulse", len(prop.rows))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(prop.rows) {
		t.Fatalf("sample returned %d rows, want %d", len(rows), len(prop.rows))
	}
	for _, row := range rows {
		checkVecRowMap(t, "sample", prop, row)
	}
}

func checkVecLookup(t *testing.T, p *pulse.Pulse, prop *vecProp) {
	t.Helper()
	ctx := context.Background()
	if _, err := p.BuildIndex(ctx, "v.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	for r := 0; r < len(prop.rows); r += 9 {
		res, err := p.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Field: "id", Value: strconv.Itoa(r + 1)})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 1 {
			t.Fatalf("lookup id %d: %d rows", r+1, len(res.Rows))
		}
		checkVecRowMap(t, "lookup", prop, res.Rows[0])
	}
	// A projected lookup returning only the last vector.
	last := prop.schema.Fields[prop.vecs[len(prop.vecs)-1]].Name
	res, err := p.Lookup(ctx, &types.LookupRequest{Cohort: &types.Cohort{Filename: "v.pulse"}, Field: "id", Value: "4",
		ReturnColumns: []string{last}})
	if err != nil {
		t.Fatal(err)
	}
	want := widen(prop.rows[3][prop.vecs[len(prop.vecs)-1]])
	got, present := res.Rows[0][last]
	if want == nil {
		if present {
			t.Fatalf("projected lookup %s = %#v, want absent", last, got)
		}
	} else if v, _ := got.([]float64); !sameElems(v, want) {
		t.Fatalf("projected lookup %s = %#v, want %v", last, got, want)
	}
}

// checkVecProcess runs the fingerprint aggregators grouped by grp under
// several engine configurations and compares each group to the
// ground-truth fingerprint.
func checkVecProcess(t *testing.T, prop *vecProp, fs afero.Fs, exts pulse.Extensions) {
	t.Helper()
	ctx := context.Background()
	want := map[string]map[string]float64{} // field -> grp -> fp
	for _, i := range prop.vecs {
		name := prop.schema.Fields[i].Name
		want[name] = map[string]float64{}
		for _, row := range prop.rows {
			g := row[prop.grpPos].(string)
			want[name][g] += vecFingerprint(widen(row[i]), row[i] == nil)
		}
	}
	configs := map[string]pulse.Options{
		"default":            {FS: fs, Extensions: exts},
		"no-projection":      {FS: fs, Extensions: exts, DisableProjection: true},
		"serial-shards":      {FS: fs, Extensions: exts, ShardWorkers: 1},
		"serial-decode":      {FS: fs, Extensions: exts, DecodeWorkers: 1},
		"projection-on-many": {FS: fs, Extensions: exts, ShardWorkers: 4},
	}
	names := make([]string, 0, len(configs))
	for n := range configs {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, cfg := range names {
		p, err := pulse.New(configs[cfg])
		if err != nil {
			t.Fatal(err)
		}
		for _, agg := range []types.AggregationType{"AGG_VECT_FP_ONLINE", "AGG_VECT_FP_BUFFERED"} {
			for _, i := range prop.vecs {
				field := prop.schema.Fields[i].Name
				resp, err := p.Process(ctx, &types.Request{
					Cohort:       &types.Cohort{Filename: "v.pulse"},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grp"}},
					Aggregations: []*types.Aggregation{{Type: agg, Field: field, Label: "fp"}},
				})
				if err != nil {
					t.Fatalf("%s %s %s: %v", cfg, agg, field, err)
				}
				if len(resp.Data) != len(want[field]) {
					t.Fatalf("%s %s %s: %d groups, want %d", cfg, agg, field, len(resp.Data), len(want[field]))
				}
				for _, row := range resp.Data {
					g, _ := row["grp"].(string)
					got, _ := row["fp"].(float64)
					if got != want[field][g] {
						t.Fatalf("%s %s %s grp %s: fingerprint %v, want %v", cfg, agg, field, g, got, want[field][g])
					}
				}
			}
		}
	}
}
