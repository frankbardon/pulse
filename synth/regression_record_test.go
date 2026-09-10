package synth

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// dummyTestSchema builds a cohort schema with one scalar target, one
// three-level categorical, one four-option set and one plain numeric
// predictor — the four shapes newDummyPlan has to expand.
func dummyTestSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dma := encoding.NewDictionary()
	for _, v := range []string{"501", "602", "803"} {
		if _, err := dma.Add(v); err != nil {
			t.Fatalf("dma dict add: %v", err)
		}
	}
	assets := encoding.NewDictionary()
	for _, v := range []string{"premium", "value", "eco", "legacy"} {
		if _, err := assets.Add(v); err != nil {
			t.Fatalf("assets dict add: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "spend", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "age", Type: encoding.FieldTypeU8, Nullable: true},
		{Name: "dma", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: dma},
		{Name: "brandAssets", Type: encoding.FieldTypeSetU8, Nullable: true, Dictionary: assets},
	}}
}

func TestDummyPlan_ExpandsEveryPredictorShape(t *testing.T) {
	plan, err := newDummyPlan(dummyTestSchema(t), "spend", []string{"dma", "brandAssets", "age"})
	if err != nil {
		t.Fatalf("newDummyPlan: %v", err)
	}
	want := []string{
		"c:3:dma=501", "c:3:dma=602", "c:3:dma=803",
		"s:11:brandAssets[premium]", "s:11:brandAssets[value]",
		"s:11:brandAssets[eco]", "s:11:brandAssets[legacy]",
		"age",
	}
	got := plan.PredictorNames()
	if len(got) != len(want) {
		t.Fatalf("PredictorNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d = %q, want %q", i, got[i], want[i])
		}
	}
	if plan.Target() != "spend" {
		t.Errorf("Target() = %q, want %q", plan.Target(), "spend")
	}
}

// TestDummyRecord_NumericValue is the core table: every dummy kind
// against every row shape the acceptance criteria name.
func TestDummyRecord_NumericValue(t *testing.T) {
	schema := dummyTestSchema(t)
	plan, err := newDummyPlan(schema, "spend", []string{"dma", "brandAssets", "age"})
	if err != nil {
		t.Fatalf("newDummyPlan: %v", err)
	}

	tests := []struct {
		name    string
		values  map[string]float64
		nulls   map[string]bool
		wide    map[string]any
		column  string
		wantVal float64
		wantOK  bool
	}{
		{
			name:   "multi-level categorical: the level the row holds",
			values: map[string]float64{"spend": 12, "dma": 1},
			column: "c:3:dma=602", wantVal: 1, wantOK: true,
		},
		{
			name:   "multi-level categorical: a level absent from the row is a measured zero",
			values: map[string]float64{"spend": 12, "dma": 1},
			column: "c:3:dma=501", wantVal: 0, wantOK: true,
		},
		{
			name:   "multi-level categorical: the third level, also absent",
			values: map[string]float64{"spend": 12, "dma": 1},
			column: "c:3:dma=803", wantVal: 0, wantOK: true,
		},
		{
			name:   "null predictor: every level column of that field is missing, not zero",
			values: map[string]float64{"spend": 12, "dma": 0},
			nulls:  map[string]bool{"dma": true},
			column: "c:3:dma=501", wantVal: 0, wantOK: false,
		},
		{
			name:   "unresolvable dictionary id is missing, not zero",
			values: map[string]float64{"spend": 12, "dma": 99},
			column: "c:3:dma=501", wantVal: 0, wantOK: false,
		},
		{
			name:   "null target",
			values: map[string]float64{"spend": 0, "dma": 1},
			nulls:  map[string]bool{"spend": true},
			column: "spend", wantVal: 0, wantOK: false,
		},
		{
			name:   "non-null target passes through",
			values: map[string]float64{"spend": 12.5},
			column: "spend", wantVal: 12.5, wantOK: true,
		},
		{
			name:   "set option selected",
			values: map[string]float64{"spend": 12, "brandAssets": 5},
			wide:   map[string]any{"brandAssets": uint64(0b0101)},
			column: "s:11:brandAssets[premium]", wantVal: 1, wantOK: true,
		},
		{
			name:   "set option not selected is a measured zero",
			values: map[string]float64{"spend": 12, "brandAssets": 5},
			wide:   map[string]any{"brandAssets": uint64(0b0101)},
			column: "s:11:brandAssets[value]", wantVal: 0, wantOK: true,
		},
		{
			name:   "set option at a higher bit",
			values: map[string]float64{"spend": 12, "brandAssets": 5},
			wide:   map[string]any{"brandAssets": uint64(0b0101)},
			column: "s:11:brandAssets[eco]", wantVal: 1, wantOK: true,
		},
		{
			name:   "set option beyond the mask",
			values: map[string]float64{"spend": 12, "brandAssets": 5},
			wide:   map[string]any{"brandAssets": uint64(0b0101)},
			column: "s:11:brandAssets[legacy]", wantVal: 0, wantOK: true,
		},
		{
			name:   "empty set mask is a real all-zero row, not a null",
			values: map[string]float64{"spend": 12, "brandAssets": 0},
			wide:   map[string]any{"brandAssets": uint64(0)},
			column: "s:11:brandAssets[premium]", wantVal: 0, wantOK: true,
		},
		{
			name:   "null set field: no option column may claim a zero",
			values: map[string]float64{"spend": 12, "brandAssets": 0},
			nulls:  map[string]bool{"brandAssets": true},
			wide:   map[string]any{},
			column: "s:11:brandAssets[premium]", wantVal: 0, wantOK: false,
		},
		{
			name:   "set field with no decoded mask is missing",
			values: map[string]float64{"spend": 12},
			column: "s:11:brandAssets[premium]", wantVal: 0, wantOK: false,
		},
		{
			name:   "plain numeric predictor passes through",
			values: map[string]float64{"spend": 12, "age": 41},
			column: "age", wantVal: 41, wantOK: true,
		},
		{
			name:   "null plain numeric predictor",
			values: map[string]float64{"spend": 12, "age": 0},
			nulls:  map[string]bool{"age": true},
			column: "age", wantVal: 0, wantOK: false,
		},
		{
			name:   "unknown column name",
			values: map[string]float64{"spend": 12, "dma": 1},
			column: "c:3:dma=999", wantVal: 0, wantOK: false,
		},
		{
			name:   "a real field that is not in the plan is not readable",
			values: map[string]float64{"spend": 12, "dma": 1},
			column: "dma", wantVal: 0, wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := plan.newRecord()
			nulls := tc.nulls
			if nulls == nil {
				nulls = map[string]bool{}
			}
			rec.bind(tc.values, nulls, tc.wide)
			got, ok := rec.NumericValue(tc.column)
			if ok != tc.wantOK || got != tc.wantVal {
				t.Errorf("NumericValue(%q) = (%v, %v), want (%v, %v)",
					tc.column, got, ok, tc.wantVal, tc.wantOK)
			}
		})
	}
}

// TestDummyRecord_UnboundIsMissing guards the zero value: an adapter
// that has not seen a row must not answer a confident zero.
func TestDummyRecord_UnboundIsMissing(t *testing.T) {
	plan, err := newDummyPlan(dummyTestSchema(t), "spend", []string{"dma"})
	if err != nil {
		t.Fatalf("newDummyPlan: %v", err)
	}
	rec := plan.newRecord()
	for _, name := range append(plan.PredictorNames(), plan.Target()) {
		if v, ok := rec.NumericValue(name); ok || v != 0 {
			t.Errorf("unbound NumericValue(%q) = (%v, %v), want (0, false)", name, v, ok)
		}
	}
}

// TestDummyColumnNames_CollisionSafe is the forging test: a level whose
// literal text contains the delimiters must not be able to name another
// column. The length prefix on the field name is what makes the
// encoding injective.
func TestDummyColumnNames_CollisionSafe(t *testing.T) {
	pairs := []struct{ field, level string }{
		{"dma", "501"},
		{"dma", "501=602"},
		{"dm", "a=501"},
		{"dma=501", ""},
		{"a", "b"},
		{"a=b", ""},
		{"brandAssets", "premium"},
		{"brandAssets", "premium]x"},
		{"brand", "Assets[premium"},
		{"x", "3:y=z"},
	}
	seen := make(map[string]struct{ field, level string })
	for _, p := range pairs {
		for _, name := range []string{
			dummyCategoricalName(p.field, p.level),
			dummySetName(p.field, p.level),
		} {
			if prev, dup := seen[name]; dup {
				t.Errorf("name %q generated for both (%q,%q) and (%q,%q)",
					name, prev.field, prev.level, p.field, p.level)
			}
			seen[name] = struct{ field, level string }{p.field, p.level}
		}
	}
	// A categorical column and a set column can never coincide either.
	if dummyCategoricalName("f", "v") == dummySetName("f", "v") {
		t.Error("categorical and set encodings collide")
	}
}

func TestNewDummyPlan_Rejects(t *testing.T) {
	schema := dummyTestSchema(t)
	tests := []struct {
		name       string
		target     string
		predictors []string
	}{
		{"unknown target", "nope", []string{"dma"}},
		{"categorical target", "dma", []string{"age"}},
		{"set target", "brandAssets", []string{"age"}},
		{"unknown predictor", "spend", []string{"nope"}},
		{"duplicate predictor", "spend", []string{"dma", "dma"}},
		{"target as predictor", "spend", []string{"spend"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := newDummyPlan(schema, tc.target, tc.predictors); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
	if _, err := newDummyPlan(nil, "spend", []string{"dma"}); err == nil {
		t.Error("nil schema: expected an error, got nil")
	}
}

// TestDummyPlan_ViewSchemaDeclaresEveryColumn checks the throwaway view
// schema follows the SyntheticAsCategoricalSchema precedent: every
// column the engine will be driven with is present, declared f64, and
// the real cohort schema is left untouched.
func TestDummyPlan_ViewSchemaDeclaresEveryColumn(t *testing.T) {
	schema := dummyTestSchema(t)
	plan, err := newDummyPlan(schema, "spend", []string{"dma", "brandAssets", "age"})
	if err != nil {
		t.Fatalf("newDummyPlan: %v", err)
	}
	view := plan.viewSchema()
	if got, want := len(view.Fields), len(plan.PredictorNames())+1; got != want {
		t.Fatalf("view schema has %d fields, want %d", got, want)
	}
	if view.Fields[0].Name != "spend" {
		t.Errorf("view schema field 0 = %q, want the target", view.Fields[0].Name)
	}
	for _, name := range append(plan.PredictorNames(), plan.Target()) {
		f := view.Field(name)
		if f == nil {
			t.Fatalf("view schema is missing column %q", name)
		}
		if f.Type != encoding.FieldTypeF64 {
			t.Errorf("column %q declared %s, want f64", name, f.Type)
		}
		if f.Nullable {
			t.Errorf("column %q declared nullable; nullity rides NumericValue's ok return", name)
		}
	}
	if len(schema.Fields) != 4 {
		t.Errorf("view schema construction mutated the real schema: %d fields", len(schema.Fields))
	}
	if schema.Field("dma").Type != encoding.FieldTypeCategoricalU8 {
		t.Error("view schema construction mutated the real schema's dma type")
	}
}

// TestDummyPlan_DrivesShippedOLSEngine is the FR-3 gate: it proves the
// shipped engine accepts predictor names that exist in NO cohort schema
// and fits them correctly, so no hand-rolled normal-equations fallback
// is needed in synth/.
//
// The generating model is exact and noiseless so the recovered
// coefficients are checkable to tolerance:
//
//	spend = 4 + 3·[dma=602] + 7·[dma=803] + 5·[brandAssets has eco] + 0.5·age
//
// dma=501 is the dropped reference level (see newDummyPlan on the dummy
// trap); leaving it in would make the design matrix rank-deficient.
func TestDummyPlan_DrivesShippedOLSEngine(t *testing.T) {
	schema := dummyTestSchema(t)
	plan, err := newDummyPlan(schema, "spend", []string{"dma", "brandAssets", "age"})
	if err != nil {
		t.Fatalf("newDummyPlan: %v", err)
	}
	const (
		colDMA602 = "c:3:dma=602"
		colDMA803 = "c:3:dma=803"
		colEco    = "s:11:brandAssets[eco]"
	)
	spec := &types.RegressionSpec{
		Type:       types.REG_OLS,
		Name:       "spend_model",
		Target:     plan.Target(),
		Predictors: []string{colDMA602, colDMA803, colEco, "age"},
	}

	engines, err := regression.BuildStreaming([]*types.RegressionSpec{spec}, plan.viewSchema())
	if err != nil {
		t.Fatalf("BuildStreaming rejected dummy predictors: %v", err)
	}
	if len(engines) != 1 {
		t.Fatalf("BuildStreaming returned %d engines, want 1", len(engines))
	}

	rec := plan.newRecord()
	values := make(map[string]float64, 4)
	nulls := make(map[string]bool, 4)
	wide := make(map[string]any, 1)

	// Rows walk every dma level crossed with an eco-selected and an
	// eco-unselected mask, plus varying age, so no column is collinear.
	type row struct {
		dmaID   float64
		mask    uint64
		age     float64
		dropped bool // null somewhere ⇒ listwise-deleted, must not shift the fit
	}
	rows := []row{
		{dmaID: 0, mask: 0b0000, age: 20},
		{dmaID: 0, mask: 0b0100, age: 31},
		{dmaID: 1, mask: 0b0000, age: 44},
		{dmaID: 1, mask: 0b0100, age: 57},
		{dmaID: 2, mask: 0b0001, age: 62},
		{dmaID: 2, mask: 0b0110, age: 73},
		{dmaID: 0, mask: 0b1000, age: 29},
		{dmaID: 2, mask: 0b0100, age: 38},
		{dmaID: 1, mask: 0b0101, age: 51},
		{dmaID: 0, mask: 0b0110, age: 66},
		{dmaID: 0, mask: 0b0000, age: 25, dropped: true},
		{dmaID: 1, mask: 0b0100, age: 35, dropped: true},
	}
	const (
		wantIntercept = 4.0
		want602       = 3.0
		want803       = 7.0
		wantEco       = 5.0
		wantAge       = 0.5
	)
	contributing := 0
	for i, r := range rows {
		clear(values)
		clear(nulls)
		clear(wide)
		values["dma"] = r.dmaID
		values["age"] = r.age
		values["brandAssets"] = float64(r.mask)
		wide["brandAssets"] = r.mask

		y := wantIntercept + wantAge*r.age
		if r.dmaID == 1 {
			y += want602
		}
		if r.dmaID == 2 {
			y += want803
		}
		if r.mask&0b0100 != 0 {
			y += wantEco
		}
		values["spend"] = y

		switch {
		case r.dropped && i%2 == 0:
			// Null TARGET: the row carries perfectly good predictors and
			// must still drop out entirely.
			values["spend"] = 0
			nulls["spend"] = true
		case r.dropped:
			// Null PREDICTOR: dma is unknown, so no level column may
			// contribute a zero and the row must drop out.
			values["dma"] = 0
			nulls["dma"] = true
			delete(wide, "brandAssets")
			nulls["brandAssets"] = true
		default:
			contributing++
		}

		rec.bind(values, nulls, wide)
		if err := engines[0].UpdateRow(rec); err != nil {
			t.Fatalf("UpdateRow(row %d): %v", i, err)
		}
	}

	res, err := engines[0].Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.NObs != contributing {
		t.Errorf("NObs = %d, want %d (null rows must be listwise-deleted)", res.NObs, contributing)
	}
	want := map[string]float64{
		regression.InterceptKey: wantIntercept,
		colDMA602:               want602,
		colDMA803:               want803,
		colEco:                  wantEco,
		"age":                   wantAge,
	}
	for name, w := range want {
		got, ok := res.Coefficients[name]
		if !ok {
			t.Errorf("no coefficient for %q; got %v", name, res.Coefficients)
			continue
		}
		if math.Abs(got-w) > 1e-6 {
			t.Errorf("coefficient %q = %v, want %v", name, got, w)
		}
	}
}
