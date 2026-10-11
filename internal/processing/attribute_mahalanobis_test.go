package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// mahalanobisOracle is one mv_mahalanobis.json case (scripts/reference/
// gen_fitted_attributes.R): stats::mahalanobis + pchisq per row, the fit
// over the listwise, positive-weight, fit_on rows; every complete row
// scored, the others null.
type mahalanobisOracle struct {
	Fixture string   `json:"fixture"`
	Fields  []string `json:"fields"`
	Weight  string   `json:"weight"`
	FitOn   *struct {
		Field string  `json:"field"`
		Level float64 `json:"level"`
	} `json:"fit_on"`
	NFit  int        `json:"n_fit"`
	NStar float64    `json:"n_star"`
	D2    []*float64 `json:"d2"`
	P     []*float64 `json:"p"`
}

// mahalanobisOracleTol: R factors with LAPACK, Pulse with the FMA-free
// reference Cholesky and a forward substitution; on these fixtures the
// two agree to ~1e-13 relative. The absolute floor covers a D² near 0.
const (
	mahalanobisOracleTol = 1e-10
	mahalanobisAbsFloor  = 1e-12
)

func mahaClose(got, want float64) bool {
	return math.Abs(got-want) <= mahalanobisOracleTol*math.Abs(want)+mahalanobisAbsFloor
}

// runMahalanobisArms runs build's request on the streaming two-pass arm
// (ungrouped, one AGG_COUNT) and the buffered arm (grouped on the
// fixture's categorical), asserts each arm ran, and returns each arm's
// per-row score under label (NaN = null) plus the streaming response.
// The records are rebuilt per arm (mk), so the arms never share state.
func runMahalanobisArms(t *testing.T, schema *encoding.Schema, mk func() []*Record, label string, build func() *types.Request) (streamed, buffered []float64, resp *types.Response) {
	t.Helper()
	var out [2][]float64
	for i, grouped := range []bool{false, true} {
		recs := mk()
		req := build()
		gf := groupField(schema)
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_COUNT, Field: gf, Label: "n_rows", Weight: types.NullSlotWeight()})
		if grouped {
			req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: gf}}
		}
		p := NewProcessor(schema)
		r, err := p.Process(context.Background(), req, NewSliceIterator(recs))
		if err != nil {
			t.Fatalf("grouped=%v: %v", grouped, err)
		}
		want := map[bool]ProcessPath{false: PathStreaming, true: PathBuffered}[grouped]
		if p.LastPath() != want {
			t.Fatalf("grouped=%v ran %v, want %v", grouped, p.LastPath(), want)
		}
		vals := make([]float64, len(recs))
		for k, rec := range recs {
			if v, ok := rec.NumericValue(label); ok {
				vals[k] = v
			} else {
				vals[k] = math.NaN()
			}
		}
		out[i] = vals
		if i == 0 {
			resp = r
		} else {
			wa, _ := json.Marshal(resp.Warnings)
			wb, _ := json.Marshal(r.Warnings)
			if string(wa) != string(wb) {
				t.Fatalf("warnings differ: streaming %s, buffered %s", wa, wb)
			}
		}
	}
	return out[0], out[1], resp
}

// sameBits reports whether a and b are identical, NaN matching NaN.
func sameBits(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Float64bits(a[i]) != math.Float64bits(b[i]) && !(math.IsNaN(a[i]) && math.IsNaN(b[i])) {
			return false
		}
	}
	return true
}

// TestMahalanobis_MatchesROracle pins every mv_mahalanobis.json case —
// attitude, mtcars (incl. a fit_on subset), weighted (zero-weight rows)
// and nulls (listwise) under no weight, frequency and probability
// weights, both outputs — through the real processor on the streaming
// two-pass and buffered arms (bit-identical), alternating the member
// source between inline fields and a named Request.Vectors entry.
func TestMahalanobis_MatchesROracle(t *testing.T) {
	doc := loadReference[mahalanobisOracle](t, "mv_mahalanobis")
	fitOnCases, weighted := 0, map[string]bool{}
	for ci, c := range doc.Cases {
		split := ""
		if c.FitOn != nil {
			split = c.FitOn.Field
			fitOnCases++
		}
		weighted[c.Weight] = true
		for _, output := range []string{"d2", "p"} {
			t.Run(fmt.Sprintf("%d/%s/%s/fit_on=%s/%s", ci, c.Fixture, c.Weight, split, output), func(t *testing.T) {
				schema, _ := hotellingFixture(t, c.Fixture, split)
				mk := func() []*Record { _, recs := hotellingFixture(t, c.Fixture, split); return recs }
				viaVector := ci%2 == 1
				streamed, buffered, resp := runMahalanobisArms(t, schema, mk, "md", func() *types.Request {
					attr := &types.Attribute{Type: types.ATTR_MAHALANOBIS, Label: "md"}
					req := &types.Request{Weight: oracleWeight(c.Weight), Attributes: []*types.Attribute{attr}}
					if viaVector {
						req.Vectors = []types.VectorSpec{{Name: "battery", Fields: c.Fields}}
						attr.Vector = "battery"
					} else {
						attr.Fields = c.Fields
					}
					params := map[string]any{"output": output}
					if c.FitOn != nil {
						params["fit_on"] = []map[string]any{{"type": "FILTER_INCLUDE", "field": "grp", "values": []string{fmt.Sprintf("%g", c.FitOn.Level)}}}
					}
					attr.Params, _ = json.Marshal(params)
					return req
				})
				if !sameBits(streamed, buffered) {
					t.Fatalf("streaming two-pass and buffered scores differ:\n%v\n%v", streamed, buffered)
				}
				if len(resp.Warnings) != 0 {
					t.Fatalf("unexpected warnings %+v", resp.Warnings)
				}
				want := c.D2
				if output == "p" {
					want = c.P
				}
				if len(want) != len(streamed) {
					t.Fatalf("%d scores, R has %d", len(streamed), len(want))
				}
				for i, w := range want {
					got := streamed[i]
					if w == nil {
						if !math.IsNaN(got) {
							t.Errorf("row %d: score %v, R null (a member is null)", i, got)
						}
						continue
					}
					if !mahaClose(got, *w) {
						t.Errorf("row %d: %s = %.17g, R = %.17g", i, output, got, *w)
					}
				}
			})
		}
	}
	if fitOnCases == 0 || !weighted["frequency"] || !weighted["probability"] || !weighted["none"] {
		t.Fatalf("vacuous oracle: fit_on cases %d, weight kinds %v", fitOnCases, weighted)
	}
}

// mahaSchema is a small numeric fixture: x, y, z, a categorical g, and
// row-count helpers.
func mahaSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "z", Type: encoding.FieldTypeF64},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
}

// mahaRecords builds rows from (x, y, z, g) tuples; a NaN cell is null.
func mahaRecords(schema *encoding.Schema, rows [][3]float64, groups []string) []*Record {
	recs := make([]*Record, len(rows))
	for i, r := range rows {
		vals := map[string]float64{"g": float64(dictIDOrAdd(schema, "g", groups[i]))}
		for j, name := range []string{"x", "y", "z"} {
			if !math.IsNaN(r[j]) {
				vals[name] = r[j]
			}
		}
		recs[i] = NewRecord(schema, vals)
	}
	return recs
}

var mahaRows = [][3]float64{
	{1, 2, 0.5}, {2, 1, 1.5}, {3, 4, 0.2}, {4, 3, 2.2}, {5, 6, 1.1},
	{6, 4, 0.7}, {7, 8, 2.9}, {8, 9, 1.4}, {2.5, 7, 0.1}, {9, 2, 3.3},
}
var mahaGroups = []string{"a", "a", "a", "a", "a", "b", "b", "b", "b", "b"}

// TestMahalanobis_FitOnRestrictsFitScoresEveryFilteredRow: fit_on
// changes the fit (the scores differ from a whole-population fit) but
// every FILTERED row is still scored — rows outside fit_on included —
// while a row the request's filter drops gets no value.
func TestMahalanobis_FitOnRestrictsFitScoresEveryFilteredRow(t *testing.T) {
	schema := mahaSchema()
	build := func(fitOn bool) func() *types.Request {
		return func() *types.Request {
			attr := &types.Attribute{Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: []string{"x", "y"}}
			if fitOn {
				attr.Params = json.RawMessage(`{"fit_on": [{"type": "FILTER_RANGE", "field": "x", "values": ["1", "5"]}]}`)
			}
			return &types.Request{
				// The request filter drops the last row (x = 9).
				Filterers:  []*types.Filterer{{Type: types.FILTER_RANGE, Field: "x", Values: []string{"0", "8.5"}}},
				Attributes: []*types.Attribute{attr},
			}
		}
	}
	mk := func() []*Record { return mahaRecords(schema, mahaRows, mahaGroups) }
	all, _, _ := runMahalanobisArms(t, schema, mk, "md", build(false))
	sub, subBuf, _ := runMahalanobisArms(t, schema, mk, "md", build(true))
	if !sameBits(sub, subBuf) {
		t.Fatalf("fit_on: streaming and buffered differ:\n%v\n%v", sub, subBuf)
	}
	for i := range mahaRows {
		filtered := mahaRows[i][0] <= 8.5
		if !filtered {
			if !math.IsNaN(sub[i]) {
				t.Errorf("row %d is filtered out but scored %v", i, sub[i])
			}
			continue
		}
		if math.IsNaN(sub[i]) {
			t.Errorf("row %d passes the filter but has no score under fit_on (fit_on restricts the fit only)", i)
		}
	}
	// fit_on [1, 5] leaves x = 6, 7, 8 out of the fit, which moves every
	// score away from the whole-population fit's.
	differs := false
	for i := range all {
		if !math.IsNaN(all[i]) && all[i] != sub[i] {
			differs = true
		}
	}
	if !differs {
		t.Fatal("fit_on did not change the fit: the scores equal a whole-population fit")
	}
}

// TestMahalanobis_FitOnScoresRowsOutsideTheFit pins the row-level half:
// a row fit_on excludes from the fit still receives the score of the
// fit it did not join — exactly D² against the subset's centre and
// covariance.
func TestMahalanobis_FitOnScoresRowsOutsideTheFit(t *testing.T) {
	schema := mahaSchema()
	recs := mahaRecords(schema, mahaRows, mahaGroups)
	req := &types.Request{Attributes: []*types.Attribute{{
		Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: []string{"x", "y"},
		Params: json.RawMessage(`{"fit_on": [{"type": "FILTER_INCLUDE", "field": "g", "values": ["a"]}]}`),
	}}}
	p := NewProcessor(schema)
	if _, err := p.processRecords(context.Background(), p.prepare(req), recs); err != nil {
		t.Fatal(err)
	}
	// The "a" rows' fit, by hand.
	var mx, my float64
	for i := 0; i < 5; i++ {
		mx += mahaRows[i][0]
		my += mahaRows[i][1]
	}
	mx, my = mx/5, my/5
	var sxx, syy, sxy float64
	for i := 0; i < 5; i++ {
		dx, dy := mahaRows[i][0]-mx, mahaRows[i][1]-my
		sxx, syy, sxy = sxx+dx*dx, syy+dy*dy, sxy+dx*dy
	}
	sxx, syy, sxy = sxx/4, syy/4, sxy/4
	det := sxx*syy - sxy*sxy
	for i, r := range mahaRows {
		dx, dy := r[0]-mx, r[1]-my
		want := (syy*dx*dx - 2*sxy*dx*dy + sxx*dy*dy) / det
		got, ok := recs[i].NumericValue("md")
		if !ok {
			t.Fatalf("row %d (group %s) not scored", i, mahaGroups[i])
		}
		if math.Abs(got-want) > 1e-12*math.Max(1, math.Abs(want)) {
			t.Errorf("row %d (group %s): D² %v, hand %v", i, mahaGroups[i], got, want)
		}
	}
}

// TestMahalanobis_SingularAndInsufficientNullEveryScore: a member that
// is a linear combination of others, and a fit with N* ≤ p, null every
// score and raise the coded warning on both arms.
func TestMahalanobis_SingularAndInsufficientNullEveryScore(t *testing.T) {
	cases := []struct {
		name string
		rows [][3]float64
		code errors.Code
	}{
		{"singular", func() [][3]float64 {
			out := make([][3]float64, len(mahaRows))
			for i, r := range mahaRows {
				out[i] = [3]float64{r[0], r[1], 2*r[0] - r[1]} // z = 2x − y
			}
			return out
		}(), errors.PULSE_MATRIX_SINGULAR},
		// Numerically singular yet Cholesky-factorable: only the shared
		// rank rule (rankDeficient) refuses it — never a ridge.
		{"near singular", func() [][3]float64 {
			out := make([][3]float64, len(mahaRows))
			for i, r := range mahaRows {
				d := 1e-7
				if i%2 == 1 {
					d = -d
				}
				out[i] = [3]float64{r[0], r[1], 2*r[0] - r[1] + d}
			}
			return out
		}(), errors.PULSE_MATRIX_SINGULAR},
		{"constant member", func() [][3]float64 {
			out := make([][3]float64, len(mahaRows))
			for i, r := range mahaRows {
				out[i] = [3]float64{r[0], r[1], 4}
			}
			return out
		}(), errors.PULSE_MATRIX_SINGULAR},
		{"insufficient", mahaRows[:3], errors.PULSE_MATRIX_INSUFFICIENT_N},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			schema := mahaSchema()
			mk := func() []*Record { return mahaRecords(schema, c.rows, mahaGroups[:len(c.rows)]) }
			streamed, buffered, resp := runMahalanobisArms(t, schema, mk, "md", func() *types.Request {
				return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: []string{"x", "y", "z"}}}}
			})
			for i := range streamed {
				if !math.IsNaN(streamed[i]) || !math.IsNaN(buffered[i]) {
					t.Fatalf("row %d scored %v / %v, want null", i, streamed[i], buffered[i])
				}
			}
			if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(c.code) {
				t.Fatalf("warnings %+v, want one %s", resp.Warnings, c.code)
			}
			d := resp.Warnings[0].Details
			if d["attribute"] != "md" || d["type"] != "ATTR_MAHALANOBIS" || d["scope"] != "fit" || d["p"] != 3 {
				t.Errorf("warning details %v", d)
			}
		})
	}
}

// TestMahalanobis_NullMemberNullScore: a row with a null member is left
// out of the fit and scores null; the other rows are scored off the
// complete-case fit (listwise).
func TestMahalanobis_NullMemberNullScore(t *testing.T) {
	schema := mahaSchema()
	rows := append([][3]float64(nil), mahaRows...)
	rows[3][1] = math.NaN()
	mk := func() []*Record { return mahaRecords(schema, rows, mahaGroups) }
	streamed, _, _ := runMahalanobisArms(t, schema, mk, "md", func() *types.Request {
		return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: []string{"x", "y"}}}}
	})
	complete := append(append([][3]float64(nil), rows[:3]...), rows[4:]...)
	mk2 := func() []*Record {
		return mahaRecords(schema, complete, append(append([]string(nil), mahaGroups[:3]...), mahaGroups[4:]...))
	}
	ref, _, _ := runMahalanobisArms(t, schema, mk2, "md", func() *types.Request {
		return &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: []string{"x", "y"}}}}
	})
	if !math.IsNaN(streamed[3]) {
		t.Fatalf("row 3 has a null member but scored %v", streamed[3])
	}
	got := append(append([]float64(nil), streamed[:3]...), streamed[4:]...)
	if !sameBits(got, ref) {
		t.Fatalf("the null row moved the fit:\n%v\n%v", got, ref)
	}
}

// TestMahalanobis_DefaultLabels: "<TYPE>_<vector>" and
// "<TYPE>_<first fields entry>".
func TestMahalanobis_DefaultLabels(t *testing.T) {
	schema := mahaSchema()
	for _, c := range []struct {
		attr  *types.Attribute
		vecs  []types.VectorSpec
		label string
	}{
		{&types.Attribute{Type: types.ATTR_MAHALANOBIS, Vector: "battery"}, []types.VectorSpec{{Name: "battery", Fields: []string{"x", "y"}}}, "ATTR_MAHALANOBIS_battery"},
		{&types.Attribute{Type: types.ATTR_MAHALANOBIS, Fields: []string{"y", "x"}}, nil, "ATTR_MAHALANOBIS_y"},
	} {
		recs := mahaRecords(schema, mahaRows, mahaGroups)
		req := &types.Request{Vectors: c.vecs, Attributes: []*types.Attribute{c.attr}}
		p := NewProcessor(schema)
		if _, err := p.processRecords(context.Background(), p.prepare(req), recs); err != nil {
			t.Fatal(err)
		}
		if _, ok := recs[0].NumericValue(c.label); !ok {
			t.Errorf("no value under %s", c.label)
		}
		if c.attr.Fields != nil && c.attr.Fields[0] != "y" || c.attr.Label != "" {
			t.Errorf("prepare mutated the caller's spec: %+v", c.attr)
		}
	}
}

// TestMahalanobis_FitOnReadsEarlierAttribute: a fit_on filterer on an
// earlier attribute's label makes the fitted attribute a layer-1 two-pass
// stage; the streaming planner and the buffered arm agree bit for bit.
func TestMahalanobis_FitOnReadsEarlierAttribute(t *testing.T) {
	schema := mahaSchema()
	mk := func() []*Record { return mahaRecords(schema, mahaRows, mahaGroups) }
	streamed, buffered, _ := runMahalanobisArms(t, schema, mk, "md", func() *types.Request {
		return &types.Request{Attributes: []*types.Attribute{
			{Type: types.ATTR_ZSCORE, Field: "z", Label: "zz"},
			{Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: []string{"x", "y"},
				Params: json.RawMessage(`{"fit_on": [{"type": "FILTER_RANGE", "field": "zz", "values": ["-1", "1"]}]}`)},
		}}
	})
	if !sameBits(streamed, buffered) {
		t.Fatalf("streaming and buffered differ:\n%v\n%v", streamed, buffered)
	}
	plan := planTwoPassStages([]*types.Attribute{
		{Type: types.ATTR_ZSCORE, Field: "z", Label: "zz"},
		{Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: []string{"x", "y"},
			Params: json.RawMessage(`{"fit_on": [{"type": "FILTER_RANGE", "field": "zz", "values": ["-1", "1"]}]}`)},
	}, []bool{true, true}, nil)
	if plan.layer[1] != 1 {
		t.Fatalf("the fit_on reader is on layer %d, want 1 (it reads zz)", plan.layer[1])
	}
}

// TestMahalanobis_FactoryRefusals: an unresolved vector and bad params
// are refused at construction; fit_on unbound is an internal error.
func TestMahalanobis_FactoryRefusals(t *testing.T) {
	schema := mahaSchema()
	if _, err := newMahalanobisAttribute(&types.Attribute{Type: types.ATTR_MAHALANOBIS, Vector: "v"}, schema); err == nil {
		t.Error("an unresolved vector was accepted")
	}
	if _, err := newMahalanobisAttribute(&types.Attribute{Type: types.ATTR_MAHALANOBIS, Fields: []string{"x"}, Params: json.RawMessage(`{"output": "q"}`)}, schema); err == nil {
		t.Error("output q was accepted")
	}
	c, err := newMahalanobisAttribute(&types.Attribute{Type: types.ATTR_MAHALANOBIS, Fields: []string{"x", "y"},
		Params: json.RawMessage(`{"fit_on": [{"type": "FILTER_TRUE"}]}`)}, schema)
	if err != nil {
		t.Fatal(err)
	}
	recs := mahaRecords(schema, mahaRows, mahaGroups)
	if err := c.(TwoPassAttribute).PrePass(recs[0], ""); err == nil || !strings.Contains(err.Error(), "fit_on") {
		t.Errorf("PrePass with an unbound fit_on: %v", err)
	}
}

// TestMahalanobis_ZeroWeightRowScoredNotFitted: a zero-weight row adds
// nothing to the fit but is scored; the result equals the fit without
// the row.
func TestMahalanobis_ZeroWeightRowScoredNotFitted(t *testing.T) {
	schema := mahaSchema()
	schema.Fields = append(schema.Fields, encoding.Field{Name: "w", Type: encoding.FieldTypeF64})
	recs := mahaRecords(schema, mahaRows, mahaGroups)
	for i, r := range recs {
		w := 1.0
		if i == 9 {
			w = 0
		}
		r.Set("w", w)
	}
	req := &types.Request{Weight: &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency},
		Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: []string{"x", "y"}}}}
	p := NewProcessor(schema)
	if _, err := p.processRecords(context.Background(), p.prepare(req), recs); err != nil {
		t.Fatal(err)
	}
	ref := mahaRecords(schema, mahaRows[:9], mahaGroups[:9])
	refReq := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: []string{"x", "y"}}}}
	if _, err := NewProcessor(schema).processRecords(context.Background(), refReq, ref); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		got, _ := recs[i].NumericValue("md")
		want, _ := ref[i].NumericValue("md")
		if got != want {
			t.Errorf("row %d: %v, want %v (the zero-weight row moved the fit)", i, got, want)
		}
	}
	if v, ok := recs[9].NumericValue("md"); !ok || math.IsNaN(v) {
		t.Errorf("the zero-weight row was not scored: %v %v", v, ok)
	}
	// The fit's row count leaves the zero-weight row out too (it is
	// scored, not fitted): two massed rows + one zero-weight row for
	// p = 2 is N* = 2 ≤ p, and the warning's n is 2, not 3.
	few := mahaRecords(schema, mahaRows[:3], mahaGroups[:3])
	for i, r := range few {
		r.Set("w", float64(min(1, 2-i)))
	}
	fp := NewProcessor(schema)
	resp, err := fp.processRecords(context.Background(), fp.prepare(req), few)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_MATRIX_INSUFFICIENT_N) {
		t.Fatalf("warnings %+v, want one PULSE_MATRIX_INSUFFICIENT_N", resp.Warnings)
	}
	if n := resp.Warnings[0].Details["n"]; n != int64(2) {
		t.Errorf("fit n = %v (%T), want 2: the zero-weight row was counted", n, n)
	}
}

// TestDeriveRecords_SurfacesFitWarnings: the materialize drive reads a
// fitted attribute's warning back through DeriveWarnings.
func TestDeriveRecords_SurfacesFitWarnings(t *testing.T) {
	schema := mahaSchema()
	recs := mahaRecords(schema, mahaRows[:2], mahaGroups[:2])
	req := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "md", Fields: []string{"x", "y"}}}}
	p := NewProcessor(schema)
	err := p.DeriveRecords(context.Background(), req, NewSliceIterator(recs), []DerivedColumn{{Name: "md"}}, func(int64, []any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if w := p.DeriveWarnings(); len(w) != 1 || w[0].Code != string(errors.PULSE_MATRIX_INSUFFICIENT_N) {
		t.Fatalf("derive warnings %+v, want one PULSE_MATRIX_INSUFFICIENT_N", w)
	}
}

// TestNeededFields_AttributeVectorMembersAndFitOn: projection decodes
// the inline members and the fit_on filterer columns.
func TestNeededFields_AttributeVectorMembersAndFitOn(t *testing.T) {
	schema := mahaSchema()
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x"}},
		Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Fields: []string{"y", "z"},
			Params: json.RawMessage(`{"fit_on": [{"type": "FILTER_INCLUDE", "field": "g", "values": ["a"]}]}`)}},
	}
	fs := NeededFields(req, schema, nil)
	for _, f := range []string{"x", "y", "z", "g"} {
		if !fs.Has(f) {
			t.Errorf("projection omits %s", f)
		}
	}
}
