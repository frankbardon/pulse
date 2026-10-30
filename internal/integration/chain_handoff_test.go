package integration_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// TestChainHandOff_OutputUnchanged (U51 E5-S12, #499) pins the whole
// ProcessChain wire output of a retention-shaped chain — the final
// stage AND every stages[i] echo, Components, the weighted floors and
// per-stage `return` shaping — to digests recorded BEFORE the grouped
// tail released its buckets while emitting rows and before a later
// stage read the previous stage's rows lazily. Each variant runs on the
// serial archive scan (ShardWorkers 1) and the shard-parallel arm
// (ShardWorkers 4); both must marshal to the recorded bytes.
//
// The fixture keeps every figure exact on every architecture: amounts
// are whole cents, the probability weight is dyadic (0.5 / 1 / 2) and
// no weighted AGG_AVERAGE reports its Welford components (those merge
// within ULP, not bit for bit). Regenerate the digests only for a
// deliberate output change:
//
//	go test . -run TestChainHandOff_OutputUnchanged -update-chain-handoff
var updateChainHandOff = flag.Bool("update-chain-handoff", false, "rewrite testdata/chain_handoff/digests.json from the current runtime")

const chainHandOffDigests = "testdata/chain_handoff/digests.json"

// chainHandOffMonths are the epoch days of 2025-01-01 … 2025-07-01: six
// one-month shards.
var chainHandOffMonths = [7]int32{20089, 20120, 20148, 20179, 20209, 20240, 20270}

const (
	chainHandOffPerShard  = 400
	chainHandOffCustomers = 300
	chainHandOffStride    = 41
)

func chainHandOffSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "line_id", Type: encoding.FieldTypeU64, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "order_id", Type: encoding.FieldTypeU64, ByteOffset: 8, CsvColumnIdx: 1},
		{Name: "customer_id", Type: encoding.FieldTypeU32, ByteOffset: 16, CsvColumnIdx: 2},
		{Name: "order_date", Type: encoding.FieldTypeDate, ByteOffset: 20, CsvColumnIdx: 3},
		{Name: "net_amount", Type: encoding.FieldTypeF64, ByteOffset: 24, CsvColumnIdx: 4, Nullable: true},
		{Name: "loyalty_member", Type: encoding.FieldTypeU8, ByteOffset: 32, CsvColumnIdx: 5},
		{Name: "pw", Type: encoding.FieldTypeF64, ByteOffset: 33, CsvColumnIdx: 6},
	}}
}

// chainHandOffCohort writes the six month shards and the archive
// orders.pulse under a fresh OS directory. Customers whose id is a
// multiple of 37 have every amount null (their spend column is
// undefined, which the next stage reads as null); every 13th other
// line is null too.
func chainHandOffCohort(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	schema := chainHandOffSchema()
	var shards []string
	for m := range len(chainHandOffMonths) - 1 {
		name := fmt.Sprintf("orders-2025-%02d.pulse", m+1)
		shards = append(shards, name)
		f, err := os.Create(filepath.Join(dir, name))
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
		row := make([]byte, chainHandOffStride+schema.BitmapByteSize())
		lo, days := chainHandOffMonths[m], chainHandOffMonths[m+1]-chainHandOffMonths[m]
		for r := range chainHandOffPerShard {
			clear(row)
			g := uint64(m*chainHandOffPerShard + r)
			h := bitExactHash(g, 21)
			cust := uint32(1 + h%chainHandOffCustomers)
			day := lo + int32((h>>32)%uint64(days))
			binary.LittleEndian.PutUint64(row[0:], g+1)
			binary.LittleEndian.PutUint64(row[8:], uint64(cust)*1000+uint64(day-lo))
			binary.LittleEndian.PutUint32(row[16:], cust)
			binary.LittleEndian.PutUint32(row[20:], uint32(day))
			binary.LittleEndian.PutUint64(row[24:], math.Float64bits(float64(bitExactHash(g, 22)%50_000)/100))
			row[32] = byte(cust & 1)
			binary.LittleEndian.PutUint64(row[33:], math.Float64bits([3]float64{0.5, 1, 2}[(h>>40)%3])) // dyadic
			if cust%37 == 0 || g%13 == 5 {
				encoding.BitmapSetNull(row[chainHandOffStride:], 4)
			}
			if _, err := w.Write(row); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(context.Background(), "orders.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return dir
}

// chainHandOffVariant is one chain shape and engine option set.
type chainHandOffVariant struct {
	name     string
	weighted bool // weighted stage-0 slots
	avgW     bool // a weighted AGG_AVERAGE too (components off only)
	ret      bool // per-stage `return` blocks
	compOff  bool
}

var chainHandOffVariants = []chainHandOffVariant{
	{name: "plain/components-off", compOff: true},
	{name: "plain/components-on"},
	{name: "weighted/components-on", weighted: true},
	{name: "weighted/components-off", weighted: true, avgW: true, compOff: true},
	{name: "return/components-on", weighted: true, ret: true},
}

// chainHandOffChain is the retention chain at six months plus a third
// stage grouping by the key the second forwards (a categorical column
// of the synthesised schema). Rebuilt per run: a chain normalises its
// requests in place.
func chainHandOffChain(v chainHandOffVariant) *types.ChainRequest {
	pw := types.SlotWeightOf(types.WeightSpec{Field: "pw", Kind: types.WeightKindProbability})
	stage0 := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "customer_id"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_DATE_PART, Field: "order_date", Label: "month", Params: json.RawMessage(`{"part": "year_month"}`)},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "net_amount", Label: "spend"},
			{Type: types.AGG_DISTINCT_COUNT, Field: "order_id", Label: "orders"},
			{Type: types.AGG_MIN, Field: "month", Label: "first_month"},
			{Type: types.AGG_MAX, Field: "loyalty_member", Label: "is_member"},
		},
	}
	if v.weighted {
		stage0.Aggregations = append(stage0.Aggregations,
			&types.Aggregation{Type: types.AGG_SUM, Field: "net_amount", Label: "w_spend", Weight: pw},
			&types.Aggregation{Type: types.AGG_COUNT, Field: "net_amount", Label: "w_lines", Weight: pw})
	}
	if v.avgW {
		stage0.Aggregations = append(stage0.Aggregations,
			&types.Aggregation{Type: types.AGG_AVERAGE, Field: "net_amount", Label: "w_avg", Weight: pw})
	}
	stage1 := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "first_month"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "spend", Label: "customers"},
			{Type: types.AGG_AVERAGE, Field: "spend", Label: "avg_spend"},
			{Type: types.AGG_AVERAGE, Field: "orders", Label: "avg_orders"},
			{Type: types.AGG_SUM, Field: "is_member", Label: "members"},
		},
		Sort: []types.OrderKey{{Field: "first_month"}},
	}
	if v.weighted {
		stage1.Aggregations = append(stage1.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "w_spend", Label: "w_spend"})
	}
	for m := range len(chainHandOffMonths) - 1 {
		flag := fmt.Sprintf("in_m%02d", m+1)
		active := fmt.Sprintf("active_m%02d", m+1)
		stage0.Attributes = append(stage0.Attributes, &types.Attribute{Type: types.ATTR_FORMULA, Field: "order_date", Label: flag,
			Expression: fmt.Sprintf("order_date >= %d && order_date < %d ? 1 : 0", chainHandOffMonths[m], chainHandOffMonths[m+1])})
		stage0.Aggregations = append(stage0.Aggregations, &types.Aggregation{Type: types.AGG_MAX, Field: flag, Label: active})
		stage1.Aggregations = append(stage1.Aggregations, &types.Aggregation{Type: types.AGG_AVERAGE, Field: active, Label: fmt.Sprintf("m%02d", m+1)})
	}
	stage2 := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "first_month"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "customers", Label: "customers"},
			{Type: types.AGG_MAX, Field: "avg_spend", Label: "avg_spend"},
		},
	}
	if v.ret {
		stage0.Return = &types.Return{Exclude: []string{"data"}}
		stage1.Return = &types.Return{Preset: types.ReturnPresetMinimal, Precision: 6}
	}
	return &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "orders.pulse"},
		Stages: []*types.ChainStage{
			{Name: "per_customer", Request: stage0},
			{Name: "by_first_month", Request: stage1},
			{Name: "rollup", Request: stage2},
		},
	}
}

type chainHandOffDigest struct {
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

func TestChainHandOff_OutputUnchanged(t *testing.T) {
	t.Parallel()

	dir := chainHandOffCohort(t)
	got := map[string]chainHandOffDigest{}
	for _, v := range chainHandOffVariants {
		var ref []byte
		for _, workers := range []int{1, 4} {
			rec := &execRecorder{}
			p := execNew(t, pulse.Options{DataDir: dir, ShardWorkers: workers, DecodeWorkers: 1, DisableComponents: v.compOff}, rec)
			resp, err := p.ProcessChain(context.Background(), chainHandOffChain(v))
			if err != nil {
				t.Fatalf("%s shards=%d: ProcessChain: %v", v.name, workers, err)
			}
			if n := len(resp.Stages); n != 3 {
				t.Fatalf("%s shards=%d: %d stages", v.name, workers, n)
			}
			if !v.ret && len(resp.Stages[0].Data) < chainHandOffCustomers*9/10 {
				t.Fatalf("%s shards=%d: stage 0 answered %d customers: the fixture proves nothing", v.name, workers, len(resp.Stages[0].Data))
			}
			// The arm the variant names really ran stage 0.
			want := observe.Arm("")
			if workers > 1 {
				want = observe.ArmShardParallel
			}
			rec.mu.Lock()
			ran := false
			for _, e := range rec.ends {
				if want == "" && e.Arm != observe.ArmShardParallel && e.Arm != "" || want != "" && e.Arm == want {
					ran = true
				}
			}
			rec.mu.Unlock()
			if !ran {
				t.Fatalf("%s shards=%d: no stage ran the %q arm", v.name, workers, want)
			}
			body, err := json.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			if ref == nil {
				ref = body
			} else if string(body) != string(ref) {
				t.Errorf("%s: shard-parallel output differs from the serial scan\n got %s\nwant %s", v.name, body, ref)
			}
		}
		sum := sha256.Sum256(ref)
		got[v.name] = chainHandOffDigest{SHA256: hex.EncodeToString(sum[:]), Bytes: len(ref)}
	}
	if *updateChainHandOff {
		names := make([]string, 0, len(got))
		for n := range got {
			names = append(names, n)
		}
		sort.Strings(names)
		body, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(chainHandOffDigests), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(chainHandOffDigests, append(body, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%v)", chainHandOffDigests, names)
		return
	}
	raw, err := os.ReadFile(chainHandOffDigests)
	if err != nil {
		t.Fatalf("read %s (regenerate with -update-chain-handoff): %v", chainHandOffDigests, err)
	}
	var want map[string]chainHandOffDigest
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(got) {
		t.Errorf("%d recorded variants, %d run", len(want), len(got))
	}
	for name, g := range got {
		if w, ok := want[name]; !ok {
			t.Errorf("%s: no recorded digest", name)
		} else if g != w {
			t.Errorf("%s: chain output changed: %d bytes sha256 %s, recorded %d bytes %s", name, g.Bytes, g.SHA256, w.Bytes, w.SHA256)
		}
	}
}
