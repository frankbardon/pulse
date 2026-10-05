package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Weighted reference fixtures (weighting-descriptive E2-S4,
// .claude/reference/weighting.md "Operation-order exactness rule").
//
// TestWeightReferenceValues pins every weight-aware aggregator's
// weighted figure — and its floor keys sum_weights / n_eff /
// n_weight_invalid — to values an EXTERNAL tool computed offline:
// statsmodels DescrStatsW (mean, population and sample variance),
// numpy type-7 percentiles on np.repeat-expanded data (frequency
// weights), R Hmisc wtd.quantile(normwt = TRUE) (probability-weighted
// median / percentile, via testdata/weight_reference/hmisc_quantile.R),
// scipy's biased skewness / kurtosis, and numpy weighted sums for
// shares, ratios, modes and set figures. The numbers live in the
// generated weight_reference_values_test.go — written by
// testdata/weight_reference/gen_weight_reference.py (tool versions
// pinned in its PEP 723 header and the R script, and echoed in
// weightRefProvenance; the script's docstring maps each Pulse
// definition onto the library call configured to match it). CI never
// runs Python or R.
//
// Three weight columns over one 14-row fixture: w (fractional
// probability weights with a zero, a negative and a NaN), f (integer
// frequency weights with zeros, a negative, a NaN and a non-integer)
// and p = c·f (a non-integer probability twin of f). Every case runs on
// the buffered arm and, where the operator streams, the streaming arm.
//
// Tolerance: 1e-12 relative (absolute when the reference is 0) — the
// references are computed in a different operation order (statsmodels'
// two-pass, exact rationals), and amd64 FMA contraction differs from
// arm64, so nothing here is bit-exact. n_weight_invalid is exact.

const weightRefTol = 1e-12

type weightRefRow struct {
	x, y    float64
	xNull   bool
	s       uint64
	sNull   bool
	w, f, p float64
}

type weightRefCase struct {
	weight, kind, op, field, params string
	// data holds the wire figure under the slot label: "" for a scalar,
	// else the keys of an object figure (AGG_WELFORD's mean / variance,
	// AGG_SET_FREQUENCY's member map — compared as the whole key set).
	data map[string]float64
	// components holds operator component keys pinned beside the data.
	components     map[string]float64
	sumWeights     float64
	nEff           float64
	hasNEff        bool
	nWeightInvalid int
	source         string
}

func weightRefSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
		{Name: "s", Type: encoding.FieldTypeSetU8, ByteOffset: 16, CsvColumnIdx: 2, Nullable: true, Dictionary: parityDict()},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 17, CsvColumnIdx: 3},
		{Name: "f", Type: encoding.FieldTypeF64, ByteOffset: 25, CsvColumnIdx: 4},
		{Name: "p", Type: encoding.FieldTypeF64, ByteOffset: 33, CsvColumnIdx: 5},
	}}
}

func weightRefService(t *testing.T) *Service {
	t.Helper()
	recs := make([][]uint64, len(weightRefRows))
	for i, r := range weightRefRows {
		recs[i] = []uint64{math.Float64bits(r.x), math.Float64bits(r.y), r.s,
			math.Float64bits(r.w), math.Float64bits(r.f), math.Float64bits(r.p)}
	}
	b := writeNullablePulse(t, weightRefSchema(), recs, func(r, f int) bool {
		return (f == 0 && weightRefRows[r].xNull) || (f == 2 && weightRefRows[r].sNull)
	})
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "ref.pulse", b, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	svc.SetShardWorkers(1)
	svc.SetDecodeWorkers(1)
	return svc
}

func weightRefKind(k string) types.WeightKind {
	if k == "frequency" {
		return types.WeightKindFrequency
	}
	return types.WeightKindProbability
}

const weightRefLabel = "ref"

// weightRefRun runs one case's slot under its weight, buffered or
// streamed; ok=false when the operator cannot take the streaming arm.
func weightRefRun(t *testing.T, svc *Service, c weightRefCase, buffered bool) (*types.Response, bool) {
	t.Helper()
	agg := &types.Aggregation{Type: types.AggregationType(c.op), Field: c.field, Label: weightRefLabel}
	if c.params != "" {
		agg.Params = json.RawMessage(c.params)
	}
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "ref.pulse"},
		Weight:       &types.WeightSpec{Field: c.weight, Kind: weightRefKind(c.kind)},
		Aggregations: []*types.Aggregation{agg},
	}
	if buffered {
		// AGG_MEDIAN never streams; opted out so it stays unweighted.
		req.Aggregations = append(req.Aggregations, &types.Aggregation{
			Type: types.AGG_MEDIAN, Field: "y", Label: paritySteer + "median", Weight: types.NullSlotWeight()})
	} else if !processing.CanStreamRequest(req, weightRefSchema()) {
		return nil, false
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: %v", c.op, err)
	}
	stripSteer(resp)
	return resp, true
}

func weightRefName(c weightRefCase) string {
	name := c.weight + "_" + c.kind + "/" + c.op
	if c.params != "" && c.op == string(types.AGG_PERCENTILE) {
		var p struct{ Percentile float64 }
		_ = json.Unmarshal([]byte(c.params), &p)
		name += fmt.Sprintf("_p%g", p.Percentile)
	}
	return name
}

// TestWeightReferenceValues — see the file comment.
func TestWeightReferenceValues(t *testing.T) {
	t.Run("coverage", assertWeightRefCoverage)
	t.Run("tests", testWeightRefTests)
	if !strings.Contains(weightRefProvenance, "statsmodels") || !strings.Contains(weightRefProvenance, "numpy") || !strings.Contains(weightRefProvenance, "Hmisc") {
		t.Fatalf("weightRefProvenance %q does not record the generating tool versions", weightRefProvenance)
	}
	svc := weightRefService(t)
	streamed := 0
	for _, c := range weightRefCases {
		t.Run(weightRefName(c), func(t *testing.T) {
			if c.source == "" {
				t.Fatal("case has no provenance source")
			}
			for _, buffered := range []bool{true, false} {
				arm := "streaming"
				if buffered {
					arm = "buffered"
				}
				resp, ok := weightRefRun(t, svc, c, buffered)
				if !ok {
					continue
				}
				if !buffered {
					streamed++
				}
				assertWeightRef(t, arm, c, resp)
			}
		})
	}
	if streamed == 0 {
		t.Fatal("no case ran on the streaming arm: the streaming half is vacuous")
	}
}

func assertWeightRef(t *testing.T, arm string, c weightRefCase, resp *types.Response) {
	t.Helper()
	if len(resp.Data) != 1 {
		t.Fatalf("%s: %d result rows, want 1", arm, len(resp.Data))
	}
	var rows []map[string]any
	if err := json.Unmarshal(mustMarshal(t, resp.Data), &rows); err != nil {
		t.Fatal(err)
	}
	got := rows[0][weightRefLabel]
	if want, scalar := c.data[""]; scalar {
		weightRefClose(t, arm+" "+c.op, got, want)
	} else {
		obj, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("%s: figure %T, want an object", arm, got)
		}
		if c.op == string(types.AGG_SET_FREQUENCY) && len(obj) != len(c.data) {
			t.Errorf("%s: members %v, reference %v", arm, sortedKeys(obj), c.data)
		}
		for k, want := range c.data {
			weightRefClose(t, arm+" "+c.op+"."+k, obj[k], want)
		}
	}
	if resp.Components == nil || len(resp.Components.Aggregations) != 1 {
		t.Fatalf("%s: want one component slot", arm)
	}
	comp := resp.Components.Aggregations[0]
	for k, want := range c.components {
		weightRefClose(t, arm+" components."+k, comp.Operator[k], want)
	}
	if comp.SumWeights == nil || comp.NWeightInvalid == nil {
		t.Fatalf("%s: weighted floor missing — the weight did not apply", arm)
	}
	weightRefClose(t, arm+" sum_weights", *comp.SumWeights, c.sumWeights)
	if *comp.NWeightInvalid != c.nWeightInvalid {
		t.Errorf("%s: n_weight_invalid = %d, reference %d", arm, *comp.NWeightInvalid, c.nWeightInvalid)
	}
	switch {
	case c.hasNEff && comp.NEff == nil:
		t.Errorf("%s: n_eff missing under kind %s", arm, c.kind)
	case c.hasNEff:
		weightRefClose(t, arm+" n_eff", *comp.NEff, c.nEff)
	case comp.NEff != nil:
		t.Errorf("%s: n_eff %v under kind %s", arm, *comp.NEff, c.kind)
	}
}

func weightRefClose(t *testing.T, where string, got any, want float64) {
	t.Helper()
	g, ok := parityFloat(got)
	if !ok {
		t.Errorf("%s = %v (%T), reference %v", where, got, got, want)
		return
	}
	if !weightRefWithin(g, want) {
		t.Errorf("%s = %.17g, reference %.17g (rel %.3g)", where, g, want, math.Abs(g-want)/math.Abs(want))
	}
}

func weightRefWithin(got, want float64) bool {
	if want == 0 {
		return math.Abs(got) <= weightRefTol
	}
	return math.Abs(got-want) <= weightRefTol*math.Abs(want)
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// assertWeightRefCoverage: every manifest weight_aware aggregator has a
// reference case under each of the three weight configurations, and
// every case names a weight-aware operator — so an aggregator flipped
// to weight-aware owes a generated reference, not just a parity row.
func assertWeightRefCoverage(t *testing.T) {
	t.Helper()
	aware := manifestAware()
	configs := []string{"w_probability", "f_frequency", "p_probability"}
	have := map[string]map[string]bool{}
	for _, c := range weightRefCases {
		if _, ok := aware[c.op]; !ok {
			t.Errorf("reference case %s: the manifest does not mark it weight_aware", c.op)
		}
		if have[c.op] == nil {
			have[c.op] = map[string]bool{}
		}
		have[c.op][c.weight+"_"+c.kind] = true
	}
	for name := range aware {
		for _, cfg := range configs {
			if !have[name][cfg] {
				t.Errorf("%s is weight_aware but has no %s reference case — extend gen_weight_reference.py and regenerate", name, cfg)
			}
		}
	}
}

// weightRefScale says how a figure moves when every weight is
// multiplied by c: scale-free figures are unchanged, totals scale by c.
// AGG_WELFORD's sample variance divides by Σw − 1 and the probability
// quantiles are normalized, so neither has a cross-kind identity.
var weightRefScale = map[types.AggregationType]string{
	types.AGG_COUNT:               "total",
	types.AGG_SUM:                 "total",
	types.AGG_AVERAGE:             "free",
	types.AGG_WEIGHTED_MEAN:       "free",
	types.AGG_VARIANCE:            "free",
	types.AGG_STDDEV:              "free",
	types.AGG_WELFORD:             "none",
	types.AGG_MEDIAN:              "none",
	types.AGG_PERCENTILE:          "none",
	types.AGG_MODE:                "free",
	types.AGG_MODE_COUNT:          "total",
	types.AGG_SKEWNESS:            "free",
	types.AGG_KURTOSIS:            "free",
	types.AGG_FREQUENCY:           "total",
	types.AGG_RATIO:               "free",
	types.AGG_SET_FREQUENCY:       "total",
	types.AGG_SET_CARDINALITY_SUM: "total",
	types.AGG_SET_CARDINALITY_AVG: "free",
}

// TestWeightReferenceKindsAgree: the same replication pattern given as
// integer FREQUENCY weights f and as non-integer PROBABILITY weights
// p = c·f answers the same point estimate for every scale-free figure
// and c × the frequency figure for every weighted total — checked on
// Pulse's own answers, so it holds independent of the reference
// numbers. The table is held total over the manifest's weight_aware
// set.
func TestWeightReferenceKindsAgree(t *testing.T) {
	t.Run("tests", testWeightRefTestsKindsAgree)
	for name := range manifestAware() {
		if _, ok := weightRefScale[types.AggregationType(name)]; !ok {
			t.Errorf("%s is weight_aware but has no weightRefScale entry", name)
		}
	}
	svc := weightRefService(t)
	byKey := map[string]weightRefCase{}
	for _, c := range weightRefCases {
		byKey[c.weight+"|"+c.op+"|"+c.params] = c
	}
	compared := 0
	for _, fc := range weightRefCases {
		if fc.weight != "f" {
			continue
		}
		scale := weightRefScale[types.AggregationType(fc.op)]
		if scale == "none" {
			continue
		}
		pc, ok := byKey["p|"+fc.op+"|"+fc.params]
		if !ok {
			t.Fatalf("%s: no p twin case", fc.op)
		}
		t.Run(fc.op, func(t *testing.T) {
			fr, _ := weightRefRun(t, svc, fc, true)
			pr, _ := weightRefRun(t, svc, pc, true)
			factor := 1.0
			if scale == "total" {
				factor = weightRefPScale
			}
			var fd, pd []map[string]any
			_ = json.Unmarshal(mustMarshal(t, fr.Data), &fd)
			_ = json.Unmarshal(mustMarshal(t, pr.Data), &pd)
			compareKinds(t, fc.op, pd[0][weightRefLabel], fd[0][weightRefLabel], factor)
			compared++
		})
	}
	if compared == 0 {
		t.Fatal("no operator compared across kinds")
	}
}

func compareKinds(t *testing.T, where string, prob, freq any, factor float64) {
	t.Helper()
	if fm, ok := freq.(map[string]any); ok {
		pm, ok := prob.(map[string]any)
		if !ok || len(pm) != len(fm) {
			t.Fatalf("%s: probability %v, frequency %v", where, prob, freq)
		}
		for k, v := range fm {
			compareKinds(t, where+"."+k, pm[k], v, factor)
		}
		return
	}
	f, _ := parityFloat(freq)
	p, _ := parityFloat(prob)
	if !weightRefWithin(p, f*factor) {
		t.Errorf("%s: probability p = %.17g, frequency f × %g = %.17g", where, p, factor, f*factor)
	}
}
