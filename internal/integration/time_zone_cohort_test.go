package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Cohort default zone field (U75 E1-S2): a cohort whose schema declares
// a default zone field (encoding.Schema.ZoneField) resolves every
// zone-capable slot no request zone reaches per row from that field —
// predict echoes source "cohort", and the run equals the same request
// naming the field as `time_zone_field` over the undeclared cohort.

// declareZoneField copies the single-file cohort at src to dst with its
// schema declaring field the cohort default zone field; the record
// bytes are copied unchanged.
func declareZoneField(t *testing.T, fs afero.Fs, src, dst, field string) string {
	t.Helper()
	data, err := afero.ReadFile(fs, src)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(data)
	s, _, err := encx.ReadPreamble(r)
	if err != nil {
		t.Fatal(err)
	}
	records, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	s.ZoneField = field
	var out bytes.Buffer
	if err := encx.WritePreamble(&out, s); err != nil {
		t.Fatal(err)
	}
	out.Write(records)
	if err := afero.WriteFile(fs, dst, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

// zoneEcho returns predict's TimeZones for req over the cohort at path.
func zoneEcho(t *testing.T, p *pulse.Pulse, fs afero.Fs, path string, req *types.Request) []descriptor.ResolvedZone {
	t.Helper()
	env := predictEnvelope(t, p, fs, path, req)
	if len(env.Errors) != 0 {
		t.Fatalf("predict errors: %+v", env.Errors)
	}
	return env.Data.(*descriptor.PredictResult).TimeZones
}

// runFigures runs req and flattens it (perRowFigures).
func runFigures(t *testing.T, p *pulse.Pulse, req *types.Request) map[string]float64 {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return perRowFigures(t, resp, req.Groups)
}

// TestTimeZoneCohort_PrecedenceAtRuntime pins the cohort rung at
// runtime with the predict echo beside it: a request `time_zone` beats
// the cohort default, and the cohort default beats
// Options.DefaultTimeZone.
func TestTimeZoneCohort_PrecedenceAtRuntime(t *testing.T) {
	t.Parallel()

	fs, plain := zoneFieldCohort(t)
	declared := declareZoneField(t, fs, plain, "zf_declared.pulse", "zone")
	p := zonePulse(t, fs, "Asia/Tokyo")
	day := func(cohort, reqTZ, reqField string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(), TimeZone: reqTZ, TimeZoneField: reqField,
			Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"day"}`)}}}
	}
	cases := []struct {
		name     string
		declared *types.Request
		twin     *types.Request
		source   string
	}{
		{"request zone beats cohort", day(declared, "America/Los_Angeles", ""), day(plain, "America/Los_Angeles", ""), descriptor.ZoneSourceRequest},
		{"cohort beats options", day(declared, "", ""), day(plain, "", "zone"), descriptor.ZoneSourceCohort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			zs := zoneEcho(t, p, fs, declared, tc.declared)
			if len(zs) != 1 || zs[0].Source != tc.source {
				t.Fatalf("echo = %+v, want source %s", zs, tc.source)
			}
			requireFigures(t, tc.name, runFigures(t, p, tc.declared), runFigures(t, p, tc.twin))
		})
	}
	// Sanity: the options zone alone differs from the per-row zones, so
	// "cohort beats options" is not vacuous.
	opt, cohort := runFigures(t, p, day(plain, "", "")), runFigures(t, p, day(declared, "", ""))
	if len(opt) == len(cohort) {
		same := true
		for k, v := range opt {
			if cohort[k] != v {
				same = false
			}
		}
		if same {
			t.Fatal("fixture: the options zone and the cohort default give the same figures")
		}
	}
}

// TestTimeZoneCohort_RefusalPredictMatchesRuntime: the cohort default
// onto a derived slot field is refused identically by predict and the
// runtime, and a calendar-date slot never inherits it (accepted by
// both, echoed null tz).
func TestTimeZoneCohort_RefusalPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	fs, plain := zoneFieldCohort(t)
	declared := declareZoneField(t, fs, plain, "zf_declared.pulse", "zone")
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	mk := func(field string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: declared}, Aggregations: countAgg(),
			Groups: []*types.Group{{Type: types.GROUP_DATE, Field: field, Params: json.RawMessage(`{"component":"month"}`)}}}
	}
	_, rerr := p.Process(ctx, mk("derived_ts"))
	requireCode(t, rerr, errors.PROCESSING_CONFIG)
	sameEntry(t, predictEnvelope(t, p, fs, declared, mk("derived_ts")), rerr)

	if _, err := p.Process(ctx, mk("d")); err != nil {
		t.Fatalf("calendar date refused: %v", err)
	}
	zs := zoneEcho(t, p, fs, declared, mk("d"))
	if len(zs) != 1 || zs[0].TZ != nil || zs[0].Source != descriptor.ZoneSourceCohort {
		t.Fatalf("echo = %+v", zs)
	}
}

// TestTimeZoneCohort_JoinCarriesLeftDefault: a join carries the LEFT
// cohort's default — a joined left datetime slot runs per row from it,
// equal to `time_zone_field` — while the right cohort's default is not
// inherited: a left-undeclared join runs exactly like the undeclared
// join. Predict echoes the same sources the runtime ran.
func TestTimeZoneCohort_JoinCarriesLeftDefault(t *testing.T) {
	t.Parallel()

	fs, plain := zoneFieldCohort(t)
	declared := declareZoneField(t, fs, plain, "zf_declared.pulse", "zone")
	p := zonePulse(t, fs, "")
	joined := func(left, right, field, reqField string) *types.Request {
		return &types.Request{
			Cohort:        &types.Cohort{Filename: left},
			Aggregations:  countAgg(),
			TimeZoneField: reqField,
			Groups:        []*types.Group{{Type: types.GROUP_DATE, Field: field, Params: json.RawMessage(`{"component":"hour"}`)}},
			Joins:         []*types.JoinSpec{{Right: right, On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: "r_"}},
		}
	}
	t.Run("left default carried", func(t *testing.T) {
		zs := zoneEcho(t, p, fs, declared, joined(declared, plain, "ts", ""))
		if len(zs) != 1 || zs[0].Source != descriptor.ZoneSourceCohort || zs[0].TZField != "zone" {
			t.Fatalf("echo = %+v", zs)
		}
		got := runFigures(t, p, joined(declared, plain, "ts", ""))
		requireFigures(t, "left default", got, runFigures(t, p, joined(plain, plain, "ts", "zone")))
		if utc := runFigures(t, p, joined(plain, plain, "ts", "")); len(utc) == len(got) {
			same := true
			for k, v := range utc {
				if got[k] != v {
					same = false
				}
			}
			if same {
				t.Fatal("fixture: per-row hours equal UTC hours — the join case proves nothing")
			}
		}
	})
	t.Run("right default not inherited", func(t *testing.T) {
		zs := zoneEcho(t, p, fs, plain, joined(plain, declared, "r_ts", ""))
		if len(zs) != 1 || zs[0].Source != descriptor.ZoneSourceDefault || zs[0].TZField != "" {
			t.Fatalf("echo = %+v", zs)
		}
		requireFigures(t, "right default", runFigures(t, p, joined(plain, declared, "r_ts", "")), runFigures(t, p, joined(plain, plain, "r_ts", "")))
	})
}

// TestTimeZoneCohort_ChainStages: chain stage 0 inherits the cohort
// default (equal to `time_zone_field` on stage 0); a stage >= 1 inherits
// nothing — over the declared cohort it behaves exactly as over the
// undeclared one, refusing a zone-capable slot over an absent column
// with the same field-reference refusal (never a zone refusal naming
// the default), identically in the chain validator and the runtime.
func TestTimeZoneCohort_ChainStages(t *testing.T) {
	t.Parallel()

	fs, plain := zoneFieldCohort(t)
	declared := declareZoneField(t, fs, plain, "zf_declared.pulse", "zone")
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}

	stage0 := func(cohort, reqField string) *types.ChainRequest {
		return &types.ChainRequest{
			Cohort: &types.Cohort{Filename: cohort},
			Stages: []*types.ChainStage{
				{Request: &types.Request{TimeZoneField: reqField,
					Attributes:   []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: "ts", Label: "lhour", Params: json.RawMessage(`{"part":"hour"}`)}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "lhour", Label: "h"}},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}}},
				{Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "h", Label: "total"}}}},
			},
		}
	}
	t.Run("stage 0 inherits", func(t *testing.T) {
		got, err := p.ProcessChain(ctx, stage0(declared, ""))
		if err != nil {
			t.Fatal(err)
		}
		want, err := p.ProcessChain(ctx, stage0(plain, "zone"))
		if err != nil {
			t.Fatal(err)
		}
		utc, err := p.ProcessChain(ctx, stage0(plain, ""))
		if err != nil {
			t.Fatal(err)
		}
		g, _ := json.Marshal(got.Stages[0].Data)
		w, _ := json.Marshal(want.Stages[0].Data)
		u, _ := json.Marshal(utc.Stages[0].Data)
		if string(g) != string(w) {
			t.Fatalf("stage 0 over the declared cohort:\n%s\nwant (time_zone_field):\n%s", g, w)
		}
		if string(g) == string(u) {
			t.Fatal("fixture: local hours equal UTC hours — the stage-0 case proves nothing")
		}
	})
	t.Run("stage 1 inherits nothing", func(t *testing.T) {
		mk := func(cohort string) *types.ChainRequest {
			return &types.ChainRequest{
				Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{
					{Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}},
						Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}}},
					{Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}},
						Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "no_ts", Params: dateRanges}}}},
				},
			}
		}
		_, plainErr := p.ProcessChain(ctx, mk(plain))
		_, declErr := p.ProcessChain(ctx, mk(declared))
		pce := requireCode(t, plainErr, errors.SERVICE_VALIDATION)
		dce := requireCode(t, declErr, errors.SERVICE_VALIDATION)
		if pce.Message != dce.Message {
			t.Fatalf("declared stage 1 refusal %q differs from undeclared %q", dce.Message, pce.Message)
		}
		data, err := afero.ReadFile(fs, declared)
		if err != nil {
			t.Fatal(err)
		}
		sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk(declared), opts), declErr)
	})
}

// TestTimeZoneCohort_InspectShowsDefault: inspect reports the declared
// default (read from the schema block) and omits it when undeclared.
func TestTimeZoneCohort_InspectShowsDefault(t *testing.T) {
	t.Parallel()

	fs, plain := zoneFieldCohort(t)
	declared := declareZoneField(t, fs, plain, "zf_declared.pulse", "zone")
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	for path, want := range map[string]string{declared: "zone", plain: ""} {
		res, err := p.Inspect(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if res.ZoneField != want {
			t.Fatalf("%s: inspect zone_field = %q, want %q", path, res.ZoneField, want)
		}
		raw, _ := json.Marshal(res)
		if has := bytes.Contains(raw, []byte(`"zone_field"`)); has != (want != "") {
			t.Fatalf("%s: zone_field key present = %v in %s", path, has, raw)
		}
	}
}
