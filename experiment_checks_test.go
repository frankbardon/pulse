package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// U48 E1-S1: the sample-ratio check as one test — TEST_CHISQ goodness
// of fit (params.expected) and the one-proportion TEST_PROP_Z
// (params.p0). End to end: the showcase SRM shape equals the
// OVERLAY_CHISQ_VS_POP facet run against a 50/50 design cohort; every
// new refusal is raised by predict and the runtime alike.

// importArmCohort writes a one-column categorical cohort ("arm") with
// counts[i] rows of levels[i], interleaved so no level is contiguous.
func importArmCohort(t *testing.T, p *pulse.Pulse, fs afero.Fs, target string, levels []string, counts []int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("arm\n")
	left := append([]int(nil), counts...)
	for more := true; more; {
		more = false
		for i, l := range levels {
			if left[i] > 0 {
				b.WriteString(l + "\n")
				left[i]--
				more = true
			}
		}
	}
	src := target + ".csv"
	if err := afero.WriteFile(fs, src, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "arm", Type: encoding.FieldTypeCategoricalU16, CsvColumnIdx: 0, Dictionary: encoding.NewDictionary()},
	}}
	job := &pio.ImportJob{Source: csv.NewReader(fs, src), Target: target, Schema: schema, FS: fs}
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("import %s: %v", target, err)
	}
}

// TestExperimentSRM_GOFEqualsChiSqVsPopFacet: on the showcase shape
// (new_page 20,087 / old_page 19,913) TEST_CHISQ with params.expected
// 50/50 reports χ² 0.757, df 1, p 0.384 — the very statistic and
// p-value of the FacetSchema OVERLAY_CHISQ_VS_POP run against a 1:1
// design cohort, the detour it replaces. One-proportion TEST_PROP_Z
// against p0 = 0.5 is the same test: z² = χ², equal p-values.
func TestExperimentSRM_GOFEqualsChiSqVsPopFacet(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	importArmCohort(t, p, fs, "ab.pulse", []string{"old_page", "new_page"}, []int{19913, 20087})
	importArmCohort(t, p, fs, "design.pulse", []string{"old_page", "new_page"}, []int{1, 1})

	var req types.Request
	if err := json.Unmarshal([]byte(`{"cohort":{"filename":"ab.pulse"},"tests":[
		{"type":"TEST_CHISQ","rows":"arm","params":{"expected":{"new_page":0.5,"old_page":0.5}},"label":"srm"},
		{"type":"TEST_PROP_Z","field":"arm","params":{"success":"new_page","p0":0.5},"label":"srm_prop"}]}`), &req); err != nil {
		t.Fatal(err)
	}
	if env := predictEnvelope(t, p, fs, "ab.pulse", &req); len(env.Errors) != 0 {
		t.Fatalf("predict: %+v", env.Errors)
	}
	resp, err := p.Process(ctx, &req)
	if err != nil {
		t.Fatal(err)
	}
	gof, prop := resp.Tests[0], resp.Tests[1]
	if gof.Variant != "goodness_of_fit" || gof.DF != 1 ||
		math.Abs(gof.Statistic-0.757) > 5e-4 || math.Abs(gof.PValue-0.384) > 5e-4 {
		t.Fatalf("GOF %s χ² %v df %v p %v, want goodness_of_fit 0.757 / 1 / 0.384", gof.Variant, gof.Statistic, gof.DF, gof.PValue)
	}

	facet, err := p.FacetSchema(ctx, &pulse.FacetRequest{
		Cohort: &types.Cohort{Filename: "ab.pulse"},
		Fields: []string{"arm"},
		Overlays: []types.OverlaySpec{{
			Name: "srm", Kind: types.OverlayKindChiSqVsPop, Scope: types.OverlayScopeGroup,
			Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "design.pulse"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := facet.Overlays[0].Summary
	if math.Abs(gof.Statistic-*sum.Statistic) > 1e-9*gof.Statistic || math.Abs(gof.PValue-*sum.PValue) > 1e-9 || gof.DF != sum.Parameters["df"] {
		t.Fatalf("GOF χ² %v p %v df %v; OVERLAY_CHISQ_VS_POP %v %v %v", gof.Statistic, gof.PValue, gof.DF, *sum.Statistic, *sum.PValue, sum.Parameters["df"])
	}

	if prop.Variant != "one_proportion" || math.Abs(prop.Statistic*prop.Statistic-gof.Statistic) > 1e-9 || math.Abs(prop.PValue-gof.PValue) > 1e-9 {
		t.Fatalf("one-proportion %s z %v p %v; want z² = χ² %v and p %v", prop.Variant, prop.Statistic, prop.PValue, gof.Statistic, gof.PValue)
	}
}

// TestExperimentChecks_PredictMatchesRuntime: every params refusal of
// the goodness-of-fit and one-proportion forms is raised by predict and
// the runtime with the same code and message. The data-dependent
// refusal (an observed level expected omits) is the runtime's alone:
// predict accepts the request, as for any data-dependent test refusal.
func TestExperimentChecks_PredictMatchesRuntime(t *testing.T) {
	p, fs, cohort := packedBoolLevelCohort(t)
	ctx := context.Background()
	cases := []struct {
		name, test string
		code       errors.Code
		predict    bool // predict raises it too
	}{
		{"gof sum", `{"type":"TEST_CHISQ","rows":"cat_flag","params":{"expected":{"0":0.5,"1":0.4}}}`, errors.PULSE_TEST_EXPECTED_INVALID, true},
		{"gof one label", `{"type":"TEST_CHISQ","rows":"cat_flag","params":{"expected":{"0":1}}}`, errors.PULSE_TEST_EXPECTED_INVALID, true},
		{"gof non-positive", `{"type":"TEST_CHISQ","rows":"cat_flag","params":{"expected":{"0":0,"1":1}}}`, errors.PULSE_TEST_EXPECTED_INVALID, true},
		{"gof with cols", `{"type":"TEST_CHISQ","rows":"cat_flag","cols":"cat_grp","params":{"expected":{"0":0.5,"1":0.5}}}`, errors.PULSE_TEST_EXPECTED_INVALID, true},
		{"gof packed_bool label", `{"type":"TEST_CHISQ","rows":"pb_flag","params":{"expected":{"true":0.5,"false":0.5}}}`, errors.PULSE_TEST_EXPECTED_INVALID, true},
		{"gof numeric rows", `{"type":"TEST_CHISQ","rows":"x","params":{"expected":{"0":0.5,"1":0.5}}}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED, true},
		{"gof missing data label", `{"type":"TEST_CHISQ","rows":"cat_flag","params":{"expected":{"1":0.5,"2":0.5}}}`, errors.PULSE_TEST_EXPECTED_LABEL_MISSING, false},
		{"p0 range", `{"type":"TEST_PROP_Z","field":"cat_flag","params":{"success":"1","p0":1.2}}`, errors.PULSE_TEST_P0_INVALID, true},
		{"p0 zero", `{"type":"TEST_PROP_Z","field":"pb_flag","params":{"success":"1","p0":0}}`, errors.PULSE_TEST_P0_INVALID, true},
		{"p0 with split_by", `{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"cat_grp","params":{"success":"1","p0":0.5}}`, errors.PULSE_TEST_P0_INVALID, true},
		{"no field", `{"type":"TEST_PROP_Z","split_by":"cat_grp","params":{"success":"1"}}`, errors.PROCESSING_CONFIG, true},
		{"two-sample without split_by", `{"type":"TEST_PROP_Z","field":"cat_flag","params":{"success":"1"}}`, errors.PROCESSING_CONFIG, true},
		// A categorical success names a dictionary entry of the outcome,
		// both forms (cat_flag's levels are "0" / "1").
		{"success not a dictionary level", `{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"cat_grp","params":{"success":"yes"}}`, errors.PULSE_TEST_SUCCESS_VALUE_MISSING, true},
		{"success not a dictionary level, one-proportion", `{"type":"TEST_PROP_Z","field":"cat_flag","params":{"success":"true","p0":0.5}}`, errors.PULSE_TEST_SUCCESS_VALUE_MISSING, true},
		{"success packed_bool alias", `{"type":"TEST_PROP_Z","field":"pb_flag","split_by":"cat_grp","params":{"success":"true"}}`, errors.PULSE_TEST_SUCCESS_VALUE_MISSING, true},
		// U48 E1-S3: params.reference names a split_by level.
		{"reference not a dictionary level", `{"type":"TEST_WELCH","field":"x","split_by":"cat_grp","params":{"reference":"control"}}`, errors.PULSE_TEST_REFERENCE_INVALID, true},
		{"reference packed_bool alias", `{"type":"TEST_MANN_WHITNEY_U","field":"x","split_by":"pb_grp","params":{"reference":"true"}}`, errors.PULSE_TEST_REFERENCE_INVALID, true},
		{"reference z two-sample", `{"type":"TEST_Z_TWO_SAMPLE","field":"x","split_by":"cat_grp","params":{"reference":"2"}}`, errors.PULSE_TEST_REFERENCE_INVALID, true},
		{"reference prop_z", `{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"pb_grp","params":{"success":"1","reference":"yes"}}`, errors.PULSE_TEST_REFERENCE_INVALID, true},
		{"reference one-sample t", `{"type":"TEST_T","field":"x","params":{"reference":"0"}}`, errors.PULSE_TEST_REFERENCE_INVALID, true},
		{"reference one-proportion", `{"type":"TEST_PROP_Z","field":"cat_flag","params":{"success":"1","p0":0.5,"reference":"0"}}`, errors.PULSE_TEST_REFERENCE_INVALID, true},
		{"reference two-sample t", `{"type":"TEST_T","field":"x","split_by":"cat_grp","params":{"reference":"9"}}`, errors.PULSE_TEST_REFERENCE_INVALID, true},
		// A missing required slot is the constructor's PROCESSING_CONFIG,
		// message for message, on every generic test.
		{"mann-whitney no field", `{"type":"TEST_MANN_WHITNEY_U","split_by":"cat_grp"}`, errors.PROCESSING_CONFIG, true},
		{"mann-whitney no split_by", `{"type":"TEST_MANN_WHITNEY_U","field":"x"}`, errors.PROCESSING_CONFIG, true},
		{"welch no split_by", `{"type":"TEST_WELCH","field":"x"}`, errors.PROCESSING_CONFIG, true},
		{"z two-sample no field", `{"type":"TEST_Z_TWO_SAMPLE","split_by":"cat_grp"}`, errors.PROCESSING_CONFIG, true},
		{"anova no split_by", `{"type":"TEST_ANOVA_F","field":"x"}`, errors.PROCESSING_CONFIG, true},
		{"paired t no field2", `{"type":"TEST_PAIRED_T","field":"x"}`, errors.PROCESSING_CONFIG, true},
		{"pearson no field2", `{"type":"TEST_PEARSON_R","field":"x"}`, errors.PROCESSING_CONFIG, true},
		{"spearman no field2", `{"type":"TEST_SPEARMAN_R","field":"x"}`, errors.PROCESSING_CONFIG, true},
		{"anova rm no subject", `{"type":"TEST_ANOVA_RM","field":"x","split_by":"cat_grp"}`, errors.PROCESSING_CONFIG, true},
		{"shapiro no field", `{"type":"TEST_SHAPIRO_WILK"}`, errors.PROCESSING_CONFIG, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var req types.Request
			if err := json.Unmarshal([]byte(`{"tests":[`+c.test+`]}`), &req); err != nil {
				t.Fatal(err)
			}
			req.Cohort = &types.Cohort{Filename: cohort}
			_, err := p.Process(ctx, &req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("runtime: %v, want %s", err, c.code)
			}
			env := predictEnvelope(t, p, fs, cohort, &req)
			if !c.predict {
				if len(env.Errors) != 0 {
					t.Fatalf("predict refused a data-dependent case: %+v", env.Errors)
				}
				return
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(c.code) || env.Errors[0].Message != ce.Message {
				t.Fatalf("predict %+v, runtime %s %q", env.Errors, ce.Code, ce.Message)
			}
		})
	}

	// Post-tests: a missing required slot is the post-test
	// constructor's PROCESSING_CONFIG on both sides, message for message.
	for _, test := range []string{
		`{"type":"TEST_PEARSON_R","field":"m"}`,
		`{"type":"TEST_ANOVA_F","field":"m"}`,
		`{"type":"TEST_TREND"}`,
		`{"type":"TEST_KS","split_by":"cat_grp"}`,
	} {
		var req types.Request
		if err := json.Unmarshal([]byte(`{"groups":[{"type":"GROUP_CATEGORY","field":"cat_grp"}],"aggregations":[{"type":"AGG_SUM","field":"x","label":"m"}],"post_tests":[`+test+`]}`), &req); err != nil {
			t.Fatal(err)
		}
		req.Cohort = &types.Cohort{Filename: cohort}
		_, err := p.Process(ctx, &req)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG {
			t.Errorf("post %s runtime: %v, want PROCESSING_CONFIG", test, err)
			continue
		}
		env := predictEnvelope(t, p, fs, cohort, &req)
		if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
			t.Errorf("post %s: predict %+v, runtime %s %q", test, env.Errors, ce.Code, ce.Message)
		}
	}

	// The accepted forms: predict and the runtime both accept them.
	for _, test := range []string{
		`{"type":"TEST_CHISQ","rows":"pb_flag","params":{"expected":{"0":0.5,"1":0.5}}}`,
		`{"type":"TEST_CHISQ","rows":"cat_flag","params":{"expected":{"0":0.25,"1":0.5,"9":0.25}}}`,
		`{"type":"TEST_PROP_Z","field":"pb_flag","params":{"success":"1","p0":0.4}}`,
		`{"type":"TEST_PROP_Z","field":"cat_flag","params":{"success":"0","p0":0.4}}`,
		`{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"cat_grp","params":{"success":"1"}}`,
	} {
		var req types.Request
		if err := json.Unmarshal([]byte(`{"tests":[`+test+`]}`), &req); err != nil {
			t.Fatal(err)
		}
		req.Cohort = &types.Cohort{Filename: cohort}
		if env := predictEnvelope(t, p, fs, cohort, &req); len(env.Errors) != 0 {
			t.Errorf("predict refused %s: %+v", test, env.Errors)
		}
		if _, err := p.Process(ctx, &req); err != nil {
			t.Errorf("runtime refused %s: %v", test, err)
		}
	}
}

// TestExperimentChecks_SuccessLevelOverShardArchive: over a shard
// archive a categorical success is judged against the canonical
// schema's dictionary — the union of every shard's — so a level only a
// later shard holds is accepted (and counted) by predict and the
// runtime alike, and a level no shard holds is refused by both with
// the same code and message.
func TestExperimentChecks_SuccessLevelOverShardArchive(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	importArmCohort(t, p, fs, "s0.pulse", []string{"no"}, []int{10})
	importArmCohort(t, p, fs, "s1.pulse", []string{"yes", "no"}, []int{6, 4})
	if _, err := p.CreateShardArchive(ctx, "archive.pulse", []string{"s0.pulse", "s1.pulse"}); err != nil {
		t.Fatal(err)
	}
	request := func(success string) *types.Request {
		var req types.Request
		if err := json.Unmarshal([]byte(`{"cohort":{"filename":"archive.pulse"},"tests":[{"type":"TEST_PROP_Z","field":"arm","params":{"success":`+strconv.Quote(success)+`,"p0":0.5}}]}`), &req); err != nil {
			t.Fatal(err)
		}
		return &req
	}

	res, err := p.Predict(ctx, request("yes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("predict refused a level only the second shard holds: %+v", res.Errors)
	}
	resp, err := p.Process(ctx, request("yes"))
	if err != nil {
		t.Fatalf("runtime refused a level only the second shard holds: %v", err)
	}
	if got := resp.Tests[0].Details["successes"]; got != int64(6) {
		t.Fatalf("successes %v (%T), want 6", got, got)
	}

	_, err = p.Process(ctx, request("Yes"))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_SUCCESS_VALUE_MISSING {
		t.Fatalf("runtime: %v, want PULSE_TEST_SUCCESS_VALUE_MISSING", err)
	}
	res, err = p.Predict(ctx, request("Yes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 || res.Errors[0].Code != string(ce.Code) || res.Errors[0].Message != ce.Message {
		t.Fatalf("predict %+v, runtime %s %q", res.Errors, ce.Code, ce.Message)
	}
}
