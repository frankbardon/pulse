package pulse_test

import (
	"context"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Local-Saturday exclusion under per-row zones (U50 E2-S2): the
// showcase's daily-screen shape — FEAT_DATE_FEATURES reading each row
// on its own site's clock, then FILTER_EXCLUDE on the ISO `dow` = 6.

// weekdayRows: each site sees one instant that is a LOCAL Saturday but
// not a UTC one (or the reverse) — the rows a UTC reading misjudges.
// Each half holds every site in one order, so both shards of the
// archive carry the same zone dictionary.
func weekdayRows(t *testing.T) []perRowRow {
	t.Helper()
	at := func(s string) int64 {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return tm.Unix()
	}
	return []perRowRow{
		{at("2026-03-14T02:00:00Z"), "nyc", "America/New_York"}, // 0: Fri 22:00 local, Sat UTC — kept
		{at("2026-03-13T23:30:00Z"), "ber", "Europe/Berlin"},    // 1: Sat 00:30 local, Fri UTC — dropped
		{at("2026-03-13T20:00:00Z"), "del", "Asia/Kolkata"},     // 2: Sat 01:30 local, Fri UTC — dropped
		{at("2026-03-14T12:00:00Z"), "lon", "UTC"},              // 3: Sat both — dropped
		{at("2026-03-15T03:30:00Z"), "nyc", "America/New_York"}, // 4: Sat 23:30 local, Sun UTC — dropped
		{at("2026-03-14T23:30:00Z"), "ber", "Europe/Berlin"},    // 5: Sun 00:30 local, Sat UTC — kept
		{at("2026-03-14T19:00:00Z"), "del", "Asia/Kolkata"},     // 6: Sun 00:30 local, Sat UTC — kept
		{at("2026-03-15T04:30:00Z"), "lon", "UTC"},              // 7: Sun both — kept
	}
}

// weekdayExcludeReq drops `dow` = 6 (ISO Saturday) read under z; zone
// "" with no field reads UTC.
func weekdayExcludeReq(cohort string, z perRowZoning) *types.Request {
	req := perRowBase(cohort, z)
	f := &types.Feature{Type: types.FEAT_DATE_FEATURES, Field: "ts", Label: "f"}
	z.zoned(&f.TimeZone, &f.TimeZoneField)
	req.Features = []*types.Feature{f}
	req.Filterers = append(req.Filterers, &types.Filterer{Type: types.FILTER_EXCLUDE, Field: "f_dow", Values: []string{"6"}})
	req.Aggregations = []*types.Aggregation{
		{Type: types.AGG_COUNT, Field: "n", Label: "count"},
		{Type: types.AGG_SUM, Field: "f_day", Label: "day"},
		{Type: types.AGG_SUM, Field: "f_hour", Label: "hour"},
	}
	req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}}
	return req
}

// TestTimeZoneField_ExcludeLocalSaturdayByISODow: FILTER_EXCLUDE on
// FEAT_DATE_FEATURES `dow` = 6 under a per-row zone (slot `tz_field` or
// request `time_zone_field`) drops exactly the rows that are Saturday
// on their own site's clock — a UTC Saturday that is a local Friday or
// Sunday is kept, a local Saturday that is a UTC Friday or Sunday is
// dropped — in every execution mode. The same request read in UTC
// keeps a different set, so the fixture proves the zone reached the
// feature. Each site keeps one row; its local day and hour name it.
func TestTimeZoneField_ExcludeLocalSaturdayByISODow(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := weekdayRows(t)
	cohort, archive := importPerRow(t, fs, "weekday", rows)
	want := map[string]float64{ // kept: rows 0, 5, 6, 7 (local day / hour)
		"nyc|count": 1, "nyc|day": 13, "nyc|hour": 22,
		"ber|count": 1, "ber|day": 15,
		"del|count": 1, "del|day": 15,
		"lon|count": 1, "lon|day": 15, "lon|hour": 4,
	}
	utcWant := map[string]float64{ // kept in UTC: rows 1, 2, 4, 7 (UTC day / hour)
		"ber|count": 1, "ber|day": 13, "ber|hour": 23,
		"del|count": 1, "del|day": 13, "del|hour": 20,
		"nyc|count": 1, "nyc|day": 15, "nyc|hour": 3,
		"lon|count": 1, "lon|day": 15, "lon|hour": 4,
	}
	runners := perRowRunners(t, fs)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	requireFigures(t, "utc reading", runners[0].run(t, weekdayExcludeReq(cohort, perRowZoning{})), utcWant)
	for _, zoning := range []struct {
		name string
		z    perRowZoning
	}{{"tz_field", perRowZoning{tzField: "tz"}}, {"time_zone_field", perRowZoning{reqField: "tz"}}} {
		for _, rn := range runners {
			t.Run(zoning.name+"/"+rn.name, func(t *testing.T) {
				path := cohort
				if rn.archive {
					path = archive
				}
				requireFigures(t, rn.name, rn.run(t, weekdayExcludeReq(path, zoning.z)), want)
			})
		}
		t.Run(zoning.name+"/predict", func(t *testing.T) {
			pr, err := p.Predict(context.Background(), weekdayExcludeReq(cohort, zoning.z))
			if err != nil || !pr.Valid {
				t.Fatalf("predict refuses the screen: valid=%v err=%v", pr != nil && pr.Valid, err)
			}
		})
	}
}
