package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// fieldTypeRefusalCase is one (request, field type) pair the
// accepted-types table (internal/fieldaccept) refuses: the runtime
// returns the coded refusal from Process instead of a number, and
// predict reports exactly one error with the same code.
type fieldTypeRefusalCase struct {
	op   string
	body string
	ft   encoding.FieldType
	code errors.Code
}

// refusedValueTypes are the twelve field types no value window reads:
// a categorical's dictionary index, a set's bitmask, decimal128,
// packed_bool and datetime.
func refusedValueTypes() []encoding.FieldType {
	var out []encoding.FieldType
	for _, ft := range acceptanceFieldTypes() {
		if ft.IsCategorical() || ft.IsSet() || ft.IsDecimal() ||
			ft == encoding.FieldTypePackedBool || ft == encoding.FieldTypeDateTime {
			out = append(out, ft)
		}
	}
	return out
}

func fieldTypeRefusalCases() []fieldTypeRefusalCase {
	var out []fieldTypeRefusalCase
	fill := func(op, tmpl string, ft encoding.FieldType, code errors.Code) {
		v, w, s := acceptanceValues(ft)
		body := strings.NewReplacer("$F", "t_"+ft.String(), "$V", v, "$W", w, "$S", s).Replace(tmpl)
		out = append(out, fieldTypeRefusalCase{op: op, body: body, ft: ft, code: code})
	}
	// Value windows: 8 ops x 12 types = 96.
	for _, op := range []string{"WIN_LAG", "WIN_LEAD", "WIN_RUNNING_SUM", "WIN_RUNNING_AVG", "WIN_MOVING_AVG", "WIN_EWMA", "WIN_PCT_CHANGE", "WIN_DELTA"} {
		for _, ft := range refusedValueTypes() {
			fill(op, acceptanceTemplates[op], ft, errors.PULSE_FIELD_TYPE_UNSUPPORTED)
		}
	}
	// FEAT_POLY: the same twelve minus decimal128 (read via f64) = 11.
	for _, ft := range refusedValueTypes() {
		if !ft.IsDecimal() {
			fill("FEAT_POLY", acceptanceTemplates["FEAT_POLY"], ft, errors.PULSE_FIELD_TYPE_UNSUPPORTED)
		}
	}
	// Regressions on a categorical, as predictor (3 ops x 3 = 9) and as
	// target.
	for _, op := range []string{"REG_OLS", "REG_GLM", "REG_BAYES_LINEAR"} {
		for _, ft := range acceptanceFieldTypes() {
			if !ft.IsCategorical() && !ft.IsSet() {
				continue
			}
			fill(op, acceptanceTemplates[op], ft, errors.PULSE_FIELD_TYPE_UNSUPPORTED)
			if ft.IsCategorical() {
				tmpl := strings.Replace(acceptanceTemplates[op], `"predictors":["$F"]`, `"predictors":["y"]`, 1)
				tmpl = strings.Replace(tmpl, `"target":"x"`, `"target":"$F"`, 1)
				tmpl = strings.Replace(tmpl, `"target":"y"`, `"target":"$F"`, 1)
				fill(op, tmpl, ft, errors.PULSE_FIELD_TYPE_UNSUPPORTED)
			}
		}
	}
	// Tier-1 numeric tests on every set rung: field, and field2 for the
	// bivariate tests.
	field2 := map[string]bool{"TEST_PAIRED_T": true, "TEST_PEARSON_R": true, "TEST_SPEARMAN_R": true, "TEST_KENDALL_TAU": true, "TEST_WILCOXON_SR": true}
	for _, op := range []string{"TEST_T", "TEST_WELCH", "TEST_ANOVA_F", "TEST_ANOVA_RM", "TEST_ANOVA_WELCH",
		"TEST_BROWN_FORSYTHE", "TEST_KS", "TEST_KRUSKAL_WALLIS", "TEST_MANN_WHITNEY_U", "TEST_SHAPIRO_WILK",
		"TEST_Z_TWO_SAMPLE", "TEST_PAIRED_T", "TEST_PEARSON_R", "TEST_SPEARMAN_R", "TEST_KENDALL_TAU", "TEST_WILCOXON_SR"} {
		for _, ft := range acceptanceFieldTypes() {
			if !ft.IsSet() {
				continue
			}
			fill(op, acceptanceTemplates[op], ft, errors.PULSE_TEST_FIELD_NOT_NUMERIC)
			if field2[op] {
				tmpl := `{"tests":[{"type":"` + op + `","field":"x","field2":"$F"}]}`
				fill(op, tmpl, ft, errors.PULSE_TEST_FIELD2_NOT_NUMERIC)
			}
		}
	}
	return out
}

// TestFieldTypeRefusal_RuntimeRefusesPredictAgrees pins TODO #199: every
// pair the accepted-types table refuses is a coded refusal from Process
// (never a number computed over a dictionary index, a bitmask or raw
// bytes, never an n=0 test), and predict reports exactly one error with
// the same code.
func TestFieldTypeRefusal_RuntimeRefusesPredictAgrees(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	cases := fieldTypeRefusalCases()
	if len(cases) < 116 {
		t.Fatalf("only %d cases; the #199 set alone is 116", len(cases))
	}
	for _, c := range cases {
		mk := func() *types.Request {
			var req types.Request
			if err := json.Unmarshal([]byte(c.body), &req); err != nil {
				t.Fatalf("%s: %v", c.body, err)
			}
			req.Cohort = &types.Cohort{Filename: cohort}
			return &req
		}
		_, rerr := p.Process(ctx, mk())
		var ce *errors.CodedError
		if !stderrors.As(rerr, &ce) || ce.Code != c.code {
			t.Errorf("%s x %s runtime: %v, want %s (%s)", c.op, c.ft, rerr, c.code, c.body)
			continue
		}
		env := predictEnvelope(t, p, fs, cohort, mk())
		if len(env.Errors) != 1 || env.Errors[0].Code != string(c.code) {
			var got []string
			for _, e := range env.Errors {
				got = append(got, e.Code+" "+e.Message)
			}
			t.Errorf("%s x %s predict: %v, want one %s", c.op, c.ft, got, c.code)
			continue
		}
		if env.Errors[0].Message != ce.Message {
			t.Errorf("%s x %s: predict message %q, runtime %q", c.op, c.ft, env.Errors[0].Message, ce.Message)
		}
	}
}

// TestFieldTypeRefusal_SafePairsStillRun pins the other direction: the
// numeric inputs the table accepts still run on both sides.
func TestFieldTypeRefusal_SafePairsStillRun(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	ops := []string{"WIN_LAG", "WIN_LEAD", "WIN_RUNNING_SUM", "WIN_RUNNING_AVG", "WIN_MOVING_AVG", "WIN_EWMA",
		"WIN_PCT_CHANGE", "WIN_DELTA", "FEAT_POLY", "REG_OLS", "REG_GLM", "REG_BAYES_LINEAR", "TEST_T", "TEST_PEARSON_R"}
	safe := []encoding.FieldType{encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32,
		encoding.FieldTypeU64, encoding.FieldTypeF32, encoding.FieldTypeF64, encoding.FieldTypeDate}
	for _, op := range ops {
		for _, ft := range safe {
			v, w, s := acceptanceValues(ft)
			body := strings.NewReplacer("$F", "t_"+ft.String(), "$V", v, "$W", w, "$S", s).Replace(acceptanceTemplates[op])
			mk := func() *types.Request {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatal(err)
				}
				req.Cohort = &types.Cohort{Filename: cohort}
				return &req
			}
			_, rerr := p.Process(ctx, mk())
			var ce *errors.CodedError
			if rerr != nil && !(stderrors.As(rerr, &ce) && acceptanceDataOutcomes[ce.Code]) {
				t.Errorf("%s x %s runtime refused: %v", op, ft, rerr)
			}
			if env := predictEnvelope(t, p, fs, cohort, mk()); len(env.Errors) != 0 {
				t.Errorf("%s x %s predict refused: %s %s", op, ft, env.Errors[0].Code, env.Errors[0].Message)
			}
		}
	}
}
