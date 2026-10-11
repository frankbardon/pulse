package pulse_test

import (
	"context"
	stderrors "errors"
	"math"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Named vectors in expressions (U26 E2-S1): a Request.Vectors entry an
// ATTR_FORMULA, a FILTER_EXPRESSION or a total() argument names binds
// as a []float64 of its members — a null member is a NaN element, a
// row whose members are all null an all-NaN vector — and a nil formula
// result is a null attribute value. Every execution mode runs under the
// default projected decode, so a member the projection drops would
// silently read NaN and move every figure below.

const vexprMembers = 4

// vexprSchema: four nullable f64 members b1..b4, plus columns no
// expression reads (score, qty) so the projection has something to drop.
func vexprSchema() *encoding.Schema {
	fields := []encoding.Field{
		{Name: "b1", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "score", Type: encoding.FieldTypeF64},
		{Name: "b2", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "b3", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "qty", Type: encoding.FieldTypeU32},
		{Name: "b4", Type: encoding.FieldTypeF64, Nullable: true},
	}
	off := 0
	for i := range fields {
		fields[i].ByteOffset = off
		fields[i].CsvColumnIdx = i
		off += fields[i].Type.ByteSize()
	}
	return &encoding.Schema{Fields: fields}
}

// vexprMember is row r's member j (0-based): ok=false is a null. Every
// 17th row (r%17 == 8) has all four members null.
func vexprMember(r, j int) (float64, bool) {
	if r%17 == 8 || (r+j)%5 == 0 {
		return 0, false
	}
	return float64(r%10+j) + 0.25*float64(j), true
}

func vexprPayload(s *encoding.Schema, from, to int) []byte {
	stride := s.RecordByteSize()
	bm := s.BitmapByteSize()
	out := make([]byte, 0, (to-from)*stride)
	members := []int{0, 2, 3, 5} // schema index of b1..b4
	for r := from; r < to; r++ {
		rec := make([]byte, stride)
		bitmap := rec[stride-bm:]
		put := func(off int, v uint64, n int) {
			for i := 0; i < n; i++ {
				rec[off+i] = byte(v >> (8 * i))
			}
		}
		for j, fi := range members {
			if v, ok := vexprMember(r, j); ok {
				put(s.Fields[fi].ByteOffset, math.Float64bits(v), 8)
			} else {
				encoding.BitmapSetNull(bitmap, fi)
			}
		}
		put(s.Fields[1].ByteOffset, math.Float64bits(1000+float64(r)), 8)
		put(s.Fields[4].ByteOffset, uint64(r%7), 4)
		out = append(out, rec...)
	}
	return out
}

func writeVexprCohort(t testing.TB, fsys afero.Fs, path string, from, to int) {
	t.Helper()
	s := vexprSchema()
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, s); err != nil {
		t.Fatal(err)
	}
	buf = append(buf, vexprPayload(s, from, to)...)
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// vexprRow is one row's battery as the binding sees it.
func vexprRow(r int) []float64 {
	v := make([]float64, vexprMembers)
	for j := range v {
		if x, ok := vexprMember(r, j); ok {
			v[j] = x
		} else {
			v[j] = math.NaN()
		}
	}
	return v
}

func vexprStats(v []float64) (n int, sum, max float64) {
	max = math.Inf(-1)
	for _, x := range v {
		if math.IsNaN(x) {
			continue
		}
		n++
		sum += x
		max = math.Max(max, x)
	}
	return n, sum, max
}

// vexprWant is one request's expected figures over rows [0, rows).
type vexprWant struct {
	values map[string]float64 // Data[0][label]
	nNull  map[string]int     // Components.Aggregations[label].NNull
}

type vexprCase struct {
	name string
	req  func() *types.Request
	want func(rows int) vexprWant
}

func vexprBattery() []types.VectorSpec {
	return []types.VectorSpec{{Name: "battery", Fields: []string{"b*"}}}
}

func vexprCases() []vexprCase {
	return []vexprCase{
		{
			// ATTR_FORMULA + FILTER_EXPRESSION both reading the vector.
			name: "formula and filter",
			req: func() *types.Request {
				return &types.Request{
					Vectors:      vexprBattery(),
					Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "vcount(battery) > 0"}},
					Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "vmean(battery)", Label: "m"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "m", Label: "s_m"}, {Type: types.AGG_COUNT, Field: "qty", Label: "n"}},
				}
			},
			want: func(rows int) vexprWant {
				var s float64
				n := 0
				for r := 0; r < rows; r++ {
					c, sum, _ := vexprStats(vexprRow(r))
					if c > 0 {
						s += sum / float64(c)
						n++
					}
				}
				return vexprWant{values: map[string]float64{"s_m": s, "n": float64(n)}}
			},
		},
		{
			// A filter alone: the members must decode for the verdict.
			name: "filter only",
			req: func() *types.Request {
				return &types.Request{
					Vectors:      vexprBattery(),
					Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "vmean(battery) > 6"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "qty", Label: "n"}},
				}
			},
			want: func(rows int) vexprWant {
				n := 0
				for r := 0; r < rows; r++ {
					c, sum, _ := vexprStats(vexprRow(r))
					if c > 0 && sum/float64(c) > 6 {
						n++
					}
				}
				return vexprWant{values: map[string]float64{"n": float64(n)}}
			},
		},
		{
			// The owner's example: a nil result is a null attribute
			// value, skipped by the aggregation and counted in n_null.
			name: "nil result is null",
			req: func() *types.Request {
				return &types.Request{
					Vectors:      vexprBattery(),
					Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "vcount(battery) >= 3 ? vmean(battery) : nil", Label: "g"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "g", Label: "s_g"}},
				}
			},
			want: func(rows int) vexprWant {
				var s float64
				null := 0
				for r := 0; r < rows; r++ {
					c, sum, _ := vexprStats(vexprRow(r))
					if c >= 3 {
						s += sum / float64(c)
					} else {
						null++
					}
				}
				return vexprWant{values: map[string]float64{"s_g": s}, nNull: map[string]int{"s_g": null}}
			},
		},
		{
			// Nested ternary: two guards, nil only for the all-null rows.
			name: "nested ternary",
			req: func() *types.Request {
				return &types.Request{
					Vectors: vexprBattery(),
					Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA,
						Expression: "vcount(battery) >= 3 ? vmean(battery) : (vcount(battery) >= 1 ? vmax(battery) : nil)", Label: "h"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "h", Label: "s_h"}},
				}
			},
			want: func(rows int) vexprWant {
				var s float64
				null := 0
				for r := 0; r < rows; r++ {
					c, sum, mx := vexprStats(vexprRow(r))
					switch {
					case c >= 3:
						s += sum / float64(c)
					case c >= 1:
						s += mx
					default:
						null++
					}
				}
				return vexprWant{values: map[string]float64{"s_h": s}, nNull: map[string]int{"s_h": null}}
			},
		},
		{
			// total() arguments read the vector too, and a nil
			// argument row is skipped by the total.
			name: "total argument",
			req: func() *types.Request {
				return &types.Request{
					Vectors:   vexprBattery(),
					Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "vcount(battery) > 0"}},
					Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA,
						Expression: "vsum(battery) / total(vcount(battery) >= 3 ? vsum(battery) : nil)", Label: "share"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "share", Label: "s_share"}},
				}
			},
			want: func(rows int) vexprWant {
				var all, ge3 float64
				for r := 0; r < rows; r++ {
					c, sum, _ := vexprStats(vexprRow(r))
					if c > 0 {
						all += sum
					}
					if c >= 3 {
						ge3 += sum
					}
				}
				return vexprWant{values: map[string]float64{"s_share": all / ge3}}
			},
		},
	}
}

type vexprMode struct {
	name     string
	rows     int
	open     func(t *testing.T) (*pulse.Pulse, string)
	buffered bool // add a buffered-only attribute to force the buffered arm
}

func vexprModes() []vexprMode {
	const small = 96
	return []vexprMode{
		{name: "streaming", rows: small, open: func(t *testing.T) (*pulse.Pulse, string) {
			fsys := afero.NewMemMapFs()
			writeVexprCohort(t, fsys, "v.pulse", 0, small)
			p, err := pulse.New(pulse.Options{FS: fsys})
			if err != nil {
				t.Fatal(err)
			}
			return p, "v.pulse"
		}},
		{name: "buffered", rows: small, buffered: true, open: func(t *testing.T) (*pulse.Pulse, string) {
			fsys := afero.NewMemMapFs()
			writeVexprCohort(t, fsys, "v.pulse", 0, small)
			p, err := pulse.New(pulse.Options{FS: fsys})
			if err != nil {
				t.Fatal(err)
			}
			return p, "v.pulse"
		}},
		{name: "parallel_decode", rows: parityLargeRows, open: func(t *testing.T) (*pulse.Pulse, string) {
			dir := t.TempDir()
			writeVexprCohort(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"), 0, parityLargeRows)
			p, err := pulse.New(pulse.Options{DataDir: dir, DecodeWorkers: 4})
			if err != nil {
				t.Fatal(err)
			}
			return p, "large.pulse"
		}},
		{name: "shard_archive", rows: 120, open: func(t *testing.T) (*pulse.Pulse, string) {
			fsys := afero.NewMemMapFs()
			shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
			for i, path := range shards {
				writeVexprCohort(t, fsys, path, i*40, (i+1)*40)
			}
			p, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: 3})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
				t.Fatal(err)
			}
			return p, "archive.pulse"
		}},
	}
}

func vexprClose(got, want float64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	return math.Abs(got-want) <= 1e-9*math.Max(1, math.Abs(want))
}

func vexprFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// TestVectorExpr_BindsUnderProjection: every consumer, every execution
// mode, the default projected decode — the figures equal the oracle
// computed from the generated rows, and predict accepts the request
// with the same streamability as before (ATTR_FORMULA stays row-local).
func TestVectorExpr_BindsUnderProjection(t *testing.T) {
	ctx := context.Background()
	for _, m := range vexprModes() {
		t.Run(m.name, func(t *testing.T) {
			p, cohort := m.open(t)
			for _, c := range vexprCases() {
				t.Run(c.name, func(t *testing.T) {
					req := c.req()
					req.Cohort = &types.Cohort{Filename: cohort}
					// A column read beside the vector keeps the
					// projection narrow (an empty one decodes wide).
					req.Aggregations = append(req.Aggregations,
						&types.Aggregation{Type: types.AGG_SUM, Field: "qty", Label: "s_qty"})
					if m.buffered {
						req.Attributes = append(req.Attributes,
							&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"})
					}
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					want := c.want(m.rows)
					for label, w := range want.values {
						got, ok := vexprFloat(resp.Data[0][label])
						if !ok || !vexprClose(got, w) {
							t.Errorf("%s = %v, want %v", label, resp.Data[0][label], w)
						}
					}
					for label, w := range want.nNull {
						found := false
						if resp.Components != nil {
							for _, a := range resp.Components.Aggregations {
								if a.Label == label {
									found = true
									if a.NNull != w {
										t.Errorf("%s n_null = %d, want %d", label, a.NNull, w)
									}
								}
							}
						}
						if !found {
							t.Errorf("no components for %s", label)
						}
					}
					res, err := p.Predict(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					if !res.Valid {
						t.Errorf("predict refuses a request Process runs")
					}
				})
			}
		})
	}
}

// TestVectorExpr_NameCollisionRefused: a vector named like a schema
// field is refused by Process and predict alike, before any record.
func TestVectorExpr_NameCollisionRefused(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 20)
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "v.pulse"},
		Vectors:      []types.VectorSpec{{Name: "score", Fields: []string{"b1", "b2"}}},
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "vmean(score)", Label: "m"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "m", Label: "s"}},
	}
	_, err = p.Process(ctx, req)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_VECTOR_NAME_COLLISION {
		t.Fatalf("Process err = %v, want %s", err, perr.PULSE_VECTOR_NAME_COLLISION)
	}
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	env, _ := p.PredictBytes(ctx, data, req)
	if env == nil || len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_VECTOR_NAME_COLLISION) {
		t.Errorf("predict errors = %+v, want %s", env, perr.PULSE_VECTOR_NAME_COLLISION)
	}
}
