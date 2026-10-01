package pulse

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/processing/window"
	"github.com/frankbardon/pulse/types"
)

// ---- attribute fixtures ------------------------------------------------

// adaptAttrBase returns one value per row (the row's x doubled).
type adaptAttrBase struct{}

func (adaptAttrBase) Compute(rows extend.Rows, field string) ([]float64, error) {
	out := make([]float64, rows.Len())
	for i := range out {
		v, _ := rows.At(i).NumericValue(field)
		out[i] = 2 * v
	}
	return out, nil
}

type adaptAttrRow struct{ adaptAttrBase }

func (adaptAttrRow) Row(rec extend.Record, field string) (float64, error) {
	v, _ := rec.NumericValue(field)
	return 3 * v, nil
}

// adaptAttrTwoPass records its lifecycle so the forwarding of PrePass
// and Finalize is observable.
type adaptAttrTwoPass struct {
	adaptAttrRow
	prePasses *int
	finalized *bool
}

func (a adaptAttrTwoPass) PrePass(extend.Record, string) error { *a.prePasses++; return nil }
func (a adaptAttrTwoPass) Finalize() error                     { *a.finalized = true; return nil }

// TestAdaptAttribute_ForwardsExactlyTheImplementedTier walks the three
// tiers: the engine's RowLocalAttribute / TwoPassAttribute assertions
// succeed on the adapted value iff the embedder value implements the
// extend sibling, and every forwarded method reaches the embedder.
func TestAdaptAttribute_ForwardsExactlyTheImplementedTier(t *testing.T) {
	recs := twoRecords()
	pre, fin := 0, false
	cases := []struct {
		name          string
		inner         extend.AttributeComputer
		rowLocal, two bool
	}{
		{"buffered", adaptAttrBase{}, false, false},
		{"row_local", adaptAttrRow{}, true, false},
		{"two_pass", adaptAttrTwoPass{prePasses: &pre, finalized: &fin}, true, true},
	}
	for _, c := range cases {
		got := adaptAttribute(c.inner)
		vals, err := got.Compute(recs, "x")
		if err != nil || len(vals) != 2 || vals[0] != 2 || vals[1] != 4 {
			t.Errorf("%s: Compute = %v, %v; want [2 4]", c.name, vals, err)
		}
		row, isRow := got.(processing.RowLocalAttribute)
		two, isTwo := got.(processing.TwoPassAttribute)
		if isRow != c.rowLocal || isTwo != c.two {
			t.Errorf("%s: row_local=%v two_pass=%v; want %v %v", c.name, isRow, isTwo, c.rowLocal, c.two)
		}
		if isRow {
			if v, err := row.Row(recs[1], "x"); err != nil || v != 6 {
				t.Errorf("%s: Row = %v, %v; want 6", c.name, v, err)
			}
		}
		if isTwo {
			for _, r := range recs {
				if err := two.PrePass(r, "x"); err != nil {
					t.Fatalf("%s: PrePass: %v", c.name, err)
				}
			}
			if err := two.Finalize(); err != nil {
				t.Fatalf("%s: Finalize: %v", c.name, err)
			}
			if pre != 2 || !fin {
				t.Errorf("%s: prePasses=%d finalized=%v; want 2 true", c.name, pre, fin)
			}
		}
	}
}

// TestAdaptAttributeFactory_PropagatesErrorAndNil keeps the factory
// adapter transparent for the two non-instance outcomes.
func TestAdaptAttributeFactory_PropagatesErrorAndNil(t *testing.T) {
	boom := stderrors.New("boom")
	f := adaptAttributeFactory(AttributeRegistration{
		Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) { return nil, boom },
	})
	if _, err := f(&types.Attribute{}, nil); !stderrors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	f = adaptAttributeFactory(AttributeRegistration{
		Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) { return nil, nil },
	})
	if inst, err := f(&types.Attribute{}, nil); inst != nil || err != nil {
		t.Errorf("nil instance = %v, %v; want nil, nil", inst, err)
	}
}

// ---- test fixtures -----------------------------------------------------

type adaptRowTest struct {
	seen *[]extend.Record
}

func (a adaptRowTest) UpdateRow(rec extend.Record) error { *a.seen = append(*a.seen, rec); return nil }
func (a adaptRowTest) Finalize() (*types.TestResult, error) {
	return &types.TestResult{Statistic: float64(len(*a.seen))}, nil
}

type adaptPostTest struct{}

func (adaptPostTest) Run(rows []map[string]any) (*types.TestResult, error) {
	return &types.TestResult{Statistic: float64(len(rows))}, nil
}

// TestAdaptRowTestFactory_ForwardsUpdateRowAndFinalize asserts the
// engine's RowTest reaches the embedder with the engine's own record
// and that Finalize's result passes through untouched.
func TestAdaptRowTestFactory_ForwardsUpdateRowAndFinalize(t *testing.T) {
	var seen []extend.Record
	f := adaptRowTestFactory(TestRegistration{
		RowFactory: func(*types.Test, *encoding.Schema) (extend.RowTest, error) {
			return adaptRowTest{seen: &seen}, nil
		},
	})
	inst, err := f(&types.Test{}, nil)
	if err != nil || inst == nil {
		t.Fatalf("factory = %v, %v", inst, err)
	}
	recs := twoRecords()
	for _, r := range recs {
		if err := inst.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	res, err := inst.Finalize()
	if err != nil || res.Statistic != 2 {
		t.Fatalf("Finalize = %+v, %v; want statistic 2", res, err)
	}
	for i := range recs {
		if seen[i].(*processing.Record) != recs[i] {
			t.Errorf("UpdateRow %d did not receive the engine's record", i)
		}
	}
}

// TestAdaptTestFactories_PropagateErrorAndNil covers both tiers'
// non-instance outcomes, plus the tier-2 instance handed over as-is.
func TestAdaptTestFactories_PropagateErrorAndNil(t *testing.T) {
	boom := stderrors.New("boom")
	row := adaptRowTestFactory(TestRegistration{
		RowFactory: func(*types.Test, *encoding.Schema) (extend.RowTest, error) { return nil, boom },
	})
	if _, err := row(&types.Test{}, nil); !stderrors.Is(err, boom) {
		t.Errorf("row err = %v, want boom", err)
	}
	row = adaptRowTestFactory(TestRegistration{
		RowFactory: func(*types.Test, *encoding.Schema) (extend.RowTest, error) { return nil, nil },
	})
	if inst, err := row(&types.Test{}, nil); inst != nil || err != nil {
		t.Errorf("row nil = %v, %v; want nil, nil", inst, err)
	}
	post := adaptPostTestFactory(TestRegistration{
		PostFactory: func(*types.Test, *encoding.Schema) (extend.PostTest, error) { return nil, boom },
	})
	if _, err := post(&types.Test{}, nil); !stderrors.Is(err, boom) {
		t.Errorf("post err = %v, want boom", err)
	}
	post = adaptPostTestFactory(TestRegistration{
		PostFactory: func(*types.Test, *encoding.Schema) (extend.PostTest, error) { return nil, nil },
	})
	if inst, err := post(&types.Test{}, nil); inst != nil || err != nil {
		t.Errorf("post nil = %v, %v; want nil, nil", inst, err)
	}
	post = adaptPostTestFactory(TestRegistration{
		PostFactory: func(*types.Test, *encoding.Schema) (extend.PostTest, error) { return adaptPostTest{}, nil },
	})
	inst, err := post(&types.Test{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := inst.Run(make([]map[string]any, 3)); err != nil || res.Statistic != 3 {
		t.Errorf("Run = %+v, %v; want statistic 3", res, err)
	}
}

// ---- window fixtures ---------------------------------------------------

type adaptWindow struct{}

func (adaptWindow) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for pos, i := range part {
			rows[i][label] = float64(pos)
		}
	}
	return nil
}

// TestAdaptWindowFactory_ForwardsComputeErrorAndNil asserts the engine
// window factory reaches the embedder (spec passed through) and
// propagates its error and nil outcomes.
func TestAdaptWindowFactory_ForwardsComputeErrorAndNil(t *testing.T) {
	var gotSpec *types.Window
	f := adaptWindowFactory(WindowRegistration{
		Factory: func(w *types.Window, _ extend.WindowOptions) (extend.WindowComputer, error) {
			gotSpec = w
			return adaptWindow{}, nil
		},
	})
	spec := &types.Window{Label: "w"}
	inst, err := f(spec, window.WindowOptions{})
	if err != nil || inst == nil || gotSpec != spec {
		t.Fatalf("factory = %v, %v (spec forwarded: %v)", inst, err, gotSpec == spec)
	}
	rows := []map[string]any{{}, {}, {}}
	if err := inst.Compute(rows, [][]int{{2, 0}, {1}}, "w"); err != nil {
		t.Fatal(err)
	}
	if rows[2]["w"] != 0.0 || rows[0]["w"] != 1.0 || rows[1]["w"] != 0.0 {
		t.Errorf("rows = %v", rows)
	}
	boom := stderrors.New("boom")
	f = adaptWindowFactory(WindowRegistration{
		Factory: func(*types.Window, extend.WindowOptions) (extend.WindowComputer, error) { return nil, boom },
	})
	if _, err := f(spec, window.WindowOptions{}); !stderrors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	f = adaptWindowFactory(WindowRegistration{
		Factory: func(*types.Window, extend.WindowOptions) (extend.WindowComputer, error) { return nil, nil },
	})
	if inst, err := f(spec, window.WindowOptions{}); inst != nil || err != nil {
		t.Errorf("nil instance = %v, %v; want nil, nil", inst, err)
	}
}

// ---- feature fixtures --------------------------------------------------

// adaptFeatBase doubles x and marks odd rows null.
type adaptFeatBase struct{}

func (adaptFeatBase) Compute(rows extend.Rows, field string) (map[string]extend.FeatureOutput, error) {
	out := extend.FeatureOutput{Values: make([]float64, rows.Len()), Nulls: make([]bool, rows.Len())}
	for i := 0; i < rows.Len(); i++ {
		v, _ := rows.At(i).NumericValue(field)
		out.Values[i] = 2 * v
		out.Nulls[i] = i%2 == 1
	}
	return map[string]extend.FeatureOutput{"d": out}, nil
}

type adaptFeatStreaming struct {
	adaptFeatBase
	emit      map[string]extend.FeatureOutput
	seen      *[]extend.Record
	finalized *bool
}

func (a adaptFeatStreaming) PrePass(rec extend.Record, _ string) error {
	*a.seen = append(*a.seen, rec)
	return nil
}
func (a adaptFeatStreaming) Finalize() error { *a.finalized = true; return nil }
func (a adaptFeatStreaming) EmitRow(rec extend.Record, _ string) (map[string]extend.FeatureOutput, error) {
	*a.seen = append(*a.seen, rec)
	return a.emit, nil
}

// TestAdaptFeature_ForwardsStreamingIffImplemented asserts the engine's
// StreamingComputer assertion succeeds iff the embedder value
// implements extend.StreamingFeatureComputer, that Compute reaches the
// embedder over the engine's own records and converts the outputs, and
// that PrePass / Finalize / EmitRow are forwarded.
func TestAdaptFeature_ForwardsStreamingIffImplemented(t *testing.T) {
	recs := twoRecords()
	view := []feature.Record{recs[0], recs[1]}

	base := adaptFeature("FEAT_ACME_T", adaptFeatBase{})
	if _, ok := base.(feature.StreamingComputer); ok {
		t.Error("buffered feature surfaced as StreamingComputer")
	}
	out, err := base.Compute(view, "x")
	if err != nil {
		t.Fatal(err)
	}
	d := out["d"]
	if len(d.Values) != 2 || d.Values[0] != 2 || d.Values[1] != 4 || len(d.Nulls) != 2 || d.Nulls[0] || !d.Nulls[1] {
		t.Errorf("Compute = %+v; want values [2 4] nulls [false true]", d)
	}

	var seen []extend.Record
	fin := false
	emit := map[string]extend.FeatureOutput{"d": {Values: []float64{9}}}
	s := adaptFeature("FEAT_ACME_T", adaptFeatStreaming{emit: emit, seen: &seen, finalized: &fin})
	sc, ok := s.(feature.StreamingComputer)
	if !ok {
		t.Fatal("streaming feature not surfaced as StreamingComputer")
	}
	if err := sc.PrePass(recs[0], "x"); err != nil {
		t.Fatal(err)
	}
	if err := sc.Finalize(); err != nil || !fin {
		t.Fatalf("Finalize = %v, finalized=%v", err, fin)
	}
	got, err := sc.EmitRow(recs[1], "x")
	if err != nil || got["d"].Values[0] != 9 {
		t.Fatalf("EmitRow = %+v, %v; want 9", got, err)
	}
	if len(seen) != 2 || seen[0].(*processing.Record) != recs[0] || seen[1].(*processing.Record) != recs[1] {
		t.Errorf("streaming calls did not receive the engine's records: %v", seen)
	}
}

// TestAdaptFeature_EmitRowRejectsMalformedOutput asserts a streaming
// output that is not exactly one row is a PROCESSING_INTERNAL coded
// error, never an index panic in the engine's write loop, while a
// null-marked row with no value is accepted.
func TestAdaptFeature_EmitRowRejectsMalformedOutput(t *testing.T) {
	recs := twoRecords()
	for _, c := range []struct {
		name string
		out  extend.FeatureOutput
		ok   bool
	}{
		{"one value", extend.FeatureOutput{Values: []float64{1}}, true},
		{"null without value", extend.FeatureOutput{Nulls: []bool{true}}, true},
		{"no value", extend.FeatureOutput{}, false},
		{"two values", extend.FeatureOutput{Values: []float64{1, 2}}, false},
		{"two nulls", extend.FeatureOutput{Values: []float64{1}, Nulls: []bool{false, false}}, false},
		{"non-null without value", extend.FeatureOutput{Nulls: []bool{false}}, false},
	} {
		var seen []extend.Record
		fin := false
		sc := adaptFeature("FEAT_ACME_T", adaptFeatStreaming{
			emit: map[string]extend.FeatureOutput{"d": c.out}, seen: &seen, finalized: &fin,
		}).(feature.StreamingComputer)
		_, err := sc.EmitRow(recs[0], "x")
		if c.ok && err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
		if !c.ok {
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_INTERNAL {
				t.Errorf("%s: err = %v; want PROCESSING_INTERNAL", c.name, err)
			}
		}
	}
}

// minimalFeatureRecord implements only the narrow feature.Record
// contract, as a non-engine caller of feature.Apply might.
type minimalFeatureRecord struct{}

func (minimalFeatureRecord) NumericValue(name string) (float64, bool) {
	return 5, name == "x"
}
func (minimalFeatureRecord) StringValue(name string) (string, bool) { return "lbl", name == "c" }
func (minimalFeatureRecord) Set(string, float64)                    {}
func (minimalFeatureRecord) SetNull(string)                         {}

// TestFeatureRecord_EngineRecordPassesThroughAndMinimalDegrades pins
// the read view: the engine's *processing.Record reaches the embedder
// as itself; any other feature.Record degrades to a read-only view
// that reports its numeric and categorical reads and "no value" for
// everything else.
func TestFeatureRecord_EngineRecordPassesThroughAndMinimalDegrades(t *testing.T) {
	recs := twoRecords()
	if featureRecord(recs[0]).(*processing.Record) != recs[0] {
		t.Error("engine record was wrapped instead of passed through")
	}
	v := featureRecord(minimalFeatureRecord{})
	if _, wrapped := v.(featureRecordView); !wrapped {
		t.Fatalf("minimal record not wrapped: %T", v)
	}
	if n, ok := v.NumericValue("x"); !ok || n != 5 {
		t.Errorf("NumericValue = %v, %v", n, ok)
	}
	if s, ok := v.StringValue("c"); !ok || s != "lbl" {
		t.Errorf("StringValue = %v, %v", s, ok)
	}
	if v.IsNull("x") || v.IsNull("c") || !v.IsNull("missing") {
		t.Errorf("IsNull x=%v c=%v missing=%v; want false false true", v.IsNull("x"), v.IsNull("c"), v.IsNull("missing"))
	}
	if _, ok := v.SetMaskValue("x"); ok {
		t.Error("SetMaskValue reported a value")
	}
	if _, ok := v.DecimalValue("x"); ok {
		t.Error("DecimalValue reported a value")
	}
	if v.Schema() != nil {
		t.Error("Schema should be nil on the degraded view")
	}
}

// TestAdaptFeatureFactory_PropagatesErrorAndNil keeps the factory
// adapter transparent for the two non-instance outcomes.
func TestAdaptFeatureFactory_PropagatesErrorAndNil(t *testing.T) {
	boom := stderrors.New("boom")
	f := adaptFeatureFactory(FeatureRegistration{
		Factory: func(*types.Feature, *encoding.Schema) (extend.FeatureComputer, error) { return nil, boom },
	})
	if _, err := f(&types.Feature{}, nil); !stderrors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
	f = adaptFeatureFactory(FeatureRegistration{
		Factory: func(*types.Feature, *encoding.Schema) (extend.FeatureComputer, error) { return nil, nil },
	})
	if inst, err := f(&types.Feature{}, nil); inst != nil || err != nil {
		t.Errorf("nil instance = %v, %v; want nil, nil", inst, err)
	}
}
