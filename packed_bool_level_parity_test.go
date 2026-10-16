package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
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

// packedBoolLevelCohort imports a cohort whose packed_bool columns
// (pb_flag, pb_grp) each have a categorical twin (cat_flag, cat_grp)
// carrying the SAME levels as dictionary labels "0" / "1" — the labels a
// packed_bool level reads as. Rows pair up for TEST_ANOVA_RM: subject
// subj = row/2 sees grp 0 then grp 1 exactly once (a balanced design).
func packedBoolLevelCohort(t *testing.T) (*pulse.Pulse, afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"x", "y", "subj", "pb_flag", "cat_flag", "pb_grp", "cat_grp"}
	ftypes := []encoding.FieldType{
		encoding.FieldTypeF64, encoding.FieldTypeF64, encoding.FieldTypeCategoricalU8,
		encoding.FieldTypePackedBool, encoding.FieldTypeCategoricalU8,
		encoding.FieldTypePackedBool, encoding.FieldTypeCategoricalU8,
	}
	fields := make([]encoding.Field, len(names))
	off := 0
	for i, n := range names {
		f := encoding.Field{Name: n, Type: ftypes[i], CsvColumnIdx: i, ByteOffset: off}
		if ftypes[i].HasDictionary() {
			f.Dictionary = encoding.NewDictionary()
		}
		if ftypes[i].IsBitPacked() {
			off++
		} else {
			off += ftypes[i].ByteSize()
		}
		fields[i] = f
	}
	var b strings.Builder
	b.WriteString(strings.Join(names, ",") + "\n")
	const rows = 60
	for i := range rows {
		grp := i % 2
		// The flag leans on grp (a real association) without being a
		// function of it, so every contingency cell is populated.
		flag := 0
		if (i*7)%5 < 2+grp {
			flag = 1
		}
		x := float64((i*13)%23)*0.75 + float64(grp)*2.5
		y := float64((i*5)%11)*1.25 + float64(grp)*float64(i%3)
		pb := func(v int) string { return []string{"false", "true"}[v] }
		fmt.Fprintf(&b, "%.4f,%.4f,s%d,%s,%d,%s,%d\n", x, y, i/2, pb(flag), flag, pb(grp), grp)
	}
	if err := afero.WriteFile(fs, "pb.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	job := &pio.ImportJob{Source: csv.NewReader(fs, "pb.csv"), Target: "pb.pulse", Schema: &encoding.Schema{Fields: fields}, FS: fs}
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("import: %v", err)
	}
	return p, fs, "pb.pulse"
}

// packedBoolLevelCases are every test slot the categoricalValue class
// reads a level from: TEST_PROP_Z's outcome and split, the contingency
// axes, and each of the 15 split_by tests. $FLAG / $GRP name the
// packed_bool column or its categorical twin.
var packedBoolLevelCases = []struct{ name, body string }{
	{"TEST_PROP_Z field", `{"type":"TEST_PROP_Z","field":"$FLAG","split_by":"cat_grp","params":{"success":"1"}}`},
	{"TEST_PROP_Z split_by", `{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"$GRP","params":{"success":"1"}}`},
	{"TEST_PROP_Z both", `{"type":"TEST_PROP_Z","field":"$FLAG","split_by":"$GRP","params":{"success":"0"}}`},
	{"TEST_CHISQ rows", `{"type":"TEST_CHISQ","rows":"$FLAG","cols":"cat_grp"}`},
	{"TEST_CHISQ cols", `{"type":"TEST_CHISQ","rows":"cat_flag","cols":"$GRP"}`},
	{"TEST_FISHER_EXACT rows", `{"type":"TEST_FISHER_EXACT","rows":"$FLAG","cols":"cat_grp"}`},
	{"TEST_FISHER_EXACT cols", `{"type":"TEST_FISHER_EXACT","rows":"cat_flag","cols":"$GRP"}`},
	{"TEST_T", `{"type":"TEST_T","field":"x","split_by":"$GRP"}`},
	{"TEST_WELCH", `{"type":"TEST_WELCH","field":"x","split_by":"$GRP"}`},
	{"TEST_ANOVA_F", `{"type":"TEST_ANOVA_F","field":"x","split_by":"$GRP"}`},
	{"TEST_ANOVA_RM", `{"type":"TEST_ANOVA_RM","field":"x","split_by":"$GRP","subject_field":"subj"}`},
	{"TEST_ANOVA_WELCH", `{"type":"TEST_ANOVA_WELCH","field":"x","split_by":"$GRP"}`},
	{"TEST_BROWN_FORSYTHE", `{"type":"TEST_BROWN_FORSYTHE","field":"x","split_by":"$GRP"}`},
	{"TEST_KS", `{"type":"TEST_KS","field":"x","split_by":"$GRP"}`},
	{"TEST_KRUSKAL_WALLIS", `{"type":"TEST_KRUSKAL_WALLIS","field":"x","split_by":"$GRP"}`},
	{"TEST_MANN_WHITNEY_U", `{"type":"TEST_MANN_WHITNEY_U","field":"x","split_by":"$GRP"}`},
	{"TEST_SHAPIRO_WILK", `{"type":"TEST_SHAPIRO_WILK","field":"x","split_by":"$GRP"}`},
	{"TEST_Z_TWO_SAMPLE", `{"type":"TEST_Z_TWO_SAMPLE","field":"x","split_by":"$GRP"}`},
	{"TEST_HOTELLING_T2", `{"type":"TEST_HOTELLING_T2","fields":["x","y"],"split_by":"$GRP"}`},
	{"TEST_MANOVA", `{"type":"TEST_MANOVA","fields":["x","y"],"split_by":"$GRP"}`},
	{"TEST_BOX_M", `{"type":"TEST_BOX_M","fields":["x","y"],"split_by":"$GRP"}`},
}

// TestPackedBoolLevel_TestsMatchCategoricalTwin runs every level-reading
// test slot over a packed_bool column and over its categorical twin
// (dictionary labels "0" / "1"): a packed_bool level reads as "0" / "1"
// through the one shared accessor, so the two results are identical
// once the column names are aligned. A packed_bool row read as missing
// (the silent drop the accessor exists to prevent) changes n and every
// statistic; a refusal fails the run. Predict accepts every request the
// runtime runs.
func TestPackedBoolLevel_TestsMatchCategoricalTwin(t *testing.T) {
	p, fs, cohort := packedBoolLevelCohort(t)
	ctx := context.Background()
	run := func(t *testing.T, body string) []byte {
		t.Helper()
		var req types.Request
		if err := json.Unmarshal([]byte(`{"tests":[`+body+`]}`), &req); err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Cohort = &types.Cohort{Filename: cohort}
		// One label for both runs (the synthesized one names the field).
		req.Tests[0].Label = "level_test"
		if env := predictEnvelope(t, p, fs, cohort, &req); len(env.Errors) != 0 {
			t.Errorf("predict refused %s: %s %s", body, env.Errors[0].Code, env.Errors[0].Message)
		}
		resp, err := p.Process(ctx, &req)
		if err != nil {
			t.Fatalf("process %s: %v", body, err)
		}
		if len(resp.Tests) != 1 {
			t.Fatalf("%s: %d test results, want 1", body, len(resp.Tests))
		}
		out, err := json.Marshal(resp.Tests[0])
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, tc := range packedBoolLevelCases {
		t.Run(tc.name, func(t *testing.T) {
			pb := run(t, strings.NewReplacer("$FLAG", "pb_flag", "$GRP", "pb_grp").Replace(tc.body))
			twin := run(t, strings.NewReplacer("$FLAG", "cat_flag", "$GRP", "cat_grp").Replace(tc.body))
			// Align the echoed column names (pb_* / cat_* -> col_*);
			// every figure and level label must then match.
			norm := func(b []byte) []byte {
				return bytes.ReplaceAll(bytes.ReplaceAll(b, []byte(`"pb_`), []byte(`"col_`)), []byte(`"cat_`), []byte(`"col_`))
			}
			pb, aligned := norm(pb), norm(twin)
			if !bytes.Equal(pb, aligned) {
				t.Errorf("packed_bool result differs from its categorical twin:\npacked_bool: %s\ntwin:        %s", pb, aligned)
			}
		})
	}
}

// TestPackedBoolLevel_PropZSuccessStrict pins TEST_PROP_Z's success on a
// packed_bool outcome: only "0" / "1" (no true / false aliases), and
// anything else is refused by predict and runtime with the same code,
// whose details name the valid levels. A categorical outcome's success
// is not judged against its dictionary (unchanged).
func TestPackedBoolLevel_PropZSuccessStrict(t *testing.T) {
	p, fs, cohort := packedBoolLevelCohort(t)
	ctx := context.Background()
	for _, tc := range []struct {
		field, success string
		refused        bool
	}{
		{"pb_flag", "1", false},
		{"pb_flag", "0", false},
		{"pb_flag", "true", true},
		{"pb_flag", "false", true},
		{"pb_flag", "yes", true},
		{"pb_flag", "01", true},
		{"cat_flag", "maybe", false},
	} {
		t.Run(tc.field+"="+tc.success, func(t *testing.T) {
			body := fmt.Sprintf(`{"tests":[{"type":"TEST_PROP_Z","field":%q,"split_by":"cat_grp","params":{"success":%q}}]}`, tc.field, tc.success)
			mk := func() *types.Request {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatal(err)
				}
				req.Cohort = &types.Cohort{Filename: cohort}
				return &req
			}
			_, rerr := p.Process(ctx, mk())
			env := predictEnvelope(t, p, fs, cohort, mk())
			var ce *errors.CodedError
			if !tc.refused {
				// A categorical's unseen success counts no success: the
				// run may end in a data outcome (a zero pooled rate),
				// never in the success refusal.
				if stderrors.As(rerr, &ce) && ce.Code == errors.PULSE_TEST_SUCCESS_VALUE_MISSING {
					t.Fatalf("runtime refused: %v", rerr)
				}
				if len(env.Errors) != 0 {
					t.Fatalf("predict refused: %s %s", env.Errors[0].Code, env.Errors[0].Message)
				}
				return
			}
			if !stderrors.As(rerr, &ce) || ce.Code != errors.PULSE_TEST_SUCCESS_VALUE_MISSING {
				t.Fatalf("runtime: got %v, want %s", rerr, errors.PULSE_TEST_SUCCESS_VALUE_MISSING)
			}
			levels, _ := ce.Details["valid_levels"].([]string)
			if strings.Join(levels, ",") != "0,1" {
				t.Errorf("runtime details.valid_levels = %v, want [0 1]", ce.Details["valid_levels"])
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
				t.Fatalf("predict errors %+v, want exactly the runtime's %s %q", env.Errors, ce.Code, ce.Message)
			}
		})
	}
}

// TestPackedBoolLevel_AnovaRMSubjectRefused pins the one level slot the
// widening leaves categorical-only: a two-level subject id is no
// subject, so TEST_ANOVA_RM refuses a packed_bool subject_field on both
// sides with the table's code.
func TestPackedBoolLevel_AnovaRMSubjectRefused(t *testing.T) {
	p, fs, cohort := packedBoolLevelCohort(t)
	mk := func() *types.Request {
		var req types.Request
		if err := json.Unmarshal([]byte(`{"tests":[{"type":"TEST_ANOVA_RM","field":"x","split_by":"cat_flag","subject_field":"pb_grp"}]}`), &req); err != nil {
			t.Fatal(err)
		}
		req.Cohort = &types.Cohort{Filename: cohort}
		return &req
	}
	_, rerr := p.Process(context.Background(), mk())
	var ce *errors.CodedError
	if !stderrors.As(rerr, &ce) || ce.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED {
		t.Fatalf("runtime: got %v, want %s", rerr, errors.PULSE_FIELD_TYPE_UNSUPPORTED)
	}
	env := predictEnvelope(t, p, fs, cohort, mk())
	if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_FIELD_TYPE_UNSUPPORTED) {
		t.Fatalf("predict errors %+v, want one %s", env.Errors, errors.PULSE_FIELD_TYPE_UNSUPPORTED)
	}
}
