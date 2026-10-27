package pulse_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// bit_exact_test.go is the fixture of the bit-exactness family
// (TestBitExact_Archive / TestBitExact_Heavy, bit_exact_invariance_test.go,
// on the shared worker-invariance harness; U51 E2-S3, #357, FR-20) for
// the binned reproducible sums (internal/reprosum): AGG_SUM, AGG_AVERAGE
// (that sum over an exact integer n), AGG_RATIO, the weighted sums
// (weighted AGG_SUM / AGG_AVERAGE / AGG_COUNT, AGG_WEIGHTED_MEAN), the
// sum_weights / n_eff floor and ATTR_FORMULA total(). Over non-dyadic,
// mixed-magnitude floats (x spans 1e-3 .. 1e6; the probability weight
// and the ratio denominator are non-dyadic too) every arm must answer
// the SAME BITS: the serial single file, DecodeWorkers 2 / 3 / 7 / 8 on
// it, and ShardWorkers 1 / 2 / 3 / 8 on an archive of the same rows split
// into nine UNEVEN shards. The whole response compares bitwise; the
// fixture has no empty bucket, so no NaN reaches
// a compared figure. Welford figures (m2_weighted, weighted_variance)
// merge by Chan-Welford and stay within ULP, by contract: liftWelford
// takes them out and the family holds them within bitExactWelfordTol.

const bitExactRows = 120_000 // above the parallel-decode threshold (100k)

// bitExactShardWeights is the uneven split: each row lands in shard i
// with probability proportional to weight i (tiny, huge and odd
// shards), by a hash of the row, so the archive holds the rows in a
// DIFFERENT order from the single file — a serial archive scan folds
// them in another order, and every partition differs.
var bitExactShardWeights = []uint64{1, 17_003, 3, 40_000, 9_999, 12, 30_000, 22_981, 2}

// bitExactShardOf is row r's shard: rows 0..8 seed one shard each (no
// shard is empty), the rest follow the weights.
func bitExactShardOf(r uint64) int {
	if r < uint64(len(bitExactShardWeights)) {
		return int(r)
	}
	var total uint64
	for _, w := range bitExactShardWeights {
		total += w
	}
	u := bitExactHash(r, 9) % total
	for i, w := range bitExactShardWeights {
		if u < w {
			return i
		}
		u -= w
	}
	return len(bitExactShardWeights) - 1
}

// bitExactHash is splitmix64 over (row, column).
func bitExactHash(row, col uint64) uint64 {
	z := row*0x9E3779B97F4A7C15 + col*0xD1B54A32D192ED03 + 0x632BE59BD9B4E019
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// bitExactUnit maps a hash to [0, 1).
func bitExactUnit(h uint64) float64 { return float64(h>>11) / (1 << 53) }

// bitExactStride is the fixed-width part of a row (the null bitmap follows).
const bitExactStride = 39

var bitExactScales = [...]float64{1e-3, 1e-2, 0.1, 1, 10, 100, 1e3, 1e4, 1e5, 1e6}

func bitExactSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, v := range []string{"north", "south", "east", "west", "central"} {
		if _, err := dict.Add(v); err != nil {
			panic(err)
		}
	}
	tags := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d", "e", "f"} {
		if _, err := tags.Add(v); err != nil {
			panic(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 9, CsvColumnIdx: 2},
		{Name: "pw", Type: encoding.FieldTypeF64, ByteOffset: 17, CsvColumnIdx: 3},
		{Name: "fw", Type: encoding.FieldTypeU8, ByteOffset: 25, CsvColumnIdx: 4},
		{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 26, CsvColumnIdx: 5, Dictionary: tags},
		{Name: "rid", Type: encoding.FieldTypeU32, ByteOffset: 27, CsvColumnIdx: 6},
		{Name: "rv", Type: encoding.FieldTypeF64, ByteOffset: 31, CsvColumnIdx: 7},
	}}
}

// bitExactRow encodes row r. Every product is an explicit float64
// barrier, so arm64 FMA fusion cannot move the inputs.
func bitExactRow(buf []byte, r uint64) {
	clear(buf[bitExactStride:])
	buf[0] = byte(r % 5)
	h := bitExactHash(r, 1)
	x := float64((2*bitExactUnit(h) - 1) * bitExactScales[h%uint64(len(bitExactScales))])
	putF64(buf[1:], x)
	putF64(buf[9:], 1+float64(bitExactUnit(bitExactHash(r, 2))*97.3))
	putF64(buf[17:], 0.05+float64(bitExactUnit(bitExactHash(r, 3))*2.7))
	buf[25] = byte(1 + r%4)
	buf[26] = byte(bitExactHash(r, 4) & 0x3f) // a set_u8 mask over six members
	// rid: a respondent spans three consecutive rows and carries ONE
	// non-dyadic value rv on all of them (AGG_DISTINCT_SUM's key).
	rid := r / 3
	buf[27], buf[28], buf[29], buf[30] = byte(rid), byte(rid>>8), byte(rid>>16), byte(rid>>24)
	putF64(buf[31:], float64(bitExactUnit(bitExactHash(rid, 5))*bitExactScales[rid%uint64(len(bitExactScales))]))
	if r%11 == 5 {
		encoding.BitmapSetNull(buf[bitExactStride:], 1)
	}
}

func putF64(b []byte, v float64) {
	u := math.Float64bits(v)
	for i := range 8 {
		b[i] = byte(u >> (8 * i))
	}
}

// writeBitExactFile writes the rows keep accepts, in row order, as one
// .pulse file.
func writeBitExactFile(t *testing.T, path string, schema *encoding.Schema, keep func(r uint64) bool) int {
	t.Helper()
	return writeBitExactRows(t, path, schema, bitExactRows, keep)
}

// writeBitExactRows is writeBitExactFile over rows 0..n-1.
func writeBitExactRows(t *testing.T, path string, schema *encoding.Schema, n uint64, keep func(r uint64) bool) int {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, schema); err != nil {
		t.Fatal(err)
	}
	row := make([]byte, bitExactStride+schema.BitmapByteSize())
	kept := 0
	for r := uint64(0); r < n; r++ {
		if !keep(r) {
			continue
		}
		bitExactRow(row, r)
		if _, err := w.Write(row); err != nil {
			t.Fatal(err)
		}
		kept++
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return kept
}

// bitExactCohorts writes the single file flat.pulse of rows 0..rows-1
// and — archive — the nine-shard archive arch.pulse of the same rows
// (hash-scattered, unevenly) under a fresh OS directory (the
// parallel-decode arm needs a real, mappable file).
func bitExactCohorts(t *testing.T, rows uint64, archive bool) string {
	t.Helper()
	dir := t.TempDir()
	schema := bitExactSchema()
	writeBitExactRows(t, filepath.Join(dir, "flat.pulse"), schema, rows, func(uint64) bool { return true })
	if !archive {
		return dir
	}
	var shards []string
	total := 0
	for i := range bitExactShardWeights {
		name := fmt.Sprintf("part%d.pulse", i)
		total += writeBitExactRows(t, filepath.Join(dir, name), schema, rows, func(r uint64) bool { return bitExactShardOf(r) == i })
		shards = append(shards, name)
	}
	if uint64(total) != rows {
		t.Fatalf("shards hold %d rows, want %d", total, rows)
	}
	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(context.Background(), "arch.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return dir
}

// bitExactWelfordKeys are the component keys a weighted AGG_AVERAGE /
// AGG_WEIGHTED_MEAN reports from its Welford moments. They merge by
// Chan-Welford and are within ULP across arms, not bit-identical (the
// documented contract), so the gate lifts them out, checks them within
// bitExactWelfordTol, and compares everything else bit for bit.
var bitExactWelfordKeys = []string{"m2_weighted", "weighted_variance"}

const bitExactWelfordTol = 1e-12

// liftWelford removes the Welford keys from every component map of r
// and returns them by path.
func liftWelford(r *types.Response) map[string]float64 {
	out := map[string]float64{}
	lift := func(path string, m map[string]any) {
		for _, k := range bitExactWelfordKeys {
			if v, ok := m[k].(float64); ok {
				out[path+"."+k] = v
				delete(m, k)
			}
		}
	}
	c := r.Components
	if c == nil {
		return out
	}
	for i, e := range c.Aggregations {
		lift(fmt.Sprintf("agg[%d]", i), e.Operator)
		for j, g := range e.Groups {
			lift(fmt.Sprintf("agg[%d].group[%d]", i, j), g.Operator)
		}
	}
	if x := c.Crosstab; x != nil {
		for i, row := range x.CellComponents {
			for j, m := range row {
				lift(fmt.Sprintf("cell[%d][%d]", i, j), m)
			}
		}
		for i, m := range x.RowMarginComponents {
			lift(fmt.Sprintf("row_margin[%d]", i), m)
		}
		for i, m := range x.ColumnMarginComponents {
			lift(fmt.Sprintf("col_margin[%d]", i), m)
		}
		lift("grand", x.GrandTotalComponents)
	}
	return out
}

// bitExactPW is the non-dyadic probability weight.
func bitExactPW() types.SlotWeight {
	return types.SlotWeightOf(types.WeightSpec{Field: "pw", Kind: types.WeightKindProbability})
}

// bitExactAggs is every reproducible-sum slot: unweighted, and weighted
// by the non-dyadic probability weight pw.
func bitExactAggs() []*types.Aggregation {
	ratio, _ := json.Marshal(map[string]string{"numerator_field": "x", "denominator_field": "y"})
	return []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "x", Label: "s"},
		{Type: types.AGG_AVERAGE, Field: "x", Label: "a"},
		{Type: types.AGG_RATIO, Field: "x", Label: "r", Params: ratio},
		{Type: types.AGG_SUM, Field: "x", Label: "ws", Weight: bitExactPW()},
		{Type: types.AGG_AVERAGE, Field: "x", Label: "wa", Weight: bitExactPW()},
		{Type: types.AGG_COUNT, Field: "x", Label: "wn", Weight: bitExactPW()},
		{Type: types.AGG_WEIGHTED_MEAN, Field: "x", Label: "wm", Weight: bitExactPW()},
	}
}
