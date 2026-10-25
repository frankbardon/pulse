package sweep

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func fp(v float64) *float64 { return &v }

// sampledSpec is a valid lhs sweep over two range axes.
func sampledSpec(mode types.SweepMode, n int) *types.SweepSpec {
	return &types.SweepSpec{
		Mode:    mode,
		Samples: n,
		Axes: []types.SweepAxis{
			{Name: "decay", Range: &types.SweepRange{Min: fp(0), Max: fp(0.9)}},
			{Name: "lambda", Range: &types.SweepRange{Min: fp(1e-3), Max: fp(1e3), Scale: types.SweepScaleLog}},
		},
		Request: json.RawMessage(`{"cohort":{"filename":"c.pulse"},"params":{"d":{"$var":"decay"},"l":{"$var":"lambda"}}}`),
	}
}

func sampledComposed(spec *types.SweepSpec) *types.ComposedRequest {
	return &types.ComposedRequest{Sweep: spec}
}

func requireCode(t *testing.T, err error, code errors.Code, reason string) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want %s/%s, got %v", code, reason, err)
	}
	if ce.Code != code || ce.Details["reason"] != reason {
		t.Fatalf("got %s reason=%v (%s), want %s/%s", ce.Code, ce.Details["reason"], ce.Message, code, reason)
	}
	return ce
}

// TestSweepRandom_Refusals drives every sampling-mode refusal: its code,
// reason and located field — and that each fires at Expand (the entry
// every surface shares) exactly as at Validate.
func TestSweepRandom_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *types.SweepSpec)
		code   errors.Code
		reason string
		field  string
	}{
		{"range under grid", func(s *types.SweepSpec) { s.Mode, s.Samples = types.SweepModeGrid, 0 },
			errors.PULSE_SWEEP_MODE_MISMATCH, ReasonRangeUnderEnumeration, "sweep.axes[0].range"},
		{"range under default mode", func(s *types.SweepSpec) { s.Mode, s.Samples = "", 0 },
			errors.PULSE_SWEEP_MODE_MISMATCH, ReasonRangeUnderEnumeration, "sweep.axes[0].range"},
		{"range under zip", func(s *types.SweepSpec) { s.Mode, s.Samples = types.SweepModeZip, 0 },
			errors.PULSE_SWEEP_MODE_MISMATCH, ReasonRangeUnderEnumeration, "sweep.axes[0].range"},
		{"range under unknown mode", func(s *types.SweepSpec) { s.Mode = "cross" },
			errors.PULSE_SWEEP_INVALID, ReasonModeUnknown, "sweep.mode"},
		{"values under random", func(s *types.SweepSpec) {
			s.Mode = types.SweepModeRandom
			s.Axes[1] = types.SweepAxis{Name: "lambda", Values: []any{json.Number("1")}}
		}, errors.PULSE_SWEEP_MODE_MISMATCH, ReasonValuesUnderSampling, "sweep.axes[1].values"},
		{"values and range under lhs", func(s *types.SweepSpec) { s.Axes[0].Values = []any{json.Number("1")} },
			errors.PULSE_SWEEP_MODE_MISMATCH, ReasonValuesAndRange, "sweep.axes[0]"},
		{"samples under grid", func(s *types.SweepSpec) {
			s.Mode = types.SweepModeGrid
			s.Axes = []types.SweepAxis{{Name: "decay", Values: []any{json.Number("1")}}, {Name: "lambda", Values: []any{json.Number("1")}}}
		}, errors.PULSE_SWEEP_MODE_MISMATCH, ReasonSamplesUnderEnumeration, "sweep.samples"},
		{"seed under zip", func(s *types.SweepSpec) {
			s.Mode, s.Samples, s.RNGSeed = types.SweepModeZip, 0, 7
			s.Axes = []types.SweepAxis{{Name: "decay", Values: []any{json.Number("1")}}, {Name: "lambda", Values: []any{json.Number("1")}}}
		}, errors.PULSE_SWEEP_MODE_MISMATCH, ReasonSeedUnderEnumeration, "sweep.rng_seed"},
		{"range missing", func(s *types.SweepSpec) { s.Axes[1].Range = nil },
			errors.PULSE_SWEEP_RANGE_INVALID, ReasonRangeMissing, "sweep.axes[1].range"},
		{"min missing", func(s *types.SweepSpec) { s.Axes[0].Range.Min = nil },
			errors.PULSE_SWEEP_RANGE_INVALID, ReasonBoundMissing, "sweep.axes[0].range.min"},
		{"max missing", func(s *types.SweepSpec) { s.Axes[0].Range.Max = nil },
			errors.PULSE_SWEEP_RANGE_INVALID, ReasonBoundMissing, "sweep.axes[0].range.max"},
		{"max not finite", func(s *types.SweepSpec) { s.Axes[0].Range.Max = fp(math.Inf(1)) },
			errors.PULSE_SWEEP_RANGE_INVALID, ReasonBoundNotFinite, "sweep.axes[0].range.max"},
		{"min NaN", func(s *types.SweepSpec) { s.Axes[0].Range.Min = fp(math.NaN()) },
			errors.PULSE_SWEEP_RANGE_INVALID, ReasonBoundNotFinite, "sweep.axes[0].range.min"},
		{"min equals max", func(s *types.SweepSpec) { s.Axes[0].Range.Min = fp(0.9) },
			errors.PULSE_SWEEP_RANGE_INVALID, ReasonRangeEmpty, "sweep.axes[0].range"},
		{"min above max", func(s *types.SweepSpec) { s.Axes[0].Range.Min = fp(2) },
			errors.PULSE_SWEEP_RANGE_INVALID, ReasonRangeEmpty, "sweep.axes[0].range"},
		{"span overflows", func(s *types.SweepSpec) {
			s.Axes[0].Range = &types.SweepRange{Min: fp(-math.MaxFloat64), Max: fp(math.MaxFloat64)}
		}, errors.PULSE_SWEEP_RANGE_INVALID, ReasonRangeEmpty, "sweep.axes[0].range"},
		{"scale unknown", func(s *types.SweepSpec) { s.Axes[0].Range.Scale = "sqrt" },
			errors.PULSE_SWEEP_RANGE_INVALID, ReasonScaleUnknown, "sweep.axes[0].range.scale"},
		{"log min zero", func(s *types.SweepSpec) { s.Axes[1].Range.Min = fp(0) },
			errors.PULSE_SWEEP_RANGE_INVALID, ReasonLogMinNotPositive, "sweep.axes[1].range.min"},
		{"log min negative", func(s *types.SweepSpec) { s.Axes[1].Range.Min = fp(-1) },
			errors.PULSE_SWEEP_RANGE_INVALID, ReasonLogMinNotPositive, "sweep.axes[1].range.min"},
		{"samples missing", func(s *types.SweepSpec) { s.Samples = 0 },
			errors.PULSE_SWEEP_SAMPLES_INVALID, ReasonSamplesMissing, "sweep.samples"},
		{"samples negative", func(s *types.SweepSpec) { s.Mode, s.Samples = types.SweepModeRandom, -3 },
			errors.PULSE_SWEEP_SAMPLES_INVALID, ReasonSamplesInvalid, "sweep.samples"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := sampledSpec(types.SweepModeLHS, 4)
			tc.mutate(spec)
			ce := requireCode(t, Validate(spec), tc.code, tc.reason)
			if ce.Details["field"] != tc.field {
				t.Fatalf("field %v, want %s", ce.Details["field"], tc.field)
			}
			_, err := Expand(sampledComposed(spec), nil)
			if xce := requireCode(t, err, tc.code, tc.reason); xce.Message != ce.Message {
				t.Fatalf("Expand message %q differs from Validate %q", xce.Message, ce.Message)
			}
		})
	}
	for _, mode := range []types.SweepMode{types.SweepModeRandom, types.SweepModeLHS} {
		if err := Validate(sampledSpec(mode, 3)); err != nil {
			t.Fatalf("valid %s spec refused: %v", mode, err)
		}
	}
}

// TestSweepRandom_ForEachRefusesSampling: an item for_each shares the
// axis rules but not the sampling modes — random / lhs are an unknown
// mode there and a range axis is refused.
func TestSweepRandom_ForEachRefusesSampling(t *testing.T) {
	rules := itemRules("windows[0]")
	ce := rules.validateAxes([]types.SweepAxis{{Name: "d", Range: &types.SweepRange{Min: fp(0), Max: fp(1)}}}, types.SweepModeLHS)
	if ce == nil || ce.Code != errors.PULSE_FOR_EACH_INVALID || ce.Details["reason"] != ReasonModeUnknown ||
		!reflect.DeepEqual(ce.Details["valid"], []string{"grid", "zip"}) {
		t.Fatalf("for_each lhs: %v", ce)
	}
	ce = rules.validateAxes([]types.SweepAxis{{Name: "d", Range: &types.SweepRange{Min: fp(0), Max: fp(1)}}}, "")
	if ce == nil || ce.Code != errors.PULSE_FOR_EACH_INVALID || ce.Details["reason"] != ReasonRangeUnsupported {
		t.Fatalf("for_each range axis: %v", ce)
	}
}

// drawn expands spec and returns each slot's axis values as floats, in
// slot order.
func drawn(t *testing.T, spec *types.SweepSpec) ([]map[string]float64, *Expansion) {
	t.Helper()
	exp, err := Expand(sampledComposed(spec), nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	out := make([]map[string]float64, len(exp.Composed.Requests))
	for i, req := range exp.Composed.Requests {
		row := map[string]float64{}
		for _, ax := range spec.Axes {
			key := map[string]string{"decay": "d", "lambda": "l", "x": "x", "y": "y", "z": "z"}[ax.Name]
			p, ok := req.Params[key]
			if !ok {
				t.Fatalf("slot %d has no param %s", i, key)
			}
			b, _ := json.Marshal(p)
			v, err := strconv.ParseFloat(string(b), 64)
			if err != nil {
				t.Fatalf("slot %d param %s = %s", i, key, b)
			}
			row[ax.Name] = v
		}
		out[i] = row
	}
	return out, exp
}

// stratum is the 0-based stratum of v among n equal-probability strata
// of rg (log space under scale log).
func stratum(rg *types.SweepRange, v float64, n int) int {
	lo, hi := *rg.Min, *rg.Max
	f := (v - lo) / (hi - lo)
	if rg.Scale == types.SweepScaleLog {
		f = (math.Log(v) - math.Log(lo)) / (math.Log(hi) - math.Log(lo))
	}
	k := int(math.Floor(f * float64(n)))
	return min(max(k, 0), n-1)
}

// TestSweepRandom_LHSStratifies: under lhs every axis (linear and log)
// is cut into `samples` strata and each stratum is drawn exactly once;
// every draw stays inside its range.
func TestSweepRandom_LHSStratifies(t *testing.T) {
	const n = 64
	spec := sampledSpec(types.SweepModeLHS, n)
	spec.RNGSeed = 11
	rows, _ := drawn(t, spec)
	if len(rows) != n {
		t.Fatalf("%d slots, want %d", len(rows), n)
	}
	for _, ax := range spec.Axes {
		seen := make([]int, n)
		for i, row := range rows {
			v := row[ax.Name]
			if v < *ax.Range.Min || v > *ax.Range.Max {
				t.Fatalf("slot %d %s=%v outside [%v, %v]", i, ax.Name, v, *ax.Range.Min, *ax.Range.Max)
			}
			seen[stratum(ax.Range, v, n)]++
		}
		for k, c := range seen {
			if c != 1 {
				t.Fatalf("axis %s stratum %d drawn %d times, want exactly once (%v)", ax.Name, k, c, seen)
			}
		}
	}
}

// TestSweepRandom_UniformAndLogUniform: random draws are uniform on a
// linear range and log-uniform under scale log — half the log draws on
// [1e-3, 1e3] fall below the geometric midpoint 1, against ~0.1% of
// linear draws.
func TestSweepRandom_UniformAndLogUniform(t *testing.T) {
	const n = 4000
	spec := sampledSpec(types.SweepModeRandom, n)
	spec.RNGSeed = 5
	rows, _ := drawn(t, spec)
	var sum float64
	below := 0
	for _, row := range rows {
		sum += row["decay"]
		if row["lambda"] < 1 {
			below++
		}
	}
	if mean := sum / n; math.Abs(mean-0.45) > 0.02 {
		t.Fatalf("linear mean %v, want ~0.45", mean)
	}
	if frac := float64(below) / n; math.Abs(frac-0.5) > 0.03 {
		t.Fatalf("log draws below 1: %v, want ~0.5", frac)
	}

	lin := sampledSpec(types.SweepModeRandom, n)
	lin.RNGSeed = 5
	lin.Axes[1].Range.Scale = types.SweepScaleLinear
	rows, _ = drawn(t, lin)
	below = 0
	for _, row := range rows {
		if row["lambda"] < 1 {
			below++
		}
	}
	if frac := float64(below) / n; frac > 0.01 {
		t.Fatalf("linear draws below 1 on [1e-3, 1e3]: %v, want ~0.001", frac)
	}
}

// TestSweepRandom_FrozenDraws pins the first draws of a fixed seed to
// their exact text: the stream is integer PCG words mapped by FMA-free
// arithmetic and internal/detmath, so these words hold on every
// architecture (CI runs amd64) — a drift here breaks replay by seed.
func TestSweepRandom_FrozenDraws(t *testing.T) {
	for _, tc := range []struct {
		mode types.SweepMode
		want []string
	}{
		{types.SweepModeRandom, frozenRandom},
		{types.SweepModeLHS, frozenLHS},
	} {
		spec := sampledSpec(tc.mode, 3)
		spec.RNGSeed = 42
		exp, err := Expand(sampledComposed(spec), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(exp.Labels, tc.want) {
			t.Fatalf("%s labels\n got %q\nwant %q", tc.mode, exp.Labels, tc.want)
		}
	}
}

var (
	frozenRandom = []string{
		"sample=0_decay=0.4086147095978019_lambda=685.587592159228",
		"sample=1_decay=0.6472160477269242_lambda=0.11810915859230853",
		"sample=2_decay=0.7545321895355136_lambda=466.34916657310265",
	}
	frozenLHS = []string{
		"sample=0_decay=0.21573868257564138_lambda=0.5420415790076353",
		"sample=1_decay=0.7036141787807105_lambda=0.004131589693187432",
		"sample=2_decay=0.5515107298451711_lambda=562.5549239486134",
	}
)

// TestSweepRandom_SeedReproducible: the same spec and seed give the
// identical slot list; another pinned seed gives another; a zero seed is
// derived from the spec (rng_seed excluded) — the same on every call and
// echoed on Expansion.Seed — and moves when the spec does.
func TestSweepRandom_SeedReproducible(t *testing.T) {
	enc := func(exp *Expansion) string {
		b, err := json.Marshal(exp.Composed.Requests)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	pinned := sampledSpec(types.SweepModeLHS, 8)
	pinned.RNGSeed = 99
	_, a := drawn(t, pinned)
	_, b := drawn(t, pinned)
	if enc(a) != enc(b) || a.Seed == nil || *a.Seed != 99 {
		t.Fatalf("pinned seed not reproducible (seed %v)", a.Seed)
	}
	other := sampledSpec(types.SweepModeLHS, 8)
	other.RNGSeed = 100
	_, c := drawn(t, other)
	if enc(c) == enc(a) {
		t.Fatal("seeds 99 and 100 drew the same slots")
	}

	zero := sampledSpec(types.SweepModeLHS, 8)
	_, z1 := drawn(t, zero)
	_, z2 := drawn(t, sampledSpec(types.SweepModeLHS, 8))
	if z1.Seed == nil || *z1.Seed == 0 || *z1.Seed != *z2.Seed || enc(z1) != enc(z2) {
		t.Fatalf("zero seed not deterministic: %v vs %v", z1.Seed, z2.Seed)
	}
	if *z1.Seed != EffectiveSeed(zero) {
		t.Fatalf("Expansion.Seed %d != EffectiveSeed %d", *z1.Seed, EffectiveSeed(zero))
	}
	// Pinning the derived seed replays the zero-seed run exactly.
	replay := sampledSpec(types.SweepModeLHS, 8)
	replay.RNGSeed = *z1.Seed
	_, r := drawn(t, replay)
	if enc(r) != enc(z1) {
		t.Fatal("pinning the echoed seed does not replay the run")
	}
	moved := sampledSpec(types.SweepModeLHS, 8)
	moved.Axes[0].Range.Max = fp(0.8)
	if EffectiveSeed(moved) == *z1.Seed {
		t.Fatal("derived seed ignores the spec")
	}
}

// TestSweepRandom_LabelsUniqueStable: a sampled slot's default label is
// `sample=<ordinal>_` then the `<axis>=<value>` pairs — unique, and
// stable for a seed; an authored pattern is used as written.
func TestSweepRandom_LabelsUniqueStable(t *testing.T) {
	spec := sampledSpec(types.SweepModeRandom, 50)
	spec.RNGSeed = 3
	_, exp := drawn(t, spec)
	seen := map[string]bool{}
	for i, l := range exp.Labels {
		if !strings.HasPrefix(l, "sample="+strconv.Itoa(i)+"_decay=") || !strings.Contains(l, "_lambda=") {
			t.Fatalf("label %d = %q", i, l)
		}
		if seen[l] {
			t.Fatalf("duplicate label %q", l)
		}
		seen[l] = true
		if exp.Composed.Requests[i].Label != l {
			t.Fatalf("slot %d label %q != %q", i, exp.Composed.Requests[i].Label, l)
		}
	}
	authored := sampledSpec(types.SweepModeRandom, 2)
	authored.RNGSeed = 3
	authored.Label = "d{{decay}}"
	_, a := drawn(t, authored)
	for _, l := range a.Labels {
		if !strings.HasPrefix(l, "d0.") {
			t.Fatalf("authored label %q", l)
		}
	}
}

// TestSweepRandom_CountAndPreflight: Count is samples, and the
// MaxComposeSlots preflight sees explicit + samples before any slot is
// drawn or rendered.
func TestSweepRandom_CountAndPreflight(t *testing.T) {
	spec := sampledSpec(types.SweepModeLHS, 7)
	if got := Count(spec); got != 7 {
		t.Fatalf("Count %d, want 7", got)
	}
	c := sampledComposed(spec)
	c.Requests = []*types.Request{{}, {}}
	var seen int
	boom := stderrors.New("over")
	_, err := Expand(c, func(slots int) error { seen = slots; return boom })
	if err != boom || seen != 9 {
		t.Fatalf("preflight saw %d (err %v), want 9 and its error", seen, err)
	}
}

// TestSweepRandom_EnumerationWireUnchanged: a grid / zip spec marshals
// without any sampling key, so its wire form (and CanonicalHash) is
// what it was before the sampling modes.
func TestSweepRandom_EnumerationWireUnchanged(t *testing.T) {
	spec := validSpec()
	spec.Mode = types.SweepModeZip
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"axes":[{"name":"tv","values":[10,0.5]},{"name":"_flag2","values":[true,"x"]}],"mode":"zip","request":{"cohort":{"filename":"c.pulse"}}}`
	if string(b) != want {
		t.Fatalf("grid/zip wire form\n got %s\nwant %s", b, want)
	}
	h1 := (&types.ComposedRequest{Sweep: spec}).Hash()
	var back types.ComposedRequest
	if err := json.Unmarshal([]byte(`{"requests":null,"sweep":`+want+`}`), &back); err != nil {
		t.Fatal(err)
	}
	if h2 := back.Hash(); h1 != h2 {
		t.Fatalf("hash moved across a wire round trip: %s vs %s", h1, h2)
	}
}

// TestSweepRandom_LatinHypercubeIsTheLHSDraw: LatinHypercube is the lhs
// sweep's own draw — over unit linear ranges the sweep's slot values are
// its points exactly — and every stratum of every dimension holds one
// point; nothing is drawn for n or dims below 1.
func TestSweepRandom_LatinHypercubeIsTheLHSDraw(t *testing.T) {
	spec := &types.SweepSpec{Mode: types.SweepModeLHS, Samples: 7, Axes: []types.SweepAxis{
		{Name: "a", Range: &types.SweepRange{Min: fp(0), Max: fp(1)}},
		{Name: "b", Range: &types.SweepRange{Min: fp(0), Max: fp(1)}},
	}}
	pts := LatinHypercube(42, 7, 2)
	forEachSample(spec, 42, func(slot int, values map[string]any) bool {
		for a, name := range []string{"a", "b"} {
			if got, want := values[name], sampledNumber(pts[a][slot]); got != want {
				t.Fatalf("slot %d axis %s: sweep %v, LatinHypercube %v", slot, name, got, want)
			}
		}
		return true
	})
	for a := range pts {
		seen := map[int]bool{}
		for _, u := range pts[a] {
			seen[int(u*7)] = true
		}
		if len(seen) != 7 {
			t.Fatalf("dimension %d strata %v: want each of 7 once", a, seen)
		}
	}
	if LatinHypercube(42, 0, 2) != nil || LatinHypercube(42, 3, 0) != nil {
		t.Fatal("an empty design drew points")
	}
}
