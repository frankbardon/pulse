package processing

import (
	"encoding/json"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// parallelComponentsCase enumerates a single aggregator the parity
// sweep covers. Per-category key buckets target each value flavor:
// floatKeys allow a ULP budget (0 = byte-equal bit pattern, >0 =
// Welford-family ULP comparison); intKeys / mapKeys / sliceKeys
// demand reflect.DeepEqual against the serial emission.
type parallelComponentsCase struct {
	name        string
	aggType     types.AggregationType
	field       string
	params      json.RawMessage
	floatKeys   []string
	intKeys     []string
	mapKeys     []string
	sliceKeys   []string
	floatBudget uint64
}

func parallelMergeSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64},
			{Name: "num", Type: encoding.FieldTypeF64},
			{Name: "den", Type: encoding.FieldTypeF64},
			{Name: "weight", Type: encoding.FieldTypeF64},
			{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict},
		},
	}
}

// parallelMergeRecord builds one record carrying all five fields. The
// caller supplies the per-field scalar and the set mask; nulls are not
// exercised in the parity sweep (the merge contract is independent of
// null handling and the per-aggregator null tests cover those paths).
func parallelMergeRecord(schema *encoding.Schema, score, num, den, weight float64, mask uint64) *Record {
	return NewRecordWithWide(schema,
		map[string]float64{
			"score":  score,
			"num":    num,
			"den":    den,
			"weight": weight,
		},
		nil,
		map[string]any{"tags": mask},
	)
}

// generateParallelMergeCohort builds n deterministic records seeded
// from the given source. Values are normally distributed around 50 so
// the Welford-family recurrences stay numerically well-conditioned;
// the set mask cycles 1..15 (all four bits) so AGG_SET_UNION lands on
// the full 0b1111 mask and AGG_SET_CARDINALITY_SUM accumulates a
// predictable popcount distribution.
func generateParallelMergeCohort(t *testing.T, schema *encoding.Schema, n int, seed int64) []*Record {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	records := make([]*Record, n)
	for i := 0; i < n; i++ {
		score := rng.NormFloat64()*4 + 50
		num := float64(rng.Intn(100))
		den := float64(rng.Intn(100) + 1) // never zero — keep ratio defined
		weight := float64(rng.Intn(10) + 1)
		mask := uint64((i % 15) + 1)
		records[i] = parallelMergeRecord(schema, score, num, den, weight, mask)
	}
	return records
}

// runSerialComponents drives the buffered Aggregate path over every
// record in one go and returns the emitted Components map. Mirrors the
// orchestrator's single-shard / single-pass code path.
func runSerialComponents(t *testing.T, c parallelComponentsCase, schema *encoding.Schema, records []*Record) map[string]any {
	t.Helper()
	factory := aggregatorRegistry[c.aggType]
	if factory == nil {
		t.Fatalf("no factory for %s", c.aggType)
	}
	spec := &types.Aggregation{Type: c.aggType, Field: c.field, Params: c.params}
	agg, err := factory(spec, schema)
	if err != nil {
		t.Fatalf("factory(%s): %v", c.aggType, err)
	}
	if _, err := agg.Aggregate(records, c.field); err != nil {
		t.Fatalf("%s.Aggregate: %v", c.aggType, err)
	}
	meta, ok := agg.(MetaAggregator)
	if !ok {
		t.Fatalf("%s does not implement MetaAggregator", c.aggType)
	}
	got, err := meta.Components()
	if err != nil {
		t.Fatalf("%s.Components: %v", c.aggType, err)
	}
	return got
}

// runParallelComponents fans the cohort across `shards` worker
// instances, each driving UpdateRow over its slice; folds the partials
// into the head instance via MergeOnline (shard insertion order, same
// as service/shard_reduce.go); calls Finalize once; reads Components.
func runParallelComponents(t *testing.T, c parallelComponentsCase, schema *encoding.Schema, records []*Record, shards int) map[string]any {
	t.Helper()
	factory := aggregatorRegistry[c.aggType]
	if factory == nil {
		t.Fatalf("no factory for %s", c.aggType)
	}
	spec := &types.Aggregation{Type: c.aggType, Field: c.field, Params: c.params}

	instances := make([]Aggregator, shards)
	for s := 0; s < shards; s++ {
		a, err := factory(spec, schema)
		if err != nil {
			t.Fatalf("factory(%s) shard %d: %v", c.aggType, s, err)
		}
		instances[s] = a
	}

	// Even split with the tail shard absorbing the remainder. Mirrors
	// the per-shard archive's central-directory enumeration: shard k
	// owns records[chunk*k : chunk*(k+1)].
	chunkSize := len(records) / shards
	for s := 0; s < shards; s++ {
		online, ok := instances[s].(OnlineAggregator)
		if !ok {
			t.Fatalf("%s does not implement OnlineAggregator", c.aggType)
		}
		start := s * chunkSize
		end := start + chunkSize
		if s == shards-1 {
			end = len(records)
		}
		for _, r := range records[start:end] {
			if err := online.UpdateRow(r, c.field); err != nil {
				t.Fatalf("%s.UpdateRow shard %d: %v", c.aggType, s, err)
			}
		}
	}

	head := instances[0].(OnlineAggregator)
	if shards > 1 {
		mergeIface, ok := head.(MergeableAggregator)
		if !ok {
			t.Fatalf("%s does not implement MergeableAggregator", c.aggType)
		}
		for s := 1; s < shards; s++ {
			other := instances[s].(OnlineAggregator)
			if err := mergeIface.MergeOnline(other); err != nil {
				t.Fatalf("%s.MergeOnline shard %d: %v", c.aggType, s, err)
			}
		}
	}
	if _, err := head.Finalize(); err != nil {
		t.Fatalf("%s.Finalize: %v", c.aggType, err)
	}
	meta, ok := head.(MetaAggregator)
	if !ok {
		t.Fatalf("%s does not implement MetaAggregator", c.aggType)
	}
	got, err := meta.Components()
	if err != nil {
		t.Fatalf("%s.Components: %v", c.aggType, err)
	}
	return got
}

// componentsParityCompare asserts the parallel-merge emission matches
// the serial-pass emission according to the per-case key category:
// floatKeys allow a ULP budget, intKeys / mapKeys / sliceKeys demand
// byte-equality. Missing keys on either side are an unconditional
// failure — schema drift in the merge path.
func componentsParityCompare(t *testing.T, c parallelComponentsCase, serial, parallel map[string]any, shards int) {
	t.Helper()
	for _, k := range c.floatKeys {
		sv, sok := serial[k]
		pv, pok := parallel[k]
		if !sok || !pok {
			t.Errorf("[%s] key %q present serial=%v parallel=%v (shards=%d)",
				c.name, k, sok, pok, shards)
			continue
		}
		sf, sIsF := sv.(float64)
		pf, pIsF := pv.(float64)
		if !sIsF || !pIsF {
			t.Errorf("[%s] key %q non-float: serial=%T parallel=%T (shards=%d)",
				c.name, k, sv, pv, shards)
			continue
		}
		if c.floatBudget == 0 {
			if math.Float64bits(sf) != math.Float64bits(pf) {
				t.Errorf("[%s] key %q: serial=%v parallel=%v not byte-equal (shards=%d)",
					c.name, k, sf, pf, shards)
			}
			continue
		}
		if d := ulpDistance(sf, pf); d > c.floatBudget {
			t.Errorf("[%s] key %q: serial=%v parallel=%v differ by %d ULPs (budget %d, shards=%d)",
				c.name, k, sf, pf, d, c.floatBudget, shards)
		}
	}
	for _, k := range c.intKeys {
		sv, sok := serial[k]
		pv, pok := parallel[k]
		if !sok || !pok {
			t.Errorf("[%s] key %q present serial=%v parallel=%v (shards=%d)",
				c.name, k, sok, pok, shards)
			continue
		}
		if !reflect.DeepEqual(sv, pv) {
			t.Errorf("[%s] key %q: serial=%#v parallel=%#v (shards=%d)",
				c.name, k, sv, pv, shards)
		}
	}
	for _, k := range c.mapKeys {
		sv, sok := serial[k]
		pv, pok := parallel[k]
		if !sok || !pok {
			t.Errorf("[%s] key %q present serial=%v parallel=%v (shards=%d)",
				c.name, k, sok, pok, shards)
			continue
		}
		if !reflect.DeepEqual(sv, pv) {
			t.Errorf("[%s] key %q: serial=%#v parallel=%#v (shards=%d)",
				c.name, k, sv, pv, shards)
		}
	}
	for _, k := range c.sliceKeys {
		sv, sok := serial[k]
		pv, pok := parallel[k]
		if !sok || !pok {
			t.Errorf("[%s] key %q present serial=%v parallel=%v (shards=%d)",
				c.name, k, sok, pok, shards)
			continue
		}
		// Labels slice — set aggregator emission is dictionary-ordered
		// so the slice is already deterministic and DeepEqual is safe.
		if !reflect.DeepEqual(sortedStringSlice(sv), sortedStringSlice(pv)) {
			t.Errorf("[%s] key %q: serial=%#v parallel=%#v (shards=%d)",
				c.name, k, sv, pv, shards)
		}
	}
}

// sortedStringSlice returns a defensive sorted copy of any []string
// stored in `v` so the comparator is insensitive to dictionary-walk
// order (set aggregators emit ascending bit order; both paths share
// the same recurrence so the order matches, but the helper guards
// against any future re-ordering inside one path).
func sortedStringSlice(v any) []string {
	s, ok := v.([]string)
	if !ok {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	sort.Strings(out)
	return out
}

func TestComponents_ParallelMergeMatchesSerial(t *testing.T) {
	schema := parallelMergeSchema(t)

	cases := []parallelComponentsCase{
		{
			name:        "AGG_SUM",
			aggType:     types.AGG_SUM,
			field:       "score",
			floatKeys:   []string{"sum"},
			floatBudget: 64,
		},
		{
			name:        "AGG_AVERAGE",
			aggType:     types.AGG_AVERAGE,
			field:       "score",
			floatKeys:   []string{"sum"},
			floatBudget: 64,
		},
		{
			name:      "AGG_MIN",
			aggType:   types.AGG_MIN,
			field:     "score",
			floatKeys: []string{"min"},
		},
		{
			name:      "AGG_MAX",
			aggType:   types.AGG_MAX,
			field:     "score",
			floatKeys: []string{"max"},
		},
		{
			name:      "AGG_RANGE",
			aggType:   types.AGG_RANGE,
			field:     "score",
			floatKeys: []string{"min", "max"},
		},
		{
			name:        "AGG_VARIANCE",
			aggType:     types.AGG_VARIANCE,
			field:       "score",
			floatKeys:   []string{"mean", "m2", "variance"},
			floatBudget: 256,
		},
		{
			name:        "AGG_STDDEV",
			aggType:     types.AGG_STDDEV,
			field:       "score",
			floatKeys:   []string{"mean", "m2", "variance", "stddev"},
			floatBudget: 256,
		},
		{
			name:        "AGG_WELFORD",
			aggType:     types.AGG_WELFORD,
			field:       "score",
			floatKeys:   []string{"mean", "m2", "variance", "stddev"},
			floatBudget: 256,
		},
		// Composite — weighted_mean uses Chan-Welford under the weight
		// recurrence; ratio sums num+den independently and divides.
		{
			name:        "AGG_WEIGHTED_MEAN",
			aggType:     types.AGG_WEIGHTED_MEAN,
			field:       "score",
			params:      json.RawMessage(`{"weight_field":"weight"}`),
			floatKeys:   []string{"sum_weighted", "sum_weights", "weighted_mean"},
			floatBudget: 256,
		},
		{
			name:      "AGG_RATIO",
			aggType:   types.AGG_RATIO,
			field:     "score",
			params:    json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`),
			floatKeys: []string{"numerator", "denominator", "ratio"},
			// ratio reduction is sum(num)/sum(den) — both sums are
			// associative under reorder; the divide is identical given
			// equal sums. Demand byte-equal.
			floatBudget: 0,
		},
		// Set — bitwise OR / popcount sum. Byte-equal under any merge
		// order because the reductions are exact bit / integer ops.
		{
			name:      "AGG_SET_UNION",
			aggType:   types.AGG_SET_UNION,
			field:     "tags",
			intKeys:   []string{"mask_union", "popcount"},
			sliceKeys: []string{"labels"},
		},
		{
			name:    "AGG_SET_CARDINALITY_SUM",
			aggType: types.AGG_SET_CARDINALITY_SUM,
			field:   "tags",
			intKeys: []string{"sum_cardinality"},
		},
	}

	sizes := []int{1_000, 10_000}
	if !testing.Short() {
		sizes = append(sizes, 100_000)
	}
	shardCounts := []int{1, 2, 4, 8}

	for _, size := range sizes {
		size := size
		records := generateParallelMergeCohort(t, schema, size, int64(0x57E4D+size))
		for _, c := range cases {
			c := c
			serial := runSerialComponents(t, c, schema, records)
			for _, shards := range shardCounts {
				shards := shards
				t.Run(formatParityName(c.name, size, shards), func(t *testing.T) {
					parallel := runParallelComponents(t, c, schema, records, shards)
					componentsParityCompare(t, c, serial, parallel, shards)
				})
			}
		}
	}
}

// formatParityName builds the subtest name "<AGG>_size<N>_shards<S>"
// so test failures pinpoint the (operator, cohort tier, shard fan-out)
// triple without requiring a stack walk. itoaSize emits 1K / 10K /
// 100K so the subtest names stay readable; the package-local itoa
// helper (from grouper_keyfor_test.go) handles fallback.
func formatParityName(agg string, size, shards int) string {
	return agg + "_size" + itoaSize(size) + "_shards" + itoa(shards)
}

// itoaSize emits 1K / 10K / 100K so the subtest names stay readable.
// strconv.Itoa would render "1000" — the suffixed form makes log
// scanning cheaper at a glance.
func itoaSize(n int) string {
	switch n {
	case 1_000:
		return "1K"
	case 10_000:
		return "10K"
	case 100_000:
		return "100K"
	default:
		return itoa(n)
	}
}
