package pulse_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Extension weight contract (.claude/reference/weighting.md, Extension
// contract): a registration declaring WeightAware reads the resolved
// row weight through extend.Record.Weight(); a non-aware one is
// skipped (aggregator, instance default) or refused
// (PULSE_EXTENSION_NOT_WEIGHT_AWARE) exactly like the built-in classes.
//
// Every test runs over weightedCohort:
//
//	x: 2  4  6  8  10
//	w: 1  2  -1 3  0.5

// wsumAgg folds Σ w·x, reading the weight from the record (1 when no
// weight is in force). It is streamable and mergeable.
type wsumAgg struct{ sum float64 }

func (a *wsumAgg) fold(rec extend.Record, field string) {
	x, ok := rec.NumericValue(field)
	if !ok {
		return
	}
	w, wok := rec.Weight()
	if !wok {
		w = 1
	}
	a.sum += w * x
}

func (a *wsumAgg) Aggregate(rows extend.Rows, field string) (float64, error) {
	a.sum = 0
	for i := 0; i < rows.Len(); i++ {
		a.fold(rows.At(i), field)
	}
	return a.sum, nil
}

func (a *wsumAgg) UpdateRow(rec extend.Record, field string) error {
	a.fold(rec, field)
	return nil
}

func (a *wsumAgg) Finalize() (float64, error) { return a.sum, nil }

func (a *wsumAgg) Merge(other extend.OnlineAggregator) error {
	a.sum += other.(*wsumAgg).sum
	return nil
}

// bufferedWsum hides the streaming siblings so the request runs the
// buffered Aggregate path.
type bufferedWsum struct{ inner wsumAgg }

func (b *bufferedWsum) Aggregate(rows extend.Rows, field string) (float64, error) {
	return b.inner.Aggregate(rows, field)
}

// specLog records the slot weight each factory call saw.
type specLog struct {
	mu   sync.Mutex
	seen []types.SlotWeight
}

func (l *specLog) add(w types.SlotWeight) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, w)
}

// wrowAttr is a row-local attribute emitting the row's weight, -1 when
// none is reported.
type wrowAttr struct{}

func (wrowAttr) Compute(rows extend.Rows, field string) ([]float64, error) {
	out := make([]float64, rows.Len())
	for i := range out {
		out[i], _ = wrowAttr{}.Row(rows.At(i), field)
	}
	return out, nil
}

func (wrowAttr) Row(rec extend.Record, _ string) (float64, error) {
	if w, ok := rec.Weight(); ok {
		return w, nil
	}
	return -1, nil
}

// wsumTest is a tier-1 row test whose statistic is Σw over the rows it
// sees (0 with no weight in force) and whose df is the row count.
type wsumTest struct{ sumW, rows float64 }

func (t *wsumTest) UpdateRow(rec extend.Record) error {
	t.rows++
	if w, ok := rec.Weight(); ok {
		t.sumW += w
	}
	return nil
}

func (t *wsumTest) Finalize() (*types.TestResult, error) {
	return &types.TestResult{Statistic: t.sumW, DF: t.rows}, nil
}

const (
	extWsum        = "AGG_ACME_WEIGHT_SUM"
	extWsumBuf     = "AGG_ACME_WEIGHT_SUMB"
	extPlainAgg    = "AGG_ACME_PLAIN_SUM"
	extWrowAttr    = "ATTR_ACME_WEIGHT_ROW"
	extPlainAttr   = "ATTR_ACME_PLAIN_ROW"
	extWsumTest    = "TEST_ACME_WEIGHT_SUM"
	extPlainTest   = "TEST_ACME_PLAIN_SUM"
	extWeightField = "w"
)

func weightExtensions(log *specLog) pulse.Extensions {
	return pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{
			{
				Name: extWsum, WeightAware: true, Streamable: true, Mergeable: true,
				Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
					log.add(spec.Weight)
					return &wsumAgg{}, nil
				},
			},
			{
				Name: extWsumBuf, WeightAware: true,
				Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
					log.add(spec.Weight)
					return &bufferedWsum{}, nil
				},
			},
			{
				Name: extPlainAgg, Streamable: true,
				Factory: func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
					log.add(spec.Weight)
					return &wsumAgg{}, nil
				},
			},
		},
		Attributes: []pulse.AttributeRegistration{
			{Name: extWrowAttr, WeightAware: true, Mode: pulse.AttributeModeRowLocal,
				Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) { return wrowAttr{}, nil }},
			{Name: extPlainAttr, Mode: pulse.AttributeModeRowLocal,
				Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) { return wrowAttr{}, nil }},
		},
		Tests: []pulse.TestRegistration{
			{Name: extWsumTest, WeightAware: true, Tier: pulse.TestTierRow, Streamable: true,
				RowFactory: func(*types.Test, *encoding.Schema) (extend.RowTest, error) { return &wsumTest{}, nil }},
			{Name: extPlainTest, Tier: pulse.TestTierRow, Streamable: true,
				RowFactory: func(*types.Test, *encoding.Schema) (extend.RowTest, error) { return &wsumTest{}, nil }},
		},
	}
}

func weightExtPulse(t *testing.T, fs afero.Fs, log *specLog, def *types.WeightSpec) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs, Extensions: weightExtensions(log), DefaultWeight: def})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	// Forget the probe constructions: the assertions are about runs.
	log.mu.Lock()
	log.seen = nil
	log.mu.Unlock()
	return p
}

// TestExtensionWeight_AwareAggregatorReadsWeight: a WeightAware
// aggregator sees the resolved weight on its spec and only VALID
// weights through Record.Weight() — the negative-weight row never
// reaches it — on the streaming and buffered paths alike, from every
// weight source; the orchestrator stamps the weighted floor keys and
// the invalid-row warning; predict reports `applied`.
func TestExtensionWeight_AwareAggregatorReadsWeight(t *testing.T) {
	fs, cohort := weightedCohort(t)
	ctx := context.Background()
	const sumW, sumWX = 1 + 2 + 3 + 0.5, 2*1 + 4*2 + 8*3 + 10*0.5

	type source struct {
		def  *types.WeightSpec
		reqW *types.WeightSpec
		slot types.SlotWeight
	}
	sources := map[string]source{
		"options": {def: &types.WeightSpec{Field: extWeightField}},
		"request": {reqW: &types.WeightSpec{Field: extWeightField}},
		"slot":    {slot: types.SlotWeightField(extWeightField)},
	}
	for name, src := range sources {
		for _, op := range []string{extWsum, extWsumBuf} {
			t.Run(name+"/"+op, func(t *testing.T) {
				log := &specLog{}
				p := weightExtPulse(t, fs, log, src.def)
				req := func() *types.Request {
					return &types.Request{
						Cohort: &types.Cohort{Filename: cohort},
						Weight: src.reqW,
						Aggregations: []*types.Aggregation{
							{Type: types.AggregationType(op), Field: "x", Label: "s", Weight: src.slot},
						},
					}
				}
				resp, err := p.Process(ctx, req())
				if err != nil {
					t.Fatal(err)
				}
				if got := resp.Data[0]["s"]; got != sumWX {
					t.Fatalf("weighted sum = %v, want %v", got, sumWX)
				}
				c := resp.Components.Aggregations[0]
				if c.SumWeights == nil || *c.SumWeights != sumW || c.NWeightInvalid == nil || *c.NWeightInvalid != 1 || c.NEff == nil {
					t.Fatalf("floor = %+v, want sum_weights %v, n_weight_invalid 1, n_eff set", c, sumW)
				}
				if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_WEIGHT_INVALID_ROWS) {
					t.Fatalf("warnings = %+v, want one PULSE_WEIGHT_INVALID_ROWS", resp.Warnings)
				}
				log.mu.Lock()
				if len(log.seen) == 0 {
					t.Error("the factory was never called")
				}
				for _, w := range log.seen {
					if s := w.Spec(); s == nil || s.Field != extWeightField || s.Kind != types.WeightKindProbability {
						t.Errorf("factory saw weight %+v, want the resolved {w, probability}", s)
					}
				}
				log.mu.Unlock()

				pr, err := p.Predict(ctx, req())
				if err != nil {
					t.Fatal(err)
				}
				if len(pr.Weights) != 1 || pr.Weights[0].Status != descriptor.WeightStatusApplied {
					t.Fatalf("predict weights = %+v, want applied", pr.Weights)
				}
			})
		}
	}

	t.Run("unweighted", func(t *testing.T) {
		p := weightExtPulse(t, fs, &specLog{}, nil)
		resp, err := p.Process(ctx, &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: extWsum, Field: "x", Label: "s"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := resp.Data[0]["s"]; got != 30.0 {
			t.Fatalf("unweighted sum = %v, want 30 (Weight() reports false)", got)
		}
		if c := resp.Components.Aggregations[0]; c.SumWeights != nil {
			t.Fatalf("unweighted floor carries sum_weights: %+v", c)
		}
	})
}

// TestExtensionWeight_NonAwareAggregator: an explicit (slot or request)
// weight on a non-aware extension aggregator is
// PULSE_EXTENSION_NOT_WEIGHT_AWARE at runtime and in predict; the
// instance default is skipped (unweighted figure, no floor keys,
// predict `skipped_not_weight_aware`, factory sees `null`).
func TestExtensionWeight_NonAwareAggregator(t *testing.T) {
	fs, cohort := weightedCohort(t)
	ctx := context.Background()
	one := func(a *types.Aggregation, reqW *types.WeightSpec) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Weight: reqW, Aggregations: []*types.Aggregation{a}}
	}
	p := weightExtPulse(t, fs, &specLog{}, nil)
	cases := map[string]*types.Request{
		"slot":    one(&types.Aggregation{Type: extPlainAgg, Field: "x", Weight: types.SlotWeightField(extWeightField)}, nil),
		"request": one(&types.Aggregation{Type: extPlainAgg, Field: "x"}, &types.WeightSpec{Field: extWeightField}),
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := p.Process(ctx, req)
			ce := requireCode(t, err, errors.PULSE_EXTENSION_NOT_WEIGHT_AWARE)
			if ce.Details["slot"] != "aggregations[0]" || ce.Details["operator"] != extPlainAgg || ce.Details["field"] != extWeightField {
				t.Fatalf("details = %v", ce.Details)
			}
			sameEntry(t, predictEnvelope(t, p, fs, cohort, req), err)
		})
	}
	if _, err := p.Process(ctx, one(&types.Aggregation{Type: extPlainAgg, Field: "x", Weight: types.NullSlotWeight()}, &types.WeightSpec{Field: extWeightField})); err != nil {
		t.Fatalf("opted-out non-aware aggregator refused: %v", err)
	}

	log := &specLog{}
	def := weightExtPulse(t, fs, log, &types.WeightSpec{Field: extWeightField})
	req := one(&types.Aggregation{Type: extPlainAgg, Field: "x", Label: "s"}, nil)
	resp, err := def.Process(ctx, req)
	if err != nil {
		t.Fatalf("default weight on a non-aware aggregator refused: %v", err)
	}
	if got := resp.Data[0]["s"]; got != 30.0 {
		t.Fatalf("skipped sum = %v, want the unweighted 30", got)
	}
	if c := resp.Components.Aggregations[0]; c.SumWeights != nil {
		t.Fatalf("skipped slot carries sum_weights: %+v", c)
	}
	if len(log.seen) == 0 {
		t.Fatal("the factory was never called")
	}
	for _, w := range log.seen {
		if !w.IsNull() {
			t.Fatalf("non-aware factory saw weight %+v, want null", w)
		}
	}
	pr, err := def.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(pr.Weights) != 1 || pr.Weights[0].Status != descriptor.WeightStatusSkippedNotWeightAware {
		t.Fatalf("predict weights = %+v, want skipped_not_weight_aware", pr.Weights)
	}
}

// TestExtensionWeight_NonAwareAttributeAndTestRefused: a non-aware
// extension attribute or test is refused under ANY weight in force —
// the instance default included — and `weight: null` opts it out.
func TestExtensionWeight_NonAwareAttributeAndTestRefused(t *testing.T) {
	fs, cohort := weightedCohort(t)
	ctx := context.Background()
	p := weightExtPulse(t, fs, &specLog{}, &types.WeightSpec{Field: extWeightField})
	base := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		}
	}
	attr := base()
	attr.Attributes = []*types.Attribute{{Type: extPlainAttr, Field: "x", Label: "a"}}
	test := base()
	test.Tests = []*types.Test{{Type: extPlainTest, Field: "x", Label: "t"}}
	cases := map[string]struct {
		req  *types.Request
		slot string
		op   string
	}{
		"attribute": {attr, "attributes[0]", extPlainAttr},
		"test":      {test, "tests[0]", extPlainTest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := p.Process(ctx, tc.req)
			ce := requireCode(t, err, errors.PULSE_EXTENSION_NOT_WEIGHT_AWARE)
			if ce.Details["slot"] != tc.slot || ce.Details["operator"] != tc.op || ce.Details["field"] != extWeightField {
				t.Fatalf("details = %v", ce.Details)
			}
			sameEntry(t, predictEnvelope(t, p, fs, cohort, tc.req), err)
		})
	}
	attr.Attributes[0].Weight = types.NullSlotWeight()
	test.Tests[0].Weight = types.NullSlotWeight()
	for name, req := range map[string]*types.Request{"attribute": attr, "test": test} {
		if _, err := p.Process(ctx, req); err != nil {
			t.Fatalf("opted-out %s refused: %v", name, err)
		}
	}
}

// TestExtensionWeight_AwareAttributeAndTest: a WeightAware attribute
// sees the valid weight on every row (an invalid-weight row still
// reaches it — it owes a value per row — but Weight() reports false
// there); a WeightAware row test sees only the valid-weight rows.
func TestExtensionWeight_AwareAttributeAndTest(t *testing.T) {
	fs, cohort := weightedCohort(t)
	ctx := context.Background()
	req := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Attributes:   []*types.Attribute{{Type: extWrowAttr, Field: "x", Label: "rw"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "rw", Label: "s", Weight: types.NullSlotWeight()}},
			Tests:        []*types.Test{{Type: extWsumTest, Field: "x", Label: "t"}},
		}
	}
	p := weightExtPulse(t, fs, &specLog{}, &types.WeightSpec{Field: extWeightField})
	resp, err := p.Process(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["s"]; got != 1+2-1+3+0.5 {
		t.Fatalf("Σ attribute = %v, want 5.5 (the negative weight reports false → -1)", got)
	}
	if len(resp.Tests) != 1 || resp.Tests[0].Statistic != 6.5 || resp.Tests[0].DF != 4 {
		t.Fatalf("test = %+v, want Σw 6.5 over the 4 valid-weight rows", resp.Tests)
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_WEIGHT_INVALID_ROWS) {
		t.Fatalf("warnings = %+v, want one PULSE_WEIGHT_INVALID_ROWS (only the extension slots read w)", resp.Warnings)
	}
	// Each extension slot alone still reads w, so each alone warns.
	attrOnly, testOnly := req(), req()
	attrOnly.Tests, testOnly.Attributes = nil, nil
	testOnly.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n", Weight: types.NullSlotWeight()}}
	for name, r := range map[string]*types.Request{"attribute": attrOnly, "test": testOnly} {
		one, err := p.Process(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if len(one.Warnings) != 1 || one.Warnings[0].Code != string(errors.PULSE_WEIGHT_INVALID_ROWS) {
			t.Fatalf("%s alone: warnings = %+v, want one PULSE_WEIGHT_INVALID_ROWS", name, one.Warnings)
		}
	}

	pr, err := p.Predict(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, w := range pr.Weights {
		status[w.Slot] = w.Status
	}
	if status["attributes[0]"] != descriptor.WeightStatusApplied || status["tests[0]"] != descriptor.WeightStatusApplied {
		t.Fatalf("predict statuses = %v, want applied on the aware attribute and test", status)
	}

	un := weightExtPulse(t, fs, &specLog{}, nil)
	resp, err = un.Process(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["s"]; got != -5.0 {
		t.Fatalf("unweighted Σ attribute = %v, want -5 (no weight in force)", got)
	}
	if resp.Tests[0].Statistic != 0 || resp.Tests[0].DF != 5 {
		t.Fatalf("unweighted test = %+v, want Σw 0 over 5 rows", resp.Tests[0])
	}
}

// TestExtensionWeight_ManifestProjectsWeightAware: the manifest's
// extension entries carry weight_aware for exactly the registrations
// that declare it.
func TestExtensionWeight_ManifestProjectsWeightAware(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: weightExtensions(&specLog{})})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p.Manifest(context.Background()).Extensions)
	if err != nil {
		t.Fatal(err)
	}
	var ext struct {
		Aggregators []map[string]any `json:"aggregators"`
		Attributes  []map[string]any `json:"attributes"`
		Tests       []map[string]any `json:"tests"`
	}
	if err := json.Unmarshal(raw, &ext); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{extWsum: true, extWsumBuf: true, extWrowAttr: true, extWsumTest: true}
	seen := 0
	for _, list := range [][]map[string]any{ext.Aggregators, ext.Attributes, ext.Tests} {
		for _, m := range list {
			seen++
			name, _ := m["name"].(string)
			got, present := m["weight_aware"]
			if want[name] != present || (present && got != true) {
				t.Errorf("%s weight_aware = %v (present %v), want %v", name, got, present, want[name])
			}
		}
	}
	if seen != 7 {
		t.Fatalf("saw %d extension entries, want 7", seen)
	}
}

// TestExtensionWeight_ProbeConstructsWithAndWithoutWeight: a
// WeightAware factory is probed twice — name-only and with a weight on
// its spec — and either construction failing surfaces the existing
// probe codes; a non-aware factory is never handed a weight.
func TestExtensionWeight_ProbeConstructsWithAndWithoutWeight(t *testing.T) {
	refuseWeighted := func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
		if spec.Weight.Spec() != nil {
			return nil, errTestWeighted
		}
		return &wsumAgg{}, nil
	}
	bufferedWhenWeighted := func(spec *types.Aggregation, _ *encoding.Schema) (extend.Aggregator, error) {
		if spec.Weight.Spec() != nil {
			return &bufferedWsum{}, nil
		}
		return &wsumAgg{}, nil
	}
	cases := map[string]struct {
		ext   pulse.Extensions
		code  errors.Code
		valid bool
	}{
		"aware aggregator errors when weighted": {
			ext:  pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{{Name: extWsum, WeightAware: true, Factory: refuseWeighted}}},
			code: errors.PULSE_EXTENSION_FACTORY_PANIC,
		},
		"aware aggregator loses streaming when weighted": {
			ext:  pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{{Name: extWsum, WeightAware: true, Streamable: true, Factory: bufferedWhenWeighted}}},
			code: errors.PULSE_EXTENSION_STREAMABLE_MISMATCH,
		},
		"non-aware aggregator never probed weighted": {
			ext:   pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{{Name: extWsum, Streamable: true, Factory: refuseWeighted}}},
			valid: true,
		},
		"aware attribute panics when weighted": {
			ext: pulse.Extensions{Attributes: []pulse.AttributeRegistration{{Name: extWrowAttr, WeightAware: true, Mode: pulse.AttributeModeRowLocal,
				Factory: func(spec *types.Attribute, _ *encoding.Schema) (extend.AttributeComputer, error) {
					if spec.Weight.Spec() != nil {
						panic("weighted")
					}
					return wrowAttr{}, nil
				}}}},
			code: errors.PULSE_EXTENSION_FACTORY_PANIC,
		},
		"aware test errors unweighted": {
			ext: pulse.Extensions{Tests: []pulse.TestRegistration{{Name: extWsumTest, WeightAware: true, Tier: pulse.TestTierRow,
				RowFactory: func(spec *types.Test, _ *encoding.Schema) (extend.RowTest, error) {
					if spec.Weight.Spec() == nil {
						return nil, errTestWeighted
					}
					return &wsumTest{}, nil
				}}}},
			code: errors.PULSE_EXTENSION_FACTORY_PANIC,
		},
		"aware test errors weighted": {
			ext: pulse.Extensions{Tests: []pulse.TestRegistration{{Name: extWsumTest, WeightAware: true, Tier: pulse.TestTierRow,
				RowFactory: func(spec *types.Test, _ *encoding.Schema) (extend.RowTest, error) {
					if spec.Weight.Spec() != nil {
						return nil, errTestWeighted
					}
					return &wsumTest{}, nil
				}}}},
			code: errors.PULSE_EXTENSION_FACTORY_PANIC,
		},
		"non-aware test never probed": {
			ext: pulse.Extensions{Tests: []pulse.TestRegistration{{Name: extWsumTest, Tier: pulse.TestTierRow,
				RowFactory: func(*types.Test, *encoding.Schema) (extend.RowTest, error) { return nil, errTestWeighted }}}},
			valid: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := pulse.New(newTestOptions(tc.ext))
			if tc.valid {
				if err != nil {
					t.Fatalf("pulse.New: %v", err)
				}
				return
			}
			ce := requireCode(t, err, tc.code)
			if name != "aware test errors unweighted" && ce.Details["weighted"] != true {
				t.Fatalf("details = %v, want weighted: true on the weighted probe", ce.Details)
			}
		})
	}
}

var errTestWeighted = errorString("refused")

type errorString string

func (e errorString) Error() string { return string(e) }

// TestExtensionWeight_CrosstabCellMatchesBuiltin: a WeightAware
// extension Σw·x cell equals the weighted built-in AGG_SUM cell —
// cells, margins, grand total and the orchestrator's floor keys — on
// the fused arm (a mergeable, summable registration) and on the
// buffered arm (a buffered-only one).
func TestExtensionWeight_CrosstabCellMatchesBuiltin(t *testing.T) {
	fs, cohort := weightedCohort(t)
	ctx := context.Background()
	const fused = "AGG_ACME_WEIGHT_SUMF"
	ext := weightExtensions(&specLog{})
	ext.Aggregators = append(ext.Aggregators, pulse.AggregatorRegistration{
		Name: fused, WeightAware: true, Streamable: true, Mergeable: true, MarginReducibility: types.MarginSummable,
		FieldInputs: func(json.RawMessage) []string { return nil },
		Factory:     func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) { return &wsumAgg{}, nil },
	})
	p, err := pulse.New(pulse.Options{FS: fs, Extensions: ext, DefaultWeight: &types.WeightSpec{Field: extWeightField}})
	if err != nil {
		t.Fatal(err)
	}
	req := func(cell string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Params: json.RawMessage(`{"interval":5}`)}},
				Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Params: json.RawMessage(`{"interval":100}`)}},
				Cell:    &types.Aggregation{Type: types.AggregationType(cell), Field: "x", Label: "s"},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		}
	}
	type view struct {
		Cells      any
		Rows, Cols any
		Grand      any
		Floor      any
	}
	run := func(cell string) (view, *descriptor.PredictResult) {
		resp, err := p.Process(ctx, req(cell))
		if err != nil {
			t.Fatalf("%s: %v", cell, err)
		}
		m := resp.Crosstab.Matrix
		var floor []any
		for _, row := range resp.Components.Crosstab.CellComponents {
			for _, c := range row {
				floor = append(floor, []any{c["sum_weights"], c["n_eff"], c["n_weight_invalid"]})
			}
		}
		pr, err := p.Predict(ctx, req(cell))
		if err != nil {
			t.Fatal(err)
		}
		return view{m.Cells, m.RowMargins, m.ColumnMargins, m.GrandTotal, floor}, pr
	}
	want, _ := run(string(types.AGG_SUM))
	wantJSON, _ := json.Marshal(want)
	for cell, wantFusable := range map[string]bool{fused: true, extWsumBuf: false} {
		got, pr := run(cell)
		if pr.CrosstabFusable == nil || *pr.CrosstabFusable != wantFusable {
			t.Fatalf("%s: crosstab_fusable = %v (%v), want %v", cell, *pr.CrosstabFusable, pr.CrosstabFusionReasons, wantFusable)
		}
		gotJSON, _ := json.Marshal(got)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("%s crosstab = %s\nwant (weighted AGG_SUM) %s", cell, gotJSON, wantJSON)
		}
	}
	if !json.Valid(wantJSON) || len(want.Floor.([]any)) == 0 {
		t.Fatalf("no cell components to compare: %s", wantJSON)
	}
}
