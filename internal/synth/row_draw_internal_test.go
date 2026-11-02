package synth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// preRuleDrawFixtureHashes are the SHA-256 of every shipped spec fixture
// generated at seed 20240630 BEFORE uniform() / bernoulli() and
// `"output": false` existed (commit 24a997a7). Rules without draws and
// fields without `output` must not move a byte.
var preRuleDrawFixtureHashes = map[string]string{
	"../examples/synth/01_minimal.synth.json":              "ec9fa1f765f64cf860f9bdfbd6debeb90735aab42c135b3028974a5537344489",
	"../examples/synth/02_correlated_marginals.synth.json": "660dcfe043e42a2a608ee43ed4f9d41f61a99dea5c07ab44d07515d027ce699c",
	"../examples/synth/03_constrained.synth.json":          "642e05ab710c46ba50c9e615b6a1cd82e20de9c3591a98ab7af05574ecf4bc01",
	"../examples/synth/04_decimal_currency.synth.json":     "a53c05172d1b3769594f54202950944d381cb20dfae47b05b22a3c843eed3d1e",
	"../examples/synth/05_regex_ids.synth.json":            "8c91d61e233c072a08d20c9b7a0f622dabb5b60edb668f63ebc9c12a1a75b7a2",
	"../examples/synth/06_mixture_bimodal.synth.json":      "e68975be85f249b280b7c12b189911f1816a5e0d59bbe20ec7eb69e60c5e1cec",
	"../examples/synth/07_mmm_adstock.synth.json":          "3be07cccd510b7dce76647406ec52ecf0c093d546358b83d541130ba45c14dbd",
	"../examples/synth/08_zoned_business_hours.synth.json": "57994bb809b8f0ab9282d7e5775dbc86f12703b4f8fca22dc99dbde9444b2d9e",
	"../../testdata/demo/demo_cohort.synth.json":           "d3d3646f63560dd8bb75bd93484f144c97ccdeaa1e514a51c1a388b1247e7581",
	"../../testdata/experiment/ab_test.synth.json":         "c518dd7dd754cef63fc5fde2f14785c0ae2f16469da13c460f06988d4f74fc03",
}

func TestRuleDraw_EveryExistingSpecFixtureIsByteIdentical(t *testing.T) {
	t.Parallel()

	for path, want := range preRuleDrawFixtureHashes {
		spec := readSpecFixture(t, path)
		data, _, err := SynthBytes(spec, Options{Seed: 20240630})
		if err != nil {
			t.Fatalf("synth %s: %v", path, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s changed bytes\n got %s\nwant %s", path, got, want)
		}
	}
}

func readSpecFixture(t *testing.T, path string) *Spec {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	spec, err := ParseSpec(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return spec
}

func boolPtr(b bool) *bool { return &b }

// drawTestSpec is two drawn fields plus a helper `u` a rule fills with
// uniform(); output says whether u is written.
func drawTestSpec(rows int, output *bool) *Spec {
	return &Spec{RowCount: rows, Fields: []FieldSpec{
		{Name: "a", Type: "f64", Distribution: DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
		{Name: "u", Type: "f64", Distribution: DistConstant, Params: map[string]any{"value": 0.0}, Output: output},
		{Name: "b", Type: "f64", Distribution: DistUniform, Params: map[string]any{"min": 0.0, "max": 10.0}},
	}}
}

// A draw reads its own stream, never the generation RNG: adding one
// moves no other field's value.
func TestRuleDraw_AddingADrawMovesNoOtherField(t *testing.T) {
	t.Parallel()

	plain := drawTestSpec(500, nil)
	drawn := drawTestSpec(500, nil)
	drawn.Rules = []RuleSpec{{SetExpr: map[string]string{"u": "uniform()"}}}
	p, _, err := SynthBytes(plain, Options{Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := SynthBytes(drawn, Options{Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	pn, _ := decodeCohort(t, p)
	dn, _ := decodeCohort(t, d)
	for _, f := range []string{"a", "b"} {
		for i := range pn[f] {
			if math.Float64bits(pn[f][i]) != math.Float64bits(dn[f][i]) {
				t.Fatalf("%s[%d] moved: %v -> %v", f, i, pn[f][i], dn[f][i])
			}
		}
	}
	var sum float64
	for _, u := range dn["u"] {
		if u < 0 || u >= 1 {
			t.Fatalf("uniform() = %v outside [0, 1)", u)
		}
		sum += u
	}
	if mean := sum / float64(len(dn["u"])); math.Abs(mean-0.5) > 0.05 {
		t.Errorf("uniform() mean = %.4f, want ~0.5", mean)
	}
}

// The documented derivation is the contract other stages reuse: an
// accepted row's draw is drawUniformAt(seed key, site key, ordinal,
// attempt, call), with attempt counting this ordinal's rejected tries.
// A constraint on the drawn value rejects about half the attempts, and
// each redraw must read FRESH values — replaying attempt 0 would reject
// the same draw forever.
func TestRuleDraw_RejectedAttemptsDrawFreshValuesFromTheDocumentedStream(t *testing.T) {
	t.Parallel()

	spec := drawTestSpec(300, nil)
	spec.Rules = []RuleSpec{{SetExpr: map[string]string{"u": "uniform()"}}}
	spec.Constraints = []ConstraintSpec{{Expr: "u >= 0.5"}}
	spec.MaxRejectionRate = 0.8
	const seed = 41
	data, res, err := SynthBytes(spec, Options{Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsRejected < 200 {
		t.Fatalf("rejected %d attempts, want roughly as many as accepted", res.RowsRejected)
	}
	again, _, err := SynthBytes(spec, Options{Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again) {
		t.Fatal("same spec and seed produced different bytes")
	}

	nums, _ := decodeCohort(t, data)
	seedKey := drawStreamKey(seed, ruleDrawDomain)
	site := drawSiteKey("rule/0/set_expr/u/0")
	rejected := 0
	for ordinal, got := range nums["u"] {
		attempt := 0
		want := drawUniformAt(seedKey, site, ordinal, attempt, 0)
		for want < 0.5 {
			attempt++
			want = drawUniformAt(seedKey, site, ordinal, attempt, 0)
		}
		rejected += attempt
		if got != want {
			t.Fatalf("row %d: u = %v, want %v (attempt %d)", ordinal, got, want, attempt)
		}
	}
	if rejected != res.RowsRejected {
		t.Errorf("stream predicts %d rejections, run reports %d", rejected, res.RowsRejected)
	}
}

// Two sites never share a value, and one site called twice in one
// expression reads two values.
func TestRuleDraw_SitesAndCallsAreIndependent(t *testing.T) {
	t.Parallel()

	spec := &Spec{RowCount: 200, Fields: []FieldSpec{
		{Name: "x", Type: "f64", Distribution: DistConstant, Params: map[string]any{"value": 0.0}},
		{Name: "y", Type: "f64", Distribution: DistConstant, Params: map[string]any{"value": 0.0}},
		{Name: "z", Type: "f64", Distribution: DistConstant, Params: map[string]any{"value": 0.0}},
	}, Rules: []RuleSpec{
		{SetExpr: map[string]string{"x": "uniform()"}},
		{SetExpr: map[string]string{"y": "uniform()", "z": "uniform() - uniform()"}},
	}}
	data, _, err := SynthBytes(spec, Options{Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	nums, _ := decodeCohort(t, data)
	for i := range nums["x"] {
		if nums["x"][i] == nums["y"][i] {
			t.Fatalf("row %d: two sites drew the same value %v", i, nums["x"][i])
		}
		if nums["z"][i] == 0 {
			t.Fatalf("row %d: two calls in one expression drew the same value", i)
		}
	}
}

func TestRuleDraw_BernoulliRateAndSeed(t *testing.T) {
	t.Parallel()

	spec := &Spec{RowCount: 20000, Fields: []FieldSpec{
		{Name: "flag", Type: "packed_bool", Distribution: DistBernoulli, Params: map[string]any{"p": 0.0}},
	}, Rules: []RuleSpec{{SetExpr: map[string]string{"flag": "bernoulli(0.3)"}}}}
	a, _, err := SynthBytes(spec, Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := SynthBytes(spec, Options{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("a different seed produced identical draws")
	}
	vals, _ := decodeConstraintTestField(t, a, "flag")
	var n float64
	for _, v := range vals {
		n += v
	}
	// SE = sqrt(0.3*0.7/20000) ~ 0.0032; 4 SE.
	if rate := n / float64(len(vals)); math.Abs(rate-0.3) > 0.013 {
		t.Errorf("bernoulli(0.3) rate = %.4f", rate)
	}
}

func TestRuleDraw_MisuseIsRefusedWithACode(t *testing.T) {
	t.Parallel()

	base := func() *Spec {
		return &Spec{RowCount: 10, Fields: []FieldSpec{
			{Name: "x", Type: "f64", Distribution: DistUniform, Params: map[string]any{"min": 0.0, "max": 2.0}},
			{Name: "flag", Type: "packed_bool", Distribution: DistBernoulli, Params: map[string]any{"p": 0.5}},
		}}
	}
	cases := []struct {
		name  string
		edit  func(*Spec)
		code  errors.Code
		synth bool // refused at generation (row time), not validation
	}{
		{"uniform with an argument", func(s *Spec) {
			s.Rules = []RuleSpec{{SetExpr: map[string]string{"x": "uniform(3)"}}}
		}, errors.PULSE_SYNTH_RULE_DRAW_INVALID, false},
		{"bernoulli without p", func(s *Spec) {
			s.Rules = []RuleSpec{{SetExpr: map[string]string{"flag": "bernoulli()"}}}
		}, errors.PULSE_SYNTH_RULE_DRAW_INVALID, false},
		{"literal p above one", func(s *Spec) {
			s.Rules = []RuleSpec{{SetExpr: map[string]string{"flag": "bernoulli(1.5)"}}}
		}, errors.PULSE_SYNTH_RULE_DRAW_INVALID, false},
		{"literal p below zero in a when", func(s *Spec) {
			s.Rules = []RuleSpec{{When: "bernoulli(-0.1)", SetNull: nil, Set: map[string]any{"x": 1.0}}}
		}, errors.PULSE_SYNTH_RULE_DRAW_INVALID, false},
		{"a draw in a constraint", func(s *Spec) {
			s.Constraints = []ConstraintSpec{{Expr: "uniform() < 0.5"}}
		}, errors.PULSE_SYNTH_RULE_DRAW_INVALID, false},
		{"computed p outside [0, 1]", func(s *Spec) {
			s.Rules = []RuleSpec{{SetExpr: map[string]string{"flag": "bernoulli(x)"}}}
		}, errors.PULSE_SYNTH_RULE_DRAW_INVALID, true},
		{"calling a draw a field shadows", func(s *Spec) {
			s.Fields = append(s.Fields, FieldSpec{Name: "uniform", Type: "f64", Distribution: DistConstant, Params: map[string]any{"value": 0.0}})
			s.Rules = []RuleSpec{{SetExpr: map[string]string{"x": "uniform()"}}}
		}, errors.PULSE_SYNTH_RULE_EXPR_INVALID, false},
	}
	for _, tc := range cases {
		s := base()
		tc.edit(s)
		err := validateSpec(s)
		if tc.synth {
			if err != nil {
				t.Fatalf("%s: validation refused (%v); the fault is row-time", tc.name, err)
			}
			_, _, err = SynthBytes(s, Options{Seed: 5})
		}
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != tc.code {
			t.Errorf("%s: got %v, want %s", tc.name, err, tc.code)
		}
	}
}

// A helper field is drawn and readable by rules and constraints, and
// absent from the written schema; every other column is value-identical
// to the same spec with the helper written.
func TestHelperField_AbsentFromSchemaAndMovesNoOtherColumn(t *testing.T) {
	t.Parallel()

	mk := func(output *bool) *Spec {
		s := &Spec{RowCount: 400, Fields: []FieldSpec{
			{Name: "a", Type: "f64", Nullable: true, NullRate: 0.2, Distribution: DistNormal, Params: map[string]any{"mean": 0.0, "std": 1.0}},
			{Name: "h", Type: "f64", Nullable: true, NullRate: 0.3, Distribution: DistNormal, Params: map[string]any{"mean": 5.0, "std": 1.0}, Output: output},
			{Name: "c", Type: "categorical_u8", Distribution: DistWeightedCategorical, Params: map[string]any{"values": []any{"lo", "hi"}}},
			{Name: "b", Type: "f64", Nullable: true, NullRate: 0.4, Distribution: DistUniform, Params: map[string]any{"min": 0.0, "max": 1.0}},
		}}
		s.Rules = []RuleSpec{{SetExpr: map[string]string{"c": `!isnull(h) && h > 5 ? "hi" : "lo"`}}}
		s.Constraints = []ConstraintSpec{{Expr: "isnull(h) || h > 3"}}
		return s
	}
	hidden, _, err := SynthBytes(mk(boolPtr(false)), Options{Seed: 12})
	if err != nil {
		t.Fatal(err)
	}
	written, _, err := SynthBytes(mk(boolPtr(true)), Options{Seed: 12})
	if err != nil {
		t.Fatal(err)
	}
	defaulted, _, err := SynthBytes(mk(nil), Options{Seed: 12})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, defaulted) {
		t.Fatal(`"output": true is not byte-identical to an absent output`)
	}
	if got := schemaFieldNames(t, hidden); !equalStrings(got, []string{"a", "c", "b"}) {
		t.Fatalf("written schema = %v, want [a c b]", got)
	}
	for _, f := range []string{"a", "b"} {
		hv, hn := decodeConstraintTestField(t, hidden, f)
		wv, wn := decodeConstraintTestField(t, written, f)
		for i := range wv {
			if hn[i] != wn[i] || (!wn[i] && hv[i] != wv[i]) {
				t.Fatalf("%s[%d]: hidden (%v, null %v) != written (%v, null %v)", f, i, hv[i], hn[i], wv[i], wn[i])
			}
		}
	}
	// The rule read the helper: c is "hi" exactly where the written h > 5.
	_, hc := decodeCohort(t, hidden)
	wh, whn := decodeConstraintTestField(t, written, "h")
	for i := range wh {
		want := "lo"
		if !whn[i] && wh[i] > 5 {
			want = "hi"
		}
		if hc["c"][i] != want {
			t.Fatalf("row %d: c = %q, want %q from the helper", i, hc["c"][i], want)
		}
		if !whn[i] && wh[i] <= 3 {
			t.Fatalf("row %d: the constraint on the helper did not hold (h = %v)", i, wh[i])
		}
	}
}

func TestHelperField_AllHelpersIsRefused(t *testing.T) {
	t.Parallel()

	s := &Spec{RowCount: 5, Fields: []FieldSpec{
		{Name: "h", Type: "f64", Distribution: DistConstant, Params: map[string]any{"value": 1.0}, Output: boolPtr(false)},
	}}
	var ce *errors.CodedError
	if err := validateSpec(s); !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
		t.Fatalf("got %v, want SERVICE_VALIDATION", err)
	}
}

// The credit-risk idiom: a true PD computed in a rule and an outcome
// drawn against it, with both helpers kept out of the file.
func TestRuleDraw_CreditSpecDropsItsHelperColumns(t *testing.T) {
	t.Parallel()

	spec := readSpecFixture(t, "testdata/credit_rule_draws.synth.json")
	data, res, err := SynthBytes(spec, Options{Seed: 20240630})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsRejected == 0 {
		t.Fatal("the fixture's constraints must reject some attempts for this test to cover rejection")
	}
	want := []string{"fico_score", "dti", "credit_band", "default_12m", "collections_flag", "recovery_amount"}
	if got := schemaFieldNames(t, data); !equalStrings(got, want) {
		t.Fatalf("written schema = %v, want %v", got, want)
	}
	again, _, err := SynthBytes(readSpecFixture(t, "testdata/credit_rule_draws.synth.json"), Options{Seed: 20240630})
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("not deterministic (%v)", err)
	}

	// The same spec with the helpers written: every shared column is
	// identical, and the drawn outcome tracks the PD it was drawn on.
	for i := range spec.Fields {
		spec.Fields[i].Output = nil
	}
	open, _, err := SynthBytes(spec, Options{Seed: 20240630})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range want {
		hv, hn := decodeConstraintTestField(t, data, f)
		ov, on := decodeConstraintTestField(t, open, f)
		for i := range ov {
			if hn[i] != on[i] || hv[i] != ov[i] {
				t.Fatalf("%s[%d] differs once the helpers are hidden", f, i)
			}
		}
	}
	pd, _ := decodeConstraintTestField(t, open, "true_pd")
	def, _ := decodeConstraintTestField(t, open, "default_12m")
	_, recNull := decodeConstraintTestField(t, open, "recovery_amount")
	var sumPD, sumDef, var_ float64
	for i := range pd {
		sumPD += pd[i]
		sumDef += def[i]
		var_ += float64(pd[i] * (1 - pd[i]))
		if recNull[i] != (def[i] == 0) {
			t.Fatalf("row %d: recovery null %v with default %v", i, recNull[i], def[i])
		}
	}
	if se := math.Sqrt(var_); math.Abs(sumDef-sumPD) > 4*se {
		t.Errorf("defaults %v vs expected %.1f (4 SE = %.1f)", sumDef, sumPD, 4*se)
	}
}

func schemaFieldNames(t *testing.T, data []byte) []string {
	t.Helper()
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range schema.Fields {
		names = append(names, f.Name)
	}
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
