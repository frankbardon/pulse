package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// zoneCohort imports a 40-row CSV with a calendar `date` column (d), a
// `datetime` column (ts), a categorical (cat) and a numeric (n) into a
// fresh MemMapFs and returns the cohort path. Options are applied to
// the instance that runs the requests; the import runs on its own.
func zoneCohort(t *testing.T) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	body := "d,ts,cat,n\n"
	for i := range 40 {
		day := 1 + i%28
		body += fmt.Sprintf("2024-%02d-%02d,2024-%02d-%02dT%02d:30:00Z,%s,%d\n",
			1+i%12, day, 1+i%12, day, i%24, []string{"a", "b", "c"}[i%3], i)
	}
	if err := afero.WriteFile(fs, "z.csv", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "z.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	ins, err := p.Inspect(context.Background(), res.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"d": "date", "ts": "datetime"}
	for _, f := range ins.Fields {
		if w, ok := want[f.Name]; ok && f.Type != w {
			t.Fatalf("field %s inferred as %s, want %s — the zone tests would prove nothing", f.Name, f.Type, w)
		}
	}
	return fs, res.Path
}

func zonePulse(t *testing.T, fs afero.Fs, defaultZone string) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs, DefaultTimeZone: defaultZone})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var dateRanges = json.RawMessage(`{"ranges":[{"label":"h1","start":"2024-01-01","end":"2024-06-30"},{"label":"h2","start":"2024-07-01","end":"2024-12-31"}]}`)

func countAgg() []*types.Aggregation {
	return []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n", Label: "count"}}
}

// requireCode asserts err is a *errors.CodedError (through any
// wrapping) with the given code, and returns it.
func requireCode(t *testing.T, err error, code errors.Code) *errors.CodedError {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s, got nil error", code)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want %s, got uncoded error %v", code, err)
	}
	if ce.Code != code {
		t.Fatalf("code = %s, want %s (%v)", ce.Code, code, err)
	}
	return ce
}

// zoneCapableOverDate returns one request per zone-capable operator,
// each reading the calendar `date` field d, with tz set on the slot.
func zoneCapableOverDate(cohort, tz string) map[string]*types.Request {
	c := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	return map[string]*types.Request{
		"GROUP_DATE": {Cohort: c(), Aggregations: countAgg(),
			Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "d", Params: json.RawMessage(`{"component":"month"}`), TimeZone: tz}}},
		"GROUP_DATE_RANGES": {Cohort: c(), Aggregations: countAgg(),
			Groups: []*types.Group{{Type: types.GROUP_DATE_RANGES, Field: "d", Params: dateRanges, TimeZone: tz}}},
		"FILTER_DATE_RANGES": {Cohort: c(), Aggregations: countAgg(),
			Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "d", Params: dateRanges, TimeZone: tz}}},
		"ATTR_DATE_PART": {Cohort: c(), Aggregations: countAgg(),
			Attributes: []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: "d", Label: "yr", Params: json.RawMessage(`{"part":"year"}`), TimeZone: tz}}},
		"FEAT_DATE_FEATURES": {Cohort: c(), Aggregations: countAgg(),
			Features: []*types.Feature{{Type: types.FEAT_DATE_FEATURES, Field: "d", Label: "df", TimeZone: tz}}},
	}
}

// TestDateFieldRejectsTZ: an explicit slot `tz` on a zone-capable
// operator over a calendar `date` field is PROCESSING_CONFIG (even
// "UTC" — the slot asked for something a date cannot honour), while the
// same operator inheriting a non-UTC Request.TimeZone runs: the
// inherited zone is not applied to a date, and predict echoes tz null.
func TestDateFieldRejectsTZ(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()

	for _, tz := range []string{"Europe/Berlin", "UTC"} {
		for op, req := range zoneCapableOverDate(cohort, tz) {
			t.Run("explicit/"+tz+"/"+op, func(t *testing.T) {
				_, err := p.Process(ctx, req)
				ce := requireCode(t, err, errors.PROCESSING_CONFIG)
				if ce.Details["operator"] != op || ce.Details["tz"] != tz || ce.Details["field_type"] != "date" {
					t.Errorf("details = %v", ce.Details)
				}
			})
		}
	}
	for op, req := range zoneCapableOverDate(cohort, "") {
		t.Run("inherited/"+op, func(t *testing.T) {
			req.TimeZone = "Europe/Berlin"
			if _, err := p.Process(ctx, req); err != nil {
				t.Fatalf("Process with only Request.TimeZone: %v", err)
			}
			pr, err := p.Predict(ctx, req)
			if err != nil || !pr.Valid {
				t.Fatalf("Predict: valid=%v err=%v", pr != nil && pr.Valid, err)
			}
			if len(pr.TimeZones) != 1 || pr.TimeZones[0].TZ != nil || pr.TimeZones[0].Source != "request" || pr.TimeZones[0].Operator != op {
				t.Fatalf("TimeZones = %+v, want one %s slot with tz null from request", pr.TimeZones, op)
			}
		})
	}
}

func groupDateOverTS(cohort, slotTZ, reqTZ string) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Aggregations: countAgg(),
		Groups:       []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"month"}`), TimeZone: slotTZ}},
		TimeZone:     reqTZ,
	}
}

func assertRefusal(t *testing.T, err error, slot, op, tz string) {
	t.Helper()
	ce := requireCode(t, err, errors.PROCESSING_CONFIG)
	if ce.Details["operator"] == "GROUP_DATE" || ce.Details["operator"] == "FILTER_DATE_RANGES" {
		if !strings.Contains(ce.Message, "zone cannot be applied to a derived field") {
			t.Errorf("message %q does not name the derived-field rule", ce.Message)
		}
	}
	if ce.Details["slot"] != slot || ce.Details["operator"] != op || ce.Details["tz"] != tz {
		t.Errorf("details = %v, want slot=%s operator=%s tz=%s", ce.Details, slot, op, tz)
	}
}

// TestTimeZone_DerivedFieldRefusedEveryMode: a non-UTC zone resolving
// onto a field absent from the schema (a derived column — the operators
// would read it as epoch days and silently ignore the zone) — whether
// from the slot, the request or Options.DefaultTimeZone — is refused
// with PROCESSING_CONFIG and {slot, operator, tz} in every execution
// mode, FacetSchema included, and predict refuses it too. (A non-UTC
// zone on a `datetime` schema field is applied:
// TestTimeZone_LocalDayEveryMode.)
func TestTimeZone_DerivedFieldRefusedEveryMode(t *testing.T) {
	fs, cohort := zoneCohort(t)
	ctx := context.Background()
	const berlin = "Europe/Berlin"
	const derived = "derived_ts"
	sources := []struct {
		name, slot, req, opts string
	}{
		{"slot", berlin, "", ""},
		{"request", "", berlin, ""},
		{"options", "", "", berlin},
	}
	for _, src := range sources {
		p := zonePulse(t, fs, src.opts)
		mk := func() *types.Request {
			req := groupDateOverTS(cohort, src.slot, src.req)
			req.Groups[0].Field = derived
			return req
		}
		t.Run(src.name+"/process", func(t *testing.T) {
			_, err := p.Process(ctx, mk())
			assertRefusal(t, err, "groups[0]", "GROUP_DATE", berlin)
		})
		t.Run(src.name+"/stream", func(t *testing.T) {
			_, err := p.ProcessStream(ctx, mk())
			assertRefusal(t, err, "groups[0]", "GROUP_DATE", berlin)
		})
		t.Run(src.name+"/compose", func(t *testing.T) {
			_, err := p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{mk()}})
			assertRefusal(t, err, "groups[0]", "GROUP_DATE", berlin)
		})
		t.Run(src.name+"/compose-parallel", func(t *testing.T) {
			_, err := p.ComposeParallel(ctx, &types.ComposedRequest{Requests: []*types.Request{mk(), mk()}}, pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
			assertRefusal(t, err, "groups[0]", "GROUP_DATE", berlin)
		})
		t.Run(src.name+"/chain", func(t *testing.T) {
			// The chain arm uses the mergeable FILTER_DATE_RANGES (zones
			// resolve before the chain gate either way —
			// TestTimeZone_RefusalCarriesLocation).
			_, err := p.ProcessChain(ctx, &types.ChainRequest{
				Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{{Request: &types.Request{
					Aggregations: countAgg(),
					TimeZone:     src.req,
					Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: derived, Params: dateRanges, TimeZone: src.slot}},
				}}},
			})
			assertRefusal(t, err, "filterers[0]", "FILTER_DATE_RANGES", berlin)
		})
		t.Run(src.name+"/crosstab", func(t *testing.T) {
			_, err := p.Process(ctx, &types.Request{
				Cohort:   &types.Cohort{Filename: cohort},
				TimeZone: src.req,
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_DATE, Field: derived, Params: json.RawMessage(`{"component":"month"}`), TimeZone: src.slot}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "n"},
				},
			})
			assertRefusal(t, err, "crosstab.rows[0]", "GROUP_DATE", berlin)
		})
		t.Run(src.name+"/join", func(t *testing.T) {
			req := mk()
			req.Joins = []*types.JoinSpec{{Right: cohort, On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: "r"}}
			_, err := p.Process(ctx, req)
			assertRefusal(t, err, "groups[0]", "GROUP_DATE", berlin)
		})
		t.Run(src.name+"/facet", func(t *testing.T) {
			_, err := p.FacetSchema(ctx, &types.FacetRequest{
				Cohort:    &types.Cohort{Filename: cohort},
				Fields:    []string{"cat"},
				TimeZone:  src.req,
				Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: derived, Params: dateRanges, TimeZone: src.slot}},
			})
			assertRefusal(t, err, "filterers[0]", "FILTER_DATE_RANGES", berlin)
		})
		t.Run(src.name+"/predict", func(t *testing.T) {
			pr, err := p.Predict(ctx, mk())
			if err != nil {
				t.Fatal(err)
			}
			if pr.Valid {
				t.Fatalf("predict accepted a request the runtime refuses")
			}
			env := predictEnvelope(t, p, fs, cohort, mk())
			_, rerr := p.Process(ctx, mk())
			sameEntry(t, env, rerr)
		})
	}
}

// TestTimeZone_ChainLaterStageResolved: stages after the first run
// against a synthesised schema, not through Process, so they carry
// their own resolution call — a `tz` on a non-capable stage-1 slot is
// refused there too.
func TestTimeZone_ChainLaterStageResolved(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	_, err := p.ProcessChain(context.Background(), &types.ChainRequest{
		Cohort: &types.Cohort{Filename: cohort},
		Stages: []*types.ChainStage{
			{Request: &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}},
			}},
			{Request: &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat", TimeZone: "UTC"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}},
			}},
		},
	})
	assertRefusal(t, err, "groups[0]", "GROUP_CATEGORY", "UTC")
}

// TestTimeZone_NonCapableOperatorRefused: `tz` on a slot whose
// operator is not zone-capable — built-in or embedder-registered — is
// PROCESSING_CONFIG, never silently ignored.
func TestTimeZone_NonCapableOperatorRefused(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs, Extensions: pulse.Extensions{
		Filterers: []pulse.FiltererRegistration{{Name: "FILTER_ACME_PASS", Factory: stubFiltererFactory}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	c := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	cases := []struct {
		slot, op string
		req      *types.Request
	}{
		{"groups[0]", "GROUP_CATEGORY", &types.Request{Cohort: c(), Aggregations: countAgg(),
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat", TimeZone: "UTC"}}}},
		{"filterers[0]", "FILTER_INCLUDE", &types.Request{Cohort: c(), Aggregations: countAgg(),
			Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "cat", Values: []string{"a"}, TimeZone: "UTC"}}}},
		{"attributes[0]", "ATTR_ZSCORE", &types.Request{Cohort: c(), Aggregations: countAgg(),
			Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "n", Label: "z", TimeZone: "UTC"}}}},
		{"filterers[0]", "FILTER_ACME_PASS", &types.Request{Cohort: c(), Aggregations: countAgg(),
			Filterers: []*types.Filterer{{Type: "FILTER_ACME_PASS", Field: "n", TimeZone: "UTC"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			_, err := p.Process(context.Background(), tc.req)
			assertRefusal(t, err, tc.slot, tc.op, "UTC")
			pr, perr := p.Predict(context.Background(), tc.req)
			if perr != nil || pr.Valid {
				t.Fatalf("predict valid=%v err=%v, want invalid", pr != nil && pr.Valid, perr)
			}
		})
	}
}

// TestTimeZone_UnknownZoneRefused: an unknown name anywhere — slot,
// request, facet request — is PULSE_TIMEZONE_UNKNOWN, even when no
// zone-capable slot would consume it.
func TestTimeZone_UnknownZoneRefused(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	_, err := p.Process(ctx, groupDateOverTS(cohort, "Mars/Olympus", ""))
	ce := requireCode(t, err, errors.PULSE_TIMEZONE_UNKNOWN)
	if ce.Details["slot"] != "groups[0]" || ce.Details["tz"] != "Mars/Olympus" {
		t.Errorf("details = %v", ce.Details)
	}
	_, err = p.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(), TimeZone: "EST"})
	requireCode(t, err, errors.PULSE_TIMEZONE_UNKNOWN)
	_, err = p.FacetSchema(ctx, &types.FacetRequest{Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"}, TimeZone: "Local"})
	requireCode(t, err, errors.PULSE_TIMEZONE_UNKNOWN)
}

// TestFilterToFileRequest_RefusesTZ: the structured filter-to-file
// translator supports no zone-capable filterer, so a `tz` there is
// refused rather than dropped.
func TestFilterToFileRequest_RefusesTZ(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	_, err := p.FilterToFileWithRequest(context.Background(), &pulse.FilterToFileRequest{
		SourcePath: cohort,
		Filterers:  []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "cat", Values: []string{"a"}, TimeZone: "UTC"}},
		OutputDir:  "out",
	})
	requireCode(t, err, errors.PROCESSING_CONFIG)
}
