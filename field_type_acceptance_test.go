package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// acceptanceFieldTypes is every registered field type, in type-byte
// order; the cohort carries one column per type, named t_<type>. The
// vector types are included: a vec_f32 / vec_f64 column (dim 2) is
// judged by every operator like any other type.
func acceptanceFieldTypes() []encoding.FieldType {
	var out []encoding.FieldType
	for b := 0; b < 256; b++ {
		if ft := encoding.FieldType(b); ft.IsKnown() {
			out = append(out, ft)
		}
	}
	return out
}

// acceptanceVecDim is the dim of every vector column in the acceptance
// cohort.
const acceptanceVecDim = 2

// acceptanceVecCell is row i's value for a vector column of type ft:
// dim acceptanceVecDim, varying per row so a matrix over it is not
// singular.
func acceptanceVecCell(ft encoding.FieldType, i int) any {
	v := []float64{1 + float64((i*7)%50)*0.5, float64((i*11)%13) + 0.25}
	if ft == encoding.FieldTypeVecF64 {
		return v
	}
	return []float32{float32(v[0]), float32(v[1])}
}

// acceptanceCell renders row i's value for a column of type ft.
func acceptanceCell(ft encoding.FieldType, i int) string {
	switch {
	case ft == encoding.FieldTypeU4:
		return fmt.Sprint(i % 16)
	case ft == encoding.FieldTypeU8, ft == encoding.FieldTypeU16, ft == encoding.FieldTypeU32, ft == encoding.FieldTypeU64:
		return fmt.Sprint(1 + (i*7)%50)
	case ft == encoding.FieldTypeF32, ft == encoding.FieldTypeF64:
		return fmt.Sprintf("%.2f", 1.25+float64((i*7)%50)*1.5)
	case ft == encoding.FieldTypeDecimal128:
		return fmt.Sprintf("%d.%02d", 1+(i*7)%50, i%100)
	case ft == encoding.FieldTypeDate:
		return fmt.Sprintf("2024-%02d-%02d", 1+i%12, 1+(i*3)%28)
	case ft == encoding.FieldTypeDateTime:
		return fmt.Sprintf("2024-%02d-%02dT%02d:15:00Z", 1+i%12, 1+(i*3)%28, i%24)
	case ft == encoding.FieldTypePackedBool:
		return []string{"true", "false", "false"}[i%3]
	case ft.IsCategorical():
		return []string{"a", "b"}[(i/2)%2]
	case ft.IsSet():
		return []string{"a|b", "b", "a|c", "c|b"}[i%4]
	}
	panic("unhandled field type " + ft.String())
}

// acceptanceCohort imports a cohort with one column per field type plus
// the helper columns the operator templates reference: x / y (f64),
// g (two-level categorical, a split_by) and subj (an ANOVA_RM subject).
func acceptanceCohort(t *testing.T) (*pulse.Pulse, afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	// The scalar columns import from CSV (the import path owns their
	// dictionaries, decimals and temporal parsing); the vector columns,
	// which have no CSV spelling yet, are appended by rebuilding that
	// cohort with a CohortBuilder: every imported row reads back
	// unchanged and gains its vector cells.
	var fields []encoding.Field
	var scalarTypes, vecTypes []encoding.FieldType
	for _, ft := range acceptanceFieldTypes() {
		if ft.IsVector() {
			vecTypes = append(vecTypes, ft)
		} else {
			scalarTypes = append(scalarTypes, ft)
		}
	}
	add := func(name string, ft encoding.FieldType) {
		f := encoding.Field{Name: name, Type: ft, CsvColumnIdx: len(fields)}
		if ft.HasDictionary() {
			f.Dictionary = encoding.NewDictionary()
		}
		if ft.IsDecimal() {
			f.Precision, f.Scale = 18, 2
		}
		fields = append(fields, f)
	}
	for _, ft := range scalarTypes {
		add("t_"+ft.String(), ft)
	}
	add("x", encoding.FieldTypeF64)
	add("y", encoding.FieldTypeF64)
	add("g", encoding.FieldTypeCategoricalU8)
	add("subj", encoding.FieldTypeCategoricalU8)
	off := 0
	for i := range fields {
		fields[i].ByteOffset = off
		if fields[i].Type.IsBitPacked() {
			off++
		} else {
			off += fields[i].Type.ByteSize()
		}
	}
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(f.Name)
	}
	b.WriteByte('\n')
	const rows = 48
	for i := range rows {
		for _, ft := range scalarTypes {
			b.WriteString(acceptanceCell(ft, i))
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%.3f,%.3f,%s,s%d\n", float64(i)*1.1+float64(i%5), float64((i*13)%17)+0.5, []string{"a", "b"}[i%2], i/2)
	}
	if err := afero.WriteFile(fs, "acc.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	job := &pio.ImportJob{Source: csv.NewReader(fs, "acc.csv"), Target: "acc_scalar.pulse", Schema: &encoding.Schema{Fields: fields}, FS: fs}
	if _, err := p.Import(ctx, job); err != nil {
		t.Fatalf("import: %v", err)
	}
	scalar, err := p.Open(ctx, "acc_scalar.pulse")
	if err != nil {
		t.Fatal(err)
	}
	rd, err := scalar.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	schema := encoding.Schema{Fields: append([]encoding.Field(nil), rd.Schema().Fields...)}
	for _, ft := range vecTypes {
		schema.Vectors = append(schema.Vectors, encoding.VectorField{FieldIndex: len(schema.Fields), Dim: acceptanceVecDim})
		schema.Fields = append(schema.Fields, encoding.Field{Name: "t_" + ft.String(), Type: ft})
	}
	bld, err := p.NewCohortBuilder(ctx, "acc.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range int64(rd.Len()) {
		row, err := rd.RecordAt(i)
		if err != nil {
			t.Fatal(err)
		}
		for _, ft := range vecTypes {
			row = append(row, acceptanceVecCell(ft, int(i)))
		}
		if err := bld.Append(row); err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
	}
	if _, err := bld.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fs, "acc.pulse"
}

// acceptanceTemplates holds, per registered operator, a minimal request
// with $F where the field under judgement goes. The other names are the
// helper columns. Every template is accepted by both sides for at least
// one field type (asserted below), so a refusal is the type's, not a
// malformed slot's.
var acceptanceTemplates = map[string]string{
	// Aggregations.
	"AGG_COUNT":          `{"aggregations":[{"type":"AGG_COUNT","field":"$F"}]}`,
	"AGG_SUM":            `{"aggregations":[{"type":"AGG_SUM","field":"$F"}]}`,
	"AGG_AVERAGE":        `{"aggregations":[{"type":"AGG_AVERAGE","field":"$F"}]}`,
	"AGG_MIN":            `{"aggregations":[{"type":"AGG_MIN","field":"$F"}]}`,
	"AGG_MAX":            `{"aggregations":[{"type":"AGG_MAX","field":"$F"}]}`,
	"AGG_STDDEV":         `{"aggregations":[{"type":"AGG_STDDEV","field":"$F"}]}`,
	"AGG_RANGE":          `{"aggregations":[{"type":"AGG_RANGE","field":"$F"}]}`,
	"AGG_MODE_COUNT":     `{"aggregations":[{"type":"AGG_MODE_COUNT","field":"$F"}]}`,
	"AGG_FREQUENCY":      `{"aggregations":[{"type":"AGG_FREQUENCY","field":"$F","params":{"value":"$V"}}]}`,
	"AGG_ZSCORE":         `{"aggregations":[{"type":"AGG_ZSCORE","field":"$F"}]}`,
	"AGG_MEDIAN":         `{"aggregations":[{"type":"AGG_MEDIAN","field":"$F"}]}`,
	"AGG_VARIANCE":       `{"aggregations":[{"type":"AGG_VARIANCE","field":"$F"}]}`,
	"AGG_MODE":           `{"aggregations":[{"type":"AGG_MODE","field":"$F"}]}`,
	"AGG_SKEWNESS":       `{"aggregations":[{"type":"AGG_SKEWNESS","field":"$F"}]}`,
	"AGG_KURTOSIS":       `{"aggregations":[{"type":"AGG_KURTOSIS","field":"$F"}]}`,
	"AGG_DISTINCT_COUNT": `{"aggregations":[{"type":"AGG_DISTINCT_COUNT","field":"$F"}]}`,
	"AGG_PERCENTILE":     `{"aggregations":[{"type":"AGG_PERCENTILE","field":"$F","params":{"percentile":90}}]}`,
	"AGG_NULL_COUNT":     `{"aggregations":[{"type":"AGG_NULL_COUNT","field":"$F"}]}`,
	"AGG_WEIGHTED_MEAN":  `{"aggregations":[{"type":"AGG_WEIGHTED_MEAN","field":"$F","params":{"weight_field":"y"}}]}`,
	"AGG_RATIO":          `{"aggregations":[{"type":"AGG_RATIO","field":"$F","params":{"numerator_field":"$F","denominator_field":"y"}}]}`,
	"AGG_DISTINCT_SUM":   `{"aggregations":[{"type":"AGG_DISTINCT_SUM","field":"$F","params":{"distinct_by":"subj"}}]}`,
	// Model error: the field under judgement is the actual; the paired
	// prediction column (params.prediction_field, the same type check)
	// is pinned by TestModelError_PredictMatchesRuntime.
	"AGG_MAPE": `{"aggregations":[{"type":"AGG_MAPE","field":"$F","params":{"prediction_field":"y"}}]}`,
	"AGG_RMSE": `{"aggregations":[{"type":"AGG_RMSE","field":"$F","params":{"prediction_field":"y"}}]}`,
	"AGG_MAE":  `{"aggregations":[{"type":"AGG_MAE","field":"$F","params":{"prediction_field":"y"}}]}`,
	// AGG_AUC's template judges the score; the outcome column is pinned
	// by TestModelError_PredictionFieldTypeParity.
	"AGG_AUC":                 `{"aggregations":[{"type":"AGG_AUC","field":"$F","params":{"outcome":"t_packed_bool"}}]}`,
	"AGG_CI_LOWER":            `{"aggregations":[{"type":"AGG_CI_LOWER","field":"$F","params":{"confidence":0.95,"method":"normal"}}]}`,
	"AGG_CI_UPPER":            `{"aggregations":[{"type":"AGG_CI_UPPER","field":"$F","params":{"confidence":0.95,"method":"normal"}}]}`,
	"AGG_WELFORD":             `{"aggregations":[{"type":"AGG_WELFORD","field":"$F"}]}`,
	"AGG_SET_UNION":           `{"aggregations":[{"type":"AGG_SET_UNION","field":"$F"}]}`,
	"AGG_SET_INTERSECTION":    `{"aggregations":[{"type":"AGG_SET_INTERSECTION","field":"$F"}]}`,
	"AGG_SET_FREQUENCY":       `{"aggregations":[{"type":"AGG_SET_FREQUENCY","field":"$F"}]}`,
	"AGG_SET_CARDINALITY_SUM": `{"aggregations":[{"type":"AGG_SET_CARDINALITY_SUM","field":"$F"}]}`,
	"AGG_SET_CARDINALITY_AVG": `{"aggregations":[{"type":"AGG_SET_CARDINALITY_AVG","field":"$F"}]}`,
	"AGG_SET_DISTINCT_VALUES": `{"aggregations":[{"type":"AGG_SET_DISTINCT_VALUES","field":"$F"}]}`,
	"AGG_VEC_MEAN":            `{"aggregations":[{"type":"AGG_VEC_MEAN","fields":["$F","x"]}]}`,
	"AGG_VEC_SUM":             `{"aggregations":[{"type":"AGG_VEC_SUM","fields":["$F","x"]}]}`,
	"AGG_VEC_SD":              `{"aggregations":[{"type":"AGG_VEC_SD","fields":["$F","x"]}]}`,

	// Attributes (aggregated afterwards so the label is consumed).
	"ATTR_ZSCORE":     `{"attributes":[{"type":"ATTR_ZSCORE","field":"$F","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_TSCORE":     `{"attributes":[{"type":"ATTR_TSCORE","field":"$F","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_NORMALIZED": `{"attributes":[{"type":"ATTR_NORMALIZED","field":"$F","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_FORMULA":    `{"attributes":[{"type":"ATTR_FORMULA","field":"$F","label":"o","expression":"x * 2"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_PERCENTILE": `{"attributes":[{"type":"ATTR_PERCENTILE","field":"$F","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_DATE_PART":  `{"attributes":[{"type":"ATTR_DATE_PART","field":"$F","label":"o","params":{"part":"year"}}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_REG_FITTED": `{"attributes":[{"type":"ATTR_REG_FITTED","target":"x","predictors":["$F"],"label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	// Scores a model bound by a compose fit, which a plain request never
	// has; its predictor-type parity is pinned by
	// TestRegPredict_PredictRuntimeParity.
	"ATTR_REG_PREDICT":        ``,
	"ATTR_REG_RESIDUAL":       `{"attributes":[{"type":"ATTR_REG_RESIDUAL","target":"x","predictors":["$F"],"label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_REG_LEVERAGE":       `{"attributes":[{"type":"ATTR_REG_LEVERAGE","target":"x","predictors":["$F"],"label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_SET_POPCOUNT":       `{"attributes":[{"type":"ATTR_SET_POPCOUNT","field":"$F","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_SET_HAS":            `{"attributes":[{"type":"ATTR_SET_HAS","field":"$F","label":"o","params":{"label":"a"}}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_CODE_IN":            `{"attributes":[{"type":"ATTR_CODE_IN","field":"$F","label":"o","params":{"codes":["$V"]}}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_MAHALANOBIS":        `{"attributes":[{"type":"ATTR_MAHALANOBIS","fields":["$F","x"],"label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_FACTOR_SCORE":       `{"attributes":[{"type":"ATTR_FACTOR_SCORE","fields":["$F","x","y"],"label":"o","params":{"factor":1,"factors":1}}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_PC_SCORE":           `{"attributes":[{"type":"ATTR_PC_SCORE","fields":["$F","x"],"label":"o","params":{"component":1}}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_SCALE_SCORE":        `{"attributes":[{"type":"ATTR_SCALE_SCORE","fields":["$F","x"],"label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_PROFILE_SIMILARITY": `{"attributes":[{"type":"ATTR_PROFILE_SIMILARITY","fields":["$F","x"],"label":"o","params":{"metric":"cosine","reference":[1,2]}}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_VEC_DISTANCE":       `{"attributes":[{"type":"ATTR_VEC_DISTANCE","fields":["$F","x"],"label":"o","params":{"metric":"euclidean","reference":[1,2]}}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,

	// Features.
	"FEAT_LOG":              `{"features":[{"type":"FEAT_LOG","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FEAT_SQRT":             `{"features":[{"type":"FEAT_SQRT","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FEAT_BUCKETIZE":        `{"features":[{"type":"FEAT_BUCKETIZE","field":"$F","params":{"quantiles":4}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FEAT_ONE_HOT":          `{"features":[{"type":"FEAT_ONE_HOT","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FEAT_DATE_FEATURES":    `{"features":[{"type":"FEAT_DATE_FEATURES","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FEAT_FREQUENCY_ENCODE": `{"features":[{"type":"FEAT_FREQUENCY_ENCODE","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FEAT_TARGET_ENCODE":    `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"$F","params":{"target":"x","smoothing":5}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FEAT_TRAIN_TEST_SPLIT": `{"features":[{"type":"FEAT_TRAIN_TEST_SPLIT","params":{"ratios":[0.7,0.3],"seed":42,"stratify":"$F"}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FEAT_POLY":             `{"features":[{"type":"FEAT_POLY","field":"$F","params":{"degree":2}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,

	// Groupers.
	"GROUP_CATEGORY":        `{"groups":[{"type":"GROUP_CATEGORY","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_ROUNDED":         `{"groups":[{"type":"GROUP_ROUNDED","field":"$F","interval":10}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_RANGE":           `{"groups":[{"type":"GROUP_RANGE","field":"$F","interval":10}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_KMEANS":          `{"groups":[{"type":"GROUP_KMEANS","fields":["$F","x"],"params":{"k":2}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_QUANTILE":        `{"groups":[{"type":"GROUP_QUANTILE","field":"$F","interval":4}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_DATE":            `{"groups":[{"type":"GROUP_DATE","field":"$F","params":{"component":"month"}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_DATE_RANGES":     `{"groups":[{"type":"GROUP_DATE_RANGES","field":"$F","params":{"ranges":[{"label":"h1","start":"2024-01-01","end":"2024-06-30"}]}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_NUMERIC_RANGES":  `{"groups":[{"type":"GROUP_NUMERIC_RANGES","field":"$F","params":{"ranges":[{"label":"lo","end":10},{"label":"hi","start":10}]}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_SET_VALUE":       `{"groups":[{"type":"GROUP_SET_VALUE","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_SET_PER_ELEMENT": `{"groups":[{"type":"GROUP_SET_PER_ELEMENT","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,

	// Windows (ungrouped: one row per record).
	"WIN_LAG":         `{"windows":[{"type":"WIN_LAG","field":"$F","order_by":[{"field":"x"}],"params":{"offset":1}}]}`,
	"WIN_LEAD":        `{"windows":[{"type":"WIN_LEAD","field":"$F","order_by":[{"field":"x"}],"params":{"offset":1}}]}`,
	"WIN_ROW_NUMBER":  `{"windows":[{"type":"WIN_ROW_NUMBER","order_by":[{"field":"$F"}]}]}`,
	"WIN_RANK":        `{"windows":[{"type":"WIN_RANK","order_by":[{"field":"$F"}]}]}`,
	"WIN_DENSE_RANK":  `{"windows":[{"type":"WIN_DENSE_RANK","order_by":[{"field":"$F"}]}]}`,
	"WIN_RUNNING_SUM": `{"windows":[{"type":"WIN_RUNNING_SUM","field":"$F","order_by":[{"field":"x"}],"frame":{"mode":"rows","preceding":null,"following":0}}]}`,
	"WIN_RUNNING_AVG": `{"windows":[{"type":"WIN_RUNNING_AVG","field":"$F","order_by":[{"field":"x"}],"frame":{"mode":"rows","preceding":null,"following":0}}]}`,
	"WIN_MOVING_AVG":  `{"windows":[{"type":"WIN_MOVING_AVG","field":"$F","order_by":[{"field":"x"}],"frame":{"mode":"rows","preceding":3,"following":0}}]}`,
	"WIN_EWMA":        `{"windows":[{"type":"WIN_EWMA","field":"$F","order_by":[{"field":"x"}],"frame":{"mode":"rows","preceding":null,"following":0},"params":{"alpha":0.3}}]}`,
	"WIN_PCT_CHANGE":  `{"windows":[{"type":"WIN_PCT_CHANGE","field":"$F","order_by":[{"field":"x"}],"params":{"periods":1}}]}`,
	"WIN_DELTA":       `{"windows":[{"type":"WIN_DELTA","field":"$F","order_by":[{"field":"x"}],"params":{"periods":1}}]}`,
	"WIN_SHARE":       `{"windows":[{"type":"WIN_SHARE","field":"$F","order_by":[{"field":"x"}],"params":{"cumulative":true}}]}`,
	"WIN_LIFT":        `{"windows":[{"type":"WIN_LIFT","field":"$F","order_by":[{"field":"x"}],"params":{"base":"x","cumulative":true}}]}`,

	// Filterers.
	"FILTER_INCLUDE":           `{"filterers":[{"type":"FILTER_INCLUDE","field":"$F","values":["$V"]}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_EXCLUDE":           `{"filterers":[{"type":"FILTER_EXCLUDE","field":"$F","values":["$V"]}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_RANGE":             `{"filterers":[{"type":"FILTER_RANGE","field":"$F","values":["$V","$W"]}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_NULL":              `{"filterers":[{"type":"FILTER_NULL","field":"$F","values":["is_not_null"]}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_TRUE":              `{"filterers":[{"type":"FILTER_TRUE","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_FALSE":             `{"filterers":[{"type":"FILTER_FALSE","field":"$F"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_EXPRESSION":        `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"$F != nil"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_SET_CONTAINS_ANY":  `{"filterers":[{"type":"FILTER_SET_CONTAINS_ANY","field":"$F","values":["a"]}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_SET_CONTAINS_ALL":  `{"filterers":[{"type":"FILTER_SET_CONTAINS_ALL","field":"$F","values":["a"]}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_SET_CONTAINS_NONE": `{"filterers":[{"type":"FILTER_SET_CONTAINS_NONE","field":"$F","values":["a"]}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_SET_EQUALS":        `{"filterers":[{"type":"FILTER_SET_EQUALS","field":"$F","values":["a","b"]}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"FILTER_DATE_RANGES":       `{"filterers":[{"type":"FILTER_DATE_RANGES","field":"$F","params":{"ranges":[{"label":"h1","start":"2024-01-01","end":"2024-06-30"}]}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,

	// Tier-1 tests.
	"TEST_ANOVA_F":             `{"tests":[{"type":"TEST_ANOVA_F","field":"$F","split_by":"g"}]}`,
	"TEST_ANOVA_RM":            `{"tests":[{"type":"TEST_ANOVA_RM","field":"$F","split_by":"g","subject_field":"subj"}]}`,
	"TEST_ANOVA_WELCH":         `{"tests":[{"type":"TEST_ANOVA_WELCH","field":"$F","split_by":"g"}]}`,
	"TEST_BARTLETT_SPHERICITY": `{"tests":[{"type":"TEST_BARTLETT_SPHERICITY","fields":["$F","x"]}]}`,
	"TEST_BOX_M":               `{"tests":[{"type":"TEST_BOX_M","fields":["$F","x"],"split_by":"g"}]}`,
	"TEST_BROWN_FORSYTHE":      `{"tests":[{"type":"TEST_BROWN_FORSYTHE","field":"$F","split_by":"g"}]}`,
	"TEST_CHISQ":               `{"tests":[{"type":"TEST_CHISQ","rows":"$F","cols":"g"}]}`,
	"TEST_FISHER_EXACT":        `{"tests":[{"type":"TEST_FISHER_EXACT","rows":"$F","cols":"g"}]}`,
	"TEST_HOTELLING_T2":        `{"tests":[{"type":"TEST_HOTELLING_T2","fields":["$F","x"],"split_by":"g"}]}`,
	"TEST_KENDALL_TAU":         `{"tests":[{"type":"TEST_KENDALL_TAU","field":"$F","field2":"x"}]}`,
	"TEST_KRUSKAL_WALLIS":      `{"tests":[{"type":"TEST_KRUSKAL_WALLIS","field":"$F","split_by":"g"}]}`,
	"TEST_KS":                  `{"tests":[{"type":"TEST_KS","field":"$F","split_by":"g"}]}`,
	"TEST_MANN_WHITNEY_U":      `{"tests":[{"type":"TEST_MANN_WHITNEY_U","field":"$F","split_by":"g"}]}`,
	"TEST_MANOVA":              `{"tests":[{"type":"TEST_MANOVA","fields":["$F","x"],"split_by":"g"}]}`,
	"TEST_MARDIA":              `{"tests":[{"type":"TEST_MARDIA","fields":["$F","x"]}]}`,
	"TEST_PAIRED_T":            `{"tests":[{"type":"TEST_PAIRED_T","field":"$F","field2":"x"}]}`,
	"TEST_PEARSON_R":           `{"tests":[{"type":"TEST_PEARSON_R","field":"$F","field2":"x"}]}`,
	"TEST_PROP_Z":              `{"tests":[{"type":"TEST_PROP_Z","field":"$F","split_by":"g","params":{"success":"$S"}}]}`,
	"TEST_SHAPIRO_WILK":        `{"tests":[{"type":"TEST_SHAPIRO_WILK","field":"$F"}]}`,
	"TEST_SPEARMAN_R":          `{"tests":[{"type":"TEST_SPEARMAN_R","field":"$F","field2":"x"}]}`,
	"TEST_T":                   `{"tests":[{"type":"TEST_T","field":"$F","params":{"mu":10}}]}`,
	"TEST_WELCH":               `{"tests":[{"type":"TEST_WELCH","field":"$F","split_by":"g"}]}`,
	"TEST_WILCOXON_SR":         `{"tests":[{"type":"TEST_WILCOXON_SR","field":"$F","field2":"x"}]}`,
	"TEST_Z_TWO_SAMPLE":        `{"tests":[{"type":"TEST_Z_TWO_SAMPLE","field":"$F","split_by":"g"}]}`,
	// Tier-2 only: judged over output rows, whose columns are f64.
	"TEST_TREND":     ``,
	"TEST_TUKEY_HSD": ``,

	// Regressions (the field under judgement is a predictor).
	"REG_OLS":          `{"regressions":[{"type":"REG_OLS","name":"r","target":"x","predictors":["$F"]}]}`,
	"REG_GLM":          `{"regressions":[{"type":"REG_GLM","name":"r","target":"y","predictors":["$F"],"family":"poisson","link":"log"}]}`,
	"REG_BAYES_LINEAR": `{"regressions":[{"type":"REG_BAYES_LINEAR","name":"r","target":"x","predictors":["$F"],"prior":"nig","credible_level":0.95}]}`,
}

// acceptanceValues are the filter literals per field type (the
// value filterers parse numbers, so a temporal or boolean bound is its
// stored number) and the TEST_PROP_Z success literal.
func acceptanceValues(ft encoding.FieldType) (v, w, success string) {
	switch {
	case ft == encoding.FieldTypeDate:
		return "19723", "19904", "19723"
	case ft == encoding.FieldTypeDateTime:
		return "1704067200", "1719705600", "1704067200"
	case ft == encoding.FieldTypePackedBool:
		// TEST_PROP_Z's packed_bool success is a level, "0" / "1".
		return "1", "1", "1"
	case ft.IsCategorical(), ft.IsSet():
		return "a", "b", "a"
	}
	return "1", "20", "1"
}

func registeredOperatorNames() []string {
	var out []string
	for _, t := range types.AllAggregationTypes() {
		out = append(out, string(t))
	}
	for _, t := range types.AllAttributeTypes() {
		out = append(out, string(t))
	}
	for _, t := range types.AllFeatureTypes() {
		out = append(out, string(t))
	}
	for _, t := range types.AllGroupTypes() {
		out = append(out, string(t))
	}
	for _, t := range types.AllWindowTypes() {
		out = append(out, string(t))
	}
	for _, t := range types.AllFiltererTypes() {
		out = append(out, string(t))
	}
	for _, t := range types.AllTestTypes() {
		out = append(out, string(t))
	}
	for _, t := range types.AllRegressionTypes() {
		out = append(out, string(t))
	}
	sort.Strings(out)
	return out
}

// acceptanceDataOutcomes are runtime refusals raised AFTER the field
// type was accepted — too few rows or groups, a degenerate table, an
// ill-conditioned design. They judge the data, not the type, so the
// gate counts the type as accepted.
var acceptanceDataOutcomes = map[errors.Code]bool{
	errors.PULSE_TEST_INSUFFICIENT_N:            true,
	errors.PULSE_TEST_SPLIT_GROUPS_LT_2:         true,
	errors.PULSE_TEST_CONTINGENCY_DEGENERATE:    true,
	errors.PROCESSING_REGRESSION_RANK_DEFICIENT: true,
}

// acceptanceRefusalCodes are the codes an operator × field-type refusal
// may carry, on both sides (U45 #313 — a code says what it means): the
// accepted-types table's PULSE_FIELD_TYPE_UNSUPPORTED, the tier-1 numeric
// tests' PULSE_TEST_FIELD_NOT_NUMERIC and the decimal rule's
// PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL; the vector rules'
// PULSE_VECTOR_FIELD_NOT_SCALAR / PULSE_VECTOR_MEMBER_TYPE; a window
// order_by that is not orderable (PULSE_WINDOW_INVALID, the field-
// reference walk's); and the weight resolver's PULSE_WEIGHT_UNSUPPORTED
// (acceptancePrecedingOutcomes).
var acceptanceRefusalCodes = map[errors.Code]bool{
	errors.PULSE_FIELD_TYPE_UNSUPPORTED:         true,
	errors.PULSE_TEST_FIELD_NOT_NUMERIC:         true,
	errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL: true,
	errors.PULSE_VECTOR_FIELD_NOT_SCALAR:        true,
	errors.PULSE_VECTOR_MEMBER_TYPE:             true,
	errors.PULSE_WINDOW_INVALID:                 true,
	errors.PULSE_WEIGHT_UNSUPPORTED:             true,
}

// acceptanceSlot is the accepted-types table slot an operator's
// acceptance template puts $F in.
func acceptanceSlot(op string) fieldaccept.Slot {
	switch {
	case strings.HasPrefix(op, "REG_"), strings.HasPrefix(op, "ATTR_REG_"):
		return fieldaccept.SlotPredictor
	case op == "TEST_CHISQ", op == "TEST_FISHER_EXACT":
		return fieldaccept.SlotRows
	case op == "FEAT_TRAIN_TEST_SPLIT":
		return fieldaccept.SlotStratify
	}
	return fieldaccept.SlotField
}

// acceptancePrecedingOutcomes are refusals raised BEFORE the field type
// is judged, on both sides with the same single code: the weight
// resolver runs before any aggregator dispatch, so AGG_WEIGHTED_MEAN's
// params.weight_field over a decimal128 value field is refused
// PULSE_WEIGHT_UNSUPPORTED and the decimal rule is never reached
// (TestDecimalAggregation_PredictMatchesRuntime pins that rule with the
// weight opted out).
var acceptancePrecedingOutcomes = map[errors.Code]bool{
	errors.PULSE_WEIGHT_UNSUPPORTED: true,
}

// TestFieldTypeAcceptance_PredictMatchesRuntime walks every registered
// operator × every field type and asserts predict accepts exactly what
// the runtime accepts — with no allowance: there is no divergence
// ledger. A predict stricter than the runtime turns a working request
// away before it runs; a predict looser than the runtime promises a
// request the engine then refuses. The accepted-types table
// (internal/fieldaccept) is the third party: every pair it refuses is
// refused by both sides with its code (once at predict), and no pair it
// accepts is refused with that code.
//
// A vector type is judged by one rule on both sides: every slot that
// reads one value refuses a vec field with PULSE_VECTOR_FIELD_NOT_SCALAR
// (the field-reference walk's vectorScalarRefusals), and every vector
// slot accepts it.
func TestFieldTypeAcceptance_PredictMatchesRuntime(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	seenCodes := map[errors.Code]bool{}
	defer func() {
		// Every code the type refusals may carry is exercised, so the
		// list cannot keep a code no refusal raises any more.
		for code := range acceptanceRefusalCodes {
			if !seenCodes[code] {
				t.Errorf("%s: no operator × type pair refused with it; drop it from acceptanceRefusalCodes", code)
			}
		}
	}()
	for _, op := range registeredOperatorNames() {
		tmpl, ok := acceptanceTemplates[op]
		if !ok {
			t.Errorf("%s: registered operator has no acceptance template", op)
			continue
		}
		if tmpl == "" {
			continue
		}
		anyAccepted := false
		for _, ft := range acceptanceFieldTypes() {
			v, w, success := acceptanceValues(ft)
			body := strings.NewReplacer("$F", "t_"+ft.String(), "$V", v, "$W", w, "$S", success).Replace(tmpl)
			if ft.IsVector() {
				// A vector field in a vector slot expands to its dim
				// element members, so a literal reference grows with it.
				body = strings.ReplaceAll(body, `"reference":[1,2]`, `"reference":[1,2,3]`)
			}
			mk := func() *types.Request {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatalf("%s: template: %v", op, err)
				}
				req.Cohort = &types.Cohort{Filename: cohort}
				return &req
			}
			_, rerr := p.Process(ctx, mk())
			var ce *errors.CodedError
			runtimeOK := rerr == nil || (stderrors.As(rerr, &ce) && acceptanceDataOutcomes[ce.Code])
			env := predictEnvelope(t, p, fs, cohort, mk())
			predictOK := len(env.Errors) == 0
			if runtimeOK && predictOK {
				anyAccepted = true
			}
			// The accepted-types table is the third party: a slot it
			// refuses is refused by BOTH sides with its code (once at
			// predict), and a type it accepts is never refused with it.
			slot := acceptanceSlot(op)
			var predictCodes []string
			for _, e := range env.Errors {
				predictCodes = append(predictCodes, e.Code)
			}
			preceded := ce != nil && acceptancePrecedingOutcomes[ce.Code] && len(predictCodes) == 1 && predictCodes[0] == string(ce.Code)
			if want := fieldaccept.Refusal(op, slot, "f", ft); want != nil && !preceded {
				if runtimeOK || ce == nil || ce.Code != want.Code {
					t.Errorf("%s × %s: the table refuses it (%s); runtime returned %v", op, ft, want.Code, rerr)
				}
				if len(predictCodes) != 1 || predictCodes[0] != string(want.Code) {
					t.Errorf("%s × %s: the table refuses it (%s); predict reported %v", op, ft, want.Code, predictCodes)
				}
			} else if rule, tabled := fieldaccept.Lookup(op, slot); tabled && ce != nil && ce.Code == rule.Code && !ft.IsVector() {
				t.Errorf("%s × %s: the table accepts it, yet the runtime refused %v", op, ft, rerr)
			}
			// Both refuse: the SAME code, once at predict (rule 3 —
			// no refusal is raised under two codes).
			if !runtimeOK && !predictOK && ce != nil {
				if len(predictCodes) != 1 || predictCodes[0] != string(ce.Code) {
					t.Errorf("%s × %s: runtime refused %s, predict reported %v", op, ft, ce.Code, predictCodes)
				}
				if !acceptanceRefusalCodes[ce.Code] {
					t.Errorf("%s × %s: refused with %s, which names no type refusal (%v)", op, ft, ce.Code, rerr)
				}
				seenCodes[ce.Code] = true
			}
			if runtimeOK != predictOK {
				detail := ""
				if rerr != nil && !runtimeOK {
					detail = "runtime: " + rerr.Error()
				} else {
					detail = "predict: " + env.Errors[0].Code + " " + env.Errors[0].Message
				}
				t.Errorf("%s × %s: runtime accepts=%v, predict accepts=%v (%s)", op, ft, runtimeOK, predictOK, detail)
			}
			// A vector refusal is ONE rule on both sides: the runtime
			// and every predict error carry PULSE_VECTOR_FIELD_NOT_SCALAR
			// (no slot-specific type error repeats it).
			if ft.IsVector() && !runtimeOK {
				if ce == nil || ce.Code != errors.PULSE_VECTOR_FIELD_NOT_SCALAR {
					t.Errorf("%s × %s: runtime refusal %v, want %s", op, ft, rerr, errors.PULSE_VECTOR_FIELD_NOT_SCALAR)
				}
				for _, e := range env.Errors {
					if e.Code != string(errors.PULSE_VECTOR_FIELD_NOT_SCALAR) {
						t.Errorf("%s × %s: predict error %s %q beside the vector refusal", op, ft, e.Code, e.Message)
					}
				}
			}
		}
		if !anyAccepted {
			t.Errorf("%s: no field type accepted by both sides — the template is malformed", op)
		}
	}
}

// TestModelError_PredictionFieldTypeParity walks every field type on
// the PREDICTION side of the model-error aggregators (the acceptance
// templates judge the actual): predict and runtime accept exactly the
// same types, and that set is the declared numeric one — the integer
// and float scalars plus packed_bool. decimal128 is refused on both
// sides (fit-scoring E2-S1 decision: no decimal implementation).
func TestModelError_PredictionFieldTypeParity(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	wantOK := map[encoding.FieldType]bool{
		encoding.FieldTypeU4: true, encoding.FieldTypeU8: true, encoding.FieldTypeU16: true,
		encoding.FieldTypeU32: true, encoding.FieldTypeU64: true,
		encoding.FieldTypeF32: true, encoding.FieldTypeF64: true, encoding.FieldTypePackedBool: true,
	}
	// AGG_AUC's outcome column shares the set (model-workbench E2-S1).
	paramKey := map[string]string{"AGG_MAPE": "prediction_field", "AGG_RMSE": "prediction_field", "AGG_MAE": "prediction_field", "AGG_AUC": "outcome"}
	for _, op := range []string{"AGG_MAPE", "AGG_RMSE", "AGG_MAE", "AGG_AUC"} {
		for _, ft := range acceptanceFieldTypes() {
			body := `{"aggregations":[{"type":"` + op + `","field":"x","params":{"` + paramKey[op] + `":"t_` + ft.String() + `"}}]}`
			mk := func() *types.Request {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatal(err)
				}
				req.Cohort = &types.Cohort{Filename: cohort}
				return &req
			}
			_, rerr := p.Process(ctx, mk())
			// The fixture's values are not 0 / 1: an AUC outcome whose TYPE
			// is accepted reaches the run and is refused on its VALUES, a
			// data fault predict cannot see — the type passed.
			var ce *errors.CodedError
			if op == "AGG_AUC" && stderrors.As(rerr, &ce) && ce.Code == errors.PROCESSING_AUC_OUTCOME_NOT_BINARY {
				rerr = nil
			}
			predictOK := len(predictEnvelope(t, p, fs, cohort, mk()).Errors) == 0
			if (rerr == nil) != predictOK {
				t.Errorf("%s prediction %s: runtime err %v, predict ok %v", op, ft, rerr, predictOK)
			}
			if (rerr == nil) != wantOK[ft] {
				t.Errorf("%s prediction %s: accepted %v, want %v (%v)", op, ft, rerr == nil, wantOK[ft], rerr)
			}
		}
	}
}
