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
	write := func(path string, rows []scoreNullRow) {
		b, err := p.NewCohortBuilder(ctx, path, scoreNullSchema(), pulse.CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range rows {
			row := pulse.CohortRow{r.site, r.half, nil, nil, nil, nil, nil, float64(i)}
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
	write(single, rows)
	half := len(rows) / 2
	write("scorenulls_a.pulse", rows[:half])
	write("scorenulls_b.pulse", rows[half:])
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
