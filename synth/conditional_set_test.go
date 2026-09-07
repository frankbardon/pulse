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

// buildSetCategoricalCohort emits a minimal valid single-file .pulse
// cohort with one set_u8 field ("features", dictionary = featureOpts,
// in order) and one categorical_u8 field ("region", dictionary =
// regionOpts, in order). Rows are laid out in contiguous per-region
// BLOCKS (rowCount/len(regionOpts) rows per region, remainder folded
// into the last block) rather than round-robin, so featureSelected
// receives localIdx — the row's own index WITHIN its region's block,
// always starting at 0 — letting a caller write a clean "row%N"-style
// selection rule that means the same thing in every region regardless
// of which block starts at global row 0. featureSelected decides, per
// row and per feature bit, whether that bit is set — deterministic
// (not probabilistic), so the resulting per-region per-feature
// selection rate is an EXACT rational, letting the acceptance test
// assert hand-computed ground truth.
func buildSetCategoricalCohort(t *testing.T, featureOpts, regionOpts []string, featureSelected func(localIdx int, region string, featureIdx int) bool, rowCount int) []byte {
	t.Helper()
	featureDict := encoding.NewDictionary()
	for _, v := range featureOpts {
		if _, err := featureDict.Add(v); err != nil {
			t.Fatalf("featureDict.Add(%q): %v", v, err)
		}
	}
	regionDict := encoding.NewDictionary()
	for _, v := range regionOpts {
		if _, err := regionDict.Add(v); err != nil {
			t.Fatalf("regionDict.Add(%q): %v", v, err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "features", Type: encoding.FieldTypeSetU8, ByteOffset: 0, Dictionary: featureDict},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, Dictionary: regionDict},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	blockSize := rowCount / len(regionOpts)
	for r := 0; r < rowCount; r++ {
		regionIdx := r / blockSize
		if regionIdx >= len(regionOpts) {
			regionIdx = len(regionOpts) - 1
		}
		region := regionOpts[regionIdx]
		localIdx := r - regionIdx*blockSize
		var mask byte
		for i := range featureOpts {
			if featureSelected(localIdx, region, i) {
				mask |= 1 << uint(i)
			}
		}
		regionID, _ := regionDict.IDFor(region)
		buf.WriteByte(mask)
		buf.WriteByte(byte(regionID))
	}
	return buf.Bytes()
}

// TestProfile_ConditionalCapturesSetCategoricalAssociation is E5-S2's
// non-negotiable acceptance bar: a "select all that apply" field
// ("features") paired with a categorical field ("region") where a real
// association exists — feature "export" selected far more often in
// "eu" than in "us" — must have that association captured correctly.
// The assertion compares captured contingency counts against the
// fixture's own known ground truth (computed independently in the test
// from the exact same deterministic assignment rule), not an
// approximate/statistical tolerance — a build-failing test on the
// actual numbers, not a smoke test that capture merely completes.
func TestProfile_ConditionalCapturesSetCategoricalAssociation(t *testing.T) {
	featureOpts := []string{"darkmode", "export", "api", "sso"}
	regionOpts := []string{"us", "eu"}
	const rowsPerRegion = 500
	const rowCount = rowsPerRegion * 2

	// "export" (index 1) selected in 90% of "eu" rows, 10% of "us" rows
	// — a strong, unmistakable real association. Every other feature is
	// selected at a flat 50% regardless of region (no association),
	// exercising that the pair-capture mechanism does not manufacture a
	// spurious association where none exists.
	featureSelected := func(row int, region string, featureIdx int) bool {
		switch featureIdx {
		case 1: // export
			if region == "eu" {
				return row%10 != 0 // 90% selected
			}
			return row%10 == 0 // 10% selected
		default:
			return row%2 == 0 // flat 50%, no association with region
		}
	}
	data := buildSetCategoricalCohort(t, featureOpts, regionOpts, featureSelected, rowCount)

	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if prof.Conditional == nil {
		t.Fatal("expected Conditional section to be populated")
	}
	if len(prof.Conditional.SetCategoricalPairs) != len(featureOpts) {
		t.Fatalf("expected one SetCategoricalPairs entry per feature option (%d), got %d: %+v",
			len(featureOpts), len(prof.Conditional.SetCategoricalPairs), prof.Conditional.SetCategoricalPairs)
	}

	var exportPair *synth.SetCategoricalPairProfile
	for i := range prof.Conditional.SetCategoricalPairs {
		p := &prof.Conditional.SetCategoricalPairs[i]
		if p.Set != "features" || p.Categorical != "region" {
			t.Fatalf("unexpected pair fields: %+v", p)
		}
		if p.Option == "export" {
			exportPair = p
		}
	}
	if exportPair == nil {
		t.Fatalf("expected an \"export\" option entry, got %+v", prof.Conditional.SetCategoricalPairs)
	}
	if exportPair.N != rowCount {
		t.Errorf("export pair N = %d, want %d (no nulls in this fixture)", exportPair.N, rowCount)
	}

	cellCount := func(cells []synth.ContingencyCell, aVal, bVal string) int {
		for _, c := range cells {
			if c.AValue == aVal && c.BValue == bVal {
				return c.Count
			}
		}
		return 0
	}
	// Ground truth computed directly from the fixture's own deterministic
	// assignment rule: eu selected = 450 (90% of 500), eu not_selected = 50;
	// us selected = 50 (10% of 500), us not_selected = 450.
	wantEuSelected := 450
	wantEuNotSelected := 50
	wantUsSelected := 50
	wantUsNotSelected := 450

	if got := cellCount(exportPair.Cells, "selected", "eu"); got != wantEuSelected {
		t.Errorf("export selected x eu = %d, want %d", got, wantEuSelected)
	}
	if got := cellCount(exportPair.Cells, "not_selected", "eu"); got != wantEuNotSelected {
		t.Errorf("export not_selected x eu = %d, want %d", got, wantEuNotSelected)
	}
	if got := cellCount(exportPair.Cells, "selected", "us"); got != wantUsSelected {
		t.Errorf("export selected x us = %d, want %d", got, wantUsSelected)
	}
	if got := cellCount(exportPair.Cells, "not_selected", "us"); got != wantUsNotSelected {
		t.Errorf("export not_selected x us = %d, want %d", got, wantUsNotSelected)
	}

	// A feature with NO real association (flat 50% regardless of
	// region) must show a roughly even split across regions — asserted
	// exactly here since the assignment rule is deterministic.
	var darkmodePair *synth.SetCategoricalPairProfile
	for i := range prof.Conditional.SetCategoricalPairs {
		if prof.Conditional.SetCategoricalPairs[i].Option == "darkmode" {
			darkmodePair = &prof.Conditional.SetCategoricalPairs[i]
		}
	}
	if darkmodePair == nil {
		t.Fatal("expected a \"darkmode\" option entry")
	}
	if got := cellCount(darkmodePair.Cells, "selected", "eu"); got != 250 {
		t.Errorf("darkmode selected x eu = %d, want 250 (no association)", got)
	}
	if got := cellCount(darkmodePair.Cells, "selected", "us"); got != 250 {
		t.Errorf("darkmode selected x us = %d, want 250 (no association)", got)
	}
}

// TestProfile_WithoutConditional_OmitsSetCategoricalPairs is the
// additive regression check mirroring E2-S1/E3-S1/E3-S2's own
// regression tests: no "set_categorical_pairs" key at all unless
// --conditional was requested.
func TestProfile_WithoutConditional_OmitsSetCategoricalPairs(t *testing.T) {
	featureOpts := []string{"a", "b"}
	regionOpts := []string{"us", "eu"}
	data := buildSetCategoricalCohort(t, featureOpts, regionOpts,
		func(row int, region string, featureIdx int) bool { return row%2 == 0 }, 200)

	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if prof.Conditional != nil {
		t.Fatal("expected Conditional to be nil when --conditional was not requested")
	}
}

// TestProfile_SetCategoricalRespectsTopKAndCellCap asserts the existing
// per-field categorical top-K cap AND the joint-cell ContingencyCellCap
// (E3-S1) both compose with set x categorical capture: a set field
// paired with a very high-cardinality categorical field must not
// explode the profile document — every retained cell's category value
// is drawn from {top-K values, "other"}, and the option's own cell
// count never exceeds ContingencyCellCap.
func TestProfile_SetCategoricalRespectsTopKAndCellCap(t *testing.T) {
	featureOpts := []string{"only"}
	const numRegions = 300 // far above the default top-K (32)
	regionOpts := make([]string, numRegions)
	for i := range regionOpts {
		regionOpts[i] = fmt.Sprintf("region_%03d", i)
	}
	const rowsPerRegion = 5
	data := buildSetCategoricalCohort(t, featureOpts, regionOpts,
		func(row int, region string, featureIdx int) bool { return row%2 == 0 },
		numRegions*rowsPerRegion)

	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.SetCategoricalPairs) != 1 {
		t.Fatalf("expected exactly one SetCategoricalPairs entry, got Conditional=%+v", prof.Conditional)
	}
	pair := prof.Conditional.SetCategoricalPairs[0]
	if len(pair.Cells) > synth.ContingencyCellCap {
		t.Fatalf("cell count %d exceeds ContingencyCellCap %d — cap did not compose with set capture",
			len(pair.Cells), synth.ContingencyCellCap)
	}

	var fieldRegion *synth.FieldProfile
	for i := range prof.Fields {
		if prof.Fields[i].Name == "region" {
			fieldRegion = &prof.Fields[i]
		}
	}
	if fieldRegion == nil || fieldRegion.Categorical == nil {
		t.Fatal("expected the region field profile to be populated")
	}
	allowed := map[string]bool{"other": true}
	for _, hit := range fieldRegion.Categorical.Top {
		allowed[hit.Value] = true
	}
	for _, c := range pair.Cells {
		if !allowed[c.BValue] {
			t.Errorf("cell category %q is not in the region field's own top-K set (or \"other\")", c.BValue)
		}
	}
}

// TestProfile_SetCategoricalThinCellWarning asserts a thin set x
// categorical cell (co-occurrence count below synth.MinPairObservations)
// emits the same warning shape every other pair kind already uses — no
// separate, divergent warning mechanism for this fourth pair kind.
func TestProfile_SetCategoricalThinCellWarning(t *testing.T) {
	featureOpts := []string{"rare_feature"}
	regionOpts := []string{"common", "rare"}
	const rowCount = 500
	// "rare_feature" selected only for a handful of "rare"-region rows,
	// guaranteeing that cell's co-occurrence count falls well under
	// MinPairObservations (30).
	data := buildSetCategoricalCohort(t, featureOpts, regionOpts,
		func(row int, region string, featureIdx int) bool {
			return region == "rare" && row%100 == 0
		},
		rowCount)
	// region assignment: round-robin over 2 values means half are
	// "rare" (250 rows); of those, selected only when row%100==0.

	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.SetCategoricalPairs) != 1 {
		t.Fatalf("expected exactly one SetCategoricalPairs entry, got Conditional=%+v", prof.Conditional)
	}
	pair := prof.Conditional.SetCategoricalPairs[0]

	var thinFound bool
	for _, c := range pair.Cells {
		if c.Count < synth.MinPairObservations {
			thinFound = true
		}
	}
	if !thinFound {
		t.Fatalf("expected at least one thin cell (< %d observations), got cells=%+v",
			synth.MinPairObservations, pair.Cells)
	}

	found := false
	for _, w := range prof.Warnings {
		if strings.Contains(w, "thin") && strings.Contains(w, "set-categorical") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a thin set-categorical warning matching the shared warning shape, got warnings=%v", prof.Warnings)
	}
}

// TestProfile_ConditionalCapturesSetNumericAssociation asserts set x
// numeric capture: a numeric field's mean genuinely differs between a
// set option's selected/not_selected rows, and that difference must be
// captured exactly against the fixture's own ground truth.
func TestProfile_ConditionalCapturesSetNumericAssociation(t *testing.T) {
	featureDict := encoding.NewDictionary()
	for _, v := range []string{"premium"} {
		if _, err := featureDict.Add(v); err != nil {
			t.Fatalf("featureDict.Add: %v", err)
		}
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
	const rowCount = 1000
	var sumSelected, sumNotSelected float64
	var nSelected, nNotSelected int
	for r := 0; r < rowCount; r++ {
		selected := r%2 == 0
		var spend float64
		if selected {
			spend = 100.0 + float64(r%5) // clusters around 100-104
			sumSelected += spend
			nSelected++
		} else {
			spend = 10.0 + float64(r%5) // clusters around 10-14
			sumNotSelected += spend
			nNotSelected++
		}
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

	prof, err := synth.ProfileBytes(buf.Bytes(), synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.SetNumericPairs) != 1 {
		t.Fatalf("expected exactly one SetNumericPairs entry, got Conditional=%+v", prof.Conditional)
	}
	pair := prof.Conditional.SetNumericPairs[0]
	if pair.Set != "tier" || pair.Option != "premium" || pair.Numeric != "spend" {
		t.Fatalf("unexpected pair: %+v", pair)
	}
	if pair.N != rowCount {
		t.Errorf("pair N = %d, want %d", pair.N, rowCount)
	}

	wantSelectedMean := sumSelected / float64(nSelected)
	wantNotSelectedMean := sumNotSelected / float64(nNotSelected)
	var gotSelected, gotNotSelected *synth.CategoricalNumericCategoryStat
	for i := range pair.Categories {
		switch pair.Categories[i].Category {
		case "selected":
			gotSelected = &pair.Categories[i]
		case "not_selected":
			gotNotSelected = &pair.Categories[i]
		}
	}
	if gotSelected == nil || gotNotSelected == nil {
		t.Fatalf("expected both selected and not_selected categories, got %+v", pair.Categories)
	}
	const tol = 1e-9
	if math.Abs(gotSelected.Mean-wantSelectedMean) > tol {
		t.Errorf("selected mean = %.8f, want %.8f", gotSelected.Mean, wantSelectedMean)
	}
	if math.Abs(gotNotSelected.Mean-wantNotSelectedMean) > tol {
		t.Errorf("not_selected mean = %.8f, want %.8f", gotNotSelected.Mean, wantNotSelectedMean)
	}
	if gotSelected.N != nSelected || gotNotSelected.N != nNotSelected {
		t.Errorf("category N = selected:%d not_selected:%d, want selected:%d not_selected:%d",
			gotSelected.N, gotNotSelected.N, nSelected, nNotSelected)
	}
}

// TestProfile_ConditionalCapturesSetSetAssociation asserts set x set
// capture between two DIFFERENT set_* fields: a real co-selection
// association between one option of each field must be captured
// exactly against the fixture's own ground truth, and no pair is ever
// captured between two options of the SAME field.
func TestProfile_ConditionalCapturesSetSetAssociation(t *testing.T) {
	dictA := encoding.NewDictionary()
	for _, v := range []string{"x"} {
		if _, err := dictA.Add(v); err != nil {
			t.Fatalf("dictA.Add: %v", err)
		}
	}
	dictB := encoding.NewDictionary()
	for _, v := range []string{"y"} {
		if _, err := dictB.Add(v); err != nil {
			t.Fatalf("dictB.Add: %v", err)
		}
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
	const rowCount = 1000
	// x and y co-occur (both selected or both unselected) on 90% of
	// rows -- a strong positive association.
	wantBothSelected, wantBothNot, wantXOnly, wantYOnly := 0, 0, 0, 0
	for r := 0; r < rowCount; r++ {
		xSel := r%2 == 0
		ySel := xSel
		if r%10 == 0 {
			ySel = !ySel // flip 10% of the time to break perfect correlation
		}
		var maskA, maskB byte
		if xSel {
			maskA = 1
		}
		if ySel {
			maskB = 1
		}
		switch {
		case xSel && ySel:
			wantBothSelected++
		case !xSel && !ySel:
			wantBothNot++
		case xSel && !ySel:
			wantXOnly++
		default:
			wantYOnly++
		}
		buf.WriteByte(maskA)
		buf.WriteByte(maskB)
	}

	prof, err := synth.ProfileBytes(buf.Bytes(), synth.ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.SetSetPairs) != 1 {
		t.Fatalf("expected exactly one SetSetPairs entry, got Conditional=%+v", prof.Conditional)
	}
	pair := prof.Conditional.SetSetPairs[0]
	if pair.SetA != "setA" || pair.OptionA != "x" || pair.SetB != "setB" || pair.OptionB != "y" {
		t.Fatalf("unexpected pair: %+v", pair)
	}
	if pair.N != rowCount {
		t.Errorf("pair N = %d, want %d", pair.N, rowCount)
	}
	cellCount := func(aVal, bVal string) int {
		for _, c := range pair.Cells {
			if c.AValue == aVal && c.BValue == bVal {
				return c.Count
			}
		}
		return 0
	}
	if got := cellCount("selected", "selected"); got != wantBothSelected {
		t.Errorf("selected x selected = %d, want %d", got, wantBothSelected)
	}
	if got := cellCount("not_selected", "not_selected"); got != wantBothNot {
		t.Errorf("not_selected x not_selected = %d, want %d", got, wantBothNot)
	}
	if got := cellCount("selected", "not_selected"); got != wantXOnly {
		t.Errorf("selected x not_selected = %d, want %d", got, wantXOnly)
	}
	if got := cellCount("not_selected", "selected"); got != wantYOnly {
		t.Errorf("not_selected x selected = %d, want %d", got, wantYOnly)
	}
}

// TestProfile_WithoutConditional_OmitsSetNumericAndSetSetPairs mirrors
// the additive regression checks every other pair kind already has.
func TestProfile_WithoutConditional_OmitsSetNumericAndSetSetPairs(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, v := range []string{"a"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeF64, ByteOffset: 0},
			{Name: "flags", Type: encoding.FieldTypeSetU8, ByteOffset: 8, Dictionary: dict},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for r := 0; r < 100; r++ {
		var rec [9]byte
		buf.Write(rec[:])
	}
	prof, err := synth.ProfileBytes(buf.Bytes(), synth.ProfileOptions{})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if prof.Conditional != nil {
		t.Fatal("expected Conditional to be nil when --conditional was not requested")
	}
}
