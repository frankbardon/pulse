package pulse_test

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// leakageBody target-encodes the categorical g on the numeric x.
const leakageBody = `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"x"}}],` +
	`"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`

func leakageWarnings(ws []*types.ResponseWarning) []*types.ResponseWarning {
	var out []*types.ResponseWarning
	for _, w := range ws {
		if w.Code == string(perr.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
			out = append(out, w)
		}
	}
	return out
}

func leakageEntries(es []*descriptor.EnvelopeEntry) []*descriptor.EnvelopeEntry {
	var out []*descriptor.EnvelopeEntry
	for _, e := range es {
		if e.Code == string(perr.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
			out = append(out, e)
		}
	}
	return out
}

// TestTargetLeakage_ProcessRepeatsPredict pins #309: Process repeats
// predict's PULSE_FEAT_TARGET_LEAKAGE_RISK on Response.Warnings — same
// code, message and details — and under Options.Strict refuses the run
// with it, the error predict reports.
func TestTargetLeakage_ProcessRepeatsPredict(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()

	resp, err := p.Process(ctx, categoricalRequest(t, leakageBody, cohort))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	got := leakageWarnings(resp.Warnings)
	env := predictEnvelope(t, p, fs, cohort, categoricalRequest(t, leakageBody, cohort))
	want := leakageEntries(env.Warnings)
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("runtime %d, predict %d leakage warnings; want one each", len(got), len(want))
	}
	if got[0].Message != want[0].Message || !reflect.DeepEqual(got[0].Details, want[0].Details) {
		t.Errorf("runtime %q %v, predict %q %v", got[0].Message, got[0].Details, want[0].Message, want[0].Details)
	}

	strict, err := pulse.New(pulse.Options{FS: fs, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := strict.Process(ctx, categoricalRequest(t, leakageBody, cohort))
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) || ce.Code != perr.PULSE_FEAT_TARGET_LEAKAGE_RISK {
		t.Fatalf("strict runtime: %v, want %s", rerr, perr.PULSE_FEAT_TARGET_LEAKAGE_RISK)
	}
	senv := predictEnvelope(t, strict, fs, cohort, categoricalRequest(t, leakageBody, cohort))
	// (The cohort's undescribed fields add description warnings, which
	// strict predict also promotes; the leakage entry is the one here.)
	if got := leakageEntries(senv.Errors); len(got) != 1 || len(leakageEntries(senv.Warnings)) != 0 {
		t.Errorf("strict predict leakage errors %d, runtime %s", len(got), ce.Code)
	}
}

// TestTargetLeakage_ComposeMatchesPredict: a Compose slot carries the
// warning on its response and predict-compose reports it located
// details.request; under Strict both refuse with it, located the same
// way. (A chain stage carries no features: PULSE_CHAIN_NOT_MERGEABLE.)
func TestTargetLeakage_ComposeMatchesPredict(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	compose := func() *pulse.ComposedRequest {
		return &pulse.ComposedRequest{Requests: []*pulse.Request{
			categoricalRequest(t, `{"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`, cohort),
			categoricalRequest(t, leakageBody, cohort),
		}}
	}
	out, err := p.Compose(ctx, compose())
	if err != nil {
		t.Fatal(err)
	}
	if n0, n1 := len(leakageWarnings(out.Responses[0].Warnings)), len(leakageWarnings(out.Responses[1].Warnings)); n0 != 0 || n1 != 1 {
		t.Errorf("slot responses carry %d / %d leakage warnings, want 0 / 1", n0, n1)
	}
	env, err := p.PredictCompose(ctx, compose())
	if err != nil {
		t.Fatal(err)
	}
	if w := leakageEntries(env.Warnings); len(w) != 1 || w[0].Details["request"] != 1 {
		t.Errorf("predict-compose leakage warnings %v, want one at request 1", w)
	}

	strict, err := pulse.New(pulse.Options{FS: fs, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := strict.Compose(ctx, compose())
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) || ce.Code != perr.PULSE_FEAT_TARGET_LEAKAGE_RISK {
		t.Fatalf("strict runtime: %v, want %s", rerr, perr.PULSE_FEAT_TARGET_LEAKAGE_RISK)
	}
	senv, err := strict.PredictCompose(ctx, compose())
	if err != nil {
		t.Fatal(err)
	}
	got := leakageEntries(senv.Errors)
	if len(got) != 1 || len(leakageEntries(senv.Warnings)) != 0 {
		t.Fatalf("strict predict leakage errors %v, warnings %v; want exactly one error", got, entryCodes(senv.Warnings))
	}
	if got[0].Details["request"] != 1 || ce.Details["request"] != 1 {
		t.Errorf("location: predict %v, runtime %v; want request = 1", got[0].Details, ce.Details)
	}
}
