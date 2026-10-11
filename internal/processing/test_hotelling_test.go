package processing

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// hotellingOracle is one mv_hotelling.json case (scripts/reference/
// gen_multivariate_tests.R): Hotelling one-sample (closed form,
// stats::anova.mlm asserted) or two-sample (Hotelling::hotelling.test),
// unweighted, frequency (= the rep() expansion, asserted) and
// probability weighted.
type hotellingOracle struct {
	Fixture string          `json:"fixture"`
	Fields  []string        `json:"fields"`
	Split   *string         `json:"split"`
	Mu      []float64       `json:"mu"`
	Weight  string          `json:"weight"`
	N       json.RawMessage `json:"n"`
	NStar   []float64       `json:"n_star"`
	T2      float64         `json:"t2"`
	F       float64         `json:"f"`
	DF1     float64         `json:"df1"`
	DF2     float64         `json:"df2"`
	PValue  float64         `json:"p_value"`
}

// hotellingOracleTol: R inverts with LAPACK, Pulse with the FMA-free
// reference Cholesky solve; on these well-conditioned fixtures the two
// agree to ~1e-13 relative.
const hotellingOracleTol = 1e-10

// readFixtureCSV reads a reference fixture CSV (the generator's dialect:
// `#` header lines, a column line, %.17g cells, empty = null).
func readFixtureCSV(t *testing.T, name string) (cols []string, rows [][]float64) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(referenceDir, "fixtures", name+".csv"))
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if cols == nil {
			cols = strings.Split(line, ",")
			continue
		}
		cells := strings.Split(line, ",")
		row := make([]float64, len(cells))
		for i, c := range cells {
			if c == "" {
				row[i] = math.NaN()
				continue
			}
			v, err := strconv.ParseFloat(c, 64)
			if err != nil {
				t.Fatal(err)
			}
			row[i] = v
		}
		rows = append(rows, row)
	}
	return cols, rows
}

// hotellingFixture builds the fixture as records: every CSV column an
// f64 field plus a categorical "grp" holding the split column's value
// ("%g") — or "all" when split is "".
func hotellingFixture(t *testing.T, name, split string) (*encoding.Schema, []*Record) {
	t.Helper()
	cols, rows := readFixtureCSV(t, name)
	schema := &encoding.Schema{}
	for _, c := range cols {
		schema.Fields = append(schema.Fields, encoding.Field{Name: c, Type: encoding.FieldTypeF64})
	}
	schema.Fields = append(schema.Fields, encoding.Field{Name: "grp", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()})
	splitAt := -1
	for i, c := range cols {
		if c == split {
			splitAt = i
		}
	}
	var recs []*Record
	for _, r := range rows {
		vals := map[string]float64{}
		for i, c := range cols {
			if !math.IsNaN(r[i]) {
				vals[c] = r[i]
			}
		}
		level := "all"
		if splitAt >= 0 {
			level = strconv.FormatFloat(r[splitAt], 'g', -1, 64)
		}
		vals["grp"] = float64(dictIDOrAdd(schema, "grp", level))
		recs = append(recs, NewRecord(schema, vals))
	}
	return schema, recs
}

func oracleWeight(kind string) *types.WeightSpec {
	switch kind {
	case "frequency":
		return &types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency}
	case "probability":
		return &types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability}
	}
	return nil
}

// runHotelling runs one Hotelling request through the processor on the
// streaming path (tests only) and the buffered one (grouped), asserts
// each path ran and that the two results are byte-identical, and
// returns the result and the response.
func runHotelling(t *testing.T, schema *encoding.Schema, recs []*Record, build func() *types.Request) (*types.TestResult, *types.Response) {
	t.Helper()
	var out [2][]byte
	var res *types.TestResult
	var resp0 *types.Response
	for i, grouped := range []bool{false, true} {
		req := build()
		// A tests-only request has nothing to stream into: an
		// aggregation keeps the ungrouped run on the streaming path,
		// and grouping it forces the buffered one.
		gf := groupField(schema)
		req.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: gf, Label: "n", Weight: types.NullSlotWeight()}}
		if grouped {
			req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: gf}}
		}
		p := NewProcessor(schema)
		resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
		if err != nil {
			t.Fatalf("grouped=%v: %v", grouped, err)
		}
		want := map[bool]ProcessPath{false: PathStreaming, true: PathBuffered}[grouped]
		if p.LastPath() != want {
			t.Fatalf("grouped=%v ran %v, want %v", grouped, p.LastPath(), want)
		}
		if len(resp.Tests) != 1 || resp.Tests[0] == nil {
			t.Fatalf("grouped=%v: tests = %+v", grouped, resp.Tests)
		}
		out[i], _ = json.Marshal(resp.Tests)
		if i == 0 {
			res, resp0 = resp.Tests[0], resp
		}
	}
	if !bytes.Equal(out[0], out[1]) {
		t.Fatalf("streaming and buffered differ:\n%s\n%s", out[0], out[1])
	}
	return res, resp0
}

// groupField is the fixture's categorical column ("grp" or "g").
func groupField(schema *encoding.Schema) string {
	if schema.Field("grp") != nil {
		return "grp"
	}
	return "g"
}

func hotClose(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol*math.Abs(want) || got == want
}

// TestHotellingT2_MatchesROracle pins every mv_hotelling.json case —
// one-sample (attitude, mtcars, weighted) and two-sample (mtcars by am,
// weighted by y_bin; zero-weight rows excluded) under no weight,
// frequency and probability weights — through the real processor on
// both execution paths, alternating the member source between an
// inline fields list and a named Request.Vectors entry.
func TestHotellingT2_MatchesROracle(t *testing.T) {
	doc := loadReference[hotellingOracle](t, "mv_hotelling")
	if doc.Packages["Hotelling"] == "" {
		t.Fatal("mv_hotelling.json does not record the Hotelling package version")
	}
	twoSample := 0
	for ci, c := range doc.Cases {
		split := ""
		if c.Split != nil {
			split = *c.Split
			twoSample++
		}
		t.Run(fmt.Sprintf("%s/split=%s/%s", c.Fixture, split, c.Weight), func(t *testing.T) {
			schema, recs := hotellingFixture(t, c.Fixture, split)
			viaVector := ci%2 == 1
			res, _ := runHotelling(t, schema, recs, func() *types.Request {
				spec := &types.Test{Type: types.TEST_HOTELLING_T2}
				req := &types.Request{Weight: oracleWeight(c.Weight), Tests: []*types.Test{spec}}
				if viaVector {
					req.Vectors = []types.VectorSpec{{Name: "profile", Fields: c.Fields}}
					spec.Vector = "profile"
				} else {
					spec.Fields = c.Fields
				}
				if split != "" {
					spec.SplitBy = "grp"
				} else {
					raw, _ := json.Marshal(map[string]any{"mu": c.Mu})
					spec.Params = raw
				}
				return req
			})
			for _, chk := range []struct {
				name      string
				got, want float64
			}{
				{"t2", res.Statistic, c.T2},
				{"f", res.Details["f"].(float64), c.F},
				{"df2", res.Details["df2"].(float64), c.DF2},
				{"p_value", res.PValue, c.PValue},
			} {
				if !hotClose(chk.got, chk.want, hotellingOracleTol) {
					t.Errorf("%s = %.17g, R = %.17g", chk.name, chk.got, chk.want)
				}
			}
			if res.DF != c.DF1 || res.Details["df1"] != c.DF1 {
				t.Errorf("df1 = %v / %v, want %v", res.DF, res.Details["df1"], c.DF1)
			}
			if !reflect.DeepEqual(res.Details["members"], c.Fields) {
				t.Errorf("members = %v, want %v", res.Details["members"], c.Fields)
			}
			var wantN bytes.Buffer
			if err := json.Compact(&wantN, c.N); err != nil {
				t.Fatal(err)
			}
			if gotN, _ := json.Marshal(res.Details["n"]); string(gotN) != wantN.String() {
				t.Errorf("n = %s, want %s", gotN, wantN.String())
			}
			var nStar []float64
			switch c.Weight {
			case "frequency":
				nStar = asFloats(res.Details["sum_weights"])
			case "probability":
				nStar = asFloats(res.Details["n_eff"])
			default:
				if res.Details["sum_weights"] != nil {
					t.Errorf("unweighted run carries sum_weights")
				}
				return
			}
			if len(nStar) != len(c.NStar) {
				t.Fatalf("N* = %v, want %v", nStar, c.NStar)
			}
			for i := range nStar {
				if !hotClose(nStar[i], c.NStar[i], 1e-12) {
					t.Errorf("N*[%d] = %.17g, R = %.17g", i, nStar[i], c.NStar[i])
				}
			}
		})
	}
	if twoSample == 0 || twoSample == len(doc.Cases) {
		t.Fatalf("oracle must hold one- and two-sample cases, got %d two-sample of %d", twoSample, len(doc.Cases))
	}
}

func asFloats(v any) []float64 {
	switch x := v.(type) {
	case float64:
		return []float64{x}
	case []float64:
		return x
	}
	return nil
}

// hotellingSchema is a small 3-member schema with a split and a weight.
func hotellingSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x1", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "x2", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "x3", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "w", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary(), Nullable: true},
	}}
}

// hotellingRows returns n deterministic rows over x1..x3 in groups g.
func hotellingRows(schema *encoding.Schema, n int, groups []string, mod func(i int, v map[string]float64)) []*Record {
	var recs []*Record
	for i := 0; i < n; i++ {
		f := float64(i)
		v := map[string]float64{
			"x1": math.Sin(f*1.7) * 3,
			"x2": math.Cos(f*0.9)*2 + 0.1*f,
			"x3": math.Sin(f*0.37+1) * 5,
			"w":  1 + float64(i%3),
			"g":  float64(dictIDOrAdd(schema, "g", groups[i%len(groups)])),
		}
		if mod != nil {
			mod(i, v)
		}
		recs = append(recs, NewRecord(schema, v))
	}
	return recs
}

func hotellingReq(mod func(*types.Test)) func() *types.Request {
	return func() *types.Request {
		spec := &types.Test{Type: types.TEST_HOTELLING_T2, Fields: []string{"x1", "x2", "x3"}}
		if mod != nil {
			mod(spec)
		}
		return &types.Request{Tests: []*types.Test{spec}}
	}
}

func processErr(t *testing.T, schema *encoding.Schema, recs []*Record, req *types.Request) error {
	t.Helper()
	_, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(recs))
	return err
}

func hotCode(err error) errors.Code {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

// TestHotellingT2_SplitNeedsTwoLevels: a split_by with one level or
// with three is PULSE_TEST_SPLIT_GROUPS_LT_2 naming the levels.
func TestHotellingT2_SplitNeedsTwoLevels(t *testing.T) {
	for _, groups := range [][]string{{"a"}, {"a", "b", "c"}} {
		schema := hotellingSchema()
		recs := hotellingRows(schema, 30, groups, nil)
		err := processErr(t, schema, recs, hotellingReq(func(s *types.Test) { s.SplitBy = "g" })())
		if hotCode(err) != errors.PULSE_TEST_SPLIT_GROUPS_LT_2 {
			t.Fatalf("%d levels: err = %v, want PULSE_TEST_SPLIT_GROUPS_LT_2", len(groups), err)
		}
		var ce *errors.CodedError
		stderrors.As(err, &ce)
		if got := ce.Details["groups"].([]string); len(got) != len(groups) {
			t.Fatalf("details.groups = %v", got)
		}
	}
}

// TestHotellingT2_SingularCovarianceWarns: a member that is a linear
// combination of the others leaves the (pooled) covariance singular —
// not an error: a PULSE_MATRIX_SINGULAR warning, a null statistic and
// p-value (JSON null, never 0), on both forms.
func TestHotellingT2_SingularCovarianceWarns(t *testing.T) {
	dependent := func(_ int, v map[string]float64) { v["x3"] = v["x1"] + 2*v["x2"] }
	constant := func(_ int, v map[string]float64) { v["x3"] = 4 }
	for _, c := range []struct {
		split string
		mod   func(int, map[string]float64)
	}{{"", dependent}, {"g", dependent}, {"", constant}, {"g", constant}} {
		split := c.split
		schema := hotellingSchema()
		recs := hotellingRows(schema, 40, []string{"a", "b"}, c.mod)
		res, resp := runHotelling(t, schema, recs, hotellingReq(func(s *types.Test) { s.SplitBy = split }))
		if !math.IsNaN(res.Statistic) || !math.IsNaN(res.PValue) || res.RejectNull {
			t.Fatalf("split=%q: statistic %v p %v reject %v, want NaN NaN false", split, res.Statistic, res.PValue, res.RejectNull)
		}
		if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], string(errors.PULSE_MATRIX_SINGULAR)+":") {
			t.Fatalf("split=%q: warnings = %v", split, res.Warnings)
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte(`"statistic":null`)) || !bytes.Contains(raw, []byte(`"p_value":null`)) {
			t.Fatalf("split=%q: undefined figures not null on the wire: %s", split, raw)
		}
	}
}

// TestHotellingT2_ListwiseDrop: a row with any null member is dropped
// and counted; the result equals the run without that row. A null
// split key skips the row uncounted.
func TestHotellingT2_ListwiseDrop(t *testing.T) {
	schema := hotellingSchema()
	clean := hotellingRows(schema, 30, []string{"a", "b"}, nil)
	withNull := append([]*Record(nil), clean...)
	withNull = append(withNull,
		NewRecord(schema, map[string]float64{"x1": 99, "x3": 1, "g": 0}),          // null x2: dropped
		NewRecord(schema, map[string]float64{"x1": 99, "x2": 99, "x3": 99, "w": 1}), // null split: skipped
	)
	req := hotellingReq(func(s *types.Test) { s.SplitBy = "g" })
	a, _ := runHotelling(t, schema, clean, req)
	b, _ := runHotelling(t, schema, withNull, req)
	if b.Details["n_dropped"] != int64(1) || a.Details["n_dropped"] != int64(0) {
		t.Fatalf("n_dropped = %v / %v, want 1 / 0", b.Details["n_dropped"], a.Details["n_dropped"])
	}
	if a.Statistic != b.Statistic || a.PValue != b.PValue {
		t.Fatalf("dropped row leaked: %v vs %v", a.Statistic, b.Statistic)
	}
}

// TestHotellingT2_InsufficientN: one-sample needs n > p complete rows,
// two-sample n₁ + n₂ > p + 1.
func TestHotellingT2_InsufficientN(t *testing.T) {
	for _, c := range []struct {
		split string
		n     int
	}{{"", 3}, {"g", 4}} {
		schema := hotellingSchema()
		recs := hotellingRows(schema, c.n, []string{"a", "b"}, nil)
		err := processErr(t, schema, recs, hotellingReq(func(s *types.Test) { s.SplitBy = c.split })())
		if hotCode(err) != errors.PULSE_TEST_INSUFFICIENT_N {
			t.Fatalf("split=%q n=%d: err = %v", c.split, c.n, err)
		}
	}
	// The raw row floor binds even when frequency weights lift Σw past
	// p: three distinct rows span at most a plane in three members.
	schema := hotellingSchema()
	req := hotellingReq(nil)()
	req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	if err := processErr(t, schema, hotellingRows(schema, 3, []string{"a"}, nil), req); hotCode(err) != errors.PULSE_TEST_INSUFFICIENT_N {
		t.Fatalf("n = p under frequency weights (Σw > p): err = %v", err)
	}
	schema = hotellingSchema()
	if _, err := NewProcessor(schema).Process(context.Background(), hotellingReq(nil)(), NewSliceIterator(hotellingRows(schema, 4, []string{"a"}, nil))); err != nil {
		t.Fatalf("n = p + 1 must run: %v", err)
	}
}

// TestHotellingT2_FrequencyEqualsExpansion: integer frequency weights
// equal the unweighted test on the rep()-expanded rows (the one weight
// rule), and probability weights are scale invariant.
func TestHotellingT2_FrequencyEqualsExpansion(t *testing.T) {
	for _, split := range []string{"", "g"} {
		schema := hotellingSchema()
		weighted := hotellingRows(schema, 25, []string{"a", "b"}, nil)
		var expanded []*Record
		for i, r := range weighted {
			w := 1 + i%3
			for k := 0; k < w; k++ {
				expanded = append(expanded, r)
			}
		}
		req := func(ws *types.WeightSpec) func() *types.Request {
			return func() *types.Request {
				r := hotellingReq(func(s *types.Test) { s.SplitBy = split })()
				r.Weight = ws
				return r
			}
		}
		wf, _ := runHotelling(t, schema, weighted, req(&types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}))
		ex, _ := runHotelling(t, schema, expanded, req(nil))
		if !hotClose(wf.Statistic, ex.Statistic, 1e-12) || !hotClose(wf.PValue, ex.PValue, 1e-10) {
			t.Fatalf("split=%q: frequency %v/%v, expansion %v/%v", split, wf.Statistic, wf.PValue, ex.Statistic, ex.PValue)
		}
		scaled := hotellingRows(schema, 25, []string{"a", "b"}, func(i int, v map[string]float64) { v["w"] *= 7.5 })
		p1, _ := runHotelling(t, schema, weighted, req(&types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}))
		p2, _ := runHotelling(t, schema, scaled, req(&types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}))
		if !hotClose(p1.Statistic, p2.Statistic, 1e-12) || p1.Details["n_eff"] == nil {
			t.Fatalf("split=%q: probability not scale invariant: %v vs %v", split, p1.Statistic, p2.Statistic)
		}
	}
}

// TestBuildRowTests_StashesResolvedMembers: a named vector with a glob
// reaches the factory as its resolved literal members; the result label
// stays the caller's spec's, and the caller's spec is not mutated.
func TestBuildRowTests_StashesResolvedMembers(t *testing.T) {
	schema := hotellingSchema()
	recs := hotellingRows(schema, 30, []string{"a", "b"}, nil)
	spec := &types.Test{Type: types.TEST_HOTELLING_T2, Vector: "xs", SplitBy: "g"}
	req := &types.Request{Vectors: []types.VectorSpec{{Name: "xs", Fields: []string{"x[13]", "x2"}}}, Tests: []*types.Test{spec}}
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatal(err)
	}
	res := resp.Tests[0]
	if !reflect.DeepEqual(res.Details["members"], []string{"x1", "x3", "x2"}) {
		t.Fatalf("members = %v", res.Details["members"])
	}
	if res.Label != "TEST_HOTELLING_T2" || spec.Vector != "xs" || spec.Fields != nil {
		t.Fatalf("label %q, caller spec mutated: %+v", res.Label, spec)
	}
	req.Tests[0] = &types.Test{Type: types.TEST_HOTELLING_T2, Vector: "missing"}
	if err := processErr(t, schema, recs, req); hotCode(err) != errors.PULSE_VECTOR_UNKNOWN {
		t.Fatalf("unknown vector: err = %v", err)
	}
}

// TestNeededFields_TestVectorMembers: projection decodes a vector
// test's members — inline fields (globs expanded) and a named vector's —
// plus its split_by, and nothing else.
func TestNeededFields_TestVectorMembers(t *testing.T) {
	schema := hotellingSchema()
	for _, c := range []struct {
		name string
		req  *types.Request
	}{
		{"inline", &types.Request{Tests: []*types.Test{{Type: types.TEST_HOTELLING_T2, Fields: []string{"x[12]"}, SplitBy: "g"}}}},
		{"vector", &types.Request{
			Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"x1", "x2"}}},
			Tests:   []*types.Test{{Type: types.TEST_HOTELLING_T2, Vector: "v", SplitBy: "g"}},
		}},
	} {
		got := NeededFields(c.req, schema, nil)
		if got.IsWide() || got.Len() != 3 || !got.Has("x1") || !got.Has("x2") || !got.Has("g") {
			t.Fatalf("%s: needed = %v", c.name, got)
		}
	}
}
