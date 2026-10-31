package pulse

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
	"github.com/frankbardon/pulse/types"
)

// E2-S4 (FR-18): every scalar field type's marginal is scored, with an
// effect size beside it, and a flagged marginal on a field a structural
// rule writes names the rule. Each test drives the real bridge
// (writeSynthFidelityReport + runFidelityTest), so the operator a type
// is routed to must actually accept it.

// typesFixtureSpec is one field of every newly-scored type. shifted
// moves each one's marginal away from the unshifted source.
func typesFixtureSpec(rows int, shifted bool) *synth.Spec {
	p, levelW, end, hourW, push := 0.5, []any{8.0, 4.0, 2.0, 1.0}, "2026-03-31", morningHours(), 0.5
	if shifted {
		p, levelW, end, hourW, push = 0.8, []any{1.0, 2.0, 4.0, 8.0}, "2026-06-30", eveningHours(), 0.85
	}
	return &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "flag", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": p}},
			{Name: "level", Type: "u4", Distribution: synth.DistDiscrete,
				Params: map[string]any{"values": []any{1.0, 5.0, 9.0, 13.0}, "weights": levelW}},
			{Name: "joined", Type: "date", Distribution: synth.DistUniformDate,
				Params: map[string]any{"start": "2026-01-01", "end": end}},
			{Name: "seen", Type: "datetime", Distribution: synth.DistUniformDateTime,
				Params: map[string]any{"start": "2026-01-05", "end": "2026-03-29", "hour_weights": hourW}},
			{Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{"options": []any{"email", "push"}, "frequencies": []any{0.4, push}}},
		},
	}
}

func morningHours() []any {
	w := make([]any, 24)
	for h := range w {
		w[h] = 0.0
		if h >= 6 && h < 12 {
			w[h] = 1.0
		}
	}
	return w
}

func eveningHours() []any {
	w := make([]any, 24)
	for h := range w {
		w[h] = 0.0
		if h >= 17 && h < 23 {
			w[h] = 1.0
		}
	}
	return w
}

func typesReport(t *testing.T, twin *synth.Spec) (*synth.FidelityReport, []byte) {
	t.Helper()
	report, raw, err := writeUtilityReport(t, typesFixtureSpec(1500, false), twin,
		SynthOptions{FidelityReportPath: "/report.json"})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	return report, raw
}

// scored asserts name's entry ran test with a result and an effect size
// of measure, and returns it.
func scored(t *testing.T, report *synth.FidelityReport, name string, test types.TestType, measure string) *synth.FieldFidelity {
	t.Helper()
	e := fieldEntry(report, name)
	if e == nil {
		t.Fatalf("%s: no fields[] entry — the type is not scored", name)
	}
	if e.Test != test || e.Result == nil || e.Error != "" {
		t.Fatalf("%s: test %s result %v error %q, want %s with a result", name, e.Test, e.Result, e.Error, test)
	}
	if e.EffectSize == nil || e.EffectSize.Measure != measure {
		t.Fatalf("%s: effect size %+v, want %s", name, e.EffectSize, measure)
	}
	return e
}

// TestSynthFidelity_PackedBoolIsAProportionChiSq: a packed_bool is the
// χ² test of its 2x2 table against `_synthetic` (the two-sample
// proportion test) with V = |phi|; a 0.5 → 0.8 prevalence shift is a
// "small"-or-larger V and a faithful twin a "very small" one.
func TestSynthFidelity_PackedBoolIsAProportionChiSq(t *testing.T) {
	t.Parallel()

	shifted, _ := typesReport(t, typesFixtureSpec(1500, true))
	e := scored(t, shifted, "flag", types.TEST_CHISQ, synth.EffectMeasureCramersV)
	if e.EffectSize.Value < 0.2 || e.EffectSize.Magnitude == "very small" {
		t.Errorf("shifted flag V = %+v, want ≥ 0.2", e.EffectSize)
	}
	faithful, _ := typesReport(t, typesFixtureSpec(1500, false))
	if f := scored(t, faithful, "flag", types.TEST_CHISQ, synth.EffectMeasureCramersV); f.EffectSize.Magnitude != "very small" {
		t.Errorf("faithful flag V = %+v, want very small", f.EffectSize)
	}
}

// TestSynthFidelity_U4IsKSWithCohensD: a u4 rides the same KS + d arm
// as the wider integers; reversing its level weights moves the mean by
// well over one SD.
func TestSynthFidelity_U4IsKSWithCohensD(t *testing.T) {
	t.Parallel()

	report, _ := typesReport(t, typesFixtureSpec(1500, true))
	e := scored(t, report, "level", types.TEST_KS, synth.EffectMeasureCohensD)
	if e.EffectSize.Value < 0.8 || !e.Result.RejectNull {
		t.Errorf("level d = %+v, reject %v; want a large positive d and a rejected KS", e.EffectSize, e.Result.RejectNull)
	}
}

// TestSynthFidelity_DateIsKSOverEpochDays: a date's KS and d read its
// epoch days; widening the range from one quarter to two moves both.
func TestSynthFidelity_DateIsKSOverEpochDays(t *testing.T) {
	t.Parallel()

	report, _ := typesReport(t, typesFixtureSpec(1500, true))
	e := scored(t, report, "joined", types.TEST_KS, synth.EffectMeasureCohensD)
	if e.EffectSize.Value < 0.8 || !e.Result.RejectNull {
		t.Errorf("joined d = %+v, reject %v; want a large positive d", e.EffectSize, e.Result.RejectNull)
	}
	if e.Shape != nil {
		t.Errorf("joined carries a datetime shape %+v", e.Shape)
	}
}

// TestSynthFidelity_DateTimeShapeCatchesTheWrongHours: the same days at
// evening hours instead of morning ones. d over epoch seconds stays
// small — the instants barely move as a line — while the hour-of-day
// TVD is 1 (the two cycles share no hour) and the weekday TVD stays
// near 0. That gap is why the shape rides beside the KS entry.
func TestSynthFidelity_DateTimeShapeCatchesTheWrongHours(t *testing.T) {
	t.Parallel()

	report, _ := typesReport(t, typesFixtureSpec(1500, true))
	e := scored(t, report, "seen", types.TEST_KS, synth.EffectMeasureCohensD)
	if e.EffectSize.Magnitude != "very small" {
		t.Errorf("seen d = %+v, want very small (same days, other hours)", e.EffectSize)
	}
	if e.Shape == nil {
		t.Fatal("seen: no shape")
	}
	if e.Shape.Clock != synth.TimeShapeClockUTC || e.Shape.HourTVD != 1 || e.Shape.WeekdayTVD > 0.1 {
		t.Errorf("seen shape = %+v, want utc, hour_tvd 1, weekday_tvd ≤ 0.1", e.Shape)
	}

	faithful, _ := typesReport(t, typesFixtureSpec(1500, false))
	f := scored(t, faithful, "seen", types.TEST_KS, synth.EffectMeasureCohensD)
	if f.Shape == nil || f.Shape.HourTVD > 0.1 {
		t.Errorf("faithful seen shape = %+v, want hour_tvd ≤ 0.1", f.Shape)
	}
}

// TestSynthFidelity_SetOptionsCarryAnEffectSize: each set option's
// rates gain V of its own 2x2 table; the shifted option reads at least
// "small", the unshifted one "very small".
func TestSynthFidelity_SetOptionsCarryAnEffectSize(t *testing.T) {
	t.Parallel()

	report, _ := typesReport(t, typesFixtureSpec(1500, true))
	if len(report.SetFields) != 1 {
		t.Fatalf("set_fields = %d entries, want 1", len(report.SetFields))
	}
	opts := map[string]*synth.SetOptionFidelity{}
	for _, o := range report.SetFields[0].Options {
		opts[o.Value] = o
	}
	push, email := opts["push"], opts["email"]
	if push == nil || push.EffectSize == nil || push.EffectSize.Measure != synth.EffectMeasureCramersV || push.EffectSize.Magnitude == "very small" {
		t.Errorf("push = %+v, want V of at least small", push)
	}
	if email == nil || email.EffectSize == nil || email.EffectSize.Magnitude != "very small" {
		t.Errorf("email = %+v, want V very small", email)
	}
}

// TestSynthFidelity_RuleNoteNamesTheRuleBehindAFlag: a rule forcing
// `flag` on 30% of generated rows flags its marginal, and the entry
// names the rule; `level`, flagged by its own shift and written by no
// rule, gets no note; `joined`, written by a rule but not flagged,
// gets none either.
func TestSynthFidelity_RuleNoteNamesTheRuleBehindAFlag(t *testing.T) {
	t.Parallel()

	twin := typesFixtureSpec(1500, false)
	twin.Fields[1].Params["weights"] = []any{1.0, 2.0, 4.0, 8.0} // level shifted, no rule
	twin.Rules = []synth.RuleSpec{
		{When: "level >= 9", Set: map[string]any{"flag": 1.0}},
		{SetExpr: map[string]string{"joined": "joined"}},
	}
	report, raw := typesReport(t, twin)

	flag := scored(t, report, "flag", types.TEST_CHISQ, synth.EffectMeasureCramersV)
	if flag.EffectSize.Magnitude == "very small" {
		t.Fatalf("flag V = %+v: the rule did not move the marginal enough to flag", flag.EffectSize)
	}
	if !strings.Contains(flag.RuleNote, "structural rule 0 (set)") || !strings.Contains(flag.RuleNote, "generated rows only") {
		t.Errorf("flag rule_note = %q, want it to name structural rule 0 (set)", flag.RuleNote)
	}
	if lv := fieldEntry(report, "level"); lv == nil || lv.EffectSize == nil || lv.EffectSize.Magnitude == "very small" || lv.RuleNote != "" {
		t.Errorf("level = %+v: want flagged with no rule note", lv)
	}
	if j := fieldEntry(report, "joined"); j == nil || j.RuleNote != "" {
		t.Errorf("joined = %+v: written by a rule but unflagged, want no note", j)
	}
	if bytes.Count(raw, []byte(`"rule_note"`)) != 1 {
		t.Errorf("raw report carries %d rule_note keys, want 1", bytes.Count(raw, []byte(`"rule_note"`)))
	}
}

// TestSynthFidelity_RuleNoteOnASetField: a rule selecting `push` on
// every generated row flags the set field, and the set_fields entry
// names the rule.
func TestSynthFidelity_RuleNoteOnASetField(t *testing.T) {
	t.Parallel()

	twin := typesFixtureSpec(1500, false)
	twin.Rules = []synth.RuleSpec{{Set: map[string]any{"channels": []any{"push"}}}}
	report, _ := typesReport(t, twin)
	if len(report.SetFields) != 1 || !strings.Contains(report.SetFields[0].RuleNote, "structural rule 0 (set)") {
		t.Errorf("set_fields = %+v, want a rule note naming structural rule 0 (set)", report.SetFields)
	}
}

// TestSynthFidelity_ReportWithoutTheNewTypesCarriesNoNewKey: a report
// over only the previously scored types (u32, f64, categorical) gains
// neither key, so it is byte-identical to before (measured: the
// report's SHA-256 is unchanged across this story on this fixture).
func TestSynthFidelity_ReportWithoutTheNewTypesCarriesNoNewKey(t *testing.T) {
	t.Parallel()

	src := fidelitySmallSpec(300)
	twin := fidelitySmallSpec(300)
	twin.Rules = []synth.RuleSpec{{SetExpr: map[string]string{"score": "score"}}}
	report, raw, err := writeUtilityReport(t, src, twin, SynthOptions{FidelityReportPath: "/report.json"})
	if err != nil {
		t.Fatalf("synth: %v", err)
	}
	for _, key := range []string{`"shape"`, `"rule_note"`} {
		if bytes.Contains(raw, []byte(key)) {
			t.Errorf("report carries %s for a cohort without the new types or a flag", key)
		}
	}
	if len(report.Fields) != 3 {
		t.Errorf("fields = %d, want 3 (id, score, country)", len(report.Fields))
	}
}
