package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"strconv"
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

// Every union refusal is one rule: for every refusal a union can draw —
// the schema-free shape rules, the merged-schema rules (a member the
// shard-archive merge refuses, a source field that collides, the
// flattened member limit, a join key the merged left side lacks) —
// Predict, Process, ProcessStream, a Compose
// slot and Compose predict all report the same code with the same
// details. A union on a join's left side is read: the join-key rule
// judges the merged schema alike on every surface.
func TestUnion_RefusalsMatchRuntime(t *testing.T) {
	t.Parallel()
	p := unionFixture(t)
	ctx := context.Background()
	conflict := unionRequest("a.pulse", "x")
	conflict.Cohort = &types.Cohort{Filename: "b.pulse"}
	stray := unionRequest()
	stray.Cohort = &types.Cohort{Filename: "a.pulse"}
	stray.UnionSourceField = "wave"
	collision := unionRequest("a.pulse", "x", "b.pulse", "y")
	collision.UnionSourceField = "amount"
	joined := unionRequest("a.pulse", "x", "b.pulse", "y")
	joined.Joins = []*types.JoinSpec{{Right: "b.pulse", On: []types.OnPair{{LeftField: "nope", RightField: "region"}}}}
	tooMany := unionRequest()
	for i := 0; i <= 65535; i++ {
		tooMany.Union = append(tooMany.Union, types.UnionMember{Cohort: &types.Cohort{Filename: "a.pulse"}, Label: strconv.Itoa(i)})
	}
	cases := []struct {
		name    string
		req     *types.Request
		code    errors.Code
		details map[string]any
	}{
		{"cohort and union", conflict, errors.PULSE_UNION_COHORT_CONFLICT, nil},
		{"duplicate label", unionRequest("a.pulse", "x", "b.pulse", "x"), errors.PULSE_UNION_LABEL_INVALID, map[string]any{"reason": "duplicate"}},
		{"empty label", unionRequest("a.pulse", ""), errors.PULSE_UNION_LABEL_INVALID, map[string]any{"reason": "empty"}},
		{"member without cohort", &types.Request{Union: []types.UnionMember{{Label: "x"}}}, errors.PULSE_UNION_MEMBER_INVALID, nil},
		{"source field without union", stray, errors.PULSE_UNION_SOURCE_FIELD_INVALID, map[string]any{"reason": "without_union"}},
		{"member limit", tooMany, errors.PULSE_UNION_MEMBER_LIMIT, map[string]any{"member_count": 65536}},
		{"source field collision", collision, errors.PULSE_UNION_SOURCE_FIELD_INVALID, map[string]any{"reason": "collision"}},
		{"schema mismatch", unionRequest("a.pulse", "source", "nullable.pulse", "twin"), errors.PULSE_UNION_SCHEMA_MISMATCH,
			map[string]any{"dimension": "nullable", "member": 1, "label": "twin", "archive_code": string(errors.PULSE_SHARD_SCHEMA_MISMATCH)}},
		{"union joined", joined, errors.PULSE_JOIN_FIELD_UNKNOWN, map[string]any{"field": "nope", "index": 0}},
	}
	// sameDetails reports whether every wanted detail is present with
	// the wanted value (JSON-normalised, so ints compare with floats).
	sameDetails := func(got map[string]any, want map[string]any) bool {
		for k, v := range want {
			a, _ := json.Marshal(got[k])
			b, _ := json.Marshal(v)
			if string(a) != string(b) {
				return false
			}
		}
		return true
	}
	coded := func(err error) *errors.CodedError {
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			return ce
		}
		return nil
	}
	for _, tc := range cases {
		res, err := p.Predict(ctx, tc.req)
		if err != nil {
			t.Fatalf("%s: Predict: %v", tc.name, err)
		}
		if res.Valid || len(res.Errors) == 0 || res.Errors[0].Code != string(tc.code) || !sameDetails(res.Errors[0].Details, tc.details) {
			t.Errorf("%s: predict = %+v, want %s %v", tc.name, res.Errors, tc.code, tc.details)
		}
		_, perr := p.Process(ctx, tc.req)
		if ce := coded(perr); ce == nil || ce.Code != tc.code || !sameDetails(ce.Details, tc.details) {
			t.Errorf("%s: Process = %v, want %s %v", tc.name, perr, tc.code, tc.details)
		}
		if _, serr := p.ProcessStream(ctx, tc.req); !errors.HasCode(serr, tc.code) {
			t.Errorf("%s: ProcessStream = %v, want %s", tc.name, serr, tc.code)
		}
		// A Compose slot: the runtime's slot fault and Compose predict's
		// carry the code, the details and the slot index alike.
		slot := *tc.req
		slot.Label = "slot"
		composed := &ComposedRequest{Requests: []*Request{&slot}}
		slotDetails := map[string]any{"request": 0}
		for k, v := range tc.details {
			slotDetails[k] = v
		}
		_, cerr := p.Compose(ctx, composed)
		if ce := coded(cerr); ce == nil || ce.Code != tc.code || !sameDetails(ce.Details, slotDetails) {
			t.Errorf("%s: Compose = %v, want %s %v", tc.name, cerr, tc.code, slotDetails)
		}
		env, err := p.PredictCompose(ctx, composed)
		if err != nil {
			t.Fatalf("%s: PredictCompose: %v", tc.name, err)
		}
		if len(env.Errors) == 0 || env.Errors[0].Code != string(tc.code) || !sameDetails(env.Errors[0].Details, slotDetails) {
			t.Errorf("%s: PredictCompose = %+v, want %s %v", tc.name, env.Errors, tc.code, slotDetails)
		}
	}

	// A valid union is read, never refused, by Process and ProcessStream.
	valid := unionRequest("a.pulse", "source", "b.pulse", "twin")
	if _, err := p.Process(ctx, valid); err != nil {
		t.Errorf("Process(valid union) = %v", err)
	}
	// Materialize reads it too: the predicted output columns are the
	// columns the run writes, the source field among them.
	mat := &types.Request{Union: valid.Union, Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "amount"}}}
	env, err := p.PredictMaterialize(ctx, mat)
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("PredictMaterialize(union) = %+v, %v", env, err)
	}
	res, err := p.Materialize(ctx, mat, "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatalf("Materialize(union) = %v", err)
	}
	info, err := p.Inspect(ctx, res.Target)
	if err != nil {
		t.Fatal(err)
	}
	cols := env.Data.(*descriptor.MaterializePredictResult).Columns
	if len(cols) != len(info.Fields) || res.Records != 8 {
		t.Fatalf("predicted %d columns, wrote %d (%d records)", len(cols), len(info.Fields), res.Records)
	}
	for i, c := range cols {
		if f := info.Fields[i]; c.Name != f.Name || c.Type != f.Type {
			t.Errorf("column %d: predicted %s %s, wrote %s %s", i, c.Name, c.Type, f.Name, f.Type)
		}
	}
	if cols[2].Name != types.DefaultUnionSourceField {
		t.Errorf("columns %+v: no source field after the members' fields", cols)
	}
}
