package processing

import (
	"math"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Weighted z / t scores and quantile buckets (U12 E5-S2).
//
// ATTR_ZSCORE / ATTR_TSCORE standardise against the weighted mean and
// the weighted POPULATION sd √(M2_w/Σw) — scale-free, so frequency and
// probability weights answer the same scores (statsmodels
// DescrStatsW(ddof = 0)). GROUP_QUANTILE cuts its buckets at the
// weighted order statistics (the U11 Hmisc wtd.quantile rule;
// probability weights rescaled to the row count). The external pins
// are generated into internal/service/weight_reference_values_test.go
// (TestWeightReferenceValues/regressions: the attr_zscore / attr_tscore
// / group_quantile cases).

func scoreSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "w", Type: encoding.FieldTypeF64, Nullable: true},
	}}
}

// scoreValues are distinct, unsorted values (distinct so a bucket
// boundary never splits a tie run, whose order the sort leaves open).
var scoreValues = []float64{4.2, -1.5, 9.75, 3.1, 0.4, 12.8, 7.3, 5.55, -3.2, 6.05, 2.6}

// scoreWeights: uneven integer weights, valid under both kinds.
var scoreWeights = []float64{3, 1, 2, 5, 1, 4, 2, 1, 3, 2, 6}

func scoreRecords(schema *encoding.Schema, ws []float64) []*Record {
	out := make([]*Record, len(scoreValues))
	for i, v := range scoreValues {
		out[i] = NewRecord(schema, map[string]float64{"x": v, "w": ws[i]})
	}
	return out
}

func scoreWeightOf(kind types.WeightKind) types.SlotWeight {
	return types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
}

var scoreTypes = []types.AttributeType{types.ATTR_ZSCORE, types.ATTR_TSCORE}

func computeScore(t *testing.T, typ types.AttributeType, recs []*Record, weight types.SlotWeight) []float64 {
	t.Helper()
	c, err := attributeRegistry[typ](&types.Attribute{Type: typ, Field: "x", Weight: weight}, scoreSchema())
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Compute(recs, "x")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func ones(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 1
	}
	return out
}

// TestScoreAttrWeighted_UnityBitIdentical: all-ones weights under either
// kind reproduce the unweighted scores bit for bit.
func TestScoreAttrWeighted_UnityBitIdentical(t *testing.T) {
	recs := scoreRecords(scoreSchema(), ones(len(scoreValues)))
	for _, typ := range scoreTypes {
		base := computeScore(t, typ, recs, types.SlotWeight{})
		for _, kind := range bothKinds {
			got := computeScore(t, typ, recs, scoreWeightOf(kind))
			for i := range base {
				if math.Float64bits(got[i]) != math.Float64bits(base[i]) {
					t.Fatalf("%s/%s row %d: %.17g, unweighted %.17g", typ, kind, i, got[i], base[i])
				}
			}
		}
	}
}

// TestScoreAttrWeighted_FrequencyIsExpansion: integer frequency weights
// give each row the score its copies get on the expanded rows.
func TestScoreAttrWeighted_FrequencyIsExpansion(t *testing.T) {
	recs := scoreRecords(scoreSchema(), scoreWeights)
	var exp []*Record
	var first []int
	for i, r := range recs {
		first = append(first, len(exp))
		for k := 0; k < int(scoreWeights[i]); k++ {
			exp = append(exp, r)
		}
	}
	for _, typ := range scoreTypes {
		got := computeScore(t, typ, recs, scoreWeightOf(types.WeightKindFrequency))
		want := computeScore(t, typ, exp, types.SlotWeight{})
		for i := range recs {
			if !closeRel(got[i], want[first[i]], 1e-12) {
				t.Errorf("%s row %d: %.17g, expansion %.17g", typ, i, got[i], want[first[i]])
			}
		}
	}
}

// TestScoreAttrWeighted_ClosedForm: the score is (x − μ_w)/σ_w with
// μ_w = Σwx/Σw and σ_w = √(Σw(x − μ_w)²/Σw), identical under both kinds
// and invariant to multiplying every weight by a constant.
func TestScoreAttrWeighted_ClosedForm(t *testing.T) {
	ws := []float64{0.8, 1.7, 0.35, 2.2, 0.95, 1.15, 0.6, 1.3, 2.7, 0.45, 1.05}
	sw, swx := 0.0, 0.0
	for i, w := range ws {
		sw += w
		swx += w * scoreValues[i]
	}
	mean := swx / sw
	m2 := 0.0
	for i, w := range ws {
		m2 += w * (scoreValues[i] - mean) * (scoreValues[i] - mean)
	}
	sd := math.Sqrt(m2 / sw)
	scaled := make([]float64, len(ws))
	for i, w := range ws {
		scaled[i] = w * 7.3
	}
	recs := scoreRecords(scoreSchema(), ws)
	recsScaled := scoreRecords(scoreSchema(), scaled)
	for _, typ := range scoreTypes {
		got := computeScore(t, typ, recs, scoreWeightOf(types.WeightKindProbability))
		gotScaled := computeScore(t, typ, recsScaled, scoreWeightOf(types.WeightKindProbability))
		for i, x := range scoreValues {
			want := (x - mean) / sd
			if typ == types.ATTR_TSCORE {
				want = want*10 + 50
			}
			if !closeRel(got[i], want, 1e-12) {
				t.Errorf("%s row %d: %.17g, closed form %.17g", typ, i, got[i], want)
			}
			if !closeRel(gotScaled[i], got[i], 1e-12) {
				t.Errorf("%s row %d: weights × 7.3 give %.17g, unscaled %.17g", typ, i, gotScaled[i], got[i])
			}
		}
		// Integer weights: frequency and probability give the same scores.
		intRecs := scoreRecords(scoreSchema(), scoreWeights)
		f := computeScore(t, typ, intRecs, scoreWeightOf(types.WeightKindFrequency))
		p := computeScore(t, typ, intRecs, scoreWeightOf(types.WeightKindProbability))
		if !reflect.DeepEqual(f, p) {
			t.Errorf("%s: frequency %v, probability %v", typ, f, p)
		}
	}
}

// TestScoreAttrWeighted_ExcludedRows: a zero, negative, NaN or null
// weight keeps the row out of the population statistics while the row
// still gets its score against them.
func TestScoreAttrWeighted_ExcludedRows(t *testing.T) {
	schema := scoreSchema()
	ws := append([]float64(nil), scoreWeights...)
	ws[0], ws[1], ws[2] = 0, -1, math.NaN()
	recs := scoreRecords(schema, ws)
	recs[3] = NewRecordWithNulls(schema, map[string]float64{"x": scoreValues[3]}, map[string]bool{"w": true})
	for _, typ := range scoreTypes {
		got := computeScore(t, typ, recs, scoreWeightOf(types.WeightKindProbability))
		// The statistics of the rows that carry a valid positive weight,
		// evaluated on every row.
		c, err := attributeRegistry[typ](&types.Attribute{Type: typ, Field: "x", Weight: scoreWeightOf(types.WeightKindProbability)}, schema)
		if err != nil {
			t.Fatal(err)
		}
		two := c.(TwoPassAttribute)
		for _, r := range recs[4:] {
			if err := two.PrePass(r, "x"); err != nil {
				t.Fatal(err)
			}
		}
		if err := two.Finalize(); err != nil {
			t.Fatal(err)
		}
		for i, r := range recs {
			want, err := two.Row(r, "x")
			if err != nil {
				t.Fatal(err)
			}
			if math.Float64bits(got[i]) != math.Float64bits(want) {
				t.Errorf("%s row %d: %.17g, statistics without the excluded rows %.17g", typ, i, got[i], want)
			}
		}
		if got[0] == got[4] {
			t.Fatalf("%s: excluded rows were not scored", typ)
		}
	}
}

// --- GROUP_QUANTILE ---------------------------------------------------

func quantileGroup(t *testing.T, recs []*Record, buckets int, weight types.SlotWeight) (map[string][]*Record, map[string]any) {
	t.Helper()
	g, err := grouperRegistry[types.GROUP_QUANTILE](&types.Group{Type: types.GROUP_QUANTILE, Field: "x", Interval: float64(buckets), Weight: weight}, scoreSchema())
	if err != nil {
		t.Fatal(err)
	}
	groups, err := g.Group(recs, "x")
	if err != nil {
		t.Fatal(err)
	}
	comp, err := g.(MetaGrouper).Components()
	if err != nil {
		t.Fatal(err)
	}
	return groups, comp
}

// bucketByRecord inverts a grouping: record → bucket number (1-based,
// parsed off the key), the highest one when a record repeats.
func bucketByRecord(t *testing.T, groups map[string][]*Record) map[*Record]int {
	t.Helper()
	out := map[*Record]int{}
	for key, rs := range groups {
		b, err := strconv.Atoi(key[1:])
		if err != nil {
			t.Fatalf("key %q", key)
		}
		for _, r := range rs {
			out[r] = max(out[r], b)
		}
	}
	return out
}

// TestQuantileGrouperWeighted_UnityBitIdentical: all-ones weights under
// either kind give the unweighted partition (same records, same order)
// and the same components, for every bucket count.
func TestQuantileGrouperWeighted_UnityBitIdentical(t *testing.T) {
	recs := scoreRecords(scoreSchema(), ones(len(scoreValues)))
	for _, k := range []int{2, 3, 4, 10, 100} {
		base, baseComp := quantileGroup(t, recs, k, types.SlotWeight{})
		for _, kind := range bothKinds {
			got, comp := quantileGroup(t, recs, k, scoreWeightOf(kind))
			if !reflect.DeepEqual(got, base) {
				t.Fatalf("k=%d %s: partition differs from the unweighted one", k, kind)
			}
			if !reflect.DeepEqual(comp, baseComp) {
				t.Fatalf("k=%d %s: components %v, unweighted %v", k, kind, comp, baseComp)
			}
		}
	}
}

// TestQuantileGrouperWeighted_FrequencyIsExpansion: under integer
// frequency weights each row lands in the bucket its LAST copy takes on
// the expanded rows (the cut is the order statistic that opens a
// bucket), and the bucket counts stay raw row counts.
func TestQuantileGrouperWeighted_FrequencyIsExpansion(t *testing.T) {
	recs := scoreRecords(scoreSchema(), scoreWeights)
	var exp []*Record
	for i, r := range recs {
		for k := 0; k < int(scoreWeights[i]); k++ {
			exp = append(exp, r)
		}
	}
	for _, k := range []int{2, 3, 4, 10} {
		got, comp := quantileGroup(t, recs, k, scoreWeightOf(types.WeightKindFrequency))
		want, _ := quantileGroup(t, exp, k, types.SlotWeight{})
		gb, wb := bucketByRecord(t, got), bucketByRecord(t, want)
		for i, r := range recs {
			if gb[r] != wb[r] {
				t.Errorf("k=%d row %d (x=%v, w=%v): bucket %d, expansion's last copy %d", k, i, scoreValues[i], scoreWeights[i], gb[r], wb[r])
			}
		}
		total := 0
		for _, b := range comp["buckets"].([]map[string]any) {
			n := b["count"].(int)
			if n != len(got[b["key"].(string)]) {
				t.Errorf("k=%d %v: count %d, rows %d", k, b["key"], n, len(got[b["key"].(string)]))
			}
			total += n
		}
		if total != len(recs) {
			t.Errorf("k=%d: bucket counts sum to %d, want the raw %d rows", k, total, len(recs))
		}
	}
}

// TestQuantileGrouperWeighted_ProbabilityScaleInvariance: multiplying
// every probability weight by a constant leaves the partition and the
// components unchanged (the weights are rescaled to the row count,
// Hmisc normwt = TRUE); integer weights cut the same as frequency ones.
func TestQuantileGrouperWeighted_ProbabilityScaleInvariance(t *testing.T) {
	frac := []float64{0.8, 1.7, 0.35, 2.2, 0.95, 1.15, 0.6, 1.3, 2.7, 0.45, 1.05}
	base := scoreRecords(scoreSchema(), frac)
	for _, k := range []int{3, 4, 10} {
		want, wantComp := quantileGroup(t, base, k, scoreWeightOf(types.WeightKindProbability))
		wb := bucketByRecord(t, want)
		for _, c := range []float64{1.0 / 1024, 1.0 / 3, 1e-6, 7.3} {
			ws := make([]float64, len(frac))
			for i, w := range frac {
				ws[i] = w * c
			}
			recs := scoreRecords(scoreSchema(), ws)
			got, comp := quantileGroup(t, recs, k, scoreWeightOf(types.WeightKindProbability))
			gb := bucketByRecord(t, got)
			for i := range recs {
				if gb[recs[i]] != wb[base[i]] {
					t.Errorf("k=%d ×%g row %d: bucket %d, unscaled %d", k, c, i, gb[recs[i]], wb[base[i]])
				}
			}
			if !reflect.DeepEqual(comp, wantComp) {
				t.Errorf("k=%d ×%g: components %v, unscaled %v", k, c, comp, wantComp)
			}
		}
		// The weighting moves the cut off the row-count one.
		unweighted, _ := quantileGroup(t, base, k, types.SlotWeight{})
		if reflect.DeepEqual(bucketByRecord(t, unweighted), wb) {
			t.Errorf("k=%d: the probability weights did not move a cut", k)
		}
	}
}

// TestQuantileGrouperWeighted_ExcludedRows: a row whose weight is zero,
// negative, NaN or null carries no mass — the other rows bucket as if
// it were absent — but is still bucketed by its value (with the row
// sorted just below it) and counted raw.
func TestQuantileGrouperWeighted_ExcludedRows(t *testing.T) {
	schema := scoreSchema()
	ws := append([]float64(nil), scoreWeights...)
	ws[0], ws[1], ws[2] = 0, -1, math.NaN()
	recs := scoreRecords(schema, ws)
	recs[3] = NewRecordWithNulls(schema, map[string]float64{"x": scoreValues[3]}, map[string]bool{"w": true})
	for _, kind := range bothKinds {
		got, comp := quantileGroup(t, recs, 4, scoreWeightOf(kind))
		kept, _ := quantileGroup(t, recs[4:], 4, scoreWeightOf(kind))
		gb, kb := bucketByRecord(t, got), bucketByRecord(t, kept)
		for _, r := range recs[4:] {
			if gb[r] != kb[r] {
				t.Errorf("%s: a contributing row moved bucket (%d vs %d) when excluded rows joined", kind, gb[r], kb[r])
			}
		}
		// Each excluded row shares the bucket of the next-lower value
		// (the first bucket when it is the minimum).
		order := append([]*Record(nil), recs...)
		sort.Slice(order, func(i, j int) bool {
			a, _ := order[i].NumericValue("x")
			b, _ := order[j].NumericValue("x")
			return a < b
		})
		for i, r := range order {
			if r != recs[0] && r != recs[1] && r != recs[2] && r != recs[3] {
				continue
			}
			want := 1
			if i > 0 {
				want = gb[order[i-1]]
			}
			if gb[r] != want {
				x, _ := r.NumericValue("x")
				t.Errorf("%s: excluded row x=%v in bucket %d, want %d", kind, x, gb[r], want)
			}
		}
		total := 0
		for _, b := range comp["buckets"].([]map[string]any) {
			total += b["count"].(int)
		}
		if total != len(recs) {
			t.Errorf("%s: counts sum to %d, want the raw %d", kind, total, len(recs))
		}
	}
}

// TestStampWeights_Groupers: a weight in force stamps GROUP_QUANTILE on
// groups[i] and on both crosstab axes (kind spelled out), nulls every
// other grouper's set weight, leaves `weight: null` alone; the tally
// counts the stamped grouper slots, and a grouper slot weight alone
// makes the request weighted.
func TestStampWeights_Groupers(t *testing.T) {
	reqW := &types.WeightSpec{Field: "w"}
	q := func(w types.SlotWeight) *types.Group {
		return &types.Group{Type: types.GROUP_QUANTILE, Field: "x", Weight: w}
	}
	req := &types.Request{
		Weight: reqW,
		Groups: []*types.Group{q(types.SlotWeight{}), q(types.NullSlotWeight()),
			{Type: types.GROUP_CATEGORY, Field: "x", Weight: types.SlotWeightField("w")}},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{q(types.SlotWeight{})},
			Columns: []*types.Group{q(types.SlotWeightOf(types.WeightSpec{Field: "v", Kind: types.WeightKindFrequency}))},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Weight: types.NullSlotWeight()},
		},
	}
	got := StampWeights(req, nil)
	prob := types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	for name, c := range map[string]struct{ got, want types.SlotWeight }{
		"groups[0]":           {got.Groups[0].Weight, prob},
		"groups[1]":           {got.Groups[1].Weight, types.NullSlotWeight()},
		"groups[2]":           {got.Groups[2].Weight, types.NullSlotWeight()},
		"crosstab.rows[0]":    {got.Crosstab.Rows[0].Weight, prob},
		"crosstab.columns[0]": {got.Crosstab.Columns[0].Weight, types.SlotWeightOf(types.WeightSpec{Field: "v", Kind: types.WeightKindFrequency})},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: stamped %v, want %v", name, c.got, c.want)
		}
	}
	if !req.Groups[0].Weight.IsZero() || req.Groups[2].Weight.IsNull() {
		t.Fatal("StampWeights mutated the request")
	}
	tally := NewWeightRowTally(got)
	if tally == nil || len(tally.entries) != 2 || tally.entries[0].field != "v" || !tally.entries[0].frequency || tally.entries[1].field != "w" {
		t.Fatalf("tally entries %+v, want v (frequency) and w from the grouper slots", tally)
	}
	// A grouper slot weight alone names a weight.
	only := &types.Request{Groups: []*types.Group{q(types.SlotWeightField("w"))}}
	if s := StampWeights(only, nil); s == only || s.Groups[0].Weight.Spec() == nil {
		t.Fatal("a GROUP_QUANTILE slot weight alone did not stamp")
	}
}

// TestQuantileGrouperWeighted_EqualWeightsKnifeEdge: equal fractional
// probability weights cut exactly where the unweighted grouper does.
// Their float cumulative sums land a few ulps off the integer ranks
// (0.1 × 3 = 0.30000000000000004), so this holds only because every
// cumulative weight within 1e-9 of an integer is snapped to it.
func TestQuantileGrouperWeighted_EqualWeightsKnifeEdge(t *testing.T) {
	for _, w := range []float64{0.1, 0.3, 1.0 / 3, 0.7} {
		ws := make([]float64, len(scoreValues))
		for i := range ws {
			ws[i] = w
		}
		recs := scoreRecords(scoreSchema(), ws)
		for _, k := range []int{2, 3, 4, 10} {
			base, baseComp := quantileGroup(t, recs, k, types.SlotWeight{})
			got, comp := quantileGroup(t, recs, k, scoreWeightOf(types.WeightKindProbability))
			if !reflect.DeepEqual(got, base) || !reflect.DeepEqual(comp, baseComp) {
				t.Errorf("w=%v k=%d: components %v, unweighted %v", w, k, comp, baseComp)
			}
		}
	}
}
