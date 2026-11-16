package synth_test

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
)

// unpartitionedLagSpecs are lag-using specs WITHOUT a partition argument,
// in every slot (set_expr, when, a rejecting constraint) and over every
// lag class, whose bytes were pinned on the tree before the partition
// argument existed.
func unpartitionedLagSpecs(t *testing.T) map[string]*synth.Spec {
	t.Helper()
	raw, err := os.ReadFile("../examples/synth/07_mmm_adstock.synth.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	example, err := synth.ParseSpec(raw)
	if err != nil {
		t.Fatalf("ParseSpec(example): %v", err)
	}
	slots := adstockSpec(400)
	slots.Fields = append(slots.Fields, synth.FieldSpec{Name: "flag", Type: "f64", Distribution: synth.DistConstant,
		Params: map[string]any{"value": 0.0}})
	slots.Rules = append(slots.Rules, synth.RuleSpec{When: `lag("spend", 2, 0) > 110`, Set: map[string]any{"flag": 1.0}})
	slots.Constraints = []synth.ConstraintSpec{{Expr: `spend > lag("spend", 1, 0) - 40`}}
	slots.MaxRejectionRate = 0.9
	cat, err := synth.ParseSpec([]byte(lagDoc(`"rules": [{"set_expr": {"y": "lag(\"x\", 3, 0.5) + (lag(\"region\", 1, \"east\") == \"west\" ? 1 : 0)"}}]`)))
	if err != nil {
		t.Fatalf("ParseSpec(cat): %v", err)
	}
	cat.RowCount = 200
	return map[string]*synth.Spec{"example07": example, "slots": slots, "category": cat}
}

// preLagPartitionHashes are unpartitionedLagSpecs' SHA-256 at seed 31,
// taken on the tree BEFORE lag's partition argument existed.
var preLagPartitionHashes = map[string]string{
	"example07": "e4ec6a529fbbe6d536c8134fa83c6a77520271536208daa4865ef807a9aa79cf",
	"slots":     "89f2b39b91a8cc4d3d6d31a3bab2c08a601a9024288e58e38c8273a8ec0797a0",
	"category":  "9d5ab12a5b48903556fb43b218bd0150e01523e6d3eca8c7f3b7a5c276223306",
}

// TestSynthLagPartition_UnpartitionedSpecsAreByteIdentical: a spec that
// never passes a partition argument writes exactly its old bytes.
func TestSynthLagPartition_UnpartitionedSpecsAreByteIdentical(t *testing.T) {
	t.Parallel()

	for name, spec := range unpartitionedLagSpecs(t) {
		data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 31})
		if err != nil {
			t.Fatalf("%s: SynthBytes: %v", name, err)
		}
		if got := sha256Hex(data); got != preLagPartitionHashes[name] {
			t.Errorf("%s: bytes moved\n got %s\nwant %s", name, got, preLagPartitionHashes[name])
		}
	}
}

// geoLagSpec is a geo panel drawn row by row with geos interleaved at
// random: spend is seeded noise, stock a per-geo recursive adstock, and
// prev / prev2 / prevnil the per-geo lag-1 / lag-2 / nil-default reads.
func geoLagSpec(rows int, geoNullRate float64) *synth.Spec {
	geo := synth.FieldSpec{Name: "geo", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
		Params: map[string]any{"values": []any{"east", "west", "north"}}}
	if geoNullRate > 0 {
		geo.Nullable, geo.NullRate = true, geoNullRate
	}
	zero := func(name string) synth.FieldSpec {
		return synth.FieldSpec{Name: name, Type: "f64", Distribution: synth.DistConstant, Params: map[string]any{"value": 0.0}}
	}
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			geo,
			{Name: "spend", Type: "f64", Distribution: synth.DistUniform, Params: map[string]any{"min": 0.0, "max": 100.0}},
			zero("stock"), zero("prev"), zero("prev2"), zero("prevnil"),
		},
		Rules: []synth.RuleSpec{{SetExpr: map[string]string{
			"stock":   `spend + 0.6 * lag("stock", 1, 0, "geo")`,
			"prev":    `lag("spend", 1, -1, "geo")`,
			"prev2":   `lag("spend", 2, -2, "geo")`,
			"prevnil": `lag("spend", 1, nil, "geo") ?? -7`,
		}}},
	}
}

// geoKeys reads geo as partition keys, a null cell as "<null>".
func geoKeys(t *testing.T, data []byte) []string {
	t.Helper()
	labels := readCategoricalField(t, data, "geo")
	_, nulls := readFieldRows(t, data, "geo")
	for i := range labels {
		if nulls[i] {
			labels[i] = "<null>"
		}
	}
	return labels
}

// checkGeoSeries recomputes every per-geo series by hand over the written
// geo and spend columns, bit for bit, and returns how many partitions
// started (the rows that read the default).
func checkGeoSeries(t *testing.T, data []byte) map[string]int {
	t.Helper()
	geos := geoKeys(t, data)
	spend := readF64Field(t, data, "spend")
	stock, prev := readF64Field(t, data, "stock"), readF64Field(t, data, "prev")
	prev2, prevnil := readF64Field(t, data, "prev2"), readF64Field(t, data, "prevnil")
	history := map[string][]float64{} // geo -> spend in accepted order
	lastStock := map[string]float64{}
	rowsOf := map[string]int{}
	for i, g := range geos {
		h := history[g]
		wantStock := spend[i] + float64(0.6*lastStock[g])
		wantPrev, wantPrevNil, wantPrev2 := -1.0, -7.0, -2.0
		if len(h) >= 1 {
			wantPrev, wantPrevNil = h[len(h)-1], h[len(h)-1]
		}
		if len(h) >= 2 {
			wantPrev2 = h[len(h)-2]
		}
		if math.Float64bits(stock[i]) != math.Float64bits(wantStock) {
			t.Fatalf("row %d (%s): stock = %v, want %v", i, g, stock[i], wantStock)
		}
		if prev[i] != wantPrev || prevnil[i] != wantPrevNil || prev2[i] != wantPrev2 {
			t.Fatalf("row %d (%s): prev/prevnil/prev2 = %v/%v/%v, want %v/%v/%v", i, g, prev[i], prevnil[i], prev2[i], wantPrev, wantPrevNil, wantPrev2)
		}
		history[g] = append(h, spend[i])
		lastStock[g] = wantStock
		rowsOf[g]++
	}
	return rowsOf
}

// TestSynthLagPartition_SeriesRestartPerGeo: with geos interleaved, each
// geo's lag reads only that geo's earlier rows and the default stands in
// on each geo's first row (and first two for n = 2) — never the previous
// row of another geo.
func TestSynthLagPartition_SeriesRestartPerGeo(t *testing.T) {
	t.Parallel()

	data, _, err := synth.SynthBytes(geoLagSpec(300, 0), synth.Options{Seed: 9})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	rowsOf := checkGeoSeries(t, data)
	if len(rowsOf) != 3 {
		t.Fatalf("partitions = %v, want three geos", rowsOf)
	}
	// The geos must actually interleave, or a global lag would pass too.
	geos := geoKeys(t, data)
	switches := 0
	for i := 1; i < len(geos); i++ {
		if geos[i] != geos[i-1] {
			switches++
		}
	}
	if switches < 50 {
		t.Fatalf("geo changed %d times in %d rows; the rows do not interleave", switches, len(geos))
	}
}

// TestSynthLagPartition_NullIsItsOwnPartition: a null partition cell keys
// one more partition (SQL PARTITION BY), never the previous labelled row.
func TestSynthLagPartition_NullIsItsOwnPartition(t *testing.T) {
	t.Parallel()

	data, _, err := synth.SynthBytes(geoLagSpec(300, 0.3), synth.Options{Seed: 21})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if rowsOf := checkGeoSeries(t, data); rowsOf["<null>"] < 20 {
		t.Fatalf("null partition rows = %d, want the null arm exercised", rowsOf["<null>"])
	}
}

// TestSynthLagPartition_RejectedRowsNeverEnterAPartition: a constraint
// reading the partitioned lag rejects attempts; the written series still
// recompute from the written rows alone, two runs are byte-identical, and
// every written row satisfies the constraint as recomputed.
func TestSynthLagPartition_RejectedRowsNeverEnterAPartition(t *testing.T) {
	t.Parallel()

	spec := geoLagSpec(250, 0)
	spec.Constraints = []synth.ConstraintSpec{{Expr: `spend > lag("spend", 1, 0, "geo") - 30`}}
	spec.MaxRejectionRate = 0.9
	a, res, err := synth.SynthBytes(spec, synth.Options{Seed: 3})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if res.RowsRejected == 0 {
		t.Fatal("the constraint rejected nothing; the rejection arm is not exercised")
	}
	b, _, err := synth.SynthBytes(spec, synth.Options{Seed: 3})
	if err != nil {
		t.Fatalf("SynthBytes again: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("one spec and seed wrote two different cohorts")
	}
	checkGeoSeries(t, a)
	spend, prev := readF64Field(t, a, "spend"), readF64Field(t, a, "prev")
	for i := range spend {
		p := prev[i]
		if p == -1 {
			p = 0
		}
		if !(spend[i] > p-30) {
			t.Fatalf("row %d: spend %v violates the constraint against per-geo previous %v", i, spend[i], p)
		}
	}
}

// TestSynthLagPartition_CategoryUnderPartition: a categorical lagged
// under a partition keeps lag's typing — the previous same-geo row's
// level, the default label on each geo's first row.
func TestSynthLagPartition_CategoryUnderPartition(t *testing.T) {
	t.Parallel()

	spec := geoLagSpec(200, 0)
	levels := []any{"lo", "hi", "none"}
	spec.Fields = append(spec.Fields,
		synth.FieldSpec{Name: "tier", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
			Params: map[string]any{"values": levels[:2]}},
		synth.FieldSpec{Name: "prev_tier", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
			Params: map[string]any{"values": levels}})
	spec.Rules = append(spec.Rules, synth.RuleSpec{SetExpr: map[string]string{"prev_tier": `lag("tier", 1, "none", "geo")`}})
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 12})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	geos, tier, prevTier := geoKeys(t, data), readCategoricalField(t, data, "tier"), readCategoricalField(t, data, "prev_tier")
	last := map[string]string{}
	for i, g := range geos {
		want, ok := last[g]
		if !ok {
			want = "none"
		}
		if prevTier[i] != want {
			t.Fatalf("row %d (%s): prev_tier = %q, want %q", i, g, prevTier[i], want)
		}
		last[g] = tier[i]
	}
}

const partitionDocFields = `"fields": [
	{"name": "x", "type": "f64", "distribution": "uniform", "params": {"min": 0, "max": 1}},
	{"name": "y", "type": "f64", "distribution": "uniform", "params": {"min": 0, "max": 1}},
	{"name": "geo", "type": "categorical_u8", "nullable": true, "null_rate": 0.1, "distribution": "weighted_categorical", "params": {"values": ["east", "west"]}},
	{"name": "flag", "type": "categorical_u8", "nullable": true, "null_rate": 0.1, "distribution": "weighted_categorical", "params": {"values": ["a", "b"]}}
]`

func partitionDoc(tail string) string {
	return `{"row_count": 10, ` + partitionDocFields + `, ` + tail + `}`
}

// TestSynthLagPartition_Refusals: a partition that is not a literal, not
// declared, not categorical, or written at or after the reading rule is
// refused at parse with PULSE_SYNTH_LAG_PARTITION_INVALID, in every slot.
func TestSynthLagPartition_Refusals(t *testing.T) {
	t.Parallel()

	read := `lag(\"x\", 1, 0, \"geo\")`
	for _, tc := range []struct {
		name, tail, fragment string
	}{
		{"identifier", `"rules": [{"set_expr": {"y": "lag(\"x\", 1, 0, geo)"}}]`, "string literal"},
		{"undeclared", `"rules": [{"set_expr": {"y": "lag(\"x\", 1, 0, \"region\")"}}]`, "does not declare"},
		{"numeric", `"rules": [{"set_expr": {"y": "lag(\"x\", 1, 0, \"x\")"}}]`, "not categorical"},
		{"numeric in constraint", `"constraints": [{"expr": "lag(\"x\", 1, 0, \"y\") >= 0"}]`, "not categorical"},
		{"later set", fmt.Sprintf(`"rules": [{"set_expr": {"y": "%s"}}, {"set": {"geo": "west"}}]`, read), "rule 1 writes"},
		{"same rule set_null", fmt.Sprintf(`"rules": [{"set_expr": {"y": "%s"}, "set_null": ["geo"]}]`, read), "rule 0 writes"},
		{"later set_expr", fmt.Sprintf(`"rules": [{"when": "%s > 0", "set": {"y": 1}}, {"set_expr": {"geo": "\"east\""}}]`, read), "rule 1 writes"},
		{"later null_together member", fmt.Sprintf(`"rules": [{"set_expr": {"y": "%s"}}, {"null_together": ["flag", "geo"]}]`, read), "rule 1 writes"},
	} {
		ce := expectCode(t, partitionDoc(tc.tail), errors.PULSE_SYNTH_LAG_PARTITION_INVALID, tc.fragment)
		if ce.Details["function"] != "lag" {
			t.Errorf("%s: details = %v", tc.name, ce.Details)
		}
	}
	ce := expectCode(t, partitionDoc(fmt.Sprintf(`"rules": [{"set_expr": {"y": "%s"}}, {"set": {"geo": "west"}}]`, read)), errors.PULSE_SYNTH_LAG_PARTITION_INVALID)
	if ce.Details["lag_partition"] != "geo" || ce.Details["writer_rule"] != 1 || ce.Details["field"] != "y" {
		t.Errorf("ordering details = %v", ce.Details)
	}
	// Accepted: a write BEFORE the reading rule, the gate of a
	// null_together (copied, never written), and a constraint reading a
	// partition the rules write (constraints run after every rule).
	for _, tail := range []string{
		fmt.Sprintf(`"rules": [{"set": {"geo": "west"}}, {"set_expr": {"y": "%s"}}]`, read),
		fmt.Sprintf(`"rules": [{"set_expr": {"y": "%s"}}, {"null_together": ["geo", "flag"]}]`, read),
		fmt.Sprintf(`"rules": [{"set": {"geo": "west"}}], "constraints": [{"expr": "%s >= 0"}]`, read),
		`"rules": [{"set_expr": {"y": "lag(\"x\", 1, nil, \"geo\") ?? 0"}}]`,
	} {
		if _, err := synth.ParseSpec([]byte(partitionDoc(tail))); err != nil {
			t.Errorf("valid partitioned lag refused (%s): %v", tail, err)
		}
	}
}

// TestSynthLagPartition_RulesFileRefusal: a rules file merged onto a spec
// meets the same ordering refusal, and leaves the spec's rules untouched.
func TestSynthLagPartition_RulesFileRefusal(t *testing.T) {
	t.Parallel()

	spec, err := synth.ParseSpec([]byte(partitionDoc(`"rules": []`)))
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "rules.json", []byte(`[{"set_expr": {"y": "lag(\"x\", 1, 0, \"geo\")"}}, {"set": {"geo": "east"}}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	err = synth.ApplyRulesFile(fs, spec, "rules.json")
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PULSE_SYNTH_LAG_PARTITION_INVALID {
		t.Fatalf("ApplyRulesFile = %v, want PULSE_SYNTH_LAG_PARTITION_INVALID", err)
	}
	if len(spec.Rules) != 0 {
		t.Fatalf("refused file left rules %v", spec.Rules)
	}
}
