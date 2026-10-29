package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// PredictMaterialize: the no-execute verdict on a materialize request —
// the output schema the run writes, every static refusal with the run's
// code and details, and the data-dependent risks — read off the source's
// header and schema only.

// predictMat runs PredictMaterialize and returns the envelope and its
// result.
func predictMat(t *testing.T, p *Pulse, req *Request) (*descriptor.Envelope, *descriptor.MaterializePredictResult) {
	t.Helper()
	env, err := p.PredictMaterialize(context.Background(), req)
	if err != nil {
		t.Fatalf("PredictMaterialize: %v", err)
	}
	res, ok := env.Data.(*descriptor.MaterializePredictResult)
	if !ok {
		t.Fatalf("Data is %T, want *descriptor.MaterializePredictResult", env.Data)
	}
	if res.Valid != (len(env.Errors) == 0) {
		t.Fatalf("Valid %v with %d errors", res.Valid, len(env.Errors))
	}
	return env, res
}

// assertPredictedSchema checks the predicted columns against the
// written cohort's schema field for field.
func assertPredictedSchema(t *testing.T, got []descriptor.MaterializePredictColumn, written *encoding.Schema) {
	t.Helper()
	if len(got) != len(written.Fields) {
		t.Fatalf("predicted %d columns, run wrote %d (%v)", len(got), len(written.Fields), fieldNames(written))
	}
	for i, f := range written.Fields {
		c := got[i]
		if c.Name != f.Name || c.Type != f.Type.String() || c.Nullable != f.Nullable || c.Description != f.Description {
			t.Errorf("column %d predicted {%s %s nullable=%v %q}, written {%s %s nullable=%v %q}",
				i, c.Name, c.Type, c.Nullable, c.Description, f.Name, f.Type, f.Nullable, f.Description)
		}
		var want []string
		if f.Type.HasDictionary() && f.Dictionary != nil {
			want = f.Dictionary.Values()
		}
		var have []string
		if c.Dictionary != nil {
			have = c.Dictionary.Values
			if c.Dictionary.TotalEntries != len(want) {
				t.Errorf("column %s dictionary total %d, written %d", f.Name, c.Dictionary.TotalEntries, len(want))
			}
		}
		if !slices.Equal(have, want) {
			t.Errorf("column %s dictionary predicted %v, written %v", f.Name, have, want)
		}
		if f.Type.IsDecimal() {
			if c.Precision == nil || c.Scale == nil || *c.Precision != f.Precision || *c.Scale != f.Scale {
				t.Errorf("column %s decimal shape predicted %v/%v, written %d/%d", f.Name, c.Precision, c.Scale, f.Precision, f.Scale)
			}
		} else if c.Precision != nil || c.Scale != nil {
			t.Errorf("column %s carries a decimal shape", f.Name)
		}
	}
}

// TestPredictMaterialize_SchemaMatchesRun: across an absent output (a
// single-file and a shard-archive source), a columns list (renamed,
// redescribed, nullability overridden), an exclude list, a pure reshape
// and an inheriting ATTR_PREVIOUS_STATE column, the predicted output
// schema and derived column names are exactly what the run writes.
func TestPredictMaterialize_SchemaMatchesRun(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		src  CohortBuilderOptions
		req  func() *Request
	}{
		{"absent output", CohortBuilderOptions{}, func() *Request { return matRequest("src.pulse") }},
		{"absent output, archive", CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 5}}, func() *Request { return matRequest("src.pulse") }},
		{"columns", CohortBuilderOptions{}, func() *Request {
			return matOutputReq(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
				{From: "rn", As: "row"},
				{From: "region", As: "area"},
				{From: "price"},
				{From: "tags"},
				{From: "id", Description: strPtr("Row key carried from the source cohort"), Nullable: boolPtr(true)},
				{From: "LOG_spend", Nullable: boolPtr(true)},
			}})
		}},
		{"exclude", CohortBuilderOptions{}, func() *Request {
			return matOutputReq(&types.MaterializeOutput{Exclude: []string{"tags", "LOG_spend", "level"}})
		}},
		{"pure reshape", CohortBuilderOptions{}, func() *Request {
			return &Request{
				Cohort:    &types.Cohort{Filename: "src.pulse"},
				Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"south"}}},
				Output:    &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{{From: "spend", As: "amount"}, {From: "id"}}},
			}
		}},
		{"previous state inherits", CohortBuilderOptions{}, func() *Request {
			return &Request{
				Cohort: &types.Cohort{Filename: "src.pulse"},
				Attributes: []*types.Attribute{
					{Type: types.ATTR_PREVIOUS_STATE, Field: "region", SubjectField: "week", OrderField: "day", Label: "prev_region"},
					{Type: types.ATTR_ZSCORE, Field: "spend", Label: "z"},
				},
			}
		}},
		{"previous state renamed", CohortBuilderOptions{}, func() *Request {
			return &Request{
				Cohort: &types.Cohort{Filename: "src.pulse"},
				Attributes: []*types.Attribute{
					{Type: types.ATTR_PREVIOUS_STATE, Field: "region", SubjectField: "week", OrderField: "day", Label: "prev_region"},
				},
				Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{{From: "id"}, {From: "prev_region", As: "was"}}},
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p, _ := matPulse(t, Options{})
			buildMatSource(t, p, "src.pulse", c.src)
			env, pred := predictMat(t, p, c.req())
			if !pred.Valid {
				for _, e := range env.Errors {
					t.Errorf("predict refused: %s %s %v", e.Code, e.Message, e.Details)
				}
				t.FailNow()
			}
			res, err := p.Materialize(context.Background(), c.req(), "out.pulse", MaterializeOptions{})
			if err != nil {
				t.Fatalf("Materialize: %v", err)
			}
			schema, _ := readAll(t, p, "out.pulse")
			assertPredictedSchema(t, pred.Columns, schema)
			if !slices.Equal(pred.DerivedColumns, res.DerivedColumns) {
				t.Errorf("DerivedColumns predicted %v, run %v", pred.DerivedColumns, res.DerivedColumns)
			}
			// The inheriting column is the state field's categorical,
			// dictionary and all — never the f64 fallback.
			for _, col := range pred.Columns {
				if col.From == "prev_region" && (col.Type != "categorical_u8" || col.Dictionary == nil || col.Dictionary.TotalEntries != 4 || !col.Derived) {
					t.Errorf("previous-state column %+v, want a derived categorical_u8 with region's 4 entries", col)
				}
			}
			if pred.SchemaInfo == nil || pred.SchemaInfo.FieldCount != len(matSchema().Fields) {
				t.Errorf("SchemaInfo = %+v, want the source's %d fields", pred.SchemaInfo, len(matSchema().Fields))
			}
		})
	}
}

// TestPredictMaterialize_RefusalsMatchRun: every static refusal a
// materialize run raises — the slot gates, NO_DERIVED, OUTPUT_UNDECLARED,
// the output block's refusals, an over-long written description and
// Process's own pre-flight — is predict's first error with the run's
// code, message and details; nothing is written by either.
func TestPredictMaterialize_RefusalsMatchRun(t *testing.T) {
	t.Parallel()

	p, fsys := matPulse(t, Options{Extensions: Extensions{
		Features: []FeatureRegistration{{Name: "FEAT_ACME_DOUBLE", Factory: matStubFeature, Streamable: false}},
	}})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	src := func() *types.Cohort { return &types.Cohort{Filename: "src.pulse"} }
	logSpend := func() []*types.Feature { return []*types.Feature{{Type: types.FEAT_LOG, Field: "spend"}} }
	out := func(o *types.MaterializeOutput) func() *Request { return func() *Request { return matOutputReq(o) } }
	col := func(from, as string) types.MaterializeOutputColumn {
		return types.MaterializeOutputColumn{From: from, As: as}
	}
	cases := []struct {
		name string
		code errors.Code
		req  func() *Request
	}{
		{"slot unsupported", errors.PULSE_MATERIALIZE_SLOT_UNSUPPORTED, func() *Request {
			return &Request{Cohort: src(), Features: logSpend(), Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}}
		}},
		{"unknown params key", "", func() *Request {
			return &Request{Cohort: src(), Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "spend", Params: json.RawMessage(`{"bse":2}`)}}}
		}},
		{"no derived", errors.PULSE_MATERIALIZE_NO_DERIVED, func() *Request {
			return &Request{Cohort: src(), Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north"}}}}
		}},
		{"undeclared extension feature", errors.PULSE_MATERIALIZE_OUTPUT_UNDECLARED, func() *Request {
			return &Request{Cohort: src(), Features: append(logSpend(), &types.Feature{Type: "FEAT_ACME_DOUBLE", Field: "spend"})}
		}},
		{"undeclared built-in feature", errors.PULSE_MATERIALIZE_OUTPUT_UNDECLARED, func() *Request {
			return &Request{Cohort: src(), Features: []*types.Feature{{Type: types.FEAT_POLY, Field: "spend", Params: json.RawMessage(`{"degree":1}`)}}}
		}},
		{"output both lists", errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			out(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{col("id", "")}, Exclude: []string{"day"}})},
		{"output empty columns", errors.PULSE_MATERIALIZE_OUTPUT_INVALID, out(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{}})},
		{"output from missing", errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			out(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{col("id", ""), col("", "x")}})},
		{"output excludes every column", errors.PULSE_MATERIALIZE_OUTPUT_INVALID,
			out(&types.MaterializeOutput{Exclude: []string{"id", "region", "week", "spend", "level", "flag", "day", "price", "tags", "LOG_spend", "rn"}})},
		{"output unknown column", errors.PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN,
			out(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{col("id", ""), col("spnd", "")}})},
		{"output duplicate", errors.PULSE_MATERIALIZE_OUTPUT_DUPLICATE,
			out(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{col("id", ""), col("week", "id")}})},
		{"output description too long", errors.PULSE_IMPORT_DESCRIPTION_TOO_LONG,
			out(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
				col("id", ""), {From: "week", Description: strPtr(strings.Repeat("w", encoding.MaxDescriptionBytes+1))}}})},
		{"unknown feature field", "", func() *Request {
			return &Request{Cohort: src(), Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "nope"}}}
		}},
		{"bad zone", "", func() *Request {
			return &Request{Cohort: src(), TimeZone: "Mars/Olympus", Features: logSpend()}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, runErr := p.Materialize(context.Background(), c.req(), "out.pulse", MaterializeOptions{})
			var ce *errors.CodedError
			if !stderrors.As(runErr, &ce) || (c.code != "" && ce.Code != c.code) {
				t.Fatalf("run error %v, want %s", runErr, c.code)
			}
			assertAbsent(t, fsys, "out.pulse")
			env, pred := predictMat(t, p, c.req())
			if pred.Valid || len(env.Errors) == 0 {
				t.Fatalf("predict accepted a request the run refuses with %s", ce.Code)
			}
			first := env.Errors[0]
			if first.Code != string(ce.Code) || first.Message != ce.Message || !reflect.DeepEqual(first.Details, ce.Details) {
				t.Errorf("predict first error {%s %q %v}, run {%s %q %v}", first.Code, first.Message, first.Details, ce.Code, ce.Message, ce.Details)
			}
			if c.code != "" && len(pred.Columns) != 0 {
				// A materialize gate stops the layout; Process's own
				// pre-flight findings leave it resolvable.
				t.Errorf("refused layout still reports columns %v", pred.Columns)
			}
			assertAbsent(t, fsys, "out.pulse")
		})
	}
}

// TestPredictMaterialize_ReadsNoRecord: a source whose record bytes are
// truncated mid-record or overwritten with garbage predicts exactly as
// the intact one — header and schema are all predict reads.
func TestPredictMaterialize_ReadsNoRecord(t *testing.T) {
	t.Parallel()

	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	raw, err := afero.ReadFile(fsys, "src.pulse")
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := p.Open(context.Background(), "src.pulse")
	if err != nil {
		t.Fatal(err)
	}
	stride := cohort.Schema().RecordByteSize()
	payload := len(raw) - int(stride)*12
	if payload <= 0 {
		t.Fatalf("payload offset %d from %d bytes, stride %d", payload, len(raw), stride)
	}
	truncated := slices.Clone(raw[:len(raw)-int(stride)/2])
	garbage := slices.Clone(raw)
	for i := payload; i < len(garbage); i++ {
		garbage[i] = 0xFF
	}
	// Same base name, so the derived descriptions (which name the
	// source) match the intact source's.
	for dir, b := range map[string][]byte{"truncated": truncated, "garbage": garbage} {
		if err := afero.WriteFile(fsys, dir+"/src.pulse", b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	req := func(dir string) *Request {
		r := matOutputReq(&types.MaterializeOutput{Exclude: []string{"tags"}})
		r.Cohort = &types.Cohort{Filename: "src.pulse", DataDir: dir}
		return r
	}
	_, want := predictMat(t, p, req(""))
	if !want.Valid {
		t.Fatal("intact source refused")
	}
	for _, file := range []string{"truncated", "garbage"} {
		env, got := predictMat(t, p, req(file))
		if !got.Valid {
			t.Fatalf("%s: predict refused %+v", file, env.Errors)
		}
		if !reflect.DeepEqual(got.Columns, want.Columns) || !slices.Equal(got.DerivedColumns, want.DerivedColumns) {
			t.Errorf("%s: predicted %+v, intact source %+v", file, got.Columns, want.Columns)
		}
	}
}

// TestPredictMaterialize_Risks: a nullable: false column over a column
// that may hold a null (a nullable source field, a derived column) is a
// risk naming the run's PULSE_MATERIALIZE_OUTPUT_NULL; over a
// non-nullable source field it is none — and the run agrees: the risky
// column with a null fails, the safe one writes.
func TestPredictMaterialize_Risks(t *testing.T) {
	t.Parallel()

	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	req := func(cols ...types.MaterializeOutputColumn) *Request {
		return matOutputReq(&types.MaterializeOutput{Columns: cols})
	}
	notNull := func(from string) types.MaterializeOutputColumn {
		return types.MaterializeOutputColumn{From: from, Nullable: boolPtr(false)}
	}
	_, pred := predictMat(t, p, req(notNull("id"), notNull("spend"), notNull("rn")))
	want := []descriptor.MaterializeRisk{
		{Column: "spend", Path: "output.columns[1].nullable", Code: string(errors.PULSE_MATERIALIZE_OUTPUT_NULL)},
		{Column: "rn", Path: "output.columns[2].nullable", Code: string(errors.PULSE_MATERIALIZE_OUTPUT_NULL)},
	}
	if len(pred.Risks) != len(want) {
		t.Fatalf("risks = %+v, want %d", pred.Risks, len(want))
	}
	for i, r := range pred.Risks {
		if r.Column != want[i].Column || r.Path != want[i].Path || r.Code != want[i].Code || r.Check == "" {
			t.Errorf("risk %d = %+v, want %+v", i, r, want[i])
		}
	}
	// The run settles them on the records: spend holds a null.
	_, err := p.Materialize(context.Background(), req(notNull("id"), notNull("spend")), "risky.pulse", MaterializeOptions{})
	assertCode(t, err, errors.PULSE_MATERIALIZE_OUTPUT_NULL)
	if _, err := p.Materialize(context.Background(), req(notNull("id")), "safe.pulse", MaterializeOptions{}); err != nil {
		t.Fatalf("a nullable: false over a non-nullable field failed: %v", err)
	}
	_, none := predictMat(t, p, req(notNull("id")))
	if none.Risks == nil || len(none.Risks) != 0 {
		t.Errorf("risks = %#v, want an empty list", none.Risks)
	}
}

// TestPredictMaterialize_DescriptionWarningsFollowOutput: the
// description-quality warnings are the WRITTEN schema's, as the run
// warns — a weak source description the output renames carries its
// warning under the output name, a redescribed column none — and the
// instance's Strict promotes them into errors.
func TestPredictMaterialize_DescriptionWarningsFollowOutput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	req := func() *Request {
		return matOutputReq(&types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
			{From: "id", Description: strPtr("tbd")},
			{From: "week", Description: strPtr("Week number of the record, redescribed")},
		}})
	}
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	res, err := p.Materialize(ctx, req(), "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var runFields []any
	for _, w := range res.Warnings {
		if w.Code == errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY {
			runFields = append(runFields, w.Details["field"])
		}
	}
	env, pred := predictMat(t, p, req())
	if !pred.Valid {
		t.Fatalf("refused: %+v", env.Errors)
	}
	var predFields []any
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY) {
			predFields = append(predFields, w.Details["field"])
		}
	}
	if !reflect.DeepEqual(predFields, runFields) || len(runFields) != 1 || runFields[0] != "id" {
		t.Errorf("description warnings predicted %v, run %v; want [id]", predFields, runFields)
	}

	strict, _ := matPulse(t, Options{Strict: true, EchoRequest: true})
	buildMatSource(t, strict, "src.pulse", CohortBuilderOptions{})
	env, pred = predictMat(t, strict, req())
	if pred.Valid || len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY) || len(env.Warnings) != 0 {
		t.Errorf("Strict: valid %v errors %+v warnings %+v", pred.Valid, env.Errors, env.Warnings)
	}
	echoed, ok := env.Request.(*types.Request)
	if !ok || echoed.Output == nil || len(echoed.Output.Columns) != 2 {
		t.Errorf("EchoRequest: env.Request = %#v", env.Request)
	}
}

// TestPredictMaterialize_WritesNothing: a predict leaves the filesystem
// as it found it.
func TestPredictMaterialize_WritesNothing(t *testing.T) {
	t.Parallel()

	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	before := matFiles(t, fsys)
	if len(before) == 0 {
		t.Fatal("no file listed: the walk sees nothing")
	}
	predictMat(t, p, matRequest("src.pulse"))
	if after := matFiles(t, fsys); !slices.Equal(before, after) {
		t.Errorf("files before %v, after %v", before, after)
	}
}

func matFiles(t *testing.T, fsys afero.Fs) []string {
	t.Helper()
	var out []string
	if err := afero.Walk(fsys, "", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			out = append(out, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}
