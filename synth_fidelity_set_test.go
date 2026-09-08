package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// buildMarketingSurveyCohort writes a minimal valid single-file .pulse
// cohort mirroring the headline "select all that apply" marketing
// survey use case the whole synth-from-sample effort was scoped
// around: a set_* field ("channels": email/sms/push) with real
// per-option variation, correlated with a categorical field ("region"),
// plus a genuinely bimodal, otherwise-unrelated numeric field
// ("loyalty_score") for ProfileOptions.FitShape (E4) to engage on.
//
// Associations, all deterministic (row%N patterns) except
// loyalty_score's own within-bucket noise, mirroring
// buildSetCategoricalCohort's "exact rational, not a statistical
// approximation" discipline for every non-numeric axis:
//   - channels[email]: flat 70% selection, independent of region.
//   - channels[sms]: region-dependent — 90% selected in eu, 10% in us
//     (the set-categorical pairing under test).
//   - channels[push]: flat 50% selection, independent of region —
//     included only for the marginal-delta assertion below; it has no
//     conditioned numeric partner in THIS cohort (see buildPushSpendCohort).
//   - loyalty_score: a clearly-separated two-component mixture (means
//     10/90, std 5 each — gap/avgStd = 16, far past
//     shapeFitMinSeparationStds), unrelated to every other field, so
//     --fit-shape has a genuine shape to find.
//
// Deliberately no numeric field conditioned by a set_* option here
// (that pairing — channels[push] -> spend — is proven on its own
// isolated cohort, buildPushSpendCohort), and only ONE set_* field
// (the set-set pairing is proven separately on buildCrossSellCohort).
// Both exclusions guard against the SAME class of problem, at two
// different layers:
//
//   - drawRow (synth/writer.go) applies each captured joint-structure
//     kind as a fixed post-processing stage over a shared per-Spec
//     priority order (E6-S1, synth/conflict.go): when two DIFFERENT
//     relationships each target the SAME field, only the
//     higher-priority one survives — the other is dropped with a
//     warning rather than silently overwritten (the pre-E6-S1 bug).
//     That is correct, deterministic behavior, not a bug this fixture
//     works around; it is precisely why a numeric field must not be
//     given TWO competing conditioning relationships if the intent is
//     to prove BOTH.
//   - A categorical field and a numeric field coexisting in the same
//     cohort unconditionally yields a captured (categorical -> numeric)
//     pairing regardless of correlation strength (computeConditional-
//     CategoricalNumericPairs has no significance gate), and
//     catNumPairs runs before setNumPairs in drawRow's stage order — so
//     "region" coexisting with a push-conditioned "spend" would have
//     the (region -> spend) pairing (real signal: none) win the claim
//     over (channels[push] -> spend) (real signal: the one under test),
//     silently defeating the very relationship this fixture exists to
//     prove. Isolating "spend" onto its own cohort with no coexisting
//     categorical field sidesteps the collision entirely, the same way
//     isolating the second set_* field sidesteps the set-set collision.
//
// See TestSynth_MarketingSurveyFidelityReport_SetDeltasWithinTolerance's
// three parts: this cohort proves marginal + set-categorical,
// buildPushSpendCohort proves set-numeric, and buildCrossSellCohort
// proves set-set.
func buildMarketingSurveyCohort(t *testing.T, seed int64) (data []byte, rowCount int) {
	t.Helper()
	regionOpts := []string{"us", "eu"}
	channelOpts := []string{"email", "sms", "push"}

	regionDict := encoding.NewDictionary()
	for _, v := range regionOpts {
		if _, err := regionDict.Add(v); err != nil {
			t.Fatalf("regionDict.Add(%q): %v", v, err)
		}
	}
	channelDict := encoding.NewDictionary()
	for _, v := range channelOpts {
		if _, err := channelDict.Add(v); err != nil {
			t.Fatalf("channelDict.Add(%q): %v", v, err)
		}
	}

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: regionDict},
		{Name: "channels", Type: encoding.FieldTypeSetU8, ByteOffset: 1, Dictionary: channelDict},
		{Name: "loyalty_score", Type: encoding.FieldTypeF64, ByteOffset: 2},
	}}

	const rowsPerRegion = 3000
	rowCount = rowsPerRegion * 2
	rng := rand.New(rand.NewSource(seed))

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	writeF64 := func(v float64) {
		bits := math.Float64bits(v)
		var b [8]byte
		for i := 0; i < 8; i++ {
			b[i] = byte(bits >> (8 * i))
		}
		buf.Write(b[:])
	}

	for r := 0; r < rowCount; r++ {
		regionIdx := r / rowsPerRegion
		if regionIdx >= len(regionOpts) {
			regionIdx = len(regionOpts) - 1
		}
		region := regionOpts[regionIdx]
		localIdx := r - regionIdx*rowsPerRegion

		emailSel := localIdx%10 < 7 // flat 70%
		var smsSel bool
		if region == "eu" {
			smsSel = localIdx%10 != 0 // 90% selected
		} else {
			smsSel = localIdx%10 == 0 // 10% selected
		}
		pushSel := localIdx%2 == 0 // flat 50%, independent

		var loyalty float64
		if r%2 == 0 {
			loyalty = rng.NormFloat64()*5.0 + 10.0
		} else {
			loyalty = rng.NormFloat64()*5.0 + 90.0
		}

		regionID, _ := regionDict.IDFor(region)
		var channelMask byte
		if emailSel {
			channelMask |= 1 << 0
		}
		if smsSel {
			channelMask |= 1 << 1
		}
		if pushSel {
			channelMask |= 1 << 2
		}

		buf.WriteByte(byte(regionID))
		buf.WriteByte(channelMask)
		writeF64(loyalty)
	}
	return buf.Bytes(), rowCount
}

// buildPushSpendCohort writes a minimal valid single-file .pulse cohort
// with exactly one set_u8 field ("channels", single option "push") and
// one numeric field ("spend") and NOTHING else — no categorical field
// for the profiler to also (unconditionally) pair "spend" against. See
// buildMarketingSurveyCohort's doc comment for why this isolation is
// required to observe the set-numeric joint-structure kind's own
// reconstruction, uncontaminated by a higher-priority
// categorical-numeric pairing (E6-S1, synth/conflict.go) claiming the
// same target field. channels[push] selection is flat 50%, independent
// of anything else; spend's mean is conditioned on it (45 vs 35, std
// 15) — the relationship under test.
func buildPushSpendCohort(t *testing.T, seed int64) (data []byte, rowCount int) {
	t.Helper()
	channelDict := encoding.NewDictionary()
	if _, err := channelDict.Add("push"); err != nil {
		t.Fatalf("channelDict.Add: %v", err)
	}

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "channels", Type: encoding.FieldTypeSetU8, ByteOffset: 0, Dictionary: channelDict},
		{Name: "spend", Type: encoding.FieldTypeF64, ByteOffset: 1},
	}}

	rowCount = 6000
	rng := rand.New(rand.NewSource(seed))

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	writeF64 := func(v float64) {
		bits := math.Float64bits(v)
		var b [8]byte
		for i := 0; i < 8; i++ {
			b[i] = byte(bits >> (8 * i))
		}
		buf.Write(b[:])
	}

	for r := 0; r < rowCount; r++ {
		pushSel := r%2 == 0 // flat 50%, independent

		spendMean := 35.0
		if pushSel {
			spendMean = 45.0
		}
		spend := rng.NormFloat64()*15.0 + spendMean

		var channelMask byte
		if pushSel {
			channelMask |= 1 << 0
		}

		buf.WriteByte(channelMask)
		writeF64(spend)
	}
	return buf.Bytes(), rowCount
}

// buildCrossSellCohort writes a minimal valid single-file .pulse cohort
// with exactly two set_u8 fields ("setA" option "x", "setB" option "y")
// and NOTHING else — no categorical or numeric field for the profiler
// to also (unconditionally) pair either set field against. See
// buildMarketingSurveyCohort's doc comment for why this isolation is
// required to observe the set-set joint-structure kind's own
// reconstruction, uncontaminated by a later drawRow stage overwriting
// the same bit. x and y co-occur (both selected or both unselected) on
// every row except one in flipEvery, mirroring synth package's own
// synthSetSetPair fixture (reimplemented here since this file lives in
// package pulse, a separate test binary from synth_test).
func buildCrossSellCohort(t *testing.T, rowCount, flipEvery int) []byte {
	t.Helper()
	dictA := encoding.NewDictionary()
	if _, err := dictA.Add("x"); err != nil {
		t.Fatalf("dictA.Add: %v", err)
	}
	dictB := encoding.NewDictionary()
	if _, err := dictB.Add("y"); err != nil {
		t.Fatalf("dictB.Add: %v", err)
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "setA", Type: encoding.FieldTypeSetU8, ByteOffset: 0, Dictionary: dictA},
		{Name: "setB", Type: encoding.FieldTypeSetU8, ByteOffset: 1, Dictionary: dictB},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for r := 0; r < rowCount; r++ {
		xSel := r%2 == 0
		ySel := xSel
		if r%flipEvery == 0 {
			ySel = !ySel
		}
		var maskA, maskB byte
		if xSel {
			maskA = 1
		}
		if ySel {
			maskB = 1
		}
		buf.WriteByte(maskA)
		buf.WriteByte(maskB)
	}
	return buf.Bytes()
}

// TestSynth_MarketingSurveyFidelityReport_SetDeltasWithinTolerance is
// this effort's non-negotiable, culminating acceptance test (E5-S4):
// the full pipeline — profile create --conditional --fit-shape ->
// synth from-profile --rows N --fidelity-report (here, via the library
// facade: p.Profile with IncludeConditional+FitShape, then
// synth.SpecFromProfile, then p.Synth with SourceCohort +
// FidelityReportPath) — run on the headline "select all that apply"
// marketing-survey fixture must produce a fidelity report whose set_*
// deltas, BOTH the per-option marginal section and the joint-structure
// pairwise sections, land within tolerance.
//
// The test has three parts, run through three independent full pipeline
// executions: the first proves marginal + set-categorical on the
// marketing-survey cohort (buildMarketingSurveyCohort); the second
// proves set-numeric on an isolated single-set-field, no-categorical
// cohort (buildPushSpendCohort); the third proves set-set on an
// isolated two-set-field cohort (buildCrossSellCohort) — see
// buildMarketingSurveyCohort's doc comment for why set-numeric and
// set-set each need their own isolated cohort rather than sharing one
// with a set-categorical pairing and a coexisting categorical field
// (drawRow's fixed per-Spec claim-priority order, E6-S1, means a target
// field named by two competing relationships has only the
// higher-priority one survive — correct, but fatal to proving BOTH
// relationships on one shared target within a single cohort). All
// three parts exercise the exact same production entry points
// (p.Profile/synth.SpecFromProfile/p.Synth) and the exact same
// BuildFidelityReport/BuildSet*Pairwise machinery this story adds, so
// together they are the proof that every epic in this effort — E1
// tagging/output, E2/E3 numeric/categorical structure (exercised
// incidentally via the ordinary capture path), E4 shape fitting, and
// E5 set_* support — composes correctly on real runs of the exact
// scenario the effort was scoped around, not isolated unit checks.
func TestSynth_MarketingSurveyFidelityReport_SetDeltasWithinTolerance(t *testing.T) {
	const marginalTolerance = 0.03
	const categoricalTolerance = 0.05
	const numericMeanTolerance = 3.0
	const numericStdTolerance = 2.0
	const setSetTolerance = 0.05
	const newRows = 20000

	// --- Part 1: marketing-survey cohort — marginal + set-categorical,
	// in one pipeline run. ---
	srcData, rowCount := buildMarketingSurveyCohort(t, 101)

	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// profile create --conditional --fit-shape
	prof, err := p.Profile(context.Background(), "/source.pulse", ProfileOptions{
		IncludeConditional: true,
		FitShape:           true,
	})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil {
		t.Fatal("expected a captured Conditional section with --conditional")
	}

	var channelsProfile, loyaltyProfile *synth.FieldProfile
	for i := range prof.Fields {
		switch prof.Fields[i].Name {
		case "channels":
			channelsProfile = &prof.Fields[i]
		case "loyalty_score":
			loyaltyProfile = &prof.Fields[i]
		}
	}
	if channelsProfile == nil || channelsProfile.Set == nil {
		t.Fatal("expected a captured Set profile for field \"channels\"")
	}
	// --fit-shape (E4) should engage on the clearly bimodal, unrelated
	// loyalty_score field — a soft check (not fatal): the acceptance
	// bar for this test is the set_* deltas below, not shape selection
	// specifically, but a fixture built to trigger it should show it.
	if loyaltyProfile == nil || loyaltyProfile.Numeric == nil {
		t.Fatal("expected a captured Numeric profile for field \"loyalty_score\"")
	} else if loyaltyProfile.Numeric.Shape == nil {
		t.Error("expected --fit-shape to keep a mixture Shape for the clearly bimodal loyalty_score field")
	}

	var foundSetCategorical bool
	for _, scp := range prof.Conditional.SetCategoricalPairs {
		if scp.Set == "channels" && scp.Option == "sms" && scp.Categorical == "region" {
			foundSetCategorical = true
		}
	}
	if !foundSetCategorical {
		t.Fatalf("expected a captured set-categorical pair channels[sms] x region, got %+v", prof.Conditional.SetCategoricalPairs)
	}

	spec, _ := synth.SpecFromProfile(prof, newRows)
	if len(spec.SetCategoricalPairs) == 0 {
		t.Fatal("expected SpecFromProfile to populate at least one set-categorical pair")
	}

	// synth from-profile --rows N --fidelity-report
	res, err := p.Synth(context.Background(), spec, "/augmented.pulse", SynthOptions{
		Seed:               102,
		SourceCohort:       "/source.pulse",
		FidelityReportPath: "/report.json",
		FidelityWarnings:   prof.Warnings,
	})
	if err != nil {
		t.Fatalf("augment synth: %v", err)
	}
	if res.FidelityReportPath != "/report.json" {
		t.Fatalf("FidelityReportPath = %q, want /report.json", res.FidelityReportPath)
	}

	raw, err := afero.ReadFile(fs, "/report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if report.SourceRows != rowCount || report.SyntheticRows != newRows {
		t.Errorf("SourceRows/SyntheticRows = %d/%d, want %d/%d", report.SourceRows, report.SyntheticRows, rowCount, newRows)
	}

	// Marginal section: per-option frequency delta (E5-S4 acceptance
	// criterion 1).
	var channelsFidelity *synth.SetFieldFidelity
	for _, sf := range report.SetFields {
		if sf.Field == "channels" {
			channelsFidelity = sf
		}
	}
	if channelsFidelity == nil {
		t.Fatal("expected a SetFields entry for \"channels\"")
	}
	if len(channelsFidelity.Options) == 0 {
		t.Fatal("expected at least one option entry for \"channels\"")
	}
	for _, opt := range channelsFidelity.Options {
		if opt.Error != "" {
			t.Errorf("channels option %s: unexpected Error: %q", opt.Value, opt.Error)
			continue
		}
		if opt.Delta > marginalTolerance {
			t.Errorf("channels option %s: marginal Delta = %.4f, want <= %.2f (source=%.4f synthetic=%.4f)",
				opt.Value, opt.Delta, marginalTolerance, opt.SourceFrequency, opt.SyntheticFrequency)
		}
	}

	// Pairwise section: set-categorical (E5-S4 acceptance criterion 2,
	// one of the three kinds — the other two, set-numeric and set-set,
	// are proven in Parts 2 and 3 below).
	var scPair *synth.SetCategoricalPairFidelity
	for _, pr := range report.SetCategoricalPairwise {
		if pr.Set == "channels" && pr.Option == "sms" && pr.Categorical == "region" {
			scPair = pr
		}
	}
	if scPair == nil {
		t.Fatalf("expected a SetCategoricalPairwise entry for channels[sms] x region, got %+v", report.SetCategoricalPairwise)
	}
	if scPair.Error != "" {
		t.Fatalf("set-categorical pair: unexpected Error: %q", scPair.Error)
	}
	if scPair.N == 0 {
		t.Error("set-categorical pair: N = 0, want > 0")
	}
	if scPair.Delta > categoricalTolerance {
		t.Errorf("set-categorical pair: Delta = %.4f, want <= %.2f", scPair.Delta, categoricalTolerance)
	}

	// --- Part 2: isolated push-spend cohort — set-numeric, the second
	// joint-structure kind (E5-S4 acceptance criterion 2). Isolated onto
	// its own cohort with no coexisting categorical field: see
	// buildMarketingSurveyCohort's doc comment for why a categorical
	// field paired against the same numeric target ("spend") would win
	// the E6-S1 claim-priority ordering and silently defeat this proof.
	psFS := afero.NewMemMapFs()
	pPS, err := New(Options{FS: psFS})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	psSrc, psRowCount := buildPushSpendCohort(t, 105)
	if err := afero.WriteFile(psFS, "/source.pulse", psSrc, 0o644); err != nil {
		t.Fatalf("write push-spend source: %v", err)
	}

	psProf, err := pPS.Profile(context.Background(), "/source.pulse", ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("push-spend profile: %v", err)
	}
	if psProf.Conditional == nil {
		t.Fatal("expected a captured Conditional section with --conditional")
	}

	var foundSetNumeric bool
	for _, snp := range psProf.Conditional.SetNumericPairs {
		if snp.Set == "channels" && snp.Option == "push" && snp.Numeric == "spend" {
			foundSetNumeric = true
		}
	}
	if !foundSetNumeric {
		t.Fatalf("expected a captured set-numeric pair channels[push] x spend, got %+v", psProf.Conditional.SetNumericPairs)
	}

	psSpec, _ := synth.SpecFromProfile(psProf, newRows)
	if len(psSpec.SetNumericPairs) == 0 {
		t.Fatal("expected SpecFromProfile to populate at least one set-numeric pair")
	}

	psRes, err := pPS.Synth(context.Background(), psSpec, "/augmented.pulse", SynthOptions{
		Seed:               106,
		SourceCohort:       "/source.pulse",
		FidelityReportPath: "/report.json",
		FidelityWarnings:   psProf.Warnings,
	})
	if err != nil {
		t.Fatalf("push-spend augment synth: %v", err)
	}
	if psRes.FidelityReportPath != "/report.json" {
		t.Fatalf("FidelityReportPath = %q, want /report.json", psRes.FidelityReportPath)
	}

	psRaw, err := afero.ReadFile(psFS, "/report.json")
	if err != nil {
		t.Fatalf("read push-spend report: %v", err)
	}
	var psReport synth.FidelityReport
	if err := json.Unmarshal(psRaw, &psReport); err != nil {
		t.Fatalf("unmarshal push-spend report: %v", err)
	}
	if psReport.SourceRows != psRowCount || psReport.SyntheticRows != newRows {
		t.Errorf("push-spend SourceRows/SyntheticRows = %d/%d, want %d/%d", psReport.SourceRows, psReport.SyntheticRows, psRowCount, newRows)
	}

	var snPair *synth.SetNumericPairFidelity
	for _, pr := range psReport.SetNumericPairwise {
		if pr.Set == "channels" && pr.Option == "push" && pr.Numeric == "spend" {
			snPair = pr
		}
	}
	if snPair == nil {
		t.Fatalf("expected a SetNumericPairwise entry for channels[push] x spend, got %+v", psReport.SetNumericPairwise)
	}
	if len(snPair.Categories) == 0 {
		t.Fatal("set-numeric pair: expected at least one bucket entry")
	}
	for _, cat := range snPair.Categories {
		if cat.Error != "" {
			t.Errorf("set-numeric pair bucket %s: unexpected Error: %q", cat.Category, cat.Error)
			continue
		}
		if cat.N == 0 {
			t.Errorf("set-numeric pair bucket %s: N = 0, want > 0", cat.Category)
		}
		if math.Abs(cat.MeanDelta) > numericMeanTolerance {
			t.Errorf("set-numeric pair bucket %s: MeanDelta = %.4f, want <= %.2f (source=%.4f synthetic=%.4f)",
				cat.Category, cat.MeanDelta, numericMeanTolerance, cat.SourceMean, cat.SyntheticMean)
		}
		if cat.StdDelta > numericStdTolerance {
			t.Errorf("set-numeric pair bucket %s: StdDelta = %.4f, want <= %.2f", cat.Category, cat.StdDelta, numericStdTolerance)
		}
	}

	// --- Part 3: isolated cross-sell cohort — set-set, the third
	// joint-structure kind (E5-S4 acceptance criterion 2). ---
	crossFS := afero.NewMemMapFs()
	pCross, err := New(Options{FS: crossFS})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	crossSrc := buildCrossSellCohort(t, 8000, 10) // 90% co-occurrence
	if err := afero.WriteFile(crossFS, "/source.pulse", crossSrc, 0o644); err != nil {
		t.Fatalf("write cross-sell source: %v", err)
	}

	crossProf, err := pCross.Profile(context.Background(), "/source.pulse", ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("cross-sell profile: %v", err)
	}
	if crossProf.Conditional == nil || len(crossProf.Conditional.SetSetPairs) != 1 {
		t.Fatalf("expected exactly one captured set-set pair, got Conditional=%+v", crossProf.Conditional)
	}

	crossSpec, _ := synth.SpecFromProfile(crossProf, newRows)
	if len(crossSpec.SetSetPairs) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one set-set pair, got %d", len(crossSpec.SetSetPairs))
	}

	crossRes, err := pCross.Synth(context.Background(), crossSpec, "/augmented.pulse", SynthOptions{
		Seed:               103,
		SourceCohort:       "/source.pulse",
		FidelityReportPath: "/report.json",
		FidelityWarnings:   crossProf.Warnings,
	})
	if err != nil {
		t.Fatalf("cross-sell augment synth: %v", err)
	}
	if crossRes.FidelityReportPath != "/report.json" {
		t.Fatalf("FidelityReportPath = %q, want /report.json", crossRes.FidelityReportPath)
	}

	crossRaw, err := afero.ReadFile(crossFS, "/report.json")
	if err != nil {
		t.Fatalf("read cross-sell report: %v", err)
	}
	var crossReport synth.FidelityReport
	if err := json.Unmarshal(crossRaw, &crossReport); err != nil {
		t.Fatalf("unmarshal cross-sell report: %v", err)
	}

	var ssPair *synth.SetSetPairFidelity
	for _, pr := range crossReport.SetSetPairwise {
		if pr.SetA == "setA" && pr.OptionA == "x" && pr.SetB == "setB" && pr.OptionB == "y" {
			ssPair = pr
		}
	}
	if ssPair == nil {
		t.Fatalf("expected a SetSetPairwise entry for setA[x] x setB[y], got %+v", crossReport.SetSetPairwise)
	}
	if ssPair.Error != "" {
		t.Fatalf("set-set pair: unexpected Error: %q", ssPair.Error)
	}
	if ssPair.N == 0 {
		t.Error("set-set pair: N = 0, want > 0")
	}
	if ssPair.Delta > setSetTolerance {
		t.Errorf("set-set pair: Delta = %.4f, want <= %.2f", ssPair.Delta, setSetTolerance)
	}
}
