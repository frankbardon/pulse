package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// predictEnvelope runs PredictBytes over the cohort's own bytes so the
// test sees the envelope's coded errors (Pulse.Predict returns only the
// result).
func predictEnvelope(t *testing.T, p *pulse.Pulse, fs afero.Fs, cohort string, req *types.Request) *descriptor.Envelope {
	t.Helper()
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	env, err := p.PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// schemaLoaderFor reads header + schema from fs — the shape the facade
// hands the validators.
func schemaLoaderFor(fs afero.Fs) func(string) (*encoding.Schema, error) {
	return func(path string) (*encoding.Schema, error) {
		data, err := afero.ReadFile(fs, path)
		if err != nil {
			return nil, err
		}
		r := bytes.NewReader(data)
		v, err := encoding.ReadHeader(r)
		if err != nil {
			return nil, err
		}
		return encoding.ReadSchema(r, v)
	}
}

// sameEntry asserts the first envelope error carries exactly the
// runtime error's code, message and details.
func sameEntry(t *testing.T, env *descriptor.Envelope, runtimeErr error) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(runtimeErr, &ce) {
		t.Fatalf("runtime error is not coded: %v", runtimeErr)
	}
	if len(env.Errors) == 0 {
		t.Fatalf("validator accepted what the runtime refused (%v)", runtimeErr)
	}
	got := env.Errors[0]
	if got.Code != string(ce.Code) || got.Message != ce.Message {
		t.Fatalf("validator error = %s %q, runtime = %s %q", got.Code, got.Message, ce.Code, ce.Message)
	}
	want, _ := json.Marshal(ce.Details)
	have, _ := json.Marshal(got.Details)
	if string(want) != string(have) {
		t.Fatalf("validator details = %s, runtime details = %s", have, want)
	}
}

// derivedGroupDate is groupDateOverTS over a name absent from the
// schema — a derived column a non-UTC zone cannot be applied to.
func derivedGroupDate(cohort, slotTZ, reqTZ string) *types.Request {
	req := groupDateOverTS(cohort, slotTZ, reqTZ)
	req.Groups[0].Field = "derived_ts"
	return req
}

func joinedGroupDate(cohort, field, reqTZ string) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		TimeZone:     reqTZ,
		Aggregations: countAgg(),
		Groups:       []*types.Group{{Type: types.GROUP_DATE, Field: field, Params: json.RawMessage(`{"component":"month"}`)}},
		Joins:        []*types.JoinSpec{{Right: cohort, On: []types.OnPair{{LeftField: "n", RightField: "n"}}, As: "r_"}},
	}
}

// TestTimeZone_JoinPredictMatchesRuntime: predict resolves a joined
// request's zones against the joined schema the runtime executes over.
// An inherited non-UTC zone over a right-side `date` field is accepted
// by both (predict echoes tz null, field_type date); over a right-side
// `datetime` field both accept and predict echoes the zone; over a name
// in neither schema (a derived column) both refuse with the same code
// and details.
func TestTimeZone_JoinPredictMatchesRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	const berlin = "Europe/Berlin"

	t.Run("joined date accepted", func(t *testing.T) {
		if _, err := p.Process(ctx, joinedGroupDate(cohort, "r_d", berlin)); err != nil {
			t.Fatalf("runtime: %v", err)
		}
		pr, err := p.Predict(ctx, joinedGroupDate(cohort, "r_d", berlin))
		if err != nil || !pr.Valid {
			env := predictEnvelope(t, p, fs, cohort, joinedGroupDate(cohort, "r_d", berlin))
			t.Fatalf("predict refused what runtime accepts: valid=%v err=%v errors=%+v", pr != nil && pr.Valid, err, env.Errors)
		}
		if len(pr.TimeZones) != 1 || pr.TimeZones[0].TZ != nil || pr.TimeZones[0].FieldType != "date" {
			t.Fatalf("TimeZones = %+v, want one date slot with tz null", pr.TimeZones)
		}
	})
	t.Run("joined datetime accepted", func(t *testing.T) {
		if _, err := p.Process(ctx, joinedGroupDate(cohort, "r_ts", berlin)); err != nil {
			t.Fatalf("runtime: %v", err)
		}
		pr, err := p.Predict(ctx, joinedGroupDate(cohort, "r_ts", berlin))
		if err != nil || !pr.Valid {
			t.Fatalf("predict refused what runtime accepts: valid=%v err=%v", pr != nil && pr.Valid, err)
		}
		if len(pr.TimeZones) != 1 || pr.TimeZones[0].TZ == nil || *pr.TimeZones[0].TZ != berlin || pr.TimeZones[0].FieldType != "datetime" {
			t.Fatalf("TimeZones = %+v, want one datetime slot resolving %s", pr.TimeZones, berlin)
		}
	})
	t.Run("derived refused", func(t *testing.T) {
		_, rerr := p.Process(ctx, joinedGroupDate(cohort, "r_derived", berlin))
		assertRefusal(t, rerr, "groups[0]", "GROUP_DATE", berlin)
		env := predictEnvelope(t, p, fs, cohort, joinedGroupDate(cohort, "r_derived", berlin))
		sameEntry(t, env, rerr)
		if env.Data.(*descriptor.PredictResult).Valid {
			t.Fatal("predict Valid=true")
		}
	})
}

// TestTimeZone_DisableDefaultsSameErrorBothSides: with smart defaults
// off, a slot left without a Type is a type error, not a zone error —
// runtime and predict report the runtime's own "unknown … type"
// PROCESSING_CONFIG whether or not the slot carries a `tz`.
func TestTimeZone_DisableDefaultsSameErrorBothSides(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs, DisableDefaults: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cases := map[string]func(tz string) *types.Request{
		"group": func(tz string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(),
				Groups: []*types.Group{{Field: "ts", TimeZone: tz}}}
		},
		"filter": func(tz string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: countAgg(),
				Filterers: []*types.Filterer{{Field: "ts", Params: dateRanges, TimeZone: tz}}}
		},
	}
	for name, mk := range cases {
		for _, tz := range []string{"", "UTC", "Europe/Berlin"} {
			t.Run(name+"/"+tz, func(t *testing.T) {
				_, rerr := p.Process(ctx, mk(tz))
				ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
				if !strings.HasPrefix(ce.Message, "unknown ") || strings.Contains(ce.Message, "tz") {
					t.Fatalf("runtime message %q is not the type-validation error", ce.Message)
				}
				env := predictEnvelope(t, p, fs, cohort, mk(tz))
				if len(env.Errors) == 0 {
					t.Fatal("predict accepted a request the runtime refuses")
				}
				if env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
					t.Fatalf("predict %s %q, runtime %s %q", env.Errors[0].Code, env.Errors[0].Message, ce.Code, ce.Message)
				}
			})
		}
	}
	// Defaults ON: predict and runtime still agree (the default fills
	// the Type, so a UTC tz on the datetime slot is accepted by both).
	on := zonePulse(t, fs, "")
	req := cases["group"]("UTC")
	if _, err := on.Process(ctx, req); err != nil {
		t.Fatalf("defaults on, runtime: %v", err)
	}
	if env := predictEnvelope(t, on, fs, cohort, cases["group"]("UTC")); len(env.Errors) != 0 {
		t.Fatalf("defaults on, predict errors: %+v", env.Errors)
	}
}

// TestTimeZone_ValidatorsMatchRuntime: ValidateCompose, ValidateChain
// and ValidateFacet run the runtime's zone resolution, so each rejects
// a non-UTC zone on a derived (absent-from-schema) field with the code,
// message and details (location included) the runtime returns, and
// accepts one on a datetime field as the runtime does.
func TestTimeZone_ValidatorsMatchRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	const tokyo = "Asia/Tokyo"
	opts := &descx.PredictOptions{SchemaLoader: schemaLoaderFor(fs)}
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("compose", func(t *testing.T) {
		mk := func() *types.ComposedRequest {
			return &types.ComposedRequest{Requests: []*types.Request{
				groupDateOverTS(cohort, "", "UTC"),
				derivedGroupDate(cohort, tokyo, ""),
			}}
		}
		_, rerr := p.Compose(ctx, mk())
		sameEntry(t, descx.ValidateComposeWithOptions(mk(), opts), rerr)
		// Non-UTC zone over the datetime field: both accept.
		dt := &types.ComposedRequest{Requests: []*types.Request{groupDateOverTS(cohort, tokyo, "")}}
		if _, err := p.Compose(ctx, dt); err != nil {
			t.Fatalf("runtime: %v", err)
		}
		if env := descx.ValidateComposeWithOptions(dt, opts); len(env.Errors) != 0 {
			t.Fatalf("validator refused what runtime accepts: %+v", env.Errors)
		}
		// Inherited zone over a `date` field: both accept.
		ok := &types.ComposedRequest{Requests: []*types.Request{{
			Cohort: &types.Cohort{Filename: cohort}, TimeZone: tokyo, Aggregations: countAgg(),
			Groups: []*types.Group{{Type: types.GROUP_DATE, Field: "d"}},
		}}}
		if _, err := p.Compose(ctx, ok); err != nil {
			t.Fatalf("runtime: %v", err)
		}
		if env := descx.ValidateComposeWithOptions(ok, opts); len(env.Errors) != 0 {
			t.Fatalf("validator refused what runtime accepts: %+v", env.Errors)
		}
	})
	t.Run("chain", func(t *testing.T) {
		mk := func() *types.ChainRequest {
			return &types.ChainRequest{
				Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{{Request: &types.Request{
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
					Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "derived_ts", Params: dateRanges, TimeZone: tokyo}},
				}}},
			}
		}
		_, rerr := p.ProcessChain(ctx, mk())
		sameEntry(t, descx.ValidateChainWithOptions(bytes.NewReader(data), mk(), opts), rerr)
	})
	t.Run("facet", func(t *testing.T) {
		mk := func() *types.FacetRequest {
			return &types.FacetRequest{
				Cohort: &types.Cohort{Filename: cohort}, Fields: []string{"cat"}, TimeZone: tokyo,
				Filterers: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "derived_ts", Params: dateRanges}},
			}
		}
		_, rerr := p.FacetSchema(ctx, mk())
		sameEntry(t, descx.ValidateFacetWithOptions(bytes.NewReader(data), mk(), opts), rerr)
	})
}

// TestTimeZone_RefusalCarriesLocation: a zone refusal inside a
// multi-request root names where it happened — details.request (0-based
// Compose slot, serial and FailFast parallel alike) and details.stage
// (0-based chain stage, stage 0 included) — beside {slot, operator, tz},
// with the code still reachable through errors.As.
func TestTimeZone_RefusalCarriesLocation(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	const berlin = "Europe/Berlin"
	composed := func() *types.ComposedRequest {
		return &types.ComposedRequest{Requests: []*types.Request{
			groupDateOverTS(cohort, "", ""),
			derivedGroupDate(cohort, berlin, ""),
		}}
	}
	check := func(t *testing.T, err error, slot, op, key string, idx int) {
		t.Helper()
		assertRefusal(t, err, slot, op, berlin)
		ce := requireCode(t, err, errors.PROCESSING_CONFIG)
		if !reflect.DeepEqual(ce.Details[key], idx) {
			t.Fatalf("details[%q] = %v (%T), want %d; details = %v", key, ce.Details[key], ce.Details[key], idx, ce.Details)
		}
	}
	t.Run("compose", func(t *testing.T) {
		_, err := p.Compose(ctx, composed())
		check(t, err, "groups[0]", "GROUP_DATE", "request", 1)
	})
	t.Run("compose-parallel", func(t *testing.T) {
		_, err := p.ComposeParallel(ctx, composed(), pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
		check(t, err, "groups[0]", "GROUP_DATE", "request", 1)
	})
	stage := func(tz string) *types.Request {
		// A zoned stage reads a derived (absent) field, the refusal
		// under test; a zone-free one reads the datetime field and runs.
		field := "derived_ts"
		if tz == "" {
			field = "ts"
		}
		return &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Filterers:    []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: field, Params: dateRanges, TimeZone: tz}},
		}
	}
	t.Run("chain stage 0", func(t *testing.T) {
		_, err := p.ProcessChain(ctx, &types.ChainRequest{
			Cohort: &types.Cohort{Filename: cohort},
			Stages: []*types.ChainStage{{Request: stage(berlin)}},
		})
		check(t, err, "filterers[0]", "FILTER_DATE_RANGES", "stage", 0)
	})
	t.Run("chain stage 0 zone before gate", func(t *testing.T) {
		// GROUP_DATE is not chain-mergeable; the zone refusal still wins,
		// as it does on every later stage.
		_, err := p.ProcessChain(ctx, &types.ChainRequest{
			Cohort: &types.Cohort{Filename: cohort},
			Stages: []*types.ChainStage{{Request: derivedGroupDate("", berlin, "")}},
		})
		check(t, err, "groups[0]", "GROUP_DATE", "stage", 0)
	})
	t.Run("chain stage 1", func(t *testing.T) {
		_, err := p.ProcessChain(ctx, &types.ChainRequest{
			Cohort: &types.Cohort{Filename: cohort},
			Stages: []*types.ChainStage{
				{Request: stage("")},
				{Request: &types.Request{
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat", TimeZone: "UTC"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}},
				}},
			},
		})
		assertRefusal(t, err, "groups[0]", "GROUP_CATEGORY", "UTC")
		ce := requireCode(t, err, errors.PROCESSING_CONFIG)
		if ce.Details["stage"] != 1 {
			t.Fatalf("details = %v, want stage 1", ce.Details)
		}
	})
	t.Run("non-zone errors untouched", func(t *testing.T) {
		bad := composed()
		bad.Requests[1] = &types.Request{Cohort: &types.Cohort{Filename: "missing.pulse"}, Aggregations: countAgg()}
		_, err := p.Compose(ctx, bad)
		if err == nil {
			t.Fatal("want an error")
		}
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			if _, ok := ce.Details["request"]; ok {
				t.Fatalf("a non-zone error gained a request location: %v", ce.Details)
			}
		}
	})
}
