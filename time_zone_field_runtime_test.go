package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Per-row zones at runtime (U50 E1-S2). The fixture is the showcase's
// shape: every site carries one IANA zone (zone = f(site)) — two DST
// zones whose 2026 transitions fall on different nights, a +05:30 zone
// and UTC — and every instant is observed at every site, half-hourly
// across all four DST nights. A per-row-zone request must equal the
// same request run once per site with that site's zone as a slot `tz`,
// combined; and both must equal a time.In reference.

// perRowSites maps each site to its zone (the equivalence oracle).
var perRowSites = []struct{ site, zone string }{
	{"nyc", "America/New_York"}, // DST 2026-03-08 / 2026-11-01
	{"ber", "Europe/Berlin"},    // DST 2026-03-29 / 2026-10-25
	{"del", "Asia/Kolkata"},     // +05:30, no DST
	{"lon", "UTC"},
}

// perRowInstants: every half hour from 06:00 UTC the day before to
// 06:00 UTC the day after each 2026 New York and Berlin DST change.
func perRowInstants() []int64 {
	var out []int64
	for _, day := range []string{"2026-03-08", "2026-03-29", "2026-10-25", "2026-11-01"} {
		d, err := time.Parse("2006-01-02", day)
		if err != nil {
			panic(err)
		}
		for t := d.Add(-18 * time.Hour); !t.After(d.Add(30 * time.Hour)); t = t.Add(30 * time.Minute) {
			out = append(out, t.Unix())
		}
	}
	return out
}

// perRowRow is one fixture row.
type perRowRow struct {
	ts   int64
	site string
	zone string // "" = a null zone cell
}

// perRowRows lays out the fixture: every instant at every site. With
// nullEvery > 0 every nullEvery-th row's zone cell is empty (null).
func perRowRows(nullEvery int) []perRowRow {
	var rows []perRowRow
	for _, s := range perRowInstants() {
		for _, st := range perRowSites {
			rows = append(rows, perRowRow{ts: s, site: st.site, zone: st.zone})
		}
	}
	if nullEvery > 0 {
		for i := range rows {
			if i%nullEvery == 0 {
				rows[i].zone = ""
			}
		}
	}
	return rows
}

// perRowCSV renders rows with two extra columns (amount, tag) so a
// projected decode retains strictly fewer fields than the schema has —
// the projection is actually exercised.
func perRowCSV(rows []perRowRow) string {
	var b strings.Builder
	b.WriteString("ts,site,tz,n,amount,tag\n")
	for i, r := range rows {
		fmt.Fprintf(&b, "%s,%s,%s,%d,%d.5,%s\n", time.Unix(r.ts, 0).UTC().Format(time.RFC3339), r.site, r.zone, i, i%7, []string{"x", "y"}[i%2])
	}
	return b.String()
}

// importPerRow imports rows as a single-file cohort and as a two-shard
// archive, returning both paths.
func importPerRow(t *testing.T, fs afero.Fs, name string, rows []perRowRow) (single, archive string) {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	imp := func(stem string, rows []perRowRow) string {
		if err := afero.WriteFile(fs, stem+".csv", []byte(perRowCSV(rows)), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: stem + ".csv"})
		if err != nil {
			t.Fatalf("ImportFile %s: %v", stem, err)
		}
		return res.Path
	}
	single = imp(name, rows)
	ins, err := p.Inspect(ctx, single)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ts": "datetime", "tz": "categorical_u8", "site": "categorical_u8"}
	for _, f := range ins.Fields {
		if w, ok := want[f.Name]; ok && f.Type != w {
			t.Fatalf("field %s inferred as %s, want %s", f.Name, f.Type, w)
		}
	}
	half := len(rows) / 2
	a, b := imp(name+"_a", rows[:half]), imp(name+"_b", rows[half:])
	archive = name + "_archive.pulse"
	if _, err := p.CreateShardArchive(ctx, archive, []string{a, b}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return single, archive
}

// zoneOf loads a zone for the reference computations.
func zoneOf(t *testing.T, name string) *time.Location {
	t.Helper()
	z, err := temporal.LoadZone(name)
	if err != nil {
		t.Fatal(err)
	}
	return z.Location()
}

// perRowZoning is how a request names its zones: a slot `tz_field`, a
// request `time_zone_field`, or (the oracle) one slot `tz` for a single
// site, filtered to that site.
type perRowZoning struct {
	tzField  string // slot tz_field
	reqField string // request time_zone_field
	tz       string // slot tz (single-zone oracle run)
	site     string // single-zone oracle run: FILTER_INCLUDE site
}

// zoned stamps z onto a slot's tz / tz_field.
func (z perRowZoning) zoned(tz, tzField *string) {
	*tz, *tzField = z.tz, z.tzField
}

// perRowRanges are local-day ranges over the DST nights.
var perRowRanges = json.RawMessage(`{"ranges":[` +
	`{"label":"ny-spring","start":"2026-03-08","end":"2026-03-08"},` +
	`{"label":"eu-spring","start":"2026-03-29","end":"2026-03-29"},` +
	`{"label":"eu-fall","start":"2026-10-25","end":"2026-10-25"},` +
	`{"label":"ny-fall","start":"2026-11-01","end":"2026-11-01"}]}`)

// perRowFilterRanges keeps one short (New York spring-forward) and one
// long (Berlin fall-back) local day: unlike all four DST days, whose
// 23- and 25-hour days cancel, its local-day row count differs from
// the UTC-day count at both DST sites.
var perRowFilterRanges = json.RawMessage(`{"ranges":[` +
	`{"label":"ny-spring","start":"2026-03-08","end":"2026-03-08"},` +
	`{"label":"eu-fall","start":"2026-10-25","end":"2026-10-25"}]}`)

// perRowRangeLabel is perRowRanges' label for a local day ("" = none).
func perRowRangeLabel(day string) string {
	return map[string]string{"2026-03-08": "ny-spring", "2026-03-29": "eu-spring", "2026-10-25": "eu-fall", "2026-11-01": "ny-fall"}[day]
}

// perRowCase is one request shape run under a zoning.
type perRowCase struct {
	name string
	// build returns the request for cohort under zoning z.
	build func(cohort string, z perRowZoning) *types.Request
	// reference is the time.In answer over rows (nil: the equivalence
	// oracle alone judges the case).
	reference func(t *testing.T, rows []perRowRow) map[string]float64
	crosstab  bool
}

func perRowBase(cohort string, z perRowZoning) *types.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, TimeZoneField: z.reqField}
	if z.site != "" {
		req.Filterers = append(req.Filterers, &types.Filterer{Type: types.FILTER_INCLUDE, Field: "site", Values: []string{z.site}})
	}
	return req
}

func perRowDateGroup(component string, z perRowZoning) *types.Group {
	g := &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"` + component + `"}`)}
	z.zoned(&g.TimeZone, &g.TimeZoneField)
	return g
}

func perRowCountBy(t *testing.T, rows []perRowRow, key func(r perRowRow, loc *time.Location) string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, r := range rows {
		if r.zone == "" {
			continue
		}
		k := key(r, zoneOf(t, r.zone))
		if k == "" {
			continue
		}
		out[r.site+"|"+k+"|count"]++
	}
	return out
}

func perRowCases() []perRowCase {
	count := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n", Label: "count"}}
	site := &types.Group{Type: types.GROUP_CATEGORY, Field: "site"}
	return []perRowCase{
		{
			name: "site-day",
			build: func(cohort string, z perRowZoning) *types.Request {
				req := perRowBase(cohort, z)
				req.Aggregations = count
				req.Groups = []*types.Group{site, perRowDateGroup("day", z)}
				return req
			},
			reference: func(t *testing.T, rows []perRowRow) map[string]float64 {
				return perRowCountBy(t, rows, func(r perRowRow, loc *time.Location) string {
					return time.Unix(r.ts, 0).In(loc).Format("2006-01-02")
				})
			},
		},
		{
			name: "site-hour",
			build: func(cohort string, z perRowZoning) *types.Request {
				req := perRowBase(cohort, z)
				req.Aggregations = count
				req.Groups = []*types.Group{site, perRowDateGroup("hour", z)}
				return req
			},
			reference: func(t *testing.T, rows []perRowRow) map[string]float64 {
				return perRowCountBy(t, rows, func(r perRowRow, loc *time.Location) string {
					return time.Unix(r.ts, 0).In(loc).Format("2006-01-02T15")
				})
			},
		},
		{
			name: "site-date-ranges",
			build: func(cohort string, z perRowZoning) *types.Request {
				req := perRowBase(cohort, z)
				req.Aggregations = count
				g := &types.Group{Type: types.GROUP_DATE_RANGES, Field: "ts", Params: perRowRanges}
				z.zoned(&g.TimeZone, &g.TimeZoneField)
				req.Groups = []*types.Group{site, g}
				return req
			},
			reference: func(t *testing.T, rows []perRowRow) map[string]float64 {
				return perRowCountBy(t, rows, func(r perRowRow, loc *time.Location) string {
					if l := perRowRangeLabel(time.Unix(r.ts, 0).In(loc).Format("2006-01-02")); l != "" {
						return l
					}
					return "unmatched"
				})
			},
		},
		{
			name: "filter-date-ranges",
			build: func(cohort string, z perRowZoning) *types.Request {
				req := perRowBase(cohort, z)
				f := &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: perRowFilterRanges}
				z.zoned(&f.TimeZone, &f.TimeZoneField)
				req.Filterers = append(req.Filterers, f)
				req.Aggregations = count
				req.Groups = []*types.Group{site}
				return req
			},
			reference: func(t *testing.T, rows []perRowRow) map[string]float64 {
				out := map[string]float64{}
				for _, r := range rows {
					if r.zone == "" {
						continue
					}
					if l := perRowRangeLabel(time.Unix(r.ts, 0).In(zoneOf(t, r.zone)).Format("2006-01-02")); l == "ny-spring" || l == "eu-fall" {
						out[r.site+"|count"]++
					}
				}
				return out
			},
		},
		{
			name: "date-part-and-features",
			build: func(cohort string, z perRowZoning) *types.Request {
				req := perRowBase(cohort, z)
				a := &types.Attribute{Type: types.ATTR_DATE_PART, Field: "ts", Label: "lday", Params: json.RawMessage(`{"part":"year_month_day"}`)}
				z.zoned(&a.TimeZone, &a.TimeZoneField)
				f := &types.Feature{Type: types.FEAT_DATE_FEATURES, Field: "ts", Label: "f"}
				z.zoned(&f.TimeZone, &f.TimeZoneField)
				req.Attributes = []*types.Attribute{a}
				req.Features = []*types.Feature{f}
				req.Aggregations = []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "n", Label: "count"},
					{Type: types.AGG_SUM, Field: "lday", Label: "sum_lday"},
					{Type: types.AGG_SUM, Field: "f_hour", Label: "sum_hour"},
					{Type: types.AGG_SUM, Field: "f_day", Label: "sum_day"},
				}
				req.Groups = []*types.Group{site}
				return req
			},
			reference: func(t *testing.T, rows []perRowRow) map[string]float64 {
				out := map[string]float64{}
				for _, r := range rows {
					out[r.site+"|count"]++
					if r.zone == "" {
						continue
					}
					l := time.Unix(r.ts, 0).In(zoneOf(t, r.zone))
					out[r.site+"|sum_lday"] += float64(l.Year()*10000 + int(l.Month())*100 + l.Day())
					out[r.site+"|sum_hour"] += float64(l.Hour())
					out[r.site+"|sum_day"] += float64(l.Day())
				}
				return out
			},
		},
		{
			name:     "crosstab-site-by-day-of-week",
			crosstab: true,
			build: func(cohort string, z perRowZoning) *types.Request {
				req := perRowBase(cohort, z)
				req.Crosstab = &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}},
					Columns: []*types.Group{perRowDateGroup("day_of_week", z)},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
				}
				return req
			},
		},
		{
			name:     "crosstab-day-by-site",
			crosstab: true,
			build: func(cohort string, z perRowZoning) *types.Request {
				req := perRowBase(cohort, z)
				req.Crosstab = &types.CrosstabSpec{
					Rows:    []*types.Group{perRowDateGroup("day", z)},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
				}
				return req
			},
			reference: func(t *testing.T, rows []perRowRow) map[string]float64 {
				out := map[string]float64{}
				for _, r := range rows {
					if r.zone != "" {
						out[time.Unix(r.ts, 0).In(zoneOf(t, r.zone)).Format("2006-01-02")+"|"+r.site]++
					}
				}
				return out
			},
		},
	}
}

// perRowFigures flattens a grouped response to {"<keys…>|<label>" →
// value} (zero figures dropped), a crosstab to {"<row>|<col>" → count}.
func perRowFigures(t *testing.T, resp *types.Response, groups []*types.Group) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	if resp.Crosstab != nil && resp.Crosstab.Matrix != nil {
		m := resp.Crosstab.Matrix
		for i, rk := range m.RowKeys {
			for j, c := range m.Cells[i] {
				if v := c.Scalar(); v != 0 {
					out[fmt.Sprint(rk[0])+"|"+fmt.Sprint(m.ColumnKeys[j][0])] += v
				}
			}
		}
		return out
	}
	perRowRowsInto(t, out, resp.Data, groups)
	return out
}

func perRowRowsInto(t *testing.T, out map[string]float64, rows []map[string]any, groups []*types.Group) {
	t.Helper()
	isKey := map[string]bool{}
	for _, g := range groups {
		isKey[g.Field] = true
	}
	for _, r := range rows {
		var key []string
		for _, g := range groups {
			key = append(key, fmt.Sprint(r[g.Field]))
		}
		prefix := strings.Join(key, "|")
		for col, v := range r {
			if isKey[col] {
				continue
			}
			f := toFloat(t, v)
			if f == 0 {
				continue
			}
			k := col
			if prefix != "" {
				k = prefix + "|" + col
			}
			out[k] += f
		}
	}
}

func requireFigures(t *testing.T, what string, got, want map[string]float64) {
	t.Helper()
	if !maps.Equal(got, want) {
		var diff []string
		for k, v := range want {
			if got[k] != v {
				diff = append(diff, fmt.Sprintf("%s: got %v want %v", k, got[k], v))
			}
		}
		for k, v := range got {
			if _, ok := want[k]; !ok {
				diff = append(diff, fmt.Sprintf("%s: got %v, not wanted", k, v))
			}
		}
		if len(diff) > 12 {
			diff = append(diff[:12], fmt.Sprintf("… %d more", len(diff)-12))
		}
		t.Fatalf("%s: %d figures differ:\n%s", what, len(diff), strings.Join(diff, "\n"))
	}
}

// perRowRunner is one execution mode.
type perRowRunner struct {
	name    string
	archive bool
	run     func(t *testing.T, req *types.Request) map[string]float64
}

func perRowRunners(t *testing.T, fs afero.Fs) []perRowRunner {
	t.Helper()
	ctx := context.Background()
	mk := func(o pulse.Options) *pulse.Pulse {
		o.FS = fs
		p, err := pulse.New(o)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	process := func(p *pulse.Pulse) func(t *testing.T, req *types.Request) map[string]float64 {
		return func(t *testing.T, req *types.Request) map[string]float64 {
			t.Helper()
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			groups := req.Groups
			return perRowFigures(t, resp, groups)
		}
	}
	def := mk(pulse.Options{})
	return []perRowRunner{
		{name: "buffered-projected", run: process(def)},
		{name: "buffered-full-decode", run: process(mk(pulse.Options{DisableProjection: true}))},
		{name: "buffered-serial-decode", run: process(mk(pulse.Options{DecodeWorkers: 1}))},
		{name: "crosstab-buffered", run: process(mk(pulse.Options{DisableCrosstabFusion: true}))},
		{name: "stream", run: func(t *testing.T, req *types.Request) map[string]float64 {
			t.Helper()
			it, err := def.ProcessStream(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			defer it.Close()
			var rows []map[string]any
			for {
				row, ok, err := it.Next(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !ok {
					break
				}
				rows = append(rows, row)
			}
			out := map[string]float64{}
			perRowRowsInto(t, out, rows, req.Groups)
			return out
		}},
		{name: "shards-parallel", archive: true, run: process(def)},
		{name: "shards-serial", archive: true, run: process(mk(pulse.Options{ShardWorkers: 1}))},
	}
}

// TestTimeZoneField_PerRowEqualsSingleZoneSlotsEveryMode: every
// zone-capable operator reading a per-row zone field — from the slot's
// `tz_field` or the request's `time_zone_field` — equals the same
// request run once per site with that site's zone as a slot `tz`,
// combined, and equals a time.In reference, in every execution mode
// (projected and full buffered decode, serial decode, streaming, both
// crosstab arms — the fused one asserted taken — and a shard archive
// reduced in parallel and serially). The fixture's local days differ
// from its UTC days, so a zone field that fails to reach an arm (or
// decodes as null because the projection dropped it) fails here.
func TestTimeZoneField_PerRowEqualsSingleZoneSlotsEveryMode(t *testing.T) {
	fs := afero.NewMemMapFs()
	rows := perRowRows(0)
	cohort, archive := importPerRow(t, fs, "perrow", rows)
	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	runners := perRowRunners(t, fs)

	for _, tc := range perRowCases() {
		// The oracle: one single-zone run per site, combined.
		oracle := map[string]float64{}
		for _, st := range perRowSites {
			req := tc.build(cohort, perRowZoning{tz: st.zone, site: st.site})
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("%s oracle %s: %v", tc.name, st.site, err)
			}
			for k, v := range perRowFigures(t, resp, req.Groups) {
				oracle[k] += v
			}
		}
		if tc.reference != nil {
			requireFigures(t, tc.name+" oracle vs time.In reference", oracle, tc.reference(t, rows))
		}
		for _, zoning := range []struct {
			name string
			z    perRowZoning
		}{{"tz_field", perRowZoning{tzField: "tz"}}, {"time_zone_field", perRowZoning{reqField: "tz"}}} {
			if tc.crosstab {
				t.Run(tc.name+"/"+zoning.name+"/crosstab-fused", func(t *testing.T) {
					pr, err := p.Predict(ctx, tc.build(cohort, zoning.z))
					if err != nil || !pr.Valid || pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
						t.Fatalf("predict: the fused arm is not taken (err=%v)", err)
					}
					requireFigures(t, "fused", runners[0].run(t, tc.build(cohort, zoning.z)), oracle)
				})
			}
			for _, rn := range runners {
				if rn.name == "stream" && tc.crosstab {
					continue // a crosstab has no row stream
				}
				t.Run(tc.name+"/"+zoning.name+"/"+rn.name, func(t *testing.T) {
					path := cohort
					if rn.archive {
						path = archive
					}
					requireFigures(t, rn.name, rn.run(t, tc.build(path, zoning.z)), oracle)
				})
			}
		}
	}
}

// TestTimeZoneField_NullZoneCellIsNullLocalValue: a row whose zone cell
// is null has a null LOCAL value — never the UTC one. GROUP_DATE and
// GROUP_DATE_RANGES key it into no bucket (not even the unmatched one)
// and count it in the grouper's n_null; ATTR_DATE_PART and every
// FEAT_DATE_FEATURES column are null on it (the aggregation's n_null);
// FILTER_DATE_RANGES drops it, like a null date, and counts it in the
// slot's n_null_input. Every other row matches the time.In reference,
// on a single file, a shard archive (parallel and serial) and the fused
// crosstab.
func TestTimeZoneField_NullZoneCellIsNullLocalValue(t *testing.T) {
	fs := afero.NewMemMapFs()
	rows := perRowRows(7)
	cohort, archive := importPerRow(t, fs, "nullzone", rows)
	ctx := context.Background()
	nulls := 0
	for _, r := range rows {
		if r.zone == "" {
			nulls++
		}
	}
	if nulls == 0 || nulls == len(rows) {
		t.Fatalf("fixture: %d null zone cells of %d", nulls, len(rows))
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	pSerial, err := pulse.New(pulse.Options{FS: fs, ShardWorkers: 1, DecodeWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	z := perRowZoning{tzField: "tz"}
	cases := perRowCases()
	byName := map[string]perRowCase{}
	for _, c := range cases {
		byName[c.name] = c
	}
	for _, arm := range []struct {
		name string
		p    *pulse.Pulse
		path string
	}{{"single", p, cohort}, {"single-serial", pSerial, cohort}, {"archive", p, archive}, {"archive-serial", pSerial, archive}} {
		run := func(t *testing.T, name string) (*types.Response, map[string]float64) {
			t.Helper()
			req := byName[name].build(arm.path, z)
			resp, err := arm.p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			got := perRowFigures(t, resp, req.Groups)
			requireFigures(t, name, got, byName[name].reference(t, rows))
			return resp, got
		}
		t.Run(arm.name+"/groupers", func(t *testing.T) {
			for _, name := range []string{"site-day", "site-hour", "site-date-ranges"} {
				resp, _ := run(t, name)
				g := resp.Components.Groupers[1]
				if g.NNull != nulls || g.TotalN != len(rows)-nulls {
					t.Fatalf("%s grouper floor {total_n %d, n_null %d}, want {%d, %d}", name, g.TotalN, g.NNull, len(rows)-nulls, nulls)
				}
			}
		})
		t.Run(arm.name+"/attribute-and-features", func(t *testing.T) {
			resp, _ := run(t, "date-part-and-features")
			for _, a := range resp.Components.Aggregations {
				want := nulls
				if a.Label == "count" {
					want = 0
				}
				if a.NNull != want {
					t.Fatalf("aggregation %s n_null = %d, want %d", a.Label, a.NNull, want)
				}
			}
		})
		t.Run(arm.name+"/filter", func(t *testing.T) {
			resp, _ := run(t, "filter-date-ranges")
			f := resp.Components.Filterers[0]
			if f.NNullInput != nulls || f.NIn != len(rows) {
				t.Fatalf("filterer floor {n_in %d, n_null_input %d}, want {%d, %d}", f.NIn, f.NNullInput, len(rows), nulls)
			}
		})
	}
	t.Run("crosstab-fused", func(t *testing.T) {
		req := byName["crosstab-day-by-site"].build(cohort, z)
		pr, err := p.Predict(ctx, req)
		if err != nil || !pr.Valid || pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
			t.Fatalf("predict: the fused arm is not taken (err=%v)", err)
		}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		requireFigures(t, "fused", perRowFigures(t, resp, nil), byName["crosstab-day-by-site"].reference(t, rows))
	})
}
