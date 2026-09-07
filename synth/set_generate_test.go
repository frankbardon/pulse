package synth_test

import (
	"bytes"
	"context"
	"math"
	"math/rand"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// readSetFieldRows decodes a set_* field's per-row raw bitmask and
// resolved selected-label list, in file order, plus the field's
// dictionary size — so a caller can assert every generated mask's bits
// fall within [0, dictSize), the "no malformed masks" acceptance bar
// (E5-S3).
func readSetFieldRows(t *testing.T, data []byte, name string) (masks []uint64, labels [][]string, dictSize int) {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	f := schema.Field(name)
	if f == nil || f.Dictionary == nil {
		t.Fatalf("not a set field: %s", name)
	}
	dictSize = f.Dictionary.Count()
	opts := f.Dictionary.Values()
	rr := encoding.NewRecordReader(r, schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	for {
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			break
		}
		mask, _ := wide[name].(uint64)
		masks = append(masks, mask)
		var sel []string
		for i, opt := range opts {
			if mask&(uint64(1)<<uint(i)) != 0 {
				sel = append(sel, opt)
			}
		}
		labels = append(labels, sel)
	}
	return masks, labels, dictSize
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

// TestSynth_SetBernoulli_GeneratesValidMasksAndMarginalFrequencies is
// E5-S3's non-negotiable acceptance bar exercised directly against a
// hand-authored spec (from-schema path, no profile involved): every
// generated mask must be a valid bitmask (no bit beyond the declared
// option count) and each option's generated selection frequency must
// match its declared Bernoulli frequency within tolerance.
func TestSynth_SetBernoulli_GeneratesValidMasksAndMarginalFrequencies(t *testing.T) {
	options := []string{"email", "sms", "push"}
	freqs := []float64{0.7, 0.3, 0.9}
	const rowCount = 20000
	const tolerance = 0.02

	optionsAny := make([]any, len(options))
	freqsAny := make([]any, len(freqs))
	for i := range options {
		optionsAny[i] = options[i]
		freqsAny[i] = freqs[i]
	}
	spec := &synth.Spec{
		RowCount: rowCount,
		Fields: []synth.FieldSpec{
			{Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{"options": optionsAny, "frequencies": freqsAny}},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}

	masks, labels, dictSize := readSetFieldRows(t, data, "channels")
	if len(masks) != rowCount {
		t.Fatalf("row count = %d, want %d", len(masks), rowCount)
	}
	if dictSize != len(options) {
		t.Fatalf("dictionary size = %d, want %d", dictSize, len(options))
	}
	for i, m := range masks {
		if m>>uint(len(options)) != 0 {
			t.Fatalf("row %d: mask %#x has bits set beyond declared option count %d", i, m, len(options))
		}
	}

	counts := make([]int, len(options))
	for _, sel := range labels {
		for _, s := range sel {
			for i, opt := range options {
				if opt == s {
					counts[i]++
				}
			}
		}
	}
	for i, opt := range options {
		got := float64(counts[i]) / float64(rowCount)
		if math.Abs(got-freqs[i]) > tolerance {
			t.Errorf("option %q generated frequency = %.4f, want within %.2f of declared %.2f",
				opt, got, tolerance, freqs[i])
		}
	}
}

// TestSynth_SetBernoulli_DeterministicByteIdentical locks in the
// Determinism contract (skills/synthetic-data.md) for the set_bernoulli
// sampler specifically: its row value is a map[string]bool, and Go map
// iteration order is randomized per process, so this guards against a
// regression that accidentally makes bit assignment depend on that
// iteration order (see buildSchema's dictionary pre-registration
// comment).
func TestSynth_SetBernoulli_DeterministicByteIdentical(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 500,
		Fields: []synth.FieldSpec{
			{Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{
					"options":     []any{"a", "b", "c", "d"},
					"frequencies": []any{0.5, 0.5, 0.5, 0.5},
				}},
		},
	}
	a, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("first synth: %v", err)
	}
	b, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("second synth: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("byte mismatch for identical (spec, seed): len(a)=%d len(b)=%d", len(a), len(b))
	}
}

// TestSynth_SetJointStructure_DeterministicByteIdentical extends the
// determinism guard to the set-categorical conditional transform, whose
// fallback probability is accumulated from SetCategoricalPairSpec.Cells
// — deliberately a slice walk, never a map range, so this locks that in.
func TestSynth_SetJointStructure_DeterministicByteIdentical(t *testing.T) {
	spec := &synth.Spec{
		RowCount: 500,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"us", "eu"}, "weights": []any{0.5, 0.5}}},
			{Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{"options": []any{"a", "b"}, "frequencies": []any{0.5, 0.5}}},
		},
		SetCategoricalPairs: []synth.SetCategoricalPairSpec{
			{
				Set: "channels", Option: "a", Categorical: "region",
				Cells: []synth.CategoricalPairCellSpec{
					{AValue: "selected", BValue: "us", Count: 90},
					{AValue: "not_selected", BValue: "us", Count: 10},
					{AValue: "selected", BValue: "eu", Count: 10},
					{AValue: "not_selected", BValue: "eu", Count: 90},
				},
			},
		},
	}
	a, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("first synth: %v", err)
	}
	b, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
	if err != nil {
		t.Fatalf("second synth: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("byte mismatch for identical (spec, seed): len(a)=%d len(b)=%d", len(a), len(b))
	}
}

// TestAugmentFromProfile_SetMarginalMatchesSourceWithinTolerance is
// E5-S3's full profile -> generate round-trip acceptance bar for the
// marginal (non-joint) case, through the ACTUAL production path behind
// `synth from-profile` (AugmentFromProfile via p.Synth with
// SourceCohort set): per-option selection frequency in the newly
// GENERATED partition must match the source's captured E5-S1 frequency
// within tolerance, with no --conditional involved — proving joint
// structure is additive, not required, for correct generation.
func TestAugmentFromProfile_SetMarginalMatchesSourceWithinTolerance(t *testing.T) {
	options := []string{"email", "sms", "push", "chat"}
	const rowCount = 4000
	// Exact frequencies: 1.0, 0.5, 0.2, 0.025.
	counts := []int{4000, 2000, 800, 100}
	srcData := buildSetFieldCohort(t, options, counts, rowCount)

	const newRows = 20000
	const tolerance = 0.03

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	var fieldProfile *synth.FieldProfile
	for i := range prof.Fields {
		if prof.Fields[i].Name == "channels" {
			fieldProfile = &prof.Fields[i]
		}
	}
	if fieldProfile == nil || fieldProfile.Set == nil {
		t.Fatalf("expected a captured Set profile for field %q", "channels")
	}

	spec := synth.SpecFromProfile(prof, newRows)
	var channelsSpec *synth.FieldSpec
	for i := range spec.Fields {
		if spec.Fields[i].Name == "channels" {
			channelsSpec = &spec.Fields[i]
		}
	}
	if channelsSpec == nil || channelsSpec.Distribution != synth.DistSetBernoulli {
		t.Fatalf("expected SpecFromProfile to reconstruct %q as %q, got %+v",
			"channels", synth.DistSetBernoulli, channelsSpec)
	}

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse", pulse.SynthOptions{
		Seed: 11, SourceCohort: "/source.pulse",
	}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	augmented, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	masks, labels, dictSize := readSetFieldRows(t, augmented, "channels")
	if len(masks) != rowCount+newRows {
		t.Fatalf("augmented row count = %d, want %d", len(masks), rowCount+newRows)
	}
	if dictSize < len(options) {
		t.Fatalf("merged dictionary size = %d, want >= %d", dictSize, len(options))
	}
	for i, m := range masks {
		if m>>uint(dictSize) != 0 {
			t.Fatalf("row %d: mask %#x has bits set beyond dictionary size %d", i, m, dictSize)
		}
	}

	genLabels := labels[rowCount:]
	genCounts := make(map[string]int, len(options))
	for _, sel := range genLabels {
		for _, s := range sel {
			genCounts[s]++
		}
	}
	for i, opt := range options {
		want := float64(counts[i]) / float64(rowCount)
		got := float64(genCounts[opt]) / float64(len(genLabels))
		if math.Abs(got-want) > tolerance {
			t.Errorf("option %q generated frequency = %.4f, want within %.2f of source's %.4f",
				opt, got, tolerance, want)
		}
	}
}

// TestAugmentFromProfile_ReconstructsSetCategoricalAssociationWithinTolerance
// is E5-S3's acceptance bar for the set-categorical pair kind, driven by
// the E5-S2 fixture shape (buildSetCategoricalCohort): the generated
// partition must reproduce the source's per-region conditional
// selection rate for a feature with a genuine regional association.
func TestAugmentFromProfile_ReconstructsSetCategoricalAssociationWithinTolerance(t *testing.T) {
	featureOpts := []string{"darkmode", "export", "api", "sso"}
	regionOpts := []string{"us", "eu"}
	const rowsPerRegion = 3000
	const rowCount = rowsPerRegion * 2
	const newRows = 20000
	const tolerance = 0.05

	featureSelected := func(row int, region string, featureIdx int) bool {
		switch featureIdx {
		case 1: // export
			if region == "eu" {
				return row%10 != 0 // 90% selected
			}
			return row%10 == 0 // 10% selected
		default:
			return row%2 == 0 // flat 50%, no association
		}
	}
	srcData := buildSetCategoricalCohort(t, featureOpts, regionOpts, featureSelected, rowCount)

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.SetCategoricalPairs) != len(featureOpts) {
		t.Fatalf("expected %d captured set-categorical pairs, got Conditional=%+v", len(featureOpts), prof.Conditional)
	}

	spec := synth.SpecFromProfile(prof, newRows)
	if len(spec.SetCategoricalPairs) != len(featureOpts) {
		t.Fatalf("expected SpecFromProfile to populate %d set-categorical pairs, got %d",
			len(featureOpts), len(spec.SetCategoricalPairs))
	}

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse", pulse.SynthOptions{
		Seed: 13, SourceCohort: "/source.pulse",
	}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	augmented, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	_, labels, _ := readSetFieldRows(t, augmented, "features")
	region := readCategoricalField(t, augmented, "region")
	if len(labels) != rowCount+newRows || len(region) != rowCount+newRows {
		t.Fatalf("augmented row count mismatch: labels=%d region=%d, want %d", len(labels), len(region), rowCount+newRows)
	}
	genLabels, genRegion := labels[rowCount:], region[rowCount:]

	rate := func(wantRegion, opt string) (float64, int) {
		n, sel := 0, 0
		for i := range genRegion {
			if genRegion[i] != wantRegion {
				continue
			}
			n++
			if hasLabel(genLabels[i], opt) {
				sel++
			}
		}
		if n == 0 {
			return 0, 0
		}
		return float64(sel) / float64(n), n
	}

	euRate, euN := rate("eu", "export")
	usRate, usN := rate("us", "export")
	if euN == 0 || usN == 0 {
		t.Fatalf("expected generated rows for both eu (n=%d) and us (n=%d)", euN, usN)
	}
	if math.Abs(euRate-0.9) > tolerance {
		t.Errorf("generated P(export selected | eu) = %.4f (n=%d), want within %.2f of source's 0.90", euRate, euN, tolerance)
	}
	if math.Abs(usRate-0.1) > tolerance {
		t.Errorf("generated P(export selected | us) = %.4f (n=%d), want within %.2f of source's 0.10", usRate, usN, tolerance)
	}
}

// synthSetNumericPair writes a cohort with a set_u8 field "tier" (single
// option "premium") and a numeric field "spend" whose value is drawn
// from Normal(meanSelected, std) when "premium" is selected and
// Normal(meanNotSelected, std) otherwise — a genuine set-numeric
// dependency for the generation-side round-trip test.
func synthSetNumericPair(t *testing.T, rowCount int, seed int64, meanSelected, meanNotSelected, std, pSelected float64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	featureDict := encoding.NewDictionary()
	if _, err := featureDict.Add("premium"); err != nil {
		t.Fatalf("featureDict.Add: %v", err)
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "spend", Type: encoding.FieldTypeF64, ByteOffset: 0},
			{Name: "tier", Type: encoding.FieldTypeSetU8, ByteOffset: 8, Dictionary: featureDict},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for r := 0; r < rowCount; r++ {
		selected := rng.Float64() < pSelected
		mean := meanNotSelected
		if selected {
			mean = meanSelected
		}
		spend := rng.NormFloat64()*std + mean
		var rec [9]byte
		bits := math.Float64bits(spend)
		for i := 0; i < 8; i++ {
			rec[i] = byte(bits >> (8 * i))
		}
		if selected {
			rec[8] = 0b1
		}
		buf.Write(rec[:])
	}
	return buf.Bytes()
}

// TestAugmentFromProfile_ReconstructsSetNumericMeanWithinTolerance is
// E5-S3's acceptance bar for the set-numeric pair kind: the generated
// partition's numeric mean, conditioned on the paired set option's
// selected/not_selected state, must reproduce the source's captured
// per-bucket mean.
func TestAugmentFromProfile_ReconstructsSetNumericMeanWithinTolerance(t *testing.T) {
	const rowCount = 8000
	const newRows = 20000
	const tolerance = 2.0
	srcData := synthSetNumericPair(t, rowCount, 71, 100.0, 10.0, 5.0, 0.5)

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.SetNumericPairs) != 1 {
		t.Fatalf("expected exactly one captured set-numeric pair, got Conditional=%+v", prof.Conditional)
	}

	spec := synth.SpecFromProfile(prof, newRows)
	if len(spec.SetNumericPairs) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one set-numeric pair, got %d", len(spec.SetNumericPairs))
	}

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse", pulse.SynthOptions{
		Seed: 17, SourceCohort: "/source.pulse",
	}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	augmented, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	_, labels, _ := readSetFieldRows(t, augmented, "tier")
	spend := readF64Field(t, augmented, "spend")
	if len(labels) != rowCount+newRows {
		t.Fatalf("augmented row count = %d, want %d", len(labels), rowCount+newRows)
	}
	genLabels, genSpend := labels[rowCount:], spend[rowCount:]

	var sumSel, sumNotSel float64
	var nSel, nNotSel int
	for i, sel := range genLabels {
		if hasLabel(sel, "premium") {
			sumSel += genSpend[i]
			nSel++
		} else {
			sumNotSel += genSpend[i]
			nNotSel++
		}
	}
	if nSel == 0 || nNotSel == 0 {
		t.Fatalf("expected generated rows in both buckets, got selected=%d not_selected=%d", nSel, nNotSel)
	}
	meanSel := sumSel / float64(nSel)
	meanNotSel := sumNotSel / float64(nNotSel)
	if math.Abs(meanSel-100.0) > tolerance {
		t.Errorf("generated mean(spend | premium selected) = %.4f, want within %.2f of source's 100.0", meanSel, tolerance)
	}
	if math.Abs(meanNotSel-10.0) > tolerance {
		t.Errorf("generated mean(spend | premium not_selected) = %.4f, want within %.2f of source's 10.0", meanNotSel, tolerance)
	}
}

// synthSetSetPair writes a cohort with two set_u8 fields "setA" (option
// "x") and "setB" (option "y") where x and y co-occur (both selected or
// both unselected) on every row except one in flipEvery — a genuine
// cross-field option association for the generation-side round-trip
// test.
func synthSetSetPair(t *testing.T, rowCount, flipEvery int) []byte {
	t.Helper()
	dictA := encoding.NewDictionary()
	if _, err := dictA.Add("x"); err != nil {
		t.Fatalf("dictA.Add: %v", err)
	}
	dictB := encoding.NewDictionary()
	if _, err := dictB.Add("y"); err != nil {
		t.Fatalf("dictB.Add: %v", err)
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "setA", Type: encoding.FieldTypeSetU8, ByteOffset: 0, Dictionary: dictA},
			{Name: "setB", Type: encoding.FieldTypeSetU8, ByteOffset: 1, Dictionary: dictB},
		},
	}
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

// TestAugmentFromProfile_ReconstructsSetSetAssociationWithinTolerance is
// E5-S3's acceptance bar for the set-set pair kind: the generated
// partition must reproduce the source's captured cross-field option
// co-selection rate between two DIFFERENT set_* fields.
func TestAugmentFromProfile_ReconstructsSetSetAssociationWithinTolerance(t *testing.T) {
	const rowCount = 8000
	const newRows = 20000
	const tolerance = 0.05
	srcData := synthSetSetPair(t, rowCount, 10) // 90% co-occurrence

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.SetSetPairs) != 1 {
		t.Fatalf("expected exactly one captured set-set pair, got Conditional=%+v", prof.Conditional)
	}

	spec := synth.SpecFromProfile(prof, newRows)
	if len(spec.SetSetPairs) != 1 {
		t.Fatalf("expected SpecFromProfile to populate one set-set pair, got %d", len(spec.SetSetPairs))
	}

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse", pulse.SynthOptions{
		Seed: 19, SourceCohort: "/source.pulse",
	}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	augmented, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	_, labelsA, _ := readSetFieldRows(t, augmented, "setA")
	_, labelsB, _ := readSetFieldRows(t, augmented, "setB")
	if len(labelsA) != rowCount+newRows || len(labelsB) != rowCount+newRows {
		t.Fatalf("augmented row count mismatch: setA=%d setB=%d, want %d", len(labelsA), len(labelsB), rowCount+newRows)
	}
	genA, genB := labelsA[rowCount:], labelsB[rowCount:]

	rate := func(aSelected bool) (float64, int) {
		n, sel := 0, 0
		for i := range genA {
			if hasLabel(genA[i], "x") != aSelected {
				continue
			}
			n++
			if hasLabel(genB[i], "y") {
				sel++
			}
		}
		if n == 0 {
			return 0, 0
		}
		return float64(sel) / float64(n), n
	}

	rateGivenSelected, nSel := rate(true)
	rateGivenNotSelected, nNot := rate(false)
	if nSel == 0 || nNot == 0 {
		t.Fatalf("expected generated rows in both buckets, got selected=%d not_selected=%d", nSel, nNot)
	}
	// Ground truth from synthSetSetPair's own construction: with
	// flipEvery=10, r%10==0 only ever coincides with even r (x
	// selected), so EVERY flip lands on an x-selected row and NONE on
	// an x-not_selected row — P(y selected|x selected) = 0.80 (3200 of
	// 4000 unflipped), P(y selected|x not_selected) = 0.0 exactly (no
	// flip ever reaches an odd r).
	const wantGivenSelected = 0.80
	const wantGivenNotSelected = 0.0
	if math.Abs(rateGivenSelected-wantGivenSelected) > tolerance {
		t.Errorf("generated P(y selected | x selected) = %.4f (n=%d), want within %.2f of source's %.2f",
			rateGivenSelected, nSel, tolerance, wantGivenSelected)
	}
	if math.Abs(rateGivenNotSelected-wantGivenNotSelected) > tolerance {
		t.Errorf("generated P(y selected | x not_selected) = %.4f (n=%d), want within %.2f of source's %.2f",
			rateGivenNotSelected, nNot, tolerance, wantGivenNotSelected)
	}
}

// TestAugmentFromProfile_WithoutSetConditional_GeneratesIndependentMarginals
// is E5-S3's regression test, mirroring the categorical/numeric
// equivalent: a source cohort with a GENUINE set-categorical
// association, profiled WITHOUT --conditional, must generate a
// partition where that association has vanished — independent-marginal
// sampling — because Conditional is nil and SpecFromProfile therefore
// populates no SetCategoricalPairs.
func TestAugmentFromProfile_WithoutSetConditional_GeneratesIndependentMarginals(t *testing.T) {
	featureOpts := []string{"export"}
	regionOpts := []string{"us", "eu"}
	const rowsPerRegion = 3000
	const rowCount = rowsPerRegion * 2
	const newRows = 20000
	const independenceBound = 0.1

	featureSelected := func(row int, region string, featureIdx int) bool {
		if region == "eu" {
			return row%10 != 0 // 90% selected
		}
		return row%10 == 0 // 10% selected
	}
	srcData := buildSetCategoricalCohort(t, featureOpts, regionOpts, featureSelected, rowCount)

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if err := afero.WriteFile(fs, "/source.pulse", srcData, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// No IncludeConditional — the plain default capture.
	prof, err := p.Profile(context.Background(), "/source.pulse", pulse.ProfileOptions{})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	if prof.Conditional != nil {
		t.Fatal("expected Conditional to be nil when --conditional was not requested")
	}

	spec := synth.SpecFromProfile(prof, newRows)
	if len(spec.SetCategoricalPairs) != 0 {
		t.Fatalf("expected no set-categorical pairs reconstructed from a profile with no conditional section, got %d",
			len(spec.SetCategoricalPairs))
	}

	if _, err := p.Synth(context.Background(), spec, "/augmented.pulse", pulse.SynthOptions{
		Seed: 23, SourceCohort: "/source.pulse",
	}); err != nil {
		t.Fatalf("augment synth: %v", err)
	}

	augmented, err := afero.ReadFile(fs, "/augmented.pulse")
	if err != nil {
		t.Fatalf("read augmented: %v", err)
	}
	_, labels, _ := readSetFieldRows(t, augmented, "features")
	region := readCategoricalField(t, augmented, "region")
	genLabels, genRegion := labels[rowCount:], region[rowCount:]

	rate := func(wantRegion string) (float64, int) {
		n, sel := 0, 0
		for i := range genRegion {
			if genRegion[i] != wantRegion {
				continue
			}
			n++
			if hasLabel(genLabels[i], "export") {
				sel++
			}
		}
		if n == 0 {
			return 0, 0
		}
		return float64(sel) / float64(n), n
	}
	euRate, euN := rate("eu")
	usRate, usN := rate("us")
	if euN == 0 || usN == 0 {
		t.Fatalf("expected generated rows for both eu (n=%d) and us (n=%d)", euN, usN)
	}
	if diff := math.Abs(euRate - usRate); diff > independenceBound {
		t.Errorf("generated P(export|eu)=%.4f vs P(export|us)=%.4f differ by %.4f, want within %.2f (source's own gap is ~0.8 — this must have vanished)",
			euRate, usRate, diff, independenceBound)
	}
}
