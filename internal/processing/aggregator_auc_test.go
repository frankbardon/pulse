package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// AGG_AUC (model-workbench E2-S1): a score (Field) against a binary
// outcome (params.outcome).

func aucSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, l := range []string{"a", "b"} {
		if _, err := dict.Add(l); err != nil {
			panic(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "s", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "w", Type: encoding.FieldTypeF64},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "day", Type: encoding.FieldTypeDate},
	}}
}

// aucRow is one fixture row; a NaN s / y / w is a null (use aucNaNScore
// for a stored NaN score).
type aucRow struct {
	s, y, w float64
	cat     float64 // dictionary index into {a, b}
}

func aucRecords(rows []aucRow) []*Record {
	s := aucSchema()
	out := make([]*Record, len(rows))
	for i, r := range rows {
		vals := map[string]float64{"s": r.s, "y": r.y, "w": r.w, "flag": r.y, "cat": r.cat}
		nulls := map[string]bool{}
		for _, k := range []string{"s", "y", "w"} {
			if math.IsNaN(vals[k]) {
				nulls[k] = true
			}
		}
		if nulls["y"] {
			nulls["flag"] = true
		}
		out[i] = NewRecordWithNulls(s, vals, nulls)
	}
	return out
}

func aucAgg(params string, weighted bool) *types.Aggregation {
	a := &types.Aggregation{Type: types.AGG_AUC, Field: "s", Label: "auc", Params: json.RawMessage(params)}
	if weighted {
		a.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	}
	return a
}

func aucNew(t *testing.T, a *types.Aggregation) *aucAggregator {
	t.Helper()
	agg, err := aggregatorRegistry[types.AGG_AUC](a, aucSchema())
	if err != nil {
		t.Fatalf("AGG_AUC factory: %v", err)
	}
	return agg.(*aucAggregator)
}

func aucRun(t *testing.T, a *types.Aggregation, rows []aucRow) (float64, map[string]any) {
	t.Helper()
	agg := aucNew(t, a)
	v, err := agg.Aggregate(aucRecords(rows), a.Field)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	c, _ := agg.Components()
	return v, c
}

// aucReferenceCase is one auc.json case (scripts/reference/gen_auc.R).
type aucReferenceCase struct {
	Name     string    `json:"name"`
	Score    []float64 `json:"score"`
	Outcome  []float64 `json:"outcome"`
	Weight   []float64 `json:"weight"`
	Weighted bool      `json:"weighted"`
	NPos     int       `json:"n_pos"`
	NNeg     int       `json:"n_neg"`
	AUC      float64   `json:"auc"`
	SE       *float64  `json:"se"`
	CILower  *float64  `json:"ci_lower"`
	CIUpper  *float64  `json:"ci_upper"`
}

func loadAUCReference(t *testing.T) []aucReferenceCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "reference", "auc.json"))
	if err != nil {
		t.Fatalf("read auc.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		RVersion string             `json:"r_version"`
		Cases    []aucReferenceCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode auc.json: %v", err)
	}
	if doc.RVersion == "" || len(doc.Cases) == 0 {
		t.Fatal("auc.json has no R version or no cases")
	}
	return doc.Cases
}

// aucClose compares within 1e-12 relative (floored at 1e-12 absolute).
func aucClose(got, want float64) bool {
	return math.Abs(got-want) <= 1e-12*math.Max(1, math.Abs(want))
}

// TestAUC_Oracle: every unweighted case equals pROC's AUC and DeLong se
// / 95% interval (null where pROC answers NA); every weighted case
// equals the hand-computed weighted Mann–Whitney double sum, with the
// DeLong figures withheld and se_note set.
func TestAUC_Oracle(t *testing.T) {
	t.Parallel()

	for _, c := range loadAUCReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			rows := make([]aucRow, len(c.Score))
			for i := range rows {
				w := 1.0
				if c.Weighted {
					w = c.Weight[i]
				}
				rows[i] = aucRow{s: c.Score[i], y: c.Outcome[i], w: w}
			}
			got, comp := aucRun(t, aucAgg(`{"outcome":"y"}`, c.Weighted), rows)
			if !aucClose(got, c.AUC) {
				t.Errorf("auc = %.17g, want %.17g", got, c.AUC)
			}
			if comp["n_pos"] != c.NPos || comp["n_neg"] != c.NNeg {
				t.Errorf("n_pos / n_neg = %v / %v, want %d / %d", comp["n_pos"], comp["n_neg"], c.NPos, c.NNeg)
			}
			if g := comp["gini"].(float64); !aucClose(g, 2*c.AUC-1) {
				t.Errorf("gini = %.17g, want %.17g", g, 2*c.AUC-1)
			}
			check := func(key string, want *float64) {
				v := comp[key].(float64)
				if want == nil {
					if !math.IsNaN(v) {
						t.Errorf("%s = %v, want null (NaN)", key, v)
					}
					return
				}
				if !aucClose(v, *want) {
					t.Errorf("%s = %.17g, want %.17g", key, v, *want)
				}
			}
			if c.Weighted {
				for _, k := range []string{"se", "ci_lower", "ci_upper"} {
					check(k, nil)
				}
				if comp["se_note"] != string(errors.PULSE_AUC_DELONG_WEIGHTED) {
					t.Errorf("se_note = %v, want %s", comp["se_note"], errors.PULSE_AUC_DELONG_WEIGHTED)
				}
				return
			}
			check("se", c.SE)
			check("ci_lower", c.CILower)
			check("ci_upper", c.CIUpper)
			if _, ok := comp["se_note"]; ok {
				t.Error("se_note present on an unweighted slot")
			}
		})
	}
}

// TestAUC_TiesCountHalf: positives {1, 2} against negatives {1, 0}: the
// pairs score ½, 1, 1, 1 — AUC 3.5 / 4, exactly.
func TestAUC_TiesCountHalf(t *testing.T) {
	t.Parallel()

	got, _ := aucRun(t, aucAgg(`{"outcome":"y"}`, false), []aucRow{{1, 1, 1, 0}, {2, 1, 1, 0}, {1, 0, 1, 0}, {0, 0, 1, 0}})
	if got != 0.875 {
		t.Fatalf("auc = %v, want 0.875", got)
	}
}

// TestAUC_NullsSkipped: a null score is left out (the floor's n_null);
// a null outcome under a present score is left out and counted in
// n_null_outcome. Neither moves the figure.
func TestAUC_NullsSkipped(t *testing.T) {
	t.Parallel()

	clean := []aucRow{{0.9, 1, 1, 0}, {0.2, 0, 1, 0}, {0.6, 1, 1, 0}, {0.7, 0, 1, 0}}
	dirty := append(append([]aucRow(nil), clean...),
		aucRow{math.NaN(), 1, 1, 0}, aucRow{math.NaN(), 0, 1, 0}, aucRow{0.1, math.NaN(), 1, 0})
	want, _ := aucRun(t, aucAgg(`{"outcome":"y"}`, false), clean)
	got, comp := aucRun(t, aucAgg(`{"outcome":"y"}`, false), dirty)
	if got != want || got != 0.75 {
		t.Fatalf("auc with nulls = %v, clean %v, want 0.75", got, want)
	}
	if comp["n_null_outcome"] != 1 || comp["n_pos"] != 2 || comp["n_neg"] != 2 {
		t.Errorf("counters = %v", comp)
	}
	// Through the processor: the universal floor keys on the score.
	req := &types.Request{Aggregations: []*types.Aggregation{aucAgg(`{"outcome":"y"}`, false)}}
	resp, err := NewProcessor(aucSchema()).processRecords(context.Background(), req, aucRecords(dirty))
	if err != nil {
		t.Fatal(err)
	}
	e := resp.Components.Aggregations[0]
	if e.N != 5 || e.NNull != 2 {
		t.Errorf("floor n / n_null = %d / %d, want 5 / 2 (keyed on the score)", e.N, e.NNull)
	}
}

// TestAUC_NonBinaryOutcomeRefused: an outcome value other than 0 / 1
// (NaN included) fails the run with PROCESSING_AUC_OUTCOME_NOT_BINARY —
// never thresholded, never skipped.
func TestAUC_NonBinaryOutcomeRefused(t *testing.T) {
	t.Parallel()

	for _, bad := range []float64{2, 0.5, -1} {
		agg := aucNew(t, aucAgg(`{"outcome":"y"}`, false))
		_, err := agg.Aggregate(aucRecords([]aucRow{{0.1, 0, 1, 0}, {0.4, bad, 1, 0}, {0.8, 1, 1, 0}}), "s")
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_AUC_OUTCOME_NOT_BINARY {
			t.Fatalf("outcome %v: err = %v, want PROCESSING_AUC_OUTCOME_NOT_BINARY", bad, err)
		}
		if ce.Details["outcome"] != "y" || ce.Details["value"] != bad {
			t.Errorf("details = %v", ce.Details)
		}
	}
	// Through the processor the run fails.
	req := &types.Request{Aggregations: []*types.Aggregation{aucAgg(`{"outcome":"y"}`, false)}}
	_, err := NewProcessor(aucSchema()).processRecords(context.Background(), req, aucRecords([]aucRow{{0.1, 3, 1, 0}}))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_AUC_OUTCOME_NOT_BINARY {
		t.Fatalf("processRecords err = %v", err)
	}
}

// TestAUC_PositiveZero: positive 0 swaps the classes, so the AUC is the
// complement and the class counts swap.
func TestAUC_PositiveZero(t *testing.T) {
	t.Parallel()

	rows := []aucRow{{0.9, 1, 1, 0}, {0.2, 0, 1, 0}, {0.6, 1, 1, 0}, {0.6, 0, 1, 0}, {0.3, 0, 1, 0}}
	one, c1 := aucRun(t, aucAgg(`{"outcome":"y"}`, false), rows)
	zero, c0 := aucRun(t, aucAgg(`{"outcome":"y","positive":0}`, false), rows)
	if !aucClose(zero, 1-one) {
		t.Fatalf("positive 0 auc = %v, want 1 - %v", zero, one)
	}
	if c0["n_pos"] != c1["n_neg"] || c0["n_neg"] != c1["n_pos"] {
		t.Errorf("class counts did not swap: %v vs %v", c0, c1)
	}
}

// TestAUC_PackedBoolOutcome: a packed_bool outcome reads as 0 / 1 and
// answers the numeric twin exactly.
func TestAUC_PackedBoolOutcome(t *testing.T) {
	t.Parallel()

	rows := []aucRow{{0.9, 1, 1, 0}, {0.2, 0, 1, 0}, {0.6, 1, 1, 0}, {0.7, 0, 1, 0}, {0.6, 0, 1, 0}}
	num, _ := aucRun(t, aucAgg(`{"outcome":"y"}`, false), rows)
	flag, _ := aucRun(t, aucAgg(`{"outcome":"flag"}`, false), rows)
	if num != flag {
		t.Fatalf("packed_bool outcome %v != numeric %v", flag, num)
	}
}

// TestAUC_EmptyClassAndNaNScore: no positive (or no negative) yields
// NaN, never 0.5; a stored NaN score makes every figure NaN.
func TestAUC_EmptyClassAndNaNScore(t *testing.T) {
	t.Parallel()

	got, comp := aucRun(t, aucAgg(`{"outcome":"y"}`, false), []aucRow{{0.2, 0, 1, 0}, {0.4, 0, 1, 0}})
	if !math.IsNaN(got) || !math.IsNaN(comp["gini"].(float64)) {
		t.Errorf("no positive: auc %v gini %v, want NaN", got, comp["gini"])
	}
	// A NaN score: build the record by hand (a NaN in aucRow is a null).
	recs := aucRecords([]aucRow{{0.2, 0, 1, 0}, {0.4, 1, 1, 0}, {0.5, 1, 1, 0}})
	recs = append(recs, NewRecordWithNulls(aucSchema(), map[string]float64{"s": math.NaN(), "y": 0, "w": 1}, map[string]bool{}))
	agg := aucNew(t, aucAgg(`{"outcome":"y"}`, false))
	v, err := agg.Aggregate(recs, "s")
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(v) {
		t.Errorf("NaN score: auc %v, want NaN", v)
	}
}

// TestAUC_ConfigRefusals: the constructor's refusals.
func TestAUC_ConfigRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, field, params string
		want                errors.Code // "" = accepted
	}{
		{"accepted", "s", `{"outcome":"y"}`, ""},
		{"packed_bool outcome", "s", `{"outcome":"flag"}`, ""},
		{"positive 0", "s", `{"outcome":"y","positive":0}`, ""},
		{"derived outcome column", "s", `{"outcome":"label_col"}`, ""},
		{"missing outcome", "s", `{}`, errors.PROCESSING_CONFIG},
		{"no params", "s", ``, errors.PROCESSING_CONFIG},
		{"positive 2", "s", `{"outcome":"y","positive":2}`, errors.PROCESSING_CONFIG},
		{"malformed params", "s", `{"outcome":3}`, errors.PROCESSING_CONFIG},
		{"categorical outcome", "s", `{"outcome":"cat"}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"date outcome", "s", `{"outcome":"day"}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		{"categorical score", "cat", `{"outcome":"y"}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
	}
	for _, tc := range cases {
		a := &types.Aggregation{Type: types.AGG_AUC, Field: tc.field}
		if tc.params != "" {
			a.Params = json.RawMessage(tc.params)
		}
		_, err := aggregatorRegistry[types.AGG_AUC](a, aucSchema())
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected refusal %v", tc.name, err)
			}
			continue
		}
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != tc.want {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.want)
		}
	}
}

// TestAUC_RecordOrderInvariant: the classes are sorted by (score,
// weight) before any sum, so any record order answers the same bits —
// the property that keeps DecodeWorkers / ShardWorkers counts identical.
func TestAUC_RecordOrderInvariant(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(42))
	rows := make([]aucRow, 400)
	for i := range rows {
		y := 0.0
		if rng.Float64() < 0.3 {
			y = 1
		}
		s := math.Round(float64(rng.NormFloat64()*10)+float64(y*6)) / 10
		rows[i] = aucRow{s: s, y: y, w: 0.1 + float64(rng.Float64()*3)}
	}
	for _, weighted := range []bool{false, true} {
		want, wantC := aucRun(t, aucAgg(`{"outcome":"y"}`, weighted), rows)
		for trial := 0; trial < 5; trial++ {
			shuffled := append([]aucRow(nil), rows...)
			rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			got, gotC := aucRun(t, aucAgg(`{"outcome":"y"}`, weighted), shuffled)
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("weighted=%v: shuffled auc %.17g != %.17g", weighted, got, want)
			}
			if !weighted && math.Float64bits(gotC["se"].(float64)) != math.Float64bits(wantC["se"].(float64)) {
				t.Fatalf("shuffled se %v != %v", gotC["se"], wantC["se"])
			}
		}
	}
}

// TestAUC_UnityWeightBitIdentical: an all-ones weight answers the
// unweighted AUC bit for bit; only the DeLong figures are withheld.
func TestAUC_UnityWeightBitIdentical(t *testing.T) {
	t.Parallel()

	c := loadAUCReference(t)[1] // logistic_300
	rows := make([]aucRow, len(c.Score))
	for i := range rows {
		rows[i] = aucRow{s: c.Score[i], y: c.Outcome[i], w: 1}
	}
	plain, pc := aucRun(t, aucAgg(`{"outcome":"y"}`, false), rows)
	weighted, wc := aucRun(t, aucAgg(`{"outcome":"y"}`, true), rows)
	if math.Float64bits(plain) != math.Float64bits(weighted) {
		t.Fatalf("unity weight %.17g != unweighted %.17g", weighted, plain)
	}
	if math.IsNaN(pc["se"].(float64)) || !math.IsNaN(wc["se"].(float64)) {
		t.Errorf("se: unweighted %v (want a number), weighted %v (want NaN)", pc["se"], wc["se"])
	}
}

// TestAUC_InvalidWeightSkipsRow: a zero, negative or null weight leaves
// the row out of both classes.
func TestAUC_InvalidWeightSkipsRow(t *testing.T) {
	t.Parallel()

	clean := []aucRow{{0.9, 1, 2, 0}, {0.2, 0, 1, 0}, {0.6, 1, 0.5, 0}, {0.7, 0, 1.5, 0}}
	dirty := append(append([]aucRow(nil), clean...), aucRow{0.1, 1, 0, 0}, aucRow{0.95, 0, -1, 0}, aucRow{0.05, 1, math.NaN(), 0})
	want, _ := aucRun(t, aucAgg(`{"outcome":"y"}`, true), clean)
	got, _ := aucRun(t, aucAgg(`{"outcome":"y"}`, true), dirty)
	if got != want {
		t.Fatalf("invalid weights moved the figure: %v != %v", got, want)
	}
}

// TestAUC_Grouped: per group the figure (and its components) equals an
// ungrouped run over that group's rows alone.
func TestAUC_Grouped(t *testing.T) {
	t.Parallel()

	c := loadAUCReference(t)[1] // logistic_300
	rows := make([]aucRow, len(c.Score))
	var byCat [2][]aucRow
	for i := range rows {
		rows[i] = aucRow{s: c.Score[i], y: c.Outcome[i], w: 1, cat: float64(i % 2)}
		byCat[i%2] = append(byCat[i%2], rows[i])
	}
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
		Aggregations: []*types.Aggregation{aucAgg(`{"outcome":"y"}`, false)},
	}
	resp, err := NewProcessor(aucSchema()).processRecords(context.Background(), req, aucRecords(rows))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("rows = %v, want 2 groups", resp.Data)
	}
	groups := resp.Components.Aggregations[0].Groups
	if len(groups) != 2 {
		t.Fatalf("component groups = %d, want 2", len(groups))
	}
	for i, row := range resp.Data {
		idx := map[string]int{"a": 0, "b": 1}[row["cat"].(string)]
		want, wantC := aucRun(t, aucAgg(`{"outcome":"y"}`, false), byCat[idx])
		if got := row["auc"].(float64); math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("group %v: auc %.17g, want %.17g", row["cat"], got, want)
		}
		if got := groups[i].Operator["se"].(float64); math.Float64bits(got) != math.Float64bits(wantC["se"].(float64)) {
			t.Errorf("group %v: se %v, want %v", row["cat"], got, wantC["se"])
		}
	}
}
