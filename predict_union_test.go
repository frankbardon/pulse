package pulse

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// unionFixture lays out three cohorts on a memory fs: a.pulse (3 rows)
// and b.pulse (5 rows) with one schema, and nullable.pulse (2 rows)
// whose amount is nullable — a pair a shard archive refuses.
func unionFixture(t *testing.T) *Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	write := func(path string, nullable bool, rows int) {
		d := encoding.NewDictionary()
		for _, v := range []string{"n", "s"} {
			if _, err := d.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		s := &encoding.Schema{Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeF64, Nullable: nullable, Description: "Order amount in dollars"},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 8, Description: "Sales region of the order", Dictionary: d},
		}}
		var buf bytes.Buffer
		if err := encoding.WriteHeader(&buf); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteSchema(&buf, s); err != nil {
			t.Fatal(err)
		}
		buf.Write(make([]byte, rows*s.RecordByteSize()))
		if err := afero.WriteFile(fsys, path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.pulse", false, 3)
	write("b.pulse", false, 5)
	write("nullable.pulse", true, 2)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func unionRequest(members ...string) *types.Request {
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: types.DefaultUnionSourceField}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "total"}},
	}
	for i := 0; i+1 < len(members); i += 2 {
		req.Union = append(req.Union, types.UnionMember{Cohort: &types.Cohort{Filename: members[i]}, Label: members[i+1]})
	}
	return req
}

func TestPredict_UnionReadsEveryMember(t *testing.T) {
	t.Parallel()
	p := unionFixture(t)
	res, err := p.Predict(context.Background(), unionRequest("a.pulse", "source", "b.pulse", "twin"))
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !res.Valid || len(res.Errors) > 0 {
		t.Fatalf("union predict refused: %+v", res.Errors)
	}
	if res.RecordCount != 8 || len(res.Union) != 2 || res.Union[1].Label != "twin" || res.Union[1].RecordCount != 5 {
		t.Errorf("record count %d, members %+v", res.RecordCount, res.Union)
	}
	env, err := p.PredictEnvelope(context.Background(), unionRequest("a.pulse", "source", "b.pulse", "twin"))
	if err != nil || len(env.Errors) > 0 || env.Data.(*descriptor.PredictResult).RecordCount != 8 {
		t.Fatalf("PredictEnvelope = %+v, %v", env, err)
	}

	// A member that cannot be opened is an error naming the member.
	if _, err := p.Predict(context.Background(), unionRequest("a.pulse", "source", "missing.pulse", "twin")); err == nil {
		t.Error("an unreadable member was not an error")
	}
}

// Every union refusal is one rule: predict reports exactly the code the
// runtime refuses with, for Process and Materialize alike. Until union
// execution is wired a valid union is refused PULSE_UNION_UNSUPPORTED by
// the runtime — never read as one cohort.
func TestUnion_RefusalsMatchRuntime(t *testing.T) {
	t.Parallel()
	p := unionFixture(t)
	ctx := context.Background()
	conflict := unionRequest("a.pulse", "x")
	conflict.Cohort = &types.Cohort{Filename: "b.pulse"}
	stray := unionRequest()
	stray.Cohort = &types.Cohort{Filename: "a.pulse"}
	stray.UnionSourceField = "wave"
	cases := []struct {
		name string
		req  *types.Request
		code errors.Code
	}{
		{"cohort and union", conflict, errors.PULSE_UNION_COHORT_CONFLICT},
		{"duplicate label", unionRequest("a.pulse", "x", "b.pulse", "x"), errors.PULSE_UNION_LABEL_INVALID},
		{"empty label", unionRequest("a.pulse", ""), errors.PULSE_UNION_LABEL_INVALID},
		{"member without cohort", &types.Request{Union: []types.UnionMember{{Label: "x"}}}, errors.PULSE_UNION_MEMBER_INVALID},
		{"source field without union", stray, errors.PULSE_UNION_SOURCE_FIELD_INVALID},
	}
	for _, tc := range cases {
		res, err := p.Predict(ctx, tc.req)
		if err != nil {
			t.Fatalf("%s: Predict: %v", tc.name, err)
		}
		if res.Valid || len(res.Errors) == 0 || res.Errors[0].Code != string(tc.code) {
			t.Errorf("%s: predict = %+v, want %s", tc.name, res.Errors, tc.code)
		}
		if _, err := p.Process(ctx, tc.req); !errors.HasCode(err, tc.code) {
			t.Errorf("%s: Process = %v, want %s", tc.name, err, tc.code)
		}
	}

	// Schema mismatch: predict refuses it with the archive's dimension.
	res, err := p.Predict(ctx, unionRequest("a.pulse", "source", "nullable.pulse", "twin"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) == 0 || res.Errors[0].Code != string(errors.PULSE_UNION_SCHEMA_MISMATCH) || res.Errors[0].Details["dimension"] != "nullable" {
		t.Errorf("mismatch = %+v, want PULSE_UNION_SCHEMA_MISMATCH dimension nullable", res.Errors)
	}

	valid := unionRequest("a.pulse", "source", "b.pulse", "twin")
	if _, err := p.Process(ctx, valid); !errors.HasCode(err, errors.PULSE_UNION_UNSUPPORTED) {
		t.Errorf("Process(valid union) = %v, want PULSE_UNION_UNSUPPORTED", err)
	}
	if _, err := p.ProcessStream(ctx, valid); !errors.HasCode(err, errors.PULSE_UNION_UNSUPPORTED) {
		t.Errorf("ProcessStream(valid union) = %v, want PULSE_UNION_UNSUPPORTED", err)
	}
	mat := &types.Request{Union: valid.Union}
	env, err := p.PredictMaterialize(ctx, mat)
	if err != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_UNION_UNSUPPORTED) {
		t.Errorf("PredictMaterialize(union) = %+v, %v", env, err)
	}
	_, err = p.Materialize(ctx, mat, "out.pulse", MaterializeOptions{})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_UNION_UNSUPPORTED {
		t.Errorf("Materialize(union) = %v, want PULSE_UNION_UNSUPPORTED", err)
	}
}
