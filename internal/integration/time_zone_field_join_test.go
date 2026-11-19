package integration_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Per-row zones through a join (U75 E2-S1, #481). The per-row oracle's
// requests run over a join: the zone-carrying cohort on the LEFT
// (fields as written) or on the RIGHT (every zone-oracle field read
// through the join's `r_` prefix), the other side a decoy whose zone
// column holds the WRONG zone for every site — so a join arm that read
// the other side's zone column, or dropped the zone column, fails the
// oracle. The join is the one supported kind (inner, one per request);
// its "null rows" are the zone side's null zone cells, which give a
// null local value through the join exactly as on a single cohort.

// perRowJoinPrefix is every join's right-side `As` prefix.
const perRowJoinPrefix = "r_"

// perRowJoinFields are the zone-carrying cohort's own fields an oracle
// request names (aggregation inputs and derived labels stay as they
// are: the count reads the left key, labels are request names).
var perRowJoinFields = map[string]bool{"ts": true, "site": true, "tz": true}

// perRowDecoy is rows with every site's zone swapped for another
// site's (nulls kept), so a run reading the decoy's zones differs.
func perRowDecoy(rows []perRowRow) []perRowRow {
	other := map[string]string{}
	for i, st := range perRowSites {
		other[st.zone] = perRowSites[(i+1)%len(perRowSites)].zone
	}
	out := make([]perRowRow, len(rows))
	for i, r := range rows {
		out[i] = r
		if r.zone != "" {
			out[i].zone = other[r.zone]
		}
	}
	return out
}

// importPerRowFile imports rows as one single-file cohort.
func importPerRowFile(t *testing.T, fs afero.Fs, stem string, rows []perRowRow) string {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, stem+".csv", []byte(perRowCSV(rows)), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: stem + ".csv"})
	if err != nil {
		t.Fatalf("ImportFile %s: %v", stem, err)
	}
	return res.Path
}

// perRowJoin is one placement of the zone-carrying cohort in a join.
type perRowJoin struct {
	name string
	// zoneField is the joined name of the zone column ("tz" / "r_tz").
	zoneField string
	// rewrite turns an oracle request built over the zone cohort into
	// the joined request: zone cohort on its side, decoy on the other.
	rewrite func(req *types.Request, zone, decoy string) *types.Request
}

func perRowJoinSpec(right string) []*types.JoinSpec {
	return []*types.JoinSpec{{Right: right, On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: perRowJoinPrefix}}
}

// perRowJoins: the zone column on the left cohort, and on the right.
var perRowJoins = []perRowJoin{
	{name: "join-zone-left", zoneField: "tz", rewrite: func(req *types.Request, zone, decoy string) *types.Request {
		req.Cohort = &types.Cohort{Filename: zone}
		req.Joins = perRowJoinSpec(decoy)
		return req
	}},
	{name: "join-zone-right", zoneField: perRowJoinPrefix + "tz", rewrite: func(req *types.Request, zone, decoy string) *types.Request {
		req.Cohort = &types.Cohort{Filename: decoy}
		req.Joins = perRowJoinSpec(zone)
		prefixPerRowFields(req)
		return req
	}},
}

// prefixPerRowFields renames every zone-oracle field req names to its
// right-side joined name.
func prefixPerRowFields(req *types.Request) {
	p := func(s *string) {
		if perRowJoinFields[*s] {
			*s = perRowJoinPrefix + *s
		}
	}
	p(&req.TimeZoneField)
	for _, f := range req.Filterers {
		p(&f.Field)
		p(&f.TimeZoneField)
	}
	for _, a := range req.Attributes {
		p(&a.Field)
		p(&a.TimeZoneField)
	}
	for _, f := range req.Features {
		p(&f.Field)
		p(&f.TimeZoneField)
	}
	// Groups are shared across cases (perRowCases' site group): copy.
	clone := func(gs []*types.Group) []*types.Group {
		out := make([]*types.Group, len(gs))
		for i, g := range gs {
			c := *g
			p(&c.Field)
			p(&c.TimeZoneField)
			out[i] = &c
		}
		return out
	}
	req.Groups = clone(req.Groups)
	if ct := req.Crosstab; ct != nil {
		c := *ct
		c.Rows, c.Columns = clone(ct.Rows), clone(ct.Columns)
		req.Crosstab = &c
	}
}

// requireJoinEcho asserts predict's zone echo for a joined request
// names the zone field the run read: every zone-capable slot resolves
// per row from zoneField with the given source.
func requireJoinEcho(t *testing.T, p *pulse.Pulse, fs afero.Fs, req *types.Request, source, zoneField string) {
	t.Helper()
	zs := zoneEcho(t, p, fs, req.Cohort.Filename, req)
	if len(zs) == 0 {
		t.Fatal("predict echoed no zone-capable slot")
	}
	for _, z := range zs {
		if z.Source != source || z.TZField != zoneField || z.TZ != nil {
			t.Fatalf("predict echo %+v, want source %s tz_field %s tz null", z, source, zoneField)
		}
	}
}

// TestTimeZoneField_JoinPerRowEchoMatchesRuntime: one per-row case run
// through both join placements — the zone column on the left and on
// the right (r_tz) — equals the time.In reference (the fast-tier
// guard; the heavy oracle runs every case in every arm), and predict
// echoes the joined zone field the runtime read (source "field").
func TestTimeZoneField_JoinPerRowEchoMatchesRuntime(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	rows := perRowRows(0)
	zone := importPerRowFile(t, fs, "jz", rows)
	decoy := importPerRowFile(t, fs, "jz_decoy", perRowDecoy(rows))
	p := zonePulse(t, fs, "")
	var tc perRowCase
	for _, c := range perRowCases() {
		if c.name == "site-day" {
			tc = c
		}
	}
	want := tc.reference(t, rows)
	for _, j := range perRowJoins {
		t.Run(j.name, func(t *testing.T) {
			mk := func() *types.Request { return j.rewrite(tc.build("", perRowZoning{reqField: "tz"}), zone, decoy) }
			if j.zoneField != "tz" {
				if r := mk(); r.TimeZoneField != j.zoneField || r.Groups[1].Field != perRowJoinPrefix+"ts" {
					t.Fatalf("rewrite did not prefix the zone-oracle fields: %+v", r)
				}
			}
			requireJoinEcho(t, p, fs, mk(), descriptor.ZoneSourceField, j.zoneField)
			requireFigures(t, j.name, runFigures(t, p, mk()), want)
		})
	}
	// Sanity: the decoy side's zones give different figures.
	if d := runFigures(t, p, tc.build(decoy, perRowZoning{reqField: "tz"})); len(d) == len(want) {
		same := true
		for k, v := range want {
			if d[k] != v {
				same = false
			}
		}
		if same {
			t.Fatal("fixture: the decoy's zones give the reference figures — the join cases prove nothing")
		}
	}
}
