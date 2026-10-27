package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// A date part of a null date is null (U50 E5-S2). ATTR_DATE_PART used
// to answer 0 for a null `date` / `datetime` cell — a real-looking
// number that dragged a MIN to 0, counted in a COUNT and keyed a "0"
// group. It is now null in Data and counted in the aggregation's
// n_null, like a null zone cell.

// nullDateRow is one fixture row: a calendar day and an instant, both
// empty (null) together when null is set.
type nullDateRow struct {
	day  time.Time
	ts   time.Time
	site string
	half string
	null bool
}

// nullDateRows: 48 rows over 2024-2025 at hours 1..23 (no hour reads
// 0, so a legacy 0 is never a real part), every 4th row null.
func nullDateRows() []nullDateRow {
	var rows []nullDateRow
	for i := range 48 {
		day := time.Date(2024+i%2, time.Month(1+i%12), 1+(i*5)%28, 0, 0, 0, 0, time.UTC)
		rows = append(rows, nullDateRow{
			day:  day,
			ts:   day.Add(time.Duration(1+i%23)*time.Hour + 17*time.Minute),
			site: []string{"a", "b", "c"}[i%3],
			half: []string{"x", "y"}[i%2],
			null: i%4 == 1,
		})
	}
	return rows
}

// importNullDates imports rows as a single file and a two-shard
// archive into fs.
func importNullDates(t *testing.T, fs afero.Fs, rows []nullDateRow) (single, archive string) {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	imp := func(stem string, rows []nullDateRow) string {
		var b strings.Builder
		b.WriteString("d,ts,site,half,n\n")
		for i, r := range rows {
			d, ts := "", ""
			if !r.null {
				d, ts = r.day.Format("2006-01-02"), r.ts.Format(time.RFC3339)
			}
			fmt.Fprintf(&b, "%s,%s,%s,%s,%d\n", d, ts, r.site, r.half, i)
		}
		if err := afero.WriteFile(fs, stem+".csv", []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: stem + ".csv"})
		if err != nil {
			t.Fatalf("ImportFile %s: %v", stem, err)
		}
		return res.Path
	}
	single = imp("nulldates", rows)
	ins, err := p.Inspect(ctx, single)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"d": "date", "ts": "datetime"}
	for _, f := range ins.Fields {
		if w, ok := want[f.Name]; ok && (f.Type != w || !f.Nullable) {
			t.Fatalf("field %s inferred as %s (nullable %v), want nullable %s", f.Name, f.Type, f.Nullable, w)
		}
	}
	half := len(rows) / 2
	a, b := imp("nulldates_a", rows[:half]), imp("nulldates_b", rows[half:])
	archive = "nulldates_archive.pulse"
	if _, err := p.CreateShardArchive(ctx, archive, []string{a, b}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return single, archive
}

// nullDatePartRef is the part of t (time.Time accessors only).
func nullDatePartRef(part string, t time.Time) float64 {
	y, m, d := t.Date()
	switch part {
	case "year":
		return float64(y)
	case "month":
		return float64(m)
	case "day":
		return float64(d)
	case "year_month":
		return float64(y*100 + int(m))
	case "year_month_day":
		return float64(y*10000 + int(m)*100 + d)
	case "month_day":
		return float64(int(m)*100 + d)
	case "hour":
		return float64(t.Hour())
	case "iso_weekday":
		return float64((int(t.Weekday())+6)%7 + 1)
	}
	panic(part)
}

// TestDatePart_NullInputIsNullEveryMode: ATTR_DATE_PART over a null
// `date` / `datetime` cell is null for every part, in every execution
// mode (buffered projected / full / serial decode, both crosstab arms —
// the fused one asserted taken — streaming, a shard archive reduced in
// parallel and serially): MIN / MAX / COUNT of the part equal the
// non-null rows' reference (a legacy 0 drags MIN to 0 and COUNTs the
// row), and the aggregation's n_null counts every null row.
func TestDatePart_NullInputIsNullEveryMode(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := nullDateRows()
	cohort, archive := importNullDates(t, fs, rows)
	nulls := 0
	for _, r := range rows {
		if r.null {
			nulls++
		}
	}
	ctx := context.Background()
	runners := perRowRunners(t, fs)
	parts := []string{"year", "month", "day", "year_month", "year_month_day", "month_day", "iso_weekday", "hour"}
	for _, field := range []string{"d", "ts"} {
		for _, part := range parts {
			if part == "hour" && field == "d" {
				continue
			}
			value := func(r nullDateRow) float64 {
				if field == "d" {
					return nullDatePartRef(part, r.day)
				}
				return nullDatePartRef(part, r.ts)
			}
			attr := func() []*types.Attribute {
				return []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: field, Label: "p", Params: json.RawMessage(`{"part":"` + part + `"}`)}}
			}
			grouped := func(path string) *types.Request {
				return &types.Request{Cohort: &types.Cohort{Filename: path}, Attributes: attr(),
					Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "n", Label: "count"},
						{Type: types.AGG_COUNT, Field: "p", Label: "np"},
						{Type: types.AGG_MIN, Field: "p", Label: "lo"},
						{Type: types.AGG_MAX, Field: "p", Label: "hi"},
					}}
			}
			groupedRef := map[string]float64{}
			lo, hi := map[string]float64{}, map[string]float64{}
			for _, r := range rows {
				groupedRef[r.site+"|count"]++
				if r.null {
					continue
				}
				groupedRef[r.site+"|np"]++
				v := value(r)
				if l, ok := lo[r.site]; !ok || v < l {
					lo[r.site] = v
				}
				if h, ok := hi[r.site]; !ok || v > h {
					hi[r.site] = v
				}
			}
			for s := range lo {
				groupedRef[s+"|lo"], groupedRef[s+"|hi"] = lo[s], hi[s]
			}
			crosstab := func(path string) *types.Request {
				return &types.Request{Cohort: &types.Cohort{Filename: path}, Attributes: attr(),
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}},
						Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "half"}},
						Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "p"},
					}}
			}
			crosstabRef := map[string]float64{}
			for _, r := range rows {
				if !r.null {
					crosstabRef[r.site+"|"+r.half]++
				}
			}
			name := field + "/" + part
			t.Run(name+"/fused-taken", func(t *testing.T) {
				pr, err := nullDatePulse(t, pulse.Options{FS: fs}).Predict(ctx, crosstab(cohort))
				if err != nil || !pr.Valid || pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
					t.Fatalf("predict: the fused arm is not taken (err=%v)", err)
				}
			})
			for _, rn := range runners {
				path := cohort
				if rn.archive {
					path = archive
				}
				t.Run(name+"/"+rn.name+"/grouped", func(t *testing.T) {
					requireFigures(t, rn.name, rn.run(t, grouped(path)), groupedRef)
				})
				if rn.name != "stream" {
					t.Run(name+"/"+rn.name+"/crosstab", func(t *testing.T) {
						requireFigures(t, rn.name, rn.run(t, crosstab(path)), crosstabRef)
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
					resp, err := nullDatePulse(t, arm.o).Process(ctx, grouped(arm.path))
					if err != nil {
						t.Fatal(err)
					}
					for _, a := range resp.Components.Aggregations {
						want := nulls
						if a.Label == "count" {
							want = 0
						}
						if a.NNull != want || a.N != len(rows)-want {
							t.Fatalf("aggregation %s floor {n %d, n_null %d}, want {%d, %d}", a.Label, a.N, a.NNull, len(rows)-want, want)
						}
					}
				})
			}
		}
	}
}

// TestDateFeatures_NullInputIsNullEveryMode: the sibling audit's proof
// that FEAT_DATE_FEATURES never had the legacy 0 — over a null `date` /
// `datetime` cell every column is null in every execution mode
// (buffered Compute, streaming EmitRow, a shard archive): each column's
// COUNT and MIN equal the non-null rows' reference and its n_null
// counts every null row.
func TestDateFeatures_NullInputIsNullEveryMode(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := nullDateRows()
	cohort, archive := importNullDates(t, fs, rows)
	nulls := 0
	for _, r := range rows {
		if r.null {
			nulls++
		}
	}
	ctx := context.Background()
	runners := perRowRunners(t, fs)
	for _, field := range []string{"d", "ts"} {
		cols := map[string]string{"year": "year", "month": "month", "day": "day", "dow": "iso_weekday"}
		if field == "ts" {
			cols["hour"] = "hour"
		}
		build := func(path string) *types.Request {
			req := &types.Request{Cohort: &types.Cohort{Filename: path},
				Features: []*types.Feature{{Type: types.FEAT_DATE_FEATURES, Field: field, Label: "f"}},
				Groups:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}}}
			for col := range cols {
				req.Aggregations = append(req.Aggregations,
					&types.Aggregation{Type: types.AGG_COUNT, Field: "f_" + col, Label: "n_" + col},
					&types.Aggregation{Type: types.AGG_MIN, Field: "f_" + col, Label: "lo_" + col})
			}
			return req
		}
		ref := map[string]float64{}
		for _, r := range rows {
			if r.null {
				continue
			}
			at := r.day
			if field == "ts" {
				at = r.ts
			}
			for col, part := range cols {
				ref[r.site+"|n_"+col]++
				v := nullDatePartRef(part, at)
				if lo, ok := ref[r.site+"|lo_"+col]; !ok || v < lo {
					ref[r.site+"|lo_"+col] = v
				}
			}
		}
		for _, rn := range runners {
			path := cohort
			if rn.archive {
				path = archive
			}
			t.Run(field+"/"+rn.name, func(t *testing.T) {
				requireFigures(t, rn.name, rn.run(t, build(path)), ref)
			})
		}
		for _, path := range []string{cohort, archive} {
			t.Run(field+"/"+path+"/n_null", func(t *testing.T) {
				resp, err := nullDatePulse(t, pulse.Options{FS: fs}).Process(ctx, build(path))
				if err != nil {
					t.Fatal(err)
				}
				for _, a := range resp.Components.Aggregations {
					if a.NNull != nulls {
						t.Fatalf("aggregation %s n_null = %d, want %d", a.Label, a.NNull, nulls)
					}
				}
			})
		}
	}
}

func nullDatePulse(t *testing.T, o pulse.Options) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
