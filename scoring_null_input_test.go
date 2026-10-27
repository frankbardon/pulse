package pulse_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// A score of a null input is null (U50 E6-S1). ATTR_ZSCORE /
// ATTR_TSCORE / ATTR_NORMALIZED / ATTR_PERCENTILE used to answer 0 (50
// for the T-score) for a null source cell, on every field type, and
// ATTR_SET_POPCOUNT answered 0 for a null set — the same number as an
// empty selection. Each is now null in Data and counted in the
// aggregation's n_null, while the membership indicators ATTR_CODE_IN /
// ATTR_SET_HAS keep reading a null as "not in the set" (0).

// scoreNullRow is one fixture row: x (f64), k (u16), ts (datetime) and
// tags (set_u8) are null together when null is set; code (categorical)
// is null on its own pattern.
type scoreNullRow struct {
	site, half string
	x          float64
	k          uint64
	ts         int64
	tags       []string
	code       string
	null       bool
}

// scoreNullRows: 60 rows, every 4th null (i%4 == 1). The values are
// spread so that a null read as 0 (or 50) is never a real score, and a
// null folded into the pre-pass would move every row's score.
func scoreNullRows() []scoreNullRow {
	base := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC).Unix()
	var rows []scoreNullRow
	for i := range 60 {
		r := scoreNullRow{
			site: []string{"a", "b", "c"}[i%3],
			half: []string{"x", "y"}[i%2],
			x:    float64(i*7%23) + 0.25*float64(i%5) + 3,
			k:    uint64(10 + (i*11)%37),
			ts:   base + int64((i*13)%29)*3600 + int64(i)*61,
			tags: [][]string{{"p"}, {"p", "q"}, {}, {"q", "r"}, {"p", "q", "r"}}[i%5],
			code: []string{"top", "mid", "low"}[i%3],
			null: i%4 == 1,
		}
		if i%6 == 4 {
			r.code = ""
		}
		rows = append(rows, r)
	}
	return rows
}

func scoreNullSchema() encoding.Schema {
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			_, _ = d.Add(v)
		}
		return d
	}
	fields := []encoding.Field{
		{Name: "site", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("a", "b", "c")},
		{Name: "half", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("x", "y")},
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "k", Type: encoding.FieldTypeU16, Nullable: true},
		{Name: "ts", Type: encoding.FieldTypeDateTime, Nullable: true},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Nullable: true, Dictionary: dict("p", "q", "r")},
		{Name: "code", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: dict("top", "mid", "low")},
		{Name: "n", Type: encoding.FieldTypeF64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Score null-input column " + fields[i].Name + "."
		off += fields[i].Type.ByteSize()
	}
	return encoding.Schema{Fields: fields}
}

// writeScoreNulls writes rows as a single cohort and as a two-shard
// archive into fs.
func writeScoreNulls(t *testing.T, fs afero.Fs, rows []scoreNullRow) (single, archive string) {
	t.Helper()
	p := nullDatePulse(t, pulse.Options{FS: fs})
	ctx := context.Background()
	// n is the row's index in rows, on the single cohort and the archive
	// alike.
	write := func(path string, rows []scoreNullRow, from int) {
		b, err := p.NewCohortBuilder(ctx, path, scoreNullSchema(), pulse.CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range rows {
			row := pulse.CohortRow{r.site, r.half, nil, nil, nil, nil, nil, float64(from + i)}
			if !r.null {
				row[2], row[3], row[4], row[5] = r.x, r.k, r.ts, r.tags
			}
			if r.code != "" {
				row[6] = r.code
			}
			if err := b.Append(row); err != nil {
				t.Fatalf("Append %s row %d: %v", path, i, err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
	single = "scorenulls.pulse"
	write(single, rows, 0)
	half := len(rows) / 2
	write("scorenulls_a.pulse", rows[:half], 0)
	write("scorenulls_b.pulse", rows[half:], half)
	archive = "scorenulls_archive.pulse"
	if _, err := p.CreateShardArchive(ctx, archive, []string{"scorenulls_a.pulse", "scorenulls_b.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return single, archive
}

// TestScoringAttributes_NullInputIsNullEveryMode: ATTR_ZSCORE /
// ATTR_TSCORE / ATTR_NORMALIZED / ATTR_PERCENTILE over a null f64, u16
// or datetime cell, and ATTR_SET_POPCOUNT over a null set, are null in
// every execution mode (buffered projected / full / serial decode, both
// crosstab arms — the fused one asserted taken where the attribute is
// row-local — streaming where the attribute streams, a shard archive
// reduced in parallel and serially). Two oracles per request:
//   - the per-group COUNT of the score is the hand-counted non-null rows
//     (a legacy 0 / 50 is counted);
//   - COUNT / MIN / MAX of the score equal the same request run with
//     FILTER_NULL dropping the null rows first — so a null that reached
//     the pre-pass (moving the mean, sd, min, max or the rank base) moves
//     every score and fails, as does a null scored 0.
//
// The aggregations' n_null counts every null row.
func TestScoringAttributes_NullInputIsNullEveryMode(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := scoreNullRows()
	cohort, archive := writeScoreNulls(t, fs, rows)
	nulls := 0
	for _, r := range rows {
		if r.null {
			nulls++
		}
	}
	ctx := context.Background()
	runners := perRowRunners(t, fs)
	def := nullDatePulse(t, pulse.Options{FS: fs})

	type scoreCase struct {
		typ   types.AttributeType
		field string
	}
	var cases []scoreCase
	for _, typ := range []types.AttributeType{types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED, types.ATTR_PERCENTILE} {
		for _, field := range []string{"x", "k", "ts"} {
			cases = append(cases, scoreCase{typ, field})
		}
	}
	cases = append(cases, scoreCase{types.ATTR_SET_POPCOUNT, "tags"})

	for _, c := range cases {
		build := func(path string, dropNulls bool) *types.Request {
			req := &types.Request{Cohort: &types.Cohort{Filename: path},
				Attributes: []*types.Attribute{{Type: c.typ, Field: c.field, Label: "s"}},
				Groups:     []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "s", Label: "ns"},
					{Type: types.AGG_MIN, Field: "s", Label: "lo"},
					{Type: types.AGG_MAX, Field: "s", Label: "hi"},
				}}
			if dropNulls {
				req.Filterers = []*types.Filterer{{Type: types.FILTER_NULL, Field: c.field, Values: []string{"is_not_null"}}}
			}
			return req
		}
		crosstab := func(path string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path},
				Attributes: []*types.Attribute{{Type: c.typ, Field: c.field, Label: "s"}},
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "half"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "s"},
				}}
		}
		counts, cells := map[string]float64{}, map[string]float64{}
		for _, r := range rows {
			if !r.null {
				counts[r.site+"|ns"]++
				cells[r.site+"|"+r.half]++
			}
		}
		oracle := map[string]float64{}
		for _, path := range []string{cohort, archive} {
			resp, err := def.Process(ctx, build(path, true))
			if err != nil {
				t.Fatalf("%s %s oracle: %v", c.typ, c.field, err)
			}
			got := perRowFigures(t, resp, build(path, true).Groups)
			if path == cohort {
				oracle = got
			} else {
				requireFigures(t, "archive oracle", got, oracle)
			}
		}
		for k, v := range counts {
			if oracle[k] != v {
				t.Fatalf("%s %s: the FILTER_NULL oracle counts %s = %v, want %v", c.typ, c.field, k, oracle[k], v)
			}
		}
		name := fmt.Sprintf("%s/%s", c.typ, c.field)
		t.Run(name+"/fused", func(t *testing.T) {
			pr, err := def.Predict(ctx, crosstab(cohort))
			if err != nil || !pr.Valid || pr.CrosstabFusable == nil {
				t.Fatalf("predict: %v %+v", err, pr)
			}
			// Row-local attributes fuse; the two-pass and buffered scores
			// decline the fused arm and run the buffered one.
			if want := c.typ == types.ATTR_SET_POPCOUNT; *pr.CrosstabFusable != want {
				t.Fatalf("crosstab fusable = %v, want %v", *pr.CrosstabFusable, want)
			}
		})
		for _, rn := range runners {
			if rn.name == "stream" && !c.typ.Streamable() {
				continue
			}
			path := cohort
			if rn.archive {
				path = archive
			}
			t.Run(name+"/"+rn.name+"/grouped", func(t *testing.T) {
				requireFigures(t, rn.name, rn.run(t, build(path, false)), oracle)
			})
			if rn.name != "stream" {
				t.Run(name+"/"+rn.name+"/crosstab", func(t *testing.T) {
					requireFigures(t, rn.name, rn.run(t, crosstab(path)), cells)
				})
			}
		}
		for _, arm := range []struct {
			name string
			o    pulse.Options
			path string
		}{{"single", pulse.Options{}, cohort}, {"single-serial", pulse.Options{DecodeWorkers: 1}, cohort},
			{"archive", pulse.Options{}, archive}, {"archive-serial", pulse.Options{ShardWorkers: 1}, archive}} {
			t.Run(name+"/"+arm.name+"/n_null", func(t *testing.T) {
				arm.o.FS = fs
				resp, err := nullDatePulse(t, arm.o).Process(ctx, build(arm.path, false))
				if err != nil {
					t.Fatal(err)
				}
				if len(resp.Components.Aggregations) != 3 {
					t.Fatalf("%d aggregation components, want 3", len(resp.Components.Aggregations))
				}
				for _, a := range resp.Components.Aggregations {
					if a.NNull != nulls || a.N != len(rows)-nulls {
						t.Fatalf("aggregation %s floor {n %d, n_null %d}, want {%d, %d}", a.Label, a.N, a.NNull, len(rows)-nulls, nulls)
					}
				}
			})
		}
	}
}

// TestMembershipIndicators_NullReadsZeroEveryMode pins the deliberate
// exception: ATTR_CODE_IN and ATTR_SET_HAS answer "is the row's value
// one of these?", and a null is not, so it reads 0 and the row stays in
// the base — the mean of the flag is the share of the WHOLE base (the
// top-box convention), unchanged by U50. COUNT of the flag is every row,
// SUM is the hits, n_null is 0, in every execution mode.
func TestMembershipIndicators_NullReadsZeroEveryMode(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := scoreNullRows()
	cohort, archive := writeScoreNulls(t, fs, rows)
	ctx := context.Background()
	runners := perRowRunners(t, fs)
	attrs := func() []*types.Attribute {
		return []*types.Attribute{
			{Type: types.ATTR_CODE_IN, Field: "code", Label: "top", Params: []byte(`{"codes":["top"]}`)},
			{Type: types.ATTR_SET_HAS, Field: "tags", Label: "hasq", Params: []byte(`{"label":"q"}`)},
		}
	}
	build := func(path string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: path}, Attributes: attrs(),
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "top", Label: "ntop"},
				{Type: types.AGG_SUM, Field: "top", Label: "stop"},
				{Type: types.AGG_COUNT, Field: "hasq", Label: "nq"},
				{Type: types.AGG_SUM, Field: "hasq", Label: "sq"},
			}}
	}
	ref := map[string]float64{}
	for _, r := range rows {
		ref[r.site+"|ntop"]++
		ref[r.site+"|nq"]++
		if r.code == "top" {
			ref[r.site+"|stop"]++
		}
		if !r.null {
			for _, tag := range r.tags {
				if tag == "q" {
					ref[r.site+"|sq"]++
				}
			}
		}
	}
	for _, rn := range runners {
		path := cohort
		if rn.archive {
			path = archive
		}
		t.Run(rn.name, func(t *testing.T) {
			requireFigures(t, rn.name, rn.run(t, build(path)), ref)
		})
	}
	resp, err := nullDatePulse(t, pulse.Options{FS: fs}).Process(ctx, build(cohort))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range resp.Components.Aggregations {
		if a.NNull != 0 || a.N != len(rows) {
			t.Fatalf("aggregation %s floor {n %d, n_null %d}, want {%d, 0}", a.Label, a.N, a.NNull, len(rows))
		}
	}
}

// TestScoringAttributes_UndefinedSpreadIsNullEveryMode: a score whose
// spread is zero is 0/0 — undefined — so ATTR_ZSCORE / ATTR_TSCORE
// (population sd = 0) and ATTR_NORMALIZED (max = min) score every row
// null, never the 0 / 50 / 0 they used to answer (U50 E7-S2). Three
// ways to reach it, each in every execution mode with the n_null floor:
//   - a constant field (f64, u16 and datetime, nulls interleaved);
//   - a single non-null row in the filtered base (the population sd,
//     ddof 0, of one value is 0) — FILTER_RANGE keeps rows 1 (null)
//     and 2 of the spread fixture;
//   - the weighted z / T variants: a constant field under a general
//     weight, and the single-row base whose one row carries the only
//     mass.
//
// The score's COUNT must be 0 in every group and crosstab cell (a
// legacy 0 / 50 is counted), beside a COUNT of a non-null column so the
// expected figures are never empty; every score row is in n_null.
func TestScoringAttributes_UndefinedSpreadIsNullEveryMode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	constFS, spreadFS := afero.NewMemMapFs(), afero.NewMemMapFs()
	constRows := scoreNullRows()
	for i := range constRows {
		constRows[i].x, constRows[i].k, constRows[i].ts = 7.25, 12, constRows[0].ts
	}
	constCohort, constArchive := writeScoreNulls(t, constFS, constRows)
	spreadRows := scoreNullRows()
	spreadCohort, spreadArchive := writeScoreNulls(t, spreadFS, spreadRows)

	type spreadCase struct {
		name   string
		fs     afero.Fs
		single string
		arch   string
		rows   []scoreNullRow // the rows the request keeps
		typ    types.AttributeType
		field  string
		weight bool
		filter bool
	}
	var cases []spreadCase
	for _, typ := range []types.AttributeType{types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED} {
		for _, field := range []string{"x", "k", "ts"} {
			cases = append(cases, spreadCase{name: "constant", fs: constFS, single: constCohort, arch: constArchive, rows: constRows, typ: typ, field: field})
		}
		cases = append(cases, spreadCase{name: "single-row", fs: spreadFS, single: spreadCohort, arch: spreadArchive,
			rows: spreadRows[1:3], typ: typ, field: "x", filter: true})
		if typ != types.ATTR_NORMALIZED { // not weightable
			cases = append(cases,
				spreadCase{name: "weighted-constant", fs: constFS, single: constCohort, arch: constArchive, rows: constRows, typ: typ, field: "x", weight: true},
				spreadCase{name: "weighted-single-row", fs: spreadFS, single: spreadCohort, arch: spreadArchive,
					rows: spreadRows[1:3], typ: typ, field: "x", weight: true, filter: true})
		}
	}
	for _, c := range cases {
		attr := func() []*types.Attribute {
			a := &types.Attribute{Type: c.typ, Field: c.field, Label: "s"}
			if c.weight {
				a.Weight = types.SlotWeightField("n")
			}
			return []*types.Attribute{a}
		}
		filters := func() []*types.Filterer {
			if !c.filter {
				return nil
			}
			return []*types.Filterer{{Type: types.FILTER_RANGE, Field: "n", Values: []string{"1", "2"}}}
		}
		build := func(path string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path}, Attributes: attr(), Filterers: filters(),
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "s", Label: "ns"},
					{Type: types.AGG_COUNT, Field: "n", Label: "rows"},
				}}
		}
		crosstab := func(path string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path}, Attributes: attr(), Filterers: filters(),
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "half"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "s"},
				}}
		}
		want := map[string]float64{}
		for _, r := range c.rows {
			want[r.site+"|rows"]++
		}
		name := fmt.Sprintf("%s/%s/%s", c.name, c.typ, c.field)
		for _, rn := range perRowRunners(t, c.fs) {
			if rn.name == "stream" && !c.typ.Streamable() {
				continue
			}
			path := c.single
			if rn.archive {
				path = c.arch
			}
			t.Run(name+"/"+rn.name+"/grouped", func(t *testing.T) {
				requireFigures(t, rn.name, rn.run(t, build(path)), want)
			})
			if rn.name != "stream" {
				t.Run(name+"/"+rn.name+"/crosstab", func(t *testing.T) {
					requireFigures(t, rn.name, rn.run(t, crosstab(path)), map[string]float64{})
				})
			}
		}
		for _, arm := range []struct {
			name string
			o    pulse.Options
			arch bool
		}{{"single", pulse.Options{}, false}, {"single-serial", pulse.Options{DecodeWorkers: 1}, false},
			{"archive", pulse.Options{}, true}, {"archive-serial", pulse.Options{ShardWorkers: 1}, true}} {
			t.Run(name+"/"+arm.name+"/n_null", func(t *testing.T) {
				arm.o.FS = c.fs
				path := c.single
				if arm.arch {
					path = c.arch
				}
				resp, err := nullDatePulse(t, arm.o).Process(ctx, build(path))
				if err != nil {
					t.Fatal(err)
				}
				got := map[string][2]int{}
				for _, a := range resp.Components.Aggregations {
					got[a.Label] = [2]int{a.N, a.NNull}
				}
				if w := [2]int{0, len(c.rows)}; got["ns"] != w {
					t.Fatalf("score floor {n, n_null} = %v, want %v", got["ns"], w)
				}
				if w := [2]int{len(c.rows), 0}; got["rows"] != w {
					t.Fatalf("row-count floor {n, n_null} = %v, want %v", got["rows"], w)
				}
			})
		}
	}
}
