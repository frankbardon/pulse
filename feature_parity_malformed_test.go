package pulse

// Malformed-request cells of the hidden-name parity harness
// (feature_parity_harness_test.go). The registry categories drive
// well-formed requests, so a rule that runs BEFORE the name is looked
// up — and judges the request by that name — is only reached by a
// request the rule refuses: a `tz` where the operator cannot take one,
// parameter keys naming missing fields, a numeric aggregation over a
// categorical field under strict mode, a decimal128 field, an
// additive_fields scope over an expression filter. Each category below
// is such a request; the harness requires the hidden name to come out
// byte-identical to a never-registered one on every entry point,
// Predict included, and differently from the same name unprofiled.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// parityDecimalCohort is the parity cohort plus a decimal128 column.
const parityDecimalCohort = "parity_decimal.pulse"

// writeParityDecimalCohort lays out region (categorical), age (f64) and
// amount (decimal128, scale 2) by hand: CSV import cannot infer a
// decimal128.
func writeParityDecimalCohort(t *testing.T, fsys afero.Fs) {
	t.Helper()
	region := encoding.NewDictionary()
	for _, r := range []string{"north", "south", "east", "west"} {
		if _, err := region.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: region},
		{Name: "age", Type: encoding.FieldTypeF64, ByteOffset: 1},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, ByteOffset: 9, Precision: 18, Scale: 2},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		rec := make([]byte, schema.RecordByteSize())
		rec[0] = byte(i % 4)
		binary.LittleEndian.PutUint64(rec[1:9], math.Float64bits(float64(10+(i*7)%53)))
		enc := encoding.EncodeDecimal128(encoding.NewDecimal128FromInt(int64(100 + (i*37)%900)))
		copy(rec[9:], enc[:])
		buf.Write(rec)
	}
	if err := afero.WriteFile(fsys, parityDecimalCohort, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func parityMalformedFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := parityFS(t)
	writeParityDecimalCohort(t, fsys)
	return fsys
}

// malformedParityCategories run on the default parity host.
var malformedParityCategories = []parityCategory{
	{
		// A zone-capable grouper with `tz` over a non-temporal field:
		// the zone rule refuses the field, or — for a name that is not
		// zone-capable here — the `tz` itself.
		name:       "zone_tz_grouper",
		candidates: []string{"GROUP_DATE", "GROUP_DATE_RANGES"},
		never:      "GROUP_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups = append([]*types.Group{{Type: types.GroupType(op), Field: f.num, TimeZone: "UTC"}}, r.Groups...)
		},
	},
	{
		// An unloadable zone name on a zone-capable grouper.
		name:       "zone_tz_unknown_zone",
		candidates: []string{"GROUP_DATE", "GROUP_DATE_RANGES"},
		never:      "GROUP_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups = append([]*types.Group{{Type: types.GroupType(op), Field: f.num, TimeZone: "Not/AZone"}}, r.Groups...)
		},
	},
	{
		// A zone-capable filterer with `tz` over a non-temporal field.
		name:       "zone_tz_filterer",
		candidates: []string{"FILTER_DATE_RANGES"},
		never:      "FILTER_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FiltererType(op), Field: f.num, TimeZone: "UTC"})
		},
		facet: func(r *types.FacetRequest, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FiltererType(op), Field: f.num, TimeZone: "UTC"})
		},
	},
	{
		// A filterer with no Field: the field-reference walk requires
		// one only of the filter types that read a field.
		name:       "filterer_empty_field",
		candidates: []string{"FILTER_EXCLUDE", "FILTER_INCLUDE"},
		never:      "FILTER_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FiltererType(op), Values: []string{"south"}})
		},
		facet: func(r *types.FacetRequest, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FiltererType(op), Values: []string{"south"}})
		},
		filterToFile: func(r *FilterToFileRequest, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FiltererType(op), Values: []string{"south"}})
		},
	},
	{
		// Parameter keys the field-reference walk reads, naming fields
		// the cohort lacks.
		name:       "param_fields_missing",
		candidates: []string{"AGG_RATIO", "AGG_WEIGHTED_MEAN", "AGG_DISTINCT_SUM"},
		never:      "AGG_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AggregationType(op), Field: f.num, Label: "probe",
				Params: json.RawMessage(`{"numerator_field":"nope","denominator_field":"nope","weight_field":"nope","distinct_by":"nope"}`)})
		},
	},
	{
		// The same keys with values of the wrong JSON type.
		name:       "param_fields_wrong_type",
		candidates: []string{"AGG_RATIO", "AGG_WEIGHTED_MEAN", "AGG_DISTINCT_SUM"},
		never:      "AGG_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AggregationType(op), Field: f.num, Label: "probe",
				Params: json.RawMessage(`{"numerator_field":5,"denominator_field":5,"weight_field":5,"distinct_by":5}`)})
		},
	},
	{
		// Feature parameter keys naming missing fields.
		name:       "feature_param_fields_missing",
		candidates: []string{"FEAT_TARGET_ENCODE", "FEAT_TRAIN_TEST_SPLIT"},
		never:      "FEAT_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Features = append(r.Features, &types.Feature{Type: types.FeatureType(op), Field: f.cat, Label: "probe",
				Params: json.RawMessage(`{"target":"nope","stratify":"nope","ratios":[0.5,0.5]}`)})
		},
	},
	{
		// Post-test parameter keys naming missing output columns.
		name:       "post_test_param_fields_missing",
		candidates: []string{"TEST_ANOVA_F", "TEST_ANOVA_WELCH"},
		never:      "TEST_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.PostTests = append(r.PostTests, &types.Test{Type: types.TestType(op), Field: "n", SplitBy: f.cat, Label: "probe",
				Params: json.RawMessage(`{"n_col":"nope","variance_col":"nope"}`)})
		},
	},
	{
		// An expression filter in a FacetSchema whose additive_fields
		// scope names a field the expression reads: the facet refuses
		// the scope before the filter is built.
		name:       "facet_additive_expression",
		candidates: []string{"FILTER_EXPRESSION"},
		never:      "FILTER_NEVER_REGISTERED",
		facet: func(r *types.FacetRequest, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FiltererType(op), Expression: f.cat + ` == "south"`})
			r.AdditiveFields = append(r.AdditiveFields, f.cat)
		},
	},
	{
		// A formula attribute on a crosstab: the fused gate bails on
		// ATTR_FORMULA by name before the attribute is resolved.
		name:       "crosstab_formula_attribute",
		candidates: []string{"ATTR_FORMULA"},
		never:      "ATTR_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups, r.Aggregations = nil, nil
			r.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: f.num},
			}
			r.Attributes = append(r.Attributes, &types.Attribute{Type: types.AttributeType(op), Field: f.num, Label: "probe", Expression: f.num + " * 2"})
		},
	},
	{
		// An expression filter on a crosstab (the fused gate bails on
		// FILTER_EXPRESSION by name too).
		name:       "crosstab_expression_filter",
		candidates: []string{"FILTER_EXPRESSION"},
		never:      "FILTER_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups, r.Aggregations = nil, nil
			r.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: f.num},
			}
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FiltererType(op), Expression: f.num + " > 20"})
		},
	},
}

// strictParityCategories run on a Strict host: a numeric aggregation
// over a categorical field is refused by the strict categorical rule.
var strictParityCategories = []parityCategory{
	{
		name:       "strict_numeric_on_categorical",
		candidates: []string{"AGG_AVERAGE", "AGG_MAX"},
		never:      "AGG_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AggregationType(op), Field: f.cat, Label: "probe"})
		},
	},
	{
		name:       "strict_numeric_cell_on_categorical",
		candidates: []string{"AGG_AVERAGE", "AGG_MAX"},
		never:      "AGG_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups, r.Aggregations = nil, nil
			r.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Cell:    &types.Aggregation{Type: types.AggregationType(op), Field: f.cat},
			}
		},
	},
}

// decimalParityCategories run on the decimal cohort, num = amount: the
// buffered orchestrators dispatch a decimal128 field to the built-in
// decimal fold by aggregator name, ahead of the registry lookup.
var decimalParityCategories = []parityCategory{
	{
		name:       "decimal_aggregator",
		candidates: []string{"AGG_MAX", "AGG_MIN"},
		never:      "AGG_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AggregationType(op), Field: f.num, Label: "probe"})
		},
	},
	{
		name:       "decimal_cell",
		candidates: []string{"AGG_MAX", "AGG_MIN"},
		never:      "AGG_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups, r.Aggregations = nil, nil
			r.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Cell:    &types.Aggregation{Type: types.AggregationType(op), Field: f.num},
			}
		},
	},
}

// malformedVacuous are the by-design vacuous malformed cells, per entry
// point: the chain stage gate excludes feature / post-test / crosstab
// slots for every name, and Predict returns the result without the
// envelope's errors, so two names that are both refused (valid=false)
// predict the same there — PredictBytes compares the errors.
var malformedVacuous = map[string]map[string]string{
	"ProcessChain/stage0": {
		"feature_param_fields_missing":       chainExcluded,
		"post_test_param_fields_missing":     chainExcluded,
		"crosstab_formula_attribute":         "a crosstab stage has no aggregator slot, so the chain stage gate refuses it for every name",
		"crosstab_expression_filter":         "a crosstab stage has no aggregator slot, so the chain stage gate refuses it for every name",
		"strict_numeric_cell_on_categorical": "a crosstab stage has no aggregator slot, so the chain stage gate refuses it for every name",
		"decimal_cell":                       "a crosstab stage has no aggregator slot, so the chain stage gate refuses it for every name",
	},
	"Predict": {
		"post_test_param_fields_missing":     "Predict returns no envelope errors; PredictBytes compares them",
		"strict_numeric_cell_on_categorical": "Predict returns no envelope errors; PredictBytes compares them",
		"decimal_cell":                       predictDecimalCellVacuous,
	},
	"PredictBytes": {
		"decimal_cell": predictDecimalCellVacuous,
	},
}

// predictDecimalCellVacuous: predict judges no crosstab cell aggregator
// over a decimal field by name (it predicts a never-registered cell
// valid too), so every name predicts the same there; the runtime cells
// carry the rule.
const predictDecimalCellVacuous = "predict applies no name-keyed rule to a decimal crosstab cell"

// malformedEntryPoints is parityEntryPoints with malformedVacuous merged
// into each entry point's vacuousOK.
func malformedEntryPoints() []parityEntryPoint {
	out := make([]parityEntryPoint, len(parityEntryPoints))
	for i, ep := range parityEntryPoints {
		key := ep.name
		// PredictChain/stage0 runs the same chain stage gate.
		if key == "ProcessChain/stage1" || key == "PredictChain/stage0" {
			key = "ProcessChain/stage0"
		}
		extra := malformedVacuous[key]
		if len(extra) > 0 {
			merged := map[string]string{}
			for k, v := range ep.vacuousOK {
				merged[k] = v
			}
			for k, v := range extra {
				merged[k] = v
			}
			ep.vacuousOK = merged
		}
		out[i] = ep
	}
	return out
}

// TestHiddenOperatorMalformedParity: on malformed requests — the ones
// a pre-lookup rule refuses — a hidden built-in is still
// indistinguishable from a never-registered name on every entry point,
// Predict included.
func TestHiddenOperatorMalformedParity(t *testing.T) {
	fixtures := append([]string(nil), featureSetFixtures...)
	sort.Strings(fixtures)
	fsys := parityMalformedFS(t)
	t.Run("default", func(t *testing.T) {
		runHiddenParityWith(t, fsys, parityHostConfig{}, fixtures, malformedParityCategories, malformedEntryPoints())
	})
	t.Run("strict", func(t *testing.T) {
		runHiddenParityWith(t, fsys, parityHostConfig{options: func(o *Options) { o.Strict = true }},
			fixtures, strictParityCategories, malformedEntryPoints())
	})
	t.Run("decimal", func(t *testing.T) {
		runHiddenParityWith(t, fsys, parityHostConfig{cohort: parityDecimalCohort, fields: parityFields{num: "amount", cat: "region"}},
			fixtures, decimalParityCategories, malformedEntryPoints())
	})
}
