package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// slotParityCases put $F in a slot the acceptance gate's templates do
// not judge: a test's split_by / subject_field, a contingency test's
// cols, FEAT_TARGET_ENCODE's target, FILTER_RANGE under numeric bounds.
var slotParityCases = []struct {
	op   string
	slot fieldaccept.Slot
	body string
}{
	{"TEST_T", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_T","field":"x","split_by":"$F"}]}`},
	{"TEST_WELCH", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_WELCH","field":"x","split_by":"$F"}]}`},
	{"TEST_ANOVA_F", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_ANOVA_F","field":"x","split_by":"$F"}]}`},
	{"TEST_ANOVA_WELCH", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_ANOVA_WELCH","field":"x","split_by":"$F"}]}`},
	{"TEST_BROWN_FORSYTHE", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_BROWN_FORSYTHE","field":"x","split_by":"$F"}]}`},
	{"TEST_KS", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_KS","field":"x","split_by":"$F"}]}`},
	{"TEST_KRUSKAL_WALLIS", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_KRUSKAL_WALLIS","field":"x","split_by":"$F"}]}`},
	{"TEST_MANN_WHITNEY_U", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_MANN_WHITNEY_U","field":"x","split_by":"$F"}]}`},
	{"TEST_SHAPIRO_WILK", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_SHAPIRO_WILK","field":"x","split_by":"$F"}]}`},
	{"TEST_Z_TWO_SAMPLE", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_Z_TWO_SAMPLE","field":"x","split_by":"$F"}]}`},
	{"TEST_ANOVA_RM", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_ANOVA_RM","field":"x","split_by":"$F","subject_field":"subj"}]}`},
	{"TEST_ANOVA_RM", fieldaccept.SlotSubject, `{"tests":[{"type":"TEST_ANOVA_RM","field":"x","split_by":"g","subject_field":"$F"}]}`},
	{"TEST_HOTELLING_T2", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_HOTELLING_T2","fields":["x","y"],"split_by":"$F"}]}`},
	{"TEST_MANOVA", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_MANOVA","fields":["x","y"],"split_by":"$F"}]}`},
	{"TEST_BOX_M", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_BOX_M","fields":["x","y"],"split_by":"$F"}]}`},
	{"TEST_PROP_Z", fieldaccept.SlotSplitBy, `{"tests":[{"type":"TEST_PROP_Z","field":"g","split_by":"$F","params":{"success":"a"}}]}`},
	{"TEST_CHISQ", fieldaccept.SlotCols, `{"tests":[{"type":"TEST_CHISQ","rows":"g","cols":"$F"}]}`},
	{"TEST_FISHER_EXACT", fieldaccept.SlotCols, `{"tests":[{"type":"TEST_FISHER_EXACT","rows":"g","cols":"$F"}]}`},
	{"FEAT_TARGET_ENCODE", fieldaccept.SlotTarget, `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"$F","smoothing":5}}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`},
	// Numeric bounds: a categorical is refused by its type, not by a
	// bound that fails to parse (it used to range over dictionary
	// indexes).
	{"FILTER_RANGE", fieldaccept.SlotField, `{"filterers":[{"type":"FILTER_RANGE","field":"$F","values":["0","1"]}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`},
}

// TestFieldTypeSlots_PredictMatchesRuntime extends the acceptance gate
// to the slots its templates do not judge: for every scalar field type,
// predict and the runtime accept exactly what the accepted-types table
// accepts, and refuse the rest with the table's code — once at predict
// (rule 3: a "must be categorical" refusal is PULSE_FIELD_TYPE_UNSUPPORTED
// on both sides, never PULSE_TEST_FIELD_NOT_NUMERIC on one and
// PROCESSING_CONFIG or SERVICE_VALIDATION on the other).
func TestFieldTypeSlots_PredictMatchesRuntime(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for _, c := range slotParityCases {
		refused := 0
		for _, ft := range acceptanceFieldTypes() {
			if ft.IsVector() {
				continue
			}
			body := strings.ReplaceAll(c.body, "$F", "t_"+ft.String())
			mk := func() *types.Request {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatalf("%s: %v", body, err)
				}
				req.Cohort = &types.Cohort{Filename: cohort}
				return &req
			}
			_, rerr := p.Process(ctx, mk())
			var ce *errors.CodedError
			runtimeOK := rerr == nil || (stderrors.As(rerr, &ce) && acceptanceDataOutcomes[ce.Code])
			env := predictEnvelope(t, p, fs, cohort, mk())
			var codes []string
			for _, e := range env.Errors {
				codes = append(codes, e.Code)
			}
			want := fieldaccept.Refusal(c.op, c.slot, "f", ft)
			if want == nil {
				if !runtimeOK || len(codes) != 0 {
					t.Errorf("%s %s × %s: the table accepts it; runtime %v, predict %v", c.op, c.slot, ft, rerr, codes)
				}
				continue
			}
			refused++
			if ce == nil || ce.Code != want.Code {
				t.Errorf("%s %s × %s: runtime %v, want %s", c.op, c.slot, ft, rerr, want.Code)
			}
			if len(codes) != 1 || codes[0] != string(want.Code) {
				t.Errorf("%s %s × %s: predict %v, want one %s", c.op, c.slot, ft, codes, want.Code)
			}
		}
		if refused == 0 {
			t.Errorf("%s %s: no type refused — the case is vacuous", c.op, c.slot)
		}
	}
}

// TestQuantityReaders_TypeVerdicts pins the verdict behind the
// quantity readers' row (ATTR_NORMALIZED / PERCENTILE / TSCORE /
// ZSCORE, GROUP_QUANTILE / RANGE / ROUNDED, FILTER_RANGE, and the
// ATTR_REG_* target and predictors): a decimal128 (its float64 echo), a
// datetime (epoch seconds) and a packed_bool (0 / 1) read as the numbers
// they are — every figure equals the one over an f64 twin column holding
// those numbers — so the table accepts them and the manifest declares
// them; a categorical's numeric echo is its dictionary INDEX (labels
// "30", "5", … read 0, 1, …), never a quantity, so it is refused by both
// sides.
func TestQuantityReaders_TypeVerdicts(t *testing.T) {
	p, cohort := quantityTwinCohort(t)
	ctx := context.Background()
	run := func(body string) (string, error) {
		var req types.Request
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		req.Cohort = &types.Cohort{Filename: cohort}
		r, err := p.Process(ctx, &req)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(r.Data)
		return string(out), nil
	}
	aggs := `"aggregations":[{"type":"AGG_SUM","field":"o","label":"s"},{"type":"AGG_MAX","field":"o","label":"mx"},{"type":"AGG_STDDEV","field":"o","label":"sd"}]`
	counts := `"aggregations":[{"type":"AGG_COUNT","field":"y","label":"n"},{"type":"AGG_SUM","field":"y","label":"s"}]`
	type shape struct{ op, body string }
	shapes := []shape{}
	for _, op := range []string{"ATTR_NORMALIZED", "ATTR_PERCENTILE", "ATTR_TSCORE", "ATTR_ZSCORE"} {
		shapes = append(shapes, shape{op, `{"attributes":[{"type":"` + op + `","field":"$F","label":"o"}],` + aggs + `}`})
	}
	for _, op := range []string{"ATTR_REG_FITTED", "ATTR_REG_RESIDUAL", "ATTR_REG_LEVERAGE"} {
		shapes = append(shapes,
			shape{op, `{"attributes":[{"type":"` + op + `","target":"y","predictors":["$F"],"label":"o"}],` + aggs + `}`},
			shape{op, `{"attributes":[{"type":"` + op + `","target":"$F","predictors":["y"],"label":"o"}],` + aggs + `}`})
	}
	for _, op := range []string{"GROUP_QUANTILE", "GROUP_RANGE", "GROUP_ROUNDED"} {
		shapes = append(shapes, shape{op, `{"groups":[{"type":"` + op + `","field":"$F","interval":$I}],` + counts + `}`})
	}
	shapes = append(shapes, shape{"FILTER_RANGE", `{"filterers":[{"type":"FILTER_RANGE","field":"$F","values":["$LO","$HI"]}],` + counts + `}`})
	params := map[string][3]string{ // column: interval, low bound, high bound
		"d":  {"10", "5.5", "20.25"},
		"dt": {"2592000", "1710000000", "1720000000"},
		"b":  {"1", "1", "1"},
		"c":  {"10", "6", "20"},
	}
	for _, sh := range shapes {
		for _, col := range []string{"d", "dt", "b", "c"} {
			pr := params[col]
			interval := pr[0]
			if sh.op == "GROUP_QUANTILE" {
				interval = "4"
			}
			fill := func(name string) string {
				return strings.NewReplacer("$F", name, "$I", interval, "$LO", pr[1], "$HI", pr[2]).Replace(sh.body)
			}
			got, err := run(fill(col))
			twin, terr := run(fill(col + "_f"))
			if terr != nil {
				t.Fatalf("%s over the f64 twin %s_f: %v", sh.op, col, terr)
			}
			if col == "c" {
				if strings.HasPrefix(sh.op, "ATTR_REG_") {
					continue // the categorical regression cases are the acceptance gate's
				}
				if !errors.HasCode(err, errors.PULSE_FIELD_TYPE_UNSUPPORTED) {
					t.Errorf("%s over a categorical: %v (%s), want PULSE_FIELD_TYPE_UNSUPPORTED — its echo is a dictionary index", sh.op, err, got)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s over %s: %v", sh.op, col, err)
				continue
			}
			if want := strings.ReplaceAll(twin, `"`+col+`_f":`, `"`+col+`":`); got != want {
				t.Errorf("%s over %s:\n got  %s\n twin %s", sh.op, col, got, want)
			}
		}
	}
}

// quantityTwinCohort imports a cohort pairing each column with an f64
// twin holding the number the column means: c (categorical labels
// "30", "5", …), d (decimal128), dt (datetime) and b (packed_bool), plus
// a y outcome.
func quantityTwinCohort(t *testing.T) (*pulse.Pulse, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"c", "c_f", "d", "d_f", "dt", "dt_f", "b", "b_f", "y"}
	fts := []encoding.FieldType{encoding.FieldTypeCategoricalU8, encoding.FieldTypeF64, encoding.FieldTypeDecimal128, encoding.FieldTypeF64,
		encoding.FieldTypeDateTime, encoding.FieldTypeF64, encoding.FieldTypePackedBool, encoding.FieldTypeF64, encoding.FieldTypeF64}
	var fields []encoding.Field
	off := 0
	for i, n := range names {
		f := encoding.Field{Name: n, Type: fts[i], CsvColumnIdx: i, ByteOffset: off}
		if f.Type.HasDictionary() {
			f.Dictionary = encoding.NewDictionary()
		}
		if f.Type.IsDecimal() {
			f.Precision, f.Scale = 18, 2
		}
		if f.Type.IsBitPacked() {
			off++
		} else {
			off += f.Type.ByteSize()
		}
		fields = append(fields, f)
	}
	var b strings.Builder
	b.WriteString(strings.Join(names, ",") + "\n")
	labels := []string{"30", "5", "12", "7", "50", "3"}
	for i := 0; i < 40; i++ {
		c := labels[(i*5)%6]
		d := fmt.Sprintf("%d.%02d", 1+(i*7)%50, (i*13)%100)
		ts := time.Date(2024, time.Month(1+i%12), 1+(i*3)%28, i%24, 15, 0, 0, time.UTC)
		flag, flagNum := "false", "0"
		if i%3 == 0 {
			flag, flagNum = "true", "1"
		}
		fmt.Fprintf(&b, "%s,%s,%s,%s,%s,%d,%s,%s,%.3f\n", c, c, d, d, ts.Format(time.RFC3339), ts.Unix(), flag, flagNum, float64(i)*1.1+float64((i*7)%5))
	}
	if err := afero.WriteFile(fs, "twin.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	job := &pio.ImportJob{Source: csv.NewReader(fs, "twin.csv"), Target: "twin.pulse", Schema: &encoding.Schema{Fields: fields}, FS: fs}
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("import: %v", err)
	}
	return p, "twin.pulse"
}
