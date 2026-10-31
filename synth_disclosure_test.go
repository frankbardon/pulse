package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
	"github.com/spf13/afero"
)

// E3-S2 (FR-21) facade tests: the fidelity report's disclosure section.

// runDisclosureTwin synthesises a source from sourceSpec at seed 61, tops
// it up from twinSpec at seed 62 with opts, and returns the output and
// report bytes (nil report when none was written).
func runDisclosureTwin(t *testing.T, p *Pulse, fs afero.Fs, sourceSpec, twinSpec *synth.Spec, opts SynthOptions) (out, report []byte, err error) {
	t.Helper()
	ctx := context.Background()
	if _, err := p.Synth(ctx, sourceSpec, "/source.pulse", SynthOptions{Seed: 61}); err != nil {
		t.Fatalf("source synth: %v", err)
	}
	opts.Seed, opts.SourceCohort = 62, "/source.pulse"
	if _, err := p.Synth(ctx, twinSpec, "/twin.pulse", opts); err != nil {
		return nil, nil, err
	}
	out, rerr := afero.ReadFile(fs, "/twin.pulse")
	if rerr != nil {
		t.Fatalf("read output: %v", rerr)
	}
	if opts.FidelityReportPath != "" {
		if report, rerr = afero.ReadFile(fs, opts.FidelityReportPath); rerr != nil {
			t.Fatalf("read report: %v", rerr)
		}
	}
	return out, report, nil
}

func newDisclosurePulse(t *testing.T, l Limits) (*Pulse, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs, Limits: l})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, fs
}

func parseDisclosure(t *testing.T, raw []byte) *synth.DisclosureFidelity {
	t.Helper()
	var rep synth.FidelityReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	return rep.Disclosure
}

// TestSynth_DisclosureSectionIsDeterministicInBothLayouts: the section is
// on by default with a fidelity report, byte-identical for a fixed seed,
// and the same whether the output is tagged or synthetic-only (both read
// the tagged cohort).
func TestSynth_DisclosureSectionIsDeterministicInBothLayouts(t *testing.T) {
	t.Parallel()
	var reports [][]byte
	for _, only := range []bool{false, false, true} {
		p, fs := newDisclosurePulse(t, Limits{})
		_, rep, err := runDisclosureTwin(t, p, fs, utilityFixtureSpec(300, 100, 0, 0.5), utilityFixtureSpec(200, 100, 0, 0.5),
			SynthOptions{FidelityReportPath: "/r.json", SyntheticOnly: only})
		if err != nil {
			t.Fatalf("synth (synthetic-only=%v): %v", only, err)
		}
		reports = append(reports, rep)
	}
	d := parseDisclosure(t, reports[0])
	if d == nil {
		t.Fatalf("report carries no disclosure section:\n%s", reports[0])
	}
	if d.Distance != synth.DisclosureDistanceGower || d.Columns != 4 || d.SourceRows != 300 ||
		d.SyntheticRows != 200 || d.SampleRows != 200 || d.Error != "" {
		t.Errorf("disclosure = %+v", d)
	}
	// Continuous normal columns: no generated row repeats a source row,
	// and every one sits a positive distance away.
	if d.ExactMatches != 0 || !(d.DCRP5 > 0 && d.DCRMedian >= d.DCRP5 && d.DCRMedian < 1) {
		t.Errorf("disclosure = %+v, want no exact match and 0 < p5 <= median < 1", d)
	}
	for i := 1; i < len(reports); i++ {
		if !bytes.Equal(reports[i], reports[0]) {
			t.Errorf("run %d's report differs from run 0's", i)
		}
	}
}

// TestSynth_DisclosureExactCopiesReportMatchesAndZeroDistance: a twin
// whose every column has one value copies source rows exactly, and the
// section says so.
func TestSynth_DisclosureExactCopiesReportMatchesAndZeroDistance(t *testing.T) {
	t.Parallel()
	constant := func(rows int) *synth.Spec {
		return &synth.Spec{RowCount: rows, Fields: []synth.FieldSpec{
			{Name: "segment", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"a"}}},
			{Name: "flag", Type: "packed_bool", Distribution: synth.DistBernoulli, Params: map[string]any{"p": 1.0}},
		}}
	}
	p, fs := newDisclosurePulse(t, Limits{})
	_, rep, err := runDisclosureTwin(t, p, fs, constant(40), constant(25), SynthOptions{FidelityReportPath: "/r.json"})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	d := parseDisclosure(t, rep)
	if d == nil || d.ExactMatches != 25 || d.ExactMatchRate != 1 || d.DCRMedian != 0 || d.DCRP5 != 0 {
		t.Errorf("disclosure = %+v, want 25 exact matches, rate 1, DCR 0", d)
	}
}

// TestSynth_DisclosureOffLeavesOutputAndReportOtherwiseIdentical: the
// section draws no generation RNG, so the output is byte-identical with
// it on or off, and switching it off removes only its key.
func TestSynth_DisclosureOffLeavesOutputAndReportOtherwiseIdentical(t *testing.T) {
	t.Parallel()
	run := func(off bool, sample int) ([]byte, map[string]json.RawMessage) {
		p, fs := newDisclosurePulse(t, Limits{})
		out, rep, err := runDisclosureTwin(t, p, fs, utilityFixtureSpec(120, 100, 0, 0.5), utilityFixtureSpec(90, 100, 0, 0.5),
			SynthOptions{FidelityReportPath: "/r.json", DisableDisclosure: off, DisclosureSample: sample})
		if err != nil {
			t.Fatalf("synth: %v", err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(rep, &m); err != nil {
			t.Fatal(err)
		}
		return out, m
	}
	onOut, on := run(false, 0)
	offOut, off := run(true, 0)
	cappedOut, capped := run(false, 10)
	if !bytes.Equal(onOut, offOut) || !bytes.Equal(onOut, cappedOut) {
		t.Errorf("the generated output moved with the disclosure section")
	}
	if _, ok := off["disclosure"]; ok {
		t.Errorf("DisableDisclosure still wrote a disclosure section")
	}
	delete(on, "disclosure")
	onRest, _ := json.Marshal(on)
	offRest, _ := json.Marshal(off)
	if !bytes.Equal(onRest, offRest) {
		t.Errorf("switching disclosure off moved another section")
	}
	var d synth.DisclosureFidelity
	if err := json.Unmarshal(capped["disclosure"], &d); err != nil || d.SampleRows != 10 || d.SyntheticRows != 90 {
		t.Errorf("capped disclosure = %+v (%v), want 10 of 90 sampled", d, err)
	}
}

// TestSynth_DisclosureOverBudgetRefusesBeforeGeneration: the section's
// working set is charged to MaxEstimatedMemory; past it the call is
// refused PULSE_LIMIT_EXCEEDED and writes nothing, and the same instance
// runs once the section is switched off.
func TestSynth_DisclosureOverBudgetRefusesBeforeGeneration(t *testing.T) {
	t.Parallel()
	p, fs := newDisclosurePulse(t, Limits{MaxEstimatedMemory: 1024})
	_, _, err := runDisclosureTwin(t, p, fs, utilityFixtureSpec(300, 100, 0, 0.5), utilityFixtureSpec(200, 100, 0, 0.5),
		SynthOptions{FidelityReportPath: "/r.json"})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED || ce.Details["limit"] != "max_estimated_memory" {
		t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED on max_estimated_memory", err)
	}
	for _, path := range []string{"/twin.pulse", "/r.json"} {
		if ok, _ := afero.Exists(fs, path); ok {
			t.Errorf("a refused call wrote %s", path)
		}
	}
	if _, _, err := runDisclosureTwin(t, p, fs, utilityFixtureSpec(300, 100, 0, 0.5), utilityFixtureSpec(200, 100, 0, 0.5),
		SynthOptions{FidelityReportPath: "/r.json", DisableDisclosure: true}); err != nil {
		t.Errorf("disclosure off: %v, want the run to fit", err)
	}
}
