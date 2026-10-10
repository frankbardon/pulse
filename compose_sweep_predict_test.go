package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// composeVerdict is one surface's answer on a Compose batch: valid, or
// the refusal's code and (for a limit refusal) its observed count.
type composeVerdict struct {
	code     string
	observed any
}

func runtimeVerdict(err error) composeVerdict {
	if err == nil {
		return composeVerdict{}
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		return composeVerdict{code: "uncoded: " + err.Error()}
	}
	return composeVerdict{code: string(ce.Code), observed: ce.Details["observed"]}
}

func predictVerdict(t *testing.T, env *descriptor.Envelope) composeVerdict {
	t.Helper()
	res := env.Data.(*ComposePredictResult)
	if res.Valid != (len(env.Errors) == 0) {
		t.Fatalf("Valid=%v with %d errors", res.Valid, len(env.Errors))
	}
	if len(env.Errors) == 0 {
		return composeVerdict{}
	}
	var observed any
	if v, ok := env.Errors[0].Details["observed"]; ok {
		observed = v
	}
	return composeVerdict{code: env.Errors[0].Code, observed: observed}
}

// sweepOfOps is a batch of `explicit` hand-written slots plus a sweep
// over the given aggregator names.
func sweepOfOps(t *testing.T, explicit int, ops ...string) *ComposedRequest {
	t.Helper()
	slots := make([]string, explicit)
	for i := range slots {
		slots[i] = sweepSlotBodyWith("AGG_COUNT")
	}
	raw := `{"requests":[` + strings.Join(slots, ",") + `]`
	if len(ops) > 0 {
		vals, _ := json.Marshal(ops)
		raw += `,"sweep":{"axes":[{"name":"op","values":` + string(vals) + `}],"request":` + sweepSlotBody + `}`
	}
	return decodeComposedJSON(t, raw+`}`)
}

// TestPredictCompose_SlotLimitParity (FR-14): the same batches — under,
// at and over a custom MaxComposeSlots, with and without a sweep, and a
// sweep-only grid over the built-in default — get the same verdict,
// code and observed count from PredictCompose as from Compose and
// ComposeParallel. Predict reads the limit off the instance.
func TestPredictCompose_SlotLimitParity(t *testing.T) {
	ctx := context.Background()
	custom := sweepPulse(t, Options{Limits: Limits{MaxComposeSlots: 4}})
	defaults := sweepPulse(t, Options{})
	// 33 x 33 = 1089 sweep slots: over the default 1000.
	vals := make([]int, 33)
	for i := range vals {
		vals[i] = i
	}
	vb, _ := json.Marshal(vals)
	bigGrid := decodeComposedJSON(t, `{"sweep":{"axes":[{"name":"a","values":`+string(vb)+`},{"name":"b","values":`+string(vb)+`}],
		"request":{"cohort":{"filename":"`+parityCohort+`"},"aggregations":[{"type":"AGG_SUM","field":"age","label":"v{{a}}_{{b}}"}]}}}`)

	cases := []struct {
		name string
		p    *Pulse
		req  *ComposedRequest
		want composeVerdict
	}{
		{"sweep under", custom, sweepOfOps(t, 1, "AGG_SUM", "AGG_MAX"), composeVerdict{}},
		{"sweep at", custom, sweepOfOps(t, 1, "AGG_SUM", "AGG_MAX", "AGG_MIN"), composeVerdict{}},
		{"sweep over", custom, sweepOfOps(t, 1, "AGG_SUM", "AGG_MAX", "AGG_MIN", "AGG_AVERAGE"), composeVerdict{string(perr.PULSE_LIMIT_EXCEEDED), int64(5)}},
		{"sweep-only over", custom, sweepOfOps(t, 0, "AGG_SUM", "AGG_MAX", "AGG_MIN", "AGG_AVERAGE", "AGG_COUNT"), composeVerdict{string(perr.PULSE_LIMIT_EXCEEDED), int64(5)}},
		{"sweep-free at", custom, sweepOfOps(t, 4), composeVerdict{}},
		{"sweep-free over", custom, sweepOfOps(t, 5), composeVerdict{string(perr.PULSE_LIMIT_EXCEEDED), int64(5)}},
		{"default limit over", defaults, bigGrid, composeVerdict{string(perr.PULSE_LIMIT_EXCEEDED), int64(1089)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := tc.p.PredictCompose(ctx, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if got := predictVerdict(t, env); got != tc.want {
				t.Fatalf("predict %+v, want %+v (errors %+v)", got, tc.want, env.Errors)
			}
			_, err = tc.p.Compose(ctx, tc.req)
			if got := runtimeVerdict(err); got != tc.want {
				t.Fatalf("Compose %+v, want %+v", got, tc.want)
			}
			_, err = tc.p.ComposeParallel(ctx, tc.req, ComposeOptions{})
			if got := runtimeVerdict(err); got != tc.want {
				t.Fatalf("ComposeParallel %+v, want %+v", got, tc.want)
			}
		})
	}

	// The predict refusal is the runtime's error: code, message, details.
	over := sweepOfOps(t, 1, "AGG_SUM", "AGG_MAX", "AGG_MIN", "AGG_AVERAGE")
	env, err := custom.PredictCompose(ctx, over)
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := custom.Compose(ctx, over)
	ce := requireCoded(t, rerr, perr.PULSE_LIMIT_EXCEEDED)
	if env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
		t.Fatalf("predict %+v differs from runtime %s %v", env.Errors[0], ce.Message, ce.Details)
	}
}

// TestPredictCompose_SweepSummary (FR-13): predict reports the sweep's
// axes, resolved mode, expanded slot count and labels; an over-limit
// sweep still reports its size (no labels); a sweep-free batch carries
// no `sweep` key.
func TestPredictCompose_SweepSummary(t *testing.T) {
	ctx := context.Background()
	p := sweepPulse(t, Options{Limits: Limits{MaxComposeSlots: 4}})

	grid := decodeComposedJSON(t, `{"requests":[`+sweepSlotBodyWith("AGG_COUNT")+`],
		"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX"]},{"name":"f","values":["age"]}],
		"label":"{{op}}_{{f}}","request":{"cohort":{"filename":"`+parityCohort+`"},"aggregations":[{"type":"{{op}}","field":"{{f}}","label":"v"}]}}}`)
	env, err := p.PredictCompose(ctx, grid)
	if err != nil {
		t.Fatal(err)
	}
	res := env.Data.(*ComposePredictResult)
	want := &descriptor.SweepSummary{
		Axes:          []descriptor.SweepAxisSummary{{Name: "op", Count: 2}, {Name: "f", Count: 1}},
		Mode:          types.SweepModeGrid,
		ExpandedCount: 2,
		Labels:        []string{"AGG_SUM_age", "AGG_MAX_age"},
	}
	if !res.Valid || !reflect.DeepEqual(res.Sweep, want) {
		t.Fatalf("summary %+v (valid %v, errors %+v), want %+v", res.Sweep, res.Valid, env.Errors, want)
	}
	if res.Request != grid {
		t.Fatal("result.Request is not the request as written")
	}

	zip := decodeComposedJSON(t, `{"sweep":{"mode":"zip","axes":[{"name":"op","values":["AGG_SUM","AGG_MAX","AGG_MIN"]},{"name":"n","values":[1,2,3]}],
		"label":"z{{n}}","request":{"cohort":{"filename":"`+parityCohort+`"},"aggregations":[{"type":"{{op}}","field":"age","label":"v"}]}}}`)
	env, _ = p.PredictCompose(ctx, zip)
	s := env.Data.(*ComposePredictResult).Sweep
	if s == nil || s.Mode != types.SweepModeZip || s.ExpandedCount != 3 || !reflect.DeepEqual(s.Labels, []string{"z1", "z2", "z3"}) {
		t.Fatalf("zip summary %+v", s)
	}

	over := sweepOfOps(t, 1, "AGG_SUM", "AGG_MAX", "AGG_MIN", "AGG_AVERAGE")
	env, _ = p.PredictCompose(ctx, over)
	s = env.Data.(*ComposePredictResult).Sweep
	if s == nil || s.ExpandedCount != 4 || s.Labels != nil {
		t.Fatalf("over-limit summary %+v, want expanded_count 4 and no labels", s)
	}

	// A structurally invalid sweep has no summary: its fault is the answer.
	bad := decodeComposedJSON(t, `{"sweep":{"axes":[],"request":{}}}`)
	env, _ = p.PredictCompose(ctx, bad)
	if env.Data.(*ComposePredictResult).Sweep != nil || len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_SWEEP_INVALID) {
		t.Fatalf("invalid sweep: summary %+v, errors %+v", env.Data.(*ComposePredictResult).Sweep, env.Errors)
	}

	env, _ = p.PredictCompose(ctx, sweepOfOps(t, 2))
	b, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"sweep"`) {
		t.Fatalf("sweep-free predict carries a sweep key: %s", b)
	}
}

// TestPredictCompose_SweepSlotsPredicted: every expanded slot is
// predicted like a hand-written one — a field fault in the second sweep
// slot is located at its effective index, with the runtime's code.
func TestPredictCompose_SweepSlotsPredicted(t *testing.T) {
	ctx := context.Background()
	p := sweepPulse(t, Options{})
	c := decodeComposedJSON(t, `{"requests":[`+sweepSlotBodyWith("AGG_COUNT")+`],
		"sweep":{"axes":[{"name":"f","values":["age","no_such_field"]}],
		"request":{"cohort":{"filename":"`+parityCohort+`"},"aggregations":[{"type":"AGG_SUM","field":"{{f}}","label":"v"}]}}}`)
	env, err := p.PredictCompose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 1 || env.Errors[0].Details["request"] != 2 {
		t.Fatalf("predict errors %+v, want one located at request 2", env.Errors)
	}
	_, rerr := p.Compose(ctx, c)
	if got := runtimeVerdict(rerr); got.code != env.Errors[0].Code {
		t.Fatalf("runtime %+v, predict %s", got, env.Errors[0].Code)
	}
}

// TestPredictCompose_LabelCollisionSweepFree: a sweep-free batch with
// no overlays whose slots share a label is refused by predict, as the
// runtime refuses it — explicit duplicates and an explicit
// `request_<n>` against another slot's default alike.
func TestPredictCompose_LabelCollisionSweepFree(t *testing.T) {
	ctx := context.Background()
	p := sweepPulse(t, Options{})
	for name, labels := range map[string][2]string{
		"duplicate":        {"a", "a"},
		"explicit default": {"", "request_1"},
	} {
		t.Run(name, func(t *testing.T) {
			c := &ComposedRequest{Requests: []*Request{
				groupedSlot(labels[0], types.AGG_SUM),
				groupedSlot(labels[1], types.AGG_MAX),
			}}
			_, err := p.Compose(ctx, c)
			requireCoded(t, err, perr.PULSE_COMPOSE_LABEL_COLLISION)
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if v := predictVerdict(t, env); v.code != string(perr.PULSE_COMPOSE_LABEL_COLLISION) {
				t.Fatalf("predict %+v, want PULSE_COMPOSE_LABEL_COLLISION", env.Errors)
			}
		})
	}
}

// TestPredictCompose_TouchesSweepSlotCohorts: predict slides the
// managed-import TTL of a cohort only a sweep slot names, as it does for
// an explicit slot's.
func TestPredictCompose_TouchesSweepSlotCohorts(t *testing.T) {
	ctx := context.Background()
	past := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	p, fsys := memBuilderEngine(t)
	if err := afero.WriteFile(fsys, "data.csv", []byte("id,name\n1,a\n2,b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	imp, err := p.ImportFile(ctx, ImportSpec{SourcePath: "data.csv", TTL: time.Hour})
	if err != nil || !imp.Managed {
		t.Fatalf("ImportFile = %+v, %v", imp, err)
	}
	ageSidecar(t, fsys, imp.Path, past)
	c := decodeComposedJSON(t, `{"sweep":{"axes":[{"name":"op","values":["AGG_SUM","AGG_MAX"]}],
		"request":{"cohort":{"filename":"`+imp.Path+`"},"aggregations":[{"type":"{{op}}","field":"id","label":"v"}]}}}`)
	env, err := p.PredictCompose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("predict errors %+v", env.Errors)
	}
	if got := managedExpiry(t, fsys, imp.Path); !got.After(past) {
		t.Fatalf("expiry %v did not slide past %v", got, past)
	}
}
