package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/strictjson"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// zoneFieldCohort imports a 36-row CSV with a calendar date (d), a
// datetime (ts), a categorical (cat), a numeric (n), a categorical of
// IANA zone names (zone) and a categorical whose entries include a
// non-zone (badzone).
func zoneFieldCohort(t *testing.T) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	zones := []string{"America/Los_Angeles", "Europe/London", "Asia/Kolkata"}
	bad := []string{"Europe/Berlin", "Mars/Base"}
	body := "d,ts,cat,n,zone,badzone\n"
	for i := range 36 {
		day := 1 + i%28
		body += fmt.Sprintf("2024-%02d-%02d,2024-%02d-%02dT%02d:30:00Z,%s,%d,%s,%s\n",
			1+i%12, day, 1+i%12, day, i%24, []string{"a", "b", "c"}[i%3], i, zones[i%3], bad[i%2])
	}
	if err := afero.WriteFile(fs, "zf.csv", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "zf.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	ins, err := p.Inspect(context.Background(), res.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"d": "date", "ts": "datetime", "zone": "categorical_u8", "badzone": "categorical_u8"}
	for _, f := range ins.Fields {
		if w, ok := want[f.Name]; ok && f.Type != w {
			t.Fatalf("field %s inferred as %s, want %s — the zone-field tests would prove nothing", f.Name, f.Type, w)
		}
	}
	return fs, res.Path
}

// TestTimeZoneField_RefusalsPredictMatchRuntime: every per-row-zone
// refusal is raised identically — code, message and details — by
// predict and the runtime.
func TestTimeZoneField_RefusalsPredictMatchRuntime(t *testing.T) {
	fs, cohort := zoneFieldCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	month := json.RawMessage(`{"component":"month"}`)
	grp := func(g types.Group) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(), Groups: []*types.Group{&g}}
	}
	cases := []struct {
		name string
		req  func() *types.Request
		code errors.Code
	}{
		{"tz and tz_field", func() *types.Request {
			return grp(types.Group{Type: types.GROUP_DATE, Field: "ts", Params: month, TimeZone: "UTC", TimeZoneField: "zone"})
		}, errors.PROCESSING_CONFIG},
		{"tz_field on a non-capable operator", func() *types.Request {
			return grp(types.Group{Type: types.GROUP_CATEGORY, Field: "cat", TimeZoneField: "zone"})
		}, errors.PROCESSING_CONFIG},
		{"tz_field on a date field", func() *types.Request {
			return grp(types.Group{Type: types.GROUP_DATE, Field: "d", Params: month, TimeZoneField: "zone"})
		}, errors.PROCESSING_CONFIG},
		{"zone field not categorical", func() *types.Request {
			return grp(types.Group{Type: types.GROUP_DATE, Field: "ts", Params: month, TimeZoneField: "n"})
		}, errors.PROCESSING_CONFIG},
		{"zone field entry not a zone", func() *types.Request {
			return grp(types.Group{Type: types.GROUP_DATE, Field: "ts", Params: month, TimeZoneField: "badzone"})
		}, errors.PULSE_TIMEZONE_UNKNOWN},
		{"inherited zone field entry not a zone", func() *types.Request {
			r := grp(types.Group{Type: types.GROUP_DATE, Field: "ts", Params: month})
			r.TimeZoneField = "badzone"
			return r
		}, errors.PULSE_TIMEZONE_UNKNOWN},
		{"zone field missing", func() *types.Request {
			return grp(types.Group{Type: types.GROUP_DATE, Field: "ts", Params: month, TimeZoneField: "nope"})
		}, errors.SERVICE_VALIDATION},
		{"request zone field missing", func() *types.Request {
			r := grp(types.Group{Type: types.GROUP_CATEGORY, Field: "cat"})
			r.TimeZoneField = "nope"
			return r
		}, errors.SERVICE_VALIDATION},
		{"zone field onto a derived field", func() *types.Request {
			return grp(types.Group{Type: types.GROUP_DATE, Field: "derived_ts", Params: month, TimeZoneField: "zone"})
		}, errors.PROCESSING_CONFIG},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, rerr := p.Process(ctx, tc.req())
			requireCode(t, rerr, tc.code)
			sameEntry(t, predictEnvelope(t, p, fs, cohort, tc.req()), rerr)
		})
	}
}

// TestTimeZoneField_AcceptedPredictEchoes: a valid zone field is
// accepted by both sides, and predict echoes it as source "field".
func TestTimeZoneField_AcceptedPredictEchoes(t *testing.T) {
	fs, cohort := zoneFieldCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	for name, mk := range map[string]func() *types.Request{
		"slot": func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(),
				Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"month"}`), TimeZoneField: "zone"}}}
		},
		"request": func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(), TimeZoneField: "zone", TimeZone: "Asia/Tokyo",
				Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"month"}`)}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := p.Process(ctx, mk()); err != nil {
				t.Fatalf("runtime: %v", err)
			}
			env := predictEnvelope(t, p, fs, cohort, mk())
			if len(env.Errors) != 0 {
				t.Fatalf("predict errors: %+v", env.Errors)
			}
			zs := env.Data.(*descriptor.PredictResult).TimeZones
			if len(zs) != 1 || zs[0].Source != descriptor.ZoneSourceField || zs[0].TZField != "zone" || zs[0].TZ != nil {
				t.Fatalf("echo = %+v", zs)
			}
		})
	}
}

// TestTimeZoneField_JoinAndChainSchemas (FR-9): a zone field is judged
// against the schema the request executes over — the joined schema of
// a join, the synthesised schema of a chain stage — and a name absent
// there is refused identically by the validators and the runtime.
func TestTimeZoneField_JoinAndChainSchemas(t *testing.T) {
	fs, cohort := zoneFieldCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	joined := func(zoneField string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Aggregations: countAgg(),
			Groups:       []*types.Group{{Type: types.GROUP_DATE, Field: "r_ts", Params: json.RawMessage(`{"component":"month"}`), TimeZoneField: zoneField}},
			Joins:        []*types.JoinSpec{{Right: cohort, On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: "r_"}},
		}
	}
	t.Run("joined zone field accepted", func(t *testing.T) {
		if _, err := p.Process(ctx, joined("r_zone")); err != nil {
			t.Fatalf("runtime: %v", err)
		}
		if env := predictEnvelope(t, p, fs, cohort, joined("r_zone")); len(env.Errors) != 0 {
			t.Fatalf("predict refused what the runtime accepts: %+v", env.Errors)
		}
	})
	t.Run("joined zone field missing", func(t *testing.T) {
		_, rerr := p.Process(ctx, joined("r_nope"))
		requireCode(t, rerr, errors.SERVICE_VALIDATION)
		sameEntry(t, predictEnvelope(t, p, fs, cohort, joined("r_nope")), rerr)
	})
	t.Run("chain stage without the zone field", func(t *testing.T) {
		mk := func() *types.ChainRequest {
			return &types.ChainRequest{
				Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{
					{Request: &types.Request{
						Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}},
						Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
					}},
					{Request: &types.Request{
						TimeZoneField: "zone",
						Aggregations:  []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}},
					}},
				},
			}
		}
		_, rerr := p.ProcessChain(ctx, mk())
		ce := requireCode(t, rerr, errors.SERVICE_VALIDATION)
		if ce.Details["slot"] != "time_zone_field" || ce.Details["stage"] != 1 {
			t.Fatalf("details = %v", ce.Details)
		}
		sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), opts), rerr)
	})
	t.Run("facet", func(t *testing.T) {
		mk := func() *types.FacetRequest {
			return &types.FacetRequest{
				Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"}, TimeZoneField: "badzone",
				Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "ts", Params: dateRanges}},
			}
		}
		_, rerr := p.FacetSchema(ctx, mk())
		requireCode(t, rerr, errors.PULSE_TIMEZONE_UNKNOWN)
		sameEntry(t, descx.ValidateFacetWithOptions(bytes.NewReader(data), mk(), opts), rerr)
	})
	t.Run("compose", func(t *testing.T) {
		mk := func() *types.ComposedRequest {
			bad := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(),
				Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"month"}`), TimeZoneField: "n"}}}
			return &types.ComposedRequest{Requests: []*types.Request{groupDateOverTS(cohort, "", ""), bad}}
		}
		_, rerr := p.Compose(ctx, mk())
		requireCode(t, rerr, errors.PROCESSING_CONFIG)
		sameEntry(t, descx.ValidateComposeWithOptions(mk(), opts), rerr)
	})
}

// TestTimeZoneField_StrictDecodeAcceptsKeys: the strict request decoder
// accepts the new keys on every zone-carrying object.
func TestTimeZoneField_StrictDecodeAcceptsKeys(t *testing.T) {
	raw := []byte(`{"time_zone_field":"zone","groups":[{"type":"GROUP_DATE","field":"ts","tz_field":"zone"}],` +
		`"filterers":[{"type":"FILTER_DATE_RANGES","field":"ts","tz_field":"zone"}],` +
		`"attributes":[{"type":"ATTR_DATE_PART","field":"ts","tz_field":"zone"}],` +
		`"features":[{"type":"FEAT_DATE_FEATURES","field":"ts","tz_field":"zone"}]}`)
	req, err := strictjson.Decode[types.Request](raw, strictjson.Options{})
	if err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	if req.TimeZoneField != "zone" || req.Groups[0].TimeZoneField != "zone" || req.Filterers[0].TimeZoneField != "zone" ||
		req.Attributes[0].TimeZoneField != "zone" || req.Features[0].TimeZoneField != "zone" {
		t.Fatalf("decoded %+v", req)
	}
	if _, err := strictjson.Decode[types.FacetRequest]([]byte(`{"fields":["x"],"time_zone_field":"zone"}`), strictjson.Options{}); err != nil {
		t.Fatalf("strict facet decode: %v", err)
	}
}
