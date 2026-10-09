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
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// acceptanceFieldTypes is every registered field type, in type-byte
// order; the cohort carries one column per type, named t_<type>.
func acceptanceFieldTypes() []encoding.FieldType {
	var out []encoding.FieldType
	for b := 0; b < 256; b++ {
		if ft := encoding.FieldType(b); ft.IsKnown() {
			out = append(out, ft)
		}
	}
	return out
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
	var fields []encoding.Field
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
	for _, ft := range acceptanceFieldTypes() {
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
	for i := range 48 {
		for _, ft := range acceptanceFieldTypes() {
			b.WriteString(acceptanceCell(ft, i))
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%.3f,%.3f,%s,s%d\n", float64(i)*1.1+float64(i%5), float64((i*13)%17)+0.5, []string{"a", "b"}[i%2], i/2)
	}
	if err := afero.WriteFile(fs, "acc.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	job := &pio.ImportJob{Source: csv.NewReader(fs, "acc.csv"), Target: "acc.pulse", Schema: &encoding.Schema{Fields: fields}, FS: fs}
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("import: %v", err)
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
	"AGG_COUNT":               `{"aggregations":[{"type":"AGG_COUNT","field":"$F"}]}`,
	"AGG_SUM":                 `{"aggregations":[{"type":"AGG_SUM","field":"$F"}]}`,
	"AGG_AVERAGE":             `{"aggregations":[{"type":"AGG_AVERAGE","field":"$F"}]}`,
	"AGG_MIN":                 `{"aggregations":[{"type":"AGG_MIN","field":"$F"}]}`,
	"AGG_MAX":                 `{"aggregations":[{"type":"AGG_MAX","field":"$F"}]}`,
	"AGG_STDDEV":              `{"aggregations":[{"type":"AGG_STDDEV","field":"$F"}]}`,
	"AGG_RANGE":               `{"aggregations":[{"type":"AGG_RANGE","field":"$F"}]}`,
	"AGG_MODE_COUNT":          `{"aggregations":[{"type":"AGG_MODE_COUNT","field":"$F"}]}`,
	"AGG_FREQUENCY":           `{"aggregations":[{"type":"AGG_FREQUENCY","field":"$F","params":{"value":"$V"}}]}`,
	"AGG_ZSCORE":              `{"aggregations":[{"type":"AGG_ZSCORE","field":"$F"}]}`,
	"AGG_MEDIAN":              `{"aggregations":[{"type":"AGG_MEDIAN","field":"$F"}]}`,
	"AGG_VARIANCE":            `{"aggregations":[{"type":"AGG_VARIANCE","field":"$F"}]}`,
	"AGG_MODE":                `{"aggregations":[{"type":"AGG_MODE","field":"$F"}]}`,
	"AGG_SKEWNESS":            `{"aggregations":[{"type":"AGG_SKEWNESS","field":"$F"}]}`,
	"AGG_KURTOSIS":            `{"aggregations":[{"type":"AGG_KURTOSIS","field":"$F"}]}`,
	"AGG_DISTINCT_COUNT":      `{"aggregations":[{"type":"AGG_DISTINCT_COUNT","field":"$F"}]}`,
	"AGG_PERCENTILE":          `{"aggregations":[{"type":"AGG_PERCENTILE","field":"$F","params":{"percentile":90}}]}`,
	"AGG_NULL_COUNT":          `{"aggregations":[{"type":"AGG_NULL_COUNT","field":"$F"}]}`,
	"AGG_WEIGHTED_MEAN":       `{"aggregations":[{"type":"AGG_WEIGHTED_MEAN","field":"$F","params":{"weight_field":"y"}}]}`,
	"AGG_RATIO":               `{"aggregations":[{"type":"AGG_RATIO","field":"$F","params":{"numerator_field":"$F","denominator_field":"y"}}]}`,
	"AGG_DISTINCT_SUM":        `{"aggregations":[{"type":"AGG_DISTINCT_SUM","field":"$F","params":{"distinct_by":"subj"}}]}`,
	"AGG_CI_LOWER":            `{"aggregations":[{"type":"AGG_CI_LOWER","field":"$F","params":{"confidence":0.95,"method":"normal"}}]}`,
	"AGG_CI_UPPER":            `{"aggregations":[{"type":"AGG_CI_UPPER","field":"$F","params":{"confidence":0.95,"method":"normal"}}]}`,
	"AGG_WELFORD":             `{"aggregations":[{"type":"AGG_WELFORD","field":"$F"}]}`,
	"AGG_SET_UNION":           `{"aggregations":[{"type":"AGG_SET_UNION","field":"$F"}]}`,
	"AGG_SET_INTERSECTION":    `{"aggregations":[{"type":"AGG_SET_INTERSECTION","field":"$F"}]}`,
	"AGG_SET_FREQUENCY":       `{"aggregations":[{"type":"AGG_SET_FREQUENCY","field":"$F"}]}`,
	"AGG_SET_CARDINALITY_SUM": `{"aggregations":[{"type":"AGG_SET_CARDINALITY_SUM","field":"$F"}]}`,
	"AGG_SET_CARDINALITY_AVG": `{"aggregations":[{"type":"AGG_SET_CARDINALITY_AVG","field":"$F"}]}`,
	"AGG_SET_DISTINCT_VALUES": `{"aggregations":[{"type":"AGG_SET_DISTINCT_VALUES","field":"$F"}]}`,

	// Attributes (aggregated afterwards so the label is consumed).
	"ATTR_ZSCORE":       `{"attributes":[{"type":"ATTR_ZSCORE","field":"$F","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_TSCORE":       `{"attributes":[{"type":"ATTR_TSCORE","field":"$F","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_NORMALIZED":   `{"attributes":[{"type":"ATTR_NORMALIZED","field":"$F","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_FORMULA":      `{"attributes":[{"type":"ATTR_FORMULA","field":"$F","label":"o","expression":"x * 2"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_PERCENTILE":   `{"attributes":[{"type":"ATTR_PERCENTILE","field":"$F","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_DATE_PART":    `{"attributes":[{"type":"ATTR_DATE_PART","field":"$F","label":"o","params":{"part":"year"}}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_REG_FITTED":   `{"attributes":[{"type":"ATTR_REG_FITTED","target":"x","predictors":["$F"],"label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_REG_RESIDUAL": `{"attributes":[{"type":"ATTR_REG_RESIDUAL","target":"x","predictors":["$F"],"label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_REG_LEVERAGE": `{"attributes":[{"type":"ATTR_REG_LEVERAGE","target":"x","predictors":["$F"],"label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_SET_POPCOUNT": `{"attributes":[{"type":"ATTR_SET_POPCOUNT","field":"$F","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_SET_HAS":      `{"attributes":[{"type":"ATTR_SET_HAS","field":"$F","label":"o","params":{"label":"a"}}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,
	"ATTR_CODE_IN":      `{"attributes":[{"type":"ATTR_CODE_IN","field":"$F","label":"o","params":{"codes":["$V"]}}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`,

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
	"GROUP_QUANTILE":        `{"groups":[{"type":"GROUP_QUANTILE","field":"$F","interval":4}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_DATE":            `{"groups":[{"type":"GROUP_DATE","field":"$F","params":{"unit":"month"}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
	"GROUP_DATE_RANGES":     `{"groups":[{"type":"GROUP_DATE_RANGES","field":"$F","params":{"ranges":[{"label":"h1","start":"2024-01-01","end":"2024-06-30"}]}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
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
	"TEST_ANOVA_F":        `{"tests":[{"type":"TEST_ANOVA_F","field":"$F","split_by":"g"}]}`,
	"TEST_ANOVA_RM":       `{"tests":[{"type":"TEST_ANOVA_RM","field":"$F","split_by":"g","subject_field":"subj"}]}`,
	"TEST_ANOVA_WELCH":    `{"tests":[{"type":"TEST_ANOVA_WELCH","field":"$F","split_by":"g"}]}`,
	"TEST_BROWN_FORSYTHE": `{"tests":[{"type":"TEST_BROWN_FORSYTHE","field":"$F","split_by":"g"}]}`,
	"TEST_CHISQ":          `{"tests":[{"type":"TEST_CHISQ","rows":"$F","cols":"g"}]}`,
	"TEST_FISHER_EXACT":   `{"tests":[{"type":"TEST_FISHER_EXACT","rows":"$F","cols":"g"}]}`,
	"TEST_KENDALL_TAU":    `{"tests":[{"type":"TEST_KENDALL_TAU","field":"$F","field2":"x"}]}`,
	"TEST_KRUSKAL_WALLIS": `{"tests":[{"type":"TEST_KRUSKAL_WALLIS","field":"$F","split_by":"g"}]}`,
	"TEST_KS":             `{"tests":[{"type":"TEST_KS","field":"$F","split_by":"g"}]}`,
	"TEST_MANN_WHITNEY_U": `{"tests":[{"type":"TEST_MANN_WHITNEY_U","field":"$F","split_by":"g"}]}`,
	"TEST_PAIRED_T":       `{"tests":[{"type":"TEST_PAIRED_T","field":"$F","field2":"x"}]}`,
	"TEST_PEARSON_R":      `{"tests":[{"type":"TEST_PEARSON_R","field":"$F","field2":"x"}]}`,
	"TEST_PROP_Z":         `{"tests":[{"type":"TEST_PROP_Z","field":"$F","split_by":"g","params":{"success":"$S"}}]}`,
	"TEST_SHAPIRO_WILK":   `{"tests":[{"type":"TEST_SHAPIRO_WILK","field":"$F"}]}`,
	"TEST_SPEARMAN_R":     `{"tests":[{"type":"TEST_SPEARMAN_R","field":"$F","field2":"x"}]}`,
	"TEST_T":              `{"tests":[{"type":"TEST_T","field":"$F","params":{"mu":10}}]}`,
	"TEST_WELCH":          `{"tests":[{"type":"TEST_WELCH","field":"$F","split_by":"g"}]}`,
	"TEST_WILCOXON_SR":    `{"tests":[{"type":"TEST_WILCOXON_SR","field":"$F","field2":"x"}]}`,
	"TEST_Z_TWO_SAMPLE":   `{"tests":[{"type":"TEST_Z_TWO_SAMPLE","field":"$F","split_by":"g"}]}`,
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
		return "1", "1", "true"
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

// knownTypeDivergence is the ledger of operator × field type pairs where
// predict and the runtime are KNOWN to disagree, each with its reason;
// "" means they must agree. It is exact: an entry that no longer
// diverges fails the gate too, so a fix removes its entry in the same
// change. Two directions:
//
//   - predict looser: the runtime's operator constructor refuses a type
//     predict has no check for, so predict promises a request the engine
//     then turns away;
//   - predict stricter where the RUNTIME is wrong: the engine runs a
//     type it cannot meaningfully read (a categorical's dictionary
//     index, a set's bitmask), so predict is not widened to match.
//
// Predict stricter than a CORRECT runtime is never listed: that is a
// predict bug and is fixed (FEAT_POLY on u4 / date was one).
func knownTypeDivergence(op string, ft encoding.FieldType) string {
	set, cat := ft.IsSet(), ft.IsCategorical()
	temporal := ft == encoding.FieldTypeDate || ft == encoding.FieldTypeDateTime
	in := func(names ...string) bool {
		for _, n := range names {
			if n == op {
				return true
			}
		}
		return false
	}
	switch {
	// Predict looser. (AGG_WEIGHTED_MEAN is not here: it always carries
	// a weight, and a weight on a decimal128 value field is refused
	// PULSE_WEIGHT_UNSUPPORTED by the shared resolver on both sides.)
	case ft.IsDecimal() && in("AGG_CI_LOWER", "AGG_CI_UPPER", "AGG_DISTINCT_SUM", "AGG_FREQUENCY", "AGG_MODE_COUNT", "AGG_KURTOSIS",
		"AGG_MEDIAN", "AGG_MODE", "AGG_NULL_COUNT", "AGG_PERCENTILE", "AGG_RANGE", "AGG_RATIO", "AGG_SKEWNESS",
		"AGG_WELFORD", "AGG_ZSCORE", "AGG_SET_UNION", "AGG_SET_INTERSECTION",
		"AGG_SET_FREQUENCY", "AGG_SET_CARDINALITY_SUM", "AGG_SET_CARDINALITY_AVG", "AGG_SET_DISTINCT_VALUES"):
		return "predict only WARNS PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL (an error under Strict); the runtime refuses decimal128 regardless"
	case set && in("AGG_DISTINCT_COUNT", "AGG_FREQUENCY", "AGG_MODE_COUNT", "AGG_MODE", "AGG_WELFORD", "ATTR_NORMALIZED", "ATTR_PERCENTILE",
		"ATTR_TSCORE", "ATTR_ZSCORE", "ATTR_REG_FITTED", "ATTR_REG_RESIDUAL", "ATTR_REG_LEVERAGE", "GROUP_CATEGORY",
		"GROUP_QUANTILE", "GROUP_RANGE", "GROUP_ROUNDED", "FILTER_INCLUDE", "FILTER_EXCLUDE", "FILTER_RANGE"):
		return "the runtime constructor refuses a set_* column (no numeric value); predict has no set check"
	case !set && strings.Contains(op, "_SET_"):
		return "the runtime constructor refuses a non-set column on a set operator; predict has no type check"
	case op == "AGG_WELFORD" && (ft == encoding.FieldTypeU4 || temporal || ft == encoding.FieldTypePackedBool || cat):
		return "AGG_WELFORD's runtime numeric check is narrower than the analytics set predict assumes"
	case in("GROUP_DATE_RANGES", "FILTER_DATE_RANGES") && !temporal:
		return "the runtime refuses a non-temporal field; predict has no type check"
	case in("FILTER_TRUE", "FILTER_FALSE") && ft != encoding.FieldTypePackedBool:
		return "the runtime's strict mode refuses a non-packed_bool field; predict has no type check"
	case op == "FILTER_RANGE" && cat:
		return "the runtime cannot parse a dictionary label as a range bound; predict accepts it"
	case cat && in("ATTR_REG_FITTED", "ATTR_REG_RESIDUAL", "ATTR_REG_LEVERAGE"):
		return "the runtime refuses a categorical predictor; predict has no type check"
	case cat && op == "TEST_Z_TWO_SAMPLE":
		return "the runtime refuses a categorical field (PULSE_TEST_FIELD_NOT_NUMERIC); predict has no check"

	// Predict stricter, runtime wrong.
	case op == "FEAT_POLY" && (cat || set || ft == encoding.FieldTypePackedBool || ft == encoding.FieldTypeDateTime):
		return "the runtime expands any column (a categorical's index, a set's bitmask); the skill and manifest exclude these"
	case cat && in("REG_OLS", "REG_GLM", "REG_BAYES_LINEAR"):
		return "the runtime regresses on a categorical's dictionary index; predict refuses it as non-numeric"
	case in("WIN_LAG", "WIN_LEAD", "WIN_RUNNING_SUM", "WIN_RUNNING_AVG", "WIN_MOVING_AVG", "WIN_EWMA", "WIN_PCT_CHANGE", "WIN_DELTA") &&
		(cat || set || ft.IsDecimal() || ft == encoding.FieldTypePackedBool || ft == encoding.FieldTypeDateTime):
		return "the runtime windows whatever the record row carries for the column; predict admits only u4..u64, f32/f64, date"
	}
	return ""
}

// TestFieldTypeAcceptance_PredictMatchesRuntime walks every registered
// operator × every field type and asserts predict accepts exactly what
// the runtime accepts, apart from the knownTypeDivergence ledger. A
// predict stricter than the runtime turns a working request away before
// it runs; a predict looser than the runtime promises a request the
// engine then refuses.
func TestFieldTypeAcceptance_PredictMatchesRuntime(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
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
			known := knownTypeDivergence(op, ft)
			switch {
			case runtimeOK != predictOK && known == "":
				detail := ""
				if rerr != nil && !runtimeOK {
					detail = "runtime: " + rerr.Error()
				} else {
					detail = "predict: " + env.Errors[0].Code + " " + env.Errors[0].Message
				}
				t.Errorf("%s × %s: runtime accepts=%v, predict accepts=%v (%s)", op, ft, runtimeOK, predictOK, detail)
			case runtimeOK == predictOK && known != "":
				t.Errorf("%s × %s: listed as a known divergence (%s) but both sides now agree — remove the ledger entry", op, ft, known)
			}
		}
		if !anyAccepted {
			t.Errorf("%s: no field type accepted by both sides — the template is malformed", op)
		}
	}
}
