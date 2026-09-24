package synth_test

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
)

// E5-S2 test pack: profile capture, SpecFromProfile translation and the
// fidelity report over a WIDE set column (set_u128 / set_u256).
//
// Every assertion here exists because the failure it guards is SILENT.
// A wide set arrives in the record reader's wide map as an
// encoding.SetMask, not a uint64, so a site that type-asserts
// `.(uint64)` gets the zero value and reads the row as "selected
// nothing" — no error, a plausible-looking profile, and every
// per-element marginal above bit 63 quietly equal to 0.0. A fidelity
// report built on top of that compares zero against zero and reports a
// delta of 0, which reads as "nothing wrong".

// buildWideSetFieldCohort emits a minimal valid single-file .pulse
// cohort with one wide set field named "features" whose dictionary is
// exactly options, in order. Row r selects option i iff r < counts[i] —
// the same deterministic (not probabilistic) assignment
// buildSetFieldCohort uses for the narrow rungs, so the resulting
// per-option selection frequency is an EXACT rational and the test can
// assert hand-computed ground truth rather than a tolerance.
func buildWideSetFieldCohort(t *testing.T, ft encoding.FieldType, options []string, counts []int, rowCount int) []byte {
	t.Helper()
	if len(options) != len(counts) {
		t.Fatalf("options/counts length mismatch: %d vs %d", len(options), len(counts))
	}
	if !ft.IsWideSet() {
		t.Fatalf("buildWideSetFieldCohort needs a wide rung, got %s", ft)
	}
	dict := encoding.NewDictionary()
	for _, v := range options {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add(%q): %v", v, err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "features", Type: ft, ByteOffset: 0, Dictionary: dict},
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
		var mask encoding.SetMask
		for i, c := range counts {
			if r < c {
				mask = mask.WithBit(i)
			}
		}
		if err := encoding.WriteSetMask(&buf, ft, mask); err != nil {
			t.Fatalf("WriteSetMask(row %d): %v", r, err)
		}
	}
	return buf.Bytes()
}

// wideSetMemberCounts is the 206-member fixture's ground truth: a
// deterministic, uneven per-option selection count that never lands on 0
// or on rowCount, so every member carries a real rate both below and
// above bit 64 and a dropped high word cannot be mistaken for a
// legitimately unselected option.
func wideSetMemberCounts(members, rowCount int) []int {
	out := make([]int, members)
	for i := range out {
		out[i] = ((i*13)%90 + 5) * (rowCount / 100)
	}
	return out
}

// TestProfile_WideSetMarginalFrequencies is the story's central capture
// bar: per-element marginals across ALL members, not the first 64. The
// fixture is 206 members on a set_u256, which is the motivating SPSS
// multiple-response width, and every member's frequency is an exact
// rational the test computes independently.
func TestProfile_WideSetMarginalFrequencies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ft      encoding.FieldType
		members int
	}{
		{"set_u128", encoding.FieldTypeSetU128, 128},
		{"set_u256", encoding.FieldTypeSetU256, 206},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const rowCount = 1000
			options := wideSetOptionNames(tc.members)
			counts := wideSetMemberCounts(tc.members, rowCount)
			data := buildWideSetFieldCohort(t, tc.ft, options, counts, rowCount)

			prof, err := synth.ProfileBytes(data, synth.ProfileOptions{})
			if err != nil {
				t.Fatalf("ProfileBytes: %v", err)
			}
			if len(prof.Fields) != 1 {
				t.Fatalf("len(Fields) = %d, want 1", len(prof.Fields))
			}
			fp := prof.Fields[0]
			if fp.Set == nil {
				t.Fatalf("field %q: Set profile is nil (type %s)", fp.Name, fp.Type)
			}
			if fp.Type != tc.name {
				t.Errorf("Type = %q, want %q", fp.Type, tc.name)
			}
			if fp.Set.N != rowCount {
				t.Errorf("Set.N = %d, want %d", fp.Set.N, rowCount)
			}
			if len(fp.Set.Options) != tc.members {
				t.Fatalf("len(Set.Options) = %d, want %d — a wide dictionary must "+
					"carry one marginal per member, not the first 64", len(fp.Set.Options), tc.members)
			}
			for i, o := range fp.Set.Options {
				if o.Value != options[i] {
					t.Fatalf("Options[%d].Value = %q, want %q (bit order is the contract)",
						i, o.Value, options[i])
				}
				if o.Count != counts[i] {
					t.Errorf("Options[%d] (%q, bit %d): Count = %d, want %d",
						i, o.Value, i, o.Count, counts[i])
				}
				want := float64(counts[i]) / float64(rowCount)
				if math.Abs(o.Frequency-want) > 1e-12 {
					t.Errorf("Options[%d] (%q, bit %d): Frequency = %v, want %v",
						i, o.Value, i, o.Frequency, want)
				}
			}
			// Stated separately so a regression that zeroes only the
			// high words fails with a message that names the cause.
			for i := 64; i < tc.members; i++ {
				if fp.Set.Options[i].Count == 0 {
					t.Fatalf("option %q (bit %d) captured 0 selections against a fixture "+
						"count of %d — the high words are being read as an empty mask",
						options[i], i, counts[i])
				}
			}
		})
	}
}

// TestSpecFromProfile_WideSetRoundTrip closes the synth loop the story
// is named for: profile a 206-member cohort, translate it, generate
// from the translation, and re-profile the generated cohort. The
// per-element marginals must match the source's within tolerance BELOW
// and ABOVE bit 64.
func TestSpecFromProfile_WideSetRoundTrip(t *testing.T) {
	const members = 206
	const sourceRows = 1000
	const newRows = 20000
	// Bernoulli standard error at 20,000 rows is ~0.0035; 0.02 is a
	// band a genuine reconstruction clears with room while a dropped
	// high word (which reads 0.00 against a source rate of 0.05-0.94)
	// cannot.
	const tolerance = 0.02

	options := wideSetOptionNames(members)
	counts := wideSetMemberCounts(members, sourceRows)
	srcData := buildWideSetFieldCohort(t, encoding.FieldTypeSetU256, options, counts, sourceRows)

	srcProf, err := synth.ProfileBytes(srcData, synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("source ProfileBytes: %v", err)
	}

	spec, warnings := synth.SpecFromProfile(srcProf, newRows)
	if len(spec.Fields) != 1 {
		t.Fatalf("len(spec.Fields) = %d, want 1 (warnings: %v)", len(spec.Fields), warnings)
	}
	fs := spec.Fields[0]
	if fs.Type != "set_u256" {
		t.Errorf("spec field Type = %q, want set_u256", fs.Type)
	}
	if fs.Distribution != synth.DistSetBernoulli {
		t.Errorf("spec field Distribution = %q, want %q", fs.Distribution, synth.DistSetBernoulli)
	}
	opts, ok := fs.Params["options"].([]any)
	if !ok || len(opts) != members {
		t.Fatalf("spec params options = %T len %d, want %d entries — a wide "+
			"translation must carry every member", fs.Params["options"], len(opts), members)
	}

	genData, _, err := synth.SynthBytes(spec, synth.Options{Seed: 19})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	genProf, err := synth.ProfileBytes(genData, synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("generated ProfileBytes: %v", err)
	}
	if len(genProf.Fields) != 1 || genProf.Fields[0].Set == nil {
		t.Fatalf("re-profile produced no Set block: %+v", genProf.Fields)
	}
	got := genProf.Fields[0].Set
	if len(got.Options) != members {
		t.Fatalf("re-profiled len(Options) = %d, want %d", len(got.Options), members)
	}
	var worstLow, worstHigh float64
	for i, o := range got.Options {
		want := float64(counts[i]) / float64(sourceRows)
		d := math.Abs(o.Frequency - want)
		if i < 64 {
			worstLow = math.Max(worstLow, d)
		} else {
			worstHigh = math.Max(worstHigh, d)
		}
		if d > tolerance {
			t.Errorf("member %q (bit %d): round-tripped frequency %.4f, source %.4f, "+
				"gap %.4f exceeds %.2f", o.Value, i, o.Frequency, want, d, tolerance)
		}
	}
	// The two halves are reported together because a round trip that
	// holds below bit 64 and collapses above it is the exact failure
	// shape this story exists to remove, and a single worst-case number
	// hides it.
	t.Logf("worst per-member gap: bits 0-63 %.4f, bits 64-%d %.4f", worstLow, members-1, worstHigh)
	if worstHigh == 0 {
		t.Errorf("every member above bit 63 round-tripped to an exactly identical "+
			"frequency (%d members) — that is not a reconstruction, it is a constant", members-64)
	}
}

// wideSetPairGroundTruth is buildWideSetPairCohort's hand-computed
// answer key.
type wideSetPairGroundTruth struct {
	rows int
	// bDriverRows is the number of rows on which the `b` driver is true:
	// it selects features bit 2 (LOW) and bit 64 (HIGH) identically, so
	// the two options' captures must be equal cell for cell.
	bDriverRows int
	// aDriverRows is the number of rows on which the `a` driver is true:
	// it selects features bit 65 (HIGH) and sets spend high.
	aDriverRows  int
	spendHigh    float64
	spendLow     float64
	lowBitOption string
	hiBitOption  string
	spendOption  string
}

// buildWideSetPairCohort emits a cohort exercising all three
// set-involving conditional pair kinds against a WIDE set field:
// set x categorical, set x numeric and set x set.
//
// With mirrorLowBit, two of `features`' members — one below bit 64 and
// one above it — are driven by the SAME underlying condition, so a
// correct capture produces identical contingency tables for them and a
// capture that drops the high words produces a degenerate one for the
// high member only. That mirroring makes the two design columns exactly
// collinear, which a least-squares fit legitimately refuses, so the
// model-capture test asks for the same cohort WITHOUT it.
func buildWideSetPairCohort(t *testing.T, rowCount int, mirrorLowBit bool) ([]byte, wideSetPairGroundTruth) {
	t.Helper()
	const members = 66
	featureDict := encoding.NewDictionary()
	for _, v := range wideSetOptionNames(members) {
		if _, err := featureDict.Add(v); err != nil {
			t.Fatalf("featureDict.Add: %v", err)
		}
	}
	channelDict := encoding.NewDictionary()
	for _, v := range []string{"c0", "c1"} {
		if _, err := channelDict.Add(v); err != nil {
			t.Fatalf("channelDict.Add: %v", err)
		}
	}
	regionDict := encoding.NewDictionary()
	for _, v := range []string{"north", "south"} {
		if _, err := regionDict.Add(v); err != nil {
			t.Fatalf("regionDict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "features", Type: encoding.FieldTypeSetU128, ByteOffset: 0, Dictionary: featureDict},
			{Name: "channels", Type: encoding.FieldTypeSetU8, ByteOffset: 16, Dictionary: channelDict},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 17, Dictionary: regionDict},
			{Name: "spend", Type: encoding.FieldTypeF64, ByteOffset: 18},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	gt := wideSetPairGroundTruth{
		rows:         rowCount,
		spendHigh:    100,
		spendLow:     50,
		lowBitOption: fmt.Sprintf("opt%03d", 2),
		hiBitOption:  fmt.Sprintf("opt%03d", 64),
		spendOption:  fmt.Sprintf("opt%03d", 65),
	}
	for r := 0; r < rowCount; r++ {
		a := r%4 != 0 // spend driver, features bit 65
		b := r%3 == 0 // region + channels driver, features bit 64 (and bit 2 when mirrored)
		n := r%7 < 3  // an uncorrelated low-bit control, features bit 3
		lowMirror := b
		if !mirrorLowBit {
			lowMirror = r%11 < 4
		}
		if a {
			gt.aDriverRows++
		}
		if b {
			gt.bDriverRows++
		}
		var features encoding.SetMask
		features = features.WithBit(0) // always selected
		if lowMirror {
			features = features.WithBit(2)
		}
		if b {
			features = features.WithBit(64)
		}
		if n {
			features = features.WithBit(3)
		}
		if a {
			features = features.WithBit(65)
		}
		if err := encoding.WriteSetMask(&buf, encoding.FieldTypeSetU128, features); err != nil {
			t.Fatalf("WriteSetMask(row %d): %v", r, err)
		}
		var channels byte
		if b {
			channels |= 1 << 0
		}
		if r%5 == 0 {
			channels |= 1 << 1
		}
		buf.WriteByte(channels)
		region := byte(1) // south
		if b {
			region = 0 // north
		}
		buf.WriteByte(region)
		spend := gt.spendLow
		if a {
			spend = gt.spendHigh
		}
		bits := math.Float64bits(spend)
		var f [8]byte
		for i := 0; i < 8; i++ {
			f[i] = byte(bits >> (8 * i))
		}
		buf.Write(f[:])
	}
	return buf.Bytes(), gt
}

func findSetCategoricalPair(pairs []synth.SetCategoricalPairProfile, set, option, cat string) *synth.SetCategoricalPairProfile {
	for i := range pairs {
		if pairs[i].Set == set && pairs[i].Option == option && pairs[i].Categorical == cat {
			return &pairs[i]
		}
	}
	return nil
}

func findSetNumericPair(pairs []synth.SetNumericPairProfile, set, option, num string) *synth.SetNumericPairProfile {
	for i := range pairs {
		if pairs[i].Set == set && pairs[i].Option == option && pairs[i].Numeric == num {
			return &pairs[i]
		}
	}
	return nil
}

func findSetSetPair(pairs []synth.SetSetPairProfile, setA, optA, setB, optB string) *synth.SetSetPairProfile {
	for i := range pairs {
		if pairs[i].SetA == setA && pairs[i].OptionA == optA &&
			pairs[i].SetB == setB && pairs[i].OptionB == optB {
			return &pairs[i]
		}
	}
	return nil
}

func cellCount(cells []synth.ContingencyCell, a, b string) int {
	for _, c := range cells {
		if c.AValue == a && c.BValue == b {
			return c.Count
		}
	}
	return 0
}

// TestProfile_WideSetConditionalPairsMatchNarrowBehaviour asserts the
// story's second criterion: conditional pair capture involving a
// wide-set column behaves as it does for narrow sets. The fixture drives
// one member BELOW bit 64 and one ABOVE it from the same condition, so
// "behaves the same" is asserted as an equality between the two
// captures rather than as a tolerance.
func TestProfile_WideSetConditionalPairsMatchNarrowBehaviour(t *testing.T) {
	const rowCount = 2400
	data, gt := buildWideSetPairCohort(t, rowCount, true)

	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if prof.Conditional == nil {
		t.Fatalf("no Conditional section")
	}

	// --- set x categorical ------------------------------------------
	lowCat := findSetCategoricalPair(prof.Conditional.SetCategoricalPairs, "features", gt.lowBitOption, "region")
	hiCat := findSetCategoricalPair(prof.Conditional.SetCategoricalPairs, "features", gt.hiBitOption, "region")
	if lowCat == nil || hiCat == nil {
		t.Fatalf("missing set-categorical pair: low=%v high=%v", lowCat, hiCat)
	}
	if got := cellCount(hiCat.Cells, "selected", "north"); got != gt.bDriverRows {
		t.Errorf("features[%s] x region {selected,north} = %d, want %d — a high-bit "+
			"member read as never selected produces exactly 0 here",
			gt.hiBitOption, got, gt.bDriverRows)
	}
	if got := cellCount(hiCat.Cells, "not_selected", "south"); got != rowCount-gt.bDriverRows {
		t.Errorf("features[%s] x region {not_selected,south} = %d, want %d",
			gt.hiBitOption, got, rowCount-gt.bDriverRows)
	}
	for _, cell := range [][2]string{{"selected", "north"}, {"selected", "south"},
		{"not_selected", "north"}, {"not_selected", "south"}} {
		lo := cellCount(lowCat.Cells, cell[0], cell[1])
		hi := cellCount(hiCat.Cells, cell[0], cell[1])
		if lo != hi {
			t.Errorf("{%s,%s}: low-bit member %q = %d but high-bit member %q = %d — "+
				"both are driven by the same condition and must capture identically",
				cell[0], cell[1], gt.lowBitOption, lo, gt.hiBitOption, hi)
		}
	}

	// --- set x numeric ----------------------------------------------
	hiNum := findSetNumericPair(prof.Conditional.SetNumericPairs, "features", gt.spendOption, "spend")
	if hiNum == nil {
		t.Fatalf("missing set-numeric pair for features[%s] x spend", gt.spendOption)
	}
	if len(hiNum.Categories) != 2 {
		t.Fatalf("features[%s] x spend has %d bucket(s), want both selected and "+
			"not_selected — a member read as never selected produces one",
			gt.spendOption, len(hiNum.Categories))
	}
	for _, c := range hiNum.Categories {
		switch c.Category {
		case "selected":
			if c.N != gt.aDriverRows {
				t.Errorf("selected bucket N = %d, want %d", c.N, gt.aDriverRows)
			}
			if math.Abs(c.Mean-gt.spendHigh) > 1e-9 {
				t.Errorf("selected bucket Mean = %v, want %v", c.Mean, gt.spendHigh)
			}
		case "not_selected":
			if c.N != rowCount-gt.aDriverRows {
				t.Errorf("not_selected bucket N = %d, want %d", c.N, rowCount-gt.aDriverRows)
			}
			if math.Abs(c.Mean-gt.spendLow) > 1e-9 {
				t.Errorf("not_selected bucket Mean = %v, want %v", c.Mean, gt.spendLow)
			}
		default:
			t.Errorf("unexpected bucket %q", c.Category)
		}
	}

	// --- set x set ---------------------------------------------------
	lowSet := findSetSetPair(prof.Conditional.SetSetPairs, "features", gt.lowBitOption, "channels", "c0")
	hiSet := findSetSetPair(prof.Conditional.SetSetPairs, "features", gt.hiBitOption, "channels", "c0")
	if lowSet == nil || hiSet == nil {
		t.Fatalf("missing set-set pair: low=%v high=%v", lowSet, hiSet)
	}
	if got := cellCount(hiSet.Cells, "selected", "selected"); got != gt.bDriverRows {
		t.Errorf("features[%s] x channels[c0] {selected,selected} = %d, want %d",
			gt.hiBitOption, got, gt.bDriverRows)
	}
	for _, cell := range [][2]string{{"selected", "selected"}, {"selected", "not_selected"},
		{"not_selected", "selected"}, {"not_selected", "not_selected"}} {
		lo := cellCount(lowSet.Cells, cell[0], cell[1])
		hi := cellCount(hiSet.Cells, cell[0], cell[1])
		if lo != hi {
			t.Errorf("{%s,%s}: low-bit member = %d but high-bit member = %d",
				cell[0], cell[1], lo, hi)
		}
	}
}

// TestProfileModels_WideSetPredictorIsAdmitted covers the model-capture
// arm. A wide set member never decoded into the retained snapshot
// leaves every row MISSING for that field, which drops the candidate
// silently: the capture reports a target with no predictors, which is a
// legitimate outcome elsewhere and therefore carries no signal at all.
func TestProfileModels_WideSetPredictorIsAdmitted(t *testing.T) {
	const rowCount = 2400
	data, gt := buildWideSetPairCohort(t, rowCount, false)

	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{FitModels: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	var spend *synth.FieldModel
	for i := range prof.Models {
		if prof.Models[i].Field == "spend" {
			spend = &prof.Models[i]
		}
	}
	if spend == nil {
		t.Fatalf("no model captured for spend (models: %+v, warnings: %v)", prof.Models, prof.Warnings)
	}
	var hit *synth.ModelPredictor
	for i := range spend.Predictors {
		p := &spend.Predictors[i]
		if p.Field == "features" && p.Level == gt.spendOption {
			hit = p
		}
	}
	if hit == nil {
		t.Fatalf("spend's model carries no features[%s] predictor — the wide set "+
			"candidate was dropped. Predictors: %+v", gt.spendOption, spend.Predictors)
	}
	// The fixture makes spend exactly spendHigh when the member is
	// selected and spendLow when it is not, so the coefficient is the
	// difference and the fit is exact.
	want := gt.spendHigh - gt.spendLow
	if math.Abs(hit.Coefficient-want) > 1.0 {
		t.Errorf("features[%s] coefficient = %v, want ~%v", gt.spendOption, hit.Coefficient, want)
	}
	if spend.R2 < 0.9 {
		t.Errorf("R2 = %v, want >0.9 — the wide member explains spend entirely", spend.R2)
	}
}

// TestProfile_WideSetDictionaryBeyondMaskWidthWarns covers the story's
// degradation criterion. A dictionary wider than its own rung's mask is
// clamped at capture — every option past the cap gets no marginal, no
// pair capture and no fidelity comparison — and until this story that
// clamp was applied in silence, which is indistinguishable in the
// document from a cohort that genuinely never selects those options.
func TestProfile_WideSetDictionaryBeyondMaskWidthWarns(t *testing.T) {
	const rowCount = 100
	// A set_u64 carrying a 70-entry dictionary: six members the mask
	// cannot address. Reachable through a hand-built or corrupt file,
	// and now that the wide rungs exist it is also what a cohort looks
	// like when it was NOT widened.
	options := wideSetOptionNames(70)
	dict := encoding.NewDictionary()
	for _, v := range options {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "features", Type: encoding.FieldTypeSetU64, ByteOffset: 0, Dictionary: dict},
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
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeSetU64, uint64(r%3)); err != nil {
			t.Fatalf("WriteFieldValue: %v", err)
		}
	}

	prof, err := synth.ProfileBytes(buf.Bytes(), synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	var found string
	for _, w := range prof.Warnings {
		if strings.Contains(w, "features") && strings.Contains(w, "mask width") {
			found = w
		}
	}
	if found == "" {
		t.Fatalf("no warning naming the clamped set field; Warnings = %v", prof.Warnings)
	}
	if !strings.Contains(found, "70") || !strings.Contains(found, "64") {
		t.Errorf("warning %q does not name both the dictionary size and the mask width", found)
	}
	// The degradation must reach the terminal summary as something
	// needing ATTENTION: a clamped capture is a requested thing that did
	// not happen.
	groups := synth.GroupWarnings(prof.Warnings)
	var attention bool
	for _, g := range groups {
		for _, m := range g.Members {
			if m == found {
				attention = g.Attention
				if g.Kind == "other" {
					t.Errorf("warning landed in the catch-all group; it needs its own kind")
				}
			}
		}
	}
	if !attention {
		t.Errorf("warning %q is not classified as needing attention", found)
	}
}
