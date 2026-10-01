package synth

import (
	"bytes"
	"encoding/json"
	"math"
	mrand "math/rand/v2"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// residualFixtureSchema is the cohort shape the residual-correlation
// tests fit against: three numeric targets and one categorical
// predictor. Three targets rather than two because the whole subject is
// a SUBMATRIX — with two fields there is exactly one pair and "full
// submatrix" is indistinguishable from "the one pair we happened to
// keep".
func residualFixtureSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	region := encoding.NewDictionary()
	for _, v := range []string{"east", "west"} {
		if _, err := region.Add(v); err != nil {
			t.Fatalf("region dict add: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "alpha", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "beta", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "gamma", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: region},
	}}
}

// residualFixtureRows builds a cohort whose RESIDUALS carry a known
// structure that its RAW values do not:
//
//	alpha = 10 + 5·[region=west] + z1
//	beta  = 20 + 3·[region=west] + 0.8·z1 + 0.6·z2
//	gamma = 30 + z3
//
// alpha and beta share a predictor AND share residual noise; gamma
// shares neither. So residual corr(alpha, beta) ≈ 0.8, while
// corr(alpha, gamma) and corr(beta, gamma) are measured at ≈ 0 — the
// measured-zero case the section exists to keep distinct from a gap.
//
// The RNG is seeded and consumed in a fixed order, so the fixture is a
// constant, not a sample.
func residualFixtureRows(n int) ([]map[string]any, []map[string]bool) {
	rng := mrand.New(mrand.NewPCG(7, 11))
	rows := make([]map[string]any, 0, n)
	nulls := make([]map[string]bool, 0, n)
	for i := 0; i < n; i++ {
		region := "east"
		bump := 0.0
		if i%2 == 1 {
			region = "west"
			bump = 1
		}
		z1, z2, z3 := rng.NormFloat64(), rng.NormFloat64(), rng.NormFloat64()
		rows = append(rows, map[string]any{
			"alpha":  10 + 5*bump + z1,
			"beta":   20 + 3*bump + 0.8*z1 + 0.6*z2,
			"gamma":  30 + z3,
			"region": region,
		})
		nulls = append(nulls, map[string]bool{})
	}
	return rows, nulls
}

func residualPair(p *ResidualCorrelationProfile, a, b string) (ResidualCorrelation, bool) {
	for _, e := range p.Pairs {
		if e.A == a && e.B == b {
			return e, true
		}
	}
	return ResidualCorrelation{}, false
}

func unmeasuredPair(p *ResidualCorrelationProfile, a, b string) (ResidualUnmeasuredPair, bool) {
	for _, e := range p.Unmeasured {
		if e.A == a && e.B == b {
			return e, true
		}
	}
	return ResidualUnmeasuredPair{}, false
}

func residualFixtureProfile(t *testing.T, opts ProfileOptions) *Profile {
	t.Helper()
	schema := residualFixtureSchema(t)
	rows, nulls := residualFixtureRows(600)
	data := encodeModelRows(t, schema, rows, nulls)
	prof, err := profileRecords(schema, bytes.NewReader(data), opts)
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	return prof
}

// TestResidualCorrelations_FullSubmatrixCaptured is the acceptance bar
// on completeness: EVERY pair among participants is accounted for,
// measured or not, and the count is not bent by CorrelationTopK — which
// governs a ranked list of raw-value pairs and has nothing to say about
// a matrix.
func TestResidualCorrelations_FullSubmatrixCaptured(t *testing.T) {
	prof := residualFixtureProfile(t, ProfileOptions{
		FitModels:               true,
		FitResidualCorrelations: true,
		// Deliberately below the pair count: a top-K that leaked into
		// this section would truncate three pairs to one.
		CorrelationTopK: 1,
	})
	rc := prof.ResidualCorrelations
	if rc == nil {
		t.Fatalf("no residual_correlations section; warnings=%v", prof.Warnings)
	}
	if len(rc.Fields) != 3 {
		t.Fatalf("participants = %v, want all three modelled numerics", rc.Fields)
	}
	want := len(rc.Fields) * (len(rc.Fields) - 1) / 2
	if got := len(rc.Pairs) + len(rc.Unmeasured); got != want {
		t.Fatalf("submatrix covers %d pair(s) (%d measured + %d unmeasured), want the full %d",
			got, len(rc.Pairs), len(rc.Unmeasured), want)
	}
	// Fields must be sorted so the section does not inherit the fitter's
	// ordering.
	for i := 1; i < len(rc.Fields); i++ {
		if rc.Fields[i-1] >= rc.Fields[i] {
			t.Fatalf("participants not sorted: %v", rc.Fields)
		}
	}
	ab, ok := residualPair(rc, "alpha", "beta")
	if !ok {
		t.Fatalf("alpha x beta missing from measured pairs: %+v", rc)
	}
	if math.Abs(ab.Rho-0.8) > 0.1 {
		t.Errorf("residual rho(alpha, beta) = %v, want ~0.8 — the shared residual noise, "+
			"not the raw correlation the shared predictor also produces", ab.Rho)
	}
}

// TestResidualCorrelations_MeasuresResidualsNotRawValues pins the
// distinction the section exists for: alpha and gamma have NO shared
// residual noise but both are perfectly ordinary numerics, so a
// raw-value correlation over a cohort where alpha's level shifts with
// region is a different number from the residual one. The residual
// figure must be the one on the wire.
func TestResidualCorrelations_MeasuresResidualsNotRawValues(t *testing.T) {
	prof := residualFixtureProfile(t, ProfileOptions{
		FitModels:               true,
		FitResidualCorrelations: true,
	})
	rc := prof.ResidualCorrelations
	if rc == nil {
		t.Fatal("no residual_correlations section")
	}
	models := prof.FittedModels()
	alpha, ok := modelByField(models, "alpha")
	if !ok {
		t.Fatal("alpha carries no model")
	}
	beta, ok := modelByField(models, "beta")
	if !ok {
		t.Fatal("beta carries no model")
	}
	// Recompute straight off the residual vectors: the section must be a
	// function of these, not of the raw columns.
	xs, ys := coResiduals(&alpha, &beta)
	want := pearson(xs, ys)
	got, ok := residualPair(rc, "alpha", "beta")
	if !ok {
		t.Fatal("alpha x beta not measured")
	}
	if math.Abs(got.Rho-want) > 1e-12 {
		t.Errorf("rho on the wire = %v, residual rho = %v — the section is not reading residuals",
			got.Rho, want)
	}
	if got.N != len(xs) {
		t.Errorf("n = %d, co-present residual rows = %d", got.N, len(xs))
	}
}

// TestResidualCorrelations_MeasuredZeroSurvivesRoundTrip is one half of
// the story's central distinction: a pair genuinely measured at ~0 is a
// MEASUREMENT and must read back as one, rho key and all.
func TestResidualCorrelations_MeasuredZeroSurvivesRoundTrip(t *testing.T) {
	prof := residualFixtureProfile(t, ProfileOptions{
		FitModels:               true,
		FitResidualCorrelations: true,
	})
	before, ok := residualPair(prof.ResidualCorrelations, "alpha", "gamma")
	if !ok {
		t.Fatalf("alpha x gamma must be MEASURED (at ~0), not skipped: %+v", prof.ResidualCorrelations)
	}
	if math.Abs(before.Rho) > 0.15 {
		t.Fatalf("fixture drift: rho(alpha, gamma) = %v, expected ~0", before.Rho)
	}
	raw, err := json.Marshal(prof)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Profile
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	after, ok := residualPair(back.ResidualCorrelations, "alpha", "gamma")
	if !ok {
		t.Fatal("a measured-zero pair did not survive the round trip as measured")
	}
	if after.Rho != before.Rho || after.N != before.N {
		t.Errorf("round trip moved the measurement: %+v -> %+v", before, after)
	}
	if _, gap := unmeasuredPair(back.ResidualCorrelations, "alpha", "gamma"); gap {
		t.Error("a measured pair also appears as unmeasured; the two lists must be disjoint")
	}
}

// TestResidualCorrelations_UnmeasuredNeverEncodedAsZero is the other
// half, and the one the story is named for. A pair with no overlapping
// rows must land in `unmeasured` with a reason, and the SERIALISED form
// must carry no correlation for it anywhere — not a null, not a zero.
func TestResidualCorrelations_UnmeasuredNeverEncodedAsZero(t *testing.T) {
	// Hand-built models rather than a capture: the point is the
	// representation, and disjoint residual presence is easier to state
	// than to provoke through a cohort's null pattern.
	n := 40
	left := FieldModel{Field: "left",
		Residuals:       make([]float64, n),
		ResidualPresent: make([]bool, n)}
	right := FieldModel{Field: "right",
		Residuals:       make([]float64, n),
		ResidualPresent: make([]bool, n)}
	both := FieldModel{Field: "both",
		Residuals:       make([]float64, n),
		ResidualPresent: make([]bool, n)}
	for i := 0; i < n; i++ {
		v := float64(i%7) - 3
		left.Residuals[i], right.Residuals[i], both.Residuals[i] = v, -v, v*0.5
		// left and right never co-occur; both co-occurs with each.
		left.ResidualPresent[i] = i%2 == 0
		right.ResidualPresent[i] = i%2 == 1
		both.ResidualPresent[i] = true
	}

	var warnings []string
	rc := computeResidualCorrelations([]FieldModel{left, right, both}, &warnings)
	if rc == nil {
		t.Fatal("no submatrix")
	}
	gap, ok := unmeasuredPair(rc, "left", "right")
	if !ok {
		t.Fatalf("left x right share no row and must be UNMEASURED: %+v", rc)
	}
	if gap.Reason != ResidualUnmeasuredNoOverlap {
		t.Errorf("reason = %q, want %q", gap.Reason, ResidualUnmeasuredNoOverlap)
	}
	if gap.N != 0 {
		t.Errorf("overlap n = %d, want 0", gap.N)
	}
	if _, measured := residualPair(rc, "left", "right"); measured {
		t.Fatal("an unmeasured pair appears in the measured list")
	}
	// The other two pairs ARE measurable, so this is a genuine partial
	// submatrix rather than an all-or-nothing one.
	if len(rc.Pairs) != 2 {
		t.Fatalf("measured pairs = %d, want 2", len(rc.Pairs))
	}

	raw, err := json.Marshal(rc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc struct {
		Unmeasured []map[string]json.RawMessage `json:"unmeasured"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Unmeasured) != 1 {
		t.Fatalf("unmeasured entries = %d, want 1", len(doc.Unmeasured))
	}
	for k := range doc.Unmeasured[0] {
		if k == "rho" || k == "correlation" {
			t.Fatalf("unmeasured entry carries a %q key: %s", k, raw)
		}
	}

	var back ResidualCorrelationProfile
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if _, ok := unmeasuredPair(&back, "left", "right"); !ok {
		t.Fatal("unmeasured pair did not survive the round trip as unmeasured")
	}
	if _, ok := residualPair(&back, "left", "right"); ok {
		t.Fatal("unmeasured pair read back as measured")
	}
	if !hasWarningContaining(warnings, "recorded as unmeasured, not as zero") {
		t.Errorf("no warning naming the gap; warnings=%v", warnings)
	}
}

// TestResidualCorrelations_ConstantResidualIsUnmeasuredNotZero covers
// the second gap reason. Two residual vectors CAN overlap on every row
// and still admit no correlation, when one of them is constant — an
// exactly fitted model is the ordinary cause. 0/0 is not 0.
func TestResidualCorrelations_ConstantResidualIsUnmeasuredNotZero(t *testing.T) {
	n := 50
	flat := FieldModel{Field: "flat",
		Residuals: make([]float64, n), ResidualPresent: make([]bool, n)}
	varying := FieldModel{Field: "varying",
		Residuals: make([]float64, n), ResidualPresent: make([]bool, n)}
	for i := 0; i < n; i++ {
		flat.Residuals[i] = 0
		varying.Residuals[i] = float64(i)
		flat.ResidualPresent[i], varying.ResidualPresent[i] = true, true
	}
	rc := computeResidualCorrelations([]FieldModel{flat, varying}, nil)
	if rc == nil {
		t.Fatal("no submatrix")
	}
	gap, ok := unmeasuredPair(rc, "flat", "varying")
	if !ok {
		t.Fatalf("a constant residual admits no correlation and must be unmeasured: %+v", rc)
	}
	if gap.Reason != ResidualUnmeasuredNoVariance {
		t.Errorf("reason = %q, want %q", gap.Reason, ResidualUnmeasuredNoVariance)
	}
	if gap.N != n {
		t.Errorf("n = %d, want the full overlap %d — the diagnostic that separates this from no_overlap", gap.N, n)
	}
	if len(rc.Pairs) != 0 {
		t.Errorf("measured pairs = %+v, want none", rc.Pairs)
	}
}

// TestResidualCorrelations_ZeroPredictorModelParticipates pins the
// documented judgement call: selection can leave a target with no
// admitted predictor, and such a target still has a residual (its whole
// deviation from its own mean). Dropping it would lose real structure
// for a reason unrelated to that structure.
func TestResidualCorrelations_ZeroPredictorModelParticipates(t *testing.T) {
	prof := residualFixtureProfile(t, ProfileOptions{
		FitModels:               true,
		FitResidualCorrelations: true,
	})
	gamma, ok := modelByField(prof.FittedModels(), "gamma")
	if !ok {
		t.Fatal("gamma carries no model")
	}
	if len(gamma.Predictors) != 0 {
		t.Skipf("fixture drift: gamma acquired predictors %+v", gamma.Predictors)
	}
	rc := prof.ResidualCorrelations
	found := false
	for _, f := range rc.Fields {
		if f == "gamma" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a zero-predictor model must still participate: %v", rc.Fields)
	}
	if _, ok := residualPair(rc, "beta", "gamma"); !ok {
		t.Errorf("beta x gamma not measured: %+v", rc)
	}
}

// TestResidualCorrelations_TopKFlagsKeepTheirMeaning is the
// no-collateral-damage gate. --include-correlations / --correlation-top-k
// must still produce exactly the ranked raw-value list they always did,
// with or without the new flag.
func TestResidualCorrelations_TopKFlagsKeepTheirMeaning(t *testing.T) {
	base := ProfileOptions{
		IncludeStats:        true,
		IncludeCorrelations: true,
		CorrelationTopK:     2,
		FitModels:           true,
	}
	without := residualFixtureProfile(t, base)
	with := base
	with.FitResidualCorrelations = true
	withRC := residualFixtureProfile(t, with)

	if len(without.Pairwise) != 2 {
		t.Fatalf("pairwise = %d entries, --correlation-top-k=2 must cap it", len(without.Pairwise))
	}
	a, err := json.Marshal(without.Pairwise)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b, err := json.Marshal(withRC.Pairwise)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("the new flag moved `pairwise`:\n%s\n%s", a, b)
	}
	if withRC.ResidualCorrelations == nil {
		t.Fatal("residual submatrix missing")
	}
	if n := len(withRC.ResidualCorrelations.Pairs) + len(withRC.ResidualCorrelations.Unmeasured); n != 3 {
		t.Errorf("submatrix covers %d pair(s); --correlation-top-k=2 must not reach it", n)
	}
}

// TestResidualCorrelations_AbsentWithoutTheFlag holds the additive
// contract: a capture that did not ask for the section emits no key,
// byte-identically to before it existed.
func TestResidualCorrelations_AbsentWithoutTheFlag(t *testing.T) {
	prof := residualFixtureProfile(t, ProfileOptions{FitModels: true})
	if prof.ResidualCorrelations != nil {
		t.Fatalf("section present without the flag: %+v", prof.ResidualCorrelations)
	}
	raw, err := json.Marshal(prof)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(raw, []byte("residual_correlations")) {
		t.Error("`residual_correlations` key written by a capture that did not ask for it")
	}
}

// TestResidualCorrelations_WithoutFitModelsWarns pins the flag-combination
// policy: nothing to correlate, so no section — but said out loud, because
// a caller who asked for a submatrix and silently got none has no way to
// tell that from a cohort with no structure.
func TestResidualCorrelations_WithoutFitModelsWarns(t *testing.T) {
	prof := residualFixtureProfile(t, ProfileOptions{FitResidualCorrelations: true})
	if prof.ResidualCorrelations != nil {
		t.Fatalf("section present with no models: %+v", prof.ResidualCorrelations)
	}
	if !hasWarningContaining(prof.Warnings, "without --fit-models") {
		t.Errorf("no warning naming the missing flag; warnings=%v", prof.Warnings)
	}
}

// TestResidualCorrelations_DeterministicDocument holds the effort's hard
// contract at the capture end: same cohort + same flags => byte-identical
// document. The submatrix is quadratic and built by nested iteration, so
// it is exactly the shape a map-order fold would silently perturb.
func TestResidualCorrelations_DeterministicDocument(t *testing.T) {
	opts := ProfileOptions{
		FitModels:               true,
		FitResidualCorrelations: true,
		IncludeConditional:      true,
	}
	for i := 0; i < 4; i++ {
		first, err := json.Marshal(residualFixtureProfile(t, opts))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		second, err := json.Marshal(residualFixtureProfile(t, opts))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("profile document is not byte-reproducible on run %d", i)
		}
	}
}

// TestResidualCorrelations_SingleParticipantEmitsNothing — a submatrix
// over one field is the scalar 1 and carries no information.
func TestResidualCorrelations_SingleParticipantEmitsNothing(t *testing.T) {
	only := FieldModel{Field: "only",
		Residuals: []float64{1, 2, 3}, ResidualPresent: []bool{true, true, true}}
	if rc := computeResidualCorrelations([]FieldModel{only}, nil); rc != nil {
		t.Errorf("one participant produced a section: %+v", rc)
	}
	// A model whose residual reservoir is gone (a Profile decoded from a
	// document) is not a participant — see computeResidualCorrelations.
	stripped := FieldModel{Field: "stripped"}
	if rc := computeResidualCorrelations([]FieldModel{only, stripped}, nil); rc != nil {
		t.Errorf("a residual-less model participated: %+v", rc)
	}
}

// TestBuildCorrelator_UnmeasuredPairsAssumedAndRecorded pins the
// GENERATION-side policy this story had to choose: assume-and-record,
// not refuse. A three-field matrix built from two supplied pairs draws
// the third as independent — and says how many it completed, which is
// the half that did not exist before.
func TestBuildCorrelator_UnmeasuredPairsAssumedAndRecorded(t *testing.T) {
	spec := &Spec{
		RowCount: 1,
		Fields: []FieldSpec{
			{Name: "a", Type: "f64", Distribution: DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "b", Type: "f64", Distribution: DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "c", Type: "f64", Distribution: DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
		},
		Correlations: []CorrelationSpec{
			{A: "a", B: "b", Correlation: 0.5},
			{A: "b", B: "c", Correlation: 0.4},
		},
	}
	_, wfs, err := buildSchema(spec)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}

	corr, warnings, err := buildCorrelator(spec.Correlations, wfs)
	if err != nil {
		t.Fatalf("buildCorrelator: %v", err)
	}
	if corr == nil {
		t.Fatal("no correlator")
	}
	if !hasWarningContaining(warnings, "completed by assumption") {
		t.Fatalf("an assumed pair went unreported; warnings=%v", warnings)
	}
	if !hasWarningContaining(warnings, "1 of 3 pair(s)") {
		t.Errorf("warning does not count the assumption; warnings=%v", warnings)
	}

	// A fully supplied matrix assumes nothing and must stay silent —
	// otherwise the warning is noise rather than a signal.
	spec.Correlations = append(spec.Correlations, CorrelationSpec{A: "a", B: "c", Correlation: 0.2})
	_, warnings, err = buildCorrelator(spec.Correlations, wfs)
	if err != nil {
		t.Fatalf("buildCorrelator: %v", err)
	}
	if hasWarningContaining(warnings, "completed by assumption") {
		t.Errorf("a complete matrix warned about assumptions: %v", warnings)
	}
}

// TestBuildCorrelator_RidgeActivationIsReported — a matrix whose
// supplied entries are not jointly realizable still factorizes (the
// ridge is a deliberate safety net) but no longer does so in silence.
func TestBuildCorrelator_RidgeActivationIsReported(t *testing.T) {
	spec := &Spec{
		RowCount: 1,
		Fields: []FieldSpec{
			{Name: "a", Type: "f64", Distribution: DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "b", Type: "f64", Distribution: DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "c", Type: "f64", Distribution: DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
		},
		// Jointly impossible: a and b nearly identical, b and c nearly
		// identical, a and c nearly opposite.
		Correlations: []CorrelationSpec{
			{A: "a", B: "b", Correlation: 0.99},
			{A: "b", B: "c", Correlation: 0.99},
			{A: "a", B: "c", Correlation: -0.99},
		},
	}
	_, wfs, err := buildSchema(spec)
	if err != nil {
		t.Fatalf("buildSchema: %v", err)
	}
	_, warnings, err := buildCorrelator(spec.Correlations, wfs)
	if err != nil {
		t.Fatalf("buildCorrelator: %v", err)
	}
	if !hasWarningContaining(warnings, "ridge-regularized") {
		t.Fatalf("ridge activation went unreported; warnings=%v", warnings)
	}
	if hasWarningContaining(warnings, "completed by assumption") {
		t.Errorf("a complete matrix warned about assumptions: %v", warnings)
	}
}

// TestCholesky_ReportsNoRidgeForAConsistentMatrix keeps the ridge
// warning honest in the other direction.
func TestCholesky_ReportsNoRidgeForAConsistentMatrix(t *testing.T) {
	m := [][]float64{
		{1, 0.5, 0.2},
		{0.5, 1, 0.1},
		{0.2, 0.1, 1},
	}
	L, ridge, err := cholesky(m)
	if err != nil {
		t.Fatalf("cholesky: %v", err)
	}
	if L == nil {
		t.Fatal("no factor")
	}
	if ridge != 0 {
		t.Errorf("ridge = %v on a consistent matrix, want 0", ridge)
	}
}

func hasWarningContaining(warnings []string, needle string) bool {
	for _, w := range warnings {
		if strings.Contains(w, needle) {
			return true
		}
	}
	return false
}
