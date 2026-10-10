package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	encx "github.com/frankbardon/pulse/internal/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matSchema is the materialize source: every family of field type —
// integer, bit-packed, float (nullable), date, categorical with an
// unused pre-seeded entry, decimal128 and a set — each described.
func matSchema() encoding.Schema {
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				panic(err)
			}
		}
		return d
	}
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Record identifier, unique per row"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Sales region of the record",
			Dictionary: dict("north", "south", "east", "west")},
		{Name: "week", Type: encoding.FieldTypeU16, Description: "Week number within the season"},
		{Name: "spend", Type: encoding.FieldTypeF64, Nullable: true, Description: "Media spend in thousands"},
		{Name: "level", Type: encoding.FieldTypeU4, Description: "Priority level from zero to fifteen"},
		{Name: "flag", Type: encoding.FieldTypePackedBool, Description: "Whether the record is flagged"},
		{Name: "day", Type: encoding.FieldTypeDate, Nullable: true, Description: "Calendar day of the record"},
		{Name: "price", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, Description: "Unit price per region, exact"},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Nullable: true, Description: "Campaign tags applied",
			Dictionary: dict("a", "b", "c")},
	}}
}

// matRows are twelve source rows: three regions over four weeks, a
// null spend, a null region, a null day and null / empty sets. price is
// constant per region so a group keyed on region survives the gate.
func matRows(t *testing.T) []CohortRow {
	t.Helper()
	regions := []string{"north", "south", "east"}
	prices := map[string]encoding.Decimal128{
		"north": bigDecimal(t, "1050"), "south": bigDecimal(t, "2025"), "east": bigDecimal(t, "99999"),
	}
	var rows []CohortRow
	for i := 0; i < 12; i++ {
		reg := regions[i%3]
		var region any = reg
		var spend any = float64(i*3) + 0.5
		var day any = int32(19000 + i)
		var tags any = []string{"a", "c"}
		switch i {
		case 4:
			spend = nil
		case 7:
			day = nil
			tags = []string{}
		case 10:
			tags = nil
		}
		rows = append(rows, CohortRow{uint64(i + 1), region, uint64(i/3 + 1), spend, uint64(i % 16), i%2 == 0, day, prices[reg], tags})
	}
	return rows
}

// buildMatSource writes the source cohort through NewCohortBuilder with
// opts (sharded / grouped variants) and returns its path.
func buildMatSource(t *testing.T, p *Pulse, path string, opts CohortBuilderOptions) {
	t.Helper()
	ctx := context.Background()
	b, err := p.NewCohortBuilder(ctx, path, matSchema(), opts)
	if err != nil {
		t.Fatalf("NewCohortBuilder: %v", err)
	}
	for _, r := range matRows(t) {
		if err := b.Append(r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	res, err := b.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(opts.Groups) > 0 && res.FormatVersion != encoding.FormatVersionV2 {
		t.Fatalf("grouped source wrote format %#x, want 0x02", res.FormatVersion)
	}
}

func matPulse(t *testing.T, opts Options) (*Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	opts.FS = fsys
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

// matRequest exercises every derived family: a feature over the
// unfiltered set (frequency encoding sees the excluded region too), a
// filter, a two-pass attribute, and windows reading a feature, keyed
// by partition + order.
func matRequest(cohort string) *Request {
	return &Request{
		Cohort: &types.Cohort{Filename: cohort},
		Features: []*types.Feature{
			{Type: types.FEAT_FREQUENCY_ENCODE, Field: "region"},
			{Type: types.FEAT_LOG, Field: "spend"},
		},
		Filterers:  []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north", "south"}}},
		Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "spend"}},
		Windows: []*types.Window{
			{Type: types.WIN_LAG, Field: "LOG_spend", PartitionBy: []string{"region"}, OrderBy: []types.OrderKey{{Field: "week"}}},
			{Type: types.WIN_ROW_NUMBER, PartitionBy: []string{"region"}, OrderBy: []types.OrderKey{{Field: "week", Desc: true}}},
		},
	}
}

var matDerived = []string{"FREQ_region", "LOG_spend", "ATTR_ZSCORE_spend", "WIN_LAG_LOG_spend", "WIN_ROW_NUMBER"}

// readAll opens path and returns its schema and every row.
func readAll(t *testing.T, p *Pulse, path string) (*encoding.Schema, []CohortRow) {
	t.Helper()
	r := openReader(t, p, path)
	var rows []CohortRow
	for i := int64(0); i < r.Len(); i++ {
		row, err := r.RecordAt(i)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return r.Schema(), rows
}

// TestMaterialize_SourceRoundTripsAcrossLayouts: over a single-file, a
// shard-archive and a grouped (0x02) source, the output is a 0x01
// single file whose leading fields are the source's logical schema
// verbatim (order, type, nullability, description, decimal shape,
// dictionary) and whose leading cells are the filtered source records'
// exact values in source order; the derived columns follow as nullable
// f64 in pipeline order.
func TestMaterialize_SourceRoundTripsAcrossLayouts(t *testing.T) {
	layouts := []struct {
		name string
		opts CohortBuilderOptions
	}{
		{"single", CohortBuilderOptions{}},
		{"archive", CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 5}}},
		{"grouped", CohortBuilderOptions{Groups: []pio.GroupDecl{{Key: []string{"region"}, Members: []string{"price"}}}}},
	}
	for _, l := range layouts {
		t.Run(l.name, func(t *testing.T) {
			ctx := context.Background()
			p, fsys := matPulse(t, Options{})
			buildMatSource(t, p, "src.pulse", l.opts)
			res, err := p.Materialize(ctx, matRequest("src.pulse"), "out.pulse", MaterializeOptions{})
			if err != nil {
				t.Fatalf("Materialize: %v", err)
			}
			if res.Target != "out.pulse" || res.Records != 8 {
				t.Fatalf("result = %+v, want out.pulse with 8 records", res)
			}
			if strings.Join(res.DerivedColumns, ",") != strings.Join(matDerived, ",") {
				t.Fatalf("DerivedColumns = %v, want %v", res.DerivedColumns, matDerived)
			}
			head, err := afero.ReadFile(fsys, "out.pulse")
			if err != nil {
				t.Fatal(err)
			}
			if string(head[:5]) != "PULSE" || head[8] != encoding.FormatVersionV1 {
				t.Fatalf("output header % x, want a 0x01 single file", head[:9])
			}

			srcSchema, srcRows := readAll(t, p, "src.pulse")
			src := srcSchema.Logical()
			outSchema, outRows := readAll(t, p, "out.pulse")
			if outSchema.HasGroups() {
				t.Fatal("output is grouped")
			}
			n := len(src.Fields)
			if len(outSchema.Fields) != n+len(matDerived) {
				t.Fatalf("output has %d fields, want %d", len(outSchema.Fields), n+len(matDerived))
			}
			for i := range src.Fields {
				g, w := outSchema.Fields[i], src.Fields[i]
				if g.Name != w.Name || g.Type != w.Type || g.Nullable != w.Nullable || g.Description != w.Description ||
					g.Precision != w.Precision || g.Scale != w.Scale {
					t.Errorf("field %d = %+v, want %+v", i, g, w)
				}
				if (g.Dictionary == nil) != (w.Dictionary == nil) ||
					(w.Dictionary != nil && strings.Join(g.Dictionary.Values(), ",") != strings.Join(w.Dictionary.Values(), ",")) {
					t.Errorf("field %s dictionary differs", w.Name)
				}
			}
			for i, name := range matDerived {
				f := outSchema.Fields[n+i]
				if f.Name != name || f.Type != encoding.FieldTypeF64 || !f.Nullable || encx.IsLowQualityDescription(f.Description) {
					t.Errorf("derived field %d = %+v, want nullable f64 %s with a description", i, f, name)
				}
			}

			// Filtered (north + south) records in source order.
			var want []CohortRow
			for _, r := range srcRows {
				if r[1] == "north" || r[1] == "south" {
					want = append(want, r)
				}
			}
			if len(outRows) != len(want) {
				t.Fatalf("%d output rows, want %d", len(outRows), len(want))
			}
			for i := range want {
				assertRowEqual(t, "row", src, outRows[i][:n], want[i])
			}
		})
	}
}

// TestMaterialize_DerivedValuesMatchProcess: every derived value equals
// the windows-only Process output on the same request (projection off,
// so Process rows carry every column), row for row — features computed
// over the unfiltered set and windows over the filtered set line up
// with the right source record. Over a shard archive too, whose records
// concatenate in shard order.
func TestMaterialize_DerivedValuesMatchProcess(t *testing.T) {
	for _, l := range []struct {
		name string
		opts CohortBuilderOptions
	}{{"single", CohortBuilderOptions{}}, {"archive", CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 5}}}} {
		t.Run(l.name, func(t *testing.T) {
			ctx := context.Background()
			p, _ := matPulse(t, Options{DisableProjection: true})
			buildMatSource(t, p, "src.pulse", l.opts)
			if _, err := p.Materialize(ctx, matRequest("src.pulse"), "out.pulse", MaterializeOptions{}); err != nil {
				t.Fatal(err)
			}
			resp, err := p.Process(ctx, matRequest("src.pulse"))
			if err != nil {
				t.Fatal(err)
			}
			schema, rows := readAll(t, p, "out.pulse")
			if len(rows) != len(resp.Data) {
				t.Fatalf("%d rows, Process emitted %d", len(rows), len(resp.Data))
			}
			n := len(schema.Fields) - len(matDerived)
			sawNull, sawFreq := false, false
			for i, pr := range resp.Data {
				if pr["id"] != float64(rows[i][0].(uint64)) {
					t.Fatalf("row %d: id %v, Process id %v", i, rows[i][0], pr["id"])
				}
				for c, name := range matDerived {
					got := rows[i][n+c]
					want := pr[name]
					if want == nil {
						if got != nil {
							t.Errorf("row %d %s = %v, Process omits it (null)", i, name, got)
						}
						sawNull = true
						continue
					}
					wf, _ := toFloat(want)
					if gf, ok := got.(float64); !ok || math.Float64bits(gf) != math.Float64bits(wf) {
						t.Errorf("row %d %s = %v, Process %v", i, name, got, want)
					}
				}
				// Frequency over the UNFILTERED set: 4 of 12 records per region.
				if pr["FREQ_region"] != nil && rows[i][n].(float64) == 4.0/12 {
					sawFreq = true
				}
			}
			if !sawNull || !sawFreq {
				t.Errorf("fixture lost its edge cases: null=%v unfiltered-frequency=%v", sawNull, sawFreq)
			}
		})
	}
}

// TestMaterialize_SortMatchesProcessRowOrder: with a sort whose order
// is not any window's order, the output rows follow Process's sorted
// row set row for row — source cells and every derived value — and so
// leave source order; the windows keep the values they compute under
// their own partition / order.
func TestMaterialize_SortMatchesProcessRowOrder(t *testing.T) {
	ctx := context.Background()
	p, _ := matPulse(t, Options{DisableProjection: true})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 5}})
	req := func() *Request {
		r := matRequest("src.pulse")
		r.Sort = []types.OrderKey{{Field: "WIN_ROW_NUMBER"}, {Field: "region", Desc: true}}
		return r
	}
	if _, err := p.Materialize(ctx, req(), "out.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	resp, err := p.Process(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	unsorted, err := p.Process(ctx, matRequest("src.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[float64]map[string]any{}
	for _, r := range unsorted.Data {
		byID[r["id"].(float64)] = r
	}
	schema, rows := readAll(t, p, "out.pulse")
	if len(rows) != len(resp.Data) {
		t.Fatalf("%d rows, Process emitted %d", len(rows), len(resp.Data))
	}
	src := matRows(t)
	n := len(schema.Fields) - len(matDerived)
	inSourceOrder := true
	var prev uint64
	for i, pr := range resp.Data {
		id := rows[i][0].(uint64)
		if pr["id"] != float64(id) {
			t.Fatalf("row %d: id %d, Process id %v", i, id, pr["id"])
		}
		if i > 0 && id < prev {
			inSourceOrder = false
		}
		prev = id
		if rows[i][3] != src[id-1][3] {
			t.Errorf("row %d spend %v, source %v", i, rows[i][3], src[id-1][3])
		}
		for c, name := range matDerived {
			got := rows[i][n+c]
			for _, want := range []any{pr[name], byID[float64(id)][name]} {
				if want == nil {
					if got != nil {
						t.Errorf("row %d %s = %v, want null", i, name, got)
					}
					continue
				}
				wf, _ := toFloat(want)
				if gf, ok := got.(float64); !ok || math.Float64bits(gf) != math.Float64bits(wf) {
					t.Errorf("row %d %s = %v, want %v", i, name, got, want)
				}
			}
		}
	}
	if inSourceOrder {
		t.Fatal("the sort left the rows in source order; the fixture proves nothing")
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}

// TestMaterialize_OneHotAndRowNumberColumns: a multi-output feature
// contributes every output in featureOutputLabels order, and an
// integer window output is stored as f64.
func TestMaterialize_OneHotAndRowNumberColumns(t *testing.T) {
	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	req := &Request{
		Cohort:   &types.Cohort{Filename: "src.pulse"},
		Features: []*types.Feature{{Type: types.FEAT_ONE_HOT, Field: "region"}},
		Windows:  []*types.Window{{Type: types.WIN_ROW_NUMBER, OrderBy: []types.OrderKey{{Field: "id"}}}},
	}
	res, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"region_north", "region_south", "region_east", "region_west", "WIN_ROW_NUMBER"}
	if strings.Join(res.DerivedColumns, ",") != strings.Join(want, ",") {
		t.Fatalf("DerivedColumns = %v, want %v", res.DerivedColumns, want)
	}
	schema, rows := readAll(t, p, "out.pulse")
	n := len(schema.Fields) - len(want)
	for i, r := range rows {
		if r[n+4] != float64(i+1) {
			t.Errorf("row %d WIN_ROW_NUMBER = %#v, want float64 %d", i, r[n+4], i+1)
		}
	}
	if !strings.Contains(schema.Fields[n].Description, "FEAT_ONE_HOT of region, output region_north — derived from src.pulse") {
		t.Errorf("one-hot description = %q", schema.Fields[n].Description)
	}
}

// TestMaterialize_GeneratedDescription pins the generated description
// of a window column and its pass under Strict (with described source
// fields, Strict writes).
func TestMaterialize_GeneratedDescription(t *testing.T) {
	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	req := &Request{
		Cohort: &types.Cohort{Filename: "src.pulse"},
		Windows: []*types.Window{{Type: types.WIN_LAG, Field: "spend", Params: json.RawMessage(`{"offset":2}`),
			PartitionBy: []string{"region"}, OrderBy: []types.OrderKey{{Field: "week"}}}},
	}
	if _, err := p.Materialize(ctx, req, "dir/out.pulse", MaterializeOptions{Strict: true}); err != nil {
		t.Fatalf("Strict Materialize: %v", err)
	}
	schema, _ := readAll(t, p, "dir/out.pulse")
	got := schema.Fields[len(schema.Fields)-1].Description
	want := "WIN_LAG of spend (offset 2; partition region; order week) — derived from src.pulse"
	if got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
}

// TestMaterialize_StrictRefusesWeakSourceDescriptions: Strict escalates
// a weak SOURCE description (as the cohort builder does) before any
// file is created.
func TestMaterialize_StrictRefusesWeakSourceDescriptions(t *testing.T) {
	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	createTestPulseFile(t, fsys, "plain.pulse", []string{"id", "v"}, [][]string{{"1", "2.5"}, {"2", "3.5"}})
	req := &Request{Cohort: &types.Cohort{Filename: "plain.pulse"}, Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "v"}}}
	_, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{Strict: true})
	assertCode(t, err, errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY)
	assertAbsent(t, fsys, "out.pulse")
	res, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 || res.Warnings[0].Code != errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY {
		t.Errorf("non-strict warnings = %v, want the low-quality findings", res.Warnings)
	}
}

func assertCode(t *testing.T, err error, code errors.Code) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return ce
}

// assertAbsent fails when anything but the source exists beside path
// (no target, no temp spool).
func assertAbsent(t *testing.T, fsys afero.Fs, path string) {
	t.Helper()
	if ok, _ := afero.Exists(fsys, path); ok {
		t.Fatalf("%s was created", path)
	}
	ents, _ := afero.ReadDir(fsys, "/")
	for _, e := range ents {
		if strings.Contains(e.Name(), "out") || strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("leftover file %s", e.Name())
		}
	}
}

// TestMaterialize_RefusesUnsupportedSlots: one refusal per slot that
// does not produce one value per source record, details.slot naming it,
// nothing created.
func TestMaterialize_RefusesUnsupportedSlots(t *testing.T) {
	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	cases := map[string]func(r *Request){
		"aggregations": func(r *Request) { r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id"}} },
		"groups":       func(r *Request) { r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}} },
		"outputs":      func(r *Request) { r.Outputs = []*types.Output{{}} },
		"tests":        func(r *Request) { r.Tests = []*types.Test{{Type: types.TEST_T, Field: "spend"}} },
		"post_tests":   func(r *Request) { r.PostTests = []*types.Test{{Type: types.TEST_T, Field: "spend"}} },
		"regressions": func(r *Request) {
			r.Regressions = []*types.RegressionSpec{{Target: "spend", Predictors: []string{"week"}}}
		},
		"vectors": func(r *Request) { r.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"spend"}}} },
		"matrices": func(r *Request) {
			r.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "m", Fields: []string{"spend", "week"}}}
		},
		"joins":        func(r *Request) { r.Joins = []*types.JoinSpec{{}} },
		"labels":       func(r *Request) { r.Labels = []*types.LabelBinding{{Field: "region"}} },
		"crosstab":     func(r *Request) { r.Crosstab = &types.CrosstabSpec{} },
		"overlays":     func(r *Request) { r.Overlays = []types.OverlaySpec{{}} },
		"multiplicity": func(r *Request) { r.Multiplicity = &types.Multiplicity{} },
		"return":       func(r *Request) { r.Return = &types.Return{} },
	}
	for slot, set := range cases {
		t.Run(slot, func(t *testing.T) {
			req := &Request{Cohort: &types.Cohort{Filename: "src.pulse"}, Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "spend"}}}
			set(req)
			_, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
			ce := assertCode(t, err, errors.PULSE_MATERIALIZE_SLOT_UNSUPPORTED)
			if ce.Details["slot"] != slot {
				t.Errorf("details.slot = %v, want %s", ce.Details["slot"], slot)
			}
			assertAbsent(t, fsys, "out.pulse")
		})
	}
}

// TestMaterialize_RefusesNoDerivedAndUndeclaredOutputs: no derived slot,
// and an embedder feature (outputs unknown before it runs), both fail
// before anything is created; the same request without materialize's
// gates runs under Process.
func TestMaterialize_RefusesNoDerivedAndUndeclaredOutputs(t *testing.T) {
	ctx := context.Background()
	p, fsys := matPulse(t, Options{Extensions: Extensions{
		Features: []FeatureRegistration{{Name: "FEAT_ACME_DOUBLE", Factory: matStubFeature, Streamable: false}},
	}})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})

	_, err := p.Materialize(ctx, &Request{Cohort: &types.Cohort{Filename: "src.pulse"},
		Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north"}}}}, "out.pulse", MaterializeOptions{})
	assertCode(t, err, errors.PULSE_MATERIALIZE_NO_DERIVED)
	assertAbsent(t, fsys, "out.pulse")

	req := &Request{Cohort: &types.Cohort{Filename: "src.pulse"}, Features: []*types.Feature{
		{Type: types.FEAT_LOG, Field: "spend"}, {Type: "FEAT_ACME_DOUBLE", Field: "spend"}}}
	_, err = p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	ce := assertCode(t, err, errors.PULSE_MATERIALIZE_OUTPUT_UNDECLARED)
	if ce.Details["feature_index"] != 1 || ce.Details["feature"] != "FEAT_ACME_DOUBLE" {
		t.Errorf("details = %v", ce.Details)
	}
	assertAbsent(t, fsys, "out.pulse")
	// A built-in feature whose outputs cannot be named (POLY degree 1).
	req = &Request{Cohort: &types.Cohort{Filename: "src.pulse"}, Features: []*types.Feature{
		{Type: types.FEAT_POLY, Field: "spend", Params: json.RawMessage(`{"degree":1}`)}}}
	_, err = p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	assertCode(t, err, errors.PULSE_MATERIALIZE_OUTPUT_UNDECLARED)
	assertAbsent(t, fsys, "out.pulse")
}

func matStubFeature(*types.Feature, *encoding.Schema) (extend.FeatureComputer, error) {
	return matStubComputer{}, nil
}

type matStubComputer struct{}

func (matStubComputer) Compute(extend.Rows, string) (map[string]extend.FeatureOutput, error) {
	return nil, nil
}

// TestMaterialize_PreflightMatchesProcess: a request Process refuses in
// its pre-flight is refused with the identical error.
func TestMaterialize_PreflightMatchesProcess(t *testing.T) {
	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	cases := map[string]*Request{
		"unknown feature field": {Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "nope"}}},
		"shadowing label":       {Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "spend", Label: "week"}}},
		"bad window order":      {Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, OrderBy: []types.OrderKey{{Field: "nope"}}}}},
		"bad zone":              {TimeZone: "Mars/Olympus", Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "spend"}}},
		"no cohort":             {Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "spend"}}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			mk := func() *Request {
				r := *req
				if name != "no cohort" {
					r.Cohort = &types.Cohort{Filename: "src.pulse"}
				}
				return &r
			}
			_, perr := p.Process(ctx, mk())
			_, merr := p.Materialize(ctx, mk(), "out.pulse", MaterializeOptions{})
			if perr == nil || merr == nil || perr.Error() != merr.Error() {
				t.Fatalf("Process err %v, Materialize err %v; want the same refusal", perr, merr)
			}
			var pc, mc *errors.CodedError
			if !stderrors.As(perr, &pc) || !stderrors.As(merr, &mc) || pc.Code != mc.Code {
				t.Fatalf("codes differ: %v vs %v", perr, merr)
			}
			assertAbsent(t, fsys, "out.pulse")
		})
	}
}

// TestMaterialize_TargetRules: an existing target is refused without
// Overwrite (untouched); Overwrite replaces it and reports the
// invalidated sidecars as a warning; an anchored target is refused.
func TestMaterialize_TargetRules(t *testing.T) {
	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	buildMatSource(t, p, "out.pulse", CohortBuilderOptions{})
	if _, err := p.BuildIndex(ctx, "out.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	before, _ := afero.ReadFile(fsys, "out.pulse")
	req := func() *Request {
		return &Request{Cohort: &types.Cohort{Filename: "src.pulse"}, Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "spend"}}}
	}
	_, err := p.Materialize(ctx, req(), "out.pulse", MaterializeOptions{})
	assertCode(t, err, errors.SERVICE_VALIDATION)
	after, _ := afero.ReadFile(fsys, "out.pulse")
	if string(before) != string(after) {
		t.Fatal("refused run changed the existing target")
	}

	res, err := p.Materialize(ctx, req(), "out.pulse", MaterializeOptions{Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.InvalidatedSidecars) == 0 {
		t.Fatal("no invalidated sidecars reported")
	}
	last := res.Warnings[len(res.Warnings)-1]
	if last.Code != errors.PULSE_MATERIALIZE_SIDECARS_INVALIDATED {
		t.Errorf("last warning = %s, want PULSE_MATERIALIZE_SIDECARS_INVALIDATED", last.Code)
	}

	_, err = p.Materialize(ctx, req(), "arch.pulse#x.pulse", MaterializeOptions{})
	ce := assertCode(t, err, errors.SERVICE_VALIDATION)
	if ce.Details["reason"] != "anchored_target" {
		t.Errorf("details = %v", ce.Details)
	}
}

// TestMaterialize_FailureLeavesNoTarget: a run failing after the
// builder opened (a window copying a categorical label, which an f64
// column cannot hold) leaves nothing at the target.
func TestMaterialize_FailureLeavesNoTarget(t *testing.T) {
	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	req := &Request{Cohort: &types.Cohort{Filename: "src.pulse"}, Windows: []*types.Window{
		{Type: types.WIN_LAG, Field: "region", OrderBy: []types.OrderKey{{Field: "id"}}}}}
	_, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	ce := assertCode(t, err, errors.PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC)
	if ce.Details["column"] != "WIN_LAG_region" || ce.Details["value_type"] != "string" {
		t.Errorf("details = %v", ce.Details)
	}
	assertAbsent(t, fsys, "out.pulse")
}

// TestMaterialize_MemoryLimitRefusesLikeProcess: under a
// MaxEstimatedMemory one byte below the request's estimate, Materialize
// fails with the same PULSE_LIMIT_EXCEEDED Process raises, before
// anything is written; at the estimate it runs.
func TestMaterialize_MemoryLimitRefusesLikeProcess(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	var rows [][]string
	for i := 0; i < 400; i++ {
		rows = append(rows, []string{"1", "2.5", "3.5"})
	}
	createTestPulseFile(t, fsys, "big.pulse", []string{"a", "b", "c"}, rows)
	req := func() *Request {
		// The sort is judged by the same pre-flight: it adds no finding.
		return &Request{Cohort: &types.Cohort{Filename: "big.pulse"}, Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "b"}},
			Sort: []types.OrderKey{{Field: "LOG_b", Desc: true}, {Field: "a"}}}
	}
	open := func(max int64) *Pulse {
		p, err := New(Options{FS: fsys, Limits: Limits{MaxEstimatedMemory: max}})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	pr, err := open(1).Predict(ctx, req())
	if err != nil || len(pr.LimitFindings) != 1 {
		t.Fatalf("probe: %v %+v", err, pr)
	}
	est := pr.LimitFindings[0].Estimated

	p := open(est - 1)
	_, perr := p.Process(ctx, req())
	_, merr := p.Materialize(ctx, req(), "out.pulse", MaterializeOptions{})
	assertCode(t, merr, errors.PULSE_LIMIT_EXCEEDED)
	if perr == nil || perr.Error() != merr.Error() {
		t.Fatalf("Materialize %v, Process %v; want the same limit error", merr, perr)
	}
	assertAbsent(t, fsys, "out.pulse")

	if _, err := open(est).Materialize(ctx, req(), "out.pulse", MaterializeOptions{}); err != nil {
		t.Fatalf("at the estimate: %v", err)
	}
}

// TestMaterialize_TouchesManagedSource: materializing from a managed
// import slides its expiry by its TTL, as a Process read does.
func TestMaterialize_TouchesManagedSource(t *testing.T) {
	ctx := context.Background()
	past := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	p, fsys := memBuilderEngine(t)
	if err := afero.WriteFile(fsys, "data.csv", []byte("id,v\n1,2.5\n2,3.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	imp, err := p.ImportFile(ctx, ImportSpec{SourcePath: "data.csv", TTL: time.Hour})
	if err != nil || !imp.Managed {
		t.Fatalf("ImportFile = %+v, %v", imp, err)
	}
	ageSidecar(t, fsys, imp.Path, past)
	start := time.Now()
	req := &Request{Cohort: &types.Cohort{Filename: imp.Path}, Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "v"}}}
	if _, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := managedExpiry(t, fsys, imp.Path); got.Before(start.Add(time.Hour - time.Minute)) {
		t.Fatalf("managed expiry after Materialize = %v, want ~now+1h", got)
	}
}
