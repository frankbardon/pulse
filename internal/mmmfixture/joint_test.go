package mmmfixture

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// TestJointTruth_DeterministicShape: the joint-truth cohort is one row
// per week in schema field order, generated identically on every call,
// with each stored contribution β·hill(adstock) of its channel and the
// sales equal to the truth's sum within the noise (|residual| ≤ 5σ).
func TestJointTruth_DeterministicShape(t *testing.T) {
	d, again := Joint(), Joint()
	if !reflect.DeepEqual(d, again) {
		t.Fatal("two Joint() calls differ: the generator is not deterministic")
	}
	if len(d.Rows) != JointWeeks {
		t.Fatalf("%d rows, want %d", len(d.Rows), JointWeeks)
	}
	fields := len(JointSchema().Fields)
	for i, r := range d.Rows {
		if len(r) != fields || r[0] != uint64(i+1) {
			t.Fatalf("row %d = %v: want %d values, week %d", i, r, fields, i+1)
		}
		sales := float64(JointIntercept) + JointPriceEffect*r[5].(float64) + JointPromoEffect*r[6].(float64)
		for c, ch := range JointChannels {
			if r[1+c] != d.Spend[ch.Name][i] {
				t.Fatalf("row %d %s spend %v, stored %v", i, ch.Name, r[1+c], d.Spend[ch.Name][i])
			}
			sales += d.Contribution[ch.Name][i]
		}
		if res := r[7].(float64) - sales; math.Abs(res) > 5*JointNoise {
			t.Fatalf("row %d: sales residual %v beyond 5σ", i, res)
		}
	}
	// The stored contribution is the truth's curve on its own adstock.
	tv := JointChannels[0]
	x := d.Spend[tv.Name]
	a := 0.0
	for l := 0; l <= JointMaxLag; l++ {
		a += math.Pow(tv.Decay, float64(l)) * x[20-l]
	}
	if got, want := d.Contribution[tv.Name][20], tv.Beta*JointHill(a, tv.HalfSat, tv.Shape); math.Abs(got-want) > 1e-9*want {
		t.Fatalf("tv contribution week 21 = %v, want %v", got, want)
	}
}

// TestJointTruth_Scenario pins the documented scenario on the truth
// itself: at the current spend social has the highest marginal ROI
// (underfunded), search a high average but a low marginal ROI
// (saturated), display the lowest ROI (weak) and TV sits between; the
// oracle optimum spends the budget, holds social at its upper and
// display at its lower bound, and leaves TV and search strictly inside
// with equal marginal ROI (the shadow price λ) — so the pack's
// equalisation check is never vacuous.
func TestJointTruth_Scenario(t *testing.T) {
	d := Joint()
	v := d.JointVars(map[string]float64{})
	marg := func(c JointChannel, x float64) float64 {
		g := func(x float64) float64 { return c.Beta * JointHill(SteadyState(c.Decay)*x, c.HalfSat, c.Shape) }
		const h = 1e-4
		return (g(x+h) - g(x-h)) / (2 * h)
	}
	m, roi := map[string]float64{}, map[string]float64{}
	for _, c := range JointChannels {
		m[c.Name], roi[c.Name] = marg(c, v["mean_"+c.Name]), d.TrueROI(c.Name)
	}
	t.Logf("marginal ROI at current spend %v; ROI %v", m, roi)
	if !(m["social"] > m["tv"] && m["tv"] > m["search"] && m["tv"] > m["display"]) {
		t.Errorf("marginal ROI order %v: want social > tv > search, display", m)
	}
	if !(roi["search"] > roi["tv"] && roi["display"] < roi["tv"] && roi["display"] < roi["social"] && roi["display"] < roi["search"]) {
		t.Errorf("ROI %v: want search above tv and display lowest", roi)
	}

	alloc, lambda, lift := d.TrueOptimum()
	sum := 0.0
	for _, c := range JointChannels {
		sum += alloc[c.Name]
	}
	if math.Abs(sum-v["budget"]) > 1e-6*v["budget"] {
		t.Fatalf("oracle spends %v of the budget %v", sum, v["budget"])
	}
	for _, c := range JointChannels {
		lo, hi, x := v["lo_"+c.Name], v["hi_"+c.Name], alloc[c.Name]
		switch c.Name {
		case "social":
			if math.Abs(x-hi) > 1e-9*hi || marg(c, x) <= lambda {
				t.Errorf("social %v (upper %v), marginal %v vs λ %v: want at the upper bound, marginal above λ", x, hi, marg(c, x), lambda)
			}
		case "display":
			if math.Abs(x-lo) > 1e-9*lo || marg(c, x) >= lambda {
				t.Errorf("display %v (lower %v), marginal %v vs λ %v: want at the lower bound, marginal below λ", x, lo, marg(c, x), lambda)
			}
		default:
			if x < float64(1.02*lo) || x > float64(0.98*hi) || math.Abs(marg(c, x)-lambda) > 1e-4*lambda {
				t.Errorf("%s %v in [%v, %v], marginal %v vs λ %v: want interior with marginal λ", c.Name, x, lo, hi, marg(c, x), lambda)
			}
		}
	}
	cur := map[string]float64{}
	for _, c := range JointChannels {
		cur[c.Name] = v["mean_"+c.Name]
	}
	if want := TrueResponse(alloc) - TrueResponse(cur); lift <= 0 || lift != want {
		t.Fatalf("lift %v, want the positive gain %v", lift, want)
	}
	// No allocation the grid can see beats the oracle: a budget-neutral
	// move between any two channels, within their bounds, loses.
	for _, a := range JointChannels {
		for _, b := range JointChannels {
			if a.Name == b.Name {
				continue
			}
			for _, step := range []float64{0.5, 2, 5} {
				x := map[string]float64{}
				for k, val := range alloc {
					x[k] = val
				}
				x[a.Name] += step
				x[b.Name] -= step
				if x[a.Name] > v["hi_"+a.Name] || x[b.Name] < v["lo_"+b.Name] {
					continue
				}
				if TrueResponse(x) > TrueResponse(alloc) {
					t.Errorf("moving %v from %s to %s beats the oracle", step, b.Name, a.Name)
				}
			}
		}
	}
}

// TestJointTruth_Bodies: the sweep body samples JointLHSSamples slots of
// an lhs sweep over one axis per channel, every window reading its own
// channel's decay; Fill fills the second body from JointVars — the
// windows of the fit and of the contribution slot take the winning
// decays — and refuses a placeholder without a variable or a variable
// without a placeholder.
func TestJointTruth_Bodies(t *testing.T) {
	var sweep struct {
		Sweep struct {
			Mode    string `json:"mode"`
			Samples int    `json:"samples"`
			Axes    []struct {
				Name string `json:"name"`
			} `json:"axes"`
			Request struct {
				Windows []struct {
					Field  string `json:"field"`
					Params struct {
						Decay  map[string]string `json:"decay"`
						MaxLag int               `json:"max_lag"`
					} `json:"params"`
				} `json:"windows"`
			} `json:"request"`
		} `json:"sweep"`
	}
	if err := json.Unmarshal(JointLHSSweep, &sweep); err != nil {
		t.Fatal(err)
	}
	s := sweep.Sweep
	if s.Mode != "lhs" || s.Samples != JointLHSSamples || len(s.Axes) != len(JointChannels) || len(s.Request.Windows) != len(JointChannels) {
		t.Fatalf("sweep %+v", s)
	}
	for i, c := range JointChannels {
		w := s.Request.Windows[i]
		if s.Axes[i].Name != c.Name || w.Field != c.Name || w.Params.Decay["$var"] != c.Name || w.Params.MaxLag != JointMaxLag {
			t.Fatalf("axis %d / window %+v: want channel %s reading its own decay", i, w, c.Name)
		}
	}

	d := Joint()
	decays := map[string]float64{"tv": 0.61, "search": 0.12, "social": 0.29, "display": 0.18}
	body, err := Fill(JointFitOptimize, d.JointVars(decays))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "$var") {
		t.Fatalf("placeholder left: %s", body)
	}
	var filled struct {
		Fits []struct {
			Request struct {
				Windows []struct {
					Field  string `json:"field"`
					Params struct {
						Decay float64 `json:"decay"`
					} `json:"params"`
				} `json:"windows"`
			} `json:"request"`
		} `json:"fits"`
		Requests []struct {
			Windows []struct {
				Field  string `json:"field"`
				Params struct {
					Decay float64 `json:"decay"`
				} `json:"params"`
			} `json:"windows"`
		} `json:"requests"`
		Params map[string]float64 `json:"params"`
	}
	if err := json.Unmarshal(body, &filled); err != nil {
		t.Fatal(err)
	}
	for _, ws := range [][]struct {
		Field  string `json:"field"`
		Params struct {
			Decay float64 `json:"decay"`
		} `json:"params"`
	}{filled.Fits[0].Request.Windows, filled.Requests[0].Windows} {
		for _, w := range ws {
			if w.Params.Decay != decays[w.Field] {
				t.Fatalf("window %s decay %v, want %v", w.Field, w.Params.Decay, decays[w.Field])
			}
		}
	}
	if got, want := filled.Params["mult_tv"], SteadyState(0.61); got != want || filled.Params["budget"] <= 0 {
		t.Fatalf("params %v: mult_tv want %v", filled.Params, want)
	}
	if _, err := Fill(JointFitOptimize, map[string]float64{"budget": 1}); err == nil || !strings.Contains(err.Error(), "no variable") {
		t.Fatalf("missing variables: err = %v", err)
	}
	extra := d.JointVars(decays)
	extra["unused"] = 1
	if _, err := Fill(JointFitOptimize, extra); err == nil || !strings.Contains(err.Error(), "no placeholder") {
		t.Fatalf("unused variable: err = %v", err)
	}
}
