package service

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// MatrixSpec.encoding "records" end to end: every built-in operator's
// records result holds exactly the full encoding's numbers (the wire
// read back through MatrixValues.UnmarshalJSON, bit for bit), every
// matrix part — primary, each auxiliary, rectangular ones — is written
// as rows, and one golden per operator pins the wire shape.

var updateRecordsGolden = flag.Bool("update-records-golden", false, "rewrite testdata/matrix_records/*.json from the runtime")

const recordsGoldenDir = "testdata/matrix_records"

// recordsCohort: a categorical grp (three buckets) and four F64
// members loading on one synthetic factor, so every decomposition is
// well conditioned; y4 is null on a few rows (a listwise drop and a
// pairwise auxiliary.n that varies).
func recordsCohort(t *testing.T) *fs.Config {
	t.Helper()
	grp := encoding.NewDictionary()
	for _, v := range []string{"g1", "g2", "g3"} {
		if _, err := grp.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"y1", "y2", "y3", "y4"}
	fields := []encoding.Field{{Name: "grp", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: grp}}
	for i, n := range names {
		fields = append(fields, encoding.Field{Name: n, Type: encoding.FieldTypeF64, ByteOffset: 1 + 8*i, CsvColumnIdx: 1 + i, Nullable: true})
	}
	schema := &encoding.Schema{Fields: fields}
	const n = 240
	recs := make([][]uint64, n)
	for i := range recs {
		h := uint64(i)*0x9E3779B97F4A7C15 + 0x2545F4914F6CDD1D
		u := func(k uint64) float64 {
			v := (h ^ (h >> 31) ^ k*0xBF58476D1CE4E5B9) * 0x94D049BB133111EB
			return float64(v>>11)/(1<<53) - 0.5
		}
		f := 2 * u(1)
		ys := []float64{f + 0.5*u(2), 0.8*f + 0.6*u(3), 0.6*f + 0.8*u(4), 0.4*f + 0.9*u(5)}
		recs[i] = []uint64{uint64(i % 3)}
		for _, y := range ys {
			recs[i] = append(recs[i], math.Float64bits(y))
		}
	}
	data := writeNullablePulse(t, schema, recs, func(r, f int) bool { return f == 4 && r%17 == 3 })
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "r.pulse", data, 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// recordsSpecs is one spec per built-in operator, with the params that
// make it emit auxiliaries (rectangular ones included).
func recordsSpecs() map[types.MatrixType]types.MatrixSpec {
	members := []string{"y1", "y2", "y3", "y4"}
	spec := func(t types.MatrixType, params string) types.MatrixSpec {
		s := types.MatrixSpec{Name: "m", Type: t, Fields: members}
		if params != "" {
			s.Params = json.RawMessage(params)
		}
		return s
	}
	out := map[types.MatrixType]types.MatrixSpec{
		types.MAT_COVARIANCE:          spec(types.MAT_COVARIANCE, `{"missing": "pairwise"}`),
		types.MAT_CORRELATION:         spec(types.MAT_CORRELATION, `{"inference": true, "summary": {"top_pairs": 2}}`),
		types.MAT_PARTIAL_CORRELATION: spec(types.MAT_PARTIAL_CORRELATION, `{"control": "all"}`),
		types.MAT_RELIABILITY:         spec(types.MAT_RELIABILITY, ""),
		types.MAT_PCA:                 spec(types.MAT_PCA, `{"components": 2, "rotation": "varimax"}`),
		types.MAT_COLLINEARITY:        spec(types.MAT_COLLINEARITY, ""),
		types.MAT_FACTOR:              spec(types.MAT_FACTOR, `{"factors": 1}`),
		types.MAT_DISTANCE:            spec(types.MAT_DISTANCE, `{"between": {"type": "GROUP_CATEGORY", "field": "grp"}}`),
		types.MAT_SEM:                 spec(types.MAT_SEM, ""),
	}
	return out
}

func recordsRun(t *testing.T, cfg *fs.Config, spec types.MatrixSpec, enc types.MatrixEncoding) types.MatrixResult {
	t.Helper()
	spec.Encoding = enc
	resp := processMatrices(t, cfg, &types.Request{Cohort: &types.Cohort{Filename: "r.pulse"}, Matrices: []types.MatrixSpec{spec}})
	m := resp.Matrices[0]
	if m.Error != nil {
		t.Fatalf("%s %s: slot failed: %+v", spec.Type, enc, m.Error)
	}
	return m
}

// sameBits compares two cells bit for bit, every NaN equal.
func sameBits(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

func compareRecordsPart(t *testing.T, part string, got, want *types.MatrixValues) {
	t.Helper()
	if got == nil || want == nil {
		t.Fatalf("%s: got %v, want %v", part, got, want)
	}
	if got.Encoding != types.MatrixEncodingRecords || got.Kind != want.Kind {
		t.Errorf("%s: encoding %q kind %q, want records / %q", part, got.Encoding, got.Kind, want.Kind)
	}
	if !reflect.DeepEqual(got.RowKeys, want.RowKeys) || !reflect.DeepEqual(got.ColumnKeys, want.ColumnKeys) || !reflect.DeepEqual(got.Labels, want.Labels) {
		t.Fatalf("%s: keys %v × %v, want %v × %v", part, got.RowKeys, got.ColumnKeys, want.RowKeys, want.ColumnKeys)
	}
	if len(got.Values) != len(want.Values) {
		t.Fatalf("%s: %d rows, want %d", part, len(got.Values), len(want.Values))
	}
	for r := range want.Values {
		if len(got.Values[r]) != len(want.Values[r]) {
			t.Fatalf("%s row %d: %d cells, want %d", part, r, len(got.Values[r]), len(want.Values[r]))
		}
		for c := range want.Values[r] {
			if !sameBits(got.Values[r][c], want.Values[r][c]) {
				t.Errorf("%s[%d][%d] = %v, full %v", part, r, c, got.Values[r][c], want.Values[r][c])
			}
		}
	}
}

// TestMatrixRecords_RoundTripsFull: for every built-in operator the
// records wire, read back, carries exactly the full encoding's keys and
// bits in every part; every other figure is untouched.
func TestMatrixRecords_RoundTripsFull(t *testing.T) {
	cfg := recordsCohort(t)
	specs := recordsSpecs()
	for _, typ := range types.AllMatrixTypes() {
		spec, ok := specs[typ]
		if !ok {
			t.Fatalf("no records spec for %s", typ)
		}
		t.Run(string(typ), func(t *testing.T) {
			full := recordsRun(t, cfg, spec, "")
			rec := recordsRun(t, cfg, spec, types.MatrixEncodingRecords)
			wire, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			var raw struct {
				Primary   map[string]json.RawMessage            `json:"primary"`
				Auxiliary map[string]map[string]json.RawMessage `json:"auxiliary"`
			}
			if err := json.Unmarshal(wire, &raw); err != nil {
				t.Fatal(err)
			}
			parts := map[string]map[string]json.RawMessage{"primary": raw.Primary}
			for k, v := range raw.Auxiliary {
				parts["auxiliary."+k] = v
			}
			for name, p := range parts {
				for _, k := range []string{"row_keys", "column_keys", "values"} {
					if _, ok := p[k]; ok {
						t.Errorf("%s: records wire carries %q", name, k)
					}
				}
				for _, k := range []string{"kind", "encoding", "columns", "rows"} {
					if _, ok := p[k]; !ok {
						t.Errorf("%s: records wire lacks %q", name, k)
					}
				}
			}
			var back types.MatrixResult
			if err := json.Unmarshal(wire, &back); err != nil {
				t.Fatal(err)
			}
			compareRecordsPart(t, "primary", back.Primary, full.Primary)
			if len(back.Auxiliary) != len(full.Auxiliary) {
				t.Fatalf("auxiliary %d parts, full %d", len(back.Auxiliary), len(full.Auxiliary))
			}
			for k, want := range full.Auxiliary {
				compareRecordsPart(t, "auxiliary."+k, back.Auxiliary[k], want)
			}
			if !reflect.DeepEqual(rec.Vectors, full.Vectors) || len(rec.Scalars) != len(full.Scalars) || !reflect.DeepEqual(rec.Warnings, full.Warnings) {
				t.Errorf("non-matrix figures differ under records")
			}
			for k, v := range full.Scalars {
				if !sameBits(rec.Scalars[k], v) {
					t.Errorf("scalars.%s = %v, full %v", k, rec.Scalars[k], v)
				}
			}
			if typ == types.MAT_SEM {
				est := back.Auxiliary["estimates"]
				params, _ := full.Vectors["parameter"].([]string)
				free, _ := full.Vectors["free"].([]bool)
				if len(est.RecordFields) != len(est.RowKeys) || len(params) != len(est.RowKeys) {
					t.Fatalf("estimates record fields %d, rows %d, parameters %d", len(est.RecordFields), len(est.RowKeys), len(params))
				}
				for r, f := range est.RecordFields {
					key := f["lval"].(string) + " " + f["op"].(string) + " " + f["rval"].(string)
					if key != est.RowKeys[r] || f["parameter"] != params[r] || f["free"] != free[r] {
						t.Errorf("estimates row %d fields %v, key %q parameter %q free %v", r, f, est.RowKeys[r], params[r], free[r])
					}
				}
			}
		})
	}
}

// TestMatrixRecords_Golden pins each built-in operator's records wire
// (numbers rounded to 1e-9, so the pin is the layout, not the last
// bits of a decomposition; iterative fits within recordsGoldenTol).
// Regenerate with
// `go test ./internal/service/ -run TestMatrixRecords_Golden -update-records-golden`.
func TestMatrixRecords_Golden(t *testing.T) {
	cfg := recordsCohort(t)
	specs := recordsSpecs()
	for _, typ := range types.AllMatrixTypes() {
		t.Run(string(typ), func(t *testing.T) {
			rec := recordsRun(t, cfg, specs[typ], types.MatrixEncodingRecords)
			wire, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			got := normalizeRecordsWire(t, wire)
			path := filepath.Join(recordsGoldenDir, strings.ToLower(string(typ))+".json")
			if *updateRecordsGolden {
				if err := os.MkdirAll(recordsGoldenDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (regenerate with -update-records-golden)", err)
			}
			if tol, ok := recordsGoldenTol[typ]; ok {
				for _, d := range diffRecordsWire(t, got, want, tol) {
					t.Errorf("%s drifted from the runtime: %s", path, d)
				}
				return
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s drifted from the runtime:\n%s", path, got)
			}
		})
	}
}

// recordsGoldenTol: operators whose numbers come from an iterative fit
// compare numbers within a tolerance instead of byte for byte. The SLSQP
// stop of MAT_SEM moves with floating-point contraction (Go fuses
// multiply-add on arm64, not on amd64), so its figures differ across
// architectures by up to ~5e-8 (relative, or absolute below 1); 1e-6
// leaves headroom while still catching any real change of value.
// Everything else (keys, columns, row order, nulls, the lval / op /
// rval / parameter / free fields, encoding, kind) stays exact. The
// closed-form operators are stable across architectures at the golden's
// 1e-9 rounding and stay byte-exact.
var recordsGoldenTol = map[types.MatrixType]float64{
	types.MAT_SEM: 1e-6,
}

// diffRecordsWire compares two normalized wires: structure and every
// non-number exactly, numbers to |got-want| <= tol*max(1, |want|).
func diffRecordsWire(t *testing.T, got, want []byte, tol float64) []string {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	var diffs []string
	var walk func(path string, g, w any)
	walk = func(path string, g, w any) {
		switch w := w.(type) {
		case float64:
			gf, ok := g.(float64)
			if !ok || math.Abs(gf-w) > tol*math.Max(1, math.Abs(w)) {
				diffs = append(diffs, fmt.Sprintf("%s = %v, want %v", path, g, w))
			}
		case []any:
			ga, ok := g.([]any)
			if !ok || len(ga) != len(w) {
				diffs = append(diffs, fmt.Sprintf("%s = %v, want %v", path, g, w))
				return
			}
			for i := range w {
				walk(fmt.Sprintf("%s[%d]", path, i), ga[i], w[i])
			}
		case map[string]any:
			gm, ok := g.(map[string]any)
			if !ok || len(gm) != len(w) {
				diffs = append(diffs, fmt.Sprintf("%s = %v, want %v", path, g, w))
				return
			}
			for k, wv := range w {
				gv, ok := gm[k]
				if !ok {
					diffs = append(diffs, fmt.Sprintf("%s.%s missing", path, k))
					continue
				}
				walk(path+"."+k, gv, wv)
			}
		default:
			if !reflect.DeepEqual(g, w) {
				diffs = append(diffs, fmt.Sprintf("%s = %v, want %v", path, g, w))
			}
		}
	}
	walk("", g, w)
	sort.Strings(diffs)
	return diffs
}

// normalizeRecordsWire re-encodes wire indented with every number
// rounded to 1e-9 (negative zero folded).
func normalizeRecordsWire(t *testing.T, wire []byte) []byte {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(wire))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	var walk func(any) any
	walk = func(x any) any {
		switch x := x.(type) {
		case json.Number:
			f, err := x.Float64()
			if err != nil {
				t.Fatal(err)
			}
			r := math.Round(f*1e9) / 1e9
			if r == 0 {
				r = 0
			}
			return r
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				x[k] = walk(x[k])
			}
		}
		return x
	}
	out, err := json.MarshalIndent(walk(v), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

// TestMatrixRecords_FullAndUpperUnchanged: the records value is opt-in;
// full and upper keep their wire layout (row_keys / column_keys /
// values, no rows).
func TestMatrixRecords_FullAndUpperUnchanged(t *testing.T) {
	cfg := recordsCohort(t)
	spec := recordsSpecs()[types.MAT_CORRELATION]
	for _, enc := range []types.MatrixEncoding{"", types.MatrixEncodingFull, types.MatrixEncodingUpper} {
		m := recordsRun(t, cfg, spec, enc)
		b, err := json.Marshal(m.Primary)
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]json.RawMessage
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		if _, ok := p["rows"]; ok {
			t.Errorf("encoding %q writes rows", enc)
		}
		if _, ok := p["values"]; !ok {
			t.Errorf("encoding %q lacks values", enc)
		}
	}
}

// TestMatrixRecords_KeyCollision: a member named "key" is refused
// before the run (predict and process alike); a data-derived column of
// that name (a MAT_DISTANCE bucket label) fails the slot alone.
func TestMatrixRecords_KeyCollision(t *testing.T) {
	grp := encoding.NewDictionary()
	for _, v := range []string{"key", "other"} {
		if _, err := grp.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "grp", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: grp},
		{Name: "key", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 9, CsvColumnIdx: 2},
	}}
	recs := make([][]uint64, 20)
	for i := range recs {
		recs[i] = []uint64{uint64(i % 2), math.Float64bits(float64(i)), math.Float64bits(float64(i * i % 7))}
	}
	data := writeNullablePulse(t, schema, recs, func(int, int) bool { return false })
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "k.pulse", data, 0o644); err != nil {
		t.Fatal(err)
	}
	req := &types.Request{Cohort: &types.Cohort{Filename: "k.pulse"}, Matrices: []types.MatrixSpec{
		{Name: "c", Type: types.MAT_COVARIANCE, Fields: []string{"key", "b"}, Encoding: types.MatrixEncodingRecords}}}
	if _, err := New(cfg).Process(t.Context(), req); err == nil || !strings.Contains(err.Error(), "records") {
		t.Fatalf("member key: err %v, want a records refusal", err)
	}
	if env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{}); len(env.Errors) == 0 {
		t.Errorf("member key: predict accepted it")
	}
	req.Matrices = []types.MatrixSpec{{Name: "d", Type: types.MAT_DISTANCE, Fields: []string{"b"}, Encoding: types.MatrixEncodingRecords,
		Params: json.RawMessage(`{"between": {"type": "GROUP_CATEGORY", "field": "grp"}}`)}}
	resp, err := New(cfg).Process(t.Context(), req)
	if err != nil {
		t.Fatalf("bucket key: %v", err)
	}
	if e := resp.Matrices[0].Error; e == nil || e.Details["reason"] != "records_key_collision" {
		t.Fatalf("bucket key: slot error %+v, want records_key_collision", e)
	}
	failed := false
	for _, w := range resp.Warnings {
		failed = failed || w.Code == "PULSE_SLOT_FAILED"
	}
	if !failed {
		t.Errorf("no PULSE_SLOT_FAILED warning: %+v", resp.Warnings)
	}
}

// TestMatrixRecords_PredictMatchesRuntime: predict echoes the records
// encoding and the run's shape for every built-in operator, from the
// same resolution the run makes; a member named "key" is refused by
// both.
func TestMatrixRecords_PredictMatchesRuntime(t *testing.T) {
	cfg := recordsCohort(t)
	data, err := afero.ReadFile(cfg.Fs(), "r.pulse")
	if err != nil {
		t.Fatal(err)
	}
	for typ, spec := range recordsSpecs() {
		spec.Encoding = types.MatrixEncodingRecords
		req := &types.Request{Cohort: &types.Cohort{Filename: "r.pulse"}, Matrices: []types.MatrixSpec{spec}}
		env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
		if len(env.Errors) != 0 {
			t.Fatalf("%s: predict errors %v", typ, env.Errors)
		}
		mp := env.Data.(*descriptor.PredictResult).Matrices[0]
		m := recordsRun(t, cfg, spec, types.MatrixEncodingRecords)
		if mp.Encoding != m.Primary.Encoding {
			t.Errorf("%s: predict encoding %q, runtime %q", typ, mp.Encoding, m.Primary.Encoding)
		}
		if got := [2]int{len(m.Primary.RowKeys), len(m.Primary.ColumnKeys)}; got != mp.Shape {
			t.Errorf("%s: runtime shape %v, predict %v", typ, got, mp.Shape)
		}
		if typ != types.MAT_DISTANCE && !reflect.DeepEqual(mp.AxisKeys, m.Primary.RowKeys) {
			t.Errorf("%s: predict axis %v, runtime %v", typ, mp.AxisKeys, m.Primary.RowKeys)
		}
	}
}
