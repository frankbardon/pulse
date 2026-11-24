package regordinal_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/regordinal"
	"github.com/frankbardon/pulse/types"
)

// margins_e2e_test.go runs REG_ORDINAL's per-level marginal effects (U68
// E4-S1, #652) end to end — a cohort, a margins block, Process — against
// margins.json's ordinal cases: the effects and standard errors the
// model computes, and the statistic, p-value and interval the engine
// writes on them (Wald z, the coefficient table's), keyed
// "<level>:<term>" with outcomes listing the levels.

const marginsE2ERel = 1e-9

// marginsCaseSpec is the slot the golden margins case describes.
func marginsCaseSpec(c regordinal.MarginsOracleCase) *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_ORDINAL, Name: "ord", Target: "y", Predictors: c.Terms,
		Margins: &types.RegressionMargins{Type: c.Type, Scale: c.Scale, Binary: c.Binary}}
	if c.Link != regordinal.LinkLogit {
		s.Params = json.RawMessage(`{"link":"` + c.Link + `"}`)
	}
	switch c.Covariance {
	case "hc1":
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
	case "cluster":
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g"}
	}
	if c.Weighted {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	return s
}

// TestRegOrdinal_MarginsMatchREndToEnd: every golden case through
// Process — outcomes, terms, the "<level>:<term>" keys, and per effect
// the estimate, standard error, statistic, p-value and 95% interval (and
// the mem point) at 1e-9 relative; no df (a Wald z).
func TestRegOrdinal_MarginsMatchREndToEnd(t *testing.T) {
	t.Parallel()
	p := newInstance(t, nil)
	for i, c := range regordinal.LoadMarginsOracle(t) {
		name := "m" + string(rune('a'+i)) + ".pulse"
		writeCohort(t, p, name, cohortSpec{p: 2}, c.Rows, nil)
		mg := fitOne(t, p, name, marginsCaseSpec(c)).Margins
		if mg == nil {
			t.Fatalf("%s: no margins block", c.Name)
		}
		if !slices.Equal(mg.Outcomes, c.Outcomes) || !slices.Equal(mg.Terms, c.Terms) || mg.Type != c.Type || mg.Scale != c.Scale ||
			mg.ConfLevel != 0.95 || mg.DF != 0 || mg.Binary != c.Binary || !slices.Equal(mg.Contrasts, c.Contrasts) {
			t.Fatalf("%s: echo %+v", c.Name, mg)
		}
		if len(mg.Estimates) != len(c.Estimates) {
			t.Fatalf("%s: %d effects, R %d", c.Name, len(mg.Estimates), len(c.Estimates))
		}
		for k, lv := range c.Outcomes {
			for j, term := range c.Terms {
				i, key := k*len(c.Terms)+j, lv+":"+term
				for _, f := range []struct {
					what      string
					got, want float64
				}{
					{"estimate", mg.Estimates[key], c.Estimates[i]}, {"std_error", mg.StdErrors[key], c.StdErrors[i]},
					{"statistic", mg.Statistics[key], c.Statistics[i]}, {"p_value", mg.PValues[key], c.PValues[i]},
					{"ci_lower", mg.CILower[key], c.CILower[i]}, {"ci_upper", mg.CIUpper[key], c.CIUpper[i]},
				} {
					if e := rel(f.got, f.want); e > marginsE2ERel {
						t.Errorf("%s %s[%s] = %.17g, R %.17g (rel %.3g)", c.Name, f.what, key, f.got, f.want, e)
					}
				}
			}
		}
		if c.Type == types.MarginsMEM {
			for j, term := range c.Terms {
				if e := rel(mg.At[term], c.At[j]); e > marginsE2ERel {
					t.Errorf("%s at[%s] = %v, R %v", c.Name, term, mg.At[term], c.At[j])
				}
			}
		} else if mg.At != nil {
			t.Errorf("%s: ame carries at %v", c.Name, mg.At)
		}
	}
}

// TestRegOrdinal_MarginsMultiplicityPerLevel: a holm block corrects the
// effects as one family over every "<level>:<term>" key (m = levels ×
// terms), apart from the coefficients'.
func TestRegOrdinal_MarginsMultiplicityPerLevel(t *testing.T) {
	t.Parallel()
	c := regordinal.LoadMarginsOracle(t)[0]
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	s := marginsCaseSpec(c)
	s.Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}
	mg := fitOne(t, p, "ord.pulse", s).Margins
	want := len(c.Outcomes) * len(c.Terms)
	if mg.Multiplicity == nil || mg.Multiplicity.Family != types.MultiplicityFamilyMargins || mg.Multiplicity.M != want || len(mg.PAdjusted) != want {
		t.Fatalf("margins multiplicity %+v, %d adjusted, want family margins over %d", mg.Multiplicity, len(mg.PAdjusted), want)
	}
	for key := range mg.PValues {
		if adj, ok := mg.PAdjusted[key]; !ok || adj < mg.PValues[key] {
			t.Errorf("%s: p %v, adjusted %v (%v)", key, mg.PValues[key], adj, ok)
		}
	}
	pr, err := p.Predict(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "ord.pulse"}, Regressions: []*types.RegressionSpec{s}})
	if err != nil || len(pr.Errors) != 0 {
		t.Fatalf("predict: %v %+v", err, pr.Errors)
	}
	// Predict counts one p-value per predictor and, for the margins
	// family, at least one per term (how many levels is the data's):
	// lower_bound, never above the run's.
	if pv := pr.PValues; pv == nil || pv.Total != 2*len(c.Terms) || pv.Uncorrected != 0 || pv.Basis != "lower_bound" || pv.Total > len(c.Terms)+want {
		t.Errorf("predict p_values %+v, want total %d on lower_bound", pv, 2*len(c.Terms))
	}
}

// TestRegOrdinal_MarginsAbsentIsByteIdentical: a margins block adds the
// block and nothing else — the rest of the response is byte-identical.
func TestRegOrdinal_MarginsAbsentIsByteIdentical(t *testing.T) {
	t.Parallel()
	c := regordinal.LoadMarginsOracle(t)[6] // hc1
	p := newInstance(t, nil)
	writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
	run := func(margins bool) []byte {
		s := marginsCaseSpec(c)
		if !margins {
			s.Margins = nil
		}
		resp, err := p.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: "ord.pulse"}, Regressions: []*types.RegressionSpec{s}})
		if err != nil {
			t.Fatal(err)
		}
		if margins {
			if resp.Regressions[0].Margins == nil {
				t.Fatal("no margins block")
			}
			resp.Regressions[0].Margins = nil
		}
		b, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if a, b := run(true), run(false); string(a) != string(b) {
		t.Errorf("raw response moved:\n%s\n%s", a, b)
	}
}

// TestRegOrdinal_MarginsWorkerInvariant: the effects are byte-identical
// across DecodeWorkers / ShardWorkers counts.
func TestRegOrdinal_MarginsWorkerInvariant(t *testing.T) {
	t.Parallel()
	c := regordinal.LoadMarginsOracle(t)[0]
	var first string
	for _, w := range []int{1, 3, 8} {
		p := newInstance(t, func(o *pulse.Options) { o.DecodeWorkers, o.ShardWorkers = w, w })
		writeCohort(t, p, "ord.pulse", cohortSpec{p: 2}, c.Rows, nil)
		got := mustJSON(t, fitOne(t, p, "ord.pulse", marginsCaseSpec(c)).Margins)
		if first == "" {
			first = got
		} else if got != first {
			t.Errorf("workers %d: margins differ:\n%s\n%s", w, first, got)
		}
	}
}
