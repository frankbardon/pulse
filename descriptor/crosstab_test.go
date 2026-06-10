package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func crosstabPredictSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8,
				Description: "Region categorical identifier dimension",
				Dictionary:  makeDictionary(t, "north", "south")},
			{Name: "segment", Type: encoding.FieldTypeCategoricalU8,
				Description: "Customer segment identifier dimension",
				Dictionary:  makeDictionary(t, "retail", "wholesale")},
			{Name: "value", Type: encoding.FieldTypeF64,
				Description: "Numeric revenue value field for analytics"},
		},
	}
}

// TestPredict_Crosstab_MatrixForcesBuffered verifies the buffered gate:
// shape=matrix → non-streamable.
func TestPredict_Crosstab_MatrixForcesBuffered(t *testing.T) {
	schema := crosstabPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Fatal("crosstab shape=matrix should be non-streamable")
	}
	if !containsSubstring(result.StreamableReasons, "matrix") {
		t.Errorf("expected matrix in StreamableReasons; got %v", result.StreamableReasons)
	}
}

// TestPredict_Crosstab_LongNoMarginsStreamable verifies the lone
// streamable case: shape=long, no margins, normalize=none.
func TestPredict_Crosstab_LongNoMarginsStreamable(t *testing.T) {
	schema := crosstabPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value"},
			Shape:   types.CrosstabShapeLong,
		},
	}
	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if !result.Valid {
		t.Fatalf("expected valid result; errors=%v", env.Errors)
	}
	if !result.Streamable {
		t.Errorf("crosstab shape=long, no margins, normalize=none should be streamable; reasons=%v",
			result.StreamableReasons)
	}
}

// TestPredict_Crosstab_NormalizeForcesBuffered verifies normalize != none
// forces buffered.
func TestPredict_Crosstab_NormalizeForcesBuffered(t *testing.T) {
	schema := crosstabPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "value"},
			Shape:     types.CrosstabShapeLong,
			Normalize: types.CrosstabNormalizeRow,
		},
	}
	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("normalize=row should force buffered")
	}
}

// TestPredict_Crosstab_EmptyAxisRejected verifies structural validators.
func TestPredict_Crosstab_EmptyAxisRejected(t *testing.T) {
	schema := crosstabPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value"},
		},
	}
	env := PredictFromBytes(data, req, nil)
	if !envHasCode(env, errors.PULSE_CROSSTAB_EMPTY_ROWS) {
		t.Errorf("expected PULSE_CROSSTAB_EMPTY_ROWS; got %v", env.Errors)
	}
}

// TestPredict_Crosstab_NestedAxisBuffered verifies multi-grouper axis
// forces buffered (even shape=long).
func TestPredict_Crosstab_NestedAxisBuffered(t *testing.T) {
	schema := crosstabPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value"},
			Shape:   types.CrosstabShapeLong,
		},
	}
	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("nested axes (2 row groupers) should force buffered")
	}
}

// TestManifest_CrosstabCapabilityPopulated verifies the manifest carries
// a populated Crosstab block.
func TestManifest_CrosstabCapabilityPopulated(t *testing.T) {
	m := BuildManifest()
	if m.Crosstab.Name != "crosstab" {
		t.Errorf("manifest.crosstab.name = %q, want crosstab", m.Crosstab.Name)
	}
	if len(m.Crosstab.NormalizeModes) != 4 {
		t.Errorf("normalize modes = %d, want 4", len(m.Crosstab.NormalizeModes))
	}
	if len(m.Crosstab.Shapes) != 2 {
		t.Errorf("shapes = %d, want 2", len(m.Crosstab.Shapes))
	}
	// Spot-check membership.
	total := len(m.Crosstab.SummableAggregators) + len(m.Crosstab.MeanReducibleAggregators) + len(m.Crosstab.RecomputeAggregators)
	if total != len(types.AllAggregationTypes()) {
		t.Errorf("classified %d aggregators, want %d", total, len(types.AllAggregationTypes()))
	}
}

// TestPredict_Crosstab_NormalizeLevelGate exercises the three rejection
// branches added by the NormalizeLevel slot.
func TestPredict_Crosstab_NormalizeLevelGate(t *testing.T) {
	schema := crosstabPredictSchema(t)
	data := buildTestPulseFile(t, schema)

	mkReq := func(mode types.CrosstabNormalize, columns []*types.Group, level *int) *types.Request {
		return &types.Request{
			Crosstab: &types.CrosstabSpec{
				Rows:           []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns:        columns,
				Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "value"},
				Normalize:      mode,
				NormalizeLevel: level,
			},
		}
	}

	// Out of range: 2-grouper column axis, level=2.
	{
		bad := 2
		env := PredictFromBytes(data, mkReq(types.CrosstabNormalizeColumn,
			[]*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			}, &bad), nil)
		if !hasErrorCode(env, errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE) {
			t.Errorf("expected PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE; got errors=%v", env.Errors)
		}
	}

	// Without nested axis: normalize=none + level set.
	{
		level := 0
		env := PredictFromBytes(data, mkReq(types.CrosstabNormalizeNone,
			[]*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}}, &level), nil)
		if !hasErrorCode(env, errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS) {
			t.Errorf("expected PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS; got errors=%v", env.Errors)
		}
	}

	// Incompatible: normalize=total + level set.
	{
		level := 0
		env := PredictFromBytes(data, mkReq(types.CrosstabNormalizeTotal,
			[]*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}}, &level), nil)
		if !hasErrorCode(env, errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE) {
			t.Errorf("expected PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE; got errors=%v", env.Errors)
		}
	}

	// Valid: normalize=column on 2-grouper axis, level=0 (top). No errors.
	{
		level := 0
		env := PredictFromBytes(data, mkReq(types.CrosstabNormalizeColumn,
			[]*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
				{Type: types.GROUP_CATEGORY, Field: "segment"},
			}, &level), nil)
		if hasErrorCode(env, errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE) ||
			hasErrorCode(env, errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS) ||
			hasErrorCode(env, errors.PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE) {
			t.Errorf("valid normalize_level should not raise level errors; got %v", env.Errors)
		}
	}
}

// TestManifest_CrosstabCapabilityNormalizeLevel verifies the manifest
// capability block surfaces the new NormalizeLevel feature flag and
// its rejection rules.
func TestManifest_CrosstabCapabilityNormalizeLevel(t *testing.T) {
	m := BuildManifest()
	if !m.Crosstab.SupportsNormalizeLevel {
		t.Error("manifest.Crosstab.SupportsNormalizeLevel should be true")
	}
	if len(m.Crosstab.NormalizeLevelRules) != 3 {
		t.Errorf("NormalizeLevelRules len = %d, want 3", len(m.Crosstab.NormalizeLevelRules))
	}
	mustContain := []string{
		"PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE",
		"PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS",
		"PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE",
	}
	for _, code := range mustContain {
		if !containsSubstring(m.Crosstab.NormalizeLevelRules, code) {
			t.Errorf("NormalizeLevelRules missing %s; got %v", code, m.Crosstab.NormalizeLevelRules)
		}
	}
}

func hasErrorCode(env *Envelope, code errors.Code) bool {
	for _, e := range env.Errors {
		if e.Code == string(code) {
			return true
		}
	}
	return false
}

func containsSubstring(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
