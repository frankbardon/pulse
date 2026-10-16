package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"gonum.org/v1/gonum/stat/distuv"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The inferential prior-period kinds (U47 #328): OVERLAY_T_VS_PRIOR,
// OVERLAY_Z_VS_PRIOR and OVERLAY_PROP_Z_VS_PRIOR on a grouped Process
// and on a crosstab with no column grouper.

// priorRow is one record of the prior-period cohort.
type priorRow struct {
	wave string
	x    float64 // NaN = null
	yes  int
	h    string
}

// priorRows: waves w1..w4 in that order; w3 has no x value at all (a
// missing period for the mean kinds), every wave has a 0/1 yes.
func priorRows() []priorRow {
	var out []priorRow
	for i := range 64 {
		wave := []string{"w1", "w2", "w3", "w4"}[i%4]
		x := float64((i*37)%23) + float64(i%4)*1.5
		if wave == "w3" {
			x = math.NaN()
		}
		yes := 0
		if (i*7)%5 < 1+i%4 {
			yes = 1
		}
		out = append(out, priorRow{wave: wave, x: x, yes: yes, h: []string{"a", "b"}[(i/4)%2]})
	}
	return out
}

func priorCohort(t *testing.T) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	var b strings.Builder
	b.WriteString("wave,x,yes,h\n")
	for _, r := range priorRows() {
		x := ""
		if !math.IsNaN(r.x) {
			x = fmt.Sprintf("%g", r.x)
		}
		fmt.Fprintf(&b, "%s,%s,%d,%s\n", r.wave, x, r.yes, r.h)
	}
	if err := afero.WriteFile(fs, "prior.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "prior.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	return fs, res.Path
}

func priorRequest(t *testing.T, cohort, body string) *types.Request {
	t.Helper()
	var req types.Request
	raw := `{"cohort":{"filename":"` + cohort + `"},` + body + `}`
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return &req
}

const (
	priorSeriesAggs = `"groups":[{"type":"GROUP_CATEGORY","field":"wave"}],"aggregations":[` +
		`{"type":"AGG_WELFORD","field":"x","label":"m"},{"type":"AGG_AVERAGE","field":"yes","label":"share"}]`
	priorXtMeans = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"wave"}],"cell":{"type":"AGG_WELFORD","field":"x","label":"c"}}`
	priorXtProp  = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"wave"}],"cell":{"type":"AGG_AVERAGE","field":"yes","label":"c"}}`
)

// welchOracle is the textbook Welch test of a against b (two-sided);
// normal swaps the t tail for the normal one.
func welchOracle(a, b []float64, normal bool) (stat, p float64) {
	ma, va := meanVar(a)
	mb, vb := meanVar(b)
	sa, sb := va/float64(len(a)), vb/float64(len(b))
	stat = (ma - mb) / math.Sqrt(sa+sb)
	if normal {
		return stat, 2 * distuv.UnitNormal.Survival(math.Abs(stat))
	}
	df := (sa + sb) * (sa + sb) / (sa*sa/float64(len(a)-1) + sb*sb/float64(len(b)-1))
	return stat, 2 * distuv.StudentsT{Mu: 0, Sigma: 1, Nu: df}.Survival(math.Abs(stat))
}

func meanVar(xs []float64) (m, v float64) {
	for _, x := range xs {
		m += x
	}
	m /= float64(len(xs))
	for _, x := range xs {
		v += (x - m) * (x - m)
	}
	return m, v / float64(len(xs)-1)
}

// propOracle is the pooled two-proportion z test (two-sided).
func propOracle(x1, n1, x2, n2 float64) (stat, p float64) {
	pool := (x1 + x2) / (n1 + n2)
	stat = (x1/n1 - x2/n2) / math.Sqrt(pool*(1-pool)*(1/n1+1/n2))
	return stat, 2 * distuv.UnitNormal.Survival(math.Abs(stat))
}

func priorWave(wave string) (xs []float64, yes, n float64) {
	for _, r := range priorRows() {
		if r.wave != wave {
			continue
		}
		if !math.IsNaN(r.x) {
			xs = append(xs, r.x)
		}
		yes += float64(r.yes)
		n++
	}
	return xs, yes, n
}

// priorEntry is one series entry's test as read.
type priorEntry struct {
	key       string
	stat, p   float64 // NaN = null / absent
	hasStat   bool    // the statistic key is present (null or a number)
	hasP      bool
	pAdjusted *float64
}

func priorEntries(t *testing.T, layer types.OverlayLayer) []priorEntry {
	t.Helper()
	if layer.Payload.Series == nil {
		t.Fatalf("%s: no series payload", layer.Kind)
	}
	var out []priorEntry
	for _, en := range layer.Payload.Series.Entries {
		e := priorEntry{key: fmt.Sprint(en.Key), stat: math.NaN(), p: math.NaN(), pAdjusted: en.Summary.PAdjusted}
		if s := en.Summary.Statistic; s != nil {
			e.hasStat, e.stat = true, *s
		}
		if s := en.Summary.PValue; s != nil {
			e.hasP, e.p = true, *s
		}
		out = append(out, e)
	}
	return out
}

func priorNear(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b))
}

// TestVsPrior_OracleAndFirstPeriod: every kind on both hosts answers the
// textbook test of each wave against the last wave with a value; the
// first wave carries a null statistic and NO p-value (never 0); a wave
// with no value (w3, mean kinds) keeps its key with no statistic and
// the next wave tests against the one before the gap. The series and
// crosstab hosts agree entry for entry, on both crosstab arms.
func TestVsPrior_OracleAndFirstPeriod(t *testing.T) {
	fs, cohort := priorCohort(t)
	x1, y1, n1 := priorWave("w1")
	x2, y2, n2 := priorWave("w2")
	_, y3, n3 := priorWave("w3")
	x4, y4, n4 := priorWave("w4")
	type want struct{ stat, p float64 }
	tW2, tpW2 := welchOracle(x2, x1, false)
	tW4, tpW4 := welchOracle(x4, x2, false) // w3 is a gap
	zW2, zpW2 := welchOracle(x2, x1, true)
	zW4, zpW4 := welchOracle(x4, x2, true)
	pW2, ppW2 := propOracle(y2, n2, y1, n1)
	pW3, ppW3 := propOracle(y3, n3, y2, n2)
	pW4, ppW4 := propOracle(y4, n4, y3, n3)
	wants := map[types.OverlayKind]map[string]*want{
		types.OverlayKindTVsPrior:     {"[w2]": {tW2, tpW2}, "[w4]": {tW4, tpW4}},
		types.OverlayKindZVsPrior:     {"[w2]": {zW2, zpW2}, "[w4]": {zW4, zpW4}},
		types.OverlayKindPropZVsPrior: {"[w2]": {pW2, ppW2}, "[w3]": {pW3, ppW3}, "[w4]": {pW4, ppW4}},
	}
	ov := func(kind types.OverlayKind, field string) string {
		params := ""
		if field != "" {
			params = `,"params":{"field":"` + field + `"}`
		}
		return `{"kind":"` + string(kind) + `","scope":"group"` + params + `}`
	}
	hosts := []struct {
		name string
		body string
	}{
		{"series", priorSeriesAggs + `,"overlays":[` + ov(types.OverlayKindTVsPrior, "m") + `,` +
			ov(types.OverlayKindZVsPrior, "m") + `,` + ov(types.OverlayKindPropZVsPrior, "share") + `]`},
		{"crosstab_means", priorXtMeans + `,"overlays":[` + ov(types.OverlayKindTVsPrior, "") + `,` + ov(types.OverlayKindZVsPrior, "") + `]`},
		{"crosstab_prop", priorXtProp + `,"overlays":[` + ov(types.OverlayKindPropZVsPrior, "") + `]`},
	}
	seen := map[types.OverlayKind]string{}
	for _, disable := range []bool{false, true} {
		p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disable})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range hosts {
			t.Run(fmt.Sprintf("%s/disable_fusion_%v", h.name, disable), func(t *testing.T) {
				resp, err := p.Process(context.Background(), priorRequest(t, cohort, h.body))
				if err != nil {
					t.Fatal(err)
				}
				for _, layer := range resp.Overlays {
					got := priorEntries(t, layer)
					if len(got) != 4 {
						t.Fatalf("%s: %d entries, want one per wave (4): %+v", layer.Kind, len(got), got)
					}
					first := got[0]
					if first.key != "[w1]" || !first.hasStat || !math.IsNaN(first.stat) || first.hasP {
						t.Errorf("%s: first period %+v, want a null statistic and no p-value", layer.Kind, first)
					}
					for _, e := range got[1:] {
						w := wants[layer.Kind][e.key]
						if w == nil {
							if e.hasStat || e.hasP {
								t.Errorf("%s %s: a missing period carries %+v, want no statistic", layer.Kind, e.key, e)
							}
							continue
						}
						if !priorNear(e.stat, w.stat) || !priorNear(e.p, w.p) {
							t.Errorf("%s %s: stat %v p %v, want %v %v", layer.Kind, e.key, e.stat, e.p, w.stat, w.p)
						}
					}
					wire, _ := json.Marshal(layer.Payload.Series)
					if !strings.Contains(string(wire), `{"key":["w1"],"summary":{"statistic":null}}`) {
						t.Errorf("%s: first-period wire form %s, want a null statistic and no p_value", layer.Kind, wire)
					}
					if prev, ok := seen[layer.Kind]; ok && prev != string(wire) {
						t.Errorf("%s: hosts / arms disagree:\n %s\n %s", layer.Kind, prev, wire)
					}
					seen[layer.Kind] = string(wire)
				}
			})
		}
	}
}

// TestVsPrior_Multiplicity: a multiplicity block on the layer adjusts
// exactly the tested periods (the first period and a gap are not family
// members), and predict counts the same p-values the layer emits.
func TestVsPrior_Multiplicity(t *testing.T) {
	fs, cohort := priorCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []struct{ name, body string }{
		{"series", priorSeriesAggs + `,"overlays":[{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group","params":{"field":"share"},` +
			`"multiplicity":{"method":"bonferroni","family":"layer"}}]`},
		{"crosstab", priorXtMeans + `,"overlays":[{"kind":"OVERLAY_T_VS_PRIOR","scope":"group","multiplicity":{"method":"bonferroni","family":"layer"}}]`},
	} {
		t.Run(h.name, func(t *testing.T) {
			req := priorRequest(t, cohort, h.body)
			res := predictEnvelope(t, p, fs, cohort, req).Data.(*descriptor.PredictResult)
			resp, err := p.Process(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			layer := resp.Overlays[0]
			tested := 0
			for _, e := range priorEntries(t, layer) {
				if e.hasP {
					tested++
				}
			}
			if layer.Multiplicity == nil || layer.Multiplicity.M != tested {
				t.Fatalf("multiplicity %+v, want m = %d tested periods", layer.Multiplicity, tested)
			}
			for _, e := range priorEntries(t, layer) {
				switch {
				case !e.hasP && e.pAdjusted != nil:
					t.Errorf("%s: an untested period carries p_adjusted %v", e.key, *e.pAdjusted)
				case e.hasP && (e.pAdjusted == nil || !priorNear(*e.pAdjusted, math.Min(1, e.p*float64(tested)))):
					t.Errorf("%s: p_adjusted %v, want bonferroni of %v over %d", e.key, e.pAdjusted, e.p, tested)
				}
			}
			if res.PValues == nil || res.PValues.Total < 1 || res.PValues.Total > 3 {
				t.Errorf("predict p_values %+v, want within the period count", res.PValues)
			}
		})
	}
}

// TestVsPrior_PredictMatchesRuntime: every refusal of the three kinds
// is raised by predict with the runtime's code AND message, on both
// hosts and both crosstab dispatch arms; accepted requests are accepted
// by both.
func TestVsPrior_PredictMatchesRuntime(t *testing.T) {
	fs, cohort := priorCohort(t)
	ov := func(spec string) string { return `,"overlays":[` + spec + `]` }
	const xtCols = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"wave"}],"columns":[{"type":"GROUP_CATEGORY","field":"h"}],` +
		`"cell":{"type":"AGG_WELFORD","field":"x","label":"c"}}`
	const xtCount = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"wave"}],"cell":{"type":"AGG_COUNT","field":"x","label":"c"}}`
	const xtNormProp = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"wave"}],"cell":{"type":"AGG_AVERAGE","field":"yes","label":"c"},"normalize":"column"}`
	const xtMulti = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"wave"}],"cells":[{"type":"AGG_WELFORD","field":"x","label":"a"},{"type":"AGG_WELFORD","field":"x","label":"b"}]}`
	const xtTwoRows = `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"h"},{"type":"GROUP_CATEGORY","field":"wave"}],"cell":{"type":"AGG_WELFORD","field":"x","label":"c"}}`
	const seriesSum = `"groups":[{"type":"GROUP_CATEGORY","field":"wave"}],"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}]`
	cases := []struct {
		name string
		body string
		code errors.Code // "" = accepted
	}{
		{"series t", priorSeriesAggs + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group","params":{"field":"m"}}`), ""},
		{"series prop", priorSeriesAggs + ov(`{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group","params":{"field":"share"}}`), ""},
		{"crosstab z", priorXtMeans + ov(`{"kind":"OVERLAY_Z_VS_PRIOR","scope":"group"}`), ""},
		{"crosstab prop", priorXtProp + ov(`{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group"}`), ""},
		{"crosstab partitioned by the leading row", xtTwoRows + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group"}`), ""},
		{"explicit Ref.Prior", priorXtMeans + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group","ref":{"prior":{}}}`), ""},
		{"crosstab with columns", xtCols + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group"}`), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"multi-measure crosstab", xtMulti + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group"}`), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"series measure without moments", seriesSum + ov(`{"kind":"OVERLAY_Z_VS_PRIOR","scope":"group"}`), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"series proportion of a sum", seriesSum + ov(`{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group"}`), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		// The kind's own measure rule, not the shared non_scalar one (E4-S6).
		{"series proportion of the welford measure", priorSeriesAggs + ov(`{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group","params":{"field":"m"}}`), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"crosstab proportion of the welford measure", priorXtMeans + ov(`{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group"}`), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"crosstab count cell", xtCount + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group"}`), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"crosstab proportion of a count", xtCount + ov(`{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group"}`), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"normalized proportion crosstab", xtNormProp + ov(`{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group"}`), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"series components disabled", priorSeriesAggs + `,"disable_components":true` + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group","params":{"field":"m"}}`), errors.PULSE_OVERLAY_COMPONENTS_REQUIRED},
		{"crosstab components disabled", priorXtProp + `,"disable_components":true` + ov(`{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group"}`), errors.PULSE_OVERLAY_COMPONENTS_REQUIRED},
		{"crosstab level", priorXtMeans + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group","level":1}`), errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		{"cell scope", priorXtMeans + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"cell"}`), errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"margin ref", priorSeriesAggs + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group","params":{"field":"m"},"ref":{"margin":{"axis":"row"}}}`), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"unknown field", priorSeriesAggs + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group","params":{"field":"nope"}}`), errors.PULSE_OVERLAY_FIELD_UNKNOWN},
		{"crosstab bad partition", xtTwoRows + ov(`{"kind":"OVERLAY_T_VS_PRIOR","scope":"group","params":{"partition_by":["wave"]}}`), errors.PULSE_OVERLAY_PARTITION_INVALID},
	}
	for _, disable := range []bool{false, true} {
		p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disable})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/disable_fusion_%v", c.name, disable), func(t *testing.T) {
				req := priorRequest(t, cohort, c.body)
				env := predictEnvelope(t, p, fs, cohort, req)
				_, perr := p.Process(context.Background(), req)
				var ce *errors.CodedError
				if perr != nil && !stderrors.As(perr, &ce) {
					t.Fatalf("runtime error is uncoded: %v", perr)
				}
				if c.code == "" {
					if perr != nil || len(env.Errors) != 0 {
						t.Fatalf("want accepted; runtime %v, predict %q", perr, envCodes(env))
					}
					return
				}
				if ce == nil || ce.Code != c.code {
					t.Fatalf("runtime = %v, want %s", perr, c.code)
				}
				matched := false
				for _, e := range env.Errors {
					matched = matched || (e.Code == string(ce.Code) && e.Message == ce.Message)
				}
				if !matched {
					t.Errorf("predict %q lacks the runtime's %s %q", envCodes(env), ce.Code, ce.Message)
				}
			})
		}
	}
}

// TestVsPrior_ReturnExcludingComponents: a `return` that drops
// Response.Components still computes the components every period's leg
// is read from — the layer is the full run's, and Components stay off
// the wire.
func TestVsPrior_ReturnExcludingComponents(t *testing.T) {
	fs, cohort := priorCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		priorSeriesAggs + `,"overlays":[{"kind":"OVERLAY_T_VS_PRIOR","scope":"group","params":{"field":"m"}}]`,
		priorXtProp + `,"overlays":[{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group"}]`,
	} {
		full, err := p.Process(context.Background(), priorRequest(t, cohort, body))
		if err != nil {
			t.Fatal(err)
		}
		shaped, err := p.Process(context.Background(), priorRequest(t, cohort, body+`,"return":{"preset":"standard"}`))
		if err != nil {
			t.Fatal(err)
		}
		if shaped.Components != nil {
			t.Errorf("standard preset kept Components: %+v", shaped.Components)
		}
		a, _ := json.Marshal(full.Overlays)
		b, _ := json.Marshal(shaped.Overlays)
		if string(a) != string(b) {
			t.Errorf("the shaped run's layer differs:\n full   %s\n shaped %s", a, b)
		}
	}
}

// TestVsPrior_Partitions: over two groupers both hosts walk each leading
// key's periods separately (the series default partition_by, the
// crosstab's leading row keys): one untested first period per
// partition, and the two hosts' layers agree.
func TestVsPrior_Partitions(t *testing.T) {
	fs, cohort := priorCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	const ov = `,"overlays":[{"kind":"OVERLAY_PROP_Z_VS_PRIOR","scope":"group"}]`
	series := `"groups":[{"type":"GROUP_CATEGORY","field":"h"},{"type":"GROUP_CATEGORY","field":"wave"}],` +
		`"aggregations":[{"type":"AGG_AVERAGE","field":"yes","label":"share"}]` + ov
	xt := `"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"h"},{"type":"GROUP_CATEGORY","field":"wave"}],` +
		`"cell":{"type":"AGG_AVERAGE","field":"yes","label":"c"}}` + ov
	var wires []string
	for _, body := range []string{series, xt} {
		resp, err := p.Process(context.Background(), priorRequest(t, cohort, body))
		if err != nil {
			t.Fatal(err)
		}
		untested, tested := 0, 0
		for _, e := range priorEntries(t, resp.Overlays[0]) {
			switch {
			case e.hasStat && math.IsNaN(e.stat) && !e.hasP:
				untested++
			case e.hasP:
				tested++
			}
		}
		if untested != 2 || tested != 6 {
			t.Errorf("%d untested / %d tested periods, want 2 / 6 (two partitions of four waves)", untested, tested)
		}
		w, _ := json.Marshal(resp.Overlays[0].Payload.Series)
		wires = append(wires, string(w))
	}
	if wires[0] != wires[1] {
		t.Errorf("series and crosstab partitions differ:\n %s\n %s", wires[0], wires[1])
	}
}
